// Package persistence owns PostgreSQL access for setup-manager state.
package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/uptrace/bun"
)

type ToolInstallation struct {
	bun.BaseModel `bun:"table:tool_installation"`

	ID                 uuid.UUID       `bun:"id,pk,type:uuid"`
	PackageKind        string          `bun:"package_kind"`
	PlatformGOOS       string          `bun:"platform_goos"`
	PlatformGOARCH     string          `bun:"platform_goarch"`
	SourceName         string          `bun:"source_name"`
	ReleaseIdentity    string          `bun:"release_identity"`
	RelativePath       string          `bun:"relative_path"`
	State              string          `bun:"state"`
	ExecutableVersions json.RawMessage `bun:"executable_versions,type:jsonb"`
	ArtifactIdentities json.RawMessage `bun:"artifact_identities,type:jsonb"`
	CreatedAt          time.Time       `bun:"created_at,nullzero"`
	VerifiedAt         *time.Time      `bun:"verified_at,nullzero"`
	UpdatedAt          time.Time       `bun:"updated_at,nullzero"`
}

type Operation struct {
	bun.BaseModel `bun:"table:operation"`

	ID                   uuid.UUID       `bun:"id,pk,type:uuid"`
	Kind                 string          `bun:"kind"`
	State                string          `bun:"state"`
	Stage                string          `bun:"stage"`
	InputSnapshot        json.RawMessage `bun:"input_snapshot,type:jsonb"`
	TargetInstallationID *uuid.UUID      `bun:"target_installation_id,type:uuid,nullzero"`
	TargetSourceRootID   *uuid.UUID      `bun:"target_source_root_id,type:uuid,nullzero"`
	// TargetSourceLocationID is the source location an analyze_source operation
	// names. It is required while the operation is active and may become NULL
	// after a terminal state if the location was deleted.
	TargetSourceLocationID *uuid.UUID `bun:"target_source_location_id,type:uuid,nullzero"`
	// SourceAnalysisMode selects normalized source-analysis operation semantics.
	SourceAnalysisMode            string     `bun:"source_analysis_mode,nullzero"`
	ProviderSourceID              *uuid.UUID `bun:"provider_source_id,type:uuid,nullzero"`
	ProviderConfigurationIdentity *string    `bun:"provider_configuration_identity,nullzero"`
	ProviderID                    *uuid.UUID `bun:"-"`
	ProviderEndpoint              string     `bun:"-"`
	ProviderCacheKey              *string    `bun:"provider_cache_key,nullzero"`
	ProviderExplicitRefresh       bool       `bun:"provider_explicit_refresh"`
	ProviderDeliveryClaimedAt     *time.Time `bun:"provider_delivery_claimed_at,nullzero"`
	ProviderExecutionEpoch        int64      `bun:"provider_execution_epoch"`
	// TargetWorkID and TargetStep are both present only for a single-step
	// operation; batch operations select their work items in the snapshot.
	TargetWorkID      *uuid.UUID `bun:"target_work_id,type:uuid,nullzero"`
	TargetStep        *string    `bun:"target_step,nullzero"`
	ToolsReadRequired bool       `bun:"tools_read_required"`
	RerunTarget       bool       `bun:"rerun_target"`
	// SourceAnalysisTools is admission-only resolved context and is never stored.
	SourceAnalysisTools []SourceAnalysisToolSelection `bun:"-"`
	Attempt             int                           `bun:"attempt"`
	BytesCompleted      int64                         `bun:"bytes_completed"`
	BytesTotal          *int64                        `bun:"bytes_total,nullzero"`
	SafeError           *string                       `bun:"safe_error,nullzero"`
	RiverJobID          *int64                        `bun:"river_job_id,nullzero"`
	CreatedAt           time.Time                     `bun:"created_at,nullzero"`
	StartedAt           *time.Time                    `bun:"started_at,nullzero"`
	FinishedAt          *time.Time                    `bun:"finished_at,nullzero"`
	UpdatedAt           time.Time                     `bun:"updated_at,nullzero"`
}

type SetupManagerRepository struct {
	db            *bun.DB
	riverInserter RiverInserter
}

const existingSourceAnalysisArtifactCleanupOperationKind = SourceAnalysisArtifactCleanupOperationKind

func NewSetupManagerRepository(db *bun.DB) *SetupManagerRepository {
	return &SetupManagerRepository{db: db}
}

// SetRiverInserter wires atomic operation admission after the River client is
// constructed and before workers start consuming jobs.
func (repository *SetupManagerRepository) SetRiverInserter(client RiverInserter) {
	repository.riverInserter = client
}

func (repository *SetupManagerRepository) CreateInstallation(ctx context.Context, installation *ToolInstallation) error {
	return repository.CreateInstallationWith(ctx, repository.db, installation)
}

func (repository *SetupManagerRepository) CreateInstallationWith(ctx context.Context, database bun.IDB, installation *ToolInstallation) error {
	if !validInstallationRelativePath(installation) {
		return fmt.Errorf("create tool installation: relative path must match its package and release identity")
	}
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		return fmt.Errorf("create tool installation: %w", err)
	}
	return nil
}

func (repository *SetupManagerRepository) CreateOperation(ctx context.Context, operation *Operation) error {
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repository.CreateOperationWith(ctx, tx, operation)
	})
}

func (repository *SetupManagerRepository) CreateOperationWith(ctx context.Context, database bun.IDB, operation *Operation) error {
	if operation.Attempt == 0 {
		operation.Attempt = 1
	}
	if operation.State == "queued" {
		if operation.Kind == "move_tools_root" {
			if err := lockOutputAdmissionGateExclusive(ctx, database); err != nil {
				return fmt.Errorf("lock output admission gate: %w", err)
			}
		} else if err := AcquireOutputAdmissionGate(ctx, database); err != nil {
			return fmt.Errorf("lock output admission gate: %w", err)
		}
	}
	if operation.Kind == "move_tools_root" && operation.State == "queued" {
		if err := lockToolsMoveGateExclusive(ctx, database); err != nil {
			return fmt.Errorf("lock operation exclusivity: %w", err)
		}
	}
	if _, err := database.NewInsert().Model(operation).Exec(ctx); err != nil {
		return fmt.Errorf("create operation: %w", err)
	}
	return nil
}

func lockToolsMoveGateShared(ctx context.Context, database bun.IDB) error {
	return lockToolsMoveReaders(ctx, database)
}

func lockToolsMoveGateExclusive(ctx context.Context, database bun.IDB) error {
	return lockToolsMoveExclusive(ctx, database)
}

func lockInstallationPackages(ctx context.Context, database bun.IDB, packages ...string) error {
	return lockPackageActivations(ctx, database, packages)
}

func lockSetupCompletion(ctx context.Context, database bun.IDB) error {
	return advisoryXactLock(ctx, database, false, toolsCoordinationNamespace, setupCompletionLockKey)
}

func activeToolsMove(ctx context.Context, tx bun.Tx) (bool, error) {
	var active bool
	err := tx.NewRaw("SELECT EXISTS(SELECT 1 FROM operation WHERE kind='move_tools_root' AND state IN ('queued','running'))").Scan(ctx, &active)
	return active, err
}

// RiverInserter is the subset of a River client used to atomically enqueue an
// operation. Keeping it narrow makes the persistence boundary testable without
// coupling callers to a concrete worker client.
type RiverInserter interface {
	InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// CreateOperationAndEnqueue creates the durable snapshot and its River job in
// one database transaction. Neither record is visible if either insert fails.
func (repository *SetupManagerRepository) CreateOperationAndEnqueue(ctx context.Context, operation *Operation, client RiverInserter, args river.JobArgs, options *river.InsertOpts) error {
	if client == nil {
		return fmt.Errorf("enqueue operation: River client is required")
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repository.createOperationAndEnqueueTx(ctx, tx, operation, client, args, options)
	})
}

// AcoustIDLookupCapture is the immutable source provenance selected while the
// admission transaction holds the canonical source-mutation fence.
type AcoustIDLookupCapture struct {
	WorkID                  uuid.UUID
	FingerprintRowID        uuid.UUID
	WinningResultID         uuid.UUID
	WinnerFingerprint       string
	AlgorithmNamespace      string
	AlgorithmID             int
	FPCalcVersion           string
	ParserContractVersion   int
	SavedReportedDuration   float64
	SHA256                  string
	ImmutableMemberIdentity string
	ProviderSourceID        uuid.UUID
	ConfigurationIdentity   string
}

// CreateAcoustIDLookupOperationAndEnqueue reads the selected successful
// fingerprint and provider configuration in the same transaction that saves
// the operation and River job. The callback may only encode the captured intent.
func (repository *SetupManagerRepository) CreateAcoustIDLookupOperationAndEnqueue(
	ctx context.Context,
	workID uuid.UUID,
	explicitRefresh bool,
	client RiverInserter,
	args river.JobArgs,
	options *river.InsertOpts,
	build func(AcoustIDLookupCapture) (*Operation, error),
) error {
	if workID == uuid.Nil || client == nil || build == nil {
		return fmt.Errorf("admit AcoustID lookup: work, River client, and intent builder are required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("admit AcoustID lookup: lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("admit AcoustID lookup: lock source mutation gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "acoustid-config"); err != nil {
			return fmt.Errorf("admit AcoustID lookup: lock credential configuration: %w", err)
		}
		var rootID, locationID uuid.UUID
		if err := tx.NewRaw(`SELECT source_root_id,location_id FROM source_analysis_work WHERE id=?`, workID).Scan(ctx, &rootID, &locationID); err != nil {
			return fmt.Errorf("admit AcoustID lookup: select current work: %w", err)
		}
		root, location, work := new(SourceRoot), new(SourceLocation), new(SourceAnalysisWork)
		if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, rootID).Scan(ctx, root); err != nil {
			return fmt.Errorf("admit AcoustID lookup: lock source root: %w", err)
		}
		if err := tx.NewRaw(`SELECT * FROM source_location WHERE id=? AND source_root_id=? FOR UPDATE`, locationID, rootID).Scan(ctx, location); err != nil {
			return fmt.Errorf("admit AcoustID lookup: lock source location: %w", err)
		}
		if err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE id=? FOR UPDATE`, workID).Scan(ctx, work); err != nil {
			return fmt.Errorf("admit AcoustID lookup: lock source work: %w", err)
		}
		step := new(SourceAnalysisStep)
		if err := tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step='fingerprint' FOR UPDATE`, workID).Scan(ctx, step); err != nil {
			return fmt.Errorf("admit AcoustID lookup: select successful fingerprint step: %w", err)
		}
		if !root.Enabled || root.Stale() || root.InventoryPath == nil || *root.InventoryPath != root.ConfiguredPath ||
			work.CurrentLocationID == nil || *work.CurrentLocationID != location.ID || work.LocationID != location.ID || work.SourceRootID != root.ID ||
			work.ConfiguredPath != root.ConfiguredPath || work.InventoryPath != *root.InventoryPath ||
			location.RelativePath != work.RelativePath || location.SizeBytes != work.SizeBytes || !sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(work.Mtime)) ||
			step.State != "succeeded" || step.SuccessFingerprintResultID == nil {
			return fmt.Errorf("admit AcoustID lookup: %w", ErrSourceAnalysisStale)
		}
		fingerprint := new(SourceFingerprintResult)
		if err := tx.NewRaw(`SELECT * FROM media_fingerprint_result WHERE id=?`, *step.SuccessFingerprintResultID).Scan(ctx, fingerprint); err != nil {
			return fmt.Errorf("admit AcoustID lookup: read selected fingerprint result: %w", err)
		}
		var key, revision string
		var rows []AppSetting
		if err := tx.NewSelect().Model(&rows).Where("setting_name IN (?)", bun.List([]string{"acoustid_application_key", "internal_acoustid_config_identity"})).Scan(ctx); err != nil {
			return fmt.Errorf("admit AcoustID lookup: read credential configuration: %w", err)
		}
		present := false
		for _, row := range rows {
			switch row.Name {
			case "acoustid_application_key":
				key, present = row.Value, true
			case "internal_acoustid_config_identity":
				revision = row.Value
			}
		}
		if revision == "" || explicitRefresh && (!present || key == "") {
			return fmt.Errorf("admit AcoustID lookup: AcoustID credential configuration is unavailable")
		}
		source, err := ensureAcoustIDProviderSource(ctx, tx)
		if err != nil {
			return fmt.Errorf("admit AcoustID lookup: resolve official provider source: %w", err)
		}
		capture := AcoustIDLookupCapture{
			WorkID: work.ID, FingerprintRowID: fingerprint.ID, WinningResultID: fingerprint.WinningResultID,
			WinnerFingerprint: fingerprint.Fingerprint, AlgorithmNamespace: fingerprint.AlgorithmNamespace,
			AlgorithmID: int(fingerprint.AlgorithmID), FPCalcVersion: fingerprint.FPCalcVersion,
			ParserContractVersion: fingerprint.ParserContractVersion, SavedReportedDuration: fingerprint.ReportedDuration,
			ImmutableMemberIdentity: work.ID.String(), ProviderSourceID: source.ID, ConfigurationIdentity: revision,
		}
		if len(fingerprint.SourceSHA256) != 0 {
			capture.SHA256 = hex.EncodeToString(fingerprint.SourceSHA256)
		} else {
			var digest []byte
			err := tx.NewRaw(`SELECT variant.source_sha256 FROM source_analysis_step AS selected
				JOIN media_variant AS variant ON variant.id=selected.success_sha_variant_id
				WHERE selected.work_id=? AND selected.step='sha256' AND selected.state='succeeded'`, workID).Scan(ctx, &digest)
			if err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("admit AcoustID lookup: read selected SHA-256: %w", err)
			}
			if err == nil && len(digest) != 0 {
				capture.SHA256 = hex.EncodeToString(digest)
			}
		}
		operation, err := build(capture)
		if err != nil {
			return err
		}
		if operation == nil || operation.ID == uuid.Nil || operation.ProviderSourceID == nil || *operation.ProviderSourceID != source.ID || operation.ProviderConfigurationIdentity == nil || *operation.ProviderConfigurationIdentity != revision {
			return fmt.Errorf("admit AcoustID lookup: built operation does not match captured provider configuration")
		}
		var intent struct {
			SchemaVersion           int       `json:"schema_version"`
			WorkID                  uuid.UUID `json:"work_id"`
			FingerprintRowID        uuid.UUID `json:"fingerprint_row_id"`
			WinningResultID         uuid.UUID `json:"winning_result_id"`
			WinnerFingerprint       string    `json:"winner_fingerprint"`
			AlgorithmNamespace      string    `json:"algorithm_namespace"`
			AlgorithmID             int       `json:"algorithm_id"`
			FPCalcVersion           string    `json:"fpcalc_version"`
			ParserContractVersion   int       `json:"parser_contract_version"`
			SavedReportedDuration   float64   `json:"saved_reported_duration"`
			SHA256                  string    `json:"sha256,omitempty"`
			ImmutableMemberIdentity string    `json:"immutable_member_identity,omitempty"`
			LookupDuration          int       `json:"lookup_duration"`
		}
		if err := json.Unmarshal(operation.InputSnapshot, &intent); err != nil {
			return fmt.Errorf("admit AcoustID lookup: invalid encoded intent: %w", err)
		}
		duration := 0
		if capture.SavedReportedDuration > 0 && capture.SavedReportedDuration < float64(int(^uint(0)>>1)) {
			duration = int(capture.SavedReportedDuration)
		}
		if intent.SchemaVersion != 1 || intent.WorkID != capture.WorkID || intent.FingerprintRowID != capture.FingerprintRowID ||
			intent.WinningResultID != capture.WinningResultID || intent.WinnerFingerprint != capture.WinnerFingerprint ||
			intent.AlgorithmNamespace != capture.AlgorithmNamespace || intent.AlgorithmID != capture.AlgorithmID ||
			intent.FPCalcVersion != capture.FPCalcVersion || intent.ParserContractVersion != capture.ParserContractVersion ||
			intent.SavedReportedDuration != capture.SavedReportedDuration || intent.SHA256 != capture.SHA256 ||
			intent.ImmutableMemberIdentity != capture.ImmutableMemberIdentity || intent.LookupDuration != duration || duration == 0 {
			return fmt.Errorf("admit AcoustID lookup: encoded intent differs from selected source provenance")
		}
		canonical, err := json.Marshal(struct {
			WorkID                  uuid.UUID `json:"work_id"`
			FingerprintRowID        uuid.UUID `json:"fingerprint_row_id"`
			WinningResultID         uuid.UUID `json:"winning_result_id"`
			WinnerFingerprint       string    `json:"winner_fingerprint"`
			AlgorithmNamespace      string    `json:"algorithm_namespace"`
			AlgorithmID             int       `json:"algorithm_id"`
			FPCalcVersion           string    `json:"fpcalc_version"`
			ParserContractVersion   int       `json:"parser_contract_version"`
			SavedReportedDuration   float64   `json:"saved_reported_duration"`
			SHA256                  string    `json:"sha256,omitempty"`
			ImmutableMemberIdentity string    `json:"immutable_member_identity,omitempty"`
		}{capture.WorkID, capture.FingerprintRowID, capture.WinningResultID, capture.WinnerFingerprint, capture.AlgorithmNamespace, capture.AlgorithmID, capture.FPCalcVersion, capture.ParserContractVersion, capture.SavedReportedDuration, capture.SHA256, capture.ImmutableMemberIdentity})
		if err != nil {
			return fmt.Errorf("admit AcoustID lookup: encode captured cache identity: %w", err)
		}
		digest := sha256.Sum256(canonical)
		cacheKey := "acoustid-lookup-v1:" + hex.EncodeToString(digest[:])
		if operation.ProviderCacheKey == nil || *operation.ProviderCacheKey != cacheKey {
			return fmt.Errorf("admit AcoustID lookup: cache identity differs from selected source provenance")
		}
		return repository.createOperationAndEnqueueTx(ctx, tx, operation, client, args, options)
	})
}

