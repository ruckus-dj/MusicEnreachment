package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type matchingReviewSnapshotReader struct {
	snapshot *persistence.IncomingMatchReviewSnapshot
}

func (reader matchingReviewSnapshotReader) GetReviewSnapshot(context.Context, uuid.UUID) (*persistence.IncomingMatchReviewSnapshot, error) {
	return reader.snapshot, nil
}

func TestMatchingReviewProjectsSavedReleaseAndAssignmentConfidenceSeparately(t *testing.T) {
	groupID, memberID, providerID, sourceID, releaseID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	trackA, trackB := uuid.New(), uuid.New()
	proposal := map[string]any{
		"version": 1, "group_id": groupID,
		"selected_release": map[string]any{
			"provider_release_id": releaseID, "release_id": "release-key", "title": "Album", "tracklist_complete": true,
			"positions": []any{
				map[string]any{"track_id": trackA, "medium": 1, "position": 1, "displayed_number": "A1", "recording_id": "recording-a", "title": "Track A"},
				map[string]any{"track_id": trackB, "medium": 2, "position": 1, "displayed_number": "B1", "recording_id": "recording-b", "title": "Track B"},
			},
		},
		"files": []any{map[string]any{"file_member_id": memberID, "raw_tags": map[string][]string{}, "diagnostics": []any{}}},
	}
	proposalJSON, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := json.Marshal(map[string]any{"review_proposal": json.RawMessage(proposalJSON)})
	if err != nil {
		t.Fatal(err)
	}
	candidateEvidence := json.RawMessage(`{"edge_evidence":[` +
		`{"schema_version":1,"medium":1,"position":1,"assignment_profile":{"profile":"recording_release","confidence":0.31}},` +
		`{"schema_version":1,"medium":2,"position":1,"assignment_profile":{"profile":"recording_release","confidence":0.82}}]}`)
	reader := matchingReviewSnapshotReader{snapshot: &persistence.IncomingMatchReviewSnapshot{
		Header:  &persistence.IncomingMatchEvaluationHeader{EvaluationRevision: "revision", EvaluationEvidence: evidence},
		Members: []uuid.UUID{memberID},
		Candidates: []persistence.IncomingMatchCandidate{{
			FileMemberID: memberID, ProviderID: providerID, ProviderSourceID: sourceID, ReleaseID: releaseID,
			Score: 0.91, Evidence: candidateEvidence,
			Positions: []persistence.IncomingMatchPosition{{TrackID: trackA, Medium: 1, Position: 1}, {TrackID: trackB, Medium: 2, Position: 1}},
		}},
	}}

	got, err := NewMatchingReviewService(reader).Get(context.Background(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Evaluated || !got.ProposalAvailable || len(got.Candidates) != 1 {
		t.Fatalf("unexpected review state: %+v", got)
	}
	candidate := got.Candidates[0]
	if candidate.ReleaseConfidence != 0.91 {
		t.Fatalf("release confidence = %v, want 0.91", candidate.ReleaseConfidence)
	}
	if len(candidate.Positions) != 2 || candidate.Positions[0].AssignmentConfidence == nil || candidate.Positions[1].AssignmentConfidence == nil || *candidate.Positions[0].AssignmentConfidence != 0.31 || *candidate.Positions[1].AssignmentConfidence != 0.82 {
		t.Fatalf("assignment confidence was not projected per position: %+v", candidate.Positions)
	}
	if candidate.Positions[0].DisplayedNumber != "A1" || candidate.Positions[1].DisplayedNumber != "B1" {
		t.Fatalf("candidate positions did not expose saved displayed numbers: %+v", candidate.Positions)
	}
	var returnedProposal struct {
		SelectedRelease struct {
			Positions []struct {
				DisplayedNumber string `json:"displayed_number"`
			} `json:"positions"`
		} `json:"selected_release"`
	}
	if err := json.Unmarshal(got.Proposal, &returnedProposal); err != nil {
		t.Fatal(err)
	}
	if len(returnedProposal.SelectedRelease.Positions) != 2 || returnedProposal.SelectedRelease.Positions[0].DisplayedNumber != "A1" {
		t.Fatalf("proposal layout lost displayed number: %s", got.Proposal)
	}
}

func TestMatchingReviewRoundTripsAllGroupScopedWriterLayouts(t *testing.T) {
	member := uuid.New()
	group := IncomingGroup{ID: uuid.New(), Revision: "group-r1", Members: []uuid.UUID{member}}
	makeRelease := func(key, title string, tracks ...persistence.MatchingReleaseTrack) persistence.MatchingRelease {
		return persistence.MatchingRelease{ID: uuid.New(), ProviderKey: key, Name: title, Complete: true, Tracks: tracks}
	}
	releaseA := makeRelease("release-a", "Release A",
		persistence.MatchingReleaseTrack{ID: uuid.New(), Medium: 1, Position: 1, DisplayedNumber: "A1", RecordingProviderKey: "same-recording", RecordingTitle: "First"},
		persistence.MatchingReleaseTrack{ID: uuid.New(), Medium: 2, Position: 1, DisplayedNumber: "B1", RecordingProviderKey: "same-recording", RecordingTitle: "Second"},
	)
	releaseB := makeRelease("release-b", "Release B", persistence.MatchingReleaseTrack{ID: uuid.New(), Medium: 1, Position: 1, DisplayedNumber: "1", RecordingProviderKey: "recording-b", RecordingTitle: "Other"})
	outOfScope := makeRelease("release-c", "Other group", persistence.MatchingReleaseTrack{ID: uuid.New(), Medium: 1, Position: 1, RecordingProviderKey: "other"})
	provider := persistence.ProviderMatchingSnapshot{
		Source:   persistence.ProviderSource{ID: uuid.New(), ProviderID: uuid.New(), Namespace: "mb", Endpoint: "https://mb.example", ConfigurationIdentity: "config"},
		Releases: []persistence.MatchingRelease{releaseA, releaseB, outOfScope},
	}
	inputs := []MatchingInput{
		syntheticMatchingInput(member, "release-a", 1, 1, .8, .8),
		syntheticMatchingInput(member, "release-b", 1, 1, .8, .8),
	}
	snapshotInput := MatchingEvaluationSnapshot{Provider: provider, Grouping: IncomingGroupingSnapshot{Files: []IncomingFile{{
		VariantID: member, Locations: []IncomingLocation{{RootID: uuid.New(), RelativePath: "album/track.flac"}},
	}}}}
	result, decision, _, err := makeMatchingGroupResult(t.Context(), snapshotInput, "groups-r1", group,
		[]persistence.IncomingMatchMemberFence{{FileMemberID: member, WorkID: uuid.New()}}, inputs,
		[]string{"release-a", "release-b"}, map[string]*string{})
	if err != nil {
		t.Fatal(err)
	}
	if decision != MatchingDecisionReview {
		t.Fatalf("decision = %q, want review without a selected release", decision)
	}
	var groupEvidence struct {
		Proposal json.RawMessage `json:"review_proposal"`
	}
	if err := json.Unmarshal(result.Groups[0].Evidence, &groupEvidence); err != nil {
		t.Fatal(err)
	}
	snapshot := &persistence.IncomingMatchReviewSnapshot{
		Header:  &persistence.IncomingMatchEvaluationHeader{EvaluationRevision: "writer-revision", EvaluationEvidence: result.Groups[0].Evidence},
		Members: []uuid.UUID{member}, Candidates: result.Groups[0].Candidates,
	}
	got, err := NewMatchingReviewService(matchingReviewSnapshotReader{snapshot: snapshot}).Get(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ProposalAvailable || len(got.Releases) != 2 {
		t.Fatalf("review layouts = %+v, proposal=%s", got.Releases, groupEvidence.Proposal)
	}
	if got.Releases[0].ReleaseID != "release-a" || len(got.Releases[0].Positions) != 2 || got.Releases[0].Positions[0].DisplayedNumber != "A1" || got.Releases[0].Positions[1].DisplayedNumber != "B1" {
		t.Fatalf("release A layout was not fully projected: %+v", got.Releases[0])
	}
	if got.Releases[0].Positions[0].RecordingID != got.Releases[0].Positions[1].RecordingID || got.Releases[0].Positions[0].Medium == got.Releases[0].Positions[1].Medium {
		t.Fatalf("same recording at distinct media coordinates was not preserved: %+v", got.Releases[0].Positions)
	}
	for _, release := range got.Releases {
		if release.ReleaseID == "release-c" {
			t.Fatal("layout from another group's provider snapshot leaked into saved review")
		}
	}
	firstJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	provider.Releases[0].Name = "Changed after evaluation"
	provider.Releases[0].Tracks = nil
	again, err := NewMatchingReviewService(matchingReviewSnapshotReader{snapshot: snapshot}).Get(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(again)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("GET changed after the provider cache changed: %s != %s", firstJSON, secondJSON)
	}
}

func TestMatchingReviewRoundTripsSelectedReleaseAndAlternativeLayouts(t *testing.T) {
	member := uuid.New()
	group := IncomingGroup{ID: uuid.New(), Revision: "group-r1", Members: []uuid.UUID{member}}
	selectedID, alternativeID := uuid.New(), uuid.New()
	selectedTrack, edgeLessTrack := uuid.New(), uuid.New()
	provider := persistence.ProviderMatchingSnapshot{
		Source: persistence.ProviderSource{ID: uuid.New(), ProviderID: uuid.New(), Namespace: "mb", Endpoint: "https://mb.example", ConfigurationIdentity: "config"},
		Releases: []persistence.MatchingRelease{
			{ID: selectedID, ProviderKey: "selected", Name: "Selected", Complete: true, Tracks: []persistence.MatchingReleaseTrack{
				{ID: selectedTrack, Medium: 1, Position: 1, DisplayedNumber: "A1", RecordingProviderKey: "same-recording", RecordingTitle: "First"},
				{ID: edgeLessTrack, Medium: 2, Position: 1, DisplayedNumber: "B1", RecordingProviderKey: "same-recording", RecordingTitle: "Second"},
			}},
			{ID: alternativeID, ProviderKey: "alternative", Name: "Alternative", Complete: true, Tracks: []persistence.MatchingReleaseTrack{{ID: uuid.New(), Medium: 1, Position: 1, RecordingProviderKey: "other"}}},
		},
	}
	snapshotInput := MatchingEvaluationSnapshot{Provider: provider, Grouping: IncomingGroupingSnapshot{Files: []IncomingFile{{
		VariantID: member, Locations: []IncomingLocation{{RootID: uuid.New(), RelativePath: "album/track.flac"}},
	}}}}
	result, decision, _, err := makeMatchingGroupResult(t.Context(), snapshotInput, "groups-r1", group,
		[]persistence.IncomingMatchMemberFence{{FileMemberID: member, WorkID: uuid.New()}},
		[]MatchingInput{syntheticMatchingInput(member, "selected", 1, 1, .95, .9)},
		[]string{"alternative", "selected"}, map[string]*string{})
	if err != nil {
		t.Fatal(err)
	}
	if decision != MatchingDecisionSelected {
		t.Fatalf("decision = %q, want selected", decision)
	}
	snapshot := &persistence.IncomingMatchReviewSnapshot{
		Header:  &persistence.IncomingMatchEvaluationHeader{EvaluationRevision: "writer-revision", EvaluationEvidence: result.Groups[0].Evidence},
		Members: []uuid.UUID{member}, Candidates: result.Groups[0].Candidates,
	}
	got, err := NewMatchingReviewService(matchingReviewSnapshotReader{snapshot: snapshot}).Get(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ProposalAvailable || len(got.Releases) != 2 {
		t.Fatalf("selected/alternative layouts = %+v", got.Releases)
	}
	selected := got.Releases[1]
	if selected.ReleaseID != "selected" || len(selected.Positions) != 2 || selected.Positions[1].TrackID != edgeLessTrack || selected.Positions[0].RecordingID != selected.Positions[1].RecordingID {
		t.Fatalf("selected full layout did not retain edge-less multimedias: %+v", selected)
	}
}

func TestMatchingReviewMarksUnsupportedOrIncompleteProposalUnavailable(t *testing.T) {
	groupID, memberID := uuid.New(), uuid.New()
	for _, test := range []struct {
		name     string
		proposal string
	}{
		{name: "unsupported version", proposal: `{"version":2,"group_id":"` + groupID.String() + `","files":[{"file_member_id":"` + memberID.String() + `","raw_tags":{},"diagnostics":[]}]}`},
		{name: "incomplete selected layout", proposal: `{"version":1,"group_id":"` + groupID.String() + `","selected_release":{"provider_release_id":"` + uuid.NewString() + `","release_id":"release","tracklist_complete":false,"positions":[]},"files":[{"file_member_id":"` + memberID.String() + `","raw_tags":{},"diagnostics":[]}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(map[string]any{"review_proposal": json.RawMessage(test.proposal)})
			if err != nil {
				t.Fatal(err)
			}
			got, err := NewMatchingReviewService(matchingReviewSnapshotReader{snapshot: &persistence.IncomingMatchReviewSnapshot{
				Header: &persistence.IncomingMatchEvaluationHeader{EvaluationEvidence: encoded}, Members: []uuid.UUID{memberID},
			}}).Get(context.Background(), groupID)
			if err != nil {
				t.Fatalf("review should remain readable: %v", err)
			}
			if !got.Evaluated || got.ProposalAvailable || len(got.Members) != 1 {
				t.Fatalf("invalid proposal was not safely marked unavailable: %+v", got)
			}
		})
	}
}

func TestMatchingReviewDistinguishesUnevaluatedAndEvaluatedEmptyAndKeepsEdgeLessMembers(t *testing.T) {
	groupID := uuid.New()
	unevaluated, err := NewMatchingReviewService(matchingReviewSnapshotReader{}).Get(context.Background(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	if unevaluated.Evaluated || len(unevaluated.Candidates) != 0 || len(unevaluated.Members) != 0 {
		t.Fatalf("unevaluated group response = %+v", unevaluated)
	}

	evaluatedEmpty, err := NewMatchingReviewService(matchingReviewSnapshotReader{snapshot: &persistence.IncomingMatchReviewSnapshot{
		Header:     &persistence.IncomingMatchEvaluationHeader{EvaluationRevision: "empty-revision", EvaluationEvidence: json.RawMessage(`{}`)},
		Candidates: []persistence.IncomingMatchCandidate{}, Members: []uuid.UUID{},
	}}).Get(context.Background(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	if !evaluatedEmpty.Evaluated || evaluatedEmpty.EvaluationRevision != "empty-revision" || evaluatedEmpty.ProposalAvailable || len(evaluatedEmpty.Candidates) != 0 || len(evaluatedEmpty.Members) != 0 {
		t.Fatalf("evaluated-empty group response = %+v", evaluatedEmpty)
	}

	memberWithCandidate, memberWithoutCandidate := uuid.New(), uuid.New()
	withEdgeLessCandidate, err := NewMatchingReviewService(matchingReviewSnapshotReader{snapshot: &persistence.IncomingMatchReviewSnapshot{
		Header:  &persistence.IncomingMatchEvaluationHeader{EvaluationRevision: "members-revision", EvaluationEvidence: json.RawMessage(`{}`)},
		Members: []uuid.UUID{memberWithCandidate, memberWithoutCandidate},
		Candidates: []persistence.IncomingMatchCandidate{{
			FileMemberID: memberWithCandidate, ProviderID: uuid.New(), ProviderSourceID: uuid.New(), ReleaseID: uuid.New(),
			Score: .7, Evidence: json.RawMessage(`{}`), Positions: []persistence.IncomingMatchPosition{},
		}},
	}}).Get(context.Background(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	if !withEdgeLessCandidate.Evaluated || len(withEdgeLessCandidate.Members) != 2 || len(withEdgeLessCandidate.Candidates) != 1 || len(withEdgeLessCandidate.Candidates[0].Edges) != 0 {
		t.Fatalf("complete evaluated member scope was not retained: %+v", withEdgeLessCandidate)
	}
}

func TestMatchingReviewRejectsDuplicateLayoutCoordinatesButAllowsRepeatedRecording(t *testing.T) {
	groupID, memberID, firstTrack, secondTrack := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	proposal := func(secondMedium int) json.RawMessage {
		value := map[string]any{
			"version": 1, "group_id": groupID,
			"selected_release": map[string]any{"provider_release_id": uuid.New(), "release_id": "release", "tracklist_complete": true,
				"positions": []any{
					map[string]any{"track_id": firstTrack, "medium": 1, "position": 1, "recording_id": "same-recording"},
					map[string]any{"track_id": secondTrack, "medium": secondMedium, "position": 1, "recording_id": "same-recording"},
				}},
			"files": []any{map[string]any{"file_member_id": memberID, "raw_tags": map[string][]string{}, "diagnostics": []any{}}},
		}
		encoded, _ := json.Marshal(map[string]any{"review_proposal": value})
		return encoded
	}
	for _, test := range []struct {
		name         string
		secondMedium int
		available    bool
	}{{"same coordinates", 1, false}, {"different medium", 2, true}} {
		t.Run(test.name, func(t *testing.T) {
			got, err := NewMatchingReviewService(matchingReviewSnapshotReader{snapshot: &persistence.IncomingMatchReviewSnapshot{
				Header: &persistence.IncomingMatchEvaluationHeader{EvaluationEvidence: proposal(test.secondMedium)}, Members: []uuid.UUID{memberID},
			}}).Get(context.Background(), groupID)
			if err != nil {
				t.Fatal(err)
			}
			if got.ProposalAvailable != test.available {
				t.Fatalf("proposal_available = %v, want %v", got.ProposalAvailable, test.available)
			}
		})
	}
}
