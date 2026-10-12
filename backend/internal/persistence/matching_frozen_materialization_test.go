package persistence

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAcceptedProjectionToMaterializationUsesSelectedReleaseOnly(t *testing.T) {
	releaseID := uuid.New()
	trackID := uuid.New()
	recordingKey := "recording-a"
	selected := frozenTestRelease(releaseID, "release-a", `{"title":"ignored","date":"2001","label-info":[{"label":{"name":"A"}},{"label":{"name":"A"}}],"artist-credit":[{"name":"Credit name","joinphrase":" & ","artist":{"id":"artist-a"}},{"name":"Again","joinphrase":" feat. ","artist":{"id":"artist-a"}}],"normalized_tracks":[{"medium":0,"position":0,"recording_provider_key":"recording-a","title":"Track title","artist-credit":[{"name":"Guest","joinphrase":" + ","artist":{"id":"artist-a"}}]}]}`)
	selected.Tracks = []MatchingReleaseTrack{{ID: trackID, Medium: 0, Position: 0, RecordingProviderKey: recordingKey, RecordingTitle: "recording fallback"}}
	other := frozenTestRelease(uuid.New(), "release-b", `{"artist-credit":[{"name":"Wrong","artist":{"id":"artist-b"}}],"normalized_tracks":[{"medium":0,"position":0,"recording_provider_key":"recording-a","title":"wrong track"}]}`)
	other.Tracks = []MatchingReleaseTrack{{Medium: 0, Position: 0, RecordingProviderKey: recordingKey, RecordingTitle: "wrong recording"}}
	snapshot := ProviderMatchingSnapshot{Releases: []MatchingRelease{selected, other}, ProjectionEntities: []MatchingProjectionEntity{
		{Kind: "recording", ProviderKey: recordingKey, Name: "recording source", Fields: json.RawMessage(`{"title":"recording fields title"}`), FieldEvidence: json.RawMessage(`{"title":{"authority":2,"fetch_order":8}}`)},
		{Kind: "artist", ProviderKey: "artist-a", Name: "Canonical artist", Fields: json.RawMessage(`{}`), FieldEvidence: json.RawMessage(`{"name":{"authority":2,"fetch_order":9}}`)},
	}}
	assignment := FrozenTrackAssignment{ProviderTrackID: trackID, ProviderReleaseID: releaseID, ReleaseKey: "release-a", RecordingKey: recordingKey, Medium: 0, Position: 0}
	got, err := acceptedProjectionToMaterialization(snapshot, "release-a", []FrozenTrackAssignment{assignment})
	if err != nil {
		t.Fatal(err)
	}
	if got.Release.Title != "release name release-a" || got.Tracks[0].Title != "Track title" {
		t.Fatalf("unexpected titles: %#v", got)
	}
	if got.Tracks[0].TitleSource == nil || got.Tracks[0].TitleSource.EntityKind != "release" || got.Tracks[0].TitleSource.Authority != 2 || got.Tracks[0].TitleSource.FetchOrder != 2 {
		t.Fatalf("normalized track title provenance missing: %#v", got.Tracks[0].TitleSource)
	}
	if got.Release.Credits[0].Name != "Credit name" || got.Release.Credits[0].JoinPhrase != " & " || got.Release.Credits[1].Position != 1 || got.Release.Credits[1].ArtistProviderKey != "artist-a" {
		t.Fatalf("release credits lost raw ordering or repeated credited artists: %#v", got.Release.Credits)
	}
	if got.Release.Credits[0].ArtistName != "Canonical artist" || got.Release.Credits[0].ArtistNameSource == nil {
		t.Fatalf("artist entity evidence was not used: %#v", got.Release.Credits[0])
	}
	if got.Tracks[0].Credits[0].Name != "Guest" || got.Tracks[0].Credits[0].JoinPhrase != " + " {
		t.Fatalf("track credit not preserved: %#v", got.Tracks[0].Credits)
	}
	if got.Release.FieldSources["date"].Field != "date" || got.Release.FieldSources["label-info"].Field != "label-info" {
		t.Fatalf("raw accepted metadata field sources missing: %#v", got.Release.FieldSources)
	}
	if !strings.Contains(string(got.Release.Fields), `"label-info":[{"label":{"name":"A"}},{"label":{"name":"A"}}]`) {
		t.Fatalf("raw field arrays were not retained: %s", got.Release.Fields)
	}
	repeated, err := acceptedProjectionToMaterialization(snapshot, "release-a", []FrozenTrackAssignment{assignment})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, repeated) {
		t.Fatal("repeated conversion was not stable")
	}
}

