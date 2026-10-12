package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestSerializeFrozenFallbackEvaluationKeepsReviewInventoryWithoutEdges(t *testing.T) {
	memberID, workID, groupID, runID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	authority := testFrozenPublicationAuthority(runID, 3, groupID, "g1", memberID)
	prepared := PreparedFrozenFallbackEvaluation{
		Authority:    authority,
		KernelResult: MatchingGroupEvaluationDecisionResult{Outcome: MatchingDecisionOutcomeReview, Decision: MatchingDecisionReview},
		FrozenInventory: MatchingFrozenGroupInventory{
			Group:        IncomingGroup{ID: groupID, Revision: "g1", Members: []uuid.UUID{memberID}},
			MemberFences: []persistence.IncomingMatchMemberFence{{FileMemberID: memberID, WorkID: workID, SourceSHA256: []byte{1, 2}}},
			Files:        []IncomingFile{{VariantID: memberID, Tags: map[string][]string{"TITLE": {"captured"}}, Locations: []IncomingLocation{{RelativePath: "x.flac"}}}},
		},
	}
	publication, err := SerializeFrozenFallbackEvaluation(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if publication.Authority.TargetFenceVersion != 2 || publication.Authority.SealSchemaVersion != 3 || publication.Authority.RunID != runID {
		t.Fatalf("authority identity was not retained: %+v", publication.Authority)
	}
	if len(publication.Inventory.Members) != 1 || len(publication.Edges) != 0 || publication.Evaluation.Outcome != string(MatchingDecisionOutcomeReview) {
		t.Fatalf("unexpected frozen review publication: %+v", publication)
	}
}

func TestSerializeFrozenFallbackEvaluationSortsEdgesAndUsesOneBasedCoordinates(t *testing.T) {
	memberID, workID, groupID, runID, releaseID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	track := persistence.MatchingReleaseTrack{ID: uuid.New(), Medium: 0, Position: 0, RecordingProviderKey: "recording"}
	release := persistence.MatchingRelease{ID: releaseID, ProviderKey: "release", Tracks: []persistence.MatchingReleaseTrack{track}}
	input := MatchingInput{FileID: memberID, ReleaseID: "release", RecordingID: "recording", Medium: 0, Track: 0}
	payloadHash := sha256.Sum256([]byte(`{"release":"release"}`))
	projectionHash := sha256.Sum256([]byte(`{"accepted":"release"}`))
	requestID := uuid.MustParse("55555555-5555-4555-8555-555555555555")
	requestInput := json.RawMessage(`{"schema_version":1,"mbid":"release"}`)
	candidate := MatchingFallbackCandidate{MemberID: memberID, ReleaseID: "release", RecordingID: "recording", ProviderTrackUUID: track.ID,
		Medium: 0, Position: 0, MatchingInput: input, LookupPayloadSHA: hex.EncodeToString(payloadHash[:]), LookupProjectionSHA: hex.EncodeToString(projectionHash[:])}
	authority := testFrozenPublicationAuthority(runID, 4, groupID, "g2", memberID)
	authority.CalculationLookups = []persistence.MatchingFallbackCalculationLookupAuthority{{
		RequestID: requestID, StageIndex: 2, ReleaseID: "release", Kind: "musicbrainz_release_lookup",
		ProviderSourceID: uuid.MustParse("66666666-6666-4666-8666-666666666666"), ProviderID: uuid.MustParse("77777777-7777-4777-8777-777777777777"),
		Endpoint: "https://musicbrainz.example", Configuration: "cfg", CacheKey: "release-lookup:release", Input: requestInput,
	}}
	authority.CalculationStageIndex = 2
	authority.RunSourceID = authority.CalculationLookups[0].ProviderSourceID
	authority.RunProviderID = authority.CalculationLookups[0].ProviderID
	authority.RunEndpoint = authority.CalculationLookups[0].Endpoint
	authority.RunConfiguration = authority.CalculationLookups[0].Configuration
	authority.CalculationReceipts = []persistence.MatchingFallbackLookupReceipt{{
		RunID: runID, RequestID: requestID, StageIndex: 2, ProviderSourceID: authority.CalculationLookups[0].ProviderSourceID,
		ConfigurationIdentity: "cfg", CacheKey: "release-lookup:release", RequestInput: requestInput,
		Payload: json.RawMessage(`{"release":"release"}`), PayloadSHA256: hex.EncodeToString(payloadHash[:]),
		AcceptedProjection: json.RawMessage(`{"accepted":"release"}`), ProjectionSHA256: hex.EncodeToString(projectionHash[:]),
	}}
	prepared := PreparedFrozenFallbackEvaluation{
		Authority: authority,
		Result:    MatchingFallbackApplicabilityResult{ReleaseLayouts: []persistence.MatchingRelease{release}, AllCandidates: []MatchingFallbackCandidate{candidate}},
		KernelResult: MatchingGroupEvaluationDecisionResult{Outcome: MatchingDecisionOutcomeUnique, Decision: MatchingDecisionSelected,
			SelectedReleaseID: "release", Assignment: MatchingAssignmentResult{Unique: true, Choices: map[string]string{memberID.String(): "candidate"}},
			AssignmentCandidates: []MatchingAssignmentCandidate{{FileVariantID: memberID.String(), CandidateID: "candidate", PositionID: "release/0/0"}}},
		FrozenInventory: MatchingFrozenGroupInventory{Group: IncomingGroup{ID: groupID, Revision: "g2", Members: []uuid.UUID{memberID}},
			MemberFences: []persistence.IncomingMatchMemberFence{{FileMemberID: memberID, WorkID: workID}}, Files: []IncomingFile{{VariantID: memberID}},
			CapturedMetadata: []persistence.MatchingAcquisitionCapturedMember{{MemberID: memberID, WorkID: workID, Tags: json.RawMessage(`{}`)}}},
	}
	prepared.Authority.Intent = persistence.MatchingAcquisitionIntent{SchemaVersion: 3, FenceVersion: 2,
		TargetCaptureDigest: authority.TargetCaptureDigest, Targets: []persistence.MatchingTarget{{GroupID: groupID, Revision: "g2"}},
		CapturedMembers: []persistence.MatchingAcquisitionCapturedMember{{MemberID: memberID, WorkID: workID, Tags: json.RawMessage(`{}`)}}}
	prepared.Authority.GroupAuthority = persistence.IncomingGroupMatchingAuthority{GroupID: groupID, Mode: "automatic"}
	if prepared.Result.ReleaseLayouts[0].Tracks[0].Medium != 0 || prepared.Result.ReleaseLayouts[0].Tracks[0].Position != 0 {
		t.Fatalf("prepared release layout was not zero-based before serialization: %+v", prepared.Result.ReleaseLayouts[0].Tracks[0])
	}
	publication, err := SerializeFrozenFallbackEvaluation(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if len(publication.Evaluation.Assignments) != 1 || publication.Evaluation.Assignments[0].Medium != 1 || publication.Evaluation.Assignments[0].Position != 1 {
		t.Fatalf("coordinates were not converted exactly once: %+v", publication.Evaluation.Assignments)
	}
	if len(publication.Inventory.Releases) != 1 || len(publication.Inventory.Releases[0].Tracks) != 1 ||
		publication.Inventory.Releases[0].Tracks[0].Medium != 1 || publication.Inventory.Releases[0].Tracks[0].Position != 1 {
		t.Fatalf("review inventory layout was not converted to one-based coordinates: %+v", publication.Inventory.Releases)
	}
	if publication.Edges[0].Medium != 0 || publication.Edges[0].Position != 0 {
		t.Fatalf("scored edges must keep zero-based kernel coordinates: %+v", publication.Edges[0])
	}
	if prepared.Result.ReleaseLayouts[0].Tracks[0].Medium != 0 || prepared.Result.ReleaseLayouts[0].Tracks[0].Position != 0 {
		t.Fatalf("serialization mutated the prepared release layout: %+v", prepared.Result.ReleaseLayouts[0].Tracks[0])
	}
	if publication.Edges[0].RecordingLookupKey != "recording" || publication.Edges[0].LookupPayloadSHA != hex.EncodeToString(payloadHash[:]) || publication.Edges[0].LookupProjectionSHA != hex.EncodeToString(projectionHash[:]) || publication.Edges[0].LookupRequestID != requestID {
		t.Fatalf("edge authority was not retained: %+v", publication.Edges[0])
	}
	if publication.Edges[0].ProviderReleaseID != releaseID || publication.Authority.SealDisposition != "qualified" ||
		publication.Authority.TargetCaptureDigest != authority.TargetCaptureDigest {
		t.Fatalf("frozen release or seal authority was not retained: authority=%+v edge=%+v", publication.Authority, publication.Edges[0])
	}
	first, _ := json.Marshal(publication)
	second, err := SerializeFrozenFallbackEvaluation(prepared)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, _ := json.Marshal(second)
	if string(first) != string(secondBytes) {
		t.Fatal("serialization is not stable")
	}
}

func testFrozenPublicationAuthority(runID uuid.UUID, waitRevision int64, groupID uuid.UUID, groupRevision string, memberID uuid.UUID) FrozenFallbackEvaluationAuthority {
	capture := json.RawMessage(`{"schema_version":2,"group_id":"` + groupID.String() + `","group_revision":"` + groupRevision + `","members":[{"variant_id":"` + memberID.String() + `"}]}`)
	canonical, _ := canonicalFallbackJSON(capture)
	digest := sha256.Sum256(canonical)
	return FrozenFallbackEvaluationAuthority{
		RunID: runID, WaitRevision: waitRevision, TargetFenceVersion: 2, Disposition: "qualified",
		Digest:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		TargetCapture: capture, TargetCaptureDigest: hex.EncodeToString(digest[:]),
	}
}
