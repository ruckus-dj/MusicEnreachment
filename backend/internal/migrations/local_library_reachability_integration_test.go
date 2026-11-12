//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestLocalLibraryReachabilityGuardsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	artist, release, noTracksRelease := uuid.New(), uuid.New(), uuid.New()
	track, sharedRecording, recordingOnly := uuid.New(), uuid.New(), uuid.New()
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO local_artist(id) VALUES (?)`, []any{artist}},
			{`INSERT INTO local_release(id,title) VALUES (?,'tracked')`, []any{release}},
			{`INSERT INTO local_release(id,title) VALUES (?,'no tracks')`, []any{noTracksRelease}},
			{`INSERT INTO release_artist(release_id,"position",artist_id) VALUES (?,0,?)`, []any{release, artist}},
			{`INSERT INTO release_artist(release_id,"position",artist_id) VALUES (?,0,?)`, []any{noTracksRelease, artist}},
			{`INSERT INTO local_track(id) VALUES (?)`, []any{track}},
			{`INSERT INTO local_recording(id) VALUES (?),(?)`, []any{sharedRecording, recordingOnly}},
			{`INSERT INTO release_track(release_id,"position",track_id,recording_id) VALUES (?,0,?,?),(?,1,?,?),(?,2,?,?)`, []any{release, track, sharedRecording, release, track, sharedRecording, release, track, recordingOnly}},
		} {
			if _, err := tx.NewRaw(q.sql, q.args...).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("parent-first graph commit (including zero-track release and shared recording): %v", err)
	}

	assertRejected := func(label, diagnostic, sql string, args ...any) {
		t.Helper()
		if _, err := db.NewRaw(sql, args...).Exec(ctx); err == nil || !strings.Contains(err.Error(), diagnostic) {
			t.Fatalf("%s: expected %q, got %v", label, diagnostic, err)
		}
	}
	assertRejected("orphan release", "local release must have at least one release artist", `INSERT INTO local_release(id) VALUES (?)`, uuid.New())
	assertRejected("orphan recording", "local recording must have at least one release track", `INSERT INTO local_recording(id) VALUES (?)`, uuid.New())
	assertRejected("remove final release artist", "local release must have at least one release artist", `DELETE FROM release_artist WHERE release_id=?`, release)
	assertRejected("move only artist edge", "local release must have at least one release artist", `UPDATE release_artist SET release_id=?, position=1 WHERE release_id=?`, noTracksRelease, release)
	assertRejected("remove final recording edge", "local recording must have at least one release track", `DELETE FROM release_track WHERE recording_id=?`, recordingOnly)
	if _, err := db.NewRaw(`DELETE FROM release_track WHERE recording_id=? AND position=0`, sharedRecording).Exec(ctx); err != nil {
		t.Fatalf("removing one of multiple recording edges: %v", err)
	}
	assertRejected("truncate artist links", "cannot truncate release_artist", `TRUNCATE release_artist`)

	// Separate sessions race to remove distinct final recording edges. The
	// deferred parent-row serialization must ensure at least one commit fails.
	racing := uuid.New()
	raceReleases := []uuid.UUID{uuid.New(), uuid.New()}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO local_recording(id) VALUES (?)`, racing).Exec(ctx); err != nil {
			return err
		}
		for _, r := range raceReleases {
			trk := uuid.New()
			if _, err := tx.NewRaw(`INSERT INTO local_release(id) VALUES (?)`, r).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO release_artist(release_id,"position",artist_id) VALUES (?,0,?)`, r, artist).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO local_track(id) VALUES (?)`, trk).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO release_track(release_id,"position",track_id,recording_id) VALUES (?,0,?,?)`, r, trk, racing).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	txs := make([]*sql.Tx, 2)
	for i := range txs {
		conn, err := db.DB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := conn.Close(); err != nil {
				t.Errorf("close race connection: %v", err)
			}
		})
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		txs[i] = tx
		t.Cleanup(func() {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				t.Errorf("rollback race transaction: %v", err)
			}
		})
		if _, err := txs[i].ExecContext(ctx, `DELETE FROM release_track WHERE recording_id=$1 AND release_id=$2`, racing, raceReleases[i]); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, tx := range txs {
		go func(tx *sql.Tx) { <-start; results <- tx.Commit() }(tx)
	}
	close(start)
	first, second := <-results, <-results
	if (first == nil) == (second == nil) {
		t.Fatalf("concurrent final-edge deletions: commits returned %v and %v; expected exactly one success", first, second)
	}
}
