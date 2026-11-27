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

func TestReadReleaseMusicBrainzReleaseIDWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	linkedReleaseID, otherProviderReleaseID := uuid.New(), uuid.New()
	artistID := uuid.New()
	providerID, providerReleaseID := uuid.New(), uuid.New()
	otherProviderID, otherProviderReleaseIDRecord, otherSecondReleaseID := uuid.New(), uuid.New(), uuid.New()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?, 'Linked'), (?, 'Other provider')`, []any{linkedReleaseID, otherProviderReleaseID}},
		{`INSERT INTO local_artist(id,name) VALUES (?,'Identity Artist')`, []any{artistID}},
		{`INSERT INTO release_artist(release_id,position,artist_id,name,name_join_phrase) VALUES (?,0,?,'Identity Artist',''), (?,0,?,'Identity Artist','')`,
			[]any{linkedReleaseID, artistID, otherProviderReleaseID, artistID}},
		{`INSERT INTO provider(id,code) VALUES (?, 'musicbrainz'), (?, 'other')`, []any{providerID, otherProviderID}},
		{`INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?,?,'Release'), (?,?,?,'Other'), (?,?,?,'Other second')`,
			[]any{providerReleaseID, providerID, "musicbrainz-mbid", otherProviderReleaseIDRecord, otherProviderID, "other-key", otherSecondReleaseID, otherProviderID, "other-key-2"}},
		{`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual'), (?,?,?,'manual')`,
			[]any{otherProviderID, otherProviderReleaseIDRecord, linkedReleaseID, providerID, providerReleaseID, linkedReleaseID}},
		{`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual')`,
			[]any{otherProviderID, otherSecondReleaseID, otherProviderReleaseID}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("insert release identity fixture: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit release identity fixture: %v", err)
	}

	repository := persistence.NewLibraryRepository(db)
	mbid, linked, err := repository.ReadReleaseMusicBrainzReleaseID(ctx, linkedReleaseID)
	if err != nil || !linked || mbid != "musicbrainz-mbid" {
		t.Fatalf("linked release identity = %q %v %v", mbid, linked, err)
	}
	mbid, linked, err = repository.ReadReleaseMusicBrainzReleaseID(ctx, otherProviderReleaseID)
	if err != nil || linked || mbid != "" {
		t.Fatalf("non-MusicBrainz identity = %q %v %v", mbid, linked, err)
	}
	if _, _, err := repository.ReadReleaseMusicBrainzReleaseID(ctx, uuid.New()); !errors.Is(err, persistence.ErrLibraryReleaseNotFound) {
		t.Fatalf("missing release error = %v", err)
	}
}
