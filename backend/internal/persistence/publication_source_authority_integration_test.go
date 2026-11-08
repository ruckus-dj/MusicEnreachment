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
	db := testpostgres.OpenMigratedWithRiver(t)
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
	missing, err := readCurrentSourceAuthorityTx(ctx, db, positions[0], uuid.New(), uuid.New())
	if err != nil || missing.Status != "missing" || missing.Revision != uuid.Nil {
		t.Fatalf("missing current source authority = %+v, %v; want missing", missing, err)
	}
	manualRevision, automaticRevision := uuid.New(), uuid.New()
	for i, mode := range []string{"manual", "automatic"} {
		epoch, generation, location, variant := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		if _, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'Track.flac','{}'::jsonb,'{}'::jsonb,'{}'::jsonb,'{}'::jsonb,1,now())`, positions[i], epoch, generation); err != nil {
			t.Fatal(err)
		}
		digest := []byte("0123456789abcdef0123456789abcdef")
		digest[0] += byte(i)
		if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id) VALUES (?,0,?,now(),'sha256',?)`, variant, digest, uuid.New()); err != nil {
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
		current, err := readCurrentSourceAuthorityTx(ctx, db, positions[i], epoch, generation)
		if err != nil || current.Status != "present" || current.MediaVariantID != variant || string(current.CanonicalSourceSHA256) != string(digest) {
			t.Fatalf("current authority = %+v, %v; want present authority with canonical source digest", current, err)
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
		staleAuthority, err := readCurrentSourceAuthorityTx(ctx, db, positions[i], epoch, stale)
		if err != nil || staleAuthority.Status != "stale" || staleAuthority.PublicationGeneration != generation || staleAuthority.MediaVariantID != variant || string(staleAuthority.CanonicalSourceSHA256) != string(digest) {
			t.Fatalf("stale authority lost its raw identity or source digest = %+v, %v", staleAuthority, err)
		}
	}
}
