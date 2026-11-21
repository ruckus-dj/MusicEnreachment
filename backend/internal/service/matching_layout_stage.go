package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// MatchingLayoutPolicyVersion identifies the two-stage selection rule, distinct
// from the underlying text scorer version.
const MatchingLayoutPolicyVersion = "matching-layout-v1"

// MatchingLayoutStage is the result of one shortlist stage. A selected stage
// carries the exact saved provider positions, not instructions to rescore.
// It is a calculation result, not an apply authorization or a freshness fence.
type MatchingLayoutStage struct {
	PolicyVersion     string                     `json:"policy_version"`
	PairThreshold     float64                    `json:"pair_threshold"`
	Decision          MatchingLayoutSelection    `json:"decision"`
	ProviderReleaseID *uuid.UUID                 `json:"provider_release_id,omitempty"`
	Assignments       []MatchingLayoutAssignment `json:"assignments,omitempty"`
	ScoredPairs       int                        `json:"-"`
}

type MatchingLayoutAssignment = persistence.MatchingLayoutDecisionAssignment

// DurableDecision projects the calculation into the write-once result consumed
// by continuation and apply. Diagnostic scores and coordinate strings are not
// part of that result. Its caller binds the independently authorized evidence.
func (stage MatchingLayoutStage) DurableDecision() (persistence.MatchingLayoutDecision, error) {
	threshold, err := persistence.CanonicalMatchingLayoutThreshold(stage.PairThreshold)
	if err != nil {
		return persistence.MatchingLayoutDecision{}, err
	}
	return persistence.MatchingLayoutDecision{
		SchemaVersion:     persistence.MatchingLayoutDecisionSchemaVersion,
		PolicyVersion:     stage.PolicyVersion,
		PairThreshold:     threshold,
		Status:            persistence.MatchingLayoutDecisionStatus(stage.Decision.Status),
		ProviderReleaseID: cloneUUID(stage.ProviderReleaseID),
		Assignments:       append([]MatchingLayoutAssignment(nil), stage.Assignments...),
		Reason:            stage.Decision.Reason,
	}, nil
}

// MatchingLayoutRetrievalMembers projects saved corrections for ranking only.
// These are comparison inputs, not replacements for the raw evidence on the
// target capture. The caller must retain the capture and correction revision.
func MatchingLayoutRetrievalMembers(captures []persistence.IncomingGroupingCapture) ([]persistence.MatchingAcquisitionCapturedMember, error) {
	view, _, err := matchingLayoutStageFiles(captures)
	if err != nil {
		return nil, err
	}
	members := make([]persistence.MatchingAcquisitionCapturedMember, 0, len(view.Files))
	for _, file := range view.Files {
		capture, err := matchingCaptureForFile(captures, file.VariantID)
		if err != nil {
			return nil, err
		}
		tags, err := matchingLayoutTags(file.Tags, capture.TagEncoding)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(tags)
		if err != nil {
			return nil, fmt.Errorf("encode matching retrieval tags: %w", err)
		}
		members = append(members, persistence.MatchingAcquisitionCapturedMember{
			MemberID: file.VariantID,
			Tags:     encoded,
		})
	}
	return members, nil
}

