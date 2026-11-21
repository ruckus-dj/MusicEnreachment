package service

import (
	"context"
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestSelectMatchingLayout(t *testing.T) {
	first := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	root := uuid.MustParse("00000000-0000-0000-0000-000000000010")

	base := func(releases ...string) MatchingGroupEvaluationInput {
		input := MatchingGroupEvaluationInput{
			Members: []persistence.IncomingMatchMemberFence{{FileMemberID: first}, {FileMemberID: second}},
			Files: []IncomingFile{
				{VariantID: first, Locations: []IncomingLocation{{RootID: root, RelativePath: "album/one.flac"}}},
				{VariantID: second, Locations: []IncomingLocation{{RootID: root, RelativePath: "album/two.flac"}}},
			},
		}
		for _, release := range releases {
			layout := experimentLayoutRelease(release)
			layout.Complete = true
			input.ReleaseLayouts = append(input.ReleaseLayouts, layout)
		}
		return input
	}
	thresholdEdges := func(release string, firstPosition, secondPosition, offDiagonal float64) []MatchingInput {
		return []MatchingInput{
			experimentLayoutEdge(first, release, 0, 0, 0.90, firstPosition),
			experimentLayoutEdge(first, release, 0, 1, 0.90, offDiagonal),
			experimentLayoutEdge(second, release, 0, 0, 0.90, offDiagonal),
			experimentLayoutEdge(second, release, 0, 1, 0.90, secondPosition),
		}
	}
	selectLayout := func(input MatchingGroupEvaluationInput, threshold float64) (MatchingLayoutSelection, error) {
		for i := range input.Inputs {
			input.Inputs[i].AssignmentScore.Factors = []MatchingFactor{{
				Component: MatchingRecordingTitle, State: MatchingFactorAvailable, Value: 0.9,
			}}
		}
		return SelectMatchingLayout(t.Context(), input, threshold)
	}

	t.Run("maximum exact sum after per-pair threshold", func(t *testing.T) {
		input := base("a", "b", "c")
		// The release-profile evidence on layout a is deliberately poor: it
		// must not veto a complete assignment that clears the pair threshold.
		aEdges := thresholdEdges("a", 0.80, 0.80, 0.60)
		for i := range aEdges {
			aEdges[i].ReleaseScore.Confidence = 0.10
		}
		input.Inputs = append(aEdges, thresholdEdges("b", 0.75, 0.75, 0.75)...)
		input.Inputs = append(input.Inputs,
			experimentLayoutEdge(first, "c", 0, 0, 0.90, 1.0),
			experimentLayoutEdge(first, "c", 0, 1, 0.90, 0.40),
			experimentLayoutEdge(second, "c", 0, 0, 0.90, 1.0),
			experimentLayoutEdge(second, "c", 0, 1, 0.90, 0.40),
		)
		got, err := selectLayout(input, 0.70)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != MatchingLayoutSelectionSelected || got.ReleaseID != "a" {
			t.Fatalf("selected layout = %#v", got)
		}
		if got.Choices[first.String()] != "a/0/0" || got.Choices[second.String()] != "a/0/1" {
			t.Fatalf("choices = %#v", got.Choices)
		}
		if math.Abs(got.Score-1.6) > 1e-12 {
			t.Fatalf("score = %v", got.Score)
		}

		rejected := base("c")
		rejected.Inputs = input.Inputs[len(input.Inputs)-4:]
		got, err = selectLayout(rejected, 0.70)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != MatchingLayoutSelectionNone {
			t.Fatalf("a below-threshold pair must reject the whole layout: %#v", got)
		}
	})

	t.Run("low values use an exact per-layout scale and release ID breaks ties", func(t *testing.T) {
		input := base("d", "e")
		input.Inputs = append(thresholdEdges("d", 0.30, 0.30, 0.10), thresholdEdges("e", 0.30, 0.30, 0.30)...)
		got, err := selectLayout(input, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != MatchingLayoutSelectionSelected || got.ReleaseID != "d" {
			t.Fatalf("equal sums must choose the lower release ID: %#v", got)
		}
		if got.Choices[first.String()] != "d/0/0" || got.Choices[second.String()] != "d/0/1" {
			t.Fatalf("choices = %#v", got.Choices)
		}
		if math.Abs(got.Score-0.6) > 1e-12 {
			t.Fatalf("score = %v", got.Score)
		}

		tied := base("e")
		tied.Inputs = thresholdEdges("e", 0.8, 1, 0)
		tied.Inputs[1].AssignmentScore.Confidence = 1
		tied.Inputs[2].AssignmentScore.Confidence = 0.8
		got, err = selectLayout(tied, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != MatchingLayoutSelectionSelected || got.ReleaseID != "e" {
			t.Fatalf("within-layout tie must select deterministically: %#v", got)
		}
		if got.Choices[first.String()] != "e/0/0" || got.Choices[second.String()] != "e/0/1" {
			t.Fatalf("tie witness = %#v", got.Choices)
		}
	})

	t.Run("dynamic range overflow fails closed", func(t *testing.T) {
		input := base("overflow")
		input.Inputs = []MatchingInput{
			experimentLayoutEdge(first, "overflow", 0, 0, 0.90, math.SmallestNonzeroFloat64),
			experimentLayoutEdge(first, "overflow", 0, 1, 0.90, 0.90),
			experimentLayoutEdge(second, "overflow", 0, 0, 0.90, math.SmallestNonzeroFloat64),
			experimentLayoutEdge(second, "overflow", 0, 1, 0.90, 0.90),
		}
		got, err := selectLayout(input, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != MatchingLayoutSelectionIndeterminate {
			t.Fatalf("overflow must be indeterminate: %#v", got)
		}
	})

	t.Run("track numbers and durations are not enough by themselves", func(t *testing.T) {
		input := base("a")
		input.Inputs = thresholdEdges("a", 1, 1, 1)
		for i := range input.Inputs {
			input.Inputs[i].AssignmentScore.Factors = []MatchingFactor{
				{Component: MatchingTrackNumber, State: MatchingFactorAvailable, Value: 1},
				{Component: MatchingRecordingDuration, State: MatchingFactorAvailable, Value: 1},
			}
		}
		result, err := SelectMatchingLayout(context.Background(), input, 0.70)
		if err != nil || result.Status != MatchingLayoutSelectionNone {
			t.Fatalf("weak evidence must not select a release: %+v, %v", result, err)
		}
	})
}
