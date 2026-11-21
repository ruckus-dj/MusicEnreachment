package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestSerializeFrozenFallbackEvaluationUsesSealedTrackAndOneBasedCoordinates(t *testing.T) {
	memberID, workID, groupID, runID, releaseID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	capture := json.RawMessage(`{"schema_version":2}`)
	canonical, err := canonicalFallbackJSON(capture)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	first, second := uuid.New(), uuid.New()
	prepared := PreparedFrozenFallbackEvaluation{
		Authority: FrozenFallbackEvaluationAuthority{
			RunID: runID, WaitRevision: 4, TargetFenceVersion: 2, Digest: hex.EncodeToString(digest[:]),
			TargetCapture: capture, TargetCaptureDigest: hex.EncodeToString(digest[:]),
		},
		LayoutDecision: &persistence.MatchingLayoutDecision{
			SchemaVersion: 1, PolicyVersion: MatchingLayoutPolicyVersion, PairThreshold: "0.7",
			Status: persistence.MatchingLayoutDecisionSelected, ProviderReleaseID: &releaseID,
			Assignments: []persistence.MatchingLayoutDecisionAssignment{{FileMemberID: memberID, ProviderTrackID: second}},
		},
		Releases: []persistence.MatchingRelease{{ID: releaseID, ProviderKey: "release", Complete: true, Tracks: []persistence.MatchingReleaseTrack{
			{ID: first, Position: 0}, {ID: second, Position: 1},
		}}},
		FrozenInventory: MatchingFrozenGroupInventory{
			Group: IncomingGroup{ID: groupID, Revision: "g1"}, Files: []IncomingFile{{VariantID: memberID}},
			MemberFences: []persistence.IncomingMatchMemberFence{{FileMemberID: memberID, WorkID: workID}},
		},
	}
	publication, err := SerializeFrozenFallbackEvaluation(prepared)
	if err != nil {
		t.Fatal(err)
	}
	want := persistence.FrozenMatchingAssignment{FileMemberID: memberID, ProviderTrackID: second, Medium: 1, Position: 2}
	if publication.Evaluation.Decision != "apply" || publication.Evaluation.SelectedRelease != "release" ||
		len(publication.Evaluation.Assignments) != 1 || publication.Evaluation.Assignments[0] != want {
		t.Fatalf("evaluation differs from exact sealed assignment: %+v", publication.Evaluation)
	}
	if prepared.Releases[0].Tracks[1].Position != 1 {
		t.Fatal("serialization changed the accepted layout")
	}
	prepared.LayoutDecision = &persistence.MatchingLayoutDecision{
		SchemaVersion: 1, PolicyVersion: MatchingLayoutPolicyVersion, PairThreshold: "0.7",
		Status: persistence.MatchingLayoutDecisionNone,
	}
	review, err := SerializeFrozenFallbackEvaluation(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if review.Evaluation.Decision != "review" || len(review.Evaluation.Assignments) != 0 || len(review.Inventory.Members) != 1 ||
		len(review.Inventory.Releases) != 1 || len(review.Inventory.Releases[0].Tracks) != 2 || review.Inventory.Releases[0].Tracks[0].Position != 1 {
		t.Fatalf("review lost unassigned inventory: %+v", review)
	}
}
