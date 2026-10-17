package persistence

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationNamespaceState stores the current logical output namespace epoch.
// This schema slice is inert; publication workflows do not consume it yet.
type PublicationNamespaceState struct {
	bun.BaseModel `bun:"table:publication_namespace_state"`
	Singleton     bool      `bun:"singleton,pk"`
	OutputEpoch   uuid.UUID `bun:"output_epoch,type:uuid"`
}

// TrackPublication records actual, validated output for one release position.
// It deliberately contains no historical source identifiers or source paths.
type TrackPublication struct {
	bun.BaseModel        `bun:"table:track_publication"`
	PositionID           uuid.UUID       `bun:"position_id,pk,type:uuid"`
	Epoch                uuid.UUID       `bun:"epoch,type:uuid"`
	Generation           uuid.UUID       `bun:"generation,type:uuid"`
	RelativePath         string          `bun:"relative_path"`
	Ownership            json.RawMessage `bun:"ownership,type:jsonb"`
	ActualAudio          json.RawMessage `bun:"actual_audio,type:jsonb"`
	ActualMetadata       json.RawMessage `bun:"actual_metadata,type:jsonb"`
	ActualAssets         json.RawMessage `bun:"actual_assets,type:jsonb"`
	QualityPolicyVersion int             `bun:"quality_policy_version"`
	ValidatedAt          time.Time       `bun:"validated_at"`
}

// TrackPublicationAttempt is the single unfinished publication attempt for a
// position. Frozen inputs and journals are durable recovery state, not history.
type TrackPublicationAttempt struct {
	bun.BaseModel       `bun:"table:track_publication_attempt"`
	OperationID         uuid.UUID       `bun:"operation_id,pk,type:uuid"`
	PositionID          uuid.UUID       `bun:"position_id,type:uuid"`
	Epoch               uuid.UUID       `bun:"epoch,type:uuid"`
	ExpectedGeneration  *uuid.UUID      `bun:"expected_generation,type:uuid,nullzero"`
	CandidateGeneration uuid.UUID       `bun:"candidate_generation,type:uuid"`
	IntentKind          string          `bun:"intent_kind"`
	Phase               string          `bun:"phase"`
	FrozenInputs        json.RawMessage `bun:"frozen_inputs,type:jsonb,nullzero"`
	NamespaceManifest   json.RawMessage `bun:"namespace_manifest,type:jsonb"`
	MutationJournal     json.RawMessage `bun:"mutation_journal,type:jsonb"`
}

// PublicationExecutionClaim fences one durable operation delivery.
type PublicationExecutionClaim struct {
	bun.BaseModel      `bun:"table:publication_execution_claim"`
	OperationID        uuid.UUID `bun:"operation_id,pk,type:uuid"`
	ApplicationAttempt int       `bun:"application_attempt,pk"`
	RiverJobID         int64     `bun:"river_job_id"`
	RiverAttempt       int       `bun:"river_attempt"`
	ClaimUUID          uuid.UUID `bun:"claim_uuid,type:uuid"`
	BootUUID           uuid.UUID `bun:"boot_uuid,type:uuid"`
	DeliveryToken      string    `bun:"delivery_token"`
	Epoch              uuid.UUID `bun:"epoch,type:uuid"`
	State              string    `bun:"state"`
}

// PublicationNamespaceClaim reserves a canonical output key in an epoch.
type PublicationNamespaceClaim struct {
	bun.BaseModel     `bun:"table:publication_namespace_claim"`
	ID                uuid.UUID  `bun:"id,pk,type:uuid"`
	Epoch             uuid.UUID  `bun:"epoch,type:uuid"`
	KeyDigest         []byte     `bun:"key_digest"`
	CanonicalKey      string     `bun:"canonical_key"`
	RelativePath      string     `bun:"relative_path"`
	NodeKind          string     `bun:"node_kind"`
	OwnerPositionID   *uuid.UUID `bun:"owner_position_id,type:uuid,nullzero"`
	DirectoryScopeKey *string    `bun:"directory_scope_key,nullzero"`
}

// PublicationNamespaceReservation ties one owner generation to a claimed key.
type PublicationNamespaceReservation struct {
	bun.BaseModel    `bun:"table:publication_namespace_reservation"`
	NamespaceClaimID uuid.UUID `bun:"namespace_claim_id,pk,type:uuid"`
	PositionID       uuid.UUID `bun:"position_id,pk,type:uuid"`
	Generation       uuid.UUID `bun:"generation,pk,type:uuid"`
}

// SourceCleanupItem is a live cleanup decision, not a historical source log.
type SourceCleanupItem struct {
	bun.BaseModel         `bun:"table:source_cleanup_item"`
	ID                    uuid.UUID       `bun:"id,pk,type:uuid"`
	OperationID           uuid.UUID       `bun:"operation_id,type:uuid"`
	PositionID            uuid.UUID       `bun:"position_id,type:uuid"`
	Epoch                 uuid.UUID       `bun:"epoch,type:uuid"`
	PublicationGeneration uuid.UUID       `bun:"publication_generation,type:uuid"`
	State                 string          `bun:"state"`
	SourceSnapshot        json.RawMessage `bun:"source_snapshot,type:jsonb,nullzero"`
	BorrowerRevision      *uuid.UUID      `bun:"borrower_revision,type:uuid,nullzero"`
	SafeError             *string         `bun:"safe_error,nullzero"`
}
