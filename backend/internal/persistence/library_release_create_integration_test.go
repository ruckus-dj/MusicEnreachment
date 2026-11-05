//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestCreateStandaloneReleaseConflictDoesNotLeakNewIdentitiesPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	repo := persistence.NewLibraryRepository(db)
	releaseID := uuid.New()
	if _, err := repo.CreateStandaloneRelease(ctx, persistence.LibraryReleaseCreateRequest{ReleaseID: &releaseID, Title: "Already exists"}); err != nil {
		t.Fatalf("successful conflict baseline: %v", err)
	}
	_, err := repo.CreateStandaloneRelease(ctx, persistence.LibraryReleaseCreateRequest{
		ReleaseID: &releaseID, Title: "Must roll back",
		NewArtists:    []persistence.LibraryReleaseCreateArtist{{Key: "a", Name: "No leak"}},
		Credits:       []persistence.LibraryReleaseCreateCredit{{ArtistRef: persistence.LibraryReleaseCreateArtistRef{Kind: "new", Key: "a"}, CreditName: "No leak", Role: "performer"}},
		NewRecordings: []persistence.LibraryReleaseCreateRecording{{Key: "r", Title: "No leak"}},
		Media:         []persistence.LibraryReleaseCreateMedium{{Key: "m", Positions: []persistence.LibraryReleaseCreatePosition{{Key: "p", Title: "No leak", RecordingRef: &persistence.LibraryReleaseCreateRecordingRef{Kind: "new", Key: "r"}}}}},
	})
	if !errors.Is(err, persistence.ErrLibraryReleaseCreateConflict) {
		t.Fatalf("duplicate release ID error = %v", err)
	}
	for table, want := range map[string]int{"local_release": 1, "local_artist": 0, "local_recording": 0, "local_track": 0, "release_medium": 0, "release_track": 0} {
		var count int
		if err := db.NewRaw("SELECT count(*) FROM "+table).Scan(ctx, &count); err != nil || count != want {
			t.Fatalf("after conflict %s count = %d, %v; want %d", table, count, err, want)
		}
	}
}