// createOperationAndEnqueueTx inserts an operation and its River delivery using
// the caller's transaction. It is shared with coordinators that must atomically
// admit multiple related operations.
func (repository *SetupManagerRepository) createOperationAndEnqueueTx(ctx context.Context, tx bun.Tx, operation *Operation, client RiverInserter, args river.JobArgs, options *river.InsertOpts) error {
	var gateErr error
	if operation != nil && operation.Kind == "move_tools_root" && operation.State == "queued" {
		gateErr = lockOutputAdmissionGateExclusive(ctx, tx)
	} else {
		gateErr = AcquireOutputAdmissionGate(ctx, tx)
	}
	if gateErr != nil {
		return fmt.Errorf("enqueue operation: lock output admission gate: %w", gateErr)
	}
	if operation != nil && strings.HasPrefix(operation.Kind, "provider_") {
		if operation.Kind == "provider_acoustid_lookup" {
			if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "acoustid-config"); err != nil {
				return fmt.Errorf("enqueue AcoustID operation: lock configuration: %w", err)
			}
			var currentRevision string
			if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='internal_acoustid_config_identity'`).Scan(ctx, &currentRevision); err != nil {
				return fmt.Errorf("enqueue AcoustID operation: read credential revision: %w", err)
			}
			if operation.ProviderConfigurationIdentity == nil || currentRevision != *operation.ProviderConfigurationIdentity {
				return fmt.Errorf("enqueue AcoustID operation: credential configuration changed")
			}
			var sourceIdentity string
			if operation.ProviderSourceID == nil || tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=?`, *operation.ProviderSourceID).Scan(ctx, &sourceIdentity) != nil || sourceIdentity != acoustIDSourceIdentity {
				return fmt.Errorf("enqueue AcoustID operation: source configuration changed")
			}
		} else {
			if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
				return fmt.Errorf("enqueue provider operation: lock configuration: %w", err)
			}
			var currentIdentity string
			if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentIdentity); err != nil {
				return fmt.Errorf("enqueue provider operation: read configuration: %w", err)
			}
			if operation.ProviderConfigurationIdentity == nil || currentIdentity != *operation.ProviderConfigurationIdentity {
				return fmt.Errorf("enqueue provider operation: configuration changed")
			}
			var sourceIdentity string
			if operation.ProviderSourceID == nil || tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=?`, *operation.ProviderSourceID).Scan(ctx, &sourceIdentity) != nil || sourceIdentity != currentIdentity {
				return fmt.Errorf("enqueue provider operation: source configuration changed")
			}
		}
	}
	result, err := client.InsertTx(ctx, tx.Tx, args, options)
	if err != nil {
		return fmt.Errorf("insert River job: %w", err)
	}
	operation.RiverJobID = &result.Job.ID
	if err := repository.CreateOperationWith(ctx, tx, operation); err != nil {
		return err
	}
	return nil
}

// CreateToolsMoveOperationAndEnqueue records a tools root move and its River job
// in one transaction, under the same operation table lock a scan or analysis
// start takes. Holding that lock, it refuses the move while any active analysis
// holds a managed installation: the move rewrites the global tools directory
// every analysis's pinned executable lives under, so every such hold blocks it,
// not only the installations a preflight happened to list. An installation hold
// is a read hold, so several analyses of different roots may hold the same
// installation and this check never consumes the mutation target the install
// constraint uses.
func (repository *SetupManagerRepository) CreateToolsMoveOperationAndEnqueue(ctx context.Context, operation *Operation, client RiverInserter, args river.JobArgs, options *river.InsertOpts) error {
	if client == nil {
		return fmt.Errorf("enqueue tools move: River client is required")
	}
	if operation == nil || operation.Kind != "move_tools_root" {
		return fmt.Errorf("enqueue tools move: operation must be a tools root move")
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockOutputAdmissionGateExclusive(ctx, tx); err != nil {
			return fmt.Errorf("enqueue tools move: lock output admission gate: %w", err)
		}
		if err := lockToolsMoveGateExclusive(ctx, tx); err != nil {
			return fmt.Errorf("enqueue tools move: lock operation exclusivity: %w", err)
		}
		var snapshot struct {
			SchemaVersion int    `json:"schema_version"`
			OldRoot       string `json:"old_root"`
		}
		if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil || snapshot.SchemaVersion != 1 || snapshot.OldRoot == "" {
			return fmt.Errorf("enqueue tools move: invalid root-pinned snapshot")
		}
		var currentRoot string
		err := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "tools_directory").Scan(ctx, &currentRoot)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("enqueue tools move: read tools directory: %w", err)
		}
		if currentRoot != snapshot.OldRoot {
			return fmt.Errorf("enqueue tools move: tools directory changed since preflight")
		}
		moving, err := activeToolsMove(ctx, tx)
		if err != nil {
			return fmt.Errorf("enqueue tools move: check active root move: %w", err)
		}
		if moving {
			return fmt.Errorf("enqueue tools move: a tools root move is already active")
		}
		var moveSnapshot struct {
			NewRoot string `json:"new_root"`
		}
		if err := json.Unmarshal(operation.InputSnapshot, &moveSnapshot); err != nil || moveSnapshot.NewRoot == "" {
			return fmt.Errorf("enqueue tools move: invalid target root")
		}
		if err := validateToolsMoveRootsAgainstOutput(ctx, tx, snapshot.OldRoot, moveSnapshot.NewRoot); err != nil {
			return fmt.Errorf("enqueue tools move: %w", err)
		}
		held, err := activeAnalysisInstallationHold(ctx, tx)
		if err != nil {
			return fmt.Errorf("enqueue tools move: check active analysis holds: %w", err)
		}
		if held {
			return fmt.Errorf("enqueue tools move: %w", ErrToolsInstallationHeldByAnalysis)
		}
		result, err := client.InsertTx(ctx, tx.Tx, args, options)
		if err != nil {
			return fmt.Errorf("enqueue tools move: insert River job: %w", err)
		}
		operation.RiverJobID = &result.Job.ID
		if err := repository.CreateOperationWith(ctx, tx, operation); err != nil {
			return err
		}
		return nil
	})
}

// activeAnalysisInstallationHold reports whether any queued or running analysis
// holds a managed installation. The caller holds the exclusive tools-move gate,
// which prevents a concurrent analysis start from inserting a hold.
func activeAnalysisInstallationHold(ctx context.Context, tx bun.Tx) (bool, error) {
	var held bool
	err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation WHERE state IN ('queued', 'running') AND
		EXISTS (SELECT 1 FROM operation_tool_read_hold hold WHERE hold.operation_id=operation.id)
	)`).Scan(ctx, &held)
	return held, err
}

func (repository *SetupManagerRepository) CreateInstallationOperationAndEnqueue(ctx context.Context, expectedToolsRoot string, installation *ToolInstallation, operation *Operation, client RiverInserter, args river.JobArgs, options *river.InsertOpts) error {
	if client == nil {
		return fmt.Errorf("enqueue installation: River client is required")
	}
	if installation == nil || operation == nil || operation.TargetInstallationID == nil || *operation.TargetInstallationID != installation.ID {
		return fmt.Errorf("enqueue installation: operation target must match installation")
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("enqueue installation: lock output admission gate: %w", err)
		}
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("enqueue installation: lock tools operations: %w", err)
		}
		if err := lockSetupCompletion(ctx, tx); err != nil {
			return fmt.Errorf("enqueue installation: lock setup completion: %w", err)
		}
		if err := lockInstallationPackages(ctx, tx, installation.PackageKind); err != nil {
			return fmt.Errorf("enqueue installation: lock package: %w", err)
		}
		moving, err := activeToolsMove(ctx, tx)
		if err != nil {
			return fmt.Errorf("enqueue installation: check active tools move: %w", err)
		}
		if moving {
			return fmt.Errorf("enqueue installation: tools root move is active")
		}
		var currentToolsRoot string
		err = tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "tools_directory").Scan(ctx, &currentToolsRoot)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("enqueue installation: read tools directory: %w", err)
		}
		if currentToolsRoot != expectedToolsRoot {
			return fmt.Errorf("enqueue installation: tools directory changed since preflight")
		}
		if err := repository.checkInitialInstallationAdmission(ctx, tx, installation.PackageKind, uuid.Nil, uuid.Nil); err != nil {
			return err
		}
		result, err := client.InsertTx(ctx, tx.Tx, args, options)
		if err != nil {
			return fmt.Errorf("insert River job: %w", err)
		}
		operation.RiverJobID = &result.Job.ID
		if err := repository.CreateInstallationWith(ctx, tx, installation); err != nil {
			return err
		}
		if err := repository.CreateOperationWith(ctx, tx, operation); err != nil {
			return err
		}
		return nil
	})
}