func TestAcceptedProjectionToMaterializationCreditsFallbackAndExplicitEmpty(t *testing.T) {
	releaseID := uuid.New()
	key := "recording-a"
	firstTrackID, secondTrackID := uuid.New(), uuid.New()
	release := frozenTestRelease(releaseID, "release-a", `{"normalized_tracks":[{"medium":0,"position":0,"recording_provider_key":"recording-a","artist-credit":[]},{"medium":0,"position":1,"recording_provider_key":"recording-a"}]}`)
	release.Tracks = []MatchingReleaseTrack{
		{ID: firstTrackID, Medium: 0, Position: 0, RecordingProviderKey: key, RecordingTitle: "first"},
		{ID: secondTrackID, Medium: 0, Position: 1, RecordingProviderKey: key},
	}
	snapshot := ProviderMatchingSnapshot{Releases: []MatchingRelease{release}, ProjectionEntities: []MatchingProjectionEntity{{Kind: "recording", ProviderKey: key, Name: "recording name", Fields: json.RawMessage(`{"artist-credit":[{"name":"Recording display","joinphrase":" and ","artist":{"id":"artist-c"}}]}`), FieldEvidence: json.RawMessage(`{"artist-credit":{"authority":3,"fetch_order":12}}`)}}}
	assignments := []FrozenTrackAssignment{
		{ProviderTrackID: firstTrackID, ProviderReleaseID: releaseID, ReleaseKey: "release-a", RecordingKey: key, Medium: 0, Position: 0},
		{ProviderTrackID: secondTrackID, ProviderReleaseID: releaseID, ReleaseKey: "release-a", RecordingKey: key, Medium: 0, Position: 1},
	}
	got, err := acceptedProjectionToMaterialization(snapshot, "release-a", assignments)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tracks[0].Credits == nil || len(got.Tracks[0].Credits) != 0 {
		t.Fatalf("explicit empty track credits must suppress recording credits: %#v", got.Tracks[0].Credits)
	}
	if len(got.Tracks[1].Credits) != 1 || got.Tracks[1].Credits[0].Name != "Recording display" {
		t.Fatalf("absent track credits must inherit recording credits: %#v", got.Tracks[1].Credits)
	}
	if got.Tracks[1].Title != "recording name" {
		t.Fatalf("recording name should be title fallback: %q", got.Tracks[1].Title)
	}
}

func TestAcceptedProjectionToMaterializationRejectsInvalidIdentityAndMissingData(t *testing.T) {
	releaseID := uuid.New()
	trackID := uuid.New()
	release := frozenTestRelease(releaseID, "release-a", `{"normalized_tracks":[{"medium":0,"position":0,"recording_provider_key":"recording-a"}]}`)
	release.Tracks = []MatchingReleaseTrack{{ID: trackID, Medium: 0, Position: 0, RecordingProviderKey: "recording-a"}}
	snapshot := ProviderMatchingSnapshot{Releases: []MatchingRelease{release}}
	valid := FrozenTrackAssignment{ProviderTrackID: trackID, ProviderReleaseID: releaseID, ReleaseKey: "release-a", RecordingKey: "recording-a", Medium: 0, Position: 0}
	for _, test := range []struct {
		name         string
		releaseKey   string
		id           uuid.UUID
		recordingKey string
		coordinates  bool
	}{
		{name: "foreign release", releaseKey: "release-b", id: releaseID, recordingKey: "recording-a", coordinates: true},
		{name: "missing edge ID", releaseKey: "release-a", id: uuid.Nil, recordingKey: "recording-a", coordinates: true},
		{name: "missing accepted layout", releaseKey: "release-a", id: releaseID, recordingKey: "recording-missing", coordinates: true},
		{name: "missing coordinate", releaseKey: "release-a", id: releaseID, recordingKey: "recording-a", coordinates: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			assignment := valid
			assignment.ReleaseKey = test.releaseKey
			assignment.ProviderReleaseID = test.id
			assignment.RecordingKey = test.recordingKey
			if !test.coordinates {
				assignment.Medium = 9
			}
			if _, err := acceptedProjectionToMaterialization(snapshot, "release-a", []FrozenTrackAssignment{assignment}); err == nil {
				t.Fatal("expected invalid assignment to be rejected")
			}
		})
	}
	duplicateID := valid.ProviderTrackID
	second := valid
	second.Medium = 1
	second.ProviderTrackID = duplicateID
	if _, err := acceptedProjectionToMaterialization(snapshot, "release-a", []FrozenTrackAssignment{valid, second}); err == nil {
		t.Fatal("same edge ID at different coordinates must be rejected")
	}
	if _, err := acceptedProjectionToMaterialization(snapshot, "absent", nil); err == nil {
		t.Fatal("missing selected release must be rejected")
	}
	wrongID := valid
	wrongID.ProviderTrackID = uuid.New()
	if _, err := acceptedProjectionToMaterialization(snapshot, "release-a", []FrozenTrackAssignment{wrongID}); err == nil {
		t.Fatal("wrong nonzero provider track ID at a valid coordinate must be rejected")
	}
	reused, err := acceptedProjectionToMaterialization(snapshot, "release-a", []FrozenTrackAssignment{valid, valid})
	if err != nil || len(reused.Tracks) != 1 {
		t.Fatalf("reusing the same accepted assignment should materialize once: %#v, %v", reused, err)
	}
}

