package service

import (
	"context"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestCompareMatchingExperimentLayoutsWholeLayoutTiesAndStatuses(t *testing.T) {
	first := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	root := uuid.MustParse("00000000-0000-0000-0000-000000000010")

	input := MatchingGroupEvaluationInput{
		Members: []persistence.IncomingMatchMemberFence{{FileMemberID: first}, {FileMemberID: second}},
		Files: []IncomingFile{
			{VariantID: first, Locations: []IncomingLocation{{RootID: root, RelativePath: "album/one.flac"}}},
			{VariantID: second, Locations: []IncomingLocation{{RootID: root, RelativePath: "album/two.flac"}}},
		},
		ReleaseLayouts: []persistence.MatchingRelease{
			experimentLayoutRelease("a"), experimentLayoutRelease("b"),
			experimentLayoutRelease("c"), experimentLayoutRelease("d"),
			experimentLayoutRelease("e"),
		},
		Inputs: []MatchingInput{
			// Both members strongly prefer the same position. The whole-layout
			// optimum is tied at 1.70, below b/c at 1.75, even though the
			// per-file maximum sum (2.00) is larger.
			experimentLayoutEdge(first, "a", 0, 0, 0.90, 1.0),
			experimentLayoutEdge(first, "a", 0, 1, 0.90, 0.70),
			experimentLayoutEdge(second, "a", 0, 0, 0.90, 1.0),
			experimentLayoutEdge(second, "a", 0, 1, 0.90, 0.70),

			experimentLayoutEdge(first, "b", 0, 0, 0.90, 0.875),
			experimentLayoutEdge(first, "b", 0, 1, 0.90, 0.75),
			experimentLayoutEdge(second, "b", 0, 0, 0.90, 0.75),
			experimentLayoutEdge(second, "b", 0, 1, 0.90, 0.875),

			experimentLayoutEdge(first, "c", 0, 0, 0.90, 0.9375),
			experimentLayoutEdge(first, "c", 0, 1, 0.90, 0.75),
			experimentLayoutEdge(second, "c", 0, 0, 0.90, 0.75),
			experimentLayoutEdge(second, "c", 0, 1, 0.90, 0.8125),

			// Structurally complete, but every assignment edge is below 0.70.
			experimentLayoutEdge(first, "d", 0, 0, 0.90, 0.69),
			experimentLayoutEdge(first, "d", 0, 1, 0.90, 0.60),
			experimentLayoutEdge(second, "d", 0, 0, 0.90, 0.60),
			experimentLayoutEdge(second, "d", 0, 1, 0.90, 0.69),

			// The second member has no position edge in this layout.
			experimentLayoutEdge(first, "e", 0, 0, 0.90, 0.90),
			experimentLayoutEdge(first, "e", 0, 1, 0.90, 0.90),
		},
	}

	got, err := CompareMatchingExperimentLayouts(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Members != 2 || len(got.Layouts) != 5 {
		t.Fatalf("unexpected comparison shape: %#v", got)
	}
	byID := make(map[string]MatchingExperimentLayoutReport, len(got.Layouts))
	for _, report := range got.Layouts {
		byID[report.ReleaseID] = report
	}

	a := byID["a"]
	if !a.StructuralFull || a.ScoreStatus != MatchingExperimentScoreQualified || !a.QualifiedFull || !a.WithinLayoutTie || a.DistinctOptimalAssignments != 2 || a.Choices != nil {
		t.Fatalf("layout a should be a qualifying within-layout tie without choices: %#v", a)
	}
	if a.BestFullScore != experimentLayoutScaled(1.0)+experimentLayoutScaled(0.70) {
		t.Fatalf("layout a score = %d", a.BestFullScore)
	}
	if a.AcrossLayoutTie {
		t.Fatalf("layout a is below the best score and must not be an across-layout tie")
	}

	b := byID["b"]
	if !b.QualifiedFull || b.WithinLayoutTie || b.DistinctOptimalAssignments != 1 || !b.AcrossLayoutTie {
		t.Fatalf("layout b should be a unique across-layout tie: %#v", b)
	}
	if b.Choices[first.String()] != "b/0/0" || b.Choices[second.String()] != "b/0/1" {
		t.Fatalf("layout b choices = %#v", b.Choices)
	}
	if b.BestFullScore != experimentLayoutScaled(0.875)+experimentLayoutScaled(0.875) {
		t.Fatalf("layout b score = %d", b.BestFullScore)
	}

	c := byID["c"]
	if !c.QualifiedFull || c.WithinLayoutTie || c.DistinctOptimalAssignments != 1 || !c.AcrossLayoutTie {
		t.Fatalf("layout c should be a unique across-layout tie: %#v", c)
	}
	if c.Choices[first.String()] != "c/0/0" || c.Choices[second.String()] != "c/0/1" {
		t.Fatalf("layout c choices = %#v", c.Choices)
	}
	if c.BestFullScore != b.BestFullScore {
		t.Fatalf("across-layout tie scores differ: b=%d c=%d", b.BestFullScore, c.BestFullScore)
	}

	d := byID["d"]
	if !d.StructuralFull || d.StructuralStatus != MatchingExperimentStructuralFull || d.ScoreStatus != MatchingExperimentScoreBelowThreshold || d.QualifiedFull || d.Choices != nil || d.WithinLayoutTie {
		t.Fatalf("layout d should be structurally full and below the score floor: %#v", d)
	}
	if !d.ReleaseProfileGate {
		t.Fatalf("layout d release-profile gate is independent of the assignment floor: %#v", d)
	}

	e := byID["e"]
	if e.StructuralStatus != MatchingExperimentStructuralIncomplete || e.StructuralFull || e.ScoreStatus != MatchingExperimentScoreNotEvaluated || e.QualifiedFull || e.Choices != nil {
		t.Fatalf("layout e should be structurally incomplete: %#v", e)
	}
	if e.ReleaseProfileGate {
		t.Fatalf("layout e release-profile gate must require every member: %#v", e)
	}
	if len(e.ReleaseProfileMembers) != 2 || e.ReleaseProfileMembers[1].Present {
		t.Fatalf("layout e release-profile evidence = %#v", e.ReleaseProfileMembers)
	}

	reversed := input
	reversed.Members = append([]persistence.IncomingMatchMemberFence(nil), input.Members...)
	reversed.Files = append([]IncomingFile(nil), input.Files...)
	reversed.Inputs = append([]MatchingInput(nil), input.Inputs...)
	reversed.ReleaseLayouts = append([]persistence.MatchingRelease(nil), input.ReleaseLayouts...)
	for i := range reversed.ReleaseLayouts {
		reversed.ReleaseLayouts[i].Tracks = append([]persistence.MatchingReleaseTrack(nil), reversed.ReleaseLayouts[i].Tracks...)
		slices.Reverse(reversed.ReleaseLayouts[i].Tracks)
	}
	slices.Reverse(reversed.Members)
	slices.Reverse(reversed.Files)
	slices.Reverse(reversed.Inputs)
	slices.Reverse(reversed.ReleaseLayouts)
	repeated, err := CompareMatchingExperimentLayouts(context.Background(), reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, repeated) {
		t.Fatalf("reversing input order changed the comparison:\nforward=%#v\nreverse=%#v", got, repeated)
	}
}

func TestCompareMatchingExperimentLayoutsReusesPositionAcrossDisjointFolders(t *testing.T) {
	first := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	third := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	leftRoot := uuid.MustParse("00000000-0000-0000-0000-000000000010")
	rightRoot := uuid.MustParse("00000000-0000-0000-0000-000000000020")

	input := MatchingGroupEvaluationInput{
		Members: []persistence.IncomingMatchMemberFence{{FileMemberID: first}, {FileMemberID: second}, {FileMemberID: third}},
		Files: []IncomingFile{
			{VariantID: first, Locations: []IncomingLocation{{RootID: leftRoot, RelativePath: "left/one.flac"}}},
			{VariantID: second, Locations: []IncomingLocation{
				{RootID: leftRoot, RelativePath: "left/two.flac"},
				{RootID: rightRoot, RelativePath: "right/two.flac"},
			}},
			{VariantID: third, Locations: []IncomingLocation{{RootID: rightRoot, RelativePath: "right/three.flac"}}},
		},
		ReleaseLayouts: []persistence.MatchingRelease{experimentLayoutRelease("release")},
		Inputs: []MatchingInput{
			experimentLayoutEdge(first, "release", 0, 0, 0.90, 0.90),
			experimentLayoutEdge(first, "release", 0, 1, 0.90, 0.89),
			experimentLayoutEdge(second, "release", 0, 0, 0.90, 0.99),
			experimentLayoutEdge(second, "release", 0, 1, 0.90, 0.85),
			experimentLayoutEdge(third, "release", 0, 0, 0.90, 0.90),
			experimentLayoutEdge(third, "release", 0, 1, 0.90, 0.89),
		},
	}
	for position := 2; position < 4; position++ {
		input.ReleaseLayouts[0].Tracks = append(input.ReleaseLayouts[0].Tracks, persistence.MatchingReleaseTrack{
			ID: uuid.New(), Position: position,
		})
		input.Inputs = append(input.Inputs,
			experimentLayoutEdge(first, "release", 0, position, 0.90, 0.70),
			experimentLayoutEdge(second, "release", 0, position, 0.90, 0.70),
			experimentLayoutEdge(third, "release", 0, position, 0.90, 0.70),
		)
	}

	got, err := CompareMatchingExperimentLayouts(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Layouts) != 1 {
		t.Fatalf("unexpected layout count: %#v", got)
	}
	report := got.Layouts[0]
	if !report.StructuralFull || !report.QualifiedFull || report.ScoreStatus != MatchingExperimentScoreQualified || report.WithinLayoutTie {
		t.Fatalf("unexpected layout status: %#v", report)
	}
	if report.Choices[first.String()] != "release/0/1" ||
		report.Choices[second.String()] != "release/0/0" ||
		report.Choices[third.String()] != "release/0/1" {
		t.Fatalf("disjoint folders should reuse position 1 in the global optimum: %#v", report.Choices)
	}
	expected := experimentLayoutScaled(0.89) + experimentLayoutScaled(0.99) + experimentLayoutScaled(0.89)
	if report.BestFullScore != expected {
		t.Fatalf("layout score = %d, want %d", report.BestFullScore, expected)
	}
}

func TestMatchingExperimentFullAssignmentOutranksHigherScoringPartial(t *testing.T) {
	rows := make([]*matchingExperimentRow, 4)
	floor := experimentLayoutScaled(0.70)
	for i := range rows {
		edges := []matchingExperimentEdge{{position: i, score: floor, qualifying: true}}
		if i+1 < len(rows) {
			edges = append(edges, matchingExperimentEdge{position: i + 1, score: experimentLayoutScaled(1), qualifying: true})
		}
		byPosition := make(map[int]matchingExperimentEdge, len(edges))
		for _, edge := range edges {
			byPosition[edge.position] = edge
		}
		rows[i] = &matchingExperimentRow{edges: edges, edgeByPosition: byPosition}
	}
	choices, score, full := matchingLayoutHungarianAssignment(context.Background(), rows, 4, true, -1, -1)
	if !full || score != 4*floor || !slices.Equal(choices, []int{0, 1, 2, 3}) {
		t.Fatalf("must retain the only full assignment, got %v, %d, %t", choices, score, full)
	}
}

func experimentLayoutRelease(id string) persistence.MatchingRelease {
	return persistence.MatchingRelease{
		ProviderKey: id,
		Tracks: []persistence.MatchingReleaseTrack{
			{ID: uuid.New(), Medium: 0, Position: 0},
			{ID: uuid.New(), Medium: 0, Position: 1},
		},
	}
}

func experimentLayoutEdge(file uuid.UUID, release string, medium, position int, releaseConfidence, assignmentConfidence float64) MatchingInput {
	return MatchingInput{
		FileID: file, ReleaseID: release, Medium: medium, Track: position,
		ReleaseScore:    MatchingScore{Profile: ReleaseMatchingProfile, Confidence: releaseConfidence},
		AssignmentScore: MatchingScore{Profile: RecordingReleaseMatchingProfile, Confidence: assignmentConfidence},
	}
}

func experimentLayoutScaled(value float64) uint64 {
	return uint64(math.Ldexp(value, matchingExperimentScoreShift))
}
