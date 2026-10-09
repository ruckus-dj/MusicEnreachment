//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// This exercises the durable path end to end: HTTP API intent admission via
// StartProviderOperation, River persistence, and the production worker's fetch.
func TestProviderFetchWorkerRiverDispatchPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	repo := persistence.NewSetupManagerRepository(db)
	registry := settings.New(persistence.NewSettingsRepository(db), nil)
	var requests atomic.Int32
	status := atomic.Int32{}
	status.Store(http.StatusOK)
	payload := `{"count":0,"offset":0,"created":"2026-01-01T00:00:00Z","releases":[]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body := payload
		path := strings.TrimSuffix(r.URL.Path, "/")
		switch {
		case path == "/ws/2/release" && r.URL.Query().Get("query") == "artist:test":
		case path == "/ws/2/recording" && r.URL.Query().Get("query") == "recording:test":
			body = `{"count":0,"offset":0,"created":"2026-01-01T00:00:00Z","recordings":[],"unknown_top_level":"preserve-recording-search"}`
		case r.URL.Path == "/ws/2/release/11111111-1111-4111-8111-111111111111":
			body = `{"id":"11111111-1111-4111-8111-111111111111","title":"Fixture Release","score":100,"unknown_top_level":"preserve-release-lookup"}`
		case r.URL.Path == "/ws/2/recording/22222222-2222-4222-8222-222222222222":
			body = `{"id":"22222222-2222-4222-8222-222222222222","title":"Fixture Recording","score":100,"unknown_top_level":"preserve-recording-lookup"}`
		default:
			t.Errorf("unexpected provider request: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", server.URL+"/ws/2"); err != nil {
		t.Fatal(err)
	}
	factory := service.NewProviderFactory(registry, nil, nil, &http.Client{Timeout: 3 * time.Second}, nil)
	cache := persistence.NewProviderCacheRepository(db)
	operations := service.NewOperations(repo)
	fetch := service.NewProviderFetchService(registry, factory, cache).WithDurableOperations(repo, nil)
	worker := NewProviderFetchWorker(fetch, operations)
	client, listenerPool, err := StartWithWorkers(ctx, testpostgres.URL(t, db), db.DB, func(workers *river.Workers) {
		river.AddWorker(workers, worker)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer listenerPool.Close()
	defer stopRiverClient(t, client)
	// Wire the enqueueing service after River has been constructed.
	fetch = service.NewProviderFetchService(registry, factory, cache).WithDurableOperations(repo, client)
	worker.fetch = fetch
	events, stopEvents := client.Subscribe(river.EventKindJobCompleted)
	defer stopEvents()

	start := func(refresh bool) *persistence.Operation {
		t.Helper()
		op, err := fetch.StartProviderOperation(ctx, "release_search", "artist:test", 0, 1, refresh)
		if err != nil {
			t.Fatalf("start provider operation: %v", err)
		}
		awaitRiverCompletion(t, ctx, events, *op.RiverJobID)
		return readScanDispatchOperation(t, ctx, repo, op.ID)
	}
	first := start(false)
	if first.State != "succeeded" || first.Stage != "provider_response_saved" || requests.Load() != 1 {
		t.Fatalf("first delivery state=%s/%s HTTP calls=%d", first.State, first.Stage, requests.Load())
	}
	// A duplicate invocation carrying the original durable job identity sees
	// the terminal operation and must not issue another provider request.
	if err := worker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{
		JobRow: &rivertype.JobRow{ID: *first.RiverJobID},
		Args:   service.ProviderFetchOperationArgs{OperationID: first.ID},
	}); err != nil {
		t.Fatalf("duplicate worker delivery: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("duplicate delivery made HTTP request; calls=%d", requests.Load())
	}
	if len(first.InputSnapshot) == 0 || json.Valid(first.InputSnapshot) == false {
		t.Fatalf("missing persisted intent: %s", first.InputSnapshot)
	}
	// Ordinary requests reuse a successfully cached payload and never call HTTP.
	second := start(false)
	if second.State != "succeeded" || second.Stage != "provider_cache_reused" || requests.Load() != 1 {
		t.Fatalf("cache reuse state=%s/%s HTTP calls=%d", second.State, second.Stage, requests.Load())
	}
	before, err := cache.GetCurrentCachedResponse(ctx, *first.ProviderSourceID, *first.ProviderCacheKey, *first.ProviderConfigurationIdentity)
	if err != nil || before == nil {
		t.Fatalf("read cached response: response=%v err=%v", before, err)
	}
	status.Store(http.StatusBadGateway)
	beforeRefreshRequests := requests.Load()
	failed := start(true)
	if requests.Load() <= beforeRefreshRequests {
		t.Fatal("explicit refresh failed without an HTTP request")
	}
	if failed.State != "failed" || failed.Stage != "provider_fetch_failed" || failed.SafeError == nil {
		t.Fatalf("refresh failure operation: %#v", failed)
	}
	after, err := cache.GetCurrentCachedResponse(ctx, *first.ProviderSourceID, *first.ProviderCacheKey, *first.ProviderConfigurationIdentity)
	if err != nil || after == nil {
		t.Fatalf("read retained cache: response=%v err=%v", after, err)
	}
	if string(after.Payload) != string(before.Payload) || !reflect.DeepEqual(after.Revision, before.Revision) {
		t.Fatalf("failed refresh changed cached response: before=%#v after=%#v", before, after)
	}

	// All supported operation kinds dispatch through the same persisted intent
	// and production worker path; the adapter's captured raw body survives cache.
	status.Store(http.StatusOK)
	for _, tc := range []struct {
		kind, input, wantRaw string
	}{
		{"release_lookup", "11111111-1111-4111-8111-111111111111", "preserve-release-lookup"},
		{"recording_search", "recording:test", "preserve-recording-search"},
		{"recording_lookup", "22222222-2222-4222-8222-222222222222", "preserve-recording-lookup"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			op, err := fetch.StartProviderOperation(ctx, tc.kind, tc.input, 0, 1, false)
			if err != nil {
				t.Fatalf("start operation: %v", err)
			}
			awaitRiverCompletion(t, ctx, events, *op.RiverJobID)
			finished := readScanDispatchOperation(t, ctx, repo, op.ID)
			if finished.State != "succeeded" || finished.Stage != "provider_response_saved" {
				t.Fatalf("operation state=%s/%s", finished.State, finished.Stage)
			}
			cached, err := cache.GetCurrentCachedResponse(ctx, *finished.ProviderSourceID, *finished.ProviderCacheKey, *finished.ProviderConfigurationIdentity)
			if err != nil || cached == nil {
				t.Fatalf("cached response=%v err=%v", cached, err)
			}
			if !json.Valid(cached.Payload) || !strings.Contains(string(cached.Payload), tc.wantRaw) {
				t.Fatalf("raw response did not preserve unknown field: %s", cached.Payload)
			}
		})
	}
}