func (repository *SetupManagerRepository) GetInstallation(ctx context.Context, id uuid.UUID) (*ToolInstallation, error) {
	installation := new(ToolInstallation)
	if err := repository.db.NewSelect().Model(installation).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, fmt.Errorf("get tool installation: %w", err)
	}
	return installation, nil
}

func (repository *SetupManagerRepository) GetInstallationForUpdate(ctx context.Context, tx bun.Tx, id uuid.UUID) (*ToolInstallation, error) {
	installation := new(ToolInstallation)
	if err := tx.NewSelect().Model(installation).Where("id = ?", id).For("UPDATE").Scan(ctx); err != nil {
		return nil, fmt.Errorf("lock tool installation: %w", err)
	}
	return installation, nil
}

func (repository *SetupManagerRepository) GetInstallationByIdentity(ctx context.Context, packageKind, sourceName, releaseIdentity, goos, goarch string) (*ToolInstallation, error) {
	installation := new(ToolInstallation)
	if err := repository.db.NewSelect().Model(installation).
		Where("package_kind = ?", packageKind).Where("source_name = ?", sourceName).
		Where("release_identity = ?", releaseIdentity).Where("platform_goos = ?", goos).
		Where("platform_goarch = ?", goarch).Scan(ctx); err != nil {
		return nil, fmt.Errorf("get tool installation by identity: %w", err)
	}
	return installation, nil
}

func (repository *SetupManagerRepository) ListInstallations(ctx context.Context, packageKind, goos, goarch string) ([]ToolInstallation, error) {
	installations := make([]ToolInstallation, 0)
	query := repository.db.NewSelect().Model(&installations).Order("created_at ASC")
	if packageKind != "" {
		query.Where("package_kind = ?", packageKind)
	}
	if goos != "" {
		query.Where("platform_goos = ?", goos)
	}
	if goarch != "" {
		query.Where("platform_goarch = ?", goarch)
	}
	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("list tool installations: %w", err)
	}
	return installations, nil
}

func (repository *SetupManagerRepository) UpdateInstallation(ctx context.Context, installation *ToolInstallation) error {
	if !validInstallationRelativePath(installation) {
		return fmt.Errorf("update tool installation: relative path must match its package and release identity")
	}
	_, err := repository.db.NewUpdate().Model(installation).
		Column("relative_path", "artifact_identities", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("update tool installation: %w", err)
	}
	return nil
}

func (repository *SetupManagerRepository) MarkInstallationReady(ctx context.Context, id uuid.UUID, versions json.RawMessage, verifiedAt time.Time) error {
	result, err := repository.db.NewUpdate().Model((*ToolInstallation)(nil)).
		Set("state = 'ready'").Set("executable_versions = ?", versions).Set("verified_at = ?", verifiedAt).
		Set("updated_at = ?", verifiedAt).Where("id = ?", id).Where("state = 'preparing'").Exec(ctx)
	if err != nil {
		return fmt.Errorf("mark tool installation ready: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("mark tool installation ready: installation must exist and be preparing")
	}
	return nil
}

func (repository *SetupManagerRepository) MarkInstallationFailed(ctx context.Context, id uuid.UUID) error {
	result, err := repository.db.NewUpdate().Model((*ToolInstallation)(nil)).Set("state = 'failed'").Set("updated_at = now()").Where("id = ?", id).Where("state = 'preparing'").Exec(ctx)
	if err != nil {
		return fmt.Errorf("mark tool installation failed: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("mark tool installation failed: installation must exist and be preparing")
	}
	return nil
}

// ActivateInstallation validates a ready installation for the immutable
// platform and updates its package's active setting in the same transaction.
func (repository *SetupManagerRepository) ActivateInstallation(ctx context.Context, id uuid.UUID, packageKind, goos, goarch, activeSetting string) error {
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("lock tools move gate: %w", err)
		}
		moving, err := activeToolsMove(ctx, tx)
		if err != nil {
			return fmt.Errorf("check active tools root move: %w", err)
		}
		if moving {
			return fmt.Errorf("cannot activate installation during an active tools root move")
		}
		if err := lockSetupCompletion(ctx, tx); err != nil {
			return fmt.Errorf("lock setup completion: %w", err)
		}
		if err := lockInstallationPackages(ctx, tx, packageKind); err != nil {
			return fmt.Errorf("lock active installation: %w", err)
		}
		var completedAt string
		if err := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "setup_completed_at").Scan(ctx, &completedAt); err == sql.ErrNoRows {
			return fmt.Errorf("explicit activation is available only after setup completion")
		} else if err != nil {
			return fmt.Errorf("read setup completion: %w", err)
		}
		return repository.activateInstallationTx(ctx, tx, id, packageKind, goos, goarch, activeSetting)
	})
}

func (repository *SetupManagerRepository) ActivateInstallationDuringSetup(ctx context.Context, id uuid.UUID, packageKind, goos, goarch, activeSetting string) (bool, error) {
	activated := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("lock tools move gate: %w", err)
		}
		if err := lockSetupCompletion(ctx, tx); err != nil {
			return fmt.Errorf("lock setup completion: %w", err)
		}
		if err := lockInstallationPackages(ctx, tx, "ffmpeg", "fpcalc"); err != nil {
			return fmt.Errorf("lock active installations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("lock MusicBrainz configuration: %w", err)
		}
		var completedAt string
		err := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "setup_completed_at").Scan(ctx, &completedAt)
		if err == nil {
			return nil
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("read setup completion: %w", err)
		}
		moving, err := activeToolsMove(ctx, tx)
		if err != nil {
			return fmt.Errorf("check active tools root move: %w", err)
		}
		if moving {
			return nil
		}
		if err := refuseOtherReadyInstallationTx(ctx, tx, packageKind, id); err != nil {
			return fmt.Errorf("refuse ambiguous ready installations: %w", err)
		}
		var currentID string
		err = tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", activeSetting).Scan(ctx, &currentID)
		if err == nil {
			// A second successful selection is never silently substituted during
			// the one-time wizard. Re-delivery of the selected ID is idempotent.
			activated = currentID == id.String()
			return nil
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("read active installation: %w", err)
		}
		if err := repository.activateInstallationTx(ctx, tx, id, packageKind, goos, goarch, activeSetting); err != nil {
			return err
		}
		activated = true
		return nil
	})
	return activated, err
}

// FinalizeInstallation atomically records verified readiness, performs the
// one-time Setup activation when still appropriate, and settles only the
// current River delivery. Verification and filesystem cleanup happen outside
// this transaction.
func (repository *SetupManagerRepository) FinalizeInstallation(ctx context.Context, operationID uuid.UUID, attempt int, riverJobID int64, installationID uuid.UUID, packageKind, goos, goarch, activeSetting string, versions json.RawMessage, verifiedAt time.Time) (bool, error) {
	settled := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("finalize installation: lock tools move gate: %w", err)
		}
		if err := lockSetupCompletion(ctx, tx); err != nil {
			return fmt.Errorf("finalize installation: lock setup completion: %w", err)
		}
		if err := lockInstallationPackages(ctx, tx, "ffmpeg", "fpcalc"); err != nil {
			return fmt.Errorf("finalize installation: lock package selections: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("finalize installation: lock MusicBrainz configuration: %w", err)
		}
		installation, err := repository.GetInstallationForUpdate(ctx, tx, installationID)
		if err != nil {
			return err
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if operation.Kind != "install" || operation.State == "failed" || operation.State == "succeeded" ||
			operation.Attempt != attempt || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID ||
			operation.TargetInstallationID == nil || *operation.TargetInstallationID != installationID {
			return fmt.Errorf("finalize installation: delivery is no longer current")
		}
		if installation.PackageKind != packageKind || installation.PlatformGOOS != goos || installation.PlatformGOARCH != goarch {
			return fmt.Errorf("finalize installation: target does not match the verified package")
		}
		var completedAt string
		completionErr := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "setup_completed_at").Scan(ctx, &completedAt)
		if completionErr != nil && completionErr != sql.ErrNoRows {
			return fmt.Errorf("read setup completion: %w", completionErr)
		}
		if completionErr == sql.ErrNoRows {
			if err := refuseOtherReadyInstallationTx(ctx, tx, packageKind, installationID); err != nil {
				return fmt.Errorf("refuse second ready installation during setup: %w", err)
			}
		}
		if installation.State == "preparing" {
			if _, err := tx.NewUpdate().Model((*ToolInstallation)(nil)).
				Set("state = 'ready'").Set("executable_versions = ?", versions).Set("verified_at = ?", verifiedAt).
				Set("updated_at = ?", verifiedAt).Where("id = ?", installationID).Where("state = 'preparing'").Exec(ctx); err != nil {
				return fmt.Errorf("mark installation ready: %w", err)
			}
		} else if installation.State != "ready" {
			return fmt.Errorf("finalize installation: target is not preparing or ready")
		}
		if completionErr == sql.ErrNoRows {
			var activeID string
			err = tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", activeSetting).Scan(ctx, &activeID)
			if err == sql.ErrNoRows {
				if err := repository.activateInstallationTx(ctx, tx, installationID, packageKind, goos, goarch, activeSetting); err != nil {
					return err
				}
			} else if err != nil {
				return fmt.Errorf("read active installation: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("read setup completion: %w", err)
		}
		now := time.Now().UTC()
		operation.State = "succeeded"
		operation.Stage = "succeeded"
		operation.FinishedAt = &now
		operation.UpdatedAt = now
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "finished_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("settle installation operation: %w", err)
		}
		settled = true
		return nil
	})
	return settled, err
}

func readyInstallationsCountTx(ctx context.Context, tx bun.Tx, packageKind string, exclude *uuid.UUID) (int, error) {
	query := tx.NewSelect().Model((*ToolInstallation)(nil)).Where("package_kind = ?", packageKind).Where("state = ?", "ready")
	if exclude != nil {
		query = query.Where("id <> ?", *exclude)
	}
	count, err := query.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("count ready %s installations: %w", packageKind, err)
	}
	return int(count), nil
}

func refuseOtherReadyInstallationTx(ctx context.Context, tx bun.Tx, packageKind string, id uuid.UUID) error {
	count, err := readyInstallationsCountTx(ctx, tx, packageKind, &id)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("another ready %s installation already exists", packageKind)
	}
	return nil
}

func refuseAmbiguousReadyInstallationsTx(ctx context.Context, tx bun.Tx, packageKind string) error {
	count, err := readyInstallationsCountTx(ctx, tx, packageKind, nil)
	if err != nil {
		return err
	}
	if count > 1 {
		return fmt.Errorf("multiple ready %s installations exist", packageKind)
	}
	return nil
}

// FailInstallationDelivery atomically fails only the installation and operation
// still owned by this delivery. A stale delivery must not fail a retried target.
func (repository *SetupManagerRepository) FailInstallationDelivery(ctx context.Context, operationID uuid.UUID, attempt int, riverJobID int64, installationID uuid.UUID, stage, safe string) (bool, error) {
	return repository.failInstallationDelivery(ctx, operationID, attempt, &riverJobID, installationID, stage, safe, false)
}

// FailOrphanedInstallationRecovery fails a legacy installation operation that
// has no River job identity. Recovery is allowed only while the persisted row
// still matches the snapshot identity and remains an orphaned active operation.
func (repository *SetupManagerRepository) FailOrphanedInstallationRecovery(ctx context.Context, operationID uuid.UUID, attempt int, installationID uuid.UUID, stage, safe string) (bool, error) {
	return repository.failInstallationDelivery(ctx, operationID, attempt, nil, installationID, stage, safe, true)
}

// FailUndeliveredInstallationRecovery fails only an active legacy operation
// that has neither a River delivery nor a target installation. Its snapshot is
// compared under the operation lock so recovery cannot settle a replaced row.
func (repository *SetupManagerRepository) FailUndeliveredInstallationRecovery(ctx context.Context, operationID uuid.UUID, attempt int, inputSnapshot json.RawMessage, stage, safe string) (bool, error) {
	changed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("fail undelivered installation recovery: lock tools move gate: %w", err)
		}
		if err := lockSetupCompletion(ctx, tx); err != nil {
			return fmt.Errorf("fail undelivered installation recovery: lock setup completion: %w", err)
		}
		if err := lockInstallationPackages(ctx, tx, "ffmpeg", "fpcalc"); err != nil {
			return fmt.Errorf("fail undelivered installation recovery: lock package selections: %w", err)
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if operation.Kind != "install" || (operation.State != "queued" && operation.State != "running") ||
			operation.Attempt != attempt || operation.RiverJobID != nil || operation.TargetInstallationID != nil ||
			string(operation.InputSnapshot) != string(inputSnapshot) {
			return nil
		}
		now := time.Now().UTC()
		operation.State, operation.Stage, operation.SafeError = "failed", stage, &safe
		operation.FinishedAt, operation.UpdatedAt = &now, now
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "safe_error", "finished_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("settle undelivered installation operation: %w", err)
		}
		changed = true
		return nil
	})
	return changed, err
}

