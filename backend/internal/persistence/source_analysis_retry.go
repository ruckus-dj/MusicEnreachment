package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

// RetrySourceAnalysisOperationAndEnqueue re-runs one failed analysis under the
// same operation id and the very same immutable input snapshot: no precondition
// is re-read into the snapshot, and the snapshot is never rewritten. The queued
// transition, the restored read holds and the new River job are one transaction,
// so a refusal leaves the operation failed with its attempt, its snapshot and
// its (empty) holds exactly as they were, and inserts no job.
//
// The transaction takes the operation table lock before it locks the root, the
// location, the held variant and the installation, the order an analysis start,
// a root deletion and a tools move use. Holding that lock, the method repeats
// the start's fences against the immutable snapshot: the root must still be
// enabled and non-stale with the same inventory path, the location must still be
// the audio file of the snapshot with the snapshot's previous variant current
// (including a NULL that must match a NULL), a non-NULL previous variant must
// still exist, and the pinned installation must still exist as a ready FFmpeg
// package. The current active FFmpeg setting is deliberately not consulted:
// activation of another version does not change the pinned snapshot and must not
// block its retry. An active tools move and an active scan or analysis of the
// same root are refused, mirroring the start's exclusions.
func (repository *SetupManagerRepository) RetrySourceAnalysisOperationAndEnqueue(ctx context.Context, id uuid.UUID, client RiverInserter, args river.JobArgs, options *river.InsertOpts) (*Operation, error) {
	if client == nil {
		return nil, fmt.Errorf("retry source analysis: River client is required")
	}
	var operation *Operation
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return fmt.Errorf("retry source analysis: lock operations: %w", err)
		}
		locked, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if locked.Kind != analysisSourceOperationKind || locked.TargetSourceRootID == nil || locked.TargetSourceLocationID == nil {
			return fmt.Errorf("retry source analysis: operation is not an analysis of a source location")
		}
		if locked.State != "failed" {
			return fmt.Errorf("retry source analysis: only a failed source analysis can be retried")
		}
		snapshot, err := DecodeSourceAnalysisSnapshot(locked.InputSnapshot)
		if err != nil {
			return fmt.Errorf("retry source analysis: %w", err)
		}
		if *locked.TargetSourceRootID != snapshot.SourceRootID || *locked.TargetSourceLocationID != snapshot.SourceLocationID {
			return fmt.Errorf("retry source analysis: %w", ErrSourceAnalysisStale)
		}
		root := new(SourceRoot)
		if err := tx.NewRaw("SELECT * FROM source_root WHERE id = ? FOR UPDATE", snapshot.SourceRootID).Scan(ctx, root); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("retry source analysis: %w", ErrSourceAnalysisStale)
			}
			return fmt.Errorf("retry source analysis: lock source root: %w", err)
		}
		if !root.Enabled {
			return fmt.Errorf("retry source analysis: %w", ErrSourceRootDisabled)
		}
		if root.Stale() || root.ConfiguredPath != snapshot.ConfiguredPath ||
			root.InventoryPath == nil || *root.InventoryPath != snapshot.InventoryPath {
			return fmt.Errorf("retry source analysis: %w", ErrSourceAnalysisStale)
		}
		location := new(SourceLocation)
		if err := tx.NewRaw("SELECT * FROM source_location WHERE id = ? FOR UPDATE", snapshot.SourceLocationID).Scan(ctx, location); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("retry source analysis: %w", ErrSourceAnalysisStale)
			}
			return fmt.Errorf("retry source analysis: lock source location: %w", err)
		}
		if location.SourceRootID != snapshot.SourceRootID || location.RelativePath != snapshot.RelativePath ||
			location.SizeBytes != snapshot.SizeBytes || !sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(snapshot.Mtime)) ||
			location.ProbeStatus != SourceProbeStatusAudio ||
			!sameOptionalUUID(location.MediaVariantID, snapshot.PreviousVariantID) {
			return fmt.Errorf("retry source analysis: %w", ErrSourceAnalysisStale)
		}
		if snapshot.PreviousVariantID != nil {
			// The held variant is locked and proven to still exist, so a retry
			// never restores a hold on a row another writer removed.
			var held uuid.UUID
			if err := tx.NewRaw("SELECT id FROM media_variant WHERE id = ? FOR UPDATE", *snapshot.PreviousVariantID).Scan(ctx, &held); err != nil {
				if err == sql.ErrNoRows {
					return fmt.Errorf("retry source analysis: %w", ErrSourceAnalysisStale)
				}
				return fmt.Errorf("retry source analysis: lock the held variant: %w", err)
			}
		}
		installation := new(ToolInstallation)
		if err := tx.NewRaw("SELECT * FROM tool_installation WHERE id = ? FOR UPDATE", snapshot.AnalysisInstallationID).Scan(ctx, installation); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("retry source analysis: %w", ErrSourceAnalysisInstallationUnusable)
			}
			return fmt.Errorf("retry source analysis: lock the pinned installation: %w", err)
		}
		if installation.PackageKind != "ffmpeg" || installation.State != "ready" {
			return fmt.Errorf("retry source analysis: %w", ErrSourceAnalysisInstallationUnusable)
		}
		platformGOOS, platformGOARCH, err := instancePlatform(ctx, tx)
		if err != nil {
			return fmt.Errorf("retry source analysis: %w: %w", ErrSourceAnalysisInstallationUnusable, err)
		}
		if installation.PlatformGOOS != platformGOOS || installation.PlatformGOARCH != platformGOARCH {
			return fmt.Errorf("retry source analysis: %w", ErrSourceAnalysisInstallationUnusable)
		}
		activeMove, err := activeToolsRootMove(ctx, tx)
		if err != nil {
			return fmt.Errorf("retry source analysis: check the active tools move: %w", err)
		}
		if activeMove {
			return fmt.Errorf("retry source analysis: %w", ErrToolsRootMoveActive)
		}
		activeKind, err := activeSourceRootOperationKind(ctx, tx, snapshot.SourceRootID)
		if err != nil {
			return fmt.Errorf("retry source analysis: check the active root operation: %w", err)
		}
		switch activeKind {
		case "":
		case analysisSourceOperationKind:
			return fmt.Errorf("retry source analysis: %w", ErrSourceRootActiveAnalysis)
		default:
			return fmt.Errorf("retry source analysis: %w", ErrSourceRootActiveScan)
		}
		result, err := client.InsertTx(ctx, tx.Tx, args, options)
		if err != nil {
			return fmt.Errorf("retry source analysis: insert River job: %w", err)
		}
		locked.State = "queued"
		if !strings.HasPrefix(locked.Stage, "retry:") {
			locked.Stage = "retry:" + locked.Stage
		}
		locked.SafeError = nil
		locked.StartedAt = nil
		locked.FinishedAt = nil
		locked.BytesCompleted = 0
		locked.BytesTotal = nil
		locked.RiverJobID = &result.Job.ID
		locked.Attempt++
		// Both read holds are restored from the immutable snapshot, not from the
		// failed row, so a retry always re-pins exactly the installation and the
		// previous variant the original start recorded.
		locked.AnalysisInstallationID = &snapshot.AnalysisInstallationID
		locked.AnalysisMediaVariantID = snapshot.PreviousVariantID
		locked.UpdatedAt = time.Now().UTC()
		if _, err := tx.NewUpdate().Model(locked).
			Column("state", "stage", "bytes_completed", "bytes_total", "safe_error", "river_job_id", "attempt",
				"analysis_installation_id", "analysis_media_variant_id", "started_at", "finished_at", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("retry source analysis: %w", err)
		}
		operation = locked
		return nil
	})
	if err != nil {
		return nil, err
	}
	return operation, nil
}

// instancePlatform reads the persisted instance platform. The retry fences the
// pinned installation against it so an analysis can never be re-queued for an
// installation this instance cannot execute; an uninitialized instance has no
// platform to fence against and refuses the retry rather than guessing.
func instancePlatform(ctx context.Context, database bun.IDB) (string, string, error) {
	var goos string
	if err := database.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = 'instance.goos'").Scan(ctx, &goos); err != nil {
		return "", "", fmt.Errorf("read the instance platform goos: %w", err)
	}
	var goarch string
	if err := database.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = 'instance.goarch'").Scan(ctx, &goarch); err != nil {
		return "", "", fmt.Errorf("read the instance platform goarch: %w", err)
	}
	return goos, goarch, nil
}
