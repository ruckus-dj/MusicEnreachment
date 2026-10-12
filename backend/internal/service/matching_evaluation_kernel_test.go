package service

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestEvaluateMatchingGroupFromInputsSelectsUniqueReleaseAndAssignment(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	trackA, trackB := uuid.New(), uuid.New()
	files := []IncomingFile{
		{VariantID: first, Locations: []IncomingLocation{{RootID: uuid.New(), RelativePath: "album/one.flac"}}},
		{VariantID: second, Locations: []IncomingLocation{{RootID: uuid.New(), RelativePath: "album/two.flac"}}},
	}
	input := MatchingGroupEvaluationInput{
		Members: []persistence.IncomingMatchMemberFence{{FileMemberID: first}, {FileMemberID: second}},
		Files:   files,
		ReleaseLayouts: []persistence.MatchingRelease{{ProviderKey: "release", Tracks: []persistence.MatchingReleaseTrack{
			{ID: trackA, Medium: 0, Position: 0}, {ID: trackB, Medium: 0, Position: 1},
		}}},
		Inputs: []MatchingInput{
			kernelEdge(first, "release", 0, 0, 0.9), kernelEdge(first, "release", 0, 1, 0.8),
			kernelEdge(second, "release", 0, 0, 0.8), kernelEdge(second, "release", 0, 1, 0.9),
		},
	}
	result, err := EvaluateMatchingGroupFromInputs(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != MatchingDecisionSelected || result.Outcome != MatchingDecisionOutcomeUnique || result.SelectedReleaseID != "release" || !result.Assignment.Feasible || !result.Assignment.Unique {
		t.Fatalf("unexpected unique decision: %#v", result)
	}
	if result.Assignment.Choices[first.String()] != "release/0/0" || result.Assignment.Choices[second.String()] != "release/0/1" {
		t.Fatalf("assignment did not choose unique maximum sum: %#v", result.Assignment.Choices)
	}
	if got := result.PositionsByFileRelease[matchingGroupEdgeKey{first, "release"}][0].TrackID; got != trackA && result.PositionsByFileRelease[matchingGroupEdgeKey{first, "release"}][1].TrackID != trackA {
		t.Fatalf("kernel positions do not retain frozen layout track IDs")
	}
}

func TestEvaluateMatchingGroupFromInputsReviewsTiedAndInfeasibleAssignments(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	root := uuid.New()
	files := []IncomingFile{
		{VariantID: first, Locations: []IncomingLocation{{RootID: root, RelativePath: "album/one.flac"}}},
		{VariantID: second, Locations: []IncomingLocation{{RootID: root, RelativePath: "album/two.flac"}}},
	}
	tracks := []persistence.MatchingReleaseTrack{{ID: uuid.New(), Medium: 0, Position: 0}, {ID: uuid.New(), Medium: 0, Position: 1}}
	input := MatchingGroupEvaluationInput{
		Members: []persistence.IncomingMatchMemberFence{{FileMemberID: first}, {FileMemberID: second}}, Files: files,
		ReleaseLayouts: []persistence.MatchingRelease{{ProviderKey: "release", Tracks: tracks}},
		Inputs:         []MatchingInput{kernelEdge(first, "release", 0, 0, 0.8), kernelEdge(first, "release", 0, 1, 0.8), kernelEdge(second, "release", 0, 0, 0.8), kernelEdge(second, "release", 0, 1, 0.8)},
	}
	tied, err := EvaluateMatchingGroupFromInputs(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if tied.Decision != MatchingDecisionReview || tied.Outcome != MatchingDecisionOutcomeTie || tied.Assignment.Unique {
		t.Fatalf("tied assignment was not reviewed: %#v", tied)
	}
	input.Inputs = []MatchingInput{kernelEdge(first, "release", 0, 0, 0.8), kernelEdge(second, "release", 0, 0, 0.8)}
	infeasible, err := EvaluateMatchingGroupFromInputs(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if infeasible.Decision != MatchingDecisionReview || infeasible.Outcome != MatchingDecisionOutcomeInfeasible || infeasible.Assignment.Feasible {
		t.Fatalf("folder collision was not reviewed as infeasible: %#v", infeasible)
	}
}

func TestEvaluateMatchingGroupFromInputsValidatesScopeAndReleaseEligibility(t *testing.T) {
	member, foreign := uuid.New(), uuid.New()
	track := persistence.MatchingReleaseTrack{ID: uuid.New(), Medium: 0, Position: 0}
	base := MatchingGroupEvaluationInput{
		Members:        []persistence.IncomingMatchMemberFence{{FileMemberID: member}},
		Files:          []IncomingFile{{VariantID: member, Locations: []IncomingLocation{{RootID: uuid.New(), RelativePath: "album/song.flac"}}}},
		ReleaseLayouts: []persistence.MatchingRelease{{ProviderKey: "release", Tracks: []persistence.MatchingReleaseTrack{track}}},
		Inputs:         []MatchingInput{kernelEdge(member, "release", 0, 0, 0.70)},
	}
	result, err := EvaluateMatchingGroupFromInputs(context.Background(), base)
	if err != nil || result.Decision != MatchingDecisionSelected {
		t.Fatalf("exact release eligibility threshold: result=%#v err=%v", result, err)
	}
	base.Inputs[0].ReleaseScore.Confidence = 0.699999
	result, err = EvaluateMatchingGroupFromInputs(context.Background(), base)
	if err != nil || result.AllMembersReleaseReady || result.Decision != MatchingDecisionReview {
		t.Fatalf("below-threshold release should remain review: result=%#v err=%v", result, err)
	}
	base.Inputs[0] = kernelEdge(member, "unknown", 0, 0, 0.8)
	if _, err := EvaluateMatchingGroupFromInputs(context.Background(), base); err == nil || !strings.Contains(err.Error(), "unknown release") {
		t.Fatalf("unknown release edge error = %v", err)
	}
	base.Inputs = []MatchingInput{kernelEdge(member, "release", 0, 0, 0.8), kernelEdge(member, "release", 0, 0, 0.7)}
	if _, err := EvaluateMatchingGroupFromInputs(context.Background(), base); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate edge error = %v", err)
	}
	base.Inputs = []MatchingInput{kernelEdge(foreign, "release", 0, 0, 0.8)}
	if _, err := EvaluateMatchingGroupFromInputs(context.Background(), base); err == nil || !strings.Contains(err.Error(), "foreign file") {
		t.Fatalf("foreign edge error = %v", err)
	}
}

func TestEvaluateMatchingGroupFromInputsRejectsMalformedScoresAndLayoutsBeforeSelection(t *testing.T) {
	member, trackID := uuid.New(), uuid.New()
	base := MatchingGroupEvaluationInput{
		Members:        []persistence.IncomingMatchMemberFence{{FileMemberID: member}},
		Files:          []IncomingFile{{VariantID: member, Locations: []IncomingLocation{{RootID: uuid.New(), RelativePath: "album/song.flac"}}}},
		ReleaseLayouts: []persistence.MatchingRelease{{ProviderKey: "release", Tracks: []persistence.MatchingReleaseTrack{{ID: trackID, Medium: 0, Position: 0}}}},
		Inputs:         []MatchingInput{kernelEdge(member, "release", 0, 0, .69)},
	}
	tests := []struct {
		name   string
		mutate func(*MatchingGroupEvaluationInput)
	}{
		{"foreign release profile below threshold", func(input *MatchingGroupEvaluationInput) {
			input.Inputs[0].ReleaseScore.Profile = RecordingReleaseMatchingProfile
		}},
		{"foreign assignment profile below threshold", func(input *MatchingGroupEvaluationInput) {
			input.Inputs[0].AssignmentScore.Profile = ReleaseMatchingProfile
		}},
		{"NaN release confidence", func(input *MatchingGroupEvaluationInput) { input.Inputs[0].ReleaseScore.Confidence = math.NaN() }},
		{"infinite assignment confidence", func(input *MatchingGroupEvaluationInput) { input.Inputs[0].AssignmentScore.Confidence = math.Inf(1) }},
		{"negative release confidence", func(input *MatchingGroupEvaluationInput) { input.Inputs[0].ReleaseScore.Confidence = -0.01 }},
		{"over-range assignment confidence", func(input *MatchingGroupEvaluationInput) { input.Inputs[0].AssignmentScore.Confidence = 1.01 }},
		{"negative medium", func(input *MatchingGroupEvaluationInput) { input.ReleaseLayouts[0].Tracks[0].Medium = -1 }},
		{"negative position", func(input *MatchingGroupEvaluationInput) { input.ReleaseLayouts[0].Tracks[0].Position = -1 }},
		{"duplicate track UUID", func(input *MatchingGroupEvaluationInput) {
			input.ReleaseLayouts = append(input.ReleaseLayouts, persistence.MatchingRelease{ProviderKey: "other", Tracks: []persistence.MatchingReleaseTrack{{ID: trackID, Medium: 0, Position: 0}}})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			input.Inputs = append([]MatchingInput(nil), base.Inputs...)
			input.ReleaseLayouts = append([]persistence.MatchingRelease(nil), base.ReleaseLayouts...)
			input.ReleaseLayouts[0].Tracks = append([]persistence.MatchingReleaseTrack(nil), base.ReleaseLayouts[0].Tracks...)
			test.mutate(&input)
			if _, err := EvaluateMatchingGroupFromInputs(context.Background(), input); err == nil {
				t.Fatal("malformed kernel input was accepted")
			}
		})
	}
}

func TestEvaluateMatchingGroupFromInputsSortsInfeasibleCandidatesDeterministically(t *testing.T) {
	first := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	root := uuid.New()
	files := []IncomingFile{
		{VariantID: first, Locations: []IncomingLocation{{RootID: root, RelativePath: "album/one.flac"}}},
		{VariantID: second, Locations: []IncomingLocation{{RootID: root, RelativePath: "album/two.flac"}}},
	}
	release := persistence.MatchingRelease{ProviderKey: "release", Tracks: []persistence.MatchingReleaseTrack{{ID: uuid.New(), Medium: 0, Position: 0}}}
	input := MatchingGroupEvaluationInput{
		Members:        []persistence.IncomingMatchMemberFence{{FileMemberID: first}, {FileMemberID: second}},
		Files:          files,
		ReleaseLayouts: []persistence.MatchingRelease{release},
		Inputs: []MatchingInput{
			kernelEdge(first, "release", 0, 0, 0.8),
			kernelEdge(second, "release", 0, 0, 0.8),
		},
	}
	result, err := EvaluateMatchingGroupFromInputs(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != MatchingDecisionReview || result.Outcome != MatchingDecisionOutcomeInfeasible || result.Assignment.Feasible {
		t.Fatalf("shared folder collision was not infeasible: %#v", result)
	}
	if len(result.AssignmentCandidates) != 2 {
		t.Fatalf("infeasible selection did not retain both candidate identities: %#v", result.AssignmentCandidates)
	}
	if result.AssignmentCandidates[0].FileVariantID != first.String() || result.AssignmentCandidates[1].FileVariantID != second.String() {
		t.Fatalf("assignment candidates were not sorted by file variant then candidate: %#v", result.AssignmentCandidates)
	}

	reversed := input
	reversed.Members = []persistence.IncomingMatchMemberFence{{FileMemberID: second}, {FileMemberID: first}}
	reversed.Files = []IncomingFile{files[1], files[0]}
	reversed.Inputs = []MatchingInput{input.Inputs[1], input.Inputs[0]}
	other, err := EvaluateMatchingGroupFromInputs(context.Background(), reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, other) {
		t.Fatalf("reversing input order changed the kernel result:\nforward=%#v\nreverse=%#v", result, other)
	}
	if other.Assignment.Choices != nil {
		t.Fatalf("infeasible selection must not choose an assignment: %#v", other.Assignment.Choices)
	}
}

func kernelEdge(file uuid.UUID, release string, medium, position int, confidence float64) MatchingInput {
	return MatchingInput{
		FileID: file, ReleaseID: release, Medium: medium, Track: position,
		ReleaseScore:    MatchingScore{Profile: ReleaseMatchingProfile, Confidence: confidence},
		AssignmentScore: MatchingScore{Profile: RecordingReleaseMatchingProfile, Confidence: confidence},
	}
}
