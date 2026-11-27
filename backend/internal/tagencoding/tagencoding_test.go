package tagencoding_test

import (
	"reflect"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/tagencoding"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		wantStatus  tagencoding.Status
		wantReplace string
	}{
		{
			name:        "strong Latin-1 decoded Windows-1251",
			value:       "Punk-O-Ìàíèÿ 2",
			wantStatus:  tagencoding.StatusAutomatic,
			wantReplace: "Punk-O-Мания 2",
		},
		{
			name:        "strong standalone Cyrillic word",
			value:       "Ìàíèÿ",
			wantStatus:  tagencoding.StatusAutomatic,
			wantReplace: "Мания",
		},
		{
			name:        "another strong Cyrillic word",
			value:       "Ãðóïïà",
			wantStatus:  tagencoding.StatusAutomatic,
			wantReplace: "Группа",
		},
		{
			name:        "short Cyrillic word is automatic",
			value:       "Ìèð",
			wantStatus:  tagencoding.StatusAutomatic,
			wantReplace: "Мир",
		},
		{
			name:        "four-letter word is automatic",
			value:       "Ìàìà",
			wantStatus:  tagencoding.StatusAutomatic,
			wantReplace: "Мама",
		},
		{
			name:        "proper Cyrillic is preserved",
			value:       "Punk-O-Мания 2",
			wantStatus:  tagencoding.StatusUnchanged,
			wantReplace: "Punk-O-Мания 2",
		},
		{
			name:        "common Latin accent",
			value:       "Café del Mar",
			wantStatus:  tagencoding.StatusUnchanged,
			wantReplace: "Café del Mar",
		},
		{
			name:        "legitimate lowercase Latin accent is preserved",
			value:       "café",
			wantStatus:  tagencoding.StatusUnchanged,
			wantReplace: "café",
		},
		{
			name:        "separated Latin accents",
			value:       "Mötley Crüe",
			wantStatus:  tagencoding.StatusUnchanged,
			wantReplace: "Mötley Crüe",
		},
		{
			name:        "several separated Latin accents",
			value:       "Crème brûlée",
			wantStatus:  tagencoding.StatusUnchanged,
			wantReplace: "Crème brûlée",
		},
		{
			name:        "Latin name with accent",
			value:       "Sigur Rós",
			wantStatus:  tagencoding.StatusUnchanged,
			wantReplace: "Sigur Rós",
		},
		{
			name:        "a reversible Cyrillic run is automatic",
			value:       "ÑÑÑÑÑ",
			wantStatus:  tagencoding.StatusAutomatic,
			wantReplace: "ССССС",
		},
		{
			name:        "mixed ASCII letters no longer veto an automatic repair",
			value:       "Punk-O-Ìàíèÿ 2 / Beyoncé",
			wantStatus:  tagencoding.StatusAutomatic,
			wantReplace: "Punk-O-Мания 2 / Beyoncй",
		},
		{
			name:        "mixed Latin letters no longer veto a strong Cyrillic run",
			value:       "Café ×÷×÷×",
			wantStatus:  tagencoding.StatusAutomatic,
			wantReplace: "Cafй ЧчЧчЧ",
		},
		{
			name:        "ASCII suffix does not block a three-letter Cyrillic run",
			value:       "ÝîòLinX",
			wantStatus:  tagencoding.StatusAutomatic,
			wantReplace: "ЭотLinX",
		},
		{
			name:        "isolated Latin-1 symbol requires confirmation",
			value:       "Ìàíèÿ × 2",
			wantStatus:  tagencoding.StatusCandidate,
			wantReplace: "Мания Ч 2",
		},
		{
			name:        "compact multiplication notation requires confirmation",
			value:       "Ìàíèÿ×2",
			wantStatus:  tagencoding.StatusCandidate,
			wantReplace: "МанияЧ2",
		},
		{
			name:        "symbol inside a Cyrillic run is still a recognized name",
			value:       "×àðäûø",
			wantStatus:  tagencoding.StatusAutomatic,
			wantReplace: "Чардыш",
		},
		{
			name:        "changed non-Cyrillic glyph requires confirmation",
			value:       "Ìàíèÿ¹",
			wantStatus:  tagencoding.StatusCandidate,
			wantReplace: "Мания№",
		},
		{
			name:        "UTF-8 mojibake outside Latin-1",
			value:       "ÐŸÑƒÐ½Ðº",
			wantStatus:  tagencoding.StatusUnchanged,
			wantReplace: "ÐŸÑƒÐ½Ðº",
		},
		{
			name:        "too short to be reported",
			value:       "Ìè",
			wantStatus:  tagencoding.StatusUnchanged,
			wantReplace: "Ìè",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			detection := tagencoding.Detect(test.value)
			if detection.Status != test.wantStatus {
				t.Fatalf("status = %q, want %q", detection.Status, test.wantStatus)
			}
			if detection.Replacement != test.wantReplace {
				t.Fatalf("replacement = %q, want %q", detection.Replacement, test.wantReplace)
			}
			if test.wantStatus != tagencoding.StatusUnchanged && detection.Provenance.DetectorVersion != 3 {
				t.Fatalf("detector version = %d, want 3", detection.Provenance.DetectorVersion)
			}
		})
	}
}