func (repository *SetupManagerRepository) failInstallationDelivery(ctx context.Context, operationID uuid.UUID, attempt int, riverJobID *int64, installationID uuid.UUID, stage, safe string, orphanRecovery bool) (bool, error) {
	changed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("fail installation delivery: lock tools move gate: %w", err)
		}
		if err := lockSetupCompletion(ctx, tx); err != nil {
			return fmt.Errorf("fail installation delivery: lock setup completion: %w", err)
		}
		if err := lockInstallationPackages(ctx, tx, "ffmpeg", "fpcalc"); err != nil {
			return fmt.Errorf("fail installation delivery: lock package selections: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("fail installation delivery: lock MusicBrainz configuration: %w", err)
		}
		installation, err := repository.GetInstallationForUpdate(ctx, tx, installationID)
		if err != nil {
			return err
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if operation.Kind != "install" || operation.State == "failed" || operation.State == "succeeded" ||
			operation.Attempt != attempt ||
			operation.TargetInstallationID == nil || *operation.TargetInstallationID != installationID {
			return nil
		}
		if orphanRecovery {
			if (operation.State != "queued" && operation.State != "running") || operation.RiverJobID != nil {
				return nil
			}
		} else if operation.RiverJobID == nil || *operation.RiverJobID != *riverJobID {
			return nil
		}
		if installation.State == "preparing" {
			if _, err := tx.NewUpdate().Model((*ToolInstallation)(nil)).Set("state = 'failed'").Set("updated_at = now()").
				Where("id = ?", installationID).Where("state = 'preparing'").Exec(ctx); err != nil {
				return fmt.Errorf("mark installation failed: %w", err)
			}
		} else if installation.State != "failed" && (!orphanRecovery || installation.State != "ready") {
			return fmt.Errorf("fail installation delivery: target is not preparing or failed")
		}
		now := time.Now().UTC()
		operation.State, operation.Stage = "failed", stage
		operation.SafeError = &safe
		operation.FinishedAt, operation.UpdatedAt = &now, now
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "safe_error", "finished_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("settle failed installation operation: %w", err)
		}
		changed = true
		return nil
	})
	return changed, err
}

// checkInitialInstallationAdmission runs while the setup-completion and target
// package locks are held. Preparing and failed rows are deliberately not
// reservations: only a ready version or a queued/running install owns the slot.
func (repository *SetupManagerRepository) checkInitialInstallationAdmission(ctx context.Context, tx bun.Tx, packageKind string, excludeOperationID, excludeInstallationID uuid.UUID) error {
	var completed bool
	if err := tx.NewRaw("SELECT EXISTS(SELECT 1 FROM app_setting WHERE setting_name = ?)", "setup_completed_at").Scan(ctx, &completed); err != nil {
		return fmt.Errorf("check setup completion: %w", err)
	}
	if completed {
		return nil
	}
	var readyCount int
	readyQuery := tx.NewSelect().Model((*ToolInstallation)(nil)).Where("package_kind = ?", packageKind).Where("state = 'ready'")
	if excludeInstallationID != uuid.Nil {
		readyQuery.Where("id <> ?", excludeInstallationID)
	}
	if err := readyQuery.ColumnExpr("count(*)").Scan(ctx, &readyCount); err != nil {
		return fmt.Errorf("check ready package installations: %w", err)
	}
	if readyCount > 1 {
		return fmt.Errorf("initial setup has multiple ready %s installations and requires owner resolution", packageKind)
	}
	if readyCount == 1 {
		return fmt.Errorf("initial setup already has a successful %s installation", packageKind)
	}
	activeQuery := tx.NewSelect().Model((*Operation)(nil)).Where("operation.kind = 'install'").Where("operation.state IN ('queued', 'running')").Where("installation.package_kind = ?", packageKind).
		Join("JOIN tool_installation AS installation ON installation.id = operation.target_installation_id")
	if excludeOperationID != uuid.Nil {
		activeQuery.Where("operation.id <> ?", excludeOperationID)
	}
	active, err := activeQuery.Exists(ctx)
	if err != nil {
		return fmt.Errorf("check active package installation: %w", err)
	}
	if active {
		return fmt.Errorf("an installation for %s is already queued or running", packageKind)
	}
	return nil
}

func (repository *SetupManagerRepository) activateInstallationTx(ctx context.Context, tx bun.Tx, id uuid.UUID, packageKind, goos, goarch, activeSetting string) error {
	installation, err := repository.GetInstallationForUpdate(ctx, tx, id)
	if err != nil {
		return err
	}
	if installation.PackageKind != packageKind || installation.PlatformGOOS != goos || installation.PlatformGOARCH != goarch || installation.State != "ready" {
		return fmt.Errorf("installation is not a ready %s installation for %s/%s", packageKind, goos, goarch)
	}
	if _, err := tx.NewInsert().Model(&AppSetting{Name: activeSetting, Value: id.String()}).
		On("CONFLICT (setting_name) DO UPDATE").Set("setting_value = EXCLUDED.setting_value").Set("updated_at = now()").Exec(ctx); err != nil {
		return fmt.Errorf("set active installation: %w", err)
	}
	return nil
}

func (repository *SetupManagerRepository) DeleteInstallation(ctx context.Context, id uuid.UUID, packageKind, goos, goarch, activeSetting string, removeFiles func(*ToolInstallation, string) error) error {
	if removeFiles == nil {
		return fmt.Errorf("delete installation: filesystem remover is required")
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("lock tools operations for installation deletion: %w", err)
		}
		moving, err := activeToolsMove(ctx, tx)
		if err != nil {
			return fmt.Errorf("check active tools root move: %w", err)
		}
		if moving {
			return fmt.Errorf("cannot delete installation during an active tools root move")
		}
		if err := lockInstallationPackages(ctx, tx, packageKind); err != nil {
			return fmt.Errorf("lock active installation: %w", err)
		}
		installation, err := repository.GetInstallationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if installation.PackageKind != packageKind || installation.PlatformGOOS != goos ||
			installation.PlatformGOARCH != goarch || (installation.State != "ready" && installation.State != "failed") {
			return fmt.Errorf("installation is not deletable for %s/%s", goos, goarch)
		}
		var activeSettingValue string
		err = tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", activeSetting).
			Scan(ctx, &activeSettingValue)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read active installation setting: %w", err)
		}
		if activeSettingValue == id.String() {
			return fmt.Errorf("cannot delete active installation")
		}
		conflicts, err := repository.ListActiveOperationConflictsForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(conflicts) != 0 {
			return fmt.Errorf("cannot delete installation with an active operation")
		}
		var toolsRoot string
		err = tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "tools_directory").Scan(ctx, &toolsRoot)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read tools directory: %w", err)
		}
		if err := removeFiles(installation, toolsRoot); err != nil {
			return fmt.Errorf("remove managed installation files: %w", err)
		}
		if _, err := tx.NewDelete().Model(installation).WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("delete tool installation: %w", err)
		}
		return nil
	})
}

func validInstallationRelativePath(installation *ToolInstallation) bool {
	if installation == nil || (installation.PackageKind != "ffmpeg" && installation.PackageKind != "fpcalc") {
		return false
	}
	release := installation.ReleaseIdentity
	separator := "/"
	if installation.PlatformGOOS == "windows" {
		separator = `\`
	}
	return release != "" && release != "." && release != ".." &&
		!strings.ContainsAny(release, `/\:`) &&
		installation.RelativePath == installation.PackageKind+separator+release
}

func (repository *SetupManagerRepository) CommitToolsRootMove(ctx context.Context, operationID uuid.UUID, oldRoot, newRoot string) error {
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateExclusive(ctx, tx); err != nil {
			return fmt.Errorf("commit tools root move: lock operation exclusivity: %w", err)
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if operation.Kind != "move_tools_root" || operation.State != "running" {
			return fmt.Errorf("tools root move operation is not running")
		}
		var snapshot struct {
			OldRoot string `json:"old_root"`
			NewRoot string `json:"new_root"`
		}
		if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil || snapshot.OldRoot != oldRoot || snapshot.NewRoot != newRoot {
			return fmt.Errorf("tools root move snapshot changed before switch")
		}
		if err := validateToolsMoveRootsAgainstOutput(ctx, tx, oldRoot, newRoot); err != nil {
			return fmt.Errorf("switch tools root: %w", err)
		}
		var currentRoot string
		if err := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ? FOR UPDATE", "tools_directory").Scan(ctx, &currentRoot); err != nil {
			return fmt.Errorf("read current tools directory: %w", err)
		}
		if currentRoot != oldRoot {
			return fmt.Errorf("tools directory changed since move was started")
		}
		if _, err := tx.NewInsert().Model(&AppSetting{Name: "tools_directory", Value: newRoot}).
			On("CONFLICT (setting_name) DO UPDATE").Set("setting_value = EXCLUDED.setting_value").Set("updated_at = now()").Exec(ctx); err != nil {
			return fmt.Errorf("switch tools directory: %w", err)
		}
		now := time.Now().UTC()
		operation.Stage = "switched"
		operation.UpdatedAt = now
		if _, err := tx.NewUpdate().Model(operation).Column("stage", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("record tools root switch: %w", err)
		}
		return nil
	})
}

func (repository *SetupManagerRepository) RollbackToolsRootMove(ctx context.Context, operationID uuid.UUID, oldRoot, newRoot string) error {
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateExclusive(ctx, tx); err != nil {
			return fmt.Errorf("rollback tools root move: lock operation exclusivity: %w", err)
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if operation.Kind != "move_tools_root" || operation.State != "running" ||
			(operation.Stage != "switched" && operation.Stage != "rollback_pending") {
			return fmt.Errorf("tools root move is not ready for rollback")
		}
		if err := validateToolsMoveRootsAgainstOutput(ctx, tx, oldRoot, newRoot); err != nil {
			return fmt.Errorf("rollback tools root: %w", err)
		}
		var currentRoot string
		if err := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ? FOR UPDATE", "tools_directory").Scan(ctx, &currentRoot); err != nil {
			return fmt.Errorf("read current tools directory: %w", err)
		}
		if currentRoot != newRoot {
			return fmt.Errorf("tools directory changed since move was switched")
		}
		if _, err := tx.NewInsert().Model(&AppSetting{Name: "tools_directory", Value: oldRoot}).
			On("CONFLICT (setting_name) DO UPDATE").Set("setting_value = EXCLUDED.setting_value").Set("updated_at = now()").Exec(ctx); err != nil {
			return fmt.Errorf("restore tools directory: %w", err)
		}
		operation.Stage = "rolled_back"
		operation.UpdatedAt = time.Now().UTC()
		if _, err := tx.NewUpdate().Model(operation).Column("stage", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("record tools root rollback: %w", err)
		}
		return nil
	})
}

func (repository *SetupManagerRepository) FinishToolsRootMove(ctx context.Context, operationID uuid.UUID) error {
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if operation.Kind != "move_tools_root" || operation.State != "running" || operation.Stage != "switched" {
			return fmt.Errorf("tools root move has not switched")
		}
		now := time.Now().UTC()
		operation.State = "succeeded"
		operation.FinishedAt = &now
		operation.UpdatedAt = now
		if _, err := tx.NewUpdate().Model(operation).Column("state", "finished_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("finish tools root move: %w", err)
		}
		return nil
	})
}

func (repository *SetupManagerRepository) GetOperation(ctx context.Context, id uuid.UUID) (*Operation, error) {
	operation := new(Operation)
	if err := repository.db.NewSelect().Model(operation).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, fmt.Errorf("get operation: %w", err)
	}
	return operation, nil
}

func (repository *SetupManagerRepository) GetOperationForUpdate(ctx context.Context, tx bun.Tx, id uuid.UUID) (*Operation, error) {
	operation := new(Operation)
	if err := tx.NewSelect().Model(operation).Where("id = ?", id).For("UPDATE").Scan(ctx); err != nil {
		return nil, fmt.Errorf("lock operation: %w", err)
	}
	return operation, nil
}

// ClaimProviderOperationDelivery allows one active provider execution per
// durable attempt. The short fixed lease lets River redelivery recover after a
// process crash without introducing a provider-cache freshness policy.
func (repository *SetupManagerRepository) ClaimProviderOperationDelivery(ctx context.Context, id uuid.UUID, attempt int, riverJobID int64) (int64, bool, error) {
	var epoch int64
	err := repository.db.NewRaw(`UPDATE operation SET provider_delivery_claimed_at = now(), provider_execution_epoch = provider_execution_epoch + 1, updated_at = now()
		WHERE id = ? AND kind IN ('provider_release_lookup', 'provider_recording_lookup', 'provider_release_search', 'provider_recording_search', 'provider_artist_search', 'provider_artist_release_browse', 'provider_acoustid_lookup', 'matching_apply_v1')
		AND state IN ('queued', 'running') AND attempt = ? AND river_job_id = ?
		AND (provider_delivery_claimed_at IS NULL OR provider_delivery_claimed_at < now() - interval '5 minutes')
		RETURNING provider_execution_epoch`, id, attempt, riverJobID).Scan(ctx, &epoch)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("claim provider operation delivery: %w", err)
	}
	return epoch, true, nil
}

// TransitionProviderOperationForDelivery applies a transition only for the
// currently claimed execution, fencing work from an expired lease.
func (repository *SetupManagerRepository) TransitionProviderOperationForDelivery(ctx context.Context, id uuid.UUID, attempt int, riverJobID int64, epoch int64, transition func(*Operation) error) (bool, error) {
	return repository.transitionProviderOperationForDelivery(ctx, id, attempt, riverJobID, epoch, nil, transition)
}

// TransitionProviderOperationForDeliveryWithMatchingWake also settles matching
// dependencies and records durable parent wake deliveries in the same commit.
func (repository *SetupManagerRepository) TransitionProviderOperationForDeliveryWithMatchingWake(ctx context.Context, id uuid.UUID, attempt int, riverJobID int64, epoch int64, client RiverInserter, transition func(*Operation) error) (bool, error) {
	return repository.transitionProviderOperationForDelivery(ctx, id, attempt, riverJobID, epoch, client, transition)
}

func (repository *SetupManagerRepository) transitionProviderOperationForDelivery(ctx context.Context, id uuid.UUID, attempt int, riverJobID int64, epoch int64, client RiverInserter, transition func(*Operation) error) (bool, error) {
	changed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		operation, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if !isProviderOperationKind(operation.Kind) || operation.Attempt != attempt || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID || operation.ProviderExecutionEpoch != epoch {
			return nil
		}
		if err := transition(operation); err != nil {
			return err
		}
		if operation.State == "succeeded" && operation.Kind == "provider_release_lookup" {
			var manual bool
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM manual_matching_admission WHERE lookup_operation_id=?)`, operation.ID).Scan(ctx, &manual); err != nil {
				return fmt.Errorf("check manual lookup receipt: %w", err)
			}
			if manual {
				var receipt bool
				if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM manual_matching_lookup_seal seal
					WHERE seal.lookup_operation_id=? AND seal.provider_source_id=? AND seal.configuration_identity=?
					AND seal.cache_key=? AND seal.sealed_at IS NULL)`, operation.ID, operation.ProviderSourceID,
					operation.ProviderConfigurationIdentity, operation.ProviderCacheKey).Scan(ctx, &receipt); err != nil {
					return fmt.Errorf("check accepted manual lookup receipt: %w", err)
				}
				if !receipt {
					return fmt.Errorf("succeed manual lookup: accepted response receipt is required")
				}
			}
		}
		operation.UpdatedAt = time.Now().UTC()
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "bytes_completed", "bytes_total", "safe_error", "attempt", "started_at", "finished_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("transition provider operation delivery: %w", err)
		}
		if operation.State == "succeeded" || operation.State == "failed" {
			if operation.State == "succeeded" {
				result, err := tx.NewRaw(`UPDATE manual_matching_lookup_seal SET sealed_at=now()
						WHERE lookup_operation_id=? AND sealed_at IS NULL`, operation.ID).Exec(ctx)
				if err != nil {
					return fmt.Errorf("seal accepted manual lookup response: %w", err)
				}
				if _, err := result.RowsAffected(); err != nil {
					return fmt.Errorf("seal accepted manual lookup response: %w", err)
				}
			}
			if err := settleMatchingProviderDependencies(ctx, tx, client, operation); err != nil {
				return err
			}
		}
		changed = true
		return nil
	})
	return changed, err
}

type matchingDependencyWake struct {
	RunID    uuid.UUID `bun:"parent_run_id"`
	Quiesced bool      `bun:"quiesced"`
}

func settleMatchingProviderDependencies(ctx context.Context, tx bun.Tx, client RiverInserter, operation *Operation) error {
	// Coordinate with obsolete-run classification before settling dependency rows.
	// The classifier locks matching_run before writing quiesced_at; taking the same
	// row lock here makes its marker authoritative for whether a fresh parent wake
	// may be admitted. Provider outcome settlement remains truthful either way.
	parents := make([]matchingDependencyWake, 0)
	if err := tx.NewRaw(`SELECT dependency.parent_run_id, run.quiesced_at IS NOT NULL AS quiesced
		FROM matching_provider_dependency dependency
		JOIN matching_run run ON run.id=dependency.parent_run_id
		WHERE dependency.request_id=? AND dependency.terminal_outcome IS NULL
		ORDER BY dependency.parent_run_id FOR UPDATE OF run`, operation.ID).Scan(ctx, &parents); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("lock matching parents for dependency settlement: %w", err)
	}
	dependencies := make([]matchingDependencyWake, 0)
	if err := tx.NewRaw(`UPDATE matching_provider_dependency SET terminal_outcome=?, settled_at=now() WHERE request_id=? AND terminal_outcome IS NULL RETURNING parent_run_id`, operation.State, operation.ID).Scan(ctx, &dependencies); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("settle matching provider dependencies: %w", err)
	}
	if len(dependencies) == 0 {
		return nil
	}
	if client == nil {
		return fmt.Errorf("matching dependency settlement requires a River client")
	}
	quiesced := make(map[uuid.UUID]struct{}, len(parents))
	for _, parent := range parents {
		if parent.Quiesced {
			quiesced[parent.RunID] = struct{}{}
		}
	}
	for _, dependency := range dependencies {
		if _, isQuiesced := quiesced[dependency.RunID]; isQuiesced {
			continue
		}
		var parentID uuid.UUID
		if err := tx.NewRaw(`SELECT operation_id FROM matching_run WHERE id=?`, dependency.RunID).Scan(ctx, &parentID); err != nil {
			return fmt.Errorf("load matching parent for dependency wake: %w", err)
		}
		inserted, err := client.InsertTx(ctx, tx.Tx, MatchingEvaluationWakeArgs{OperationID: parentID}, nil)
		if err != nil {
			return fmt.Errorf("enqueue matching dependency wake: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_delivery(run_id, river_job_id, operation_attempt)
				SELECT ?, ?, attempt FROM operation WHERE id=? ON CONFLICT (river_job_id) DO NOTHING`, dependency.RunID, inserted.Job.ID, parentID).Exec(ctx); err != nil {
			return fmt.Errorf("record matching dependency wake: %w", err)
		}
	}
	return nil
}
func isProviderOperationKind(kind string) bool {
	switch kind {
	case "provider_release_lookup", "provider_recording_lookup", "provider_release_search", "provider_recording_search", "provider_artist_search", "provider_artist_release_browse", "provider_acoustid_lookup", MatchingApplyOperationKind:
		return true
	default:
		return false
	}
}

