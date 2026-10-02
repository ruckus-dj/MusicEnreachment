package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

var (
	// ErrSourceRootActiveAnalysis reports an analysis start refused because
	// another analysis of the same root is queued or running. The root
	// exclusivity rule accepts one root-targeting operation at a time, whether
	// it is a scan or an analysis.
	ErrSourceRootActiveAnalysis = errors.New("source root has an active analysis")
	// ErrToolsRootMoveActive reports an analysis start refused because a managed
	// tools root move is queued or running. A move rewrites the tools directory
	// an analysis would read its pinned executable from, so the two exclude
	// each other in both directions.
	ErrToolsRootMoveActive = errors.New("a managed tools root move is active")
	// ErrToolsInstallationHeldByAnalysis reports a tools root move refused
	// because an active analysis holds one of the managed installations it would
	// relocate. The hold is a read hold, so it may be held by several roots at
	// once and never consumes the installation mutation target.
	ErrToolsInstallationHeldByAnalysis = errors.New("a managed installation is held by an active source analysis")
	// ErrSourceAnalysisInstallationChanged reports an analysis start refused
	// because the active managed FFmpeg installation changed between the
	// operator's read and the transaction that records the snapshot. The start
	// linearizes on the active installation it read under the activation lock:
	// an activation that finished first is observed and the start is refused
	// rather than queued against a selection the operator no longer sees.
	ErrSourceAnalysisInstallationChanged = errors.New("the active managed ffmpeg installation changed while the analysis was starting")
	// ErrSourceAnalysisInstallationUnusable reports an analysis start refused
	// because the installation the active setting names is absent, unparsable,
	// not a ready FFmpeg package, or built for another platform.
	ErrSourceAnalysisInstallationUnusable = errors.New("the selected managed ffmpeg installation is not usable for this instance")
)

// sourceAnalysisActiveInstallationLock is the advisory lock activation,
// installation deletion and setup completion take for the ffmpeg package.
// Taking it here makes the active-setting read, the platform check and the
// installation readiness check atomic against a concurrent activation, so an
// activation either lands entirely before this transaction (and is observed and
// fenced) or entirely after it (and cannot touch the queued snapshot, whose
// installation is pinned by identity).
const sourceAnalysisActiveInstallationLock = "active-installation:ffmpeg"

// SourceAnalysisStartStore combines the source inventory and setup manager
// repositories behind the read and enqueue contract an analysis start needs,
// without making either repository depend on the other.
type SourceAnalysisStartStore struct {
	*SourceInventoryRepository
	*SetupManagerRepository
}

func NewSourceAnalysisStartStore(database *bun.DB) *SourceAnalysisStartStore {
	return &SourceAnalysisStartStore{
		SourceInventoryRepository: NewSourceInventoryRepository(database),
		SetupManagerRepository:    NewSetupManagerRepository(database),
	}
}

