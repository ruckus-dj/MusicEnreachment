package persistence

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestBuildPublicationProviderCanonicalTagPatchUsesAcceptedFieldsAndTrackOverride(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Release.Name = "stale source-name fallback"
	context.Release.Fields = json.RawMessage(`{"title":"Accepted album","normalized_tracks":[{"medium":1,"position":2,"recording_provider_key":"recording-key","title":"Track override"}]}`)
	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{"title": {}, "normalized_tracks": {}}
	context.Position.Medium, context.Position.Position, context.Position.RecordingProviderKey = 1, 2, "recording-key"
	context.Recording = &MatchingProjectionEntity{Kind: "recording", ProviderKey: "recording-key", Name: "stale source recording title", Fields: json.RawMessage(`{"title":"Recording title"}`), FieldEvidence: json.RawMessage(`{"title":{}}`)}

	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"ALBUM": {"Accepted album"}, "TITLE": {"Track override"}}
	if !reflect.DeepEqual(patch, want) {
		t.Fatalf("patch = %#v, want %#v", patch, want)
	}
}

func TestBuildPublicationProviderCanonicalTagPatchUsesRecordingFieldsNotNameFallback(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Release.Name = "unproven release name"
	context.Release.Fields = json.RawMessage(`{}`)
	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{}
	context.RecordingBound = true
	context.Recording = &MatchingProjectionEntity{Kind: "recording", ProviderKey: "recording-key", Name: "unproven recording name", Fields: json.RawMessage(`{"title":"Proven recording title"}`), FieldEvidence: json.RawMessage(`{"title":{}}`)}

	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(patch, map[string][]string{"TITLE": {"Proven recording title"}}) {
		t.Fatalf("patch = %#v", patch)
	}
}

func TestBuildPublicationProviderCanonicalTagPatchProjectsAcceptedReleaseAndRecordingFields(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Release.ProviderKey = "11111111-1111-4111-8111-111111111111"
	context.Release.Fields = json.RawMessage(`{"date":"1999-02-03","release-group":{"id":"22222222-2222-4222-8222-222222222222","first-release-date":"1998","primary-type":"Album","secondary-types":["Compilation","Live"]}}`)
	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{"date": {}, "release-group": {}}
	context.Position.RecordingProviderKey = "33333333-3333-4333-8333-333333333333"
	context.RecordingBound = true
	context.Recording = &MatchingProjectionEntity{
		Kind: "recording", ProviderKey: context.Position.RecordingProviderKey,
		Fields:        json.RawMessage(`{"isrcs":["USAAA9900001","USAAA9900002"]}`),
		FieldEvidence: json.RawMessage(`{"isrcs":{}}`),
	}

	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"DATE":                       {"1999-02-03"},
		"MUSICBRAINZ_ALBUMID":        {"11111111-1111-4111-8111-111111111111"},
		"MUSICBRAINZ_RELEASEGROUPID": {"22222222-2222-4222-8222-222222222222"},
		"ORIGINALDATE":               {"1998"},
		"RELEASETYPE":                {"Album"},
		"RELEASETYPES":               {"Compilation", "Live"},
		"MUSICBRAINZ_TRACKID":        {"33333333-3333-4333-8333-333333333333"},
		"ISRC":                       {"USAAA9900001", "USAAA9900002"},
	}
	if !reflect.DeepEqual(patch, want) {
		t.Fatalf("patch = %#v, want %#v", patch, want)
	}
}

func TestBuildPublicationProviderCanonicalTagPatchProjectsOrderedAcceptedLabelCatalogAndGenres(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Release.Fields = json.RawMessage(`{"label-info":[{"label":{"name":"Label B"},"catalog-number":"CAT-2"},{"catalog-number":"CAT-1"},{"label":{"name":"Label B"},"catalog-number":"CAT-2"}],"genres":[{"id":"00000000-0000-4000-8000-000000000001","name":"Genre B","count":1},{"id":"00000000-0000-4000-8000-000000000002","name":"Genre A","count":99},{"id":"00000000-0000-4000-8000-000000000001","name":"Genre B","count":2}]}`)
	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{"label-info": {}, "genres": {}}
	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"LABEL":         {"Label B", "Label B"},
		"CATALOGNUMBER": {"CAT-2", "CAT-1", "CAT-2"},
		"GENRE":         {"Genre B", "Genre A"},
	}
	if !reflect.DeepEqual(patch, want) {
		t.Fatalf("patch = %#v, want %#v", patch, want)
	}
}