func (repository *SetupManagerRepository) ListOperations(ctx context.Context, states ...string) ([]Operation, error) {
	operations := make([]Operation, 0)
	query := repository.db.NewSelect().Model(&operations).Order("created_at DESC")
	if len(states) > 0 {
		query.Where("state IN (?)", bun.List(states))
	}
	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("list operations: %w", err)
	}
	return operations, nil
}

func (repository *SetupManagerRepository) ListActiveOperationConflictsForUpdate(ctx context.Context, tx bun.Tx, targetInstallationID uuid.UUID) ([]Operation, error) {
	operations := make([]Operation, 0)
	if err := tx.NewSelect().Model(&operations).Where("state IN ('queued', 'running')").
		Where("(kind = 'move_tools_root' OR target_installation_id = ? OR EXISTS (SELECT 1 FROM operation_tool_read_hold hold WHERE hold.operation_id=operation.id AND hold.installation_id=?))", targetInstallationID, targetInstallationID).For("UPDATE").Scan(ctx); err != nil {
		return nil, fmt.Errorf("list active operation conflicts: %w", err)
	}
	return operations, nil
}

func (repository *SetupManagerRepository) UpdateOperation(ctx context.Context, operation *Operation) error {
	operation.UpdatedAt = time.Now().UTC()
	_, err := repository.db.NewUpdate().Model(operation).Column("state", "stage", "bytes_completed", "bytes_total", "safe_error", "river_job_id", "attempt", "started_at", "finished_at", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("update operation: %w", err)
	}
	return nil
}

