package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	riverdatabasesql "github.com/riverqueue/river/riverdriver/riverdatabasesql"
	rivermigrate "github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestLibraryMetadataManualSyncedLyricsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	driver := riverdatabasesql.New(database.DB)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		t.Fatalf("create River migrator: %v", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatalf("apply River migrations: %v", err)
	}
	releaseID, trackID := uuid.New(), uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,?)`, releaseID, "Release"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,?)`, trackID, "Track"); err != nil {
		t.Fatal(err)
	}
	var positionID uuid.UUID
	if err := database.NewRaw(`INSERT INTO release_track(release_id,medium,position,track_id) VALUES (?,0,1,?) RETURNING id`, releaseID, trackID).Scan(ctx, &positionID); err != nil {
		t.Fatal(err)
	}
	const publishedLyrics = `{"synced_lyrics":"published lyrics"}`
	if _, err := database.ExecContext(ctx, `INSERT INTO track_publication
		(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at)
		VALUES (?,?,?, ?, '{}'::jsonb, '{}'::jsonb, ?::jsonb, '{}'::jsonb, 1, now())`,
		positionID, uuid.New(), uuid.New(), "release/track.flac", publishedLyrics); err != nil {
		t.Fatalf("seed current publication projection: %v", err)
	}
	var operationsBefore, jobsBefore int
	if err := database.NewRaw(`SELECT count(*) FROM operation`).Scan(ctx, &operationsBefore); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &jobsBefore); err != nil {
		t.Fatal(err)
	}

	service := NewLibraryMetadataService(persistence.NewLibraryRepository(database))
	const lyrics = "[00:01.00]Привет — 世界\r\n[00:02.50]♫ café\r\n"
	lyricsJSON, err := json.Marshal(lyrics)
	if err != nil {
		t.Fatal(err)
	}
	changes := libraryMetadataChanges(t, `{"synced_lyrics":{"state":"value","value":`+string(lyricsJSON)+`}}`)
	updated, err := service.Update(ctx, librarymetadata.ScopePosition, releaseID, positionID, 1, changes)
	if err != nil {
		t.Fatalf("save synced lyrics: %v", err)
	}
	if updated.Revision != 2 {
		t.Fatalf("revision = %d, want 2", updated.Revision)
	}
	assertManualLyrics := func(expectedRevision int64, expectedState librarymetadata.ValueState) {
		t.Helper()
		stored, err := service.Read(ctx, librarymetadata.ScopePosition, releaseID, positionID)
		if err != nil {
			t.Fatalf("read metadata: %v", err)
		}
		field := stored.Envelope.Desired["synced_lyrics"]
		if stored.Revision != expectedRevision || field.State != expectedState || field.Authority != librarymetadata.AuthorityManual || field.Provenance.Kind != librarymetadata.ProvenanceManual {
			t.Fatalf("stored metadata = revision %d, field %+v", stored.Revision, field)
		}
		if expectedState == librarymetadata.StateValue && field.Value != lyrics {
			t.Fatalf("stored lyrics = %#v, want exact bytes %#v", field.Value, lyrics)
		}
		var actualMetadata []byte
		if err := database.NewRaw(`SELECT actual_metadata::text FROM track_publication WHERE position_id=?`, positionID).Scan(ctx, &actualMetadata); err != nil {
			t.Fatalf("read current publication metadata projection: %v", err)
		}
		var projection map[string]string
		if err := json.Unmarshal(actualMetadata, &projection); err != nil {
			t.Fatalf("decode current publication metadata projection: %v", err)
		}
		if projection["synced_lyrics"] != "published lyrics" {
			t.Fatalf("current publication metadata = %s, want unchanged published lyrics", actualMetadata)
		}
	}
	assertManualLyrics(2, librarymetadata.StateValue)

	if _, err := service.Update(ctx, librarymetadata.ScopePosition, releaseID, positionID, 1, changes); !errors.Is(err, ErrLibraryMetadataConflict) {
		t.Fatalf("stale write error = %v, want revision conflict", err)
	}
	assertManualLyrics(2, librarymetadata.StateValue)

	empty := libraryMetadataChanges(t, `{"synced_lyrics":{"state":"explicit_empty"}}`)
	cleared, err := service.Update(ctx, librarymetadata.ScopePosition, releaseID, positionID, 2, empty)
	if err != nil {
		t.Fatalf("save explicit-empty lyrics: %v", err)
	}
	if cleared.Revision != 3 {
		t.Fatalf("explicit-empty revision = %d, want 3", cleared.Revision)
	}
	assertManualLyrics(3, librarymetadata.StateExplicitEmpty)

	invalid := libraryMetadataChanges(t, `{"synced_lyrics":{"state":"value","value":"not an LRC"}}`)
	if _, err := service.Update(ctx, librarymetadata.ScopePosition, releaseID, positionID, 3, invalid); !errors.Is(err, ErrLibraryMetadataInvalidRequest) {
		t.Fatalf("invalid lyrics error = %v, want invalid request", err)
	}
	if _, err := service.Update(ctx, librarymetadata.ScopePosition, releaseID, uuid.New(), 3, empty); !errors.Is(err, ErrLibraryMetadataNotFound) {
		t.Fatalf("foreign position owner error = %v, want owner not found", err)
	}
	assertManualLyrics(3, librarymetadata.StateExplicitEmpty)

	var releaseRevision, trackRevision int64
	if err := database.NewRaw(`SELECT metadata_revision FROM local_release WHERE id=?`, releaseID).Scan(ctx, &releaseRevision); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw(`SELECT metadata_revision FROM local_track WHERE id=?`, trackID).Scan(ctx, &trackRevision); err != nil {
		t.Fatal(err)
	}
	if releaseRevision != 1 || trackRevision != 1 {
		t.Fatalf("unrelated owner revisions release=%d track=%d, want both unchanged at 1", releaseRevision, trackRevision)
	}
	var operationsAfter, jobsAfter int
	if err := database.NewRaw(`SELECT count(*) FROM operation`).Scan(ctx, &operationsAfter); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &jobsAfter); err != nil {
		t.Fatal(err)
	}
	if operationsAfter != operationsBefore || jobsAfter != jobsBefore {
		t.Fatalf("desired metadata writes enqueued work: operations %d->%d, river jobs %d->%d", operationsBefore, operationsAfter, jobsBefore, jobsAfter)
	}
}
