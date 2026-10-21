//go:build integration

package persistence

import (
	"context"
	"errors"
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

func TestMaterializeMatchingLocalProtectsAssignedCleanupOwnedSourcesPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, ownedVariantID := publicationSourceCleanupReadyFixture(t)
	db := f.db
	admitted, err := repo.AdmitPublicationSourceCleanup(ctx, PublicationSourceCleanupAdmission{
		PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration,
	}, &publicationSourceCleanupTestInserter{})
	if err != nil || admitted.OperationID == uuid.Nil {
		t.Fatalf("admit cleanup owner = %+v, %v", admitted, err)
	}
	if _, err := db.NewRaw(`UPDATE operation SET state='failed',safe_error='fixture cleanup failure',finished_at=now() WHERE id=?`, admitted.OperationID).Exec(ctx); err != nil {
		t.Fatalf("make cleanup owner terminal: %v", err)
	}
	memberID, omittedMemberID := uuid.New(), uuid.New()
	unownedVariantID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO media_variant(id,size_bytes) VALUES (?,1)`, unownedVariantID).Exec(ctx); err != nil {
		t.Fatalf("create unowned media variant for baseline: %v", err)
	}
	preview := MatchingLocalPreview{Sources: []MatchingLocalSource{
		{FileMemberID: memberID, MediaIDs: []uuid.UUID{unownedVariantID}},
		{FileMemberID: omittedMemberID, MediaIDs: []uuid.UUID{ownedVariantID}},
	}}
	intent := MatchingLocalIntent{ReleaseTitle: "Eligible", Tracks: []MatchingLocalTrack{{
		Medium: 1, Position: 1, DisplayedNumber: "1", Title: "Track", FileMemberIDs: []uuid.UUID{memberID},
	}}}
	materialize := func(preview MatchingLocalPreview, intent MatchingLocalIntent) error {
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
				return err
			}
			_, err := materializeMatchingLocal(ctx, tx, preview, intent)
			return err
		})
	}
	if err := materialize(preview, intent); err != nil {
		t.Fatalf("eligible local materialization baseline: %v", err)
	}
	var releaseID uuid.UUID
	if err := db.NewRaw(`SELECT id FROM local_release WHERE title='Eligible'`).Scan(ctx, &releaseID); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := db.NewRaw(`SELECT json_build_object(
		'releases',(SELECT count(*) FROM local_release),
		'recordings',(SELECT count(*) FROM local_recording),
		'tracks',(SELECT count(*) FROM local_track),
		'positions',(SELECT count(*) FROM release_track),
		'credits',(SELECT count(*) FROM release_artist)+(SELECT count(*) FROM track_artist),
		'links',(SELECT count(*) FROM track_source))::text`).Scan(ctx, &before); err != nil {
		t.Fatal(err)
	}
	blocked := intent
	blocked.Tracks = append([]MatchingLocalTrack(nil), intent.Tracks...)
	blocked.LocalReleaseID = &releaseID
	blocked.ReleaseTitle = "Must not change"
	blocked.Tracks[0].FileMemberIDs = []uuid.UUID{omittedMemberID}
	blocked.Tracks[0].Position = 2
	if err := materialize(preview, blocked); !errors.Is(err, ErrSourceCleanupOwned) {
		t.Fatalf("assigned owned source error = %v, want ErrSourceCleanupOwned", err)
	}
	var after string
	if err := db.NewRaw(`SELECT json_build_object(
		'releases',(SELECT count(*) FROM local_release),
		'recordings',(SELECT count(*) FROM local_recording),
		'tracks',(SELECT count(*) FROM local_track),
		'positions',(SELECT count(*) FROM release_track),
		'credits',(SELECT count(*) FROM release_artist)+(SELECT count(*) FROM track_artist),
		'links',(SELECT count(*) FROM track_source))::text`).Scan(ctx, &after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("owned-source rejection changed local tables: before=%s after=%s", before, after)
	}
	var title string
	if err := db.NewRaw(`SELECT title FROM local_release WHERE id=?`, releaseID).Scan(ctx, &title); err != nil || title != "Eligible" {
		t.Fatalf("release title after rejected materialization = %q, %v", title, err)
	}
	// Ownership of a source shown in the preview but omitted from assignments is
	// irrelevant: only assigned FileMemberIDs participate in materialization.
	if err := materialize(preview, intent); err != nil {
		t.Fatalf("omitted owned preview source blocked materialization: %v", err)
	}
	if _, err := db.NewRaw(`DELETE FROM source_cleanup_owner WHERE operation_id=?`, admitted.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := materialize(preview, blocked); err != nil {
		t.Fatalf("materialization after owner release: %v", err)
	}
}
