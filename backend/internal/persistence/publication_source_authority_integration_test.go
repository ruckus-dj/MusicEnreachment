//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestPositionSourceAuthorityIsIndependentAndGenerationBoundPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	repo := NewSetupManagerRepository(db)
	releaseID, trackID := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'authority fixture')`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'authority fixture')`, trackID); err != nil {
		t.Fatal(err)
	}
	positions := []uuid.UUID{uuid.New(), uuid.New()}
	for i, position := range positions {
		if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,?)`, position, releaseID, trackID, i); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := repo.ReadPositionSourceAuthority(ctx, positions[0]); err != nil || got != nil {
		t.Fatalf("authority for legacy publication = %+v, %v; want unknown", got, err)
	}
	manualRevision, automaticRevision := uuid.New(), uuid.New()
	for i, mode := range []string{"manual", "automatic"} {
		epoch, generation, location, variant := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		if _, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'Track.flac','{}'::jsonb,'{}'::jsonb,'{}'::jsonb,'{}'::jsonb,1,now())`, positions[i], epoch, generation); err != nil {
			t.Fatal(err)
		}
		authority := PositionSourceAuthority{PositionID: positions[i], Mode: mode, Revision: []uuid.UUID{manualRevision, automaticRevision}[i], PublicationEpoch: epoch, PublicationGeneration: generation, SourceLocationID: location, MediaVariantID: variant}
		var created bool
		if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			var err error
			created, err = compareAndSetPositionSourceAuthorityTx(ctx, tx, nil, authority)
			return err
		}); err != nil || !created {
			t.Fatal(err)
		}
		got, err := repo.ReadPositionSourceAuthority(ctx, positions[i])
		if err != nil || got == nil || got.Mode != mode || got.Revision != []uuid.UUID{manualRevision, automaticRevision}[i] || got.PublicationGeneration != generation {
			t.Fatalf("position %s authority = %+v, %v", positions[i], got, err)
		}
		stale := uuid.New()
		replacement := authority
		replacement.PublicationGeneration = stale
		var changed bool
		if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			var err error
			changed, err = compareAndSetPositionSourceAuthorityTx(ctx, tx, &authority.Revision, replacement)
			return err
		}); err == nil || changed {
			t.Fatal("authority CAS accepted a generation that is not the current publication")
		}
		next := authority
		next.Revision = uuid.New()
		var updated bool
		if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			var err error
			updated, err = compareAndSetPositionSourceAuthorityTx(ctx, tx, &stale, next)
			return err
		}); err != nil || updated {
			t.Fatalf("stale revision CAS updated=%t err=%v, want rejection", updated, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE track_publication SET generation=? WHERE position_id=?`, stale, positions[i]); err != nil {
			t.Fatal(err)
		}
		if got, err := repo.ReadPositionSourceAuthority(ctx, positions[i]); err != nil || got != nil {
			t.Fatalf("authority bound to replaced publication = %+v, %v; want unknown", got, err)
		}
	}
}