// CalculateMatchingLayoutStage evaluates only the supplied shortlist, over the
// complete ready group. Missing hydration is distinct from a proved no-match.
// The snapshot must belong to a single provider scope, frozen by the caller.
func CalculateMatchingLayoutStage(
	ctx context.Context,
	captures []persistence.IncomingGroupingCapture,
	shortlist []string,
	snapshot persistence.ProviderMatchingSnapshot,
	searchKeys map[uuid.UUID]MatchingSearchKeys,
	acoustIDScores map[MatchingAcoustIDKey]float64,
	threshold float64,
) (MatchingLayoutStage, error) {
	if err := ctx.Err(); err != nil {
		return MatchingLayoutStage{}, err
	}
	if !validMatchingConfidence(threshold) || len(shortlist) > MatchingReleaseShortlistLimit {
		return MatchingLayoutStage{}, fmt.Errorf("invalid matching layout stage threshold or shortlist size")
	}
	view, conflicts, err := matchingLayoutStageFiles(captures)
	if err != nil {
		return MatchingLayoutStage{}, err
	}
	result := MatchingLayoutStage{PolicyVersion: MatchingLayoutPolicyVersion, PairThreshold: threshold}
	expected := make(map[string]bool, len(shortlist))
	for _, key := range shortlist {
		if strings.TrimSpace(key) == "" || expected[key] {
			return MatchingLayoutStage{}, fmt.Errorf("missing or duplicate matching shortlist edition identity")
		}
		expected[key] = true
	}
	byKey := make(map[string]persistence.MatchingRelease, len(shortlist))
	releaseIDs := make(map[uuid.UUID]bool, len(shortlist))
	incomplete := false
	for _, release := range snapshot.Releases {
		if !expected[release.ProviderKey] || byKey[release.ProviderKey].ID != uuid.Nil ||
			release.ID == uuid.Nil || releaseIDs[release.ID] {
			return MatchingLayoutStage{}, fmt.Errorf("unexpected or duplicate hydrated matching edition %q", release.ProviderKey)
		}
		byKey[release.ProviderKey] = release
		releaseIDs[release.ID] = true
		incomplete = incomplete || !release.Complete
	}
	if len(byKey) != len(expected) || incomplete {
		result.Decision = MatchingLayoutSelection{
			Status: MatchingLayoutSelectionIndeterminate,
			Reason: "a shortlisted edition has no complete hydrated tracklist",
		}
		return result, nil
	}
	members := make([]persistence.IncomingMatchMemberFence, 0, len(view.Files))
	for _, file := range view.Files {
		members = append(members, persistence.IncomingMatchMemberFence{FileMemberID: file.VariantID})
	}
	edges, err := BuildLayoutMatchingInputs(captures, snapshot, searchKeys, acoustIDScores)
	if err != nil {
		return MatchingLayoutStage{}, err
	}
	result.ScoredPairs = len(edges)
	result.Decision, err = SelectMatchingLayout(ctx, MatchingGroupEvaluationInput{
		Members: members, Files: view.Files, Inputs: edges, ReleaseLayouts: snapshot.Releases,
		ConflictingFingerprints: conflicts,
	}, threshold)
	if err != nil {
		return MatchingLayoutStage{}, err
	}
	if result.Decision.Status != MatchingLayoutSelectionSelected {
		return result, nil
	}
	release := byKey[result.Decision.ReleaseID]
	tracks := make(map[string]uuid.UUID, len(release.Tracks))
	trackIDs := make(map[uuid.UUID]bool, len(release.Tracks))
	for _, track := range release.Tracks {
		if track.ID == uuid.Nil || trackIDs[track.ID] {
			return MatchingLayoutStage{}, fmt.Errorf("selected matching layout has a missing or duplicate provider position identity")
		}
		tracks[matchingLayoutChoiceID(release.ProviderKey, track.Medium, track.Position)] = track.ID
		trackIDs[track.ID] = true
	}
	result.ProviderReleaseID = &release.ID
	result.Assignments = make([]MatchingLayoutAssignment, 0, len(members))
	for _, member := range members {
		choice := result.Decision.Choices[member.FileMemberID.String()]
		trackID := tracks[choice]
		if trackID == uuid.Nil {
			return MatchingLayoutStage{}, fmt.Errorf("selected matching layout has no provider position for file %s", member.FileMemberID)
		}
		result.Assignments = append(result.Assignments, MatchingLayoutAssignment{
			FileMemberID: member.FileMemberID, ProviderTrackID: trackID,
		})
	}
	return result, nil
}

func matchingLayoutStageFiles(captures []persistence.IncomingGroupingCapture) (incomingCaptureView, map[uuid.UUID]bool, error) {
	byMember := make(map[uuid.UUID]persistence.IncomingGroupingCapture)
	conflicts := make(map[uuid.UUID]bool)
	locations := make(map[uuid.UUID]bool)
	for _, capture := range captures {
		if !capture.Ready || capture.CaptureAnalysisID == nil || *capture.CaptureAnalysisID == uuid.Nil ||
			capture.VariantID == uuid.Nil || capture.RootID == uuid.Nil || capture.WorkID == uuid.Nil ||
			capture.LocationID == uuid.Nil || capture.RelativePath == "" || locations[capture.LocationID] {
			return incomingCaptureView{}, nil, fmt.Errorf("matching layout stage requires distinct, ready saved captures")
		}
		locations[capture.LocationID] = true
		if prior, ok := byMember[capture.VariantID]; ok {
			if !bytes.Equal(capture.TagEncoding, prior.TagEncoding) {
				return incomingCaptureView{}, nil, fmt.Errorf("matching layout stage found conflicting saved tag corrections")
			}
			bothAbsent := missingMatchingFingerprint(prior) && missingMatchingFingerprint(capture)
			conflicts[capture.VariantID] = conflicts[capture.VariantID] ||
				!bothAbsent && !persistence.SameAcoustIDFingerprintIdentity(prior, capture)
		} else {
			byMember[capture.VariantID] = capture
		}
	}
	view, err := incomingFilesFromCaptures(captures)
	if err != nil {
		return incomingCaptureView{}, nil, err
	}
	if len(view.Files) == 0 {
		return incomingCaptureView{}, nil, fmt.Errorf("matching layout stage requires saved group members")
	}
	return view, conflicts, nil
}

func missingMatchingFingerprint(capture persistence.IncomingGroupingCapture) bool {
	return capture.FingerprintResultID == nil && capture.FingerprintWinner == nil && capture.Fingerprint == nil
}

func matchingLayoutChoiceID(releaseKey string, medium, position int) string {
	return fmt.Sprintf("%s/%d/%d", releaseKey, medium, position)
}
