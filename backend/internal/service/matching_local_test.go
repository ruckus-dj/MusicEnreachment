package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type matchingLocalTestRepository struct {
	intent       persistence.MatchingLocalIntent
	preview      persistence.MatchingLocalPreview
	err          error
	confirmCalls int
}

func (r *matchingLocalTestRepository) PreviewMatchingLocal(_ context.Context, _ uuid.UUID, intent persistence.MatchingLocalIntent) (persistence.MatchingLocalPreview, error) {
	r.intent = intent
	return r.preview, r.err
}

func (r *matchingLocalTestRepository) ConfirmMatchingLocal(_ context.Context, _ uuid.UUID, intent persistence.MatchingLocalIntent, _ string, _ uuid.UUID) (persistence.MatchingLocalPreview, error) {
	r.intent = intent
	r.confirmCalls++
	return r.preview, r.err
}

func (*matchingLocalTestRepository) ListLocalReleases(context.Context) (persistence.MatchingLocalReleaseCatalog, error) {
	return persistence.MatchingLocalReleaseCatalog{}, nil
}

func TestMatchingLocalServiceAllowsOneSourceOnSeveralExplicitRows(t *testing.T) {
	member := uuid.New()
	repository := &matchingLocalTestRepository{}
	intent := MatchingLocalIntent{ExpectedGroupRevision: "g1", ReleaseTitle: "Local release", Tracks: []MatchingLocalTrack{
		{Medium: 1, Position: 2, Title: "Second", FileMemberIDs: []uuid.UUID{member}},
		{Medium: 1, Position: 1, Title: "First", FileMemberIDs: []uuid.UUID{member}},
	}}
	if _, err := NewMatchingLocalService(repository).Preview(context.Background(), uuid.New(), intent); err != nil {
		t.Fatal(err)
	}
	if len(repository.intent.Tracks) != 2 || repository.intent.Tracks[0].Position != 2 {
		t.Fatalf("track order unexpectedly changed: %+v", repository.intent.Tracks)
	}
}

func TestMatchingLocalServiceAcceptsEmptyTextFieldsAndPreservesOrder(t *testing.T) {
	member := uuid.New()
	repository := &matchingLocalTestRepository{}
	intent := MatchingLocalIntent{ExpectedGroupRevision: "g1", ReleaseTitle: "", ReleaseArtists: []MatchingLocalArtist{{Name: ""}, {Name: "Second"}}, Tracks: []MatchingLocalTrack{
		{Medium: 1, Position: 1, DisplayedNumber: "", Title: "", Artists: []MatchingLocalArtist{{Name: ""}}, FileMemberIDs: []uuid.UUID{member}},
	}}
	if _, err := NewMatchingLocalService(repository).Preview(context.Background(), uuid.New(), intent); err != nil {
		t.Fatalf("empty text fields must be accepted: %v", err)
	}
	if repository.intent.ReleaseTitle != "" {
		t.Fatalf("release title = %q, want explicit empty preserved", repository.intent.ReleaseTitle)
	}
	if len(repository.intent.ReleaseArtists) != 2 || repository.intent.ReleaseArtists[0].Name != "" || repository.intent.ReleaseArtists[1].Name != "Second" {
		t.Fatalf("release artists = %+v, want empty name kept in order", repository.intent.ReleaseArtists)
	}
	if len(repository.intent.Tracks) != 1 {
		t.Fatalf("tracks = %+v, want one track", repository.intent.Tracks)
	}
	track := repository.intent.Tracks[0]
	if track.Title != "" || len(track.Artists) != 1 || track.Artists[0].Name != "" {
		t.Fatalf("track text = %+v, want empty title and empty artist name preserved", track)
	}
}

