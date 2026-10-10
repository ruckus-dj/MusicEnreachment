package persistence

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAcceptedProjectionRevisionIgnoresMutableResponseCacheMetadata(t *testing.T) {
	revision := "response-revision"
	fetchedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	base := &ProviderCachedResponse{
		CacheKey: "search", Payload: json.RawMessage(`{"releases":[]}`),
		FetchedAt: &fetchedAt, Revision: &revision, Generation: 1, FetchOrder: 10,
	}
	clone := *base
	otherFetchedAt := fetchedAt.Add(time.Hour)
	clone.FetchedAt = &otherFetchedAt
	clone.Generation = 99
	clone.FetchOrder = 1000
	first := acceptedProjectionRevision(nil, nil, map[string]*ProviderCachedResponse{"search": base})
	second := acceptedProjectionRevision(nil, nil, map[string]*ProviderCachedResponse{"search": &clone})
	if first != second {
		t.Fatalf("mutable cache metadata changed accepted projection revision: %q != %q", first, second)
	}

	clone.Payload = json.RawMessage(`{"releases":[{"id":"changed"}]}`)
	if changed := acceptedProjectionRevision(nil, nil, map[string]*ProviderCachedResponse{"search": &clone}); changed == first {
		t.Fatal("changed response payload did not change accepted projection revision")
	}
	clone = *base
	changedRevision := "new-response-revision"
	clone.Revision = &changedRevision
	if changed := acceptedProjectionRevision(nil, nil, map[string]*ProviderCachedResponse{"search": &clone}); changed == first {
		t.Fatal("changed response revision did not change accepted projection revision")
	}
}

func TestMatchingReleaseTrackLengthUsesAcceptedNormalizedTrackIdentity(t *testing.T) {
	fields := json.RawMessage(`{"normalized_tracks":[{"medium":0,"position":0,"recording_provider_key":"recording","length":9000}]}`)
	length := matchingReleaseTrackLength(fields, 0, 0, "recording")
	if length == nil || *length != 9000 {
		t.Fatalf("normalized release track length = %v, want 9000ms", length)
	}
	for _, test := range []struct {
		name      string
		medium    int
		position  int
		recording string
	}{
		{name: "wrong medium", medium: 1, position: 0, recording: "recording"},
		{name: "wrong position", medium: 0, position: 1, recording: "recording"},
		{name: "wrong recording", medium: 0, position: 0, recording: "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := matchingReleaseTrackLength(fields, test.medium, test.position, test.recording); got != nil {
				t.Fatalf("unexpected length for mismatched normalized track: %d", *got)
			}
		})
	}
	for _, value := range []string{"-1", `"9000"`, `1.5`} {
		invalid := json.RawMessage(`{"normalized_tracks":[{"medium":0,"position":0,"recording_provider_key":"recording","length":` + value + `}]}`)
		if got := matchingReleaseTrackLength(invalid, 0, 0, "recording"); got != nil {
			t.Fatalf("invalid track length %s accepted as %d", value, *got)
		}
	}
}

func TestMatchingRecordingLengthRejectsNullAndPreservesExplicitZero(t *testing.T) {
	if got := matchingRecordingLength(json.RawMessage(`{}`)); got != nil {
		t.Fatalf("omitted recording length = %d, want nil", *got)
	}
	if got := matchingRecordingLength(json.RawMessage(`{"length":null}`)); got != nil {
		t.Fatalf("null recording length = %d, want nil", *got)
	}
	if got := matchingRecordingLength(json.RawMessage(`{"length":-1}`)); got != nil {
		t.Fatalf("negative recording length = %d, want nil", *got)
	}
	zero := matchingRecordingLength(json.RawMessage(`{"length":0}`))
	if zero == nil || *zero != 0 {
		t.Fatalf("explicit zero recording length = %v, want 0", zero)
	}
	positive := matchingRecordingLength(json.RawMessage(`{"length":9000}`))
	if positive == nil || *positive != 9000 {
		t.Fatalf("positive recording length = %v, want 9000", positive)
	}
}

func TestMatchingReleaseTrackLengthRejectsNullAndPreservesExplicitZero(t *testing.T) {
	projection := func(value string) json.RawMessage {
		return json.RawMessage(`{"normalized_tracks":[{"medium":0,"position":0,"recording_provider_key":"recording","length":` + value + `}]}`)
	}
	omitted := json.RawMessage(`{"normalized_tracks":[{"medium":0,"position":0,"recording_provider_key":"recording"}]}`)
	if got := matchingReleaseTrackLength(omitted, 0, 0, "recording"); got != nil {
		t.Fatalf("omitted release track length = %d, want nil", *got)
	}
	if got := matchingReleaseTrackLength(projection("null"), 0, 0, "recording"); got != nil {
		t.Fatalf("null release track length = %d, want nil", *got)
	}
	if got := matchingReleaseTrackLength(projection("-1"), 0, 0, "recording"); got != nil {
		t.Fatalf("negative release track length = %d, want nil", *got)
	}
	if got := matchingReleaseTrackLength(projection("0"), 0, 0, "recording"); got == nil || *got != 0 {
		t.Fatalf("explicit zero release track length = %v, want 0", got)
	}
	if got := matchingReleaseTrackLength(projection("9000"), 0, 0, "recording"); got == nil || *got != 9000 {
		t.Fatalf("positive release track length = %v, want 9000", got)
	}
}