// CreateSourceAnalysisOperationAndEnqueue records a queued analysis of one
// source location, its River job and both of its read holds in one transaction:
// neither record is visible if either insert fails, so no analysis is ever left
// without the job that would run it and no job is ever left without its
// operation.
//
// The transaction takes the operation table lock before it locks the source
// root, which is the order scan start, root edit, root deletion, installation
// deletion and tools move use. Holding that lock, the method decodes the
// immutable snapshot from the operation and fences everything against it: the
// root must still be enabled and non-stale with the same inventory path, the
// location must still be the audio file of the snapshot with the previous
// variant the snapshot holds, and the pinned installation must still exist and
// be a ready FFmpeg package of this instance platform. It then refuses an active
// tools root move and an active scan or analysis of the same root, so exactly
// one root-targeting operation exists at a time while other roots stay
// independent.
//
// The snapshot installation is fenced against the current active FFmpeg setting
// under the same advisory lock activation takes, so the selection is current at
// the transaction's linearization point, not merely the value a caller read
// before it. An activation that committed first is observed here and refuses the
// start with ErrSourceAnalysisInstallationChanged; an activation that commits
// after this transaction is irrelevant because the queued snapshot pins the
// installation by identity. Only then does the method insert the River job and
// the operation with its analysis_installation_id and analysis_media_variant_id
// read holds; target_installation_id stays NULL because the installation is a
// read hold, never a mutation target.
func (repository *SourceInventoryRepository) CreateSourceAnalysisOperationAndEnqueue(ctx context.Context, operation *Operation, platformGOOS, platformGOARCH, activeSetting string, client RiverInserter, args river.JobArgs, options *river.InsertOpts) error {
	if client == nil {
		return fmt.Errorf("enqueue source analysis: River client is required")
	}
	if operation == nil || operation.Kind != analysisSourceOperationKind ||
		operation.TargetSourceRootID == nil || operation.TargetSourceLocationID == nil ||
		operation.AnalysisInstallationID == nil {
		return fmt.Errorf("enqueue source analysis: operation must be an analysis of a source location with a selected installation")
	}
	if activeSetting == "" {
		return fmt.Errorf("enqueue source analysis: the active ffmpeg setting is required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return fmt.Errorf("enqueue source analysis: lock operations: %w", err)
		}
		snapshot, err := DecodeSourceAnalysisSnapshot(operation.InputSnapshot)
		if err != nil {
			return fmt.Errorf("enqueue source analysis: %w", err)
		}
		if *operation.TargetSourceRootID != snapshot.SourceRootID || *operation.TargetSourceLocationID != snapshot.SourceLocationID ||
			*operation.AnalysisInstallationID != snapshot.AnalysisInstallationID ||
			!sameOptionalUUID(operation.AnalysisMediaVariantID, snapshot.PreviousVariantID) {
			return fmt.Errorf("enqueue source analysis: the operation does not match its snapshot")
		}
		// Read the active setting and validate the installation under the
		// activation lock, before touching the file rows, so a concurrent
		// activation cannot interleave between the read and the write.
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", sourceAnalysisActiveInstallationLock); err != nil {
			return fmt.Errorf("enqueue source analysis: lock the active installation: %w", err)
		}
		active, err := activeFFmpegInstallation(ctx, tx, activeSetting)
		if err != nil {
			return fmt.Errorf("enqueue source analysis: %w: %w", ErrSourceAnalysisInstallationUnusable, err)
		}
		if active != snapshot.AnalysisInstallationID {
			return fmt.Errorf("enqueue source analysis: %w", ErrSourceAnalysisInstallationChanged)
		}
		root := new(SourceRoot)
		if err := tx.NewRaw("SELECT * FROM source_root WHERE id = ? FOR UPDATE", snapshot.SourceRootID).Scan(ctx, root); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("enqueue source analysis: %w", ErrSourceAnalysisStale)
			}
			return fmt.Errorf("enqueue source analysis: lock source root: %w", err)
		}
		if !root.Enabled || root.Stale() || root.ConfiguredPath != snapshot.ConfiguredPath ||
			root.InventoryPath == nil || *root.InventoryPath != snapshot.InventoryPath {
			return fmt.Errorf("enqueue source analysis: %w", ErrSourceAnalysisStale)
		}
		location := new(SourceLocation)
		if err := tx.NewRaw("SELECT * FROM source_location WHERE id = ? FOR UPDATE", snapshot.SourceLocationID).Scan(ctx, location); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("enqueue source analysis: %w", ErrSourceAnalysisStale)
			}
			return fmt.Errorf("enqueue source analysis: lock source location: %w", err)
		}
		if location.SourceRootID != snapshot.SourceRootID || location.RelativePath != snapshot.RelativePath ||
			location.SizeBytes != snapshot.SizeBytes || !sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(snapshot.Mtime)) ||
			location.ProbeStatus != SourceProbeStatusAudio || !sameOptionalUUID(location.MediaVariantID, snapshot.PreviousVariantID) {
			return fmt.Errorf("enqueue source analysis: %w", ErrSourceAnalysisStale)
		}
		installation := new(ToolInstallation)
		if err := tx.NewRaw("SELECT * FROM tool_installation WHERE id = ? FOR UPDATE", snapshot.AnalysisInstallationID).Scan(ctx, installation); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("enqueue source analysis: %w: %w", ErrSourceAnalysisInstallationUnusable, ErrSourceAnalysisStale)
			}
			return fmt.Errorf("enqueue source analysis: lock the selected installation: %w", err)
		}
		if installation.PackageKind != "ffmpeg" || installation.State != "ready" ||
			installation.PlatformGOOS != platformGOOS || installation.PlatformGOARCH != platformGOARCH {
			return fmt.Errorf("enqueue source analysis: %w", ErrSourceAnalysisInstallationUnusable)
		}
		activeMove, err := activeToolsRootMove(ctx, tx)
		if err != nil {
			return fmt.Errorf("enqueue source analysis: check the active tools move: %w", err)
		}
		if activeMove {
			return fmt.Errorf("enqueue source analysis: %w", ErrToolsRootMoveActive)
		}
		activeKind, err := activeSourceRootOperationKind(ctx, tx, snapshot.SourceRootID)
		if err != nil {
			return fmt.Errorf("enqueue source analysis: check the active root operation: %w", err)
		}
		switch activeKind {
		case "":
		case analysisSourceOperationKind:
			return fmt.Errorf("enqueue source analysis: %w", ErrSourceRootActiveAnalysis)
		default:
			return fmt.Errorf("enqueue source analysis: %w", ErrSourceRootActiveScan)
		}
		// The job is inserted first so the operation row carries the id River
		// assigned to it; either insert failing rolls back both rows.
		result, err := client.InsertTx(ctx, tx.Tx, args, options)
		if err != nil {
			return fmt.Errorf("enqueue source analysis: insert River job: %w", err)
		}
		operation.RiverJobID = &result.Job.ID
		if operation.Attempt == 0 {
			operation.Attempt = 1
		}
		if _, err := tx.NewInsert().Model(operation).Exec(ctx); err != nil {
			return fmt.Errorf("enqueue source analysis: create operation: %w", err)
		}
		return nil
	})
}

