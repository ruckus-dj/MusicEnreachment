package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func TestFrozenPunkReplaySeparatesShortlistAndCorrectedControl(t *testing.T) {
	t.Parallel()
	opts := options{input: "testdata/punk-o-mania-2.json", stage: "shortlist", limit: 1}
	var out bytes.Buffer
	if err := run(t.Context(), opts, &out); err != nil {
		t.Fatal(err)
	}
	var shortlist report
	if err := json.Unmarshal(out.Bytes(), &shortlist); err != nil {
		t.Fatal(err)
	}
	if shortlist.ReadyFiles != 21 || len(shortlist.Ranking) != 2 ||
		shortlist.Ranking[0].ReleaseID != shortlist.ExpectedID || shortlist.ExpectedStatus != "shortlisted" ||
		shortlist.ScoredPairs != 0 || shortlist.Compared != nil || shortlist.MemoryControl != nil {
		t.Fatalf("unexpected shortlist-only replay: %+v", shortlist)
	}
	if _, exists := shortlist.Timings["tracklist_decode"]; exists {
		t.Fatal("shortlist replay decoded a tracklist")
	}

	out.Reset()
	opts.stage = "all"
	opts.limit = 10
	if err := run(t.Context(), opts, &out); err != nil {
		t.Fatal(err)
	}
	var computed report
	if err := json.Unmarshal(out.Bytes(), &computed); err != nil {
		t.Fatal(err)
	}
	if computed.Compared == nil || computed.MemoryControl == nil || !computed.CachedVerified ||
		len(computed.Compared.Layouts) != 2 || len(computed.MemoryControl.Layouts) != 1 {
		t.Fatalf("expected two original layouts and a separate corrected control: %+v", computed)
	}
	original := computed.Compared.Layouts[0]
	control := computed.MemoryControl.Layouts[0]
	if original.ReleaseID != computed.ExpectedID || original.ScoreStatus != service.MatchingExperimentScoreBelowThreshold ||
		control.ReleaseID != original.ReleaseID || !control.QualifiedFull || control.ReleaseProfileGate {
		t.Fatalf("incorrect original/control separation: %v, %v", original.ScoreStatus, control.ScoreStatus)
	}
}

func TestFrozenPunkPolicyStageRequiresCompleteProviderLayout(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/punk-o-mania-2.json")
	if err != nil {
		t.Fatal(err)
	}
	var input fixture
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	originalTags := make([][]byte, len(input.Captures))
	for i := range input.Captures {
		originalTags[i] = bytes.Clone(input.Captures[i].Tags)
	}

	opts := options{stage: "policy", limit: 1, threshold: 0.70}
	computed, err := evaluate(t.Context(), input, opts)
	if err != nil {
		t.Fatal(err)
	}
	if computed.LayoutPolicy == nil {
		t.Fatal("policy stage produced no layout result")
	}
	stage := computed.LayoutPolicy.MatchingLayoutStage
	if stage.PolicyVersion != service.MatchingLayoutPolicyVersion ||
		stage.Decision.Status != service.MatchingLayoutSelectionSelected ||
		stage.ProviderReleaseID == nil || stage.Decision.ReleaseID != input.ExpectedReleaseID ||
		len(stage.Assignments) != 21 {
		t.Fatalf("full replay did not select the expected edition: %+v", stage)
	}
	layouts, missing, err := readLayouts(input.Layouts, []string{input.ExpectedReleaseID})
	if err != nil || len(missing) != 0 {
		t.Fatalf("read expected edition: missing %v, %v", missing, err)
	}
	release := layouts[0]
	trackIDs := make(map[uuid.UUID]bool, len(release.Tracks))
	for _, track := range release.Tracks {
		trackIDs[track.ID] = true
	}
	assignedFiles := make(map[uuid.UUID]bool, len(stage.Assignments))
	for _, assignment := range stage.Assignments {
		if assignment.ProviderTrackID == uuid.Nil || !trackIDs[assignment.ProviderTrackID] || assignedFiles[assignment.FileMemberID] {
			t.Fatalf("duplicate member or unknown provider position: %+v", assignment)
		}
		assignedFiles[assignment.FileMemberID] = true
	}

	// The overlay is applied in memory: the saved raw capture bytes are still
	// the input evidence, not a rewritten copy of the corrections.
	for i := range input.Captures {
		if !bytes.Equal(originalTags[i], input.Captures[i].Tags) {
			t.Fatalf("raw tags for %s were rewritten by the policy stage", input.Captures[i].VariantID)
		}
		if len(input.Captures[i].TagEncoding) != 0 {
			t.Fatalf("policy stage wrote saved tag encoding back to %s", input.Captures[i].VariantID)
		}
	}
	// Reversing the capture order must not change the deterministic stage.
	reversed := input
	reversed.Captures = slices.Clone(input.Captures)
	slices.Reverse(reversed.Captures)
	mirror := reversed.Captures[0]
	mirror.LocationID, mirror.WorkID = uuid.New(), uuid.New()
	mirror.RelativePath = "mirror/" + mirror.RelativePath
	reversed.Captures = append(reversed.Captures, mirror)
	repeated, err := evaluate(t.Context(), reversed, opts)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.LayoutPolicy == nil ||
		!reflect.DeepEqual(repeated.LayoutPolicy.MatchingLayoutStage, stage) {
		t.Fatalf("reordering and mirroring a location changed the layout:\nfirst: %+v\nreplay: %+v", stage, repeated.LayoutPolicy)
	}

	// Deleting a single position leaves high-scoring pairs for the other files.
	// This is still not a complete layout.
	release.Tracks = release.Tracks[:len(release.Tracks)-1]
	truncatedRaw, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	incomplete := input
	incomplete.Layouts = maps.Clone(input.Layouts)
	incomplete.Layouts[input.ExpectedReleaseID] = truncatedRaw
	partial, err := evaluate(t.Context(), incomplete, opts)
	if err != nil {
		t.Fatal(err)
	}
	if partial.LayoutPolicy == nil {
		t.Fatal("incomplete policy replay produced no layout result")
	}
	partialStage := partial.LayoutPolicy.MatchingLayoutStage
	if partialStage.Decision.Status != service.MatchingLayoutSelectionNone ||
		len(partialStage.Assignments) != 0 || partialStage.ProviderReleaseID != nil ||
		partialStage.ScoredPairs == 0 {
		t.Fatalf("physically incomplete snapshot did not fail closed: %+v", partialStage)
	}

	// A saved undo remains authoritative; replay must not synthesize repairs
	// on captures from a fixture that supplies persisted correction state.
	input.SavedTagEncoding = map[uuid.UUID]json.RawMessage{
		*input.Captures[0].CaptureAnalysisID: json.RawMessage(
			`{"version":1,"automatic":{"version":1,"entries":[]},"candidates":[],"user_revision":"undo"}`,
		),
	}
	undone, err := evaluate(t.Context(), input, opts)
	if err != nil || undone.LayoutPolicy == nil || undone.LayoutPolicy.RestoredValues != 0 ||
		undone.LayoutPolicy.Decision.Status == service.MatchingLayoutSelectionSelected {
		t.Fatalf("saved correction state was replaced by fresh detection: %+v, %v", undone.LayoutPolicy, err)
	}
}
