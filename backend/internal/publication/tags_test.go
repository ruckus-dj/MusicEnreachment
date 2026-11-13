package publication

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

func TestProjectTagsPreservesCreditsAndOrderedArtistIDs(t *testing.T) {
	idOne, idTwo := uuid.NewString(), uuid.NewString()
	result, err := ProjectTags(TagProjectionInput{
		Release: ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{
			{ArtistName: "A", CreditName: "A", JoinPhrase: " & ", MBID: &idOne},
			{ArtistName: "B", CreditName: "Featured B", JoinPhrase: "", MBID: &idTwo},
		}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{
			{ArtistName: "A", CreditName: "The A", JoinPhrase: " feat. ", MBID: &idOne},
			{ArtistName: "B", CreditName: "B credited", JoinPhrase: "", MBID: &idTwo},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"ARTIST": {"The A feat. B credited"}, "ARTISTS": {"A", "B"}, "MUSICBRAINZ_ARTISTID": {idOne, idTwo},
		"ARTISTSORT": {"A", "B"}, "ARTISTSSORT": {"A", "B"},
		"ALBUMARTIST": {"A & Featured B"}, "ALBUMARTISTS": {"A", "B"}, "MUSICBRAINZ_ALBUMARTISTID": {idOne, idTwo},
		"ALBUMARTISTSORT": {"A", "B"}, "ALBUMARTISTSSORT": {"A", "B"},
		"ALBUM": {"Album"}, "TITLE": {"Song"},
	}
	if !reflect.DeepEqual(result.Tags, want) {
		t.Fatalf("tags = %#v, want %#v", result.Tags, want)
	}
}

func TestProjectTagsArtistIdentityControlsOrderedValueCollisions(t *testing.T) {
	id := "artist-one"
	result, err := ProjectTags(TagProjectionInput{
		Release: ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{
			{ArtistID: id, ArtistName: "Artist", SortName: "Artist, A", MBID: ptrString("mbid-one")},
			{ArtistID: id, ArtistName: "Artist", SortName: "Artist, A", MBID: ptrString("mbid-one")},
		}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistID: "track-artist", ArtistName: "Track Artist"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string][]string{
		"ALBUMARTISTS": {"Artist", "Artist"}, "MUSICBRAINZ_ALBUMARTISTID": {"mbid-one", "mbid-one"},
		"ALBUMARTISTSORT": {"Artist, A", "Artist, A"},
	} {
		if !reflect.DeepEqual(result.Tags[key], want) {
			t.Errorf("%s = %#v, want %#v", key, result.Tags[key], want)
		}
	}

	resolved, err := ProjectTags(TagProjectionInput{
		Release: ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{
			{ArtistID: "artist-a", ArtistName: "Same", Disambiguation: "first", MBID: ptrString("mbid-a")},
			{ArtistID: "artist-b", ArtistName: "Same", Disambiguation: "second", MBID: ptrString("mbid-b")},
		}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistID: "track-artist", ArtistName: "Track Artist"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Same (first)", "Same (second)"}; !reflect.DeepEqual(resolved.Tags["ALBUMARTISTS"], want) {
		t.Errorf("disambiguated album artists = %#v, want %#v", resolved.Tags["ALBUMARTISTS"], want)
	}

	unresolved, err := ProjectTags(TagProjectionInput{
		Release: ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{
			{ArtistID: "artist-a", ArtistName: "Same", Disambiguation: "first", MBID: ptrString("mbid-a")},
			{ArtistID: "artist-b", ArtistName: "Same", Disambiguation: "second"},
		}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistID: "track-artist", ArtistName: "Track Artist"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := unresolved.Tags["ALBUMARTISTS"]; got == nil || len(got) != 0 {
		t.Errorf("incomplete artist MBIDs emitted ordered names: %#v", got)
	}
	if got := unresolved.Tags["MUSICBRAINZ_ALBUMARTISTID"]; got == nil || len(got) != 0 {
		t.Errorf("incomplete artist MBIDs emitted IDs: %#v", got)
	}

	sortCollision, err := ProjectTags(TagProjectionInput{
		Release: ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{
			{ArtistID: "artist-a", ArtistName: "A", SortName: "Shared"},
			{ArtistID: "artist-b", ArtistName: "B", SortName: "Shared"},
		}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistID: "track-artist", ArtistName: "Track Artist"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := sortCollision.Tags["ALBUMARTISTSORT"]; got == nil || len(got) != 0 {
		t.Errorf("distinct artists with colliding sort names = %#v", got)
	}
}

func TestProjectTagsUsesDistinctRecordingAndReleaseTrackIDs(t *testing.T) {
	recording, releaseTrack := uuid.New(), uuid.New()
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, RecordingID: &recording, ReleaseTrackID: &releaseTrack},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Tags["MUSICBRAINZ_TRACKID"]; !reflect.DeepEqual(got, []string{recording.String()}) {
		t.Fatalf("recording ID = %#v", got)
	}
	if got := result.Tags["MUSICBRAINZ_RELEASETRACKID"]; !reflect.DeepEqual(got, []string{releaseTrack.String()}) {
		t.Fatalf("release-track ID = %#v", got)
	}
}

func TestProjectTagsExplicitEmptyFalseCompilationAndPartialArtistIDs(t *testing.T) {
	falseValue := false
	a, b := uuid.NewString(), uuid.NewString()
	result, err := ProjectTags(TagProjectionInput{
		Release: ReleaseTags{
			Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Compilation: &falseValue,
			Desired: &librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease, Desired: map[string]librarymetadata.FieldValue{
				"genre":       librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false),
				"compilation": librarymetadata.ManualFieldValue(librarymetadata.StateValue, falseValue, true),
			}},
		},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{
			{ArtistName: "A", MBID: &a}, {ArtistName: "B", MBID: nil}, {ArtistName: "C", MBID: &b},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Tags["GENRE"]; got == nil || len(got) != 0 {
		t.Fatalf("explicit empty genre = %#v", got)
	}
	if got := result.Tags["COMPILATION"]; !reflect.DeepEqual(got, []string{"0"}) {
		t.Fatalf("false compilation = %#v", got)
	}
	if got, exists := result.Tags["MUSICBRAINZ_ARTISTID"]; !exists || got == nil || len(got) != 0 {
		t.Fatalf("partial IDs did not explicitly clear the ID footprint: %#v", result.Tags["MUSICBRAINZ_ARTISTID"])
	}
	if result.Fields["artist_credit.mbids"].Status != "unsupported" {
		t.Fatalf("missing MBID status = %#v", result.Fields["artist_credit.mbids"])
	}
}

func TestProjectTagsRequiresA1Fields(t *testing.T) {
	_, err := ProjectTags(TagProjectionInput{Release: ReleaseTags{Title: "Album"}, Position: PositionTags{Title: "Song"}})
	if err == nil {
		t.Fatal("expected required artists to block projection")
	}
}

func TestProjectTagsSupportsDecodedEnvelopeValues(t *testing.T) {
	release, err := librarymetadata.DecodeEnvelope([]byte(`{"version":1,"scope":"release","desired":{"genre":{"state":"value","value":[{"id":"11111111-1111-4111-8111-111111111111","source_name":"jazz","name":"Jazz"},{"id":"22222222-2222-4222-8222-222222222222","source_name":"fusion","name":"Fusion"}],"authority":"manual","provenance":{"kind":"manual"}},"release_secondary_types":{"state":"value","value":["Live","Compilation"],"authority":"manual","provenance":{"kind":"manual"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	position, err := librarymetadata.DecodeEnvelope([]byte(`{"version":1,"scope":"position","desired":{"bpm":{"state":"value","value":123.450,"authority":"manual","provenance":{"kind":"manual"}},"disc_number":{"state":"value","value":2,"authority":"manual","provenance":{"kind":"manual"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &release},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist", SortName: "Zed"}, {ArtistName: "Ångström", SortName: "Ångström"}}, Desired: &position},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"GENRE": {"Jazz", "Fusion"}, "RELEASETYPES": {"Live", "Compilation"},
		"BPM": {"123.450"}, "DISCNUMBER": {"2"},
	}
	for key, values := range want {
		if !reflect.DeepEqual(result.Tags[key], values) {
			t.Errorf("%s = %#v, want %#v", key, result.Tags[key], values)
		}
	}
}

func TestProjectTagsProjectsOptionalSortMovementAndWorkMappings(t *testing.T) {
	workID := uuid.NewString()
	position := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition,
		Desired: map[string]librarymetadata.FieldValue{
			"sort_title":        librarymetadata.ManualFieldValue(librarymetadata.StateValue, "Été, Pt. 2", true),
			"artist_sort_names": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []string{"Zed", "Ångström"}, true),
			"movement":          librarymetadata.ManualFieldValue(librarymetadata.StateValue, librarymetadata.Movement{Name: "序曲", Number: 2, Count: 7}, true),
			"works":             librarymetadata.ManualFieldValue(librarymetadata.StateValue, []librarymetadata.Work{{Name: "作品一", MBID: &workID}, {Name: "作品二", MBID: ptrString(workID)}}, true),
		},
	}
	release := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
		Desired: map[string]librarymetadata.FieldValue{
			"sort_title":              librarymetadata.ManualFieldValue(librarymetadata.StateValue, "Album, The", true),
			"album_artist_sort_names": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []string{"Artist, A", "Artist, B"}, true),
		},
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist", SortName: "Album Artist Sort"}}, Desired: &release},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist", SortName: "Track Artist Sort"}, {ArtistName: "Ångström", SortName: "Ångström"}}, Desired: &position},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"ALBUMSORT": {"Album, The"}, "TITLESORT": {"Été, Pt. 2"},
		"ALBUMARTISTSORT": {"Album Artist Sort"}, "ALBUMARTISTSSORT": {"Album Artist Sort"}, "ARTISTSORT": {"Track Artist Sort", "Ångström"}, "ARTISTSSORT": {"Track Artist Sort", "Ångström"},
		"MOVEMENTNAME": {"序曲"}, "MOVEMENTNUMBER": {"2"}, "MOVEMENTCOUNT": {"7"},
		"WORK": {"作品一", "作品二"}, "MUSICBRAINZ_WORKID": {workID, workID},
	}
	for key, values := range want {
		if !reflect.DeepEqual(result.Tags[key], values) {
			t.Errorf("%s = %#v, want %#v", key, result.Tags[key], values)
		}
	}
	if result.Fields["position.works"].Status != "represented" || result.Fields["position.works.mbids"].Status != "represented" {
		t.Fatalf("work statuses = %#v / %#v", result.Fields["position.works"], result.Fields["position.works.mbids"])
	}
}

func TestProjectTagsDecodedStructuredValuesAndIncompleteWorkIDs(t *testing.T) {
	position, err := librarymetadata.DecodeEnvelope([]byte(`{"version":1,"scope":"position","desired":{"movement":{"state":"value","value":{"name":"Suite","number":1,"count":3},"authority":"manual","provenance":{"kind":"manual"}},"works":{"state":"value","value":[{"name":"Work α","mbid":"11111111-1111-4111-8111-111111111111"},{"name":"Work β"}],"authority":"manual","provenance":{"kind":"manual"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &position},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Tags["WORK"], []string{"Work α", "Work β"}) {
		t.Fatalf("work names = %#v", result.Tags["WORK"])
	}
	if _, exists := result.Tags["MUSICBRAINZ_WORKID"]; exists {
		t.Fatalf("partial work IDs emitted: %#v", result.Tags["MUSICBRAINZ_WORKID"])
	}
	if result.Fields["position.works"].Status != "represented" || result.Fields["position.works.mbids"].Status != "unsupported" {
		t.Fatalf("partial work results = %#v / %#v", result.Fields["position.works"], result.Fields["position.works.mbids"])
	}
}

func TestProjectTagsOptionalMappingsAbsentAndExplicitEmpty(t *testing.T) {
	position := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition,
		Desired: map[string]librarymetadata.FieldValue{
			"sort_title":       librarymetadata.ManualFieldValue(librarymetadata.StateAbsent, nil, false),
			"contributors":     librarymetadata.ManualFieldValue(librarymetadata.StateAbsent, nil, false),
			"content_advisory": librarymetadata.ManualFieldValue(librarymetadata.StateAbsent, nil, false),
			"movement":         librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false),
			"works":            librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false),
		},
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &position},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := result.Tags["TITLESORT"]; exists || result.Fields["position.sort_title"].Status != "" {
		t.Fatalf("absent sort title took action: tags=%#v fields=%#v", result.Tags, result.Fields)
	}
	for _, key := range []string{"COMPOSER", "LYRICIST", "ITUNESADVISORY"} {
		if _, exists := result.Tags[key]; exists {
			t.Errorf("absent optional mapping %s took action: %#v", key, result.Tags[key])
		}
	}
	for _, key := range []string{"MOVEMENTNAME", "MOVEMENTNUMBER", "MOVEMENTCOUNT", "WORK", "MUSICBRAINZ_WORKID"} {
		if values, exists := result.Tags[key]; !exists || values == nil || len(values) != 0 {
			t.Errorf("explicit empty %s = %#v, exists=%t", key, values, exists)
		}
	}
}

func TestProjectTagsMapsMusicalKeyByOutputContainer(t *testing.T) {
	for _, test := range []struct {
		name      string
		container AudioContainer
		want      map[string][]string
		status    string
	}{
		{name: "MP3 uses initial key", container: AudioContainerMP3, want: map[string][]string{"INITIALKEY": {" D♭ minor "}, "KEY": {}}, status: "represented"},
		{name: "M4A uses initial key", container: AudioContainerM4A, want: map[string][]string{"INITIALKEY": {" D♭ minor "}, "KEY": {}}, status: "represented"},
		{name: "FLAC uses key", container: AudioContainerFLAC, want: map[string][]string{"KEY": {" D♭ minor "}, "INITIALKEY": {}}, status: "represented"},
		{name: "MKA uses key", container: AudioContainerMKA, want: map[string][]string{"KEY": {" D♭ minor "}, "INITIALKEY": {}}, status: "represented"},
		{name: "unknown container is unsupported", container: AudioContainer("unknown"), status: "unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			key := librarymetadata.ManualFieldValue(librarymetadata.StateValue, " D♭ minor ", true)
			position := librarymetadata.Envelope{
				Version: librarymetadata.EnvelopeVersion,
				Scope:   librarymetadata.ScopePosition,
				Desired: map[string]librarymetadata.FieldValue{"key": key},
			}
			result, err := ProjectTags(TagProjectionInput{
				Release:         ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}},
				Position:        PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &position},
				OutputContainer: test.container,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Fields["position.key"].Status != test.status {
				t.Fatalf("key field status = %#v, want %q", result.Fields["position.key"], test.status)
			}
			for key, want := range test.want {
				if !reflect.DeepEqual(result.Tags[key], want) {
					t.Errorf("%s = %#v, want %#v", key, result.Tags[key], want)
				}
			}
			if test.status == "unsupported" {
				if _, exists := result.Tags["KEY"]; exists {
					t.Errorf("unsupported container guessed KEY mapping: %#v", result.Tags)
				}
				if _, exists := result.Tags["INITIALKEY"]; exists {
					t.Errorf("unsupported container guessed INITIALKEY mapping: %#v", result.Tags)
				}
			}
		})
	}

	position := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion,
		Scope:   librarymetadata.ScopePosition,
		Desired: map[string]librarymetadata.FieldValue{"key": librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false)},
	}
	cleared, err := ProjectTags(TagProjectionInput{
		Release:         ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}},
		Position:        PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &position},
		OutputContainer: AudioContainer("unknown"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"KEY", "INITIALKEY"} {
		if values, exists := cleared.Tags[key]; !exists || values == nil || len(values) != 0 {
			t.Errorf("explicit-empty %s = %#v, exists=%t; want clear footprint", key, values, exists)
		}
	}
}

func TestProjectTagsReleaseArtistSortNameUsesArtistSortNotAlbumArtistSort(t *testing.T) {
	release := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
		Desired: map[string]librarymetadata.FieldValue{
			"artist_sort_names":       librarymetadata.ManualFieldValue(librarymetadata.StateValue, []string{"legacy artist sort"}, false),
			"album_artist_sort_names": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []string{"legacy album sort"}, false),
		},
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist", SortName: "Sort Album Artist"}}, Desired: &release},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist", SortName: "The Artist"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Tags["ARTISTSORT"], []string{"The Artist"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ARTISTSORT = %#v, want %#v", got, want)
	}
	if got, want := result.Tags["ARTISTSSORT"], []string{"The Artist"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ARTISTSSORT = %#v, want canonical artist sort name %#v", got, want)
	}
	if got, want := result.Tags["ALBUMARTISTSORT"], []string{"Sort Album Artist"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ALBUMARTISTSORT = %#v, want canonical artist sort name %#v", got, want)
	}
	if got, want := result.Tags["ALBUMARTISTSSORT"], []string{"Sort Album Artist"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ALBUMARTISTSSORT = %#v, want canonical artist sort name %#v", got, want)
	}
}

func TestProjectTagsReleaseBarcodeCountryAndStatusStates(t *testing.T) {
	for _, test := range []struct {
		name       string
		state      librarymetadata.ValueState
		value      any
		want       []string
		wantStatus string
	}{
		{name: "value", state: librarymetadata.StateValue, value: "  value preserved  ", want: []string{"  value preserved  "}, wantStatus: "represented"},
		{name: "absent", state: librarymetadata.StateAbsent},
		{name: "explicit empty", state: librarymetadata.StateExplicitEmpty, want: []string{}, wantStatus: "explicit_empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			desired := map[string]librarymetadata.FieldValue{}
			for _, field := range []string{"barcode", "release_country", "release_status"} {
				desired[field] = librarymetadata.ManualFieldValue(test.state, test.value, false)
			}
			envelope := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease, Desired: desired}
			projection, err := ProjectTags(TagProjectionInput{
				Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &envelope},
				Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			for field, key := range map[string]string{"barcode": "BARCODE", "release_country": "RELEASECOUNTRY", "release_status": "RELEASESTATUS"} {
				if test.wantStatus == "" {
					if _, exists := projection.Tags[key]; exists || projection.Fields["release."+field].Status != "" {
						t.Errorf("absent %s projected: tags=%#v field=%#v", field, projection.Tags[key], projection.Fields["release."+field])
					}
					continue
				}
				if got := projection.Tags[key]; !reflect.DeepEqual(got, test.want) {
					t.Errorf("%s = %#v, want %#v", key, got, test.want)
				}
				if got := projection.Fields["release."+field].Status; got != test.wantStatus {
					t.Errorf("%s status = %q, want %q", field, got, test.wantStatus)
				}
			}
		})
	}
}

func TestProjectTagsMapsDistinctDateAndSubtitleSemantics(t *testing.T) {
	release := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
		Desired: map[string]librarymetadata.FieldValue{
			"release_date":    librarymetadata.ManualFieldValue(librarymetadata.StateValue, "2001-02", true),
			"release_version": librarymetadata.ManualFieldValue(librarymetadata.StateValue, "Deluxe", true),
		},
	}
	position := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition,
		Desired: map[string]librarymetadata.FieldValue{
			"recording_date": librarymetadata.ManualFieldValue(librarymetadata.StateValue, "1999-03-04", true),
			"subtitle":       librarymetadata.ManualFieldValue(librarymetadata.StateValue, "Live", true),
		},
	}
	projection, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &release},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &position},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"RELEASEDATE": {"2001-02"}, "DATE": {"1999-03-04"},
		"ALBUMVERSION": {"Deluxe"}, "SUBTITLE": {"Live"},
	}
	for key, values := range want {
		if !reflect.DeepEqual(projection.Tags[key], values) {
			t.Errorf("%s = %#v, want %#v", key, projection.Tags[key], values)
		}
	}
}

func TestProjectTagsMapsContributorRolesAndAdvisory(t *testing.T) {
	position, err := librarymetadata.DecodeEnvelope([]byte(`{"version":1,"scope":"position","desired":{"contributors":{"state":"value","value":[{"name":"Family,Given 音楽","role":" Composer ","instrument":"piano","artist_id":"11111111-1111-4111-8111-111111111111"},{"name":"Second composer","role":"composer","mbid":"33333333-3333-4333-8333-333333333333"},{"name":"作詞者","role":"LYRICIST","mbid":"22222222-2222-4222-8222-222222222222"},{"name":"作詞者 two","role":"lyricist","mbid":"44444444-4444-4444-8444-444444444444"},{"name":"Director","role":"director","mbid":"55555555-5555-4555-8555-555555555555"},{"name":"Guitarist, One","role":"performer","instrument":"guitar","mbid":"66666666-6666-4666-8666-666666666666"},{"name":"Guitarist (Live)","role":"performer","instrument":"guitar","mbid":"77777777-7777-4777-8777-777777777777"},{"name":"Guitarist, One","role":"performer","instrument":"guitar","mbid":"66666666-6666-4666-8666-666666666666"},{"name":"Pianist","role":"performer","instrument":"piano","mbid":"99999999-9999-4999-8999-999999999999"},{"name":"Vocalist","role":"performer","mbid":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},{"name":"unknown role","role":"instrumentalist"}],"authority":"manual","provenance":{"kind":"manual"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	originalPosition, err := json.Marshal(position)
	if err != nil {
		t.Fatal(err)
	}
	release, err := librarymetadata.DecodeEnvelope([]byte(`{"version":1,"scope":"release","desired":{"content_advisory":{"state":"value","value":"explicit","authority":"manual","provenance":{"kind":"manual"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	input := TagProjectionInput{
		Release:         ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Album Artist"}}, Desired: &release},
		Position:        PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Track Artist"}}, Desired: &position},
		OutputContainer: AudioContainerMP3,
	}
	result, err := ProjectTags(input)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string][]string{
		"COMPOSER": {"Family,Given 音楽", "Second composer"}, "LYRICIST": {"作詞者", "作詞者 two"}, "DIRECTOR": {"Director"}, "ITUNESADVISORY": {"1"},
	} {
		if !reflect.DeepEqual(result.Tags[key], want) {
			t.Errorf("%s = %#v, want %#v", key, result.Tags[key], want)
		}
	}
	for key, want := range map[string][]string{
		"MUSICBRAINZ_LYRICISTID": {"22222222-2222-4222-8222-222222222222", "44444444-4444-4444-8444-444444444444"},
		"MUSICBRAINZ_DIRECTORID": {"55555555-5555-4555-8555-555555555555"},
	} {
		if !reflect.DeepEqual(result.Tags[key], want) {
			t.Errorf("%s = %#v, want %#v", key, result.Tags[key], want)
		}
	}
	if _, exists := result.Tags["MUSICBRAINZ_COMPOSERID"]; exists {
		t.Fatal("incomplete composer MBID group was partially projected")
	}
	if result.Fields["position.contributors"].Status != "unsupported" {
		t.Errorf("unknown role status = %#v", result.Fields["position.contributors"])
	}
	for key := range result.Tags {
		if strings.HasPrefix(key, "PERFORMER:") || strings.HasPrefix(key, "MUSICBRAINZ_PERFORMERID:") {
			t.Fatalf("performer data was projected into MP3 key %q", key)
		}
	}
	performerNotice := false
	for _, notice := range result.Notices {
		performerNotice = performerNotice || strings.Contains(notice, "omitted from MP3 output")
	}
	if result.Fields["position.contributors.performer"].Status != "unsupported" || !performerNotice {
		t.Fatalf("MP3 performer omission was not reported: fields=%#v notices=%#v", result.Fields, result.Notices)
	}
	for _, container := range []AudioContainer{AudioContainerFLAC, AudioContainerMKA} {
		input.OutputContainer = container
		containerProjection, projectErr := ProjectTags(input)
		if projectErr != nil {
			t.Fatal(projectErr)
		}
		for key, want := range map[string][]string{
			"PERFORMER:GUITAR":    {"Guitarist, One", "Guitarist (Live)", "Guitarist, One"},
			"PERFORMER:PIANO":     {"Pianist"},
			"PERFORMER:PERFORMER": {"Vocalist"},
		} {
			if !reflect.DeepEqual(containerProjection.Tags[key], want) {
				t.Errorf("%s %s = %#v, want %#v", container, key, containerProjection.Tags[key], want)
			}
		}
		for key, want := range map[string][]string{
			"MUSICBRAINZ_PERFORMERID:GUITAR":                  {"66666666-6666-4666-8666-666666666666", "77777777-7777-4777-8777-777777777777", "66666666-6666-4666-8666-666666666666"},
			"MUSICBRAINZ_PERFORMERID:PIANO":                   {"99999999-9999-4999-8999-999999999999"},
			"MUSICBRAINZ_PERFORMERID:MUSICBRAINZ_PERFORMERID": {"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		} {
			if !reflect.DeepEqual(containerProjection.Tags[key], want) {
				t.Errorf("%s %s = %#v, want %#v", container, key, containerProjection.Tags[key], want)
			}
		}
		// Notices also carry the unrelated incomplete-artist-MBID diagnostics for
		// this fixture, so only performer-specific notices are disqualifying here.
		containerPerformerNotice := false
		for _, notice := range containerProjection.Notices {
			containerPerformerNotice = containerPerformerNotice || strings.Contains(notice, "performer")
		}
		if containerPerformerNotice || containerProjection.Fields["position.contributors.performer"].Status != "represented" {
			t.Errorf("%s performer projection = %#v", container, containerProjection)
		}
		// The input also contains an unknown instrumentalist role, so the
		// aggregate contributor capability stays unsupported even though the
		// performer data itself is represented for this container.
		if got := containerProjection.Fields["position.contributors"]; got.Status != "unsupported" || !strings.Contains(got.Reason, "no approved normalized TagLib mapping") {
			t.Errorf("%s aggregate contributor status = %#v, want unsupported for the unknown instrumentalist role", container, got)
		}
	}
	input.OutputContainer = AudioContainerM4A
	m4aProjection, err := ProjectTags(input)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string][]string{
		"COMPOSER": {"Family,Given 音楽", "Second composer"}, "LYRICIST": {"作詞者", "作詞者 two"}, "DIRECTOR": {"Director"}, "ITUNESADVISORY": {"1"},
	} {
		if !reflect.DeepEqual(m4aProjection.Tags[key], want) {
			t.Errorf("M4A %s = %#v, want %#v", key, m4aProjection.Tags[key], want)
		}
	}
	for key := range m4aProjection.Tags {
		if strings.HasPrefix(key, "PERFORMER:") || strings.HasPrefix(key, "MUSICBRAINZ_PERFORMERID:") {
			t.Fatalf("performer data was projected into M4A key %q", key)
		}
	}
	m4aPerformerNotice := false
	for _, notice := range m4aProjection.Notices {
		m4aPerformerNotice = m4aPerformerNotice || strings.Contains(notice, "omitted from M4A output")
	}
	if got := m4aProjection.Fields["position.contributors.performer"]; got.Status != "unsupported" || !m4aPerformerNotice {
		t.Fatalf("M4A performer omission was not reported: fields=%#v notices=%#v", m4aProjection.Fields, m4aProjection.Notices)
	}
	currentPosition, err := json.Marshal(position)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(currentPosition, originalPosition) {
		t.Fatal("M4A projection mutated the structured contributor input")
	}
	for _, test := range []struct {
		name         string
		contributors []librarymetadata.Contributor
		reason       string
		nameKey      string
		nameWant     []string
	}{
		{
			name:     "case-folded instruments",
			reason:   "unambiguously",
			nameKey:  "PERFORMER:GUITAR",
			nameWant: []string{"Guitarist", "Guitarist two"},
			contributors: []librarymetadata.Contributor{
				{Name: "Guitarist", Role: "performer", Instrument: "guitar", MBID: ptrString("11111111-1111-4111-8111-111111111111")},
				{Name: "Guitarist two", Role: "performer", Instrument: "GUITAR", MBID: ptrString("22222222-2222-4222-8222-222222222222")},
			},
		},
		{
			name:     "empty and performer sentinel instruments",
			reason:   "unambiguously",
			nameKey:  "PERFORMER:PERFORMER",
			nameWant: []string{"Vocalist", "Vocalist two"},
			contributors: []librarymetadata.Contributor{
				{Name: "Vocalist", Role: "performer", MBID: ptrString("33333333-3333-4333-8333-333333333333")},
				{Name: "Vocalist two", Role: "performer", Instrument: "performer", MBID: ptrString("44444444-4444-4444-8444-444444444444")},
			},
		},
		{
			name:     "literal performer sentinel alone is unrepresentable",
			reason:   "unambiguously",
			nameKey:  "PERFORMER:PERFORMER",
			nameWant: []string{"Literal performer"},
			contributors: []librarymetadata.Contributor{
				{Name: "Literal performer", Role: "performer", Instrument: "performer", MBID: ptrString("55555555-5555-4555-8555-555555555555")},
			},
		},
		{
			name:     "literal MBID tag sentinel is unrepresentable",
			reason:   "unambiguously",
			nameKey:  "PERFORMER:MUSICBRAINZ_PERFORMERID",
			nameWant: []string{"Sentinel performer"},
			contributors: []librarymetadata.Contributor{
				{Name: "Sentinel performer", Role: "performer", Instrument: "musicbrainz_performerid", MBID: ptrString("66666666-6666-4666-8666-666666666666")},
			},
		},
		{
			name:     "incomplete performer groups",
			reason:   "incomplete",
			nameKey:  "PERFORMER:GUITAR",
			nameWant: []string{"Guitarist"},
			contributors: []librarymetadata.Contributor{
				{Name: "Guitarist", Role: "performer", Instrument: "guitar", MBID: ptrString("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee")},
				{Name: "Pianist", Role: "performer", Instrument: "piano"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			collisionPosition := librarymetadata.Envelope{
				Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition,
				Desired: map[string]librarymetadata.FieldValue{
					"contributors": librarymetadata.ManualFieldValue(librarymetadata.StateValue, test.contributors, true),
				},
			}
			collisionInput := input
			collisionInput.OutputContainer = AudioContainerFLAC
			collisionInput.Position.Desired = &collisionPosition
			projection, projectErr := ProjectTags(collisionInput)
			if projectErr != nil {
				t.Fatal(projectErr)
			}
			if len(projection.Tags["MUSICBRAINZ_PERFORMERID:GUITAR"]) != 0 ||
				len(projection.Tags["MUSICBRAINZ_PERFORMERID:PIANO"]) != 0 ||
				len(projection.Tags["MUSICBRAINZ_PERFORMERID:MUSICBRAINZ_PERFORMERID"]) != 0 ||
				len(projection.Tags["MUSICBRAINZ_PERFORMERID:PERFORMER"]) != 0 {
				t.Fatalf("colliding performer IDs were projected: %#v", projection.Tags)
			}
			if !reflect.DeepEqual(projection.Tags[test.nameKey], test.nameWant) {
				t.Fatalf("performer names = %#v, want %#v", projection.Tags[test.nameKey], test.nameWant)
			}
			if test.name == "incomplete performer groups" &&
				!reflect.DeepEqual(projection.Tags["PERFORMER:PIANO"], []string{"Pianist"}) {
				t.Fatalf("incomplete performer IDs removed names: %#v", projection.Tags["PERFORMER:PIANO"])
			}
			if got := projection.Fields["position.contributors.mbids"]; got.Status != "unsupported" || !strings.Contains(got.Reason, test.reason) {
				t.Fatalf("performer MBID diagnostic = %#v", got)
			}
		})
	}
	for _, test := range []struct {
		name         string
		contributors []librarymetadata.Contributor
		wantNames    []string
	}{
		{
			name: "duplicate role names with distinct MBIDs",
			contributors: []librarymetadata.Contributor{
				{Name: "Alex", Role: "composer", MBID: ptrString("66666666-6666-4666-8666-666666666666")},
				{Name: "Alex", Role: "composer", MBID: ptrString("77777777-7777-4777-8777-777777777777")},
			},
			wantNames: []string{"Alex", "Alex"},
		},
		{
			name: "trimmed duplicate names and IDs differ",
			contributors: []librarymetadata.Contributor{
				{Name: " Alex ", Role: "composer", MBID: ptrString("88888888-8888-4888-8888-888888888888")},
				{Name: "Alex", Role: "composer", MBID: ptrString("99999999-9999-4999-8999-999999999999")},
			},
			wantNames: []string{" Alex ", "Alex"},
		},
		{
			name: "canonical UUID duplicates across names",
			contributors: []librarymetadata.Contributor{
				{Name: "Alex", Role: "composer", MBID: ptrString("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")},
				{Name: "Blair", Role: "composer", MBID: ptrString("AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA")},
			},
			wantNames: []string{"Alex", "Blair"},
		},
		{
			name: "role names contain splitting delimiters",
			contributors: []librarymetadata.Contributor{
				{Name: "AC/DC", Role: "composer", MBID: ptrString("cccccccc-cccc-4ccc-8ccc-cccccccccccc")},
				{Name: "One;Two", Role: "composer", MBID: ptrString("dddddddd-dddd-4ddd-8ddd-dddddddddddd")},
			},
			wantNames: []string{"AC/DC", "One;Two"},
		},
		{
			name: "role name exceeds reader length",
			contributors: []librarymetadata.Contributor{
				{Name: strings.Repeat("x", 1025), Role: "composer", MBID: ptrString("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")},
			},
			wantNames: []string{strings.Repeat("x", 1025)},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			misalignedPosition := librarymetadata.Envelope{
				Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition,
				Desired: map[string]librarymetadata.FieldValue{
					"contributors": librarymetadata.ManualFieldValue(librarymetadata.StateValue, test.contributors, true),
				},
			}
			misalignedInput := input
			misalignedInput.Position.Desired = &misalignedPosition
			projection, projectErr := ProjectTags(misalignedInput)
			if projectErr != nil {
				t.Fatal(projectErr)
			}
			if !reflect.DeepEqual(projection.Tags["COMPOSER"], test.wantNames) {
				t.Fatalf("composer names = %#v, want %#v", projection.Tags["COMPOSER"], test.wantNames)
			}
			if _, exists := projection.Tags["MUSICBRAINZ_COMPOSERID"]; exists {
				t.Fatalf("misaligned composer IDs were projected: %#v", projection.Tags["MUSICBRAINZ_COMPOSERID"])
			}
			if got := projection.Fields["position.contributors.mbids"]; got.Status != "unsupported" || !strings.Contains(got.Reason, "representable") {
				t.Fatalf("misalignment diagnostic = %#v", got)
			}
		})
	}
	if got := result.Fields["position.contributors.mbids"]; got.Status != "unsupported" || !strings.Contains(got.Reason, "incomplete") {
		t.Errorf("MBID status = %#v, want incomplete role group diagnostic", got)
	}
	for key, values := range result.Tags {
		if key == "MUSICBRAINZ_LYRICISTID" || key == "MUSICBRAINZ_DIRECTORID" {
			continue
		}
		joined := strings.Join(values, "")
		if strings.Contains(joined, "11111111") || strings.Contains(joined, "22222222") {
			t.Fatalf("contributor identifier leaked into normalized tag %s", key)
		}
	}
	release.Desired["content_advisory"] = librarymetadata.ManualFieldValue(librarymetadata.StateValue, "clean", true)
	result, err = ProjectTags(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Tags["ITUNESADVISORY"], []string{"2"}) {
		t.Fatalf("clean advisory = %#v, want [2]", result.Tags["ITUNESADVISORY"])
	}
}

func TestProjectTagsContributorAndAdvisoryExplicitEmptyClearsFootprints(t *testing.T) {
	position := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition,
		Desired: map[string]librarymetadata.FieldValue{
			"contributors": librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false),
		},
	}
	release := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
		Desired: map[string]librarymetadata.FieldValue{
			"content_advisory": librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false),
		},
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Album Artist"}}, Desired: &release},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Track Artist"}}, Desired: &position},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"COMPOSER", "MUSICBRAINZ_COMPOSERID", "LYRICIST", "MUSICBRAINZ_LYRICISTID", "CONDUCTOR", "MUSICBRAINZ_CONDUCTORID",
		"ARRANGER", "MUSICBRAINZ_ARRANGERID", "ENGINEER", "MUSICBRAINZ_ENGINEERID", "PRODUCER", "MUSICBRAINZ_PRODUCERID",
		"REMIXER", "MUSICBRAINZ_REMIXERID", "DJMIXER", "MUSICBRAINZ_DJMIXERID", "MIXER", "MUSICBRAINZ_MIXERID",
		"DIRECTOR", "MUSICBRAINZ_DIRECTORID", "MUSICBRAINZ_PERFORMERID", "ITUNESADVISORY",
	} {
		if values, exists := result.Tags[key]; !exists || values == nil || len(values) != 0 {
			t.Errorf("explicit empty %s = %#v, exists=%t", key, values, exists)
		}
	}
}

func ptrString(value string) *string { return &value }

func TestProjectTagsDoesNotPublishUnconfirmedSyncedLyrics(t *testing.T) {
	position := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition,
		Desired: map[string]librarymetadata.FieldValue{
			"synced_lyrics": librarymetadata.ManualFieldValue(librarymetadata.StateValue, "[00:01.00]hello", true),
		},
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &position},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := result.Tags["LYRICS"]; exists {
		t.Fatalf("unconfirmed synced lyrics leaked into tags: %#v", result.Tags["LYRICS"])
	}
	if result.Fields["position.synced_lyrics"].Status != "unsupported" {
		t.Fatalf("synced lyrics status = %#v, want unsupported", result.Fields["position.synced_lyrics"])
	}
}

func TestProjectTagsMapsArtistMBIDByScope(t *testing.T) {
	trackArtistID, releaseArtistID := uuid.NewString(), uuid.NewString()
	release := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
		Desired: map[string]librarymetadata.FieldValue{
			"artist_mbid": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []string{releaseArtistID}, true),
		},
	}
	position := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition,
		Desired: map[string]librarymetadata.FieldValue{
			"artist_mbid": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []string{trackArtistID}, true),
		},
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Album Artist"}}, Desired: &release},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Track Artist"}}, Desired: &position},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Tags["MUSICBRAINZ_ALBUMARTISTID"]; !reflect.DeepEqual(got, []string{releaseArtistID}) {
		t.Fatalf("release artist MBID = %#v", got)
	}
	if got := result.Tags["MUSICBRAINZ_ARTISTID"]; !reflect.DeepEqual(got, []string{trackArtistID}) {
		t.Fatalf("track artist MBID = %#v", got)
	}
}

func TestProjectTagsValidatesManuallyConstructedEnvelopesAndFinalRequiredTags(t *testing.T) {
	base := TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Album Artist"}}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Track Artist"}}},
	}
	badScope := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{}}
	base.Release.Desired = &badScope
	if _, err := ProjectTags(base); err == nil {
		t.Fatal("expected mismatched envelope scope to be rejected")
	}
	invalidEnvelopes := []librarymetadata.Envelope{
		{
			Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
			Desired: map[string]librarymetadata.FieldValue{
				"genre": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []any{"Jazz", 1}, true),
			},
		},
		{
			Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
			Desired: map[string]librarymetadata.FieldValue{
				"unregistered": librarymetadata.ManualFieldValue(librarymetadata.StateValue, "value", true),
			},
		},
	}
	for _, envelope := range invalidEnvelopes {
		base.Release.Desired = &envelope
		if _, err := ProjectTags(base); err == nil {
			t.Fatal("expected invalid manually constructed envelope to be rejected")
		}
	}
	base.Release.Desired = &librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
		Desired: map[string]librarymetadata.FieldValue{
			"title": librarymetadata.ManualFieldValue(librarymetadata.StateValue, " \t", true),
		},
	}
	if _, err := ProjectTags(base); err != nil {
		t.Fatalf("explicitly blank final album title should be permitted with a valid baseline: %v", err)
	}
	base.Release.Title = " \t"
	base.Release.Desired = nil
	if _, err := ProjectTags(base); err == nil {
		t.Fatal("expected an empty baseline album title to be rejected")
	}
	base.Release.Desired = nil
	base.Position.Artist = []librarymetadata.ArtistCredit{{ArtistName: " \t", CreditName: " "}}
	if _, err := ProjectTags(base); err == nil {
		t.Fatal("expected blank artist credits to be rejected")
	}
}

func TestProjectTagsClearsTerminalArtistCreditGroups(t *testing.T) {
	release := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
		Desired: map[string]librarymetadata.FieldValue{
			"artist_credit": librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false),
		},
	}
	position := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition,
		Desired: map[string]librarymetadata.FieldValue{
			"artist_credit": librarymetadata.ManualFieldValue(librarymetadata.StateAbsent, nil, false),
		},
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Desired: &release, ArtistCreditsResolved: true, ArtistCreditTerminal: true},
		Position: PositionTags{Title: "Song", Desired: &position, ArtistCreditsResolved: true, ArtistCreditTerminal: true},
	})
	if err != nil {
		t.Fatalf("ProjectTags() error = %v", err)
	}
	for _, key := range []string{"ARTIST", "ARTISTS", "MUSICBRAINZ_ARTISTID", "ARTISTSORT", "ARTISTSSORT", "ALBUMARTIST", "ALBUMARTISTS", "MUSICBRAINZ_ALBUMARTISTID", "ALBUMARTISTSORT", "ALBUMARTISTSSORT"} {
		values, exists := result.Tags[key]
		if !exists || values == nil || len(values) != 0 {
			t.Errorf("terminal artist credit %s = %#v, exists=%t", key, values, exists)
		}
	}
}

func TestProjectTagsResolvedCreditsCannotBeOverwrittenByLegacyArtistFields(t *testing.T) {
	for _, scope := range []librarymetadata.Scope{librarymetadata.ScopePosition, librarymetadata.ScopeRelease} {
		t.Run(string(scope), func(t *testing.T) {
			envelope := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: scope, Desired: map[string]librarymetadata.FieldValue{
				"artist_mbid": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []string{"11111111-1111-4111-8111-111111111111"}, true),
				"artists":     librarymetadata.ManualFieldValue(librarymetadata.StateValue, []string{"Stale Artist"}, true),
			}}
			mbidA, mbidB := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
			resolvedCollision := []librarymetadata.ArtistCredit{
				{ArtistID: "artist-a", ArtistName: "Same Name", CreditName: "Same Name", MBID: &mbidA},
				{ArtistID: "artist-b", ArtistName: "Same Name", CreditName: "Same Name", MBID: &mbidB},
			}
			input := TagProjectionInput{
				Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Album Artist", CreditName: "Album Artist"}}},
				Position: PositionTags{Title: "Song", Artist: resolvedCollision, ArtistCreditsResolved: true},
			}
			if scope == librarymetadata.ScopePosition {
				input.Position.Desired = &envelope
			} else {
				input.Release.Desired = &envelope
				input.Release.Artist = resolvedCollision
				input.Release.ArtistCreditsResolved = true
				input.Position.Artist = []librarymetadata.ArtistCredit{{ArtistName: "Track Artist", CreditName: "Track Artist"}}
			}
			projection, err := ProjectTags(input)
			if err != nil {
				t.Fatalf("ProjectTags() error = %v", err)
			}
			mbidKey, artistKey := "MUSICBRAINZ_ARTISTID", "ARTISTS"
			if scope == librarymetadata.ScopeRelease {
				mbidKey, artistKey = "MUSICBRAINZ_ALBUMARTISTID", "ALBUMARTISTS"
			}
			if values := projection.Tags[mbidKey]; values == nil || len(values) != 0 {
				t.Errorf("collision-omitted credit MBID group = %#v", values)
			}
			if values := projection.Tags[artistKey]; values == nil || len(values) != 0 {
				t.Errorf("collision-omitted artist group = %#v", values)
			}
		})
	}
}