// activeFFmpegInstallation reads the installation id the active FFmpeg setting
// names. Callers hold sourceAnalysisActiveInstallationLock, so the value cannot
// change between this read and the write it guards.
func activeFFmpegInstallation(ctx context.Context, database bun.IDB, activeSetting string) (uuid.UUID, error) {
	var value string
	if err := database.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", activeSetting).Scan(ctx, &value); err != nil {
		if err == sql.ErrNoRows {
			return uuid.Nil, fmt.Errorf("no active ffmpeg installation is configured")
		}
		return uuid.Nil, err
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, fmt.Errorf("the active ffmpeg setting does not name an installation")
	}
	return id, nil
}

// activeToolsRootMove reports whether a tools root move is still queued or
// running. Callers hold LOCK TABLE operation so the answer cannot change under
// them before they write.
func activeToolsRootMove(ctx context.Context, database bun.IDB) (bool, error) {
	var moveID uuid.UUID
	err := database.NewRaw("SELECT id FROM operation WHERE kind = 'move_tools_root' AND state IN ('queued', 'running') LIMIT 1 FOR UPDATE").Scan(ctx, &moveID)
	if err == nil {
		return true, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	return false, nil
}

// activeSourceRootOperationKind names the kind of the queued or running
// operation that currently targets the root, or the empty string when there is
// none. The root exclusivity index accepts one scan or analysis at a time.
func activeSourceRootOperationKind(ctx context.Context, database bun.IDB, rootID uuid.UUID) (string, error) {
	var kind string
	err := database.NewRaw("SELECT kind FROM operation WHERE target_source_root_id = ? AND state IN ('queued', 'running') LIMIT 1 FOR UPDATE", rootID).Scan(ctx, &kind)
	if err == nil {
		return kind, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	return "", nil
}
