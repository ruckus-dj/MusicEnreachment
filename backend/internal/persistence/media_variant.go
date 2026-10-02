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
