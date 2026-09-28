// Package persistence owns PostgreSQL access for setup-manager state.
package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
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
	CreatedAt          time.Time       `bun:"created_at,nullzero"`
	VerifiedAt         *time.Time      `bun:"verified_at,nullzero"`
	UpdatedAt          time.Time       `bun:"updated_at,nullzero"`
}

type Operation struct {
	bun.BaseModel `bun:"table:operation"`

	ID             uuid.UUID       `bun:"id,pk,type:uuid"`
	Kind           string          `bun:"kind"`
	State          string          `bun:"state"`
	Stage          string          `bun:"stage"`
	InputSnapshot  json.RawMessage `bun:"input_snapshot,type:jsonb"`
	BytesCompleted int64           `bun:"bytes_completed"`
	BytesTotal     *int64          `bun:"bytes_total,nullzero"`
	SafeError      *string         `bun:"safe_error,nullzero"`
	RiverJobID     *int64          `bun:"river_job_id,nullzero"`
	CreatedAt      time.Time       `bun:"created_at,nullzero"`
	StartedAt      *time.Time      `bun:"started_at,nullzero"`
	FinishedAt     *time.Time      `bun:"finished_at,nullzero"`
	UpdatedAt      time.Time       `bun:"updated_at,nullzero"`
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
	if _, err := database.NewInsert().Model(operation).Exec(ctx); err != nil {
		return fmt.Errorf("create operation: %w", err)
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

func (repository *SetupManagerRepository) UpdateOperation(ctx context.Context, operation *Operation) error {
	_, err := repository.db.NewUpdate().Model(operation).Column("state", "stage", "bytes_completed", "bytes_total", "safe_error", "started_at", "finished_at", "updated_at").WherePK().Exec(ctx)
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