// TransitionOperation serializes read-modify-write operation state changes.
// The callback runs while the operation row is locked and is never invoked for a
// missing operation. Source transitions acquire root, locations and work before
// the operation row; terminal cleanup occurs in the same transaction.
func (repository *SetupManagerRepository) TransitionOperation(ctx context.Context, id uuid.UUID, transition func(*Operation) error) error {
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("transition operation: lock output admission gate: %w", err)
		}
		var rootID *uuid.UUID
		if err := tx.NewRaw(`SELECT target_source_root_id FROM operation WHERE id=?`, id).Scan(ctx, &rootID); err != nil {
			return fmt.Errorf("transition operation: read source root target: %w", err)
		}
		if rootID != nil {
			root := new(SourceRoot)
			if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, *rootID).Scan(ctx, root); err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("transition operation: lock source root: %w", err)
			}
			type heldWork struct{ ID, LocationID uuid.UUID }
			works := make([]heldWork, 0)
			if err := tx.NewRaw(`SELECT h.work_id AS id,w.location_id FROM operation_source_work_hold h JOIN source_analysis_work w ON w.id=h.work_id WHERE h.operation_id=? ORDER BY h.work_id`, id).Scan(ctx, &works); err != nil {
				return fmt.Errorf("transition operation: read held work: %w", err)
			}
			locations := append([]heldWork(nil), works...)
			sort.Slice(locations, func(i, j int) bool { return locations[i].LocationID.String() < locations[j].LocationID.String() })
			for _, work := range locations {
				location := new(SourceLocation)
				if err := tx.NewRaw(`SELECT * FROM source_location WHERE id=? AND source_root_id=? FOR UPDATE`, work.LocationID, *rootID).Scan(ctx, location); err != nil && err != sql.ErrNoRows {
					return fmt.Errorf("transition operation: lock held location: %w", err)
				}
			}
			for _, work := range works {
				row := new(SourceAnalysisWork)
				if err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE id=? FOR UPDATE`, work.ID).Scan(ctx, row); err != nil && err != sql.ErrNoRows {
					return fmt.Errorf("transition operation: lock held work: %w", err)
				}
			}
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := transition(operation); err != nil {
			return err
		}
		if operation.State == "failed" || operation.State == "succeeded" {
			var hasWorkHolds, hasToolHolds, hasExecution bool
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation_source_work_hold WHERE operation_id=?)`, operation.ID).Scan(ctx, &hasWorkHolds); err != nil {
				return fmt.Errorf("transition operation: check work holds: %w", err)
			}
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation_tool_read_hold WHERE operation_id=?)`, operation.ID).Scan(ctx, &hasToolHolds); err != nil {
				return fmt.Errorf("transition operation: check tool holds: %w", err)
			}
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM source_analysis_step WHERE execution_operation_id=?)`, operation.ID).Scan(ctx, &hasExecution); err != nil {
				return fmt.Errorf("transition operation: check execution triples: %w", err)
			}
			if (operation.SourceAnalysisMode != "" || hasExecution) && operation.State == "succeeded" {
				var unfinished bool
				if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM source_analysis_step WHERE execution_operation_id=? AND state IN ('queued','running'))`, operation.ID).Scan(ctx, &unfinished); err != nil {
					return fmt.Errorf("transition normalized source analysis: check unfinished steps: %w", err)
				}
				if unfinished {
					return fmt.Errorf("transition normalized source analysis: selected steps are unfinished")
				}
			}
			if operation.State == "failed" && (operation.SourceAnalysisMode != "" || hasWorkHolds || hasToolHolds || hasExecution) {
				if operation.SafeError == nil || *operation.SafeError == "" {
					return fmt.Errorf("transition source operation: a safe error is required")
				}
				if _, err := tx.NewRaw(`UPDATE source_analysis_step SET state='failed',safe_error=?,skip_reason=NULL,
					execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL,last_operation_id=?,updated_at=now()
					WHERE execution_operation_id=? AND state IN ('queued','running')`, *operation.SafeError, operation.ID, operation.ID).Exec(ctx); err != nil {
					return fmt.Errorf("transition source operation: fail unfinished steps: %w", err)
				}
			}
			if _, err := tx.NewRaw(`UPDATE source_analysis_step SET execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL,updated_at=now() WHERE execution_operation_id=?`, operation.ID).Exec(ctx); err != nil {
				return fmt.Errorf("transition source operation: clear step execution triples: %w", err)
			}
			if _, err := tx.NewRaw(`DELETE FROM operation_source_work_hold WHERE operation_id=?`, operation.ID).Exec(ctx); err != nil {
				return fmt.Errorf("transition source operation: release work holds: %w", err)
			}
			if _, err := tx.NewRaw(`DELETE FROM operation_tool_read_hold WHERE operation_id=?`, operation.ID).Exec(ctx); err != nil {
				return fmt.Errorf("transition source operation: release tool holds: %w", err)
			}
			operation.TargetWorkID = nil
			operation.TargetStep = nil
			operation.TargetSourceRootID = nil
			operation.TargetSourceLocationID = nil
			operation.ToolsReadRequired = false
			operation.RerunTarget = false
		}
		operation.UpdatedAt = time.Now().UTC()
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "bytes_completed", "bytes_total", "safe_error", "river_job_id", "attempt", "started_at", "finished_at", "updated_at", "target_work_id", "target_step", "target_source_root_id", "target_source_location_id", "tools_read_required", "rerun_target").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("transition operation: %w", err)
		}
		return nil
	})
}

// TransitionOperationForDelivery applies an installation or source-artifact
// cleanup worker transition only while the operation still names that River job
// and attempt.
func (repository *SetupManagerRepository) TransitionOperationForDelivery(ctx context.Context, id uuid.UUID, attempt int, riverJobID int64, transition func(*Operation) error) (bool, error) {
	changed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("transition operation delivery: lock output admission gate: %w", err)
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if (operation.Kind != "install" && operation.Kind != "move_tools_root" && operation.Kind != existingSourceAnalysisArtifactCleanupOperationKind) || operation.Attempt != attempt || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID {
			return nil
		}
		if err := transition(operation); err != nil {
			return err
		}
		operation.UpdatedAt = time.Now().UTC()
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "bytes_completed", "bytes_total", "safe_error", "attempt", "started_at", "finished_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("transition operation delivery: %w", err)
		}
		changed = true
		return nil
	})
	return changed, err
}

// RetryOperationAndEnqueue locks the failed operation, preserves its immutable
// target installation and snapshot, and atomically creates its next River job.
func (repository *SetupManagerRepository) RetryOperationAndEnqueue(ctx context.Context, id uuid.UUID, client RiverInserter, args river.JobArgs, options *river.InsertOpts) (*Operation, error) {
	if client == nil {
		return nil, fmt.Errorf("retry operation: River client is required")
	}
	// Installation, activation, and deletion retries share package and
	// installation locks with DeleteInstallation. Capture the target before
	// opening the transaction so those locks precede the operation row lock.
	captured, err := repository.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	var capturedInstallation *ToolInstallation
	mutationTarget := captured.Kind == "install" || captured.Kind == "activate" || captured.Kind == "delete"
	if mutationTarget {
		if captured.TargetInstallationID == nil {
			return nil, fmt.Errorf("retry operation: installation target is unavailable")
		}
		capturedInstallation, err = repository.GetInstallation(ctx, *captured.TargetInstallationID)
		if err != nil {
			return nil, fmt.Errorf("retry operation: installation target is unavailable: %w", err)
		}
	}
	var operation *Operation
	err = repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if captured.Kind == "move_tools_root" {
			if err := lockOutputAdmissionGateExclusive(ctx, tx); err != nil {
				return fmt.Errorf("retry operation: lock output admission gate: %w", err)
			}
		} else if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("retry operation: lock output admission gate: %w", err)
		}
		switch captured.Kind {
		case "install", "activate", "delete":
			if err := lockToolsMoveGateShared(ctx, tx); err != nil {
				return fmt.Errorf("retry operation: lock tools move gate: %w", err)
			}
			if captured.Kind == "install" {
				if err := lockSetupCompletion(ctx, tx); err != nil {
					return fmt.Errorf("retry operation: lock setup completion: %w", err)
				}
			}
			moving, err := activeToolsMove(ctx, tx)
			if err != nil {
				return fmt.Errorf("retry operation: check active tools move: %w", err)
			}
			if moving {
				return fmt.Errorf("retry operation: tools root move is active")
			}
			if err := lockInstallationPackages(ctx, tx, capturedInstallation.PackageKind); err != nil {
				return fmt.Errorf("retry operation: lock installation package: %w", err)
			}
			lockedInstallation, err := repository.GetInstallationForUpdate(ctx, tx, *captured.TargetInstallationID)
			if err != nil {
				return fmt.Errorf("retry operation: installation target is unavailable: %w", err)
			}
			if lockedInstallation.PackageKind != capturedInstallation.PackageKind {
				return fmt.Errorf("retry operation: installation target changed")
			}
		case "move_tools_root":
			if err := lockToolsMoveGateExclusive(ctx, tx); err != nil {
				return fmt.Errorf("retry operation: lock tools move gate: %w", err)
			}
		}
		locked, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if locked.Kind != captured.Kind ||
			(locked.TargetInstallationID == nil) != (captured.TargetInstallationID == nil) ||
			(locked.TargetInstallationID != nil && *locked.TargetInstallationID != *captured.TargetInstallationID) {
			return fmt.Errorf("retry operation: operation target changed")
		}
		if locked.State != "failed" {
			return fmt.Errorf("only failed operations can be retried")
		}
		if locked.Kind == "install" && locked.TargetInstallationID != nil {
			if err := repository.checkInitialInstallationAdmission(ctx, tx, capturedInstallation.PackageKind, locked.ID, *locked.TargetInstallationID); err != nil {
				return fmt.Errorf("retry operation: %w", err)
			}
		}
		if locked.Kind == "install" || locked.Kind == "move_tools_root" {
			if err := verifyToolsOperationRootForRetry(ctx, tx, locked); err != nil {
				return err
			}
			if locked.Kind == "move_tools_root" {
				moving, err := activeToolsMove(ctx, tx)
				if err != nil {
					return fmt.Errorf("retry operation: check active root move: %w", err)
				}
				if moving {
					return fmt.Errorf("retry operation: another tools root move is active")
				}
			}
		}
		previousStage := locked.Stage
		result, err := client.InsertTx(ctx, tx.Tx, args, options)
		if err != nil {
			return fmt.Errorf("insert retry River job: %w", err)
		}
		if locked.Kind == "install" || locked.Kind == "move_tools_root" {
			oldJobID := int64(0)
			if locked.RiverJobID != nil {
				oldJobID = *locked.RiverJobID
			}
			claimResult, err := tx.NewRaw(`UPDATE tools_execution_claim
				SET attempt = ?, river_job_id = ?, claimed_at = now()
				WHERE operation_id = ? AND attempt = ? AND river_job_id = ? AND borrower = ? AND NOT borrowed`,
				locked.Attempt+1, result.Job.ID, locked.ID, locked.Attempt, oldJobID, locked.Kind).Exec(ctx)
			if err != nil {
				return fmt.Errorf("handoff tools execution claim for retry: %w", err)
			}
			updated, err := claimResult.RowsAffected()
			if err != nil {
				return fmt.Errorf("handoff tools execution claim for retry: %w", err)
			}
			if updated == 0 {
				var claimExists bool
				if err := tx.NewRaw("SELECT EXISTS (SELECT 1 FROM tools_execution_claim WHERE operation_id = ?)", locked.ID).Scan(ctx, &claimExists); err != nil {
					return fmt.Errorf("check tools execution claim for retry: %w", err)
				}
				if claimExists {
					return ErrToolsExecutionClaimHeld
				}
			}
		}
		if locked.Kind == "install" && locked.TargetInstallationID != nil {
			if _, err := tx.NewUpdate().Model((*ToolInstallation)(nil)).
				Set("state = 'preparing'").Set("updated_at = now()").
				Where("id = ?", *locked.TargetInstallationID).Where("state = 'failed'").Exec(ctx); err != nil {
				return fmt.Errorf("reset installation for retry: %w", err)
			}
		}
		locked.State = "queued"
		locked.Stage = "retry:" + previousStage
		locked.SafeError = nil
		locked.StartedAt = nil
		locked.FinishedAt = nil
		locked.BytesCompleted = 0
		locked.BytesTotal = nil
		locked.RiverJobID = &result.Job.ID
		locked.Attempt++
		locked.UpdatedAt = time.Now().UTC()
		locked.ProviderDeliveryClaimedAt = nil
		if _, err := tx.NewUpdate().Model(locked).Column("state", "stage", "bytes_completed", "bytes_total", "safe_error", "river_job_id", "attempt", "started_at", "finished_at", "updated_at", "provider_delivery_claimed_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("retry operation: %w", err)
		}
		operation = locked
		return nil
	})
	if err != nil {
		return nil, err
	}
	return operation, nil
}

// RequeueMatchingApplyOperation preserves the child's immutable intent while
// replacing a dead or explicitly failed River delivery in the same transaction.
func (repository *SetupManagerRepository) RequeueMatchingApplyOperation(ctx context.Context, id uuid.UUID, client RiverInserter, args river.JobArgs, options *river.InsertOpts, recoverActive bool) (*Operation, error) {
	if client == nil {
		client = repository.riverInserter
	}
	if client == nil {
		return nil, fmt.Errorf("requeue matching apply: River client is required")
	}
	var operation *Operation
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("requeue matching apply: lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("requeue matching apply: lock source mutations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
			return fmt.Errorf("requeue matching apply: lock MusicBrainz configuration: %w", err)
		}
		current, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Kind != MatchingApplyOperationKind || (current.State != "failed" && (!recoverActive || (current.State != "queued" && current.State != "running"))) {
			return fmt.Errorf("requeue matching apply: operation is not retryable")
		}
		var discriminator struct {
			SchemaVersion int `json:"schema_version"`
		}
		if err := json.Unmarshal(current.InputSnapshot, &discriminator); err != nil {
			return fmt.Errorf("requeue matching apply: immutable snapshot is invalid")
		}
		var intent matchingApplyIntent
		var snapshotV2 MatchingApplyInputSnapshotV2
		switch discriminator.SchemaVersion {
		case 1:
			decoder := json.NewDecoder(bytes.NewReader(current.InputSnapshot))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&intent); err != nil || decoder.Decode(new(any)) != io.EOF ||
				intent.SchemaVersion != 1 || intent.GroupID == uuid.Nil || intent.ParentRunID == uuid.Nil || intent.EvaluationRevision == "" {
				return fmt.Errorf("requeue matching apply: immutable schema-1 snapshot is invalid")
			}
		case 2:
			var err error
			snapshotV2, err = DecodeMatchingApplyInputSnapshotV2(current.InputSnapshot)
			if err != nil || snapshotV2.Authority.GroupID == uuid.Nil || snapshotV2.Authority.ParentRunID == uuid.Nil || snapshotV2.Authority.EvaluationRevision == "" {
				return fmt.Errorf("requeue matching apply: immutable schema-2 snapshot is invalid")
			}
			intent = matchingApplyIntent{SchemaVersion: 2, GroupID: snapshotV2.Authority.GroupID,
				ParentRunID: snapshotV2.Authority.ParentRunID, EvaluationRevision: snapshotV2.Authority.EvaluationRevision}
		default:
			return fmt.Errorf("requeue matching apply: immutable snapshot schema is unsupported")
		}
		var receipt bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_apply_receipt WHERE apply_operation_id=? AND group_id=?)`, id, intent.GroupID).Scan(ctx, &receipt); err != nil {
			return fmt.Errorf("requeue matching apply: check receipt: %w", err)
		}
		if receipt {
			return fmt.Errorf("requeue matching apply: operation already has a receipt")
		}
		var requested bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_apply_request WHERE group_id=? AND apply_operation_id=? AND parent_run_id=? AND evaluation_revision=?)`, intent.GroupID, id, intent.ParentRunID, intent.EvaluationRevision).Scan(ctx, &requested); err != nil {
			return fmt.Errorf("requeue matching apply: validate request: %w", err)
		}
		if !requested {
			return fmt.Errorf("requeue matching apply: request does not match immutable snapshot")
		}
		if discriminator.SchemaVersion == 2 {
			inputDigest, err := MatchingApplyInputSnapshotV2Digest(snapshotV2)
			if err != nil {
				return fmt.Errorf("requeue matching apply: digest immutable schema-2 snapshot: %w", err)
			}
			var request struct {
				Version       int    `bun:"schema_version"`
				InputSHA      []byte `bun:"input_sha256"`
				Authority     int    `bun:"frozen_authority_version"`
				ParentEpoch   int64  `bun:"parent_execution_epoch"`
				WaitRevision  int64  `bun:"seal_wait_revision"`
				SealDigest    []byte `bun:"seal_digest"`
				TargetCapture []byte `bun:"target_capture_sha256"`
			}
			if err := tx.NewRaw(`SELECT schema_version,input_sha256,frozen_authority_version,parent_execution_epoch,
				seal_wait_revision,seal_digest,target_capture_sha256 FROM matching_apply_request WHERE apply_operation_id=?`, id).Scan(ctx, &request); err != nil {
				return fmt.Errorf("requeue matching apply: read schema-2 request binding: %w", err)
			}
			if request.Version != 2 || request.Authority != snapshotV2.Authority.AuthorityVersion ||
				request.WaitRevision != snapshotV2.Authority.WaitRevision ||
				!equalDigest(request.InputSHA, mustDecodeHexDigest(inputDigest)) ||
				!equalDigest(request.SealDigest, mustDecodeHexDigest(snapshotV2.Authority.SealDigest)) ||
				!equalDigest(request.TargetCapture, mustDecodeHexDigest(snapshotV2.Authority.TargetCaptureDigest)) {
				return fmt.Errorf("requeue matching apply: schema-2 request binding differs from immutable snapshot")
			}
			var parentPublication struct {
				State      string `bun:"state"`
				RunState   string `bun:"run_state"`
				Epoch      int64  `bun:"execution_epoch"`
				CurrentJob *int64 `bun:"current_river_job_id"`
				JobID      *int64 `bun:"river_job_id"`
				Attempt    int    `bun:"attempt"`
			}
			if err := tx.NewRaw(`SELECT operation.state,run.state AS run_state,run.execution_epoch,run.current_river_job_id,
					operation.river_job_id,operation.attempt
					FROM matching_run run JOIN operation ON operation.id=run.operation_id
					WHERE run.id=? AND operation.kind='matching_evaluation_v1' FOR UPDATE OF run,operation`, intent.ParentRunID).Scan(ctx, &parentPublication); err != nil {
				return fmt.Errorf("requeue matching apply: read parent publication authority: %w", err)
			}
			if parentPublication.State != "succeeded" || parentPublication.RunState != "succeeded" || parentPublication.JobID == nil ||
				parentPublication.CurrentJob == nil || *parentPublication.CurrentJob != *parentPublication.JobID || parentPublication.Epoch < 1 ||
				request.ParentEpoch != parentPublication.Epoch {
				return fmt.Errorf("requeue matching apply: parent publication epoch is not authoritative")
			}
			var parentDelivered bool
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_delivery delivery JOIN river_job job ON job.id=delivery.river_job_id
					WHERE delivery.run_id=? AND delivery.river_job_id=? AND delivery.operation_attempt=?
					AND delivery.consumed_at IS NOT NULL AND job.state IN ('completed','cancelled','discarded'))`,
				intent.ParentRunID, *parentPublication.JobID, parentPublication.Attempt).Scan(ctx, &parentDelivered); err != nil {
				return fmt.Errorf("requeue matching apply: verify terminal parent delivery: %w", err)
			}
			if !parentDelivered {
				return fmt.Errorf("requeue matching apply: terminal parent delivery receipt is missing")
			}
			var currentConfiguration string
			err = tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentConfiguration)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("requeue matching apply: read current MusicBrainz configuration: %w", err)
			}
			if currentConfiguration != snapshotV2.Source.RunConfiguration {
				return nil
			}
			var currentSource struct {
				ProviderID            uuid.UUID `bun:"provider_id"`
				Endpoint              string    `bun:"endpoint"`
				ConfigurationIdentity string    `bun:"configuration_identity"`
			}
			err = tx.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=? FOR SHARE`, snapshotV2.Source.ID).Scan(ctx, &currentSource)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("requeue matching apply: read current provider source: %w", err)
			}
			if currentSource.ProviderID != snapshotV2.Source.ProviderID || currentSource.Endpoint != snapshotV2.Source.Endpoint ||
				currentSource.ConfigurationIdentity != snapshotV2.Source.ConfigurationIdentity {
				return nil
			}
			var currentAuthority struct {
				Mode             string     `bun:"mode"`
				Revision         int64      `bun:"revision"`
				CapturedRevision *int64     `bun:"authority_revision"`
				GroupRevision    string     `bun:"group_revision"`
				CapturedGroup    *string    `bun:"captured_group_revision"`
				CurrentRun       *uuid.UUID `bun:"current_run_id"`
				Generation       *int64     `bun:"high_water"`
				TargetGeneration *int64     `bun:"target_generation"`
				TargetRunID      *uuid.UUID `bun:"target_run_id"`
				TargetDigest     []byte     `bun:"target_capture_digest"`
			}
			err = tx.NewRaw(`SELECT COALESCE(authority.mode,'automatic') AS mode,COALESCE(authority.revision,0) AS revision,
				target.authority_revision,group_row.revision AS group_revision,target.captured_group_revision,
				generation.current_run_id,generation.high_water,target.target_generation,target.run_id AS target_run_id,target.target_capture_digest AS target_capture_digest
				FROM incoming_group group_row
				LEFT JOIN incoming_group_matching_authority authority ON authority.group_id=group_row.id
				LEFT JOIN matching_run_target target ON target.run_id=? AND target.group_id=group_row.id
				LEFT JOIN incoming_matching_generation generation ON generation.group_id=group_row.id
				WHERE group_row.id=? FOR UPDATE OF group_row`, intent.ParentRunID, intent.GroupID).Scan(ctx, &currentAuthority)
			if errors.Is(err, sql.ErrNoRows) {
				return nil // A removed or stale target is not safe to re-enqueue.
			}
			if err != nil {
				return fmt.Errorf("requeue matching apply: read current target authority: %w", err)
			}
			if currentAuthority.Mode != "automatic" || currentAuthority.CapturedRevision == nil || currentAuthority.Revision != *currentAuthority.CapturedRevision ||
				currentAuthority.CapturedGroup == nil || currentAuthority.GroupRevision != *currentAuthority.CapturedGroup || currentAuthority.CurrentRun == nil ||
				*currentAuthority.CurrentRun != intent.ParentRunID || currentAuthority.Generation == nil || currentAuthority.TargetGeneration == nil ||
				*currentAuthority.Generation != *currentAuthority.TargetGeneration || currentAuthority.TargetRunID == nil ||
				*currentAuthority.TargetRunID != intent.ParentRunID ||
				!equalDigest(currentAuthority.TargetDigest, mustDecodeHexDigest(snapshotV2.Authority.TargetCaptureDigest)) {
				return nil
			}
			var currentEvaluation struct {
				Revision         string          `bun:"evaluation_revision"`
				Evidence         json.RawMessage `bun:"evaluation_evidence"`
				AuthorityVersion *int            `bun:"frozen_authority_version"`
				FrozenRunID      *uuid.UUID      `bun:"frozen_run_id"`
				WaitRevision     *int64          `bun:"frozen_wait_revision"`
				SealDigest       []byte          `bun:"frozen_seal_digest"`
			}
			err = tx.NewRaw(`SELECT evaluation_revision,evaluation_evidence,frozen_authority_version,frozen_run_id,
				frozen_wait_revision,frozen_seal_digest FROM incoming_match_evaluation WHERE group_id=? FOR UPDATE`, intent.GroupID).Scan(ctx, &currentEvaluation)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("requeue matching apply: current published evaluation is missing")
			}
			if err != nil {
				return fmt.Errorf("requeue matching apply: read current published evaluation: %w", err)
			}
			if currentEvaluation.Revision != snapshotV2.Authority.EvaluationRevision {
				return nil // A newer publication should be reviewed, not applied from this retry.
			}
			if currentEvaluation.AuthorityVersion == nil || *currentEvaluation.AuthorityVersion != snapshotV2.Authority.AuthorityVersion ||
				currentEvaluation.FrozenRunID == nil || *currentEvaluation.FrozenRunID != snapshotV2.Authority.ParentRunID ||
				currentEvaluation.WaitRevision == nil || *currentEvaluation.WaitRevision != snapshotV2.Authority.WaitRevision ||
				!equalDigest(currentEvaluation.SealDigest, mustDecodeHexDigest(snapshotV2.Authority.SealDigest)) {
				return nil
			}
			decision, err := decodePersistedMatchingDecision(currentEvaluation.Evidence)
			if err != nil {
				return fmt.Errorf("requeue matching apply: current published decision is invalid: %w", err)
			}
			expectedDecision := persistedMatchingDecision{Version: 1, GroupID: snapshotV2.Authority.GroupID,
				PolicyVersion: snapshotV2.Authority.PolicyVersion, Outcome: "unique",
				SelectedRelease: &struct {
					ProviderReleaseID  uuid.UUID `json:"provider_release_id"`
					ProviderReleaseKey string    `json:"provider_release_key"`
				}{ProviderReleaseID: snapshotV2.Selected.ProviderReleaseID, ProviderReleaseKey: snapshotV2.Selected.ProviderKey},
				Assignments: make([]struct {
					FileMemberID    uuid.UUID `json:"file_member_id"`
					ProviderTrackID uuid.UUID `json:"provider_track_id"`
					Medium          int       `json:"medium"`
					Position        int       `json:"position"`
				}, 0, len(snapshotV2.Assignments))}
			for _, assignment := range snapshotV2.Assignments {
				expectedDecision.Assignments = append(expectedDecision.Assignments, struct {
					FileMemberID    uuid.UUID `json:"file_member_id"`
					ProviderTrackID uuid.UUID `json:"provider_track_id"`
					Medium          int       `json:"medium"`
					Position        int       `json:"position"`
				}{assignment.FileMemberID, assignment.ProviderTrackID, assignment.Medium + 1, assignment.Position + 1})
			}
			if !matchingJSONEqual(mustJSON(decision), mustJSON(expectedDecision)) {
				return nil // Current publication no longer authorizes this frozen decision.
			}
		}
		inserted, err := client.InsertTx(ctx, tx.Tx, args, options)
		if err != nil {
			return fmt.Errorf("requeue matching apply: insert River job: %w", err)
		}
		now := time.Now().UTC()
		current.State, current.Stage = "queued", "retry:queued"
		current.SafeError, current.StartedAt, current.FinishedAt = nil, nil, nil
		current.RiverJobID = &inserted.Job.ID
		current.ProviderDeliveryClaimedAt = nil
		current.Attempt++
		current.UpdatedAt = now
		if _, err := tx.NewUpdate().Model(current).Column("state", "stage", "safe_error", "started_at", "finished_at", "river_job_id", "provider_delivery_claimed_at", "attempt", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("requeue matching apply: update operation: %w", err)
		}
		operation = current
		return nil
	})
	return operation, err
}

func verifyToolsOperationRootForRetry(ctx context.Context, tx bun.Tx, operation *Operation) error {
	var currentRoot string
	err := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "tools_directory").Scan(ctx, &currentRoot)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("retry operation: read tools directory: %w", err)
	}
	switch operation.Kind {
	case "install":
		var snapshot struct {
			SchemaVersion int    `json:"schema_version"`
			ToolsRoot     string `json:"tools_root"`
		}
		if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil || snapshot.SchemaVersion != 2 || snapshot.ToolsRoot == "" {
			return fmt.Errorf("retry operation: installation snapshot has no historically pinned tools directory")
		}
		if currentRoot != snapshot.ToolsRoot {
			return fmt.Errorf("retry operation: tools directory changed since installation was queued")
		}
	case "move_tools_root":
		var snapshot struct {
			SchemaVersion int    `json:"schema_version"`
			OldRoot       string `json:"old_root"`
			NewRoot       string `json:"new_root"`
		}
		if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil || snapshot.SchemaVersion != 1 || snapshot.OldRoot == "" || snapshot.NewRoot == "" {
			return fmt.Errorf("retry operation: tools-root move snapshot is invalid")
		}
		if currentRoot != snapshot.OldRoot && currentRoot != snapshot.NewRoot {
			return fmt.Errorf("retry operation: tools directory no longer matches the move snapshot")
		}
		if err := validateToolsMoveRootsAgainstOutput(ctx, tx, snapshot.OldRoot, snapshot.NewRoot); err != nil {
			return fmt.Errorf("retry operation: %w", err)
		}
	}
	return nil
}

func (repository *SetupManagerRepository) DismissOperation(ctx context.Context, id uuid.UUID) error {
	result, err := repository.db.NewDelete().Model((*Operation)(nil)).
		Where("id = ?", id).
		Where("state = 'failed'").
		Where("NOT EXISTS (SELECT 1 FROM tools_execution_claim WHERE operation_id = operation.id)").
		Where("NOT EXISTS (SELECT 1 FROM source_analysis_work_execution WHERE operation_id = operation.id)").
		Where("NOT EXISTS (SELECT 1 FROM source_analysis_artifact_cleanup_item WHERE operation_id = operation.id)").
		Where("NOT EXISTS (SELECT 1 FROM publication_source_hold WHERE operation_id = operation.id)").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("dismiss operation: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("only failed operations can be dismissed")
	}
	return nil
}

func (repository *SetupManagerRepository) DeleteSucceededBefore(ctx context.Context, before time.Time) error {
	var cleanupErrors []error
	if err := repository.retireEligibleAutomaticFallbackProofs(ctx, before); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup automatic fallback proofs: %w", err))
	}
	if err := repository.DeleteObsoleteQuiescedMatchingRuns(ctx, before); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup obsolete quiesced matching proofs: %w", err))
	}
	_, err := repository.db.NewDelete().Model((*Operation)(nil)).
		Where("state = 'succeeded'").
		Where("finished_at < ?", before).
		Where("NOT EXISTS (SELECT 1 FROM tools_execution_claim WHERE operation_id = operation.id)").
		Where("NOT EXISTS (SELECT 1 FROM source_analysis_work_execution WHERE operation_id = operation.id)").
		Where("NOT EXISTS (SELECT 1 FROM source_analysis_artifact_cleanup_item WHERE operation_id = operation.id)").
		Where("NOT EXISTS (SELECT 1 FROM publication_source_hold WHERE operation_id = operation.id)").
		Where("NOT EXISTS (SELECT 1 FROM matching_run WHERE operation_id = operation.id)").
		Where("NOT EXISTS (SELECT 1 FROM matching_apply_request WHERE apply_operation_id = operation.id OR parent_run_id IN (SELECT id FROM matching_run WHERE operation_id = operation.id))").
		Where("NOT EXISTS (SELECT 1 FROM matching_provider_dependency WHERE request_id = operation.id)").
		Exec(ctx)
	if err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup succeeded operations: %w", err))
	}
	return errors.Join(cleanupErrors...)
}

// retireEligibleObsoleteQuiescedMatchingRuns is deliberately separate from
// succeeded-operation cleanup: old automatic V2/V3 runs retain their operation
// state until their durable execution and all references have drained.
func (repository *SetupManagerRepository) DeleteObsoleteQuiescedMatchingRuns(ctx context.Context, before time.Time) error {
	var runIDs []uuid.UUID
	if err := repository.db.NewRaw(`SELECT run.id FROM matching_run run
		JOIN operation parent ON parent.id=run.operation_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		WHERE acquisition.intent_version IN (2,3) AND parent.kind='matching_evaluation_v1'
		  AND parent.state IN ('queued','running','failed')
		  AND run.quiesced_at IS NOT NULL
		  AND run.quiesced_at < LEAST(?, transaction_timestamp() - interval '24 hours')
		ORDER BY run.id`, before).Scan(ctx, &runIDs); err != nil {
		return fmt.Errorf("select obsolete quiesced matching runs: %w", err)
	}
	var cleanupErrors []error
	for _, runID := range runIDs {
		if err := repository.retireObsoleteQuiescedMatchingRun(ctx, runID, before); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func (repository *SetupManagerRepository) retireObsoleteQuiescedMatchingRun(ctx context.Context, runID uuid.UUID, before time.Time) error {
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("lock source mutation gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("lock MusicBrainz configuration: %w", err)
		}
		eligible, err := obsoleteQuiescedMatchingRunRetirementEligible(ctx, tx, runID, before)
		if err != nil {
			return fmt.Errorf("check obsolete quiesced matching run %s: %w", runID, err)
		}
		if !eligible {
			return nil
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_run_retirement_authorization(run_id, cleanup_cutoff, authorization_kind)
			VALUES (?, LEAST(?, transaction_timestamp() - interval '24 hours'), 'obsolete_quiesced')`, runID, before).Exec(ctx); err != nil {
			return fmt.Errorf("authorize obsolete quiesced matching run %s: %w", runID, err)
		}
		statements := []struct{ name, sql string }{
			{"apply receipts", `DELETE FROM matching_apply_receipt receipt USING matching_apply_request request WHERE request.parent_run_id=? AND request.apply_operation_id=receipt.apply_operation_id`},
			{"apply requests", `DELETE FROM matching_apply_request WHERE parent_run_id=?`},
			{"fallback search receipts", `DELETE FROM matching_fallback_search_receipt WHERE run_id=?`},
			{"fallback lookup receipts", `DELETE FROM matching_fallback_lookup_receipt WHERE run_id=?`},
			{"fallback evaluation handoffs", `DELETE FROM matching_fallback_evaluation_handoff WHERE run_id=?`},
			{"fallback authority seals", `DELETE FROM matching_fallback_authority_seal WHERE run_id=?`},
			{"fallback calculation receipts", `DELETE FROM matching_fallback_calculation_receipt WHERE run_id=?`},
			{"acquisition phases", `DELETE FROM matching_acquisition_phase WHERE run_id=?`},
			{"fallback request registrations", `DELETE FROM matching_fallback_request_registration WHERE run_id=?`},
			{"provider dependencies", `DELETE FROM matching_provider_dependency WHERE parent_run_id=?`},
			{"acquisition", `DELETE FROM matching_acquisition WHERE run_id=?`},
			{"run targets", `DELETE FROM matching_run_target WHERE run_id=?`},
			{"retirement authorization", `DELETE FROM matching_run_retirement_authorization WHERE run_id=?`},
		}
		for _, statement := range statements {
			if _, err := tx.NewRaw(statement.sql, runID).Exec(ctx); err != nil {
				return fmt.Errorf("delete %s for obsolete matching run %s: %w", statement.name, runID, err)
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM operation WHERE id=(SELECT operation_id FROM matching_run WHERE id=?)`, runID).Exec(ctx); err != nil {
			return fmt.Errorf("delete obsolete matching operation for run %s: %w", runID, err)
		}
		return nil
	})
}

func obsoleteQuiescedMatchingRunRetirementEligible(ctx context.Context, tx bun.Tx, runID uuid.UUID, before time.Time) (bool, error) {
	var eligible bool
	err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM matching_run run
		JOIN operation parent ON parent.id=run.operation_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		WHERE run.id=? AND acquisition.intent_version IN (2,3)
		  AND parent.kind='matching_evaluation_v1' AND parent.state IN ('queued','running','failed')
		  AND run.quiesced_at IS NOT NULL
		  AND run.active_river_job_id IS NULL AND run.active_river_attempt IS NULL AND run.active_execution_epoch IS NULL
		  AND run.quiesced_at < LEAST(?::timestamptz, transaction_timestamp() - interval '24 hours')
		  AND EXISTS (SELECT 1 FROM matching_run_target target
		    JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
		    WHERE target.run_id=run.id AND generation.high_water > target.target_generation
		      AND generation.current_run_id IS DISTINCT FROM run.id)
		  AND NOT EXISTS (SELECT 1 FROM incoming_matching_generation generation WHERE generation.current_run_id=run.id)
		  AND NOT EXISTS (SELECT 1 FROM incoming_match_evaluation evaluation
		    WHERE evaluation.target_run_id=run.id OR evaluation.frozen_run_id=run.id)
		  AND NOT EXISTS (SELECT 1 FROM matching_delivery delivery WHERE delivery.run_id=run.id AND delivery.consumed_at IS NULL)
		  AND NOT EXISTS (SELECT 1 FROM matching_delivery delivery JOIN river_job job ON job.id=delivery.river_job_id
		    WHERE delivery.run_id=run.id AND job.state IN ('available','pending','running','retryable','scheduled'))
		  AND NOT EXISTS (SELECT 1 FROM matching_acquisition_seal seal JOIN river_job job ON job.id=seal.river_job_id
		    WHERE seal.run_id=run.id AND job.state IN ('available','pending','running','retryable','scheduled'))
		  AND NOT EXISTS (SELECT 1 FROM matching_apply_request request LEFT JOIN operation child ON child.id=request.apply_operation_id
		    LEFT JOIN river_job job ON job.id=child.river_job_id WHERE request.parent_run_id=run.id AND
		      (child.id IS NULL OR child.state NOT IN ('succeeded','failed') OR child.finished_at IS NULL
		       OR child.finished_at >= LEAST(?::timestamptz, transaction_timestamp() - interval '24 hours')
		       OR child.state IN ('queued','running') OR job.state IN ('available','pending','running','retryable','scheduled')))
		  AND NOT EXISTS (SELECT 1 FROM matching_provider_dependency dependency LEFT JOIN operation child ON child.id=dependency.request_id
		    LEFT JOIN river_job job ON job.id=child.river_job_id WHERE dependency.parent_run_id=run.id AND
		      (dependency.terminal_outcome IS NULL OR child.id IS NULL OR child.state NOT IN ('succeeded','failed')
		       OR child.finished_at IS NULL OR child.finished_at >= LEAST(?::timestamptz, transaction_timestamp() - interval '24 hours')
		       OR child.state IN ('queued','running') OR job.state IN ('available','pending','running','retryable','scheduled')))
		  AND NOT EXISTS (SELECT 1 FROM river_job job WHERE job.id=parent.river_job_id
		    AND job.state IN ('available','pending','running','retryable','scheduled'))
		  AND NOT EXISTS (SELECT 1 FROM river_job job WHERE job.id=run.current_river_job_id
		    AND job.state IN ('available','pending','running','retryable','scheduled'))
	)`, runID, before, before, before).Scan(ctx, &eligible)
	return eligible, err
}

