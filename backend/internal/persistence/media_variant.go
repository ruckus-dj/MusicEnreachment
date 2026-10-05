package persistence

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// MediaVariant is the minimal saved result of one successful technical analysis
// of a source file. The container and every audio stream stay in FFProbeJSON and
// are projected by the typed service read-model, so this row stores the raw
// ffprobe output and its provenance instead of duplicating parameters into
// columns that have no query consumer yet. A variant is immutable: a new
// analysis inserts a new row, and quality/fingerprint/digest values are absent
// on purpose in this slice.
type MediaVariant struct {
	bun.BaseModel `bun:"table:media_variant"`

	ID                    uuid.UUID       `bun:"id,pk,type:uuid"`
	SizeBytes             int64           `bun:"size_bytes"`
	AnalysisPolicyVersion int             `bun:"analysis_policy_version"`
	FFProbeVersion        string          `bun:"ffprobe_version"`
	FFProbeJSON           json.RawMessage `bun:"ffprobe_json,type:jsonb"`
	ObservedTags          json.RawMessage `bun:"observed_tags,type:jsonb"`
	InspectedAt           time.Time       `bun:"inspected_at,nullzero"`
	// AppliedOperationID is the operation whose result this variant is. It is a
	// plain uuid without a foreign key, mirroring source_root.last_applied_operation_id:
	// deleting the succeeded operation later must neither delete the durable
	// result nor be blocked by it.
	AppliedOperationID uuid.UUID `bun:"applied_operation_id,type:uuid"`
	CreatedAt          time.Time `bun:"created_at,nullzero"`
}

// SourceMediaVariant is the nullable, normalized representation used by the
// automatic-analysis contract. MediaVariant projects only a present technical
// result; this representation also permits an independently saved SHA identity.
type SourceMediaVariant struct {
	bun.BaseModel `bun:"table:media_variant"`

	ID                       uuid.UUID       `bun:"id,pk,type:uuid"`
	SizeBytes                int64           `bun:"size_bytes"`
	AnalysisPolicyVersion    *int            `bun:"analysis_policy_version,nullzero"`
	FFProbeVersion           *string         `bun:"ffprobe_version,nullzero"`
	FFProbeJSON              json.RawMessage `bun:"ffprobe_json,type:jsonb,nullzero"`
	ObservedTags             json.RawMessage `bun:"observed_tags,type:jsonb,nullzero"`
	InspectedAt              *time.Time      `bun:"inspected_at,nullzero"`
	AppliedOperationID       *uuid.UUID      `bun:"applied_operation_id,type:uuid,nullzero"`
	SourceSHA256             []byte          `bun:"source_sha256,nullzero"`
	SHA256CalculatedAt       *time.Time      `bun:"sha256_calculated_at,nullzero"`
	SHA256Algorithm          *string         `bun:"sha256_algorithm,nullzero"`
	SHA256AppliedOperationID *uuid.UUID      `bun:"sha256_applied_operation_id,type:uuid,nullzero"`
	AudioStreamCount         *int            `bun:"audio_stream_count,nullzero"`
	CreatedAt                time.Time       `bun:"created_at,nullzero"`
}

type SourceFingerprintResult struct {
	bun.BaseModel         `bun:"table:media_fingerprint_result"`
	ID                    uuid.UUID `bun:"id,pk,type:uuid"`
	FPCalcVersion         string    `bun:"fpcalc_version"`
	VersionBanner         string    `bun:"version_banner"`
	AlgorithmNamespace    string    `bun:"algorithm_namespace"`
	AlgorithmID           int16     `bun:"algorithm_id"`
	Fingerprint           string    `bun:"fingerprint"`
	ReportedDuration      float64   `bun:"reported_duration"`
	CalculatedAt          time.Time `bun:"calculated_at"`
	AppliedOperationID    uuid.UUID `bun:"applied_operation_id,type:uuid"`
	ParserContractVersion int       `bun:"parser_contract_version"`
}

type SourceAnalysisWork struct {
	bun.BaseModel         `bun:"table:source_analysis_work"`
	ID                    uuid.UUID `bun:"id,pk,type:uuid"`
	LocationID            uuid.UUID `bun:"location_id,type:uuid"`
	SourceRootID          uuid.UUID `bun:"source_root_id,type:uuid"`
	ConfiguredPath        string    `bun:"configured_path"`
	InventoryPath         string    `bun:"inventory_path"`
	RelativePath          string    `bun:"relative_path"`
	SizeBytes             int64     `bun:"size_bytes"`
	Mtime                 time.Time `bun:"mtime"`
	SHA256Enabled         bool      `bun:"sha256_enabled"`
	OriginScanOperationID uuid.UUID `bun:"origin_scan_operation_id,type:uuid"`
	CreatedAt             time.Time `bun:"created_at,nullzero"`
}

type SourceAnalysisStep struct {
	bun.BaseModel              `bun:"table:source_analysis_step"`
	WorkID                     uuid.UUID  `bun:"work_id,pk,type:uuid"`
	Step                       string     `bun:"step,pk"`
	State                      string     `bun:"state"`
	StepAttempt                int        `bun:"step_attempt"`
	SafeError                  *string    `bun:"safe_error,nullzero"`
	SkipReason                 *string    `bun:"skip_reason,nullzero"`
	UpdatedAt                  time.Time  `bun:"updated_at,nullzero"`
	ExecutionOperationID       *uuid.UUID `bun:"execution_operation_id,type:uuid,nullzero"`
	ExecutionOperationAttempt  *int       `bun:"execution_operation_attempt,nullzero"`
	ExecutionJobID             *int64     `bun:"execution_job_id,nullzero"`
	LastOperationID            *uuid.UUID `bun:"last_operation_id,type:uuid,nullzero"`
	SuccessSHAVariantID        *uuid.UUID `bun:"success_sha_variant_id,type:uuid,nullzero"`
	SuccessProbeVariantID      *uuid.UUID `bun:"success_probe_variant_id,type:uuid,nullzero"`
	SuccessFingerprintResultID *uuid.UUID `bun:"success_fingerprint_result_id,type:uuid,nullzero"`
	SuccessReuseOrigin         *string    `bun:"success_reuse_origin,nullzero"`
}
