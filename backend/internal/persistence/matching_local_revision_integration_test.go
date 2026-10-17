//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestMaterializeMatchingLocalAdvancesExistingMetadataRevisionsPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	releaseID, trackID, recordingID := uuid.New(), uuid.New(), uuid.New()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,?)`, []any{releaseID, "Old album"}},
		{`INSERT INTO local_track(id,title) VALUES (?,?)`, []any{trackID, "Old song"}},
		{`INSERT INTO local_recording(id,title) VALUES (?,?)`, []any{recordingID, "Song recording"}},
		{`INSERT INTO release_track(release_id,track_id,recording_id,medium,position) VALUES (?,?,?,0,0)`, []any{releaseID, trackID, recordingID}},
	} {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("insert local matching fixture: %v", err)
		}
	}
	intent := MatchingLocalIntent{
		LocalReleaseID: &releaseID,
		ReleaseTitle:   "New album",
		ReleaseArtists: []MatchingLocalArtist{{Name: "Album artist"}},
		Tracks: []MatchingLocalTrack{{
			LocalTrackID: &trackID, Medium: 1, Position: 1, DisplayedNumber: "A1",
			Title: "New song", Artists: []MatchingLocalArtist{{Name: "Track artist"}},
		}},
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := materializeMatchingLocal(ctx, tx, MatchingLocalPreview{}, intent)
		return err
	}); err != nil {
		t.Fatalf("materialize local matching: %v", err)
	}
	var releaseRevision, trackRevision int64
	if err := db.NewRaw(`SELECT metadata_revision FROM local_release WHERE id=?`, releaseID).Scan(ctx, &releaseRevision); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT metadata_revision FROM local_track WHERE id=?`, trackID).Scan(ctx, &trackRevision); err != nil {
		t.Fatal(err)
	}
	if releaseRevision != 2 || trackRevision != 2 {
		t.Fatalf("matching revisions release=%d track=%d, want 2/2", releaseRevision, trackRevision)
	}
	var releaseTitle, trackTitle string
	if err := db.NewRaw(`SELECT title FROM local_release WHERE id=?`, releaseID).Scan(ctx, &releaseTitle); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT title FROM local_track WHERE id=?`, trackID).Scan(ctx, &trackTitle); err != nil {
		t.Fatal(err)
	}
	if releaseTitle != "New album" || trackTitle != "New song" {
		t.Fatalf("matching titles release=%q track=%q", releaseTitle, trackTitle)
	}
}
