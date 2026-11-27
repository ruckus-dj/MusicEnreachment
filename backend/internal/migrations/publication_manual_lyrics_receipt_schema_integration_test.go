//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestPublicationManualLyricsReceiptRollbackRefusesPopulatedTablePostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	if err := migrations.Apply(ctx, database); err != nil {
		t.Fatalf("apply baseline migrations: %v", err)
	}
	releaseID, trackID, positionID, artistID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_artist(id) VALUES (?)`, artistID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?, 'receipt rollback')`, releaseID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_artist(release_id,"position",artist_id) VALUES (?,0,?)`, releaseID, artistID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'receipt rollback')`, trackID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, positionID, releaseID, trackID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO publication_manual_lyrics_receipt
		(position_id,epoch,desired_revision,confirmed_generation,published_generation,desired_state,desired_sha256)
		VALUES (?,?,0,?,?,'value',repeat('a',64))`, positionID, uuid.New(), uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.RollbackLastGroup(ctx, database); err == nil {
		t.Fatal("rollback with a populated manual lyrics receipt table succeeded")
	}
	var retained bool
	if err := database.NewRaw(`SELECT to_regclass('public.publication_manual_lyrics_receipt') IS NOT NULL`).Scan(ctx, &retained); err != nil || !retained {
		t.Fatalf("receipt table retained after refused rollback = %t, %v", retained, err)
	}
	var receipts int
	if err := database.NewRaw(`SELECT count(*) FROM publication_manual_lyrics_receipt WHERE position_id=?`, positionID).Scan(ctx, &receipts); err != nil || receipts != 1 {
		t.Fatalf("manual lyrics receipt after refused rollback = %d, %v; want it retained", receipts, err)
	}
}
