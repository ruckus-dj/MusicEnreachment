//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestAcoustIDCachePublicationFenceWaitsForAcceptedWriterPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	var source ProviderSource
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		source, err = ensureAcoustIDProviderSource(ctx, tx)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	const cacheKey = "integration-acoustid-cache-lock"
	writerReady := make(chan error, 1)
	writerRelease := make(chan struct{})
	var releaseWriterOnce sync.Once
	releaseWriter := func() { releaseWriterOnce.Do(func() { close(writerRelease) }) }
	defer releaseWriter()
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
			if err := lockAcoustIDCacheKeys(ctx, tx, source.ID, []string{cacheKey}, false); err != nil {
				writerReady <- err
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO provider_response_cache(provider_source_id,provider_id,cache_key,payload,fetched_at,generation,fetch_order)
				VALUES(?,?,?,?::jsonb,now(),1,nextval('provider_fetch_order_seq'))`, source.ID, source.ProviderID, cacheKey, `{"accepted":true}`).Exec(ctx); err != nil {
				writerReady <- err
				return err
			}
			writerReady <- nil
			<-writerRelease
			return nil
		})
	}()
	if err := <-writerReady; err != nil {
		t.Fatalf("reserve and accept response: %v", err)
	}

	type publicationResult struct {
		pid     int
		payload []byte
		err     error
	}
	publicationDone := make(chan publicationResult, 1)
	publicationStarted := make(chan int, 1)
	go func() {
		var result publicationResult
		result.err = db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
			if err := tx.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &result.pid); err != nil {
				return err
			}
			publicationStarted <- result.pid
			if err := lockAcoustIDCacheKeys(ctx, tx, source.ID, []string{cacheKey}, false); err != nil {
				return err
			}
			return tx.NewRaw(`SELECT payload FROM provider_response_cache WHERE provider_source_id=? AND cache_key=? FOR SHARE`, source.ID, cacheKey).Scan(ctx, &result.payload)
		})
		publicationDone <- result
	}()
	pid := <-publicationStarted
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=? AND wait_event_type='Lock' AND wait_event='advisory')`, pid).Scan(ctx, &waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("publication did not block on the per-key AcoustID gate")
		}
		time.Sleep(10 * time.Millisecond)
	}
	releaseWriter()
	if err := <-writerDone; err != nil {
		t.Fatalf("commit accepted response: %v", err)
	}
	result := <-publicationDone
	if result.err != nil {
		t.Fatalf("complete publication cache-miss fence: %v", result.err)
	}
	var accepted struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(result.payload, &accepted); err != nil || !accepted.Accepted {
		t.Fatalf("publication observed payload %s after accepted writer committed", result.payload)
	}
}
