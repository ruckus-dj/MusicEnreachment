package persistence

import (
	"encoding/json"
	"reflect"
	"testing"
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

func publicationTagProjectionFixture() PublicationProviderTagContext {
	return PublicationProviderTagContext{
		Release:  MatchingRelease{Fields: json.RawMessage(`{}`), FieldEvidence: map[string]ProviderFieldEvidence{}},
		Position: MatchingReleaseTrack{},
	}
}
