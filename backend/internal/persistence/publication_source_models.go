package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationSourceHold pins one inventory snapshot while a publication
// operation borrows its source. It is live ownership state, not history.
type PublicationSourceHold struct {
	bun.BaseModel      `bun:"table:publication_source_hold"`
	OperationID        uuid.UUID `bun:"operation_id,pk,type:uuid"`
	ApplicationAttempt int       `bun:"application_attempt"`
	RiverJobID         int64     `bun:"river_job_id"`
	Epoch              uuid.UUID `bun:"epoch,type:uuid"`
	HoldUUID           uuid.UUID `bun:"hold_uuid,type:uuid"`
	SourceRootID       uuid.UUID `bun:"source_root_id,type:uuid"`
	SourceLocationID   uuid.UUID `bun:"source_location_id,type:uuid"`
	WorkID             uuid.UUID `bun:"work_id,type:uuid"`
	MediaVariantID     uuid.UUID `bun:"media_variant_id,type:uuid"`
	ConfiguredPath     string    `bun:"configured_path"`
	InventoryPath      string    `bun:"inventory_path"`
	RelativePath       string    `bun:"relative_path"`
	SizeBytes          int64     `bun:"size_bytes"`
	Mtime              time.Time `bun:"mtime"`
}
