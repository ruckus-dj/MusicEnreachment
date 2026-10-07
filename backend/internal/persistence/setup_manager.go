// Package persistence owns PostgreSQL access for setup-manager state.
package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	SourceAnalysisMode string `bun:"source_analysis_mode,nullzero"`
	// TargetWorkID and TargetStep are both present only for a single-step
	// operation; batch operations select their work items in the snapshot.
	TargetWorkID      *uuid.UUID `bun:"target_work_id,type:uuid,nullzero"`
	TargetStep        *string    `bun:"target_step,nullzero"`
	ToolsReadRequired bool       `bun:"tools_read_required"`
	RerunTarget       bool       `bun:"rerun_target"`
	Attempt           int        `bun:"attempt"`
	BytesCompleted    int64      `bun:"bytes_completed"`
	BytesTotal        *int64     `bun:"bytes_total,nullzero"`
	SafeError         *string    `bun:"safe_error,nullzero"`
	RiverJobID        *int64     `bun:"river_job_id,nullzero"`
	CreatedAt         time.Time  `bun:"created_at,nullzero"`
	StartedAt         *time.Time `bun:"started_at,nullzero"`
	FinishedAt        *time.Time `bun:"finished_at,nullzero"`
	UpdatedAt         time.Time  `bun:"updated_at,nullzero"`
}

type SetupManagerRepository struct {
	db *bun.DB
}

func NewSetupManagerRepository(db *bun.DB) *SetupManagerRepository {
	return &SetupManagerRepository{db: db}
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
		result, err := client.InsertTx(ctx, tx.Tx, args, options)
		if err != nil {
			return fmt.Errorf("insert River job: %w", err)
		}
		operation.RiverJobID = &result.Job.ID
		if err := repository.CreateOperationWith(ctx, tx, operation); err != nil {
			return err
		}
		return nil
	})
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
	return count, nil
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

// TransitionOperationForDelivery applies an installation-worker transition
// only while the operation still names that River job and attempt.
func (repository *SetupManagerRepository) TransitionOperationForDelivery(ctx context.Context, id uuid.UUID, attempt int, riverJobID int64, transition func(*Operation) error) (bool, error) {
	changed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		operation, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if operation.Kind != "install" || operation.Attempt != attempt || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID {
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
		if _, err := tx.NewUpdate().Model(locked).Column("state", "stage", "bytes_completed", "bytes_total", "safe_error", "river_job_id", "attempt", "started_at", "finished_at", "updated_at").WherePK().Exec(ctx); err != nil {
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
	result, err := repository.db.NewDelete().Model((*Operation)(nil)).Where("id = ?", id).Where("state = 'failed'").Exec(ctx)
	if err != nil {
		return fmt.Errorf("dismiss operation: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("only failed operations can be dismissed")
	}
	return nil
}

func (repository *SetupManagerRepository) DeleteSucceededBefore(ctx context.Context, before time.Time) error {
	_, err := repository.db.NewDelete().Model((*Operation)(nil)).Where("state = 'succeeded'").Where("finished_at < ?", before).Exec(ctx)
	if err != nil {
		return fmt.Errorf("cleanup succeeded operations: %w", err)
	}
	return nil
}
