//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

type fallbackHandoffRiverInserter struct {
	client    persistence.RiverInserter
	failWake  bool
	wakeCalls int
}

func (inserter *fallbackHandoffRiverInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if inserter.client == nil {
		return nil, errors.New("fallback handoff River client is not ready")
	}
	result, err := inserter.client.InsertTx(ctx, tx, args, options)
	if err != nil {
		return result, err
	}
	if _, ok := args.(persistence.MatchingEvaluationWakeArgs); ok {
		inserter.wakeCalls++
		if inserter.failWake {
			return result, errors.New("injected matching wake insertion failure")
		}
	}
	return result, nil
}

func TestAutomaticFallbackEmptyRecordingReceiptHandoffPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAutomaticMatchingTargetFenceFixture(t)
	ctx, cancel := context.WithTimeout(fixture.ctx, 60*time.Second)
	defer cancel()

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
	if artistBearing || len(planned.Stages) == 0 || planned.Stages[0].Kind != "album_track" {
		t.Fatalf("expected an album_track fallback plan: plan=%+v artistBearing=%v", planned, artistBearing)
	}
	plan := policy.TypedFallbackQueryPlan{SchemaVersion: planned.SchemaVersion, Stages: planned.Stages}
	stage := plan.Stages[0]
	var event persistence.IncomingMatchingReconcileEvent
	if err := fixture.db.NewSelect().Model(&event).Where("state = ? AND delivery_claimed", "dispatched").Order("created_at DESC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read claimed reconciliation event: %v", err)
	}
	var eventEpoch int64
	if err := fixture.db.NewRaw(`SELECT delivery_epoch FROM incoming_matching_reconcile_event WHERE id=?`, event.ID).Scan(ctx, &eventEpoch); err != nil {
		t.Fatalf("read reconciliation delivery epoch: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	if !ok || providerKind != "provider_recording_search" || cachePrefix != "recording-search" {
		t.Fatalf("album_track fallback mapping kind=%q prefix=%q ok=%v", providerKind, cachePrefix, ok)
	}
	key := cachePrefix + ":0:10:" + stage.Query
	ticket, err := cache.BeginRefresh(ctx, source.ID, key, config.Identity)
	if err != nil {
		t.Fatalf("reserve recording-search response: %v", err)
	}
	accepted := json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":0,"offset":0,"recordings":[]}`)
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: accepted}); err != nil {
		t.Fatalf("prepopulate accepted empty recording-search response: %v", err)
	}

	riverInserter := &fallbackHandoffRiverInserter{}
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
	child, err = fixture.repository.GetOperation(ctx, childID)
	if err != nil || child.RiverJobID == nil {
		t.Fatalf("read registered fallback child: operation=%+v err=%v", child, err)
	}
	if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: *child.RiverJobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: childID}}); err != nil {
		t.Fatalf("run cache-hit provider worker: %v", err)
	}
	receipt, err := fixture.repository.GetMatchingFallbackSearchReceipt(ctx, parentID, childID)
	var acceptedValue, receiptValue any
	if json.Unmarshal(accepted, &acceptedValue) != nil || receipt == nil || json.Unmarshal(receipt.Payload, &receiptValue) != nil || !equalFallbackJSON(acceptedValue, receiptValue) {
		t.Fatalf("accepted response receipt=%+v err=%v", receipt, err)
	}
	var depOutcome string
	if err := fixture.db.NewRaw(`SELECT terminal_outcome FROM matching_provider_dependency WHERE request_id=?`, childID).Scan(ctx, &depOutcome); err != nil || depOutcome != "succeeded" {
		t.Fatalf("fallback dependency outcome=%q err=%v", depOutcome, err)
	}
	var wakeID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? ORDER BY river_job_id DESC LIMIT 1`, admitted.OriginalRunID).Scan(ctx, &wakeID); err != nil {
		t.Fatalf("read provider-settlement parent wake: %v", err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, wakeID).Exec(ctx); err != nil {
		t.Fatalf("make provider-settlement wake claimable: %v", err)
	}
	claim, outcome, err := fixture.repository.ClaimMatchingDelivery(ctx, parentID, 1, wakeID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim registered parent wake: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}
	var oldDeliveries int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND river_job_id=? AND consumed_at IS NOT NULL`, parentID, wakeID).Scan(ctx, &oldDeliveries); err != nil {
		t.Fatal(err)
	}
	if oldDeliveries != 0 {
		t.Fatalf("parent wake unexpectedly consumed before acquisition: %d", oldDeliveries)
	}
	acquisition := service.NewMatchingAcquisitionService(fixture.repository,
		service.NewProviderFetchService(registry, factory, cache), riverInserter)
	wakeCallsBefore := riverInserter.wakeCalls
	var beforeConsumed, beforeUnconsumed, beforeRunJobs, beforeDependencies int
	if err := fixture.db.NewRaw(`SELECT count(*) FILTER (WHERE consumed_at IS NOT NULL), count(*) FILTER (WHERE consumed_at IS NULL) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &beforeConsumed, &beforeUnconsumed); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, parentID).Scan(ctx, &beforeDependencies); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE args->>'operation_id'=?`, parentID.String()).Scan(ctx, &beforeRunJobs); err != nil {
		t.Fatal(err)
	}
	riverInserter.failWake = true
	changed, handled, err := acquisition.Advance(ctx, parentID, 1, wakeID, claim)
	if err == nil || changed || !handled || riverInserter.wakeCalls != wakeCallsBefore+1 {
		t.Fatalf("injected continuation wake failure: changed=%v handled=%v wakeCalls=%d err=%v", changed, handled, riverInserter.wakeCalls, err)
	}
	snapshot, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || snapshot.Phase != persistence.MatchingAcquisitionFallbackSearch || snapshot.Revision != 0 {
		t.Fatalf("failed handoff changed acquisition snapshot=%+v exists=%v err=%v", snapshot, exists, err)
	}
	var consumed, unconsumed, runJobs, dependencies int
	if err := fixture.db.NewRaw(`SELECT count(*) FILTER (WHERE consumed_at IS NOT NULL), count(*) FILTER (WHERE consumed_at IS NULL) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &consumed, &unconsumed); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, parentID).Scan(ctx, &dependencies); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE args->>'operation_id'=?`, parentID.String()).Scan(ctx, &runJobs); err != nil {
		t.Fatal(err)
	}
	if consumed != beforeConsumed || unconsumed != beforeUnconsumed || dependencies != beforeDependencies || runJobs != beforeRunJobs {
		t.Fatalf("failed handoff did not roll back source state: before=%d/%d/%d/%d after=%d/%d/%d/%d", beforeConsumed, beforeUnconsumed, beforeDependencies, beforeRunJobs, consumed, unconsumed, dependencies, runJobs)
	}

	// Change the live cache to a nonempty response after the original accepted
	// receipt. Retrying must still derive from the immutable empty receipt.
	changedTicket, err := cache.BeginRefresh(ctx, source.ID, key, config.Identity)
	if err != nil {
		t.Fatalf("reserve replacement recording-search response: %v", err)
	}
	replacement := json.RawMessage(`{"created":"2026-10-12T00:01:00Z","count":1,"offset":0,"recordings":[{"id":"00000000-0000-4000-8000-000000000001","releases":[{"id":"00000000-0000-4000-8000-000000000002"}]}]}`)
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: changedTicket, FetchedAt: time.Now().UTC(), Payload: replacement}); err != nil {
		t.Fatalf("replace live recording-search response: %v", err)
	}
	riverInserter.failWake = false
	changed, handled, err = acquisition.Advance(ctx, parentID, 1, wakeID, claim)
	if err != nil || !handled || !changed {
		t.Fatalf("retry empty fallback search from accepted receipt: changed=%v handled=%v err=%v", changed, handled, err)
	}
	snapshot, exists, err = fixture.repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || snapshot.Phase != persistence.MatchingAcquisitionFallbackApplicability || snapshot.Revision != 1 {
		t.Fatalf("retry did not use original empty receipt: snapshot=%+v exists=%v err=%v", snapshot, exists, err)
	}
	if riverInserter.wakeCalls != wakeCallsBefore+2 {
		t.Fatalf("matching wake insert attempts=%d, want failed attempt plus retry", riverInserter.wakeCalls-wakeCallsBefore)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND consumed_at IS NOT NULL`, parentID).Scan(ctx, &consumed); err != nil || consumed != 1 {
		t.Fatalf("successful retry consumed deliveries=%d err=%v", consumed, err)
	}
	var freshWake int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL ORDER BY river_job_id DESC LIMIT 1`, parentID).Scan(ctx, &freshWake); err != nil || freshWake < 1 || freshWake == wakeID {
		t.Fatalf("read fresh parent continuation wake=%d err=%v", freshWake, err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, freshWake).Exec(ctx); err != nil {
		t.Fatalf("make fresh parent continuation wake claimable: %v", err)
	}
	var wakeArgs string
	if err := fixture.db.NewRaw(`SELECT args::text FROM river_job WHERE id=?`, freshWake).Scan(ctx, &wakeArgs); err != nil {
		t.Fatalf("read fresh parent wake args: %v", err)
	}
	var args service.MatchingEvaluationOperationArgs
	if err := json.Unmarshal([]byte(wakeArgs), &args); err != nil || args.OperationID != parentID {
		t.Fatalf("fresh wake args=%s operation=%s err=%v", wakeArgs, args.OperationID, err)
	}
	newClaim, newOutcome, err := fixture.repository.ClaimMatchingDelivery(ctx, parentID, 1, freshWake, 1)
	if err != nil || newOutcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim fresh parent wake: claim=%+v outcome=%v err=%v", newClaim, newOutcome, err)
	}
	var deliveriesBeforeStale, unconsumedBeforeStale int
	if err := fixture.db.NewRaw(`SELECT count(*),count(*) FILTER (WHERE consumed_at IS NULL) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &deliveriesBeforeStale, &unconsumedBeforeStale); err != nil {
		t.Fatal(err)
	}
	changed, handled, err = acquisition.Advance(ctx, parentID, 1, wakeID, claim)
	if err != nil || changed || !handled {
		t.Fatalf("stale parent delivery advance: changed=%v handled=%v err=%v", changed, handled, err)
	}
	var finalDeliveries, finalUnconsumed int
	if err := fixture.db.NewRaw(`SELECT count(*),count(*) FILTER (WHERE consumed_at IS NULL) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &finalDeliveries, &finalUnconsumed); err != nil || finalDeliveries != deliveriesBeforeStale || finalUnconsumed != unconsumedBeforeStale {
		t.Fatalf("stale advance changed delivery ledger: before=%d/%d after=%d/%d err=%v", deliveriesBeforeStale, unconsumedBeforeStale, finalDeliveries, finalUnconsumed, err)
	}
}

func equalFallbackJSON(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftBytes) == string(rightBytes)
}
