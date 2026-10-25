package publication

import (
	"reflect"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

func TestApplyProviderTagPatchProtectsCurrentAndOnlyAppliesExplicitKeys(t *testing.T) {
	current := map[string][]string{
		"ARTIST":   {"manual", "credit"},
		"ALBUM":    {"album"},
		"X-LEGACY": {},
		"LYRICS":   {"existing lyrics"},
	}
	patch := map[string][]string{
		"ARTIST": {"provider"},
		"TITLE":  {"track"},
		"GENRE":  {},
	}
	got, err := ApplyProviderTagPatch(current, patch, []string{"ARTIST", "MUSICBRAINZ_ARTISTID"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"ARTIST":   {"manual", "credit"},
		"ALBUM":    {"album"},
		"TITLE":    {"track"},
		"GENRE":    {},
		"X-LEGACY": {},
		"LYRICS":   {"existing lyrics"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyProviderTagPatch() = %#v, want %#v", got, want)
	}
	patch["TITLE"][0] = "changed"
	current["ALBUM"][0] = "changed"
	if got["TITLE"][0] != "track" || got["ALBUM"][0] != "album" {
		t.Fatal("result shares mutable slices with inputs")
	}
}

func TestApplyProviderTagPatchDoesNotReintroduceProtectedAbsenceOrDeleteOmissions(t *testing.T) {
	got, err := ApplyProviderTagPatch(map[string][]string{"ALBUM": {"current"}}, map[string][]string{"TITLE": {"new"}, "ARTIST": {"provider must not introduce"}}, []string{"ARTIST"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["ARTIST"]; ok {
		t.Fatal("protected absent key was introduced")
	}
	if !reflect.DeepEqual(got["ALBUM"], []string{"current"}) {
		t.Fatal("omitted provider field deleted current key")
	}
}

func TestApplyProviderTagPatchValidatesAndClonesEmptyValues(t *testing.T) {
	got, err := ApplyProviderTagPatch(map[string][]string{"COMMENT": nil, "X-EMPTY": {}, "X-NIL": nil}, map[string][]string{"GENRE": {}, "COMMENT": {"音楽", "ambient"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["GENRE"] == nil || len(got["GENRE"]) != 0 || got["X-EMPTY"] == nil || len(got["X-EMPTY"]) != 0 {
		t.Fatal("explicit empty slice was not preserved as a non-nil slice")
	}
	if value, exists := got["X-NIL"]; !exists || value != nil {
		t.Fatal("CURRENT nil slice was not preserved")
	}
	if got["COMMENT"][0] != "音楽" {
		t.Fatal("multivalue order or Unicode was not preserved")
	}

	for _, test := range []struct {
		name string
		key  string
		vals []string
	}{
		{name: "nil values", key: "GENRE", vals: nil},
		{name: "lyrics", key: "LYRICS", vals: []string{"overwrite"}},
		{name: "unknown", key: "X-UNKNOWN", vals: []string{"value"}},
		{name: "lowercase", key: "genre", vals: []string{"value"}},
		{name: "whitespace", key: " GENRE", vals: []string{"value"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ApplyProviderTagPatch(map[string][]string{}, map[string][]string{test.key: test.vals}, nil); err == nil {
				t.Fatalf("expected rejection for %q", test.key)
			}
		})
	}
	if _, err := ApplyProviderTagPatch(nil, nil, nil); err == nil {
		t.Fatal("nil current map was accepted")
	}
}

func TestPublicationTagKeysForFieldUsesApprovedMappingFootprints(t *testing.T) {
	tests := []struct {
		name  string
		scope librarymetadata.Scope
		field string
		want  []string
	}{
		{"position title", librarymetadata.ScopePosition, "title", []string{"TITLE"}},
		{"release title", librarymetadata.ScopeRelease, "title", []string{"ALBUM"}},
		{"position canonical credit", librarymetadata.ScopePosition, "artist_credit", []string{"ARTIST", "ARTISTS", "MUSICBRAINZ_ARTISTID"}},
		{"release canonical credit", librarymetadata.ScopeRelease, "artist_credit", []string{"ALBUMARTIST", "ALBUMARTISTS", "MUSICBRAINZ_ALBUMARTISTID"}},
		{"artists only", librarymetadata.ScopePosition, "artists", []string{"ARTISTS"}},
		{"release artist MBID alias", librarymetadata.ScopeRelease, "artist_mbid", []string{"MUSICBRAINZ_ALBUMARTISTID"}},
		{"release sort title", librarymetadata.ScopeRelease, "sort_title", []string{"ALBUMSORT"}},
		{"position sort title", librarymetadata.ScopePosition, "sort_title", []string{"TITLESORT"}},
		{"release album artist sort names", librarymetadata.ScopeRelease, "album_artist_sort_names", []string{"ALBUMARTISTSORT"}},
		{"position artist sort names", librarymetadata.ScopePosition, "artist_sort_names", []string{"ARTISTSORT"}},
		{"position movement footprint", librarymetadata.ScopePosition, "movement", []string{"MOVEMENTNAME", "MOVEMENTNUMBER", "MOVEMENTCOUNT"}},
		{"position work footprint", librarymetadata.ScopePosition, "works", []string{"WORK", "MUSICBRAINZ_WORKID"}},
		{"release artist sort names remains unsupported", librarymetadata.ScopeRelease, "artist_sort_names", nil},
		{"unsupported registered field", librarymetadata.ScopePosition, "album_artist_credit", nil},
		{"unregistered field", librarymetadata.ScopePosition, "lyrics", nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := PublicationTagKeysForField(test.scope, test.field); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("PublicationTagKeysForField() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestApplyProviderTagPatchProtectsCompleteOptionalFieldFootprints(t *testing.T) {
	fields := []struct {
		scope librarymetadata.Scope
		name  string
	}{
		{librarymetadata.ScopeRelease, "sort_title"},
		{librarymetadata.ScopePosition, "sort_title"},
		{librarymetadata.ScopeRelease, "album_artist_sort_names"},
		{librarymetadata.ScopePosition, "artist_sort_names"},
		{librarymetadata.ScopePosition, "movement"},
		{librarymetadata.ScopePosition, "works"},
	}
	current, patch := make(map[string][]string), make(map[string][]string)
	var protected []string
	for _, field := range fields {
		for _, key := range PublicationTagKeysForField(field.scope, field.name) {
			current[key] = []string{"local:" + key}
			patch[key] = []string{"provider:" + key}
			protected = append(protected, key)
		}
	}
	got, err := ApplyProviderTagPatch(current, patch, protected)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, current) {
		t.Fatalf("protected optional footprint changed: got=%#v want=%#v", got, current)
	}
}
