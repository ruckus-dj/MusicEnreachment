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

func TestSetPendingReleaseCoverFromMusicBrainzFencesBindingPostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	releaseID := seedReleaseCoverRelease(t, db, ctx)
	providerID, providerReleaseID, otherProviderReleaseID := uuid.New(), uuid.New(), uuid.New()
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO provider(id,code) VALUES (?, 'musicbrainz')`, providerID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?,?,'Release'), (?,?,?,'Other')`,
			providerReleaseID, providerID, "musicbrainz-mbid", otherProviderReleaseID, providerID, "other-mbid"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual')`,
			providerID, providerReleaseID, releaseID)
		return err
	}); err != nil {
		t.Fatalf("seed MusicBrainz release link: %v", err)
	}

	repository := NewLibraryRepository(db)
	data := releaseCoverPNG(t)
	pending, err := repository.SetPendingReleaseCoverFromMusicBrainz(ctx, releaseID, 1, "musicbrainz-mbid", uuid.New(), data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if pending.Revision != 2 || pending.Mode != "manual" || pending.PendingGeneration == nil {
		t.Fatalf("bound pending cover = %+v", pending)
	}
	if _, err := repository.SetPendingReleaseCoverFromMusicBrainz(ctx, releaseID, 2, "other-mbid", uuid.New(), data, "image/png"); !errors.Is(err, ErrReleaseCoverConflict) {
		t.Fatalf("mismatched MBID error = %v, want conflict", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE provider_local_release SET provider_release_id=? WHERE local_release_id=?`, otherProviderReleaseID, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SetPendingReleaseCoverFromMusicBrainz(ctx, releaseID, 2, "musicbrainz-mbid", uuid.New(), data, "image/png"); !errors.Is(err, ErrReleaseCoverConflict) {
		t.Fatalf("relinked MBID error = %v, want conflict", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM provider_local_release WHERE local_release_id=?`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SetPendingReleaseCoverFromMusicBrainz(ctx, releaseID, 2, "other-mbid", uuid.New(), data, "image/png"); !errors.Is(err, ErrReleaseCoverConflict) {
		t.Fatalf("unlinked MBID error = %v, want conflict", err)
	}
	unchanged, err := repository.ReadReleaseCover(ctx, releaseID)
	if err != nil || unchanged.Revision != 2 || unchanged.PendingGeneration == nil || *unchanged.PendingGeneration != *pending.PendingGeneration {
		t.Fatalf("binding conflicts changed pending authority = %+v, %v", unchanged, err)
	}
}
