package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestFirstPublicationLyricsManualAndCurrentComparison(t *testing.T) {
	const exact = "[00:01.250] Keep bytes exactly \r\n"
	selected := &persistence.FirstPublicationSource{AssociationID: uuid.New(), AnalysedMetadata: &persistence.PublicationAnalysedMetadata{
		Tags: map[string][]string{"LYRICS": {"[00:01]automatic"}},
	}}
	lookup := &publicationLyricsLookupFixture{}
	resolver := NewPublicationLyricsResolver(&publicationLyricsSettingsFixture{enabled: true}, lookup)
	for _, tc := range []struct {
		name    string
		field   librarymetadata.FieldValue
		state   string
		warning string
		text    string
	}{
		{name: "manual value", field: librarymetadata.ManualFieldValue(librarymetadata.StateValue, exact, false), state: "value", text: exact},
		{name: "manual explicit empty", field: librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false), state: "absent"},
		{name: "manual protected absent", field: librarymetadata.ManualFieldValue(librarymetadata.StateAbsent, nil, false), state: "absent"},
		{name: "manual invalid value", field: librarymetadata.ManualFieldValue(librarymetadata.StateValue, "not synced lyrics", true), state: "unprocessable", warning: "lyrics_manual_value_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planFirstPublicationLyrics(context.Background(), resolver, selected, &persistence.PublicationInitialLyricsInput{Desired: &tc.field}, true, nil)
			if err != nil || plan.State != tc.state || plan.Lyrics != tc.text || plan.Origin != "manual" || plan.WarningCode != tc.warning {
				t.Fatalf("manual plan = %#v, %v", plan, err)
			}
			if len(lookup.metadata) != 0 {
				t.Fatalf("manual authority performed provider lookup: %#v", lookup.metadata)
			}
		})
	}

	selected.AnalysedMetadata = &persistence.PublicationAnalysedMetadata{Tags: map[string][]string{"ARTIST": {"A"}, "TITLE": {"T"}, "ALBUM": {"B"}, "LYRICS": {exact}}}
	selected.AnalysedMetadata.DurationSeconds = func() *float64 { value := 123.456789; return &value }()
	current := &persistence.LibraryPublicationCurrent{Metadata: persistence.PublicationCommitMetadata{Tags: map[string][]string{"lyrics": {exact}}}}
	plan, err := planFirstPublicationLyrics(context.Background(), resolver, selected, nil, true, current)
	if err != nil || plan.Lyrics != exact || plan.CurrentStatus != "matches" || len(lookup.metadata) != 0 {
		t.Fatalf("selected source/current exact comparison = %#v, lookup=%#v, err=%v", plan, lookup.metadata, err)
	}
	current.Metadata.Tags["lyrics"] = []string{exact + " "}
	plan, err = planFirstPublicationLyrics(context.Background(), resolver, selected, nil, true, current)
	if err != nil || plan.CurrentStatus != "differs" {
		t.Fatalf("byte-different CURRENT comparison = %#v, %v", plan, err)
	}
	current.Metadata.Tags["lyrics"] = []string{}
	plan, err = planFirstPublicationLyrics(context.Background(), resolver, selected, nil, true, current)
	if err != nil || plan.CurrentStatus != "unavailable" {
		t.Fatalf("legacy present-empty CURRENT comparison = %#v, %v", plan, err)
	}

	// With no selected-source lyrics, automatic lookup uses the saved probe
	// duration unchanged. Failure remains unknown; its warning is diagnostic,
	// not part of the semantic authority digest.
	withoutLyrics := *selected
	withoutLyrics.AnalysedMetadata = &persistence.PublicationAnalysedMetadata{Tags: map[string][]string{"ARTIST": {"A"}, "TITLE": {"T"}, "ALBUM": {"B"}}}
	withoutLyrics.AnalysedMetadata.DurationSeconds = selected.AnalysedMetadata.DurationSeconds
	failedLookup := &publicationLyricsLookupFixture{err: context.DeadlineExceeded}
	failed, err := planFirstPublicationLyrics(context.Background(), NewPublicationLyricsResolver(&publicationLyricsSettingsFixture{enabled: true}, failedLookup), &withoutLyrics, nil, true, nil)
	missingProvider, err2 := planFirstPublicationLyrics(context.Background(), NewPublicationLyricsResolver(&publicationLyricsSettingsFixture{enabled: true}, nil), &withoutLyrics, nil, true, nil)
	if err != nil || err2 != nil || failed.State != "unknown" || failed.WarningCode != "lyrics_lookup_failed" || missingProvider.WarningCode != "lyrics_provider_unavailable" || failed.SemanticAuthority != missingProvider.SemanticAuthority {
		t.Fatalf("warning must remain unknown without changing semantic digest: failure=%#v unavailable=%#v errors=%v/%v", failed, missingProvider, err, err2)
	}
	if len(failedLookup.metadata) != 1 || failedLookup.metadata[0].Duration != 123.456789 {
		t.Fatalf("lookup did not preserve frozen fractional duration: %#v", failedLookup.metadata)
	}
}
