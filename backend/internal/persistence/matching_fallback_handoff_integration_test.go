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
	client                  persistence.RiverInserter
	failWake                bool
	wakeCalls               int
	failProviderFetchAt     int
	providerFetchInsertions int
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
	if _, ok := args.(service.ProviderFetchOperationArgs); ok {
		inserter.providerFetchInsertions++
		if inserter.failProviderFetchAt > 0 && inserter.providerFetchInsertions == inserter.failProviderFetchAt {
			return result, errors.New("injected provider-fetch insertion failure")
		}
	}
	return result, nil
}

func TestAutomaticFallbackEmptyRecordingReceiptHandoffPostgreSQL(t *testing.T) {
	t.Parallel()
	runAutomaticFallbackRecordingReceiptHandoff(t, json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":0,"offset":0,"recordings":[]}`), false)
}

func TestAutomaticFallbackRecordingReceiptHandoffHydratesEveryReleasePostgreSQL(t *testing.T) {
	t.Parallel()
	accepted := json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":2,"offset":0,"recordings":[{"id":"10000000-0000-4000-8000-000000000001","releases":[{"id":"20000000-0000-4000-8000-000000000001"},{"id":"20000000-0000-4000-8000-000000000002"},{"id":"20000000-0000-4000-8000-000000000003"},{"id":"20000000-0000-4000-8000-000000000004"},{"id":"20000000-0000-4000-8000-000000000005"},{"id":"20000000-0000-4000-8000-000000000006"},{"id":"20000000-0000-4000-8000-000000000007"}]},{"id":"10000000-0000-4000-8000-000000000002","releases":[{"id":"20000000-0000-4000-8000-000000000001"},{"id":"20000000-0000-4000-8000-000000000008"},{"id":"20000000-0000-4000-8000-000000000009"},{"id":"20000000-0000-4000-8000-00000000000A"},{"id":"20000000-0000-4000-8000-00000000000b"},{"id":"20000000-0000-4000-8000-00000000000C"}]}]}`)
	runAutomaticFallbackRecordingReceiptHandoff(t, accepted, true)
}

func runAutomaticFallbackRecordingReceiptHandoff(t *testing.T, accepted json.RawMessage, positive bool) {
	t.Helper()
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
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: accepted}); err != nil {
		t.Fatalf("prepopulate accepted recording-search response: %v", err)
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
	var registrations int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?`, parentID).Scan(ctx, &registrations); err != nil || registrations != 1 {
		t.Fatalf("initial fallback request registrations=%d want=1; err=%v", registrations, err)
	}
	if _, err := fixture.db.NewRaw(`INSERT INTO matching_fallback_request_registration
		(run_id,stage_index,role,request_id,provider_source_id,configuration_identity,kind,cache_key,canonical_input,input_sha256)
		VALUES (?,0,'search',?,?,?,'provider_release_lookup','invalid', '{}'::jsonb, decode(repeat('00',32),'hex'))`, parentID, uuid.New(), source.ID, config.Identity).Exec(ctx); err == nil {
		t.Fatal("database accepted a lookup operation as a fallback search registration")
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
	var beforeConsumed, beforeUnconsumed, beforeRunJobs, beforeDependencies, beforeOperations, beforeRegistrations int
	if err := fixture.db.NewRaw(`SELECT count(*) FILTER (WHERE consumed_at IS NOT NULL), count(*) FILTER (WHERE consumed_at IS NULL) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &beforeConsumed, &beforeUnconsumed); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, parentID).Scan(ctx, &beforeDependencies); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job job JOIN operation op ON op.id=(job.args->>'operation_id')::uuid WHERE op.id=? OR op.id IN (SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=?)`, parentID, parentID).Scan(ctx, &beforeRunJobs); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM operation WHERE id=? OR id IN (SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=?)`, parentID, parentID).Scan(ctx, &beforeOperations); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?`, parentID).Scan(ctx, &beforeRegistrations); err != nil {
		t.Fatal(err)
	}
	riverInserter.failWake = !positive
	if positive {
		riverInserter.providerFetchInsertions = 0
		riverInserter.failProviderFetchAt = 2
	}
	changed, handled, err := acquisition.Advance(ctx, parentID, 1, wakeID, claim)
	if err == nil || changed || !handled || (!positive && riverInserter.wakeCalls != wakeCallsBefore+1) || (positive && riverInserter.providerFetchInsertions != 2) {
		t.Fatalf("injected continuation insertion failure: changed=%v handled=%v wakeCalls=%d providerFetchInsertions=%d err=%v", changed, handled, riverInserter.wakeCalls, riverInserter.providerFetchInsertions, err)
	}
	snapshot, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || snapshot.Phase != persistence.MatchingAcquisitionFallbackSearch || snapshot.Revision != 0 {
		t.Fatalf("failed handoff changed acquisition snapshot=%+v exists=%v err=%v", snapshot, exists, err)
	}
	var consumed, unconsumed, runJobs, dependencies, registrationsAfter int
	if err := fixture.db.NewRaw(`SELECT count(*) FILTER (WHERE consumed_at IS NOT NULL), count(*) FILTER (WHERE consumed_at IS NULL) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &consumed, &unconsumed); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, parentID).Scan(ctx, &dependencies); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job job JOIN operation op ON op.id=(job.args->>'operation_id')::uuid WHERE op.id=? OR op.id IN (SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=?)`, parentID, parentID).Scan(ctx, &runJobs); err != nil {
		t.Fatal(err)
	}
	var operationsCount int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM operation WHERE id=? OR id IN (SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=?)`, parentID, parentID).Scan(ctx, &operationsCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?`, parentID).Scan(ctx, &registrationsAfter); err != nil {
		t.Fatal(err)
	}
	if consumed != beforeConsumed || unconsumed != beforeUnconsumed || dependencies != beforeDependencies || runJobs != beforeRunJobs || operationsCount != beforeOperations || registrationsAfter != beforeRegistrations {
		t.Fatalf("failed handoff did not roll back source state: before=%d/%d/%d/%d/%d/%d after=%d/%d/%d/%d/%d/%d", beforeConsumed, beforeUnconsumed, beforeDependencies, beforeRunJobs, beforeOperations, beforeRegistrations, consumed, unconsumed, dependencies, runJobs, operationsCount, registrationsAfter)
	}

	// Change the live cache after receipt capture. Retrying must still derive
	// exclusively from the immutable accepted response.
	changedTicket, err := cache.BeginRefresh(ctx, source.ID, key, config.Identity)
	if err != nil {
		t.Fatalf("reserve replacement recording-search response: %v", err)
	}
	replacement := json.RawMessage(`{"created":"2026-10-12T00:01:00Z","count":1,"offset":0,"recordings":[{"id":"00000000-0000-4000-8000-000000000001","releases":[{"id":"00000000-0000-4000-8000-000000000002"}]}]}`)
	if positive {
		replacement = json.RawMessage(`{"created":"2026-10-12T00:01:00Z","count":0,"offset":0,"recordings":[]}`)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: changedTicket, FetchedAt: time.Now().UTC(), Payload: replacement}); err != nil {
		t.Fatalf("replace live recording-search response: %v", err)
	}
	riverInserter.failWake = false
	changed, handled, err = acquisition.Advance(ctx, parentID, 1, wakeID, claim)
	if err != nil || !handled || !changed {
		t.Fatalf("retry fallback search from accepted receipt: changed=%v handled=%v err=%v", changed, handled, err)
	}
	snapshot, exists, err = fixture.repository.GetMatchingAcquisition(ctx, parentID)
	if positive {
		assertPositiveFallbackHydration(t, ctx, fixture, snapshot, exists, err, parentID, source, config.Identity, accepted, acquisition, wakeID, claim)
		return
	}
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

func assertPositiveFallbackHydration(
	t *testing.T,
	ctx context.Context,
	fixture automaticTargetFenceFixture,
	snapshot persistence.MatchingAcquisitionSnapshot,
	exists bool,
	err error,
	runID uuid.UUID,
	source persistence.ProviderSource,
	identity string,
	accepted json.RawMessage,
	acquisition *service.MatchingAcquisitionService,
	wakeID int64,
	claim persistence.MatchingDeliveryClaim,
) {
	t.Helper()
	if err != nil || !exists || snapshot.Phase != persistence.MatchingAcquisitionFallbackHydration || snapshot.Revision != 1 {
		t.Fatalf("successful receipt handoff snapshot=%+v exists=%v err=%v", snapshot, exists, err)
	}
	var input struct {
		ReleaseIDs     []string        `json:"release_ids"`
		SearchManifest json.RawMessage `json:"search_manifest"`
	}
	if err := json.Unmarshal(snapshot.Input, &input); err != nil {
		t.Fatalf("decode fallback acquisition input %s: %v", snapshot.Input, err)
	}
	wantIDs := []string{
		"20000000-0000-4000-8000-000000000001", "20000000-0000-4000-8000-000000000002",
		"20000000-0000-4000-8000-000000000003", "20000000-0000-4000-8000-000000000004",
		"20000000-0000-4000-8000-000000000005", "20000000-0000-4000-8000-000000000006",
		"20000000-0000-4000-8000-000000000007", "20000000-0000-4000-8000-000000000008",
		"20000000-0000-4000-8000-000000000009", "20000000-0000-4000-8000-00000000000a",
		"20000000-0000-4000-8000-00000000000b", "20000000-0000-4000-8000-00000000000c",
	}
	if !equalFallbackJSON(input.ReleaseIDs, wantIDs) {
		t.Fatalf("fallback release ids=%v want=%v", input.ReleaseIDs, wantIDs)
	}
	var manifest struct {
		Kind         string          `json:"kind"`
		AcceptedHits json.RawMessage `json:"accepted_hits"`
		ReleaseIDs   []string        `json:"release_ids"`
	}
	var acceptedValue, manifestHits any
	acceptedErr := json.Unmarshal(accepted, &acceptedValue)
	manifestErr := json.Unmarshal(input.SearchManifest, &manifest)
	hitsErr := json.Unmarshal(manifest.AcceptedHits, &manifestHits)
	if manifestErr != nil || acceptedErr != nil || hitsErr != nil || manifest.Kind != "provider_recording_search" || !equalFallbackJSON(manifestHits, acceptedValue) || !equalFallbackJSON(manifest.ReleaseIDs, wantIDs) {
		t.Fatalf("fallback search manifest=%s err=%v/%v/%v", input.SearchManifest, manifestErr, acceptedErr, hitsErr)
	}
	var output struct {
		Lookups []struct {
			ReleaseID string    `json:"release_id"`
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"lookups"`
	}
	if err := json.Unmarshal(snapshot.Output, &output); err != nil || len(output.Lookups) != len(wantIDs) {
		t.Fatalf("fallback lookup output=%s count=%d err=%v", snapshot.Output, len(output.Lookups), err)
	}
	var dependencies, registrations int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, runID).Scan(ctx, &dependencies); err != nil || dependencies != 13 {
		t.Fatalf("hydration dependencies=%d want initial plus 12 children; err=%v", dependencies, err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?`, runID).Scan(ctx, &registrations); err != nil || registrations != 13 {
		t.Fatalf("hydration request registrations=%d want initial plus 12 children; err=%v", registrations, err)
	}
	for index, lookup := range output.Lookups {
		if lookup.ReleaseID != wantIDs[index] || lookup.RequestID == uuid.Nil || lookup.CacheKey != "release-lookup:"+wantIDs[index] {
			t.Fatalf("lookup[%d]=%+v", index, lookup)
		}
		var operation struct {
			Kind       string          `bun:"kind"`
			State      string          `bun:"state"`
			Input      json.RawMessage `bun:"input_snapshot"`
			SourceID   uuid.UUID       `bun:"provider_source_id"`
			ProviderID uuid.UUID       `bun:"provider_id"`
			Identity   string          `bun:"provider_configuration_identity"`
			CacheKey   string          `bun:"provider_cache_key"`
			RiverJobID sql.NullInt64   `bun:"river_job_id"`
		}
		if err := fixture.db.NewRaw(`SELECT operation.kind,operation.state,operation.input_snapshot,operation.provider_source_id,source.provider_id,operation.provider_configuration_identity,operation.provider_cache_key,operation.river_job_id FROM operation JOIN provider_source source ON source.id=operation.provider_source_id WHERE operation.id=?`, lookup.RequestID).Scan(ctx, &operation); err != nil {
			t.Fatalf("read prepared lookup %s: %v", lookup.RequestID, err)
		}
		var intent struct {
			MBID string `json:"mbid"`
		}
		if err := json.Unmarshal(operation.Input, &intent); err != nil || operation.Kind != "provider_release_lookup" || operation.State != "queued" || intent.MBID != wantIDs[index] || operation.SourceID != source.ID || operation.ProviderID != source.ProviderID || operation.Identity != identity || operation.CacheKey != lookup.CacheKey || !operation.RiverJobID.Valid {
			t.Fatalf("prepared lookup operation[%d]=%+v intent=%+v err=%v", index, operation, intent, err)
		}
		var jobArgs string
		var job struct {
			OperationID uuid.UUID `json:"operation_id"`
		}
		if err := fixture.db.NewRaw(`SELECT args::text FROM river_job WHERE id=?`, operation.RiverJobID.Int64).Scan(ctx, &jobArgs); err != nil {
			t.Fatalf("read lookup job args: %v", err)
		}
		if err := json.Unmarshal([]byte(jobArgs), &job); err != nil || job.OperationID != lookup.RequestID {
			t.Fatalf("lookup job args=%s decoded=%+v err=%v", jobArgs, job, err)
		}
	}
	var consumed, unconsumed int
	if err := fixture.db.NewRaw(`SELECT count(*) FILTER (WHERE consumed_at IS NOT NULL),count(*) FILTER (WHERE consumed_at IS NULL) FROM matching_delivery WHERE run_id=?`, runID).Scan(ctx, &consumed, &unconsumed); err != nil || consumed != 1 {
		t.Fatalf("delivery ledger after lookup admission consumed=%d unconsumed=%d err=%v", consumed, unconsumed, err)
	}
	var operationsBefore, jobsBefore int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM operation WHERE id=? OR id IN (SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=?)`, runID, runID).Scan(ctx, &operationsBefore); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job job JOIN operation op ON op.id=(job.args->>'operation_id')::uuid WHERE op.id=? OR op.id IN (SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=?)`, runID, runID).Scan(ctx, &jobsBefore); err != nil {
		t.Fatal(err)
	}
	changed, handled, staleErr := acquisition.Advance(ctx, runID, 1, wakeID, claim)
	if staleErr != nil || changed || !handled {
		t.Fatalf("duplicate consumed delivery advance: changed=%v handled=%v err=%v", changed, handled, staleErr)
	}
	var operationsAfter, jobsAfter, deliveriesAfter, unconsumedAfter int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM operation WHERE id=? OR id IN (SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=?)`, runID, runID).Scan(ctx, &operationsAfter); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job job JOIN operation op ON op.id=(job.args->>'operation_id')::uuid WHERE op.id=? OR op.id IN (SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=?)`, runID, runID).Scan(ctx, &jobsAfter); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*),count(*) FILTER (WHERE consumed_at IS NULL) FROM matching_delivery WHERE run_id=?`, runID).Scan(ctx, &deliveriesAfter, &unconsumedAfter); err != nil {
		t.Fatal(err)
	}
	if operationsAfter != operationsBefore || jobsAfter != jobsBefore || deliveriesAfter != consumed+unconsumed || unconsumedAfter != unconsumed {
		t.Fatalf("duplicate delivery changed durable work: operations %d/%d jobs %d/%d deliveries %d/%d unconsumed=%d", operationsBefore, operationsAfter, jobsBefore, jobsAfter, consumed+unconsumed, deliveriesAfter, unconsumedAfter)
	}
}
