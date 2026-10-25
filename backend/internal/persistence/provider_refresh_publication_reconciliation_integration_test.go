//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestProviderRefreshRequestsBoundedPublicationReconciliationPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	cache := persistence.NewProviderCacheRepository(db)
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES('musicbrainz_config_identity','refresh-cfg') ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	seed := func(code, key string) (persistence.Provider, persistence.ProviderSource, uuid.UUID, uuid.UUID) {
		t.Helper()
		provider := persistence.Provider{ID: uuid.New(), Code: code}
		if err := cache.CreateProvider(ctx, &provider); err != nil {
			t.Fatal(err)
		}
		source := persistence.ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "catalog", Endpoint: "https://example.invalid/" + code, ConfigurationIdentity: "refresh-cfg"}
		if err := cache.CreateProviderSource(ctx, &source); err != nil {
			t.Fatal(err)
		}
		releaseID, localReleaseID := uuid.New(), uuid.New()
		localRecordingID, localTrackID, positionID := uuid.New(), uuid.New(), uuid.New()
		ticket, err := cache.BeginRefresh(ctx, source.ID, "seed:"+key, "refresh-cfg")
		if err != nil {
			t.Fatal(err)
		}
		graph := persistence.ProviderGraph{Releases: []persistence.ProviderRelease{{ID: releaseID, ProviderKey: key, Title: "Release"}}}
		if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{}`), Graph: graph}); err != nil {
			t.Fatal(err)
		}
		for _, query := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO local_release(id,title) VALUES (?,?)`, []any{localReleaseID, "Local release"}},
			{`INSERT INTO local_recording(id,title) VALUES (?,?)`, []any{localRecordingID, "Local recording"}},
			{`INSERT INTO local_track(id,title) VALUES (?,?)`, []any{localTrackID, "Local track"}},
			{`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?, ?, ?, 'manual')`, []any{provider.ID, releaseID, localReleaseID}},
			{`INSERT INTO release_track(id,release_id,position,track_id,recording_id) VALUES (?, ?,0,?,?)`, []any{positionID, localReleaseID, localTrackID, localRecordingID}},
			{`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) SELECT ?,output_epoch,?,'album/track.flac','{}','{}','{}','{}',1,now() FROM publication_namespace_state WHERE singleton=true`, []any{positionID, uuid.New()}},
		} {
			if _, err := db.NewRaw(query.sql, query.args...).Exec(ctx); err != nil {
				t.Fatalf("seed published binding: %v", err)
			}
		}
		return provider, source, releaseID, positionID
	}

	_, source, releaseID, position := seed("refresh-main", "main-release")
	_, _, _, unrelatedPosition := seed("refresh-other", "other-release")
	apply := func(ticket persistence.ProviderRefreshTicket, release persistence.ProviderRelease, fields string) error {
		return cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
			Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"accepted":true}`),
			Graph: persistence.ProviderGraph{
				Releases: []persistence.ProviderRelease{release},
				Entities: []persistence.ProviderSourceEntity{{Kind: "release", ProviderKey: release.ProviderKey, Fields: json.RawMessage(fields), RawSource: json.RawMessage(`{}`), Authority: 3, Complete: true}},
			},
		})
	}
	first, err := cache.BeginRefresh(ctx, source.ID, "release:main", "refresh-cfg")
	if err != nil {
		t.Fatal(err)
	}
	if err := apply(first, persistence.ProviderRelease{ID: releaseID, ProviderKey: "main-release", Title: "Changed"}, `{"title":"Changed"}`); err != nil {
		t.Fatalf("apply accepted refresh: %v", err)
	}
	var requested int64
	if err := db.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE position_id=?`, position).Scan(ctx, &requested); err != nil || requested != 1 {
		t.Fatalf("main refresh request generation=%d err=%v, want 1", requested, err)
	}
	var unrelatedCount int
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request WHERE position_id=?`, unrelatedPosition).Scan(ctx, &unrelatedCount); err != nil || unrelatedCount != 0 {
		t.Fatalf("unrelated source was requested: count=%d err=%v", unrelatedCount, err)
	}
	var scope string
	if err := db.NewRaw(`SELECT scope FROM publication_reconciliation_dirty_scope WHERE position_id=?`, position).Scan(ctx, &scope); err != nil || scope != "provider_refresh" {
		t.Fatalf("refresh dirty scope=%q err=%v", scope, err)
	}
	if err := apply(first, persistence.ProviderRelease{ID: releaseID, ProviderKey: "main-release", Title: "Changed"}, `{"title":"Changed"}`); err != nil {
		t.Fatalf("replay accepted refresh: %v", err)
	}
	assertGeneration := func(want int64, label string) {
		t.Helper()
		if err := db.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE position_id=?`, position).Scan(ctx, &requested); err != nil || requested != want {
			t.Fatalf("%s changed generation=%d err=%v, want %d", label, requested, err, want)
		}
	}
	assertGeneration(1, "same-ticket replay")

	lowerAuthority, err := cache.BeginRefresh(ctx, source.ID, "release:lower-authority", "refresh-cfg")
	if err != nil {
		t.Fatal(err)
	}
	recordingID := uuid.New()
	foreignTrack := persistence.ProviderReleaseTrack{ID: uuid.New(), ReleaseID: releaseID, RecordingID: recordingID, Medium: 0, Position: 0, DisplayedNumber: "1"}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: lowerAuthority, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"accepted":true}`),
		Graph: persistence.ProviderGraph{
			Releases:   []persistence.ProviderRelease{{ID: releaseID, ProviderKey: "main-release", Title: "Ignored"}},
			Recordings: []persistence.ProviderRecording{{ID: recordingID, ProviderKey: "lower-recording", Title: "Recording"}},
			Tracks:     []persistence.ProviderReleaseTrack{foreignTrack},
			Entities:   []persistence.ProviderSourceEntity{{Kind: "release", ProviderKey: "main-release", Fields: json.RawMessage(`{"title":"Ignored"}`), RawSource: json.RawMessage(`{}`), Authority: 2, Complete: true}},
		},
	}); err != nil {
		t.Fatalf("apply lower-authority collection: %v", err)
	}
	assertGeneration(1, "lower-authority/rejected source tracks")

	stale, err := cache.BeginRefresh(ctx, source.ID, "release:stale", "refresh-cfg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.BeginRefresh(ctx, source.ID, "release:stale", "refresh-cfg"); err != nil {
		t.Fatal(err)
	}
	if err := apply(stale, persistence.ProviderRelease{ID: releaseID, ProviderKey: "main-release", Title: "Stale"}, `{"title":"Stale"}`); err == nil {
		t.Fatal("stale refresh was accepted")
	}
	if err := db.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE position_id=?`, position).Scan(ctx, &requested); err != nil || requested != 1 {
		t.Fatalf("stale refresh changed generation=%d err=%v", requested, err)
	}
	invalid, err := cache.BeginRefresh(ctx, source.ID, "release:invalid", "refresh-cfg")
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: invalid, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`not-json`)}); err == nil {
		t.Fatal("invalid refresh payload was accepted")
	}
	cacheOnly, err := cache.BeginRefresh(ctx, source.ID, "release:cache-only", "refresh-cfg")
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: cacheOnly, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("apply cache-only refresh: %v", err)
	}
	if err := db.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE position_id=?`, position).Scan(ctx, &requested); err != nil || requested != 1 {
		t.Fatalf("invalid/cache-only refresh changed generation=%d err=%v", requested, err)
	}

	before, err := cache.GetCachedResponse(ctx, source.ID, first.CacheKey)
	if err != nil || before == nil {
		t.Fatalf("read accepted cache response: value=%+v err=%v", before, err)
	}
	failTicket, err := cache.BeginRefresh(ctx, source.ID, "release:rollback", "refresh-cfg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`CREATE FUNCTION fail_provider_refresh_reconciliation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected reconciliation failure'; END $$`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`CREATE TRIGGER fail_provider_refresh_reconciliation BEFORE INSERT OR UPDATE ON publication_reconciliation_dirty_scope FOR EACH ROW EXECUTE FUNCTION fail_provider_refresh_reconciliation()`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := apply(failTicket, persistence.ProviderRelease{ID: releaseID, ProviderKey: "main-release", Title: "Must roll back"}, `{"title":"Must roll back"}`); err == nil {
		t.Fatal("injected reconciliation failure did not fail refresh")
	}
	stored, err := cache.GetCachedResponse(ctx, source.ID, failTicket.CacheKey)
	if err != nil || stored == nil || stored.Payload != nil {
		t.Fatalf("failed refresh cache was not rolled back: value=%+v err=%v", stored, err)
	}
	var sourceTitle string
	if err := db.NewRaw(`SELECT fields->>'title' FROM provider_source_entity WHERE provider_source_id=? AND provider_key='main-release'`, source.ID).Scan(ctx, &sourceTitle); err != nil || sourceTitle != "Changed" {
		t.Fatalf("failed refresh projection was not rolled back: title=%q err=%v", sourceTitle, err)
	}
}
