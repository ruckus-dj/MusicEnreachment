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
	InitialLyrics        json.RawMessage `bun:"initial_lyrics,type:jsonb,nullzero"`
	CoverEmbedding       json.RawMessage `bun:"cover_embedding,type:jsonb,nullzero"`
	QualityPolicyVersion int             `bun:"quality_policy_version"`
	ValidatedAt          time.Time       `bun:"validated_at"`
}

// PositionSourceAuthority is current authority evidence; nil means unknown,
// including for publications predating this model.
type PositionSourceAuthority struct {
	bun.BaseModel         `bun:"table:position_source_authority"`
	PositionID            uuid.UUID `bun:"position_id,pk,type:uuid"`
	Mode                  string    `bun:"mode"`
	Revision              uuid.UUID `bun:"revision,type:uuid"`
	PublicationEpoch      uuid.UUID `bun:"publication_epoch,type:uuid"`
	PublicationGeneration uuid.UUID `bun:"publication_generation,type:uuid"`
	SourceLocationID      uuid.UUID `bun:"source_location_id,type:uuid"`
	MediaVariantID        uuid.UUID `bun:"media_variant_id,type:uuid"`
	UpdatedAt             time.Time `bun:"updated_at"`
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

// ObsoletePublicationOutputCleanup is live authority for removing one exact
// superseded output. It is deleted when settled; it is not a cleanup history.
type ObsoletePublicationOutputCleanup struct {
	bun.BaseModel      `bun:"table:obsolete_publication_output_cleanup"`
	OperationID        uuid.UUID       `bun:"operation_id,pk,type:uuid"`
	Epoch              uuid.UUID       `bun:"epoch,type:uuid"`
	PositionID         uuid.UUID       `bun:"position_id,type:uuid"`
	Generation         uuid.UUID       `bun:"generation,type:uuid"`
	RelativePath       string          `bun:"relative_path"`
	NativePath         string          `bun:"native_path"`
	OutputRootIdentity json.RawMessage `bun:"output_root_identity,type:jsonb"`
	ParentIdentity     json.RawMessage `bun:"parent_identity,type:jsonb"`
	NamespaceClaimID   uuid.UUID       `bun:"namespace_claim_id,type:uuid"`
	FileSize           int64           `bun:"file_size"`
	ModifiedAt         time.Time       `bun:"modified_at"`
	SHA256             []byte          `bun:"sha256"`
	State              string          `bun:"state"`
	SafeError          *string         `bun:"safe_error,nullzero"`
}

// ObsoletePublicationOutputCleanupAdmission is the caller-frozen authority for
// one output from a superseded publication. The caller supplies native identity
// evidence only after validating it against the actual filesystem observation.
type ObsoletePublicationOutputCleanupAdmission struct {
	Epoch              uuid.UUID
	PositionID         uuid.UUID
	Generation         uuid.UUID
	RelativePath       string
	NativePath         string
	OutputRootIdentity PublicationDirectoryIdentity
	ParentIdentity     PublicationDirectoryIdentity
	NamespaceClaimID   uuid.UUID
	FileSize           int64
	ModifiedAt         time.Time
	SHA256             []byte
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
	MutationClaimUUID     *uuid.UUID      `bun:"mutation_claim_uuid,type:uuid,nullzero"`
	MutationStartedAt     *time.Time      `bun:"mutation_started_at,nullzero"`
	MutationCompletedAt   *time.Time      `bun:"mutation_completed_at,nullzero"`
}

// SourceCleanupOwner durably records the exact item, operation delivery, and
// source identity admitted for a cleanup attempt. Source identity UUIDs are
// intentionally snapshots, not foreign keys to mutable inventory rows.
type SourceCleanupOwner struct {
	bun.BaseModel      `bun:"table:source_cleanup_owner"`
	ItemID             uuid.UUID `bun:"item_id,pk,type:uuid"`
	OperationID        uuid.UUID `bun:"operation_id,type:uuid"`
	ApplicationAttempt int       `bun:"application_attempt"`
	RiverJobID         int64     `bun:"river_job_id"`
	OwnerUUID          uuid.UUID `bun:"owner_uuid,type:uuid"`
	SourceRootID       uuid.UUID `bun:"source_root_id,type:uuid"`
	SourceLocationID   uuid.UUID `bun:"source_location_id,type:uuid"`
	WorkID             uuid.UUID `bun:"work_id,type:uuid"`
}

// SourceCleanupExplicitRetry is a durable, explicit retry grant. Its binding
// snapshots retained evidence; it does not prove the prior worker has returned.
type SourceCleanupExplicitRetry struct {
	bun.BaseModel              `bun:"table:source_cleanup_explicit_retry"`
	ID                         uuid.UUID       `bun:"id,pk,type:uuid"`
	RecoveryOperationID        uuid.UUID       `bun:"recovery_operation_id,type:uuid"`
	OriginalOperationID        uuid.UUID       `bun:"original_operation_id,type:uuid"`
	OriginalApplicationAttempt int             `bun:"original_application_attempt"`
	OriginalRiverJobID         int64           `bun:"original_river_job_id"`
	OriginalClaimUUID          uuid.UUID       `bun:"original_claim_uuid,type:uuid"`
	PositionID                 uuid.UUID       `bun:"position_id,type:uuid"`
	Epoch                      uuid.UUID       `bun:"epoch,type:uuid"`
	PublicationGeneration      uuid.UUID       `bun:"publication_generation,type:uuid"`
	Binding                    json.RawMessage `bun:"binding,type:jsonb"`
	FrontierRevision           *uuid.UUID      `bun:"frontier_revision,type:uuid,nullzero"`
	CreatedAt                  time.Time       `bun:"created_at"`
	ConsumedApplicationAttempt *int            `bun:"consumed_application_attempt,nullzero"`
	ConsumedRiverJobID         *int64          `bun:"consumed_river_job_id,nullzero"`
	ConsumedClaimUUID          *uuid.UUID      `bun:"consumed_claim_uuid,type:uuid,nullzero"`
}

// SourceCleanupRecoveryState is the mutable current frontier, not a history row.
type SourceCleanupRecoveryState struct {
	bun.BaseModel               `bun:"table:source_cleanup_recovery_state"`
	OriginalOperationID         uuid.UUID       `bun:"original_operation_id,pk,type:uuid"`
	Revision                    uuid.UUID       `bun:"revision,type:uuid"`
	ReturnedRecoveryOperationID uuid.UUID       `bun:"returned_recovery_operation_id,type:uuid"`
	ReturnedApplicationAttempt  int             `bun:"returned_application_attempt"`
	ReturnedRiverJobID          int64           `bun:"returned_river_job_id"`
	ReturnedRiverAttempt        int             `bun:"returned_river_attempt"`
	ReturnedClaimUUID           uuid.UUID       `bun:"returned_claim_uuid,type:uuid"`
	ReturnedBootUUID            uuid.UUID       `bun:"returned_boot_uuid,type:uuid"`
	ReturnedDeliveryToken       string          `bun:"returned_delivery_token"`
	Proof                       json.RawMessage `bun:"proof,type:jsonb"`
}

type SourceCleanupRecoveryItem struct {
	bun.BaseModel       `bun:"table:source_cleanup_recovery_item"`
	OriginalOperationID uuid.UUID  `bun:"original_operation_id,pk,type:uuid" json:"original_operation_id"`
	ItemID              uuid.UUID  `bun:"item_id,pk,type:uuid" json:"item_id"`
	Kind                string     `bun:"kind" json:"kind"`
	StartedAt           *time.Time `bun:"started_at,nullzero" json:"started_at"`
	CompletedAt         *time.Time `bun:"completed_at,nullzero" json:"completed_at"`
}

// SourceCleanupExplicitRetryItem binds every grant to the precise retained
// cleanup item and its original owner row.
type SourceCleanupExplicitRetryItem struct {
	bun.BaseModel       `bun:"table:source_cleanup_explicit_retry_item"`
	RetryID             uuid.UUID `bun:"retry_id,pk,type:uuid"`
	OriginalOperationID uuid.UUID `bun:"original_operation_id,type:uuid"`
	ItemID              uuid.UUID `bun:"item_id,pk,type:uuid"`
	OwnerUUID           uuid.UUID `bun:"owner_uuid,type:uuid"`
}

// SourceCleanupExplicitRetryResolution is the one-shot per-item native outcome
// journal for an explicitly claimed retry delivery.
type SourceCleanupExplicitRetryResolution struct {
	bun.BaseModel       `bun:"table:source_cleanup_explicit_retry_resolution"`
	RetryID             uuid.UUID  `bun:"retry_id,pk,type:uuid"`
	ItemID              uuid.UUID  `bun:"item_id,pk,type:uuid"`
	RecoveryOperationID uuid.UUID  `bun:"recovery_operation_id,type:uuid"`
	ApplicationAttempt  int        `bun:"application_attempt"`
	RiverJobID          int64      `bun:"river_job_id"`
	ClaimUUID           uuid.UUID  `bun:"claim_uuid,type:uuid"`
	StartedAt           time.Time  `bun:"started_at"`
	CompletedAt         *time.Time `bun:"completed_at,nullzero"`
	ResolutionKind      *string    `bun:"resolution_kind,nullzero"`
}