func TestAnalyzeApplyUndoPreservesRawTagsAndOrder(t *testing.T) {
	raw := map[string][]string{
		"ARTIST": {"Punk-O-Ìàíèÿ 2", "Café", "Punk-O-Ìàíèÿ 2"},
		"ALBUM":  {"Мания"},
		"TITLE":  {"Punk-O-Ìàíèÿ 2", "Punk-O-Мания 2"},
	}
	snapshot := cloneTags(raw)

	report := tagencoding.Analyze(raw)
	if len(report.Automatic.Entries) != 3 {
		t.Fatalf("automatic entries = %d, want 3", len(report.Automatic.Entries))
	}
	if len(report.Candidates) != 0 {
		t.Fatalf("candidates = %d, want 0", len(report.Candidates))
	}
	if !reflect.DeepEqual(raw, snapshot) {
		t.Fatal("Analyze modified the raw tag map")
	}

	overlay, err := report.Confirm()
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	repaired, err := overlay.Apply(raw)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !reflect.DeepEqual(raw, snapshot) {
		t.Fatal("Apply modified the raw tag map")
	}
	want := map[string][]string{
		"ARTIST": {"Punk-O-Мания 2", "Café", "Punk-O-Мания 2"},
		"ALBUM":  {"Мания"},
		"TITLE":  {"Punk-O-Мания 2", "Punk-O-Мания 2"},
	}
	if !reflect.DeepEqual(repaired, want) {
		t.Fatalf("repaired tags = %#v, want %#v", repaired, want)
	}

	restored, err := overlay.Undo(repaired)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if !reflect.DeepEqual(restored, raw) {
		t.Fatalf("restored tags = %#v, want raw %#v", restored, raw)
	}
}

