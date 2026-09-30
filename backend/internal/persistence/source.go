package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type SourceRoot struct {
	bun.BaseModel `bun:"table:source_root"`

	ID                     uuid.UUID  `bun:"id,pk,type:uuid"`
	ConfiguredPath         string     `bun:"configured_path"`
	DisplayName            string     `bun:"display_name"`
	Enabled                bool       `bun:"enabled,notnull"`
	ScanGeneration         int64      `bun:"scan_generation"`
	InventoryPath          *string    `bun:"inventory_path,nullzero"`
	LastSuccessfulScanAt   *time.Time `bun:"last_successful_scan_at,nullzero"`
	LastAppliedOperationID *uuid.UUID `bun:"last_applied_operation_id,type:uuid,nullzero"`
	Status                 string     `bun:"status"`
	SafeError              *string    `bun:"safe_error,nullzero"`
	CreatedAt              time.Time  `bun:"created_at,nullzero"`
	UpdatedAt              time.Time  `bun:"updated_at,nullzero"`
}

type SourceLocation struct {
	bun.BaseModel `bun:"table:source_location"`

	ID                     uuid.UUID `bun:"id,pk,type:uuid"`
	SourceRootID           uuid.UUID `bun:"source_root_id,type:uuid"`
	RelativePath           string    `bun:"relative_path"`
	SizeBytes              int64     `bun:"size_bytes"`
	Mtime                  time.Time `bun:"mtime,nullzero"`
	LastSeenScanGeneration int64     `bun:"last_seen_scan_generation"`
	ProbeStatus            string    `bun:"probe_status"`
	SafeError              *string   `bun:"safe_error,nullzero"`
	CreatedAt              time.Time `bun:"created_at,nullzero"`
	UpdatedAt              time.Time `bun:"updated_at,nullzero"`
}

type SourceScanCandidate struct {
	bun.BaseModel `bun:"table:source_scan_candidate"`

	ID           uuid.UUID `bun:"id,pk,type:uuid"`
	OperationID  uuid.UUID `bun:"operation_id,type:uuid"`
	RelativePath string    `bun:"relative_path"`
	SizeBytes    int64     `bun:"size_bytes"`
	Mtime        time.Time `bun:"mtime,nullzero"`
	ProbeStatus  string    `bun:"probe_status"`
	SafeError    *string   `bun:"safe_error,nullzero"`
	CreatedAt    time.Time `bun:"created_at,nullzero"`
}