func TestAcceptedPublicationGenresUsesBoundRecordingThenReleaseThenRecordingArtists(t *testing.T) {
	const recordingGenre = `[{"id":"00000000-0000-4000-8000-000000000001","name":"recording genre"}]`
	const releaseGenre = `[{"id":"00000000-0000-4000-8000-000000000002","name":"release genre"}]`
	const artistA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const artistB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	context := publicationTagProjectionFixture()
	context.Position.RecordingProviderKey = "recording-key"
	context.RecordingBound = true
	context.Recording = &MatchingProjectionEntity{Kind: "recording", ProviderKey: "recording-key", Fields: json.RawMessage(`{"genres":` + recordingGenre + `,"artist-credit":[]}`), FieldEvidence: json.RawMessage(`{"genres":{}}`)}
	context.Release.Fields = json.RawMessage(`{"genres":` + releaseGenre + `}`)
	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{"genres": {}}
	genres, err := acceptedPublicationGenres(context, map[string]json.RawMessage{"genres": json.RawMessage(releaseGenre)})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(genres, []librarymetadata.GenreReference{{ID: "00000000-0000-4000-8000-000000000001", SourceName: "recording genre", Name: "Recording Genre"}}) {
		t.Fatalf("recording genre winner = %#v", genres)
	}
	context.Recording.Fields = json.RawMessage(`{"genres":[]}`)
	genres, err = acceptedPublicationGenres(context, map[string]json.RawMessage{"genres": json.RawMessage(releaseGenre)})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(genres, []librarymetadata.GenreReference{{ID: "00000000-0000-4000-8000-000000000002", SourceName: "release genre", Name: "Release Genre"}}) {
		t.Fatalf("release genre fallback = %#v", genres)
	}

	context.Recording.Fields = json.RawMessage(`{"artist-credit":[{"name":"A","artist":{"id":"` + artistA + `"}},{"name":"B","artist":{"id":"` + artistB + `"}}]}`)
	context.Recording.FieldEvidence = json.RawMessage(`{"artist-credit":{}}`)
	context.Release.Fields = json.RawMessage(`{}`)
	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{}
	artistEnvelope := func(mbid string) FirstPublicationGenreArtist {
		return FirstPublicationGenreArtist{
			MBID:     &mbid,
			Metadata: librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeArtist, Desired: map[string]librarymetadata.FieldValue{}},
		}
	}
	context.GenreArtists = []FirstPublicationGenreArtist{artistEnvelope(artistA), artistEnvelope(artistB)}
	context.ArtistEntities = []MatchingProjectionEntity{
		{Kind: "artist", ProviderKey: artistA, Fields: json.RawMessage(`{"genres":[{"id":"00000000-0000-4000-8000-000000000003","name":"artist one"},{"id":"00000000-0000-4000-8000-000000000004","name":"shared"}]}`), FieldEvidence: json.RawMessage(`{"genres":{}}`)},
		{Kind: "artist", ProviderKey: artistB, Fields: json.RawMessage(`{"genres":[{"id":"00000000-0000-4000-8000-000000000004","name":"shared"},{"id":"00000000-0000-4000-8000-000000000005","name":"artist two"}]}`), FieldEvidence: json.RawMessage(`{"genres":{}}`)},
	}
	genres, err = acceptedPublicationGenres(context, map[string]json.RawMessage{})
	if err != nil {
		t.Fatal(err)
	}
	want := []librarymetadata.GenreReference{
		{ID: "00000000-0000-4000-8000-000000000003", SourceName: "artist one", Name: "Artist One"},
		{ID: "00000000-0000-4000-8000-000000000004", SourceName: "shared", Name: "Shared"},
		{ID: "00000000-0000-4000-8000-000000000005", SourceName: "artist two", Name: "Artist Two"},
	}
	if !reflect.DeepEqual(genres, want) {
		t.Fatalf("artist genre union = %#v, want %#v", genres, want)
	}
}