func TestCandidateRequiresExplicitConfirmation(t *testing.T) {
	raw := map[string][]string{"TITLE": {"Ìè Ìè Ìè"}}
	report := tagencoding.Analyze(raw)
	if len(report.Automatic.Entries) != 0 {
		t.Fatalf("automatic entries = %d, want 0", len(report.Automatic.Entries))
	}
	if len(report.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(report.Candidates))
	}
	evidence := report.Candidates[0].Provenance.Evidence
	if evidence.LongestRun != 2 || evidence.CyrillicRunes != 6 {
		t.Fatalf("candidate evidence = %+v, want distributed runs", evidence)
	}

	pending, err := report.Confirm()
	if err != nil {
		t.Fatalf("Confirm pending: %v", err)
	}
	unconfirmed, err := pending.Apply(raw)
	if err != nil {
		t.Fatalf("Apply pending: %v", err)
	}
	if !reflect.DeepEqual(unconfirmed, raw) {
		t.Fatalf("pending overlay changed ambiguous value: %#v", unconfirmed)
	}

	confirmed, err := report.Confirm(0)
	if err != nil {
		t.Fatalf("Confirm candidate: %v", err)
	}
	repaired, err := confirmed.Apply(raw)
	if err != nil {
		t.Fatalf("Apply confirmed: %v", err)
	}
	want := map[string][]string{"TITLE": {"Ми Ми Ми"}}
	if !reflect.DeepEqual(repaired, want) {
		t.Fatalf("confirmed tags = %#v, want %#v", repaired, want)
	}

	report = tagencoding.Analyze(map[string][]string{
		"ALBUM":  {"Punk-O-Ìàíèÿ 2"},
		"ARTIST": {"Ãðóïïà"},
		"TITLE":  {"Ìè Ìè Ìè", "Ìàíèÿ × 2"},
	})
	if len(report.Automatic.Entries) != 2 {
		t.Fatalf("automatic entries = %d, want 2", len(report.Automatic.Entries))
	}
	if len(report.Candidates) != 2 ||
		report.Candidates[0].Original != "Ìè Ìè Ìè" ||
		report.Candidates[1].Original != "Ìàíèÿ × 2" {
		t.Fatalf("capture anchors must not promote sub-run candidates: %+v", report.Candidates)
	}
	for _, candidate := range report.Candidates {
		if candidate.Provenance.DetectorVersion != 3 {
			t.Fatalf("candidate detector version = %d, want 3", candidate.Provenance.DetectorVersion)
		}
		if candidate.Provenance.Evidence.CaptureAnchors != 0 {
			t.Fatalf("candidate gained capture anchors below a three-letter run: %+v", candidate)
		}
	}
}

func TestGroupCorroborationStillRequiresConfirmation(t *testing.T) {
	group := tagencoding.AnalyzeGroup(
		map[string][]string{"TITLE": {"à× Ìèð"}},
		map[string][]string{"TITLE": {"× Ìèð"}},
	)
	if len(group.Files[1].Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(group.Files[1].Candidates))
	}
	candidate := group.Files[1].Candidates[0]
	if candidate.Provenance.Evidence.GroupMatches == 0 {
		t.Fatal("candidate was not corroborated by the strong signature")
	}
	if candidate.Provenance.DetectorVersion != 3 {
		t.Fatalf("candidate detector version = %d, want 3", candidate.Provenance.DetectorVersion)
	}

	pending, err := group.Files[1].Confirm()
	if err != nil {
		t.Fatalf("Confirm pending: %v", err)
	}
	unconfirmed, err := pending.Apply(map[string][]string{"TITLE": {"× Ìèð"}})
	if err != nil {
		t.Fatalf("Apply pending: %v", err)
	}
	if got := unconfirmed["TITLE"][0]; got != "× Ìèð" {
		t.Fatalf("pending group value = %q, want unchanged", got)
	}

	confirmed, err := group.Files[1].ConfirmCorroborated()
	if err != nil {
		t.Fatalf("ConfirmCorroborated: %v", err)
	}
	repaired, err := confirmed.Apply(map[string][]string{"TITLE": {"× Ìèð"}})
	if err != nil {
		t.Fatalf("Apply corroborated: %v", err)
	}
	if got := repaired["TITLE"][0]; got != "Ч Мир" {
		t.Fatalf("confirmed group value = %q, want %q", got, "Ч Мир")
	}
}

func TestApplyRejectsStaleOverlay(t *testing.T) {
	report := tagencoding.Analyze(map[string][]string{"TITLE": {"Punk-O-Ìàíèÿ 2"}})
	overlay, err := report.Confirm()
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	_, err = overlay.Apply(map[string][]string{"TITLE": {"Punk-O-Ìàíèÿ 3"}})
	if err == nil {
		t.Fatal("Apply accepted a stale overlay")
	}
}

func cloneTags(tags map[string][]string) map[string][]string {
	result := make(map[string][]string, len(tags))
	for key, values := range tags {
		result[key] = append([]string(nil), values...)
	}
	return result
}
