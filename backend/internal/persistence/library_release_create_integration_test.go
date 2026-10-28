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

func TestCreateStandaloneReleaseEmptyAndRoundTripsNormalizedRowsPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repo := persistence.NewLibraryRepository(db)

	// Establish the minimal successful baseline before checking optional details.
	empty, err := repo.CreateStandaloneRelease(ctx, persistence.LibraryReleaseCreateRequest{Title: "Empty"})
	if err != nil {
		t.Fatalf("create empty standalone release: %v", err)
	}
	if empty.ReleaseID == uuid.Nil || len(empty.Media) != 0 || len(empty.Positions) != 0 {
		t.Fatalf("empty create result = %+v", empty)
	}
	for table, want := range map[string]int{"local_release": 1, "local_artist": 0, "local_recording": 0, "local_track": 0, "release_medium": 0, "release_track": 0, "track_source": 0, "release_publication_cover": 0} {
		var count int
		if err := db.NewRaw("SELECT count(*) FROM "+table).Scan(ctx, &count); err != nil || count != want {
			t.Fatalf("%s count = %d, %v; want %d", table, count, err, want)
		}
	}

	artistID, recordingID := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_artist(id,name,metadata) VALUES (?,?,'{}'::jsonb)`, artistID, "Identity Name"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_recording(id,title,metadata) VALUES (?,?,'{}'::jsonb)`, recordingID, "Existing Recording"); err != nil {
		t.Fatal(err)
	}
	durationZero := int64(0)
	request := persistence.LibraryReleaseCreateRequest{
		Title:         "Round trip",
		NewArtists:    []persistence.LibraryReleaseCreateArtist{{Key: "same-a", Name: "Repeated"}, {Key: "same-b", Name: "Repeated"}},
		NewRecordings: []persistence.LibraryReleaseCreateRecording{{Key: "blank-recording", Title: ""}},
		Credits: []persistence.LibraryReleaseCreateCredit{
			{ArtistRef: persistence.LibraryReleaseCreateArtistRef{Kind: "existing", ID: artistID}, CreditName: "Do not rename", JoinPhrase: " & ", Role: "performer"},
			{ArtistRef: persistence.LibraryReleaseCreateArtistRef{Kind: "new", Key: "same-a"}, CreditName: "First", JoinPhrase: " feat. ", Role: "composer"},
			{ArtistRef: persistence.LibraryReleaseCreateArtistRef{Kind: "new", Key: "same-b"}, CreditName: "Second", Role: "arranger"},
		},
		Media: []persistence.LibraryReleaseCreateMedium{
			{Key: "disc-a", Title: "Disc A", Format: "CD", Positions: []persistence.LibraryReleaseCreatePosition{
				{Key: "blank", DisplayedNumber: "A1", Title: "Local blank title"},
				{Key: "shared-a", DisplayedNumber: "A2", Title: "Position A", DurationMS: &durationZero, RecordingRef: &persistence.LibraryReleaseCreateRecordingRef{Kind: "existing", ID: recordingID}},
			}},
			{Key: "disc-b", Title: "Disc B", Format: "Vinyl", Positions: []persistence.LibraryReleaseCreatePosition{
				{Key: "shared-b", DisplayedNumber: "B1", Title: "Position B", RecordingRef: &persistence.LibraryReleaseCreateRecordingRef{Kind: "existing", ID: recordingID}},
				{Key: "explicit-new-blank", DisplayedNumber: "B2", Title: "Explicit blank recording", RecordingRef: &persistence.LibraryReleaseCreateRecordingRef{Kind: "new", Key: "blank-recording"}},
			}},
		},
	}
	created, err := repo.CreateStandaloneRelease(ctx, request)
	if err != nil {
		t.Fatalf("create normalized standalone release: %v", err)
	}
	if len(created.Media) != 2 || len(created.Positions) != 4 {
		t.Fatalf("create result = %+v", created)
	}
	detail, err := repo.ReadReleaseDetail(ctx, created.ReleaseID)
	if err != nil {
		t.Fatalf("read created release: %v", err)
	}
	if len(detail.Media) != 2 || detail.Media[0].Title != "Disc A" || detail.Media[0].Medium != 0 || detail.Media[1].Title != "Disc B" || detail.Media[1].Medium != 1 {
		t.Fatalf("ordered media = %+v", detail.Media)
	}
	if len(detail.Artists) != 3 || detail.Artists[0].ArtistID != artistID || detail.Artists[0].ArtistName == nil || *detail.Artists[0].ArtistName != "Identity Name" || detail.Artists[0].CreditName != "Do not rename" || detail.Artists[0].Role != "performer" || detail.Artists[0].JoinPhrase != " & " || detail.Artists[1].ArtistID == detail.Artists[2].ArtistID || detail.Artists[1].ArtistName == nil || *detail.Artists[1].ArtistName != "Repeated" || detail.Artists[2].ArtistName == nil || *detail.Artists[2].ArtistName != "Repeated" {
		t.Fatalf("ordered independent credits = %+v", detail.Artists)
	}
	if len(detail.Tracks) != 4 {
		t.Fatalf("tracks = %+v", detail.Tracks)
	}
	blank, sharedA, sharedB, explicitBlank := detail.Tracks[0], detail.Tracks[1], detail.Tracks[2], detail.Tracks[3]
	if blank.RecordingID != nil || blank.RecordingTitle != nil || blank.Title != "Local blank title" || blank.Medium != 1 || blank.DurationMS.Valid {
		t.Fatalf("omitted recording/null duration position = %+v", blank)
	}
	if sharedA.RecordingID == nil || sharedB.RecordingID == nil || *sharedA.RecordingID != recordingID || *sharedB.RecordingID != recordingID || sharedA.ID == sharedB.ID || sharedA.LocalTrackID == sharedB.LocalTrackID || sharedA.Title != "Position A" || sharedB.Title != "Position B" || !sharedA.DurationMS.Valid || sharedA.DurationMS.Int64 != 0 || sharedB.DurationMS.Valid {
		t.Fatalf("shared recording positions lost independent fields: %+v / %+v", sharedA, sharedB)
	}
	if explicitBlank.RecordingID == nil || *explicitBlank.RecordingID == uuid.Nil || explicitBlank.RecordingTitle == nil || *explicitBlank.RecordingTitle != "" || blank.RecordingID != nil {
		t.Fatalf("explicit new blank recording vs omitted recording = %+v / %+v", explicitBlank, blank)
	}
	var persistedName string
	if err := db.NewRaw(`SELECT name FROM local_artist WHERE id=?`, artistID).Scan(ctx, &persistedName); err != nil || persistedName != "Identity Name" {
		t.Fatalf("existing artist identity name = %q, %v", persistedName, err)
	}
}

func TestCreateStandaloneReleaseConflictDoesNotLeakNewIdentitiesPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
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
