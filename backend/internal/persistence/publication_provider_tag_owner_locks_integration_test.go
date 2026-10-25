//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestPublicationProviderTagOwnerRowLocksDoNotBlockMetadataWritersPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	const identity = "provider-tag-owner-locks"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES('musicbrainz_config_identity',?) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	provider := Provider{ID: uuid.New(), Code: "musicbrainz"}
	cache := NewProviderCacheRepository(db)
	if err := cache.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	source := ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: identity, Endpoint: "https://musicbrainz.example/ws/2", ConfigurationIdentity: identity}
	if err := cache.CreateProviderSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	const releaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	graph := publicationProviderTagAuthorityGraph()
	ticket, err := cache.BeginRefresh(ctx, source.ID, "release-search:0:1:owner-locks", identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"releases":[{"id":"` + releaseKey + `"}]}`), Graph: graph}); err != nil {
		t.Fatal(err)
	}
	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title,metadata) VALUES(?, 'Release','{}'::jsonb)`, []any{releaseID}},
		{`INSERT INTO local_track(id,title,metadata) VALUES(?, 'Track','{}'::jsonb)`, []any{trackID}},
		{`INSERT INTO release_track(id,release_id,medium,position,track_id,displayed_number) VALUES(?,?,0,0,?,'1')`, []any{positionID, releaseID, trackID}},
	} {
		if _, err := db.NewRaw(seed.query, seed.args...).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var providerReleaseID, providerTrackID uuid.UUID
	if err := db.NewRaw(`SELECT id FROM provider_release WHERE provider_id=? AND provider_key=?`, provider.ID, releaseKey).Scan(ctx, &providerReleaseID); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT id FROM provider_release_track WHERE id=?`, graph.Tracks[0].ID).Scan(ctx, &providerTrackID); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		statement string
		args      []any
	}{
		{`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES(?,?,?,'manual')`, []any{provider.ID, providerReleaseID, releaseID}},
		{`INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method) VALUES(?,?,?,'manual')`, []any{provider.ID, providerTrackID, trackID}},
	} {
		if _, err := db.NewRaw(query.statement, query.args...).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	var initialRevision int64
	if err := db.NewRaw(`SELECT metadata_revision FROM local_track WHERE id=?`, trackID).Scan(ctx, &initialRevision); err != nil {
		t.Fatal(err)
	}
	blockerHeld := make(chan struct{})
	releaseBlocker := make(chan struct{})
	blocker := startCoordinationTransaction(t, db, func(ctx context.Context, tx bun.Tx) error {
		var lockedID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM local_release WHERE id=? FOR UPDATE`, releaseID).Scan(ctx, &lockedID); err != nil {
			return err
		}
		close(blockerHeld)
		select {
		case <-releaseBlocker:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	<-blocker.pid
	<-blockerHeld
	var providerSnapshot *PublicationProviderTagContext
	providerRead := startCoordinationTransaction(t, db, func(ctx context.Context, tx bun.Tx) error {
		return readPublicationProviderTagContextForPositionTx(ctx, tx, releaseID, positionID, source.Endpoint, identity, true, &providerSnapshot)
	})
	assertWaitingForPostgresLock(t, db, <-providerRead.pid)

	// A waiting context reader must not retain a table-level SHARE lock that
	// prevents an unrelated selected owner metadata update from proceeding.
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.NewRaw(`UPDATE local_track SET metadata='{"updated":true}'::jsonb, metadata_revision=metadata_revision+1 WHERE id=?`, trackID).Exec(ctx)
			return err
		})
	}()
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatalf("metadata writer blocked by context lock: %v", err)
		}
	case <-time.After(coordinationWaitTimeout):
		t.Fatal("metadata writer did not complete while provider context waited on the release owner")
	}
	close(releaseBlocker)
	assertTransactionSucceeded(t, blocker.done)
	assertTransactionSucceeded(t, providerRead.done)
	if providerSnapshot == nil || providerSnapshot.TrackOwner.Revision != initialRevision+1 {
		t.Fatalf("provider context track revision = %v, want %d", providerSnapshot, initialRevision+1)
	}
	var revision int64
	if err := db.NewRaw(`SELECT metadata_revision FROM local_track WHERE id=?`, trackID).Scan(ctx, &revision); err != nil {
		t.Fatal(err)
	}
	if revision != initialRevision+1 {
		t.Fatalf("metadata revision = %d, want %d", revision, initialRevision+1)
	}
}
