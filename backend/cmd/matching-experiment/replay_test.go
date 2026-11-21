package main

import (
	"bytes"
	"encoding/json"
	"testing"

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
