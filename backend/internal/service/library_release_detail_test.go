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
		ID: releaseID, Title: "Album", MetadataVersion: 4,
		Artists: []persistence.LibraryArtistCredit{{ArtistID: artistID, ArtistName: &artistName, CreditName: "Credit", JoinPhrase: " & "}},
		Tracks: []persistence.LibraryReleaseTrackDetail{{
			ID: positionID, LocalTrackID: trackID, Medium: 1, Position: 2, Title: "Song", MetadataVersion: 7,
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
	if detail.ID != releaseID || detail.MetadataVersion != 4 || len(detail.Artists) != 1 || detail.Artists[0].ArtistID != artistID || detail.Artists[0].ArtistName == nil || *detail.Artists[0].ArtistName != artistName {
		t.Fatalf("release metadata/artist mapping = %+v", detail)
	}
	if len(detail.Tracks) != 1 || detail.Tracks[0].ID != positionID || detail.Tracks[0].LocalTrackID != trackID || detail.Tracks[0].Medium != 1 || detail.Tracks[0].Position != 2 || len(detail.Tracks[0].Sources) != 1 {
		t.Fatalf("track detail mapping = %+v", detail.Tracks)
	}
	source := detail.Tracks[0].Sources[0]
	if source.MediaVariantID != variantID || source.DecisionMethod != "automatic" || source.PolicyVersion == nil || len(source.Locations) != 1 || source.Locations[0].RootID != rootID || source.Locations[0].LocationID != locationID {
		t.Fatalf("source detail mapping = %+v", source)
	}
}

func TestLibraryReleaseDetailsPreservesNotFound(t *testing.T) {
	_, err := NewLibraryReleaseDetails(libraryReleaseDetailReaderFixture{err: persistence.ErrLibraryReleaseNotFound}).Read(context.Background(), uuid.New())
	if !errors.Is(err, ErrLibraryReleaseNotFound) {
		t.Fatalf("not found error = %v", err)
	}
}
