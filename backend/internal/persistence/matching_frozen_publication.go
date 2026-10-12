package persistence

import (
	"encoding/json"

	"github.com/google/uuid"
)

// FrozenMatchingPublication is a self-contained, immutable publication payload
// for a frozen fallback evaluation. It is deliberately separate from the
// mutable incoming-match candidate persistence contract.
type FrozenMatchingPublication struct {
	Authority  FrozenMatchingAuthorityIdentity `json:"authority"`
	Evaluation FrozenMatchingEvaluation        `json:"evaluation"`
	Edges      []FrozenMatchingScoredEdge      `json:"edges"`
	Inventory  FrozenMatchingReviewInventory   `json:"inventory"`
}

type FrozenMatchingAuthorityIdentity struct {
	SealSchemaVersion        int                            `json:"seal_schema_version"`
	RunID                    uuid.UUID                      `json:"run_id"`
	WaitRevision             int64                          `json:"wait_revision"`
	SealDigest               string                         `json:"seal_digest"`
	SealDisposition          string                         `json:"seal_disposition"`
	TargetFenceVersion       int                            `json:"target_fence_version"`
	TargetCaptureDigest      string                         `json:"target_capture_digest"`
	TargetCapture            json.RawMessage                `json:"target_capture"`
	Intent                   MatchingAcquisitionIntent      `json:"intent"`
	SourceID                 uuid.UUID                      `json:"source_id"`
	ProviderID               uuid.UUID                      `json:"provider_id"`
	Endpoint                 string                         `json:"endpoint"`
	SourceConfiguration      string                         `json:"source_configuration"`
	RunProviderConfiguration string                         `json:"run_provider_configuration"`
	RunSourceID              uuid.UUID                      `json:"run_source_id"`
	RunProviderID            uuid.UUID                      `json:"run_provider_id"`
	RunEndpoint              string                         `json:"run_endpoint"`
	RunConfiguration         string                         `json:"run_configuration"`
	CalculationStageIndex    int                            `json:"calculation_stage_index"`
	FullEvaluationContext    json.RawMessage                `json:"full_evaluation_context,omitempty"`
	GroupID                  uuid.UUID                      `json:"group_id"`
	GroupRevision            string                         `json:"group_revision"`
	GroupAuthority           IncomingGroupMatchingAuthority `json:"group_authority"`
}

type FrozenMatchingEvaluation struct {
	Outcome         string                     `json:"outcome"`
	Decision        string                     `json:"decision"`
	Reason          string                     `json:"reason"`
	SelectedRelease string                     `json:"selected_release,omitempty"`
	Assignments     []FrozenMatchingAssignment `json:"assignments"`
}

type FrozenMatchingAssignment struct {
	FileMemberID    uuid.UUID `json:"file_member_id"`
	ProviderTrackID uuid.UUID `json:"provider_track_id"`
	Medium          int       `json:"medium"`
	Position        int       `json:"position"`
}

// FrozenMatchingScoredEdge retains both scoring profiles and their exact inputs
// for every file/release-track edge, including below-threshold edges.
type FrozenMatchingScoredEdge struct {
	FileMemberID         uuid.UUID       `json:"file_member_id"`
	ReleaseID            string          `json:"release_id"`
	ProviderReleaseID    uuid.UUID       `json:"provider_release_id"`
	RecordingID          string          `json:"recording_id"`
	ProviderTrackID      uuid.UUID       `json:"provider_track_id"`
	Medium               int             `json:"medium"`
	Position             int             `json:"position"`
	RecordingLookupKey   string          `json:"recording_lookup_key"`
	LookupRequestID      uuid.UUID       `json:"lookup_request_id"`
	LookupStageIndex     int             `json:"lookup_stage_index"`
	LookupRequestInput   json.RawMessage `json:"lookup_request_input"`
	LookupPayloadSHA     string          `json:"lookup_payload_sha256"`
	LookupProjectionSHA  string          `json:"lookup_projection_sha256"`
	MetadataInputSHA     string          `json:"metadata_input_sha256"`
	ReleaseScore         json.RawMessage `json:"release_score"`
	ReleaseScoreInput    json.RawMessage `json:"release_score_input"`
	AssignmentScore      json.RawMessage `json:"assignment_score"`
	AssignmentScoreInput json.RawMessage `json:"assignment_score_input"`
}

// FrozenMatchingReviewInventory preserves the full captured target and provider
// layouts. It is intentionally not a projection of only selected candidates.
type FrozenMatchingReviewInventory struct {
	GroupID          uuid.UUID                           `json:"group_id"`
	GroupRevision    string                              `json:"group_revision"`
	Members          []FrozenMatchingReviewMember        `json:"members"`
	Releases         []MatchingRelease                   `json:"releases"`
	CapturedMetadata []MatchingAcquisitionCapturedMember `json:"captured_metadata"`
	Captures         []IncomingGroupingCapture           `json:"captures"`
}

type FrozenMatchingReviewMember struct {
	FileMemberID uuid.UUID       `json:"file_member_id"`
	WorkID       uuid.UUID       `json:"work_id"`
	SourceSHA256 []byte          `json:"source_sha256"`
	Tags         json.RawMessage `json:"tags"`
	Locations    json.RawMessage `json:"locations"`
}
