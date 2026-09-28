// Package persistence owns PostgreSQL access for setup-manager state.
package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		return fmt.Errorf("create tool installation: %w", err)
	}
	return nil
}

func (repository *SetupManagerRepository) CreateOperation(ctx context.Context, operation *Operation) error {
	return repository.CreateOperationWith(ctx, repository.db, operation)
}

func (repository *SetupManagerRepository) CreateOperationWith(ctx context.Context, database bun.IDB, operation *Operation) error {
	if operation.Attempt == 0 {
		operation.Attempt = 1
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
	_, err := repository.db.NewUpdate().Model(installation).
		Column("state", "relative_path", "executable_versions", "artifact_identities", "verified_at", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("update tool installation: %w", err)
	}
	return nil
}

func (repository *SetupManagerRepository) MarkInstallationReady(ctx context.Context, id uuid.UUID, versions json.RawMessage, verifiedAt time.Time) error {
	_, err := repository.db.NewUpdate().Model((*ToolInstallation)(nil)).
		Set("state = 'ready'").Set("executable_versions = ?", versions).Set("verified_at = ?", verifiedAt).
		Set("updated_at = ?", verifiedAt).Where("id = ?", id).Where("state = 'preparing'").Exec(ctx)
	if err != nil {
		return fmt.Errorf("mark tool installation ready: %w", err)
	}
	return nil
}

func (repository *SetupManagerRepository) MarkInstallationFailed(ctx context.Context, id uuid.UUID) error {
	_, err := repository.db.NewUpdate().Model((*ToolInstallation)(nil)).Set("state = 'failed'").Set("updated_at = now()").Where("id = ?", id).Where("state = 'preparing'").Exec(ctx)
	if err != nil {
		return fmt.Errorf("mark tool installation failed: %w", err)
	}
	return nil
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

func (repository *SetupManagerRepository) ListActiveOperationConflictsForUpdate(ctx context.Context, tx bun.Tx, targetIdentity string) ([]Operation, error) {
	operations := make([]Operation, 0)
	if err := tx.NewSelect().Model(&operations).Where("state IN ('queued', 'running')").
		Where("kind = 'move_tools_root' OR input_snapshot ->> 'target_identity' = ?", targetIdentity).For("UPDATE").Scan(ctx); err != nil {
		return nil, fmt.Errorf("list active operation conflicts: %w", err)
	}
	return operations, nil
}

func (repository *SetupManagerRepository) UpdateOperation(ctx context.Context, operation *Operation) error {
	_, err := repository.db.NewUpdate().Model(operation).Column("state", "stage", "bytes_completed", "bytes_total", "safe_error", "river_job_id", "attempt", "started_at", "finished_at", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("update operation: %w", err)
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
