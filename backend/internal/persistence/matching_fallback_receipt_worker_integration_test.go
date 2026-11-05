//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestAutomaticFallbackCacheHitWorkerCapturesReceiptPostgreSQL(t *testing.T) {
	testAutomaticFallbackCacheHitWorkerCapturesReceiptPostgreSQL(t, false)
}

func TestAutomaticFallbackReceiptReplaysAfterInterruptedRetryPostgreSQL(t *testing.T) {
	testAutomaticFallbackCacheHitWorkerCapturesReceiptPostgreSQL(t, true)
}

func testAutomaticFallbackCacheHitWorkerCapturesReceiptPostgreSQL(t *testing.T, interruptedRetry bool) {
	t.Parallel()
	fixture := newAutomaticMatchingTargetFenceFixture(t)
	ctx, cancel := context.WithTimeout(fixture.ctx, 60*time.Second)
	defer cancel()

	// Switch the already-admitted artist run's current source metadata to a
	// genuinely non-artist fallback case, then refresh so admission uses the new
	// persisted group revision while retaining the real reconciliation claim.
	if _, err := fixture.db.NewRaw(`UPDATE media_metadata_result SET observed_tags='{"ALBUM":["Captured Album"],"TITLE":["Receipt Track"]}'::jsonb`).Exec(ctx); err != nil {
		t.Fatalf("set fallback metadata: %v", err)
	}
	grouping := persistence.NewIncomingGroupingRepository(fixture.db)
	if err := service.NewIncomingGroups(grouping).Refresh(ctx); err != nil {
		t.Fatalf("refresh groups after metadata change: %v", err)
	}
	groups, err := grouping.List(ctx)
	if err != nil || len(groups) != 1 {
		t.Fatalf("read refreshed group: groups=%d err=%v", len(groups), err)
	}
	var encodedTags []string
	if err := fixture.db.NewRaw(`SELECT observed_tags::text FROM media_metadata_result ORDER BY created_at,id`).Scan(ctx, &encodedTags); err != nil {
		t.Fatalf("read frozen source metadata: %v", err)
	}
	frozenTags := make([]map[string][]string, len(encodedTags))
	for index, encoded := range encodedTags {
		if err := json.Unmarshal([]byte(encoded), &frozenTags[index]); err != nil {
			t.Fatalf("decode frozen source metadata: %v", err)
		}
	}
	planned, artistBearing := policy.PlanFallbackQueries(frozenTags)
	if artistBearing || len(planned.Stages) == 0 {
		t.Fatalf("expected an artist-free fallback plan, got %+v artistBearing=%v", planned, artistBearing)
	}
	plan := policy.TypedFallbackQueryPlan{SchemaVersion: planned.SchemaVersion, Stages: planned.Stages}
	stage := plan.Stages[0]
	if stage.Kind != "album_track" {
		t.Fatalf("expected an album_track first stage, got %+v", stage)
	}

	var event persistence.IncomingMatchingReconcileEvent
	if err := fixture.db.NewSelect().Model(&event).Where("state = ? AND delivery_claimed", "dispatched").Order("created_at DESC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read claimed reconciliation event: %v", err)
	}
	var eventEpoch int64
	if err := fixture.db.NewRaw(`SELECT delivery_epoch FROM incoming_matching_reconcile_event WHERE id=?`, event.ID).Scan(ctx, &eventEpoch); err != nil {
		t.Fatalf("read reconciliation delivery epoch: %v", err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Errorf("cache-hit worker unexpectedly called MusicBrainz: %s", r.URL.String())
		http.Error(w, "unexpected request", http.StatusBadGateway)
	}))
	defer server.Close()
	registry := settings.New(persistence.NewSettingsRepository(fixture.db), nil)
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", server.URL+"/ws/2"); err != nil {
		t.Fatalf("save provider config: %v", err)
	}
	config, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatalf("read provider config: %v", err)
	}
	cache := persistence.NewProviderCacheRepository(fixture.db)
	source, err := cache.ResolveMusicBrainzSource(ctx, config.BaseURL, config.Identity)
	if err != nil {
		t.Fatalf("resolve provider source: %v", err)
	}
	providerKind, cachePrefix, ok := policy.ProviderKindForFallbackStage(stage.Kind)
	if !ok {
		t.Fatalf("fallback stage %q has no provider mapping", stage.Kind)
	}
	if stage.Kind == "album_track" && (providerKind != "provider_recording_search" || cachePrefix != "recording-search") {
		t.Fatalf("album_track must map to a recording search, got kind=%q prefix=%q", providerKind, cachePrefix)
	}
	key := cachePrefix + ":0:10:" + stage.Query
	cacheTicket, err := cache.BeginRefresh(ctx, source.ID, key, config.Identity)
	if err != nil {
		t.Fatalf("reserve cache entry: %v", err)
	}
	accepted := json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":1,"offset":0,"recordings":[{"id":"11111111-1111-4111-8111-111111111111","title":"Receipt Track","releases":[{"id":"22222222-2222-4222-8222-222222222222","title":"Captured Album"}]}]}`)
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: cacheTicket, FetchedAt: time.Now().UTC(), Payload: accepted}); err != nil {
		t.Fatalf("prepopulate accepted cache response: %v", err)
	}
	observedCacheResponse, err := cache.GetCurrentCachedResponse(ctx, source.ID, key, config.Identity)
	if err != nil || observedCacheResponse == nil {
		t.Fatalf("read observed accepted cache response: response=%+v err=%v", observedCacheResponse, err)
	}

	riverInserter := &manualCacheReuseRiverInserter{}
	operations := service.NewOperationsWithRiver(fixture.repository, riverInserter)
	factory := service.NewProviderFactory(registry, nil, nil, server.Client(), nil)
	fetch := service.NewProviderFetchService(registry, factory, cache).WithDurableOperations(fixture.repository, riverInserter)
	providerWorker := jobs.NewProviderFetchWorker(fetch, operations)
	matchingWorker := jobs.NewMatchingEvaluationWorker(fixture.repository,
		service.NewMatchingEvaluationService(service.NewIncomingGroups(grouping), grouping, cache,
			persistence.NewIncomingMatchCandidateRepository(fixture.db), registry),
		operations)
	client, listenerPool, err := jobs.PrepareWithWorkers(ctx, testpostgres.URL(t, fixture.db), fixture.db.DB, func(workers *river.Workers) {
		river.AddWorker(workers, providerWorker)
		river.AddWorker(workers, matchingWorker)
	})
	if err != nil {
		t.Fatalf("prepare River client: %v", err)
	}
	t.Cleanup(listenerPool.Close)
	riverInserter.client = client
	providerID, sourceID, identity := source.ProviderID, source.ID, config.Identity
	input, err := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}{1, stage.Query, 0, 10})
	if err != nil {
		t.Fatalf("encode fallback intent: %v", err)
	}
	childID, parentID := uuid.New(), uuid.New()
	child := &persistence.Operation{ID: childID, Kind: providerKind, State: "queued", Stage: "queued", InputSnapshot: input,
		ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderConfigurationIdentity: &identity,
		ProviderEndpoint: source.Endpoint, ProviderCacheKey: &key, Attempt: 1}
	admitted, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(ctx, client, persistence.AutomaticFallbackMatchingAdmission{
		Parent:     &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1},
		ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Provider:   persistence.MatchingProviderRequest{Operation: child, Args: service.ProviderFetchOperationArgs{OperationID: childID}},
		Source:     persistence.AutomaticFallbackProviderSource{ID: source.ID, ProviderID: source.ProviderID, Endpoint: source.Endpoint, ConfigurationIdentity: config.Identity},
		GroupID:    groups[0].ID, ExpectedGroupRevision: groups[0].Revision, PolicyVersion: service.MatchingPolicyVersion,
		Plan: plan, EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: event.HighestDeliveryAttempt, EventDeliveryEpoch: eventEpoch,
	})
	if err != nil {
		t.Fatalf("admit automatic fallback run: %v", err)
	}
	var priorParentDeliveries int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, admitted.OriginalRunID).Scan(ctx, &priorParentDeliveries); err != nil {
		t.Fatalf("count initial parent deliveries: %v", err)
	}
	child, err = fixture.repository.GetOperation(ctx, childID)
	if err != nil || child.RiverJobID == nil {
		t.Fatalf("read registered fallback child: operation=%+v err=%v", child, err)
	}
	expectedReceiptGeneration := observedCacheResponse.Generation
	if interruptedRetry {
		// Record an accepted provider response against the real admitted operation
		// delivery, but stop before settling its dependency. This is the crash window
		// protected by the durable receipt.
		epochA, claimed, err := fixture.repository.ClaimProviderOperationDelivery(ctx, childID, child.Attempt, *child.RiverJobID)
		if err != nil || !claimed {
			t.Fatalf("claim original provider delivery: epoch=%d claimed=%v err=%v", epochA, claimed, err)
		}
		if err := operations.RunningForProviderDelivery(ctx, child, epochA, "fetching_provider"); err != nil {
			t.Fatalf("mark original provider delivery running: %v", err)
		}
		ticketA, err := cache.BeginRefresh(ctx, source.ID, key, config.Identity)
		if err != nil {
			t.Fatalf("reserve accepted response A: %v", err)
		}
		fenceA := persistence.ProviderDeliveryFence{OperationID: childID, Attempt: child.Attempt, RiverJobID: *child.RiverJobID, ExecutionEpoch: epochA}
		if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticketA, FetchedAt: time.Now().UTC(), Payload: accepted, Delivery: &fenceA}); err != nil {
			t.Fatalf("accept response A with original delivery fence: %v", err)
		}
		receiptA, err := fixture.repository.GetMatchingFallbackSearchReceipt(ctx, parentID, childID)
		var acceptedValue, receiptValue any
		if err := json.Unmarshal(accepted, &acceptedValue); err != nil {
			t.Fatalf("decode accepted response A: %v", err)
		}
		if receiptA != nil {
			if err := json.Unmarshal(receiptA.Payload, &receiptValue); err != nil {
				t.Fatalf("decode receipt response A: %v", err)
			}
		}
		if err != nil || receiptA == nil || !reflect.DeepEqual(receiptValue, acceptedValue) || receiptA.ExecutionEpoch != epochA || receiptA.ProviderAttempt != child.Attempt || receiptA.RiverJobID != *child.RiverJobID {
			t.Fatalf("read accepted response A receipt: receipt=%+v err=%v", receiptA, err)
		}
		expectedReceiptGeneration = receiptA.CacheGeneration

		// Replace the mutable cache value without an operation fence. The receipt
		// must remain the source of truth when River redelivers this operation.
		responseB := json.RawMessage(`{"created":"2026-10-12T00:01:00Z","count":1,"offset":0,"recordings":[{"id":"33333333-3333-4333-8333-333333333333","title":"Replacement Track"}]}`)
		ticketB, err := cache.BeginRefresh(ctx, source.ID, key, config.Identity)
		if err != nil {
			t.Fatalf("reserve replacement response B: %v", err)
		}
		if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticketB, FetchedAt: time.Now().UTC(), Payload: responseB}); err != nil {
			t.Fatalf("replace mutable cache with response B: %v", err)
		}
		current, err := cache.GetCurrentCachedResponse(ctx, source.ID, key, config.Identity)
		var currentValue, responseBValue any
		if current != nil {
			if err := json.Unmarshal(current.Payload, &currentValue); err != nil {
				t.Fatalf("decode current cache response B: %v", err)
			}
		}
		if err := json.Unmarshal(responseB, &responseBValue); err != nil {
			t.Fatalf("decode expected cache response B: %v", err)
		}
		if err != nil || current == nil || !reflect.DeepEqual(currentValue, responseBValue) {
			t.Fatalf("confirm response B replaced cache A: response=%+v err=%v", current, err)
		}

		// ClaimProviderOperationDelivery's existing five-minute lease is its crash
		// recovery mechanism. Moving the timestamp past that lease simulates elapsed
		// process downtime; the next real worker claim advances the execution epoch.
		if _, err := fixture.db.NewRaw(`UPDATE operation SET provider_delivery_claimed_at=now()-interval '6 minutes' WHERE id=?`, childID).Exec(ctx); err != nil {
			t.Fatalf("expire original provider delivery lease: %v", err)
		}
	}
	if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: *child.RiverJobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: childID}}); err != nil {
		t.Fatalf("run cache-hit provider worker: %v", err)
	}
	completed, err := fixture.repository.GetOperation(ctx, childID)
	wantStage := "provider_cache_reused"
	if interruptedRetry {
		wantStage = "provider_fallback_result_reused"
	}
	if err != nil || completed.State != "succeeded" || completed.Stage != wantStage {
		t.Fatalf("fallback worker completion=%+v err=%v", completed, err)
	}
	receipt, err := fixture.repository.GetMatchingFallbackSearchReceipt(ctx, parentID, childID)
	if err != nil || receipt == nil || receipt.CacheGeneration != expectedReceiptGeneration {
		t.Fatalf("fallback receipt=%+v err=%v", receipt, err)
	}
	var acceptedValue, receiptValue any
	if err := json.Unmarshal(accepted, &acceptedValue); err != nil {
		t.Fatalf("decode accepted cache response: %v", err)
	}
	if err := json.Unmarshal(receipt.Payload, &receiptValue); err != nil || !reflect.DeepEqual(receiptValue, acceptedValue) {
		t.Fatalf("fallback receipt payload differs from cache A: payload=%s err=%v", receipt.Payload, err)
	}
	var dependencyState string
	if err := fixture.db.NewRaw(`SELECT terminal_outcome FROM matching_provider_dependency WHERE request_id=?`, childID).Scan(ctx, &dependencyState); err != nil || dependencyState != "succeeded" {
		t.Fatalf("fallback dependency state=%q err=%v", dependencyState, err)
	}
	var parentDeliveries int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, admitted.OriginalRunID).Scan(ctx, &parentDeliveries); err != nil || parentDeliveries <= priorParentDeliveries {
		t.Fatalf("provider settlement did not enqueue parent wake: deliveries before=%d after=%d err=%v", priorParentDeliveries, parentDeliveries, err)
	}
	if requests.Load() != 0 {
		t.Fatalf("cache-hit worker made %d HTTP requests", requests.Load())
	}
	if interruptedRetry {
		if completed.ProviderExecutionEpoch != 2 {
			t.Fatalf("replayed delivery execution epoch=%d, want 2", completed.ProviderExecutionEpoch)
		}
		var receiptUnchanged bool
		if err := fixture.db.NewRaw(`SELECT payload=?::jsonb FROM matching_fallback_search_receipt WHERE run_id=? AND request_id=?`, string(accepted), admitted.OriginalRunID, childID).Scan(ctx, &receiptUnchanged); err != nil || !receiptUnchanged {
			t.Fatalf("response A receipt changed during replay: unchanged=%v err=%v", receiptUnchanged, err)
		}
	}
	if !json.Valid(receipt.Payload) {
		t.Fatalf("receipt did not preserve accepted response: %s", receipt.Payload)
	}
}
