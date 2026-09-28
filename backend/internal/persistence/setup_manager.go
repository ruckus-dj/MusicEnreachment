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
