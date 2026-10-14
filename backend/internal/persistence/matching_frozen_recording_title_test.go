package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestAcceptedProjectionToMaterializationSeparatesRecordingAndTrackTitles(t *testing.T) {
	releaseID, trackID := uuid.New(), uuid.New()
	release := frozenTestRelease(releaseID, "release-a", `{"normalized_tracks":[{"medium":0,"position":0,"recording_provider_key":"recording-a","title":"Track override"}]}`)
	release.Tracks = []MatchingReleaseTrack{{ID: trackID, Medium: 0, Position: 0, RecordingProviderKey: "recording-a", RecordingTitle: "Release recording title"}}
	snapshot := ProviderMatchingSnapshot{
		Releases: []MatchingRelease{release},
		ProjectionEntities: []MatchingProjectionEntity{{
			Kind: "recording", ProviderKey: "recording-a", Name: "Original recording title",
			Fields:        json.RawMessage(`{"title":"conflicting fields title"}`),
			FieldEvidence: json.RawMessage(`{"title":{"authority":3,"fetch_order":17}}`),
		}},
	}
	assignment := FrozenTrackAssignment{ProviderTrackID: trackID, ProviderReleaseID: releaseID, ReleaseKey: "release-a", RecordingKey: "recording-a"}
	materialized, err := acceptedProjectionToMaterialization(snapshot, "release-a", []FrozenTrackAssignment{assignment})
	if err != nil {
		t.Fatal(err)
	}
	track := materialized.Tracks[0]
	if track.RecordingTitle != "Original recording title" || track.RecordingTitleSource == nil || track.RecordingTitleSource.Authority != 3 || track.RecordingTitleSource.FetchOrder != 17 {
		t.Fatalf("recording title projection = %+v, want original entity title and provenance", track)
	}
	if track.Title != "Track override" || track.TitleSource == nil || track.TitleSource.EntityKind != "release" {
		t.Fatalf("release-track title projection = %+v, want independent normalized title override", track)
	}
}

func TestAcceptedProjectionSeparatesRecordingCreditsFromTrackOverride(t *testing.T) {
	releaseID, trackID := uuid.New(), uuid.New()
	release := frozenTestRelease(releaseID, "release-a", `{"artist-credit":[{"name":"Recording artist","artist":{"id":"recording-artist"}}],"normalized_tracks":[{"medium":0,"position":0,"recording_provider_key":"recording-a","artist-credit":[{"name":"Track artist","artist":{"id":"track-artist"}}]}]}`)
	release.Tracks = []MatchingReleaseTrack{{ID: trackID, Medium: 0, Position: 0, RecordingProviderKey: "recording-a", RecordingTitle: "Original"}}
	snapshot := ProviderMatchingSnapshot{
		Releases: []MatchingRelease{release},
		ProjectionEntities: []MatchingProjectionEntity{{
			Kind: "recording", ProviderKey: "recording-a", Fields: json.RawMessage(`{"artist-credit":[{"name":"Recording artist","artist":{"id":"recording-artist"}}]}`),
			FieldEvidence: json.RawMessage(`{"artist-credit":{"authority":2,"fetch_order":12}}`),
		}},
	}
	materialized, err := acceptedProjectionToMaterialization(snapshot, "release-a", []FrozenTrackAssignment{{ProviderTrackID: trackID, ProviderReleaseID: releaseID, ReleaseKey: "release-a", RecordingKey: "recording-a"}})
	if err != nil {
		t.Fatal(err)
	}
	track := materialized.Tracks[0]
	if len(track.RecordingCredits) != 1 || track.RecordingCredits[0].Name != "Recording artist" || track.RecordingCreditSource == nil || track.RecordingCreditSource.Authority != 2 {
		t.Fatalf("recording credits = %+v, want immutable recording credit and provenance", track.RecordingCredits)
	}
	if len(track.Credits) != 1 || track.Credits[0].Name != "Track artist" || track.CreditSource == nil || track.CreditSource.EntityKind != "release" {
		t.Fatalf("track credits = %+v, want normalized track override", track.Credits)
	}
}
