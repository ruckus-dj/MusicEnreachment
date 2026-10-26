package publication

import (
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
		"ALBUMARTIST": {"A & Featured B"}, "ALBUMARTISTS": {"A", "B"}, "MUSICBRAINZ_ALBUMARTISTID": {idOne, idTwo},
		"ALBUM": {"Album"}, "TITLE": {"Song"},
	}
	if !reflect.DeepEqual(result.Tags, want) {
		t.Fatalf("tags = %#v, want %#v", result.Tags, want)
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
	if _, exists := result.Tags["MUSICBRAINZ_ARTISTID"]; exists {
		t.Fatalf("partial IDs emitted a misaligned ID list: %#v", result.Tags["MUSICBRAINZ_ARTISTID"])
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
	release, err := librarymetadata.DecodeEnvelope([]byte(`{"version":1,"scope":"release","desired":{"genre":{"state":"value","value":["Jazz","Fusion"],"authority":"manual","provenance":{"kind":"manual"}},"release_secondary_types":{"state":"value","value":["Live","Compilation"],"authority":"manual","provenance":{"kind":"manual"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	position, err := librarymetadata.DecodeEnvelope([]byte(`{"version":1,"scope":"position","desired":{"bpm":{"state":"value","value":123.450,"authority":"manual","provenance":{"kind":"manual"}},"disc_number":{"state":"value","value":2,"authority":"manual","provenance":{"kind":"manual"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &release},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &position},
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
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &release},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &position},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"ALBUMSORT": {"Album, The"}, "TITLESORT": {"Été, Pt. 2"},
		"ALBUMARTISTSORT": {"Artist, A", "Artist, B"}, "ARTISTSORT": {"Zed", "Ångström"},
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

func TestProjectTagsReleaseArtistSortNameUsesArtistSortNotAlbumArtistSort(t *testing.T) {
	release := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
		Desired: map[string]librarymetadata.FieldValue{
			"artist_sort_names":       librarymetadata.ManualFieldValue(librarymetadata.StateValue, []string{"The Artist"}, false),
			"album_artist_sort_names": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []string{"Sort Album Artist"}, false),
		},
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &release},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Tags["ARTISTSORT"], []string{"The Artist"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ARTISTSORT = %#v, want %#v", got, want)
	}
	if got, want := result.Tags["ALBUMARTISTSORT"], []string{"Sort Album Artist"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ALBUMARTISTSORT = %#v, want %#v", got, want)
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

func TestProjectTagsMapsContributorRolesAndAdvisory(t *testing.T) {
	position, err := librarymetadata.DecodeEnvelope([]byte(`{"version":1,"scope":"position","desired":{"contributors":{"state":"value","value":[{"name":"Family,Given 音楽","role":" Composer ","artist_id":"11111111-1111-4111-8111-111111111111"},{"name":"作詞者","role":"LYRICIST","mbid":"22222222-2222-4222-8222-222222222222"},{"name":"unknown role","role":"instrumentalist"}],"authority":"manual","provenance":{"kind":"manual"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	release, err := librarymetadata.DecodeEnvelope([]byte(`{"version":1,"scope":"release","desired":{"content_advisory":{"state":"value","value":"explicit","authority":"manual","provenance":{"kind":"manual"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Album Artist"}}, Desired: &release},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Track Artist"}}, Desired: &position},
	})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string][]string{
		"COMPOSER": {"Family,Given 音楽"}, "LYRICIST": {"作詞者"}, "ITUNESADVISORY": {"1"},
	} {
		if !reflect.DeepEqual(result.Tags[key], want) {
			t.Errorf("%s = %#v, want %#v", key, result.Tags[key], want)
		}
	}
	if result.Fields["position.contributors"].Status != "unsupported" {
		t.Errorf("unknown role status = %#v", result.Fields["position.contributors"])
	}
	if _, exists := result.Tags["PERFORMER"]; exists {
		t.Fatal("unknown contributor role was coerced to PERFORMER")
	}
	if result.Fields["position.contributors.mbids"].Status != "unsupported" {
		t.Errorf("MBID status = %#v", result.Fields["position.contributors.mbids"])
	}
	for key, values := range result.Tags {
		joined := strings.Join(values, "")
		if strings.Contains(joined, "11111111") || strings.Contains(joined, "22222222") {
			t.Fatalf("contributor identifier leaked into normalized tag %s", key)
		}
	}
	release.Desired["content_advisory"] = librarymetadata.ManualFieldValue(librarymetadata.StateValue, "clean", true)
	result, err = ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Album Artist"}}, Desired: &release},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Track Artist"}}, Desired: &position},
	})
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
	for _, key := range []string{"COMPOSER", "LYRICIST", "CONDUCTOR", "ARRANGER", "ENGINEER", "PRODUCER", "REMIXER", "DJMIXER", "MIXER", "ITUNESADVISORY"} {
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
	if _, err := ProjectTags(base); err == nil {
		t.Fatal("expected an explicitly blank final album title to be rejected")
	}
	base.Release.Desired = nil
	base.Position.Artist = []librarymetadata.ArtistCredit{{ArtistName: " \t", CreditName: " "}}
	if _, err := ProjectTags(base); err == nil {
		t.Fatal("expected blank artist credits to be rejected")
	}
}
