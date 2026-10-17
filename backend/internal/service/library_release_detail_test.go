package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type libraryReleaseDetailReaderFixture struct {
	detail *persistence.LibraryReleaseDetail
	err    error
}

func (fixture libraryReleaseDetailReaderFixture) ReadReleaseDetail(context.Context, uuid.UUID) (*persistence.LibraryReleaseDetail, error) {
	return fixture.detail, fixture.err
}

func TestLibraryReleaseDetailsMapsPersistenceReadModel(t *testing.T) {
	artistID, releaseID, trackID, positionID, variantID, rootID, locationID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	artistName := "Local Artist"
	policy := "policy-v1"
	reader := libraryReleaseDetailReaderFixture{detail: &persistence.LibraryReleaseDetail{
		ID: releaseID, Title: "Album", MetadataVersion: 4, MetadataRevision: 9,
		PrimaryArtistID: &artistID, PrimaryArtistCandidates: []uuid.UUID{artistID},
		Artists: []persistence.LibraryArtistCredit{{ArtistID: artistID, ArtistName: &artistName, CreditName: "Credit", JoinPhrase: " & "}},
		Tracks: []persistence.LibraryReleaseTrackDetail{{
			ID: positionID, LocalTrackID: trackID, Medium: 1, Position: 2, Title: "Song", MetadataVersion: 7,
			MetadataRevision: 5, TrackMetadataRevision: 12, RecordingMetadataRevision: int64Pointer(3),
			PrimaryArtistCandidates: []uuid.UUID{artistID},
			Sources: []persistence.LibraryTrackSourceDetail{{
				MediaVariantID: variantID, Selected: true, Compatible: true, DecisionMethod: "automatic", PolicyVersion: &policy,
				Locations: []persistence.LibrarySourceLocationDetail{{RootID: rootID, LocationID: locationID, RelativePath: "a/song.flac", Mtime: sql.NullTime{Valid: true}}},
			}},
		}},
	}}
	detail, err := NewLibraryReleaseDetails(reader).Read(context.Background(), releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ID != releaseID || detail.MetadataVersion != 4 || detail.MetadataRevision != 9 || detail.PrimaryArtist.Status != LibraryArtistExplicit || detail.PrimaryArtist.EffectiveArtistID == nil || *detail.PrimaryArtist.EffectiveArtistID != artistID || len(detail.Artists) != 1 || detail.Artists[0].ArtistID != artistID || detail.Artists[0].ArtistName == nil || *detail.Artists[0].ArtistName != artistName {
		t.Fatalf("release metadata/artist mapping = %+v", detail)
	}
	if len(detail.Tracks) != 1 || detail.Tracks[0].ID != positionID || detail.Tracks[0].LocalTrackID != trackID || detail.Tracks[0].Medium != 1 || detail.Tracks[0].Position != 2 || detail.Tracks[0].MetadataRevision != 5 || detail.Tracks[0].TrackMetadataRevision != 12 || detail.Tracks[0].RecordingMetadataRevision == nil || *detail.Tracks[0].RecordingMetadataRevision != 3 || detail.Tracks[0].PrimaryArtist.Status != LibraryArtistAutomatic || len(detail.Tracks[0].Sources) != 1 {
		t.Fatalf("track detail mapping = %+v", detail.Tracks)
	}
	source := detail.Tracks[0].Sources[0]
	if source.MediaVariantID != variantID || source.DecisionMethod != "automatic" || source.PolicyVersion == nil || len(source.Locations) != 1 || source.Locations[0].RootID != rootID || source.Locations[0].LocationID != locationID {
		t.Fatalf("source detail mapping = %+v", source)
	}
}

func TestResolveLibraryArtistDoesNotChooseFirstAmbiguousCredit(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	mbid := uuid.New()
	if first.String() > second.String() {
		first, second = second, first
	}
	resolution := resolveLibraryArtist(nil, []uuid.UUID{first, second})
	if resolution.Status != LibraryArtistRequired || resolution.EffectiveArtistID != nil {
		t.Fatalf("ambiguous artists were resolved arbitrarily: %+v", resolution)
	}
	invalid := resolveLibraryArtist(&first, []uuid.UUID{second})
	if invalid.Status != LibraryArtistInvalid || invalid.EffectiveArtistID != nil {
		t.Fatalf("non-member explicit artist was accepted: %+v", invalid)
	}
	selected := resolveLibraryArtist(&first, []uuid.UUID{first, second}, map[uuid.UUID]*uuid.UUID{first: &mbid})
	if selected.Status != LibraryArtistExplicit || selected.EffectiveArtistMBID == nil || *selected.EffectiveArtistMBID != mbid {
		t.Fatalf("selected artist identity = %+v", selected)
	}
	ambiguous := resolveLibraryArtist(nil, []uuid.UUID{first}, map[uuid.UUID]*uuid.UUID{first: &mbid})
	if ambiguous.Status != LibraryArtistAutomatic || ambiguous.EffectiveArtistMBID == nil {
		t.Fatalf("sole candidate identity = %+v", ambiguous)
	}
	duplicateMBID := resolveLibraryArtist(&first, []uuid.UUID{first, second}, map[uuid.UUID]*uuid.UUID{first: &mbid, second: &mbid})
	if duplicateMBID.Status != LibraryArtistExplicit || duplicateMBID.EffectiveArtistMBID != nil {
		t.Fatalf("ambiguous MusicBrainz links were exposed: %+v", duplicateMBID)
	}
}

func TestLibraryReleaseDetailsPreservesNotFound(t *testing.T) {
	_, err := NewLibraryReleaseDetails(libraryReleaseDetailReaderFixture{err: persistence.ErrLibraryReleaseNotFound}).Read(context.Background(), uuid.New())
	if !errors.Is(err, ErrLibraryReleaseNotFound) {
		t.Fatalf("not found error = %v", err)
	}
}
