package persistence

import (
	"encoding/json"
	"testing"
	"time"

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

func TestFrozenManualCaptureMustMatchCurrentCaptureAndLocations(t *testing.T) {
	memberID, workID, rootID, locationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	mtime := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	tags := json.RawMessage(`{"title":"captured"}`)
	capture := IncomingGroupingCapture{VariantID: memberID, WorkID: workID, RootID: rootID, LocationID: locationID,
		ConfiguredPath: "/music", InventoryPath: "/music", RelativePath: "album/track.flac", SizeBytes: 42, Mtime: mtime, Tags: tags}
	location := IncomingGroupingMemberLocation{VariantID: memberID, WorkID: workID, RootID: rootID, LocationID: locationID,
		ConfiguredPath: "/music", InventoryPath: "/music", RelativePath: "album/track.flac", SizeBytes: 42, Mtime: mtime}
	target, err := json.Marshal(automaticArtistTargetCapture{SchemaVersion: 2, GroupID: uuid.New(), GroupRevision: "r1", Members: []automaticArtistTargetMember{{VariantID: memberID, Locations: []IncomingGroupingMemberLocation{location}, Captures: []IncomingGroupingCapture{capture}}}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := []MatchingAcquisitionCapturedMember{{MemberID: memberID, WorkID: workID, Tags: tags}}
	members := []IncomingMatchMemberFence{{FileMemberID: memberID, WorkID: workID}}
	if !frozenManualCaptureIsCurrent(target, metadata, members, []IncomingGroupingCapture{capture}) {
		t.Fatal("unchanged persisted capture should match the frozen target")
	}
	changed := capture
	changed.SizeBytes++
	if frozenManualCaptureIsCurrent(target, metadata, members, []IncomingGroupingCapture{changed}) {
		t.Fatal("changed persisted capture must not match the frozen target")
	}
}

func frozenManualCaptureTarget(t *testing.T, memberID, workID uuid.UUID, captures ...IncomingGroupingCapture) (json.RawMessage, []MatchingAcquisitionCapturedMember, []IncomingMatchMemberFence) {
	t.Helper()
	if len(captures) == 0 {
		t.Fatal("frozen manual capture target requires at least one capture")
	}
	locations := make([]IncomingGroupingMemberLocation, 0, len(captures))
	for _, capture := range captures {
		locations = append(locations, IncomingGroupingMemberLocation{
			VariantID: capture.VariantID, RootID: capture.RootID, WorkID: capture.WorkID, LocationID: capture.LocationID,
			ConfiguredPath: capture.ConfiguredPath, InventoryPath: capture.InventoryPath, RelativePath: capture.RelativePath,
			SizeBytes: capture.SizeBytes, Mtime: capture.Mtime,
		})
	}
	target, err := json.Marshal(automaticArtistTargetCapture{SchemaVersion: 2, GroupID: uuid.New(), GroupRevision: "r1",
		Members: []automaticArtistTargetMember{{VariantID: memberID, Locations: locations, Captures: captures}}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := []MatchingAcquisitionCapturedMember{{MemberID: memberID, WorkID: workID, Tags: captures[0].Tags}}
	members := []IncomingMatchMemberFence{{FileMemberID: memberID, WorkID: workID}}
	return target, metadata, members
}

func TestFrozenManualCaptureIsCurrentScopesToTargetMembers(t *testing.T) {
	memberID, workID, rootID, locationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	mtime := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	tags := json.RawMessage(`{"title":"captured"}`)
	capture := IncomingGroupingCapture{VariantID: memberID, WorkID: workID, RootID: rootID, LocationID: locationID,
		ConfiguredPath: "/music", InventoryPath: "/music", RelativePath: "album/track.flac", SizeBytes: 42, Mtime: mtime, Tags: tags}
	target, metadata, members := frozenManualCaptureTarget(t, memberID, workID, capture)

	unrelated := capture
	unrelated.VariantID, unrelated.WorkID, unrelated.RootID, unrelated.LocationID = uuid.New(), uuid.New(), uuid.New(), uuid.New()
	unrelated.RelativePath = "other/group.flac"
	if !frozenManualCaptureIsCurrent(target, metadata, members, []IncomingGroupingCapture{capture, unrelated}) {
		t.Fatal("unrelated inventory capture must not invalidate the frozen target scope")
	}
	if frozenManualCaptureIsCurrent(target, metadata, members, []IncomingGroupingCapture{unrelated}) {
		t.Fatal("missing target capture must not match the frozen target")
	}
	if frozenManualCaptureIsCurrent(target, metadata, members, []IncomingGroupingCapture{capture, capture}) {
		t.Fatal("duplicated persisted capture must not match the frozen target")
	}
}

func TestFrozenManualCaptureIsCurrentRejectsSecondaryLocationChange(t *testing.T) {
	memberID, workID, rootID := uuid.New(), uuid.New(), uuid.New()
	mtime := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	tags := json.RawMessage(`{"title":"captured"}`)
	first := IncomingGroupingCapture{VariantID: memberID, WorkID: workID, RootID: rootID, LocationID: uuid.New(),
		ConfiguredPath: "/music", InventoryPath: "/music", RelativePath: "album/track.flac", SizeBytes: 42, Mtime: mtime, Tags: tags}
	second := first
	second.LocationID = uuid.New()
	second.RelativePath = "album/disc/track.flac"
	target, metadata, members := frozenManualCaptureTarget(t, memberID, workID, first, second)
	if !frozenManualCaptureIsCurrent(target, metadata, members, []IncomingGroupingCapture{first, second}) {
		t.Fatal("unchanged secondary location must match the frozen target")
	}
	changed := second
	changed.SizeBytes++
	if frozenManualCaptureIsCurrent(target, metadata, members, []IncomingGroupingCapture{first, changed}) {
		t.Fatal("changed secondary location must not match the frozen target")
	}
}
