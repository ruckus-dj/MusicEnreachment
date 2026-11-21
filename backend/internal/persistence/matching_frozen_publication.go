package persistence

import (
	"encoding/json"
	"errors"
	"sort"

	"github.com/google/uuid"
)

// FrozenMatchingPublication is a self-contained, immutable publication payload
// for a frozen fallback evaluation. It is deliberately separate from the
// mutable incoming-match candidate persistence contract.
type FrozenMatchingPublication struct {
	Authority  FrozenMatchingAuthorityIdentity `json:"authority"`
	Evaluation FrozenMatchingEvaluation        `json:"evaluation"`
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

// BuildFrozenMatchingLayoutEvaluation projects a sealed result onto a
// one-based review inventory. Coordinates are display data, not identity.
func BuildFrozenMatchingLayoutEvaluation(decision *MatchingLayoutDecision, releases []MatchingRelease) (FrozenMatchingEvaluation, error) {
	result := FrozenMatchingEvaluation{Outcome: "review", Decision: "review", Assignments: []FrozenMatchingAssignment{}}
	if decision == nil {
		result.Reason = "no provider query was available"
		return result, nil
	}
	if err := decision.validate(); err != nil {
		return FrozenMatchingEvaluation{}, err
	}
	result.Reason = decision.Reason
	if decision.Status != MatchingLayoutDecisionSelected {
		return result, nil
	}
	var selected *MatchingRelease
	for i := range releases {
		if releases[i].ID != *decision.ProviderReleaseID {
			continue
		}
		if selected != nil {
			return FrozenMatchingEvaluation{}, errors.New("sealed layout release is duplicated in the review inventory")
		}
		selected = &releases[i]
	}
	if selected == nil || selected.ProviderKey == "" || !selected.Complete {
		return FrozenMatchingEvaluation{}, errors.New("sealed layout release is absent or incomplete in the review inventory")
	}
	tracks := make(map[uuid.UUID]MatchingReleaseTrack, len(selected.Tracks))
	for _, track := range selected.Tracks {
		if track.ID == uuid.Nil || track.Medium < 1 || track.Position < 1 || tracks[track.ID].ID != uuid.Nil {
			return FrozenMatchingEvaluation{}, errors.New("sealed layout release has an invalid track identity or coordinate")
		}
		tracks[track.ID] = track
	}
	result.Outcome, result.Decision, result.SelectedRelease = "unique", "apply", selected.ProviderKey
	for _, assignment := range decision.Assignments {
		track, ok := tracks[assignment.ProviderTrackID]
		if !ok {
			return FrozenMatchingEvaluation{}, errors.New("sealed layout assignment is outside the selected release")
		}
		result.Assignments = append(result.Assignments, FrozenMatchingAssignment{
			FileMemberID: assignment.FileMemberID, ProviderTrackID: track.ID, Medium: track.Medium, Position: track.Position,
		})
	}
	sort.Slice(result.Assignments, func(i, j int) bool {
		return result.Assignments[i].FileMemberID.String() < result.Assignments[j].FileMemberID.String()
	})
	return result, nil
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