func TestPublicationTagGenreRecordingScopePrecedesReleaseManualGenre(t *testing.T) {
	for _, releaseGenre := range []string{
		`{"state":"value","value":[{"id":"00000000-0000-4000-8000-000000000002","source_name":"release","name":"Release"}],"authority":"manual","provenance":{"kind":"manual"}}`,
		`{"state":"explicit_empty","authority":"manual","provenance":{"kind":"manual"}}`,
	} {
		t.Run(releaseGenre, func(t *testing.T) {
			context := publicationTagProjectionFixture()
			context.Release.Fields = json.RawMessage(`{"genres":[{"id":"00000000-0000-4000-8000-000000000002","name":"provider release"}]}`)
			context.Release.FieldEvidence = map[string]ProviderFieldEvidence{"genres": {}}
			context.Position.RecordingProviderKey = "recording-key"
			context.RecordingBound = true
			context.Recording = &MatchingProjectionEntity{Kind: "recording", ProviderKey: "recording-key", Fields: json.RawMessage(`{"genres":[{"id":"00000000-0000-4000-8000-000000000001","name":"recording provider"}]}`), FieldEvidence: json.RawMessage(`{"genres":{}}`)}
			context.ReleaseOwner.Metadata = json.RawMessage(`{"local_metadata":{"version":1,"scope":"release","desired":{"genre":` + releaseGenre + `}}}`)

			patch, err := BuildPublicationProviderCanonicalTagPatch(context)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(patch["GENRE"], []string{"Recording Provider"}) {
				t.Fatalf("genre patch = %#v, want recording provider genre", patch["GENRE"])
			}
		})
	}
}

func TestPublicationTagRecordingManualAbsentUsesRecordingProviderBeforeRelease(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Release.Fields = json.RawMessage(`{"genres":[{"id":"00000000-0000-4000-8000-000000000002","name":"provider release"}]}`)
	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{"genres": {}}
	context.Position.RecordingProviderKey = "recording-key"
	context.RecordingBound = true
	context.Recording = &MatchingProjectionEntity{Kind: "recording", ProviderKey: "recording-key", Fields: json.RawMessage(`{"genres":[{"id":"00000000-0000-4000-8000-000000000001","name":"recording provider"}]}`), FieldEvidence: json.RawMessage(`{"genres":{}}`)}
	context.RecordingOwner.Metadata = json.RawMessage(`{"local_metadata":{"version":1,"scope":"recording","desired":{"genre":{"state":"absent","authority":"manual","provenance":{"kind":"manual"}}}}}`)

	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(patch["GENRE"], []string{"Recording Provider"}) {
		t.Fatalf("genre patch = %#v, want recording provider genre", patch["GENRE"])
	}
}

func TestPublicationTagReachedRecordingExplicitEmptyClearsGenre(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Position.RecordingProviderKey = "recording-key"
	context.RecordingBound = true
	context.Recording = &MatchingProjectionEntity{Kind: "recording", ProviderKey: "recording-key", Fields: json.RawMessage(`{"genres":[{"id":"00000000-0000-4000-8000-000000000001","name":"recording provider"}]}`), FieldEvidence: json.RawMessage(`{"genres":{}}`)}
	context.RecordingOwner.Metadata = json.RawMessage(`{"local_metadata":{"version":1,"scope":"recording","desired":{"genre":{"state":"explicit_empty","authority":"manual","provenance":{"kind":"manual"}}}}}`)

	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	genres, exists := patch["GENRE"]
	if !exists || genres == nil || len(genres) != 0 {
		t.Fatalf("genre patch = %#v, want explicit empty GENRE patch", patch)
	}
}

func TestPublicationTagReachedReleaseManualGenreOverridesReleaseProvider(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Release.Fields = json.RawMessage(`{"genres":[{"id":"00000000-0000-4000-8000-000000000001","name":"provider release"}]}`)
	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{"genres": {}}
	context.ReleaseOwner.Metadata = json.RawMessage(`{"local_metadata":{"version":1,"scope":"release","desired":{"genre":{"state":"value","value":[{"id":"00000000-0000-4000-8000-000000000002","source_name":"manual release","name":"Manual Release"}],"authority":"manual","provenance":{"kind":"manual"}}}}}`)

	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(patch["GENRE"], []string{"Manual Release"}) {
		t.Fatalf("genre patch = %#v, want reached release manual genre", patch["GENRE"])
	}
}