func TestAcceptedProjectionToMaterializationPreservesCreditProvenanceAndTitleAuthority(t *testing.T) {
	releaseID := uuid.New()
	trackID := uuid.New()
	key := "recording-a"
	release := frozenTestRelease(releaseID, "release-a", `{"normalized_tracks":[{"medium":0,"position":0,"recording_provider_key":"recording-a","artist-credit":[{"name":"Track credit","artist":{"id":"track-artist"}}]}]}`)
	release.FieldEvidence["artist-credit"] = ProviderFieldEvidence{Authority: 4, FetchOrder: 40}
	delete(release.FieldEvidence, "normalized_tracks")
	release.Credits = []MatchingReleaseCredit{
		{Position: 0, ArtistProviderKey: "release-artist-a", Name: "First", Authority: 2, FetchOrder: 11},
		{Position: 1, ArtistProviderKey: "release-artist-b", Name: "Second", Authority: 3, FetchOrder: 22},
	}
	release.Tracks = []MatchingReleaseTrack{{ID: trackID, Medium: 0, Position: 0, RecordingProviderKey: key, RecordingTitle: "track fallback"}}
	snapshot := ProviderMatchingSnapshot{
		Releases: []MatchingRelease{release},
		ProjectionEntities: []MatchingProjectionEntity{{
			Kind: "recording", ProviderKey: key, Name: "Recording.Name", Fields: json.RawMessage(`{"title":"conflicting fields.title","artist-credit":[{"name":"Recording credit","artist":{"id":"recording-artist"}}]}`),
			FieldEvidence: json.RawMessage(`{"title":{"authority":5,"fetch_order":55},"artist-credit":{"authority":6,"fetch_order":66}}`),
		}},
	}
	assignment := FrozenTrackAssignment{ProviderTrackID: trackID, ProviderReleaseID: releaseID, ReleaseKey: "release-a", RecordingKey: key}
	got, err := acceptedProjectionToMaterialization(snapshot, "release-a", []FrozenTrackAssignment{assignment})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Release.Credits) != 2 || got.Release.Credits[0].Source == nil || got.Release.Credits[0].Source.Authority != 2 || got.Release.Credits[0].Source.FetchOrder != 11 || got.Release.Credits[1].Source == nil || got.Release.Credits[1].Position != 1 || got.Release.Credits[1].Source.Authority != 3 || got.Release.Credits[1].Source.FetchOrder != 22 {
		t.Fatalf("fallback credit provenance must match every credit: %#v", got.Release.Credits)
	}
	if got.Release.FieldSources["artist-credit"].Authority != 2 {
		t.Fatalf("global fallback evidence must come from an actual credit: %#v", got.Release.FieldSources["artist-credit"])
	}
	track := got.Tracks[0]
	if track.Title != "Recording.Name" || track.TitleSource == nil || track.TitleSource.EntityKind != "recording" || track.TitleSource.Authority != 5 {
		t.Fatalf("recording name should win conflicting fields.title: %#v", track)
	}
	if len(track.Credits) != 1 || track.Credits[0].Source != nil || track.CreditSource != nil {
		t.Fatalf("normalized credits without field evidence must not manufacture provenance: %#v", track)
	}
}

func TestAcceptedProjectionToMaterializationDoesNotMutateInputs(t *testing.T) {
	release := frozenTestRelease(uuid.New(), "release-a", `{"date":"2020","label-info":[1,1]}`)
	snapshot := ProviderMatchingSnapshot{Releases: []MatchingRelease{release}}
	before, _ := json.Marshal(snapshot)
	if _, err := acceptedProjectionToMaterialization(snapshot, "release-a", nil); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(snapshot)
	if string(before) != string(after) {
		t.Fatal("converter mutated its input snapshot")
	}
}

func frozenTestRelease(id uuid.UUID, key, fields string) MatchingRelease {
	return MatchingRelease{ID: id, ProviderKey: key, Name: "release name " + key, Fields: json.RawMessage(fields), FieldEvidence: map[string]ProviderFieldEvidence{"title": {Authority: 1, FetchOrder: 1}, "artist-credit": {Authority: 1, FetchOrder: 1}, "normalized_tracks": {Authority: 2, FetchOrder: 2}, "date": {Authority: 1, FetchOrder: 1}, "label-info": {Authority: 1, FetchOrder: 1}}}
}
