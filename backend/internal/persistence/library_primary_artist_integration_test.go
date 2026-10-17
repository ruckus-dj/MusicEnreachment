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

func TestSetLibraryPrimaryArtistWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewLibraryRepository(db)
	releaseID, artistA, artistB, outsideArtist := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	trackID, recordingID := uuid.New(), uuid.New()
	positionA, positionB := uuid.New(), uuid.New()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,?)`, []any{releaseID, "Album"}},
		{`INSERT INTO local_artist(id,name) VALUES (?,?),(?,?),(?,?)`, []any{artistA, "Artist A", artistB, "Artist B", outsideArtist, "Outside"}},
		{`INSERT INTO release_artist(release_id,position,artist_id,name,name_join_phrase) VALUES (?,?,?,?,'') ,(?,?,?,?,'')`, []any{releaseID, 0, artistA, "Artist A", releaseID, 1, artistB, "Artist B"}},
		{`INSERT INTO local_track(id,title) VALUES (?,?)`, []any{trackID, "Song"}},
		{`INSERT INTO local_recording(id,title) VALUES (?,?)`, []any{recordingID, "Recording"}},
		{`INSERT INTO track_artist(track_id,position,artist_id,name,name_join_phrase) VALUES (?,?,?,?,'') ,(?,?,?,?,'')`, []any{trackID, 0, artistA, "Artist A", trackID, 1, artistB, "Artist B"}},
		{`INSERT INTO release_track(id,release_id,track_id,recording_id,medium,position) VALUES (?,?,?, ?,0,0),(?,?,?, ?,0,1)`, []any{positionA, releaseID, trackID, recordingID, positionB, releaseID, trackID, recordingID}},
	} {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("insert primary artist fixture: %v", err)
		}
	}

	read := func() *persistence.LibraryReleaseDetail {
		t.Helper()
		detail, err := repository.ReadReleaseDetail(ctx, releaseID)
		if err != nil {
			t.Fatalf("read release: %v", err)
		}
		return detail
	}
	selected := func(id uuid.UUID) *uuid.UUID { return &id }

	if _, err := repository.SetPrimaryArtist(ctx, releaseID, uuid.Nil, selected(outsideArtist), 1, nil); !errors.Is(err, persistence.ErrLibraryPrimaryArtistInvalid) {
		t.Fatalf("release artist outside current credits: %v", err)
	}
	revision, err := repository.SetPrimaryArtist(ctx, releaseID, uuid.Nil, selected(artistB), 1, nil)
	if err != nil || revision != 2 {
		t.Fatalf("select release primary artist: revision=%d err=%v", revision, err)
	}
	if revision, err = repository.SetPrimaryArtist(ctx, releaseID, uuid.Nil, selected(artistB), 2, nil); err != nil || revision != 2 {
		t.Fatalf("no-op selection changed revision: revision=%d err=%v", revision, err)
	}
	if _, err := repository.SetPrimaryArtist(ctx, releaseID, uuid.Nil, nil, 1, nil); !errors.Is(err, persistence.ErrLibraryPrimaryArtistConflict) {
		t.Fatalf("stale release revision: %v", err)
	}
	if revision, err = repository.SetPrimaryArtist(ctx, releaseID, uuid.Nil, nil, 2, nil); err != nil || revision != 3 {
		t.Fatalf("clear release selection: revision=%d err=%v", revision, err)
	}

	if _, err := repository.SetPrimaryArtist(ctx, releaseID, positionA, selected(outsideArtist), 1, ptrInt64(1)); !errors.Is(err, persistence.ErrLibraryPrimaryArtistInvalid) {
		t.Fatalf("position artist outside current credits: %v", err)
	}
	if revision, err = repository.SetPrimaryArtist(ctx, releaseID, positionA, selected(artistA), 1, ptrInt64(1)); err != nil || revision != 2 {
		t.Fatalf("select first position artist: revision=%d err=%v", revision, err)
	}
	if _, err := repository.SetPrimaryArtist(ctx, releaseID, positionA, selected(artistB), 1, ptrInt64(1)); !errors.Is(err, persistence.ErrLibraryPrimaryArtistConflict) {
		t.Fatalf("stale position revision: %v", err)
	}
	if _, err := repository.SetPrimaryArtist(ctx, releaseID, positionB, selected(artistB), 1, ptrInt64(2)); !errors.Is(err, persistence.ErrLibraryPrimaryArtistConflict) {
		t.Fatalf("stale shared local-track revision not rejected: %v", err)
	}
	if revision, err = repository.SetPrimaryArtist(ctx, releaseID, positionB, selected(artistB), 1, ptrInt64(1)); err != nil || revision != 2 {
		t.Fatalf("select second position independently: revision=%d err=%v", revision, err)
	}
	if _, err := repository.SetPrimaryArtist(ctx, releaseID, uuid.New(), selected(artistA), 1, ptrInt64(1)); !errors.Is(err, persistence.ErrLibraryPositionNotFound) {
		t.Fatalf("missing position error: %v", err)
	}
	if _, err := repository.SetPrimaryArtist(ctx, uuid.New(), uuid.Nil, nil, 1, nil); !errors.Is(err, persistence.ErrLibraryReleaseNotFound) {
		t.Fatalf("missing release error: %v", err)
	}

	detail := read()
	if detail.PrimaryArtistID != nil || detail.MetadataRevision != 3 {
		t.Fatalf("release primary artist after clear = %v, revision=%d", detail.PrimaryArtistID, detail.MetadataRevision)
	}
	positions := make(map[uuid.UUID]persistence.LibraryReleaseTrackDetail, len(detail.Tracks))
	for _, position := range detail.Tracks {
		positions[position.ID] = position
	}
	first, second := positions[positionA], positions[positionB]
	if first.PrimaryArtistID == nil || *first.PrimaryArtistID != artistA || first.MetadataRevision != 2 || second.PrimaryArtistID == nil || *second.PrimaryArtistID != artistB || second.MetadataRevision != 2 {
		t.Fatalf("position selections were not independent: first=%+v second=%+v", first, second)
	}
}

func ptrInt64(value int64) *int64 { return &value }
