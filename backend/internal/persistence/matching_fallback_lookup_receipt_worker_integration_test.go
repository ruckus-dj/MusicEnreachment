//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestAutomaticFallbackLookupWorkerReceiptSurvivesCacheReplacementPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAutomaticMatchingTargetFenceFixture(t)
	ctx, cancel := context.WithTimeout(fixture.ctx, 60*time.Second)
	defer cancel()

	if _, err := fixture.db.NewRaw(`UPDATE media_metadata_result SET observed_tags='{"ALBUM":["Captured Album"],"TITLE":["Receipt Track"]}'::jsonb`).Exec(ctx); err != nil {
		t.Fatalf("set fallback metadata: %v", err)
	}
	grouping := persistence.NewIncomingGroupingRepository(fixture.db)
	if err := service.NewIncomingGroups(grouping).Refresh(ctx); err != nil {
		t.Fatalf("refresh groups: %v", err)
	}
	groups, err := grouping.List(ctx)
	if err != nil || len(groups) != 1 {
		t.Fatalf("read refreshed group: groups=%d err=%v", len(groups), err)
	}
	var tags []string
	if err := fixture.db.NewRaw(`SELECT observed_tags::text FROM media_metadata_result ORDER BY created_at,id`).Scan(ctx, &tags); err != nil {
		t.Fatalf("read fallback tags: %v", err)
	}
	frozen := make([]map[string][]string, len(tags))
	for i, encoded := range tags {
		if err := json.Unmarshal([]byte(encoded), &frozen[i]); err != nil {
			t.Fatalf("decode fallback tags: %v", err)
		}
	}
	planned, artistBearing := policy.PlanFallbackQueries(frozen)
	if artistBearing || len(planned.Stages) == 0 || planned.Stages[0].Kind != "album_track" {
		t.Fatalf("unexpected fallback plan: %+v artistBearing=%v", planned, artistBearing)
	}
	plan := policy.TypedFallbackQueryPlan{SchemaVersion: planned.SchemaVersion, Stages: planned.Stages}
	stage := plan.Stages[0]
	var event persistence.IncomingMatchingReconcileEvent
	if err := fixture.db.NewSelect().Model(&event).Where("state = ? AND delivery_claimed", "dispatched").Order("created_at DESC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read claimed reconciliation event: %v", err)
	}
	var eventEpoch int64
	if err := fixture.db.NewRaw(`SELECT delivery_epoch FROM incoming_matching_reconcile_event WHERE id=?`, event.ID).Scan(ctx, &eventEpoch); err != nil {
		t.Fatalf("read reconciliation epoch: %v", err)
	}

	var lookups atomic.Int32
	var releaseA, releaseB string
	trackID, recordingID := uuid.NewString(), uuid.NewString()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookups.Add(1)
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 4 || parts[len(parts)-2] != "release" {
			http.Error(w, "unexpected endpoint", http.StatusNotFound)
			return
		}
		id := parts[len(parts)-1]
		if id != releaseA && id != releaseB {
			http.Error(w, "unexpected release", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Album %s","artist-credit":[{"name":"Artist %s","artist":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Artist %s"}}],"media":[{"position":1,"tracks":[{"id":%q,"number":"1","recording":{"id":%q,"title":"Receipt Track","artist-credit":[{"name":"Track Artist","artist":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Artist %s"}}]}}]}]}`, id, id, id, id, trackID, recordingID, id)
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
	searchKey := "recording-search:0:10:" + stage.Query
	searchTicket, err := cache.BeginRefresh(ctx, source.ID, searchKey, config.Identity)
	if err != nil {
		t.Fatalf("reserve fallback search: %v", err)
	}
	// Use fixture-only UUIDs outside newIncomingMatchFixture's trusted cache keys
	// so this worker must fetch rather than reuse an existing lookup response.
	acceptedSearch := json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":1,"offset":0,"recordings":[{"id":"11111111-1111-4111-8111-111111111111","title":"Receipt Track","releases":[{"id":"90000000-0000-4000-8000-000000000001","title":"Captured Album"},{"id":"90000000-0000-4000-8000-000000000002","title":"Captured Album B"}]}]}`)
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: searchTicket, FetchedAt: time.Now().UTC(), Payload: acceptedSearch}); err != nil {
		t.Fatalf("accept fallback search fixture: %v", err)
	}

	inserter := &fallbackHandoffRiverInserter{}
	operations := service.NewOperationsWithRiver(fixture.repository, inserter)
	httpClient := server.Client()
	factory := service.NewProviderFactory(registry, nil, nil, httpClient, nil)
	fetch := service.NewProviderFetchService(registry, factory, cache).WithDurableOperations(fixture.repository, inserter)
	providerWorker := jobs.NewProviderFetchWorker(fetch, operations)
	matchingWorker := jobs.NewMatchingEvaluationWorker(fixture.repository,
		service.NewMatchingEvaluationService(service.NewIncomingGroups(grouping), grouping, cache,
			persistence.NewIncomingMatchCandidateRepository(fixture.db), registry), operations)
	client, listenerPool, err := jobs.PrepareWithWorkers(ctx, testpostgres.URL(t, fixture.db), fixture.db.DB, func(workers *river.Workers) {
		river.AddWorker(workers, providerWorker)
		river.AddWorker(workers, matchingWorker)
	})
	if err != nil {
		t.Fatalf("prepare River client: %v", err)
	}
	t.Cleanup(listenerPool.Close)
	inserter.client = client
	providerKind, _, ok := policy.ProviderKindForFallbackStage(stage.Kind)
	if !ok {
		t.Fatalf("no provider mapping for %q", stage.Kind)
	}
	intent, err := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}{1, stage.Query, 0, 10})
	if err != nil {
		t.Fatalf("encode search intent: %v", err)
	}
	childID, parentID := uuid.New(), uuid.New()
	providerID, sourceID, identity := source.ProviderID, source.ID, config.Identity
	child := &persistence.Operation{ID: childID, Kind: providerKind, State: "queued", Stage: "queued", InputSnapshot: intent,
		ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderConfigurationIdentity: &identity,
		ProviderEndpoint: source.Endpoint, ProviderCacheKey: &searchKey, Attempt: 1}
	admitted, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(ctx, client, persistence.AutomaticFallbackMatchingAdmission{
		Parent:     &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1},
		ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Provider:   persistence.MatchingProviderRequest{Operation: child, Args: service.ProviderFetchOperationArgs{OperationID: childID}},
		Source:     persistence.AutomaticFallbackProviderSource{ID: source.ID, ProviderID: source.ProviderID, Endpoint: source.Endpoint, ConfigurationIdentity: config.Identity},
		GroupID:    groups[0].ID, ExpectedGroupRevision: groups[0].Revision, PolicyVersion: service.MatchingPolicyVersion,
		Plan: plan, EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: event.HighestDeliveryAttempt, EventDeliveryEpoch: eventEpoch,
	})
	if err != nil {
		t.Fatalf("admit fallback run: %v", err)
	}
	child, err = fixture.repository.GetOperation(ctx, childID)
	if err != nil || child.RiverJobID == nil {
		t.Fatalf("read fallback search operation: %+v err=%v", child, err)
	}
	if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: *child.RiverJobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: childID}}); err != nil {
		t.Fatalf("complete cached fallback search: %v", err)
	}
	var wakeID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? ORDER BY river_job_id DESC LIMIT 1`, admitted.OriginalRunID).Scan(ctx, &wakeID); err != nil {
		t.Fatalf("read fallback parent wake: %v", err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, wakeID).Exec(ctx); err != nil {
		t.Fatalf("claimable parent wake: %v", err)
	}
	claim, outcome, err := fixture.repository.ClaimMatchingDelivery(ctx, parentID, 1, wakeID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim parent wake: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}
	acquisition := service.NewMatchingAcquisitionService(fixture.repository, fetch, inserter)
	changed, handled, err := acquisition.Advance(ctx, parentID, 1, wakeID, claim)
	if err != nil || !changed || !handled {
		t.Fatalf("advance fallback handoff: changed=%v handled=%v err=%v", changed, handled, err)
	}
	snapshot, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || snapshot.Phase != persistence.MatchingAcquisitionFallbackHydration {
		t.Fatalf("hydration snapshot=%+v exists=%v err=%v", snapshot, exists, err)
	}
	var output struct {
		Lookups []struct {
			ReleaseID string    `json:"release_id"`
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"lookups"`
	}
	if err := json.Unmarshal(snapshot.Output, &output); err != nil || len(output.Lookups) < 2 {
		t.Fatalf("hydration lookup manifest=%s err=%v", snapshot.Output, err)
	}
	sort.Slice(output.Lookups, func(i, j int) bool { return output.Lookups[i].ReleaseID < output.Lookups[j].ReleaseID })
	second := output.Lookups[1]
	releaseA, releaseB = output.Lookups[0].ReleaseID, second.ReleaseID
	lookupOperation, err := fixture.repository.GetOperation(ctx, second.RequestID)
	if err != nil || lookupOperation.RiverJobID == nil {
		t.Fatalf("read second lookup operation: %+v err=%v", lookupOperation, err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *lookupOperation.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("make second lookup delivery deterministic: %v", err)
	}
	if lookupOperation.ProviderSourceID == nil || *lookupOperation.ProviderSourceID != source.ID ||
		lookupOperation.ProviderConfigurationIdentity == nil || *lookupOperation.ProviderConfigurationIdentity != config.Identity ||
		lookupOperation.ProviderCacheKey == nil || *lookupOperation.ProviderCacheKey != second.CacheKey {
		t.Fatalf("lookup operation source fence differs from saved configuration: operation=%+v source=%+v config=%+v", lookupOperation, source, config)
	}
	var lookupIntent struct {
		MBID string `json:"mbid"`
	}
	if err := json.Unmarshal(lookupOperation.InputSnapshot, &lookupIntent); err != nil || lookupIntent.MBID != releaseB {
		t.Fatalf("lookup operation intent=%+v raw=%s err=%v want mbid=%s", lookupIntent, lookupOperation.InputSnapshot, err, releaseB)
	}
	currentSource, err := cache.ResolveMusicBrainzSource(ctx, config.BaseURL, config.Identity)
	if err != nil || currentSource.ID != source.ID {
		t.Fatalf("saved source no longer resolves before worker: current=%+v original=%+v err=%v", currentSource, source, err)
	}
	runtimeConfig, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil || runtimeConfig != config {
		t.Fatalf("provider config changed before worker: runtime=%+v admitted=%+v err=%v", runtimeConfig, config, err)
	}
	uncached, err := cache.GetCurrentCachedResponse(ctx, source.ID, second.CacheKey, config.Identity)
	if err != nil || uncached != nil {
		t.Fatalf("expected fresh lookup cache miss before worker: response=%+v err=%v", uncached, err)
	}
	if _, err := factory.MusicBrainzForConfig(ctx, config); err != nil {
		t.Fatalf("saved provider configuration cannot construct its adapter: %v", err)
	}
	var existingReceiptRows int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_lookup_receipt WHERE run_id=? AND request_id=?`, parentID, second.RequestID).Scan(ctx, &existingReceiptRows); err != nil || existingReceiptRows != 0 {
		t.Fatalf("unexpected preexisting lookup receipt count=%d err=%v", existingReceiptRows, err)
	}
	if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: *lookupOperation.RiverJobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: second.RequestID}}); err != nil {
		t.Fatalf("fetch second release lookup: %v", err)
	}
	if lookups.Load() != 1 {
		completed, _ := fixture.repository.GetOperation(ctx, second.RequestID)
		cached, cacheErr := cache.GetCurrentCachedResponse(ctx, source.ID, second.CacheKey, config.Identity)
		var safeError string
		if completed.SafeError != nil {
			safeError = *completed.SafeError
		}
		var cacheRows int
		_ = fixture.db.NewRaw(`SELECT count(*) FROM provider_response_cache WHERE provider_source_id=? AND cache_key=?`, source.ID, second.CacheKey).Scan(ctx, &cacheRows)
		t.Fatalf("actual lookup requests=%d, operation=%+v safeError=%q cached=%+v cacheErr=%v cacheRows=%d; want one", lookups.Load(), completed, safeError, cached, cacheErr, cacheRows)
	}
	receipt, err := fixture.repository.GetMatchingFallbackLookupReceipt(ctx, parentID, second.RequestID)
	if err != nil || receipt == nil || receipt.StageIndex != 0 || receipt.CacheKey != second.CacheKey {
		completed, operationErr := fixture.repository.GetOperation(ctx, second.RequestID)
		var safeError string
		if completed != nil && completed.SafeError != nil {
			safeError = *completed.SafeError
		}
		var acquisition persistence.MatchingAcquisitionSnapshot
		acquisition, _, _ = fixture.repository.GetMatchingAcquisition(ctx, parentID)
		t.Fatalf("second lookup receipt=%+v err=%v operationState=%q stage=%q operationErr=%v safeError=%q acquisitionPhase=%q", receipt, err, completed.State, completed.Stage, operationErr, safeError, acquisition.Phase)
	}
	var rawA any
	if err := json.Unmarshal(receipt.Payload, &rawA); err != nil {
		t.Fatalf("decode receipt raw response: %v", err)
	}
	wantRaw := map[string]any{}
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"id":%q,"title":"Album %s","artist-credit":[{"name":"Artist %s","artist":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Artist %s"}}],"media":[{"position":1,"tracks":[{"id":%q,"number":"1","recording":{"id":%q,"title":"Receipt Track","artist-credit":[{"name":"Track Artist","artist":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Artist %s"}}]}}]}]}`, releaseB, releaseB, releaseB, releaseB, trackID, recordingID, releaseB)), &wantRaw); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rawA, wantRaw) {
		t.Fatalf("receipt did not preserve raw lookup A: got=%s", receipt.Payload)
	}
	projectionBefore, err := cache.GetMusicBrainzMatchingSnapshot(ctx, source.Endpoint, config.Identity, []string{releaseB}, []string{second.CacheKey})
	if err != nil || len(projectionBefore.Releases) != 1 || projectionBefore.Responses[second.CacheKey] == nil ||
		len(projectionBefore.Releases[0].Tracks) == 0 || len(projectionBefore.Releases[0].Credits) == 0 {
		t.Fatalf("read projection A: %+v err=%v", projectionBefore, err)
	}
	var acceptedProjection persistence.ProviderMatchingSnapshot
	if err := json.Unmarshal(receipt.AcceptedProjection, &acceptedProjection); err != nil || len(acceptedProjection.Releases) != 1 ||
		len(acceptedProjection.Releases[0].Tracks) == 0 || len(acceptedProjection.Releases[0].Credits) == 0 {
		t.Fatalf("receipt did not preserve complete release projection A: %+v err=%v", acceptedProjection.Releases, err)
	}
	projectionDigest := sha256.Sum256(receipt.AcceptedProjection)
	if receipt.ProjectionSHA256 != hex.EncodeToString(projectionDigest[:]) {
		t.Fatalf("receipt projection digest=%s expected=%s", receipt.ProjectionSHA256, hex.EncodeToString(projectionDigest[:]))
	}

	responseB := json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Replacement %s","artist-credit":[],"media":[{"tracks":[]}]}`, releaseB, releaseB))
	graph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: responseB}})
	if err != nil {
		t.Fatalf("normalize cache B: %v", err)
	}
	ticketB, err := cache.BeginRefresh(ctx, source.ID, second.CacheKey, config.Identity)
	if err != nil {
		t.Fatalf("reserve cache B: %v", err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticketB, FetchedAt: time.Now().UTC(), Payload: responseB, Graph: graph}); err != nil {
		t.Fatalf("apply cache B: %v", err)
	}
	current, err := cache.GetCurrentCachedResponse(ctx, source.ID, second.CacheKey, config.Identity)
	if err != nil || current == nil || string(current.Payload) == string(receipt.Payload) {
		t.Fatalf("cache B was not accepted: response=%+v err=%v", current, err)
	}
	projectionAfter, err := cache.GetMusicBrainzMatchingSnapshot(ctx, source.Endpoint, config.Identity, []string{releaseB}, []string{second.CacheKey})
	if err != nil {
		t.Fatalf("read projection after B: %v", err)
	}
	if lookups.Load() != 1 {
		t.Fatalf("cache-only snapshot reads made %d HTTP requests, want one total lookup", lookups.Load())
	}
	if len(projectionAfter.Releases) != 1 || projectionAfter.Releases[0].Name != "Replacement "+releaseB {
		t.Fatalf("cache B did not change current source matching projection: %+v", projectionAfter.Releases)
	}
	receiptAfter, err := fixture.repository.GetMatchingFallbackLookupReceipt(ctx, parentID, second.RequestID)
	if err != nil || receiptAfter == nil || string(receiptAfter.Payload) != string(receipt.Payload) || receiptAfter.ProjectionSHA256 != receipt.ProjectionSHA256 || string(receiptAfter.AcceptedProjection) != string(receipt.AcceptedProjection) {
		t.Fatalf("cache B changed immutable receipt A: receipt=%+v err=%v", receiptAfter, err)
	}
}
