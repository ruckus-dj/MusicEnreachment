// Package persistence owns PostgreSQL access for setup-manager state.
package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	Attempt              int             `bun:"attempt"`
	BytesCompleted       int64           `bun:"bytes_completed"`
	BytesTotal           *int64          `bun:"bytes_total,nullzero"`
	SafeError            *string         `bun:"safe_error,nullzero"`
	RiverJobID           *int64          `bun:"river_job_id,nullzero"`
	CreatedAt            time.Time       `bun:"created_at,nullzero"`
	StartedAt            *time.Time      `bun:"started_at,nullzero"`
	FinishedAt           *time.Time      `bun:"finished_at,nullzero"`
	UpdatedAt            time.Time       `bun:"updated_at,nullzero"`
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
		if _, err := database.NewRaw("SELECT pg_advisory_xact_lock(hashtext(?))", "setup-operation-exclusivity").Exec(ctx); err != nil {
			return fmt.Errorf("lock operation exclusivity: %w", err)
		}
	}
	if _, err := database.NewInsert().Model(operation).Exec(ctx); err != nil {
		return fmt.Errorf("create operation: %w", err)
	}
	return nil
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

func (repository *SetupManagerRepository) CreateInstallationOperationAndEnqueue(ctx context.Context, installation *ToolInstallation, operation *Operation, client RiverInserter, args river.JobArgs, options *river.InsertOpts) error {
	if client == nil {
		return fmt.Errorf("enqueue installation: River client is required")
	}
	if installation == nil || operation == nil || operation.TargetInstallationID == nil || *operation.TargetInstallationID != installation.ID {
		return fmt.Errorf("enqueue installation: operation target must match installation")
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
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
		if _, err := tx.NewRaw("SELECT pg_advisory_xact_lock(hashtext(?))", "setup-operation-exclusivity").Exec(ctx); err != nil {
			return fmt.Errorf("lock operation exclusivity: %w", err)
		}
		var moveID uuid.UUID
		err := tx.NewRaw("SELECT id FROM operation WHERE kind = 'move_tools_root' AND state IN ('queued', 'running') LIMIT 1 FOR UPDATE").Scan(ctx, &moveID)
		if err == nil {
			return fmt.Errorf("cannot activate installation during an active tools root move")
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check active tools root move: %w", err)
		}
		return repository.activateInstallationTx(ctx, tx, id, packageKind, goos, goarch, activeSetting)
	})
}

func (repository *SetupManagerRepository) ActivateInstallationDuringSetup(ctx context.Context, id uuid.UUID, packageKind, goos, goarch, activeSetting string) (bool, error) {
	activated := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "setup-completion"); err != nil {
			return fmt.Errorf("lock setup completion: %w", err)
		}
		var completedAt string
		err := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "setup_completed_at").Scan(ctx, &completedAt)
		if err == nil {
			return nil
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("read setup completion: %w", err)
		}
		if err := repository.activateInstallationTx(ctx, tx, id, packageKind, goos, goarch, activeSetting); err != nil {
			return err
		}
		activated = true
		return nil
	})
	return activated, err
}

func (repository *SetupManagerRepository) activateInstallationTx(ctx context.Context, tx bun.Tx, id uuid.UUID, packageKind, goos, goarch, activeSetting string) error {
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "active-installation:"+packageKind); err != nil {
		return fmt.Errorf("lock active installation: %w", err)
	}
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

func (repository *SetupManagerRepository) DeleteInstallation(ctx context.Context, id uuid.UUID, packageKind, goos, goarch, activeSetting string, removeFiles func(*ToolInstallation) error) error {
	if removeFiles == nil {
		return fmt.Errorf("delete installation: filesystem remover is required")
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return fmt.Errorf("lock active operations for installation deletion: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "active-installation:"+packageKind); err != nil {
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
		if err := removeFiles(installation); err != nil {
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
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if operation.Kind != "move_tools_root" || operation.State != "running" {
			return fmt.Errorf("tools root move operation is not running")
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
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if operation.Kind != "move_tools_root" || operation.State != "running" ||
			(operation.Stage != "switched" && operation.Stage != "rollback_pending") {
			return fmt.Errorf("tools root move is not ready for rollback")
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
		Where("kind = 'move_tools_root' OR target_installation_id = ?", targetInstallationID).For("UPDATE").Scan(ctx); err != nil {
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
// The callback runs while the row is locked and is never invoked for a missing
// operation.
func (repository *SetupManagerRepository) TransitionOperation(ctx context.Context, id uuid.UUID, transition func(*Operation) error) error {
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		operation, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := transition(operation); err != nil {
			return err
		}
		operation.UpdatedAt = time.Now().UTC()
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "bytes_completed", "bytes_total", "safe_error", "river_job_id", "attempt", "started_at", "finished_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("transition operation: %w", err)
		}
		return nil
	})
}

// RetryOperationAndEnqueue locks the failed operation, preserves its immutable
// target installation and snapshot, and atomically creates its next River job.
func (repository *SetupManagerRepository) RetryOperationAndEnqueue(ctx context.Context, id uuid.UUID, client RiverInserter, args river.JobArgs, options *river.InsertOpts) (*Operation, error) {
	if client == nil {
		return nil, fmt.Errorf("retry operation: River client is required")
	}
	var operation *Operation
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw("SELECT pg_advisory_xact_lock(hashtext(?))", "setup-operation-exclusivity").Exec(ctx); err != nil {
			return fmt.Errorf("lock operation exclusivity: %w", err)
		}
		locked, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if locked.State != "failed" {
			return fmt.Errorf("only failed operations can be retried")
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