// retireEligibleAutomaticFallbackProofs removes only expired, successfully
// completed automatic fallback proof graphs. Manual admissions and live/current
// evidence remain untouched; the database authorization trigger independently
// validates each selected run before any immutable receipt is removed.
func (repository *SetupManagerRepository) retireEligibleAutomaticFallbackProofs(ctx context.Context, before time.Time) error {
	var runIDs []uuid.UUID
	if err := repository.db.NewRaw(`SELECT run.id FROM matching_run run
		JOIN operation parent ON parent.id=run.operation_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id AND acquisition.intent_version=3
		WHERE parent.kind='matching_evaluation_v1' AND parent.state='succeeded'
		  AND parent.finished_at < LEAST(?, transaction_timestamp() - interval '24 hours') AND run.state='succeeded'
		ORDER BY run.id`, before).Scan(ctx, &runIDs); err != nil {
		return fmt.Errorf("select expired fallback runs: %w", err)
	}
	var cleanupErrors []error
	for _, runID := range runIDs {
		err := repository.retireAutomaticFallbackProof(ctx, runID, before)
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

// Each run has an independent transaction: a hold or database failure on one
// graph cannot roll back retirement of another eligible graph.
func (repository *SetupManagerRepository) retireAutomaticFallbackProof(ctx context.Context, runID uuid.UUID, before time.Time) error {
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("lock source mutation gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("lock MusicBrainz configuration: %w", err)
		}
		eligible, err := fallbackRunRetirementEligible(ctx, tx, runID, before)
		if err != nil {
			return fmt.Errorf("check fallback run %s retirement eligibility: %w", runID, err)
		}
		if !eligible {
			return nil
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_run_retirement_authorization(run_id, cleanup_cutoff)
			VALUES (?, LEAST(?, transaction_timestamp() - interval '24 hours'))`, runID, before).Exec(ctx); err != nil {
			return fmt.Errorf("authorize fallback run %s retirement: %w", runID, err)
		}
		statements := []struct {
			name string
			sql  string
		}{
			{"apply receipts", `DELETE FROM matching_apply_receipt receipt USING matching_apply_request request WHERE request.parent_run_id=? AND request.apply_operation_id=receipt.apply_operation_id`},
			{"apply requests", `DELETE FROM matching_apply_request WHERE parent_run_id=?`},
			{"fallback search receipts", `DELETE FROM matching_fallback_search_receipt WHERE run_id=?`},
			{"fallback lookup receipts", `DELETE FROM matching_fallback_lookup_receipt WHERE run_id=?`},
			{"fallback evaluation handoffs", `DELETE FROM matching_fallback_evaluation_handoff WHERE run_id=?`},
			{"fallback authority seals", `DELETE FROM matching_fallback_authority_seal WHERE run_id=?`},
			{"fallback calculation receipts", `DELETE FROM matching_fallback_calculation_receipt WHERE run_id=?`},
			{"fallback acquisition phases", `DELETE FROM matching_acquisition_phase WHERE run_id=?`},
			{"fallback request registrations", `DELETE FROM matching_fallback_request_registration WHERE run_id=?`},
			{"provider dependencies", `DELETE FROM matching_provider_dependency WHERE parent_run_id=?`},
			{"fallback acquisition", `DELETE FROM matching_acquisition WHERE run_id=?`},
			{"matching run targets", `DELETE FROM matching_run_target WHERE run_id=?`},
			{"retirement authorization", `DELETE FROM matching_run_retirement_authorization WHERE run_id=?`},
		}
		for _, statement := range statements {
			if _, err := tx.NewRaw(statement.sql, runID).Exec(ctx); err != nil {
				return fmt.Errorf("delete %s for fallback run %s: %w", statement.name, runID, err)
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM operation WHERE id=(SELECT operation_id FROM matching_run WHERE id=?)`, runID).Exec(ctx); err != nil {
			return fmt.Errorf("delete retired fallback parent operation %s: %w", runID, err)
		}
		return nil
	})
}

