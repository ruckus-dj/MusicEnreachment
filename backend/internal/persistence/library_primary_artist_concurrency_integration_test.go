//go:build integration

package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// Two releases share both local tracks, but list them in opposite coordinate
// order. Materialization must take the shared track locks in UUID order rather
// than each release's position order, or concurrent confirmations can deadlock.
func TestMaterializeMatchingLocalSharedTracksDoesNotDeadlockPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	releaseA, releaseB := uuid.New(), uuid.New()
	trackA := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	trackB := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	recordingA, recordingB := uuid.New(), uuid.New()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,?),(?,?)`, []any{releaseA, "A", releaseB, "B"}},
		{`INSERT INTO local_track(id,title) VALUES (?,?),(?,?)`, []any{trackA, "A", trackB, "B"}},
		{`INSERT INTO local_recording(id,title) VALUES (?,?),(?,?)`, []any{recordingA, "A", recordingB, "B"}},
		{`INSERT INTO release_track(release_id,track_id,recording_id,medium,position) VALUES (?,?,?,0,0),(?,?,?,0,1),(?,?,?,0,0),(?,?,?,0,1)`, []any{releaseA, trackA, recordingA, releaseA, trackB, recordingB, releaseB, trackB, recordingB, releaseB, trackA, recordingA}},
	} {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("insert concurrent matching fixture: %v", err)
		}
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, fixture := range []struct {
		release uuid.UUID
		tracks  []MatchingLocalTrack
	}{
		{releaseA, []MatchingLocalTrack{{LocalTrackID: &trackA, Medium: 1, Position: 1, Title: "A1"}, {LocalTrackID: &trackB, Medium: 1, Position: 2, Title: "A2"}}},
		{releaseB, []MatchingLocalTrack{{LocalTrackID: &trackB, Medium: 1, Position: 1, Title: "B1"}, {LocalTrackID: &trackA, Medium: 1, Position: 2, Title: "B2"}}},
	} {
		fixture := fixture
		go func() {
			<-start
			intent := MatchingLocalIntent{LocalReleaseID: &fixture.release, ReleaseTitle: "updated", Tracks: fixture.tracks}
			errs <- db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				_, err := materializeMatchingLocal(ctx, tx, MatchingLocalPreview{}, intent)
				return err
			})
		}()
	}
	close(start)
	for range 2 {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatalf("materialize shared tracks concurrently: %v", err)
			}
		case <-ctx.Done():
			t.Fatalf("concurrent materialization deadlocked or timed out: %v", ctx.Err())
		}
	}
}