func TestAcceptedPublicationGenresDoesNotUseUnboundRecordingOrReleaseArtists(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Position.RecordingProviderKey = "recording-key"
	context.Recording = &MatchingProjectionEntity{Kind: "recording", ProviderKey: "recording-key", Fields: json.RawMessage(`{"genres":[{"id":"00000000-0000-4000-8000-000000000001","name":"not bound"}]}`), FieldEvidence: json.RawMessage(`{"genres":{}}`)}
	context.ArtistEntities = []MatchingProjectionEntity{{Kind: "artist", ProviderKey: "release-artist", Fields: json.RawMessage(`{"genres":[{"id":"00000000-0000-4000-8000-000000000002","name":"not recording artist"}]}`), FieldEvidence: json.RawMessage(`{"genres":{}}`)}}
	genres, err := acceptedPublicationGenres(context, map[string]json.RawMessage{"artist-credit": json.RawMessage(`[{"name":"X","artist":{"id":"release-artist"}}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(genres) != 0 {
		t.Fatalf("unbound/non-recording genres = %#v, want none", genres)
	}
}

func TestBuildPublicationProviderCanonicalTagPatchRequiresTopLevelEvidenceForLabelAndGenres(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Release.Fields = json.RawMessage(`{"label-info":[{"label":{"name":"Unaccepted"},"catalog-number":"UNACCEPTED"}],"genres":[{"name":"Unaccepted"}]}`)
	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 0 {
		t.Fatalf("patch without top-level field evidence = %#v, want empty", patch)
	}

	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{"label-info": {}, "genres": {}}
	context.Release.Fields = json.RawMessage(`{"label-info":null,"genres":null}`)
	patch, err = BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 0 {
		t.Fatalf("patch for accepted null optional fields = %#v, want empty", patch)
	}
}

func TestBuildPublicationProviderCanonicalTagPatchRejectsMalformedLabelAndGenreShapes(t *testing.T) {
	for _, fields := range []string{
		`{"label-info":{}}`,
		`{"label-info":[{"label":{"name":42}}]}`,
		`{"label-info":[{"catalog-number":42}]}`,
		`{"genres":{}}`,
		`{"genres":[{"id":"00000000-0000-4000-8000-000000000001","name":42}]}`,
		`{"genres":[{"name":"missing id"}]}`,
	} {
		t.Run(fields, func(t *testing.T) {
			context := publicationTagProjectionFixture()
			context.Release.Fields = json.RawMessage(fields)
			context.Release.FieldEvidence = map[string]ProviderFieldEvidence{"label-info": {}, "genres": {}}
			if _, err := BuildPublicationProviderCanonicalTagPatch(context); err == nil {
				t.Fatal("expected malformed accepted field shape to be rejected")
			}
		})
	}
}

func TestBuildPublicationProviderCanonicalTagPatchProjectsOnlyAcceptedReleaseBarcodeCountryAndStatus(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Release.Fields = json.RawMessage(`{"barcode":"  0123456789  ","country":"XW","status":"non-official"}`)
	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{"barcode": {}, "country": {}, "status": {}}
	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"BARCODE": {"  0123456789  "}, "RELEASECOUNTRY": {"XW"}, "RELEASESTATUS": {"non-official"}}
	if !reflect.DeepEqual(patch, want) {
		t.Fatalf("patch = %#v, want %#v", patch, want)
	}

	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{}
	patch, err = BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 0 {
		t.Fatalf("patch without field evidence = %#v, want empty", patch)
	}
}

func TestBuildPublicationProviderCanonicalTagPatchRejectsMalformedAcceptedReleaseScalars(t *testing.T) {
	for _, field := range []string{"barcode", "country", "status"} {
		t.Run(field, func(t *testing.T) {
			context := publicationTagProjectionFixture()
			context.Release.Fields = json.RawMessage(`{"` + field + `":42}`)
			context.Release.FieldEvidence = map[string]ProviderFieldEvidence{field: {}}
			if _, err := BuildPublicationProviderCanonicalTagPatch(context); err == nil {
				t.Fatal("expected accepted non-string scalar to be rejected")
			}
		})
	}
}

func TestBuildPublicationProviderCanonicalTagPatchNeverDeletesReleaseScalars(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Release.Fields = json.RawMessage(`{"barcode":"","country":null}`)
	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{"barcode": {}, "country": {}, "status": {}}
	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 0 {
		t.Fatalf("patch for empty, null, and missing scalars = %#v, want empty", patch)
	}
	current := map[string][]string{
		"BARCODE": {"existing barcode"}, "RELEASECOUNTRY": {"existing country"}, "RELEASESTATUS": {"existing status"},
	}
	updated, err := publication.ApplyProviderTagPatch(current, patch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated, current) {
		t.Fatalf("current tags after empty scalar patch = %#v, want %#v", updated, current)
	}
}

func TestBuildPublicationProviderCanonicalTagPatchRequiresEvidenceAndBoundValidRecording(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Release.ProviderKey = "11111111-1111-4111-8111-111111111111"
	context.Release.Fields = json.RawMessage(`{"date":"2001","release-group":{"id":"22222222-2222-4222-8222-222222222222"}}`)
	context.Recording = &MatchingProjectionEntity{
		Kind: "recording", ProviderKey: "33333333-3333-4333-8333-333333333333", Fields: json.RawMessage(`{"isrcs":["USAAA0100001"]}`),
		FieldEvidence: json.RawMessage(`{"isrcs":{}}`),
	}
	context.Position.RecordingProviderKey = context.Recording.ProviderKey
	context.RecordingBound = false

	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"MUSICBRAINZ_ALBUMID": {context.Release.ProviderKey}}
	if !reflect.DeepEqual(patch, want) {
		t.Fatalf("patch with unbound recording and no field evidence = %#v, want %#v", patch, want)
	}

	context.Release.ProviderKey = "not-an-mbid"
	patch, err = BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 0 {
		t.Fatalf("patch with invalid accepted release identity = %#v, want no identity tags", patch)
	}

	context.Release.ProviderKey = "11111111-1111-4111-8111-111111111111"
	context.Position.RecordingProviderKey = "not-an-mbid"
	context.Recording.ProviderKey = "not-an-mbid"
	context.RecordingBound = true
	patch, err = BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	want = map[string][]string{"MUSICBRAINZ_ALBUMID": {context.Release.ProviderKey}}
	if !reflect.DeepEqual(patch, want) {
		t.Fatalf("patch with invalid bound recording identity = %#v, want %#v", patch, want)
	}
}

func TestBuildPublicationProviderCanonicalTagPatchRejectsMalformedAcceptedFieldShapes(t *testing.T) {
	tests := []struct {
		name      string
		fields    json.RawMessage
		evidence  map[string]ProviderFieldEvidence
		recording *MatchingProjectionEntity
	}{
		{name: "release date", fields: json.RawMessage(`{"date":42}`), evidence: map[string]ProviderFieldEvidence{"date": {}}},
		{name: "release group", fields: json.RawMessage(`{"release-group":[]}`), evidence: map[string]ProviderFieldEvidence{"release-group": {}}},
		{name: "isrcs", fields: json.RawMessage(`{}`), recording: &MatchingProjectionEntity{Kind: "recording", ProviderKey: "33333333-3333-4333-8333-333333333333", Fields: json.RawMessage(`{"isrcs":{"bad":true}}`), FieldEvidence: json.RawMessage(`{"isrcs":{}}`)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context := publicationTagProjectionFixture()
			context.Release.Fields = test.fields
			context.Release.FieldEvidence = test.evidence
			context.Position.RecordingProviderKey = "33333333-3333-4333-8333-333333333333"
			context.RecordingBound = test.recording != nil
			context.Recording = test.recording
			_, err := BuildPublicationProviderCanonicalTagPatch(context)
			if err == nil {
				t.Fatal("expected malformed accepted field shape to be rejected")
			}
		})
	}
}

func TestBuildPublicationProviderCanonicalTagPatchDoesNotUseUnboundRecording(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Recording = &MatchingProjectionEntity{Kind: "recording", ProviderKey: "recording-key", Fields: json.RawMessage(`{"title":"accepted recording title"}`), FieldEvidence: json.RawMessage(`{"title":{}}`)}
	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 0 {
		t.Fatalf("patch = %#v, want no unbound-recording fallback", patch)
	}
}

func TestBuildPublicationProviderCanonicalTagPatchFansOutAcceptedCredits(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Release.Fields = json.RawMessage(`{"title":"Album","artist-credit":[{"name":"Credited Artist","joinphrase":" & ","artist":{"id":"11111111-1111-4111-8111-111111111111"}}],"normalized_tracks":[{"medium":1,"position":2,"recording_provider_key":"recording-key","artist-credit":[{"name":"Track Credit","joinphrase":" feat. ","artist":{"id":"22222222-2222-4222-8222-222222222222"}}]}]}`)
	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{"title": {}, "artist-credit": {}, "normalized_tracks": {}}
	context.Position.Medium, context.Position.Position, context.Position.RecordingProviderKey = 1, 2, "recording-key"
	context.RecordingBound = true
	context.ArtistEntities = []MatchingProjectionEntity{
		{Kind: "artist", ProviderKey: "11111111-1111-4111-8111-111111111111", Name: "not-fields-name", Fields: json.RawMessage(`{"name":"Canonical Album Artist"}`), FieldEvidence: json.RawMessage(`{"name":{}}`)},
		{Kind: "artist", ProviderKey: "22222222-2222-4222-8222-222222222222", Name: "not-fields-name", Fields: json.RawMessage(`{"name":"Canonical Track Artist"}`), FieldEvidence: json.RawMessage(`{"name":{}}`)},
	}

	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"ALBUM": {"Album"}, "ALBUMARTIST": {"Credited Artist & "}, "ALBUMARTISTS": {"Canonical Album Artist"},
		"ALBUMARTISTSORT": {"Canonical Album Artist"}, "ALBUMARTISTSSORT": {"Canonical Album Artist"},
		"MUSICBRAINZ_ALBUMARTISTID": {"11111111-1111-4111-8111-111111111111"},
		"ARTIST":                    {"Track Credit feat. "}, "ARTISTS": {"Canonical Track Artist"},
		"ARTISTSORT": {"Canonical Track Artist"}, "ARTISTSSORT": {"Canonical Track Artist"},
		"MUSICBRAINZ_ARTISTID": {"22222222-2222-4222-8222-222222222222"},
	}
	if !reflect.DeepEqual(patch, want) {
		t.Fatalf("patch = %#v, want %#v", patch, want)
	}
}

func TestCanonicalCreditsPrefersPresentBoundLocalIdentityOverProviderFallback(t *testing.T) {
	const artistKey = "11111111-1111-4111-8111-111111111111"
	credit := FrozenArtistCredit{ArtistProviderKey: artistKey, Name: "Same Artist"}
	facts := []PublicationProviderTagArtist{{
		ProviderKey:      artistKey,
		Name:             "Local Artist",
		ProviderMetadata: json.RawMessage(`{"disambiguation":"provider identity"}`),
		Metadata:         json.RawMessage(`{"disambiguation":"local identity","local_metadata":{"version":1,"scope":"artist","desired":{"sort_name":{"state":"value","value":"Artist, Same","authority":"manual","provenance":{"kind":"manual"}}}}}`),
	}}

	credits, err := canonicalCredits([]FrozenArtistCredit{credit}, nil, facts)
	if err != nil {
		t.Fatal(err)
	}
	if len(credits) != 1 || credits[0].ArtistName != "Local Artist" || credits[0].Disambiguation != "local identity" || credits[0].SortName != "Artist, Same" {
		t.Fatalf("bound artist credit = %#v, want local name, disambiguation, and sort name", credits)
	}

	entities := []MatchingProjectionEntity{{
		Kind: "artist", ProviderKey: artistKey,
		Fields:        json.RawMessage(`{"disambiguation":"accepted identity"}`),
		FieldEvidence: json.RawMessage(`{"disambiguation":{}}`),
	}}
	credits, err = canonicalCredits([]FrozenArtistCredit{credit}, entities, facts)
	if err != nil {
		t.Fatal(err)
	}
	if credits[0].ArtistName != "Local Artist" || credits[0].Disambiguation != "local identity" {
		t.Fatalf("accepted projection identity = %#v, want bound local identity", credits[0])
	}

	facts[0].Metadata = json.RawMessage(`{"disambiguation":"","local_metadata":{"version":1,"scope":"artist","desired":{}}}`)
	credits, err = canonicalCredits([]FrozenArtistCredit{credit}, entities, facts)
	if err != nil {
		t.Fatal(err)
	}
	if credits[0].Disambiguation != "" {
		t.Fatalf("explicit local empty disambiguation = %q, want empty over provider value", credits[0].Disambiguation)
	}

	facts[0].Metadata = json.RawMessage(`{"disambiguation":null,"local_metadata":{"version":1,"scope":"artist","desired":{}}}`)
	credits, err = canonicalCredits([]FrozenArtistCredit{credit}, entities, facts)
	if err != nil {
		t.Fatal(err)
	}
	if credits[0].Disambiguation != "accepted identity" {
		t.Fatalf("legacy-null local disambiguation = %q, want accepted provider fallback", credits[0].Disambiguation)
	}

	facts[0].Metadata = json.RawMessage(`{"disambiguation":7}`)
	if _, err := canonicalCredits([]FrozenArtistCredit{credit}, entities, facts); err == nil {
		t.Fatal("malformed local disambiguation was silently ignored")
	}
}

func TestFirstPublicationArtistDisambiguationDigestTracksProjectionIdentity(t *testing.T) {
	credits := []librarymetadata.ArtistCredit{{ArtistID: "artist", Disambiguation: "first"}}
	first, err := firstPublicationArtistDisambiguationDigest(credits)
	if err != nil {
		t.Fatal(err)
	}
	credits[0].Disambiguation = "second"
	second, err := firstPublicationArtistDisambiguationDigest(credits)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("artist disambiguation did not affect first-publication context digest")
	}
}

func TestBuildPublicationProviderCanonicalTagPatchOmitsUnsupportedEmptyAndNullValuesWithoutMutation(t *testing.T) {
	context := publicationTagProjectionFixture()
	context.Release.Name = "fallback must not be used"
	context.Release.Fields = json.RawMessage(`{"title":"  ","artist-credit":null,"normalized_tracks":[{"medium":1,"position":2,"recording_provider_key":"recording-key","title":""}]}`)
	context.Release.FieldEvidence = map[string]ProviderFieldEvidence{"title": {}, "artist-credit": {}, "normalized_tracks": {}}
	context.Position.Medium, context.Position.Position, context.Position.RecordingProviderKey = 1, 2, "recording-key"
	context.Recording = &MatchingProjectionEntity{Kind: "recording", ProviderKey: "recording-key", Name: "must not fill accepted empty track title", Fields: json.RawMessage(`{"title":"recording fallback"}`), FieldEvidence: json.RawMessage(`{"title":{}}`)}
	before, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}

	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 0 {
		t.Fatalf("patch = %#v, want no deletion values", patch)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("builder mutated its accepted context")
	}
}

func TestBuildPublicationProviderTagProtectionCoversAddedProviderTagFootprints(t *testing.T) {
	context := &PublicationProviderTagContext{}
	context.ReleaseOwner.Metadata = json.RawMessage(`{"local_metadata":{"version":1,"scope":"release","desired":{"release_date":{"state":"value","value":"2000","authority":"manual","provenance":{"kind":"manual"}},"release_group_mbid":{"state":"absent","authority":"manual","provenance":{"kind":"manual"}},"barcode":{"state":"value","value":"0123","authority":"manual","provenance":{"kind":"manual"}},"release_country":{"state":"value","value":"XW","authority":"manual","provenance":{"kind":"manual"}},"release_status":{"state":"absent","authority":"manual","provenance":{"kind":"manual"}}}}}`)
	context.RecordingOwner.Metadata = json.RawMessage(`{"local_metadata":{"version":1,"scope":"recording","desired":{"recording_mbid":{"state":"value","value":"33333333-3333-4333-8333-333333333333","authority":"manual","provenance":{"kind":"manual"}},"isrc":{"state":"value","value":["USAAA0000001"],"authority":"manual","provenance":{"kind":"manual"}}}}}`)

	protection, err := BuildPublicationProviderTagProtection(context)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"BARCODE", "DATE", "ISRC", "MUSICBRAINZ_RELEASEGROUPID", "MUSICBRAINZ_TRACKID", "RELEASECOUNTRY", "RELEASESTATUS"}
	if !reflect.DeepEqual(protection.Keys, want) {
		t.Fatalf("protected keys = %#v, want %#v", protection.Keys, want)
	}
	if !reflect.DeepEqual(protection.ManualAbsentFields, []string{"release_group_mbid", "release_status"}) {
		t.Fatalf("manual absent fields = %#v", protection.ManualAbsentFields)
	}
}

func publicationTagProjectionFixture() PublicationProviderTagContext {
	return PublicationProviderTagContext{
		Release:  MatchingRelease{Fields: json.RawMessage(`{}`), FieldEvidence: map[string]ProviderFieldEvidence{}},
		Position: MatchingReleaseTrack{},
	}
}