func fallbackRunRetirementEligible(ctx context.Context, tx bun.Tx, runID uuid.UUID, before time.Time) (bool, error) {
	var eligible bool
	err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM matching_run run
		JOIN operation parent ON parent.id=run.operation_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id AND acquisition.intent_version=3
		WHERE run.id=? AND parent.kind='matching_evaluation_v1' AND parent.state='succeeded'
		  AND parent.finished_at < LEAST(?::timestamptz, transaction_timestamp() - interval '24 hours')
		  AND run.state='succeeded'
		  AND NOT EXISTS (SELECT 1 FROM incoming_matching_generation generation WHERE generation.current_run_id=run.id)
		  AND NOT EXISTS (SELECT 1 FROM matching_run_target target JOIN incoming_match_evaluation evaluation
		    ON evaluation.target_run_id=run.id OR evaluation.frozen_run_id=run.id WHERE target.run_id=run.id)
		  AND NOT EXISTS (SELECT 1 FROM matching_delivery delivery WHERE delivery.run_id=run.id AND delivery.consumed_at IS NULL)
		  AND NOT EXISTS (SELECT 1 FROM matching_apply_request request JOIN operation child ON child.id=request.apply_operation_id
		    LEFT JOIN river_job job ON job.id=child.river_job_id WHERE request.parent_run_id=run.id AND
			      (child.state <> 'succeeded' OR child.finished_at IS NULL OR child.finished_at >= LEAST(?::timestamptz, transaction_timestamp() - interval '24 hours')
		       OR job.state IN ('available','pending','running','retryable','scheduled')))
		  AND NOT EXISTS (SELECT 1 FROM matching_provider_dependency dependency JOIN operation child ON child.id=dependency.request_id
		    LEFT JOIN river_job job ON job.id=child.river_job_id WHERE dependency.parent_run_id=run.id AND
		      (dependency.terminal_outcome IS NULL OR child.state NOT IN ('succeeded','failed') OR child.finished_at IS NULL
			       OR child.finished_at >= LEAST(?::timestamptz, transaction_timestamp() - interval '24 hours')
		       OR child.state IN ('queued','running') OR job.state IN ('available','pending','running','retryable','scheduled')))
		  AND NOT EXISTS (SELECT 1 FROM river_job job WHERE job.id=parent.river_job_id
		    AND job.state IN ('available','pending','running','retryable','scheduled'))
	)`, runID, before, before, before).Scan(ctx, &eligible)
	return eligible, err
}