func TestMatchingLocalServiceAcceptsEmptyArtistArrays(t *testing.T) {
	repository := &matchingLocalTestRepository{}
	intent := MatchingLocalIntent{ExpectedGroupRevision: "g1", ReleaseArtists: []MatchingLocalArtist{}, Tracks: []MatchingLocalTrack{
		{Medium: 1, Position: 1, FileMemberIDs: []uuid.UUID{uuid.New()}},
	}}
	if _, err := NewMatchingLocalService(repository).Preview(context.Background(), uuid.New(), intent); err != nil {
		t.Fatalf("empty artist arrays must be accepted: %v", err)
	}
	if len(repository.intent.ReleaseArtists) != 0 || len(repository.intent.Tracks[0].Artists) != 0 {
		t.Fatalf("empty artists unexpectedly populated: %+v %+v", repository.intent.ReleaseArtists, repository.intent.Tracks[0].Artists)
	}
}

func TestMatchingLocalServiceMapsExistingCatalogWithoutDuplicatingArtists(t *testing.T) {
	groupID, releaseID, trackID := uuid.New(), uuid.New(), uuid.New()
	repository := &matchingLocalTestRepository{preview: persistence.MatchingLocalPreview{
		GroupID: groupID,
		Tracks:  []persistence.MatchingLocalTrack{{LocalTrackID: &trackID, Artists: []persistence.MatchingLocalArtist{{Name: "Track artist"}}}},
		LocalCatalog: &persistence.MatchingLocalRelease{ID: releaseID, Title: "Local", Artists: []persistence.MatchingLocalArtist{{Name: "Release artist"}},
			Tracks: []persistence.MatchingLocalTrack{{LocalTrackID: &trackID, SourceMediaIDs: []uuid.UUID{uuid.New()}, Artists: []persistence.MatchingLocalArtist{{Name: "Track artist"}}}}},
	}}
	result, err := NewMatchingLocalService(repository).Preview(context.Background(), groupID, MatchingLocalIntent{ExpectedGroupRevision: "r1", Tracks: []MatchingLocalTrack{{Medium: 1, Position: 1, FileMemberIDs: []uuid.UUID{uuid.New()}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tracks) != 1 || len(result.Tracks[0].Artists) != 1 || result.LocalCatalog == nil || result.LocalCatalog.ID != releaseID ||
		len(result.LocalCatalog.Artists) != 1 || len(result.LocalCatalog.Tracks) != 1 || len(result.LocalCatalog.Tracks[0].SourceMediaIDs) != 1 {
		t.Fatalf("local preview catalog was not mapped correctly: %+v", result)
	}
}

func TestMatchingLocalServiceStillRequiresCoordinatesAndSourceMembers(t *testing.T) {
	for name, track := range map[string]MatchingLocalTrack{
		"missing medium":   {Medium: 0, Position: 1, Title: "", FileMemberIDs: []uuid.UUID{uuid.New()}},
		"missing position": {Medium: 1, Position: 0, Title: "", FileMemberIDs: []uuid.UUID{uuid.New()}},
		"no source member": {Medium: 1, Position: 1, Title: ""},
	} {
		repository := &matchingLocalTestRepository{}
		intent := MatchingLocalIntent{ExpectedGroupRevision: "g1", ReleaseTitle: "", Tracks: []MatchingLocalTrack{track}}
		if _, err := NewMatchingLocalService(repository).Preview(context.Background(), uuid.New(), intent); !errors.Is(err, ErrMatchingLocalInvalid) {
			t.Fatalf("%s: error = %v, want invalid", name, err)
		}
	}
}

func TestMatchingLocalServiceRejectsImplicitOrInvalidIntentBeforeRepository(t *testing.T) {
	repository := &matchingLocalTestRepository{}
	_, err := NewMatchingLocalService(repository).Preview(context.Background(), uuid.New(), MatchingLocalIntent{ExpectedGroupRevision: "g1", ReleaseTitle: "Explicit", Tracks: []MatchingLocalTrack{{Medium: 0, Position: 1, Title: "Track", FileMemberIDs: []uuid.UUID{uuid.New()}}}})
	if !errors.Is(err, ErrMatchingLocalInvalid) {
		t.Fatalf("error = %v, want invalid", err)
	}
}
