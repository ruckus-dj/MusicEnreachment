package persistence

import (
	"encoding/json"
	"reflect"
	"testing"

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
		"MUSICBRAINZ_ALBUMARTISTID": {"11111111-1111-4111-8111-111111111111"},
		"ARTIST":                    {"Track Credit feat. "}, "ARTISTS": {"Canonical Track Artist"},
		"MUSICBRAINZ_ARTISTID": {"22222222-2222-4222-8222-222222222222"},
	}
	if !reflect.DeepEqual(patch, want) {
		t.Fatalf("patch = %#v, want %#v", patch, want)
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
