//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestReadLibraryRecordingSearchCacheConfigurationFenceAfterLockWait(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	identity := "recording-search-config-before"
	providerID, sourceID, recordingKey := uuid.New(), uuid.New(), uuid.New().String()
	recordingID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:recording:"+recordingKey))
	cacheKey := "recording-search:0:10:needle"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES ('musicbrainz_config_identity',?)`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider(id,code) VALUES (?, 'musicbrainz')`, providerID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_source(id,provider_id,namespace,endpoint,configuration_identity) VALUES (?,?, 'recording-search-test','https://musicbrainz.org/ws/2',?)`, sourceID, providerID, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_recording(id,provider_id,provider_key,title) VALUES (?,?,?,'Accepted title')`, recordingID, providerID, recordingKey).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_source_entity(provider_source_id,provider_id,entity_kind,provider_key,name,fields,field_evidence,raw_source,authority,complete,fetch_order) VALUES (?,?, 'recording',?,'Accepted title','{"title":"Accepted title"}'::jsonb,'{"title":{}}'::jsonb,'{}'::jsonb,3,true,1)`, sourceID, providerID, recordingKey).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(fmt.Sprintf(`{"count":1,"offset":0,"recordings":[{"id":%q}]}`, recordingKey))
	if _, err := db.NewRaw(`INSERT INTO provider_response_cache(provider_source_id,provider_id,cache_key,payload,fetched_at) VALUES (?,?,?,?::jsonb,now())`, sourceID, providerID, cacheKey, string(payload)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewLibraryRepository(db)
	baseline, err := repo.ReadLibraryRecordingSearchCache(ctx, sourceID, identity, cacheKey, 0, 10)
	if err != nil || len(baseline.Items) != 1 || baseline.Items[0].Title != "Accepted title" {
		t.Fatalf("valid accepted cache baseline: result=%+v err=%v", baseline, err)
	}

	// The updater owns the exclusive advisory gate first. Reader-start and
	// observed-blocked channels establish ordering without relying on scheduler delay.
	updater, err := db.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := updater.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	defer updater.Close()
	updateTx, err := updater.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer updateTx.Rollback()
	// Ensure the transaction holds the exclusive lock after begin.
	if _, err := updateTx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", "musicbrainz-config"); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		var ignored *LibraryRecordingSearchCacheResult
		ignored, readErr := NewLibraryRepository(db).ReadLibraryRecordingSearchCache(ctx, sourceID, identity, cacheKey, 0, 10)
		if ignored != nil {
			done <- fmt.Errorf("stale cache was served: %+v", ignored)
			return
		}
		done <- readErr
	}()
	<-started
	// PostgreSQL's lock queue is the handshake: wait until the reader backend is
	// demonstrably blocked by the updater before switching and committing config.
	var updaterPID int
	if err := updater.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&updaterPID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var blocked bool
		if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity a WHERE a.datname=current_database() AND a.wait_event_type='Lock' AND ?=ANY(pg_blocking_pids(a.pid)))`, updaterPID).Scan(ctx, &blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reader never entered advisory-lock wait")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := updateTx.ExecContext(ctx, `UPDATE app_setting SET setting_value='recording-search-config-after' WHERE setting_name='musicbrainz_config_identity'`); err != nil {
		t.Fatal(err)
	}
	if err := updateTx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrLibraryRecordingSearchConfigurationChanged) {
			t.Fatalf("reader error = %v, want configuration-changed conflict", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("blocked reader did not finish after configuration commit")
	}
}
