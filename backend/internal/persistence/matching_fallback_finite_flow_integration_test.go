//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/uptrace/bun"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type uniquePublicationApplyFixture struct {
	Context           context.Context
	Database          *bun.DB
	Repository        *persistence.SetupManagerRepository
	ParentOperationID uuid.UUID
	ChildOperation    persistence.Operation
	Source            persistence.MatchingApplySnapshotSourceV2
	InputSnapshot     persistence.MatchingApplyInputSnapshotV2
}

func testAutomaticFallbackFiniteFlowWithPublicationCallback(t *testing.T, historicalStageLookup, emptyPlan, exhaustPlan, publishReview bool, afterPublication func(uniquePublicationApplyFixture), publishUnique ...bool) {
	testAutomaticFallbackFiniteFlowWithRecoveryCallback(t, historicalStageLookup, emptyPlan, exhaustPlan, publishReview, afterPublication, nil, publishUnique...)
}

func testAutomaticFallbackFiniteFlowWithRecoveryCallback(t *testing.T, historicalStageLookup, emptyPlan, exhaustPlan, publishReview bool, afterPublication func(uniquePublicationApplyFixture), beforePublication func(*testing.T, context.Context, *bun.DB, *persistence.SetupManagerRepository, *fallbackHandoffRiverInserter, uuid.UUID, int64, *jobs.MatchingEvaluationWorker), publishUnique ...bool) {
	testAutomaticFallbackFiniteFlowWithDatabase(t, historicalStageLookup, emptyPlan, exhaustPlan, publishReview, afterPublication, beforePublication, nil, nil, false, publishUnique...)
}

func testAutomaticFallbackFiniteFlowWithDatabase(t *testing.T, _, _ bool, _ bool, publishReview bool, afterPublication func(uniquePublicationApplyFixture), beforePublication func(*testing.T, context.Context, *bun.DB, *persistence.SetupManagerRepository, *fallbackHandoffRiverInserter, uuid.UUID, int64, *jobs.MatchingEvaluationWorker), database *bun.DB, _ func(*testing.T, context.Context, *bun.DB, uuid.UUID, int64, int64) bool, requestBoundAdmission bool, publishUnique ...bool) {
	if len(publishUnique) < 3 || publishUnique[2] {
		t.Parallel()
	}
	uniquePublication := len(publishUnique) > 0 && publishUnique[0]
	fixture := newAutomaticMatchingTargetFenceFixtureWithDatabase(t, database)
	ctx, cancel := context.WithTimeout(fixture.ctx, 60*time.Second)
	defer cancel()

	metadata := `{"ALBUM":["Captured Album"],"TITLE":["Receipt Track"]}`
	if _, err := fixture.db.NewRaw(`UPDATE media_metadata_result SET observed_tags=?::jsonb`, metadata).Exec(ctx); err != nil {
		t.Fatalf("set fallback metadata: %v", err)
	}
	if uniquePublication {
		// Keep the two actual source captures distinguishable so the accepted
		// provider graph must prove a one-to-one assignment rather than a tie.
		if _, err := fixture.db.NewRaw(`WITH ordered AS (
			SELECT id,row_number() OVER (ORDER BY created_at,id) AS ordinal FROM media_metadata_result
		) UPDATE media_metadata_result metadata SET observed_tags=jsonb_build_object(
			'ALBUM',jsonb_build_array('Captured Album'),
			'TITLE',jsonb_build_array(CASE ordered.ordinal WHEN 1 THEN 'Track Override Alpha' ELSE 'Track Override Beta' END),
			'DURATION',jsonb_build_array('180')) FROM ordered WHERE metadata.id=ordered.id`).Exec(ctx); err != nil {
			t.Fatalf("set distinct source metadata for unique publication: %v", err)
		}
	}
	grouping := persistence.NewIncomingGroupingRepository(fixture.db)
	if err := service.NewIncomingGroups(grouping).Refresh(ctx); err != nil {
		t.Fatalf("refresh incoming groups: %v", err)
	}
	groups, err := grouping.List(ctx)
	if err != nil || len(groups) != 1 {
		t.Fatalf("read fallback group: count=%d err=%v", len(groups), err)
	}
	var encodedTags []string
	if err := fixture.db.NewRaw(`SELECT observed_tags::text FROM media_metadata_result ORDER BY created_at,id`).Scan(ctx, &encodedTags); err != nil {
		t.Fatalf("read saved source metadata: %v", err)
	}
	tagSets := make([]map[string][]string, len(encodedTags))
	for i, encoded := range encodedTags {
		if err := json.Unmarshal([]byte(encoded), &tagSets[i]); err != nil {
			t.Fatalf("decode saved source metadata: %v", err)
		}
	}
	if uniquePublication {
		captures, err := grouping.CurrentCaptures(ctx)
		if err != nil {
			t.Fatalf("read unique publication source captures: %v", err)
		}
		byVariant := make(map[uuid.UUID]map[string][]string, len(captures))
		for _, capture := range captures {
			var tags map[string][]string
			if err := json.Unmarshal(capture.Tags, &tags); err != nil {
				t.Fatalf("decode unique publication capture tags: %v", err)
			}
			byVariant[capture.VariantID] = tags
		}
		tagSets = make([]map[string][]string, 0, len(groups[0].Members))
		for _, memberID := range groups[0].Members {
			tags, exists := byVariant[memberID]
			if !exists {
				t.Fatalf("unique publication member %s has no source capture", memberID)
			}
			tagSets = append(tagSets, tags)
		}
	}
	planned, artistBearing := policy.PlanFallbackQueries(tagSets)
	if artistBearing || len(planned.Stages) != 3 || planned.Stages[0].Kind != "album_track" || planned.Stages[1].Kind != "album" {
		t.Fatalf("unexpected approved fallback plan: %+v artistBearing=%v", planned, artistBearing)
	}
	plan := policy.TypedFallbackQueryPlan{SchemaVersion: planned.SchemaVersion, Stages: planned.Stages}

	var event persistence.IncomingMatchingReconcileEvent
	var eventEpoch int64
	if !requestBoundAdmission {
		if err := fixture.db.NewSelect().Model(&event).Where("state = ? AND delivery_claimed", "dispatched").Order("created_at DESC").Limit(1).Scan(ctx); err != nil {
			t.Fatalf("read claimed reconciliation event: %v", err)
		}
		if err := fixture.db.NewRaw(`SELECT delivery_epoch FROM incoming_matching_reconcile_event WHERE id=?`, event.ID).Scan(ctx, &eventEpoch); err != nil {
			t.Fatalf("read reconciliation epoch: %v", err)
		}
	}

	const releaseID = "90000000-0000-4000-8000-0000000000f1"
	const trackID = "90000000-0000-4000-8000-0000000000f2"
	const recordingID = "90000000-0000-4000-8000-0000000000f3"
	var uniqueLookupHTTPPayload []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if uniquePublication && r.URL.Path == "/ws/2/release/"+releaseID {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(uniqueLookupHTTPPayload)
			return
		}
		http.Error(w, fmt.Sprintf("unexpected provider request: %s", r.URL), http.StatusBadGateway)
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
	inserter := &fallbackHandoffRiverInserter{}
	operations := service.NewOperationsWithRiver(fixture.repository, inserter)
	factory := service.NewProviderFactory(registry, nil, nil, server.Client(), nil)
	fetch := service.NewProviderFetchService(registry, factory, cache).WithDurableOperations(fixture.repository, inserter)
	providerWorker := jobs.NewProviderFetchWorker(fetch, operations)
	acquisition := service.NewMatchingAcquisitionService(fixture.repository, fetch, inserter)
	matchingWorker := jobs.NewMatchingEvaluationWorker(fixture.repository,
		service.NewMatchingEvaluationService(service.NewIncomingGroups(grouping), grouping, cache,
			persistence.NewIncomingMatchCandidateRepository(fixture.db), registry), operations).WithAcquisition(acquisition)
	client, listenerPool, err := jobs.PrepareWithWorkers(ctx, testpostgres.URL(t, fixture.db), fixture.db.DB, func(workers *river.Workers) {
		river.AddWorker(workers, providerWorker)
		river.AddWorker(workers, matchingWorker)
		river.AddWorker(workers, jobs.NewMatchingApplyWorker(fixture.repository, operations))
	})
	if err != nil {
		t.Fatalf("prepare River client: %v", err)
	}
	t.Cleanup(listenerPool.Close)
	inserter.client = client
	fixture.repository.SetRiverInserter(client)
	var requestGeneration int64
	if requestBoundAdmission {
		inventory := persistence.NewSourceInventoryRepository(fixture.db)
		if err := fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return persistence.RequestIncomingMatchingReconciliationWith(ctx, tx, []uuid.UUID{groups[0].ID})
		}); err != nil {
			t.Fatalf("request initial fallback generation: %v", err)
		}
		// This fixture drives reconciliation claims explicitly rather than running
		// a reconciliation consumer in the provider/matching worker bundle.
		if err := inventory.DispatchPendingMatchingReconciliations(ctx, openScanEnqueueRiver(t, fixture.db)); err != nil {
			t.Fatalf("dispatch initial fallback reconciliation: %v", err)
		}
		if err := fixture.db.NewSelect().Model(&event).Where("reason = ?", "current").Where("state = ?", "dispatched").Order("created_at DESC").Order("id DESC").Limit(1).Scan(ctx); err != nil {
			t.Fatalf("read initial dispatched fallback reconciliation: %v", err)
		}
		if event.RegisteredJobID == nil {
			t.Fatal("initial fallback reconciliation has no registered River delivery")
		}
		event.HighestDeliveryAttempt = 1
		var claimed bool
		eventEpoch, claimed, err = inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *event.RegisteredJobID, event.HighestDeliveryAttempt)
		if err != nil || !claimed {
			t.Fatalf("claim initial fallback reconciliation: epoch=%d claimed=%v err=%v", eventEpoch, claimed, err)
		}
		if err := fixture.db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, groups[0].ID).Scan(ctx, &requestGeneration); err != nil {
			t.Fatalf("read initial fallback request generation: %v", err)
		}
		if requestGeneration < 1 {
			t.Fatalf("initial fallback request generation=%d, want positive provenance", requestGeneration)
		}
	}

	var stageZero *policy.TypedFallbackQueryStage
	var stageZeroKey string
	var stageZeroID uuid.UUID
	var stageZeroOperation *persistence.Operation
	stageZeroPayload := json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":0,"offset":0,"recordings":[]}`)
	stage := plan.Stages[0]
	stageZero = &stage
	providerKind, prefix, ok := policy.ProviderKindForFallbackStage(stage.Kind)
	if !ok {
		t.Fatalf("no provider mapping for stage zero %q", stage.Kind)
	}
	stageZeroKey = fmt.Sprintf("%s:%d:%d:%s", prefix, stage.Offset, stage.Limit, stage.Query)
	stageZeroTicket, err := cache.BeginRefresh(ctx, source.ID, stageZeroKey, config.Identity)
	if err != nil {
		t.Fatalf("reserve stage-zero cache response: %v", err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: stageZeroTicket, FetchedAt: time.Now().UTC(), Payload: stageZeroPayload}); err != nil {
		t.Fatalf("accept empty stage-zero response: %v", err)
	}
	stageZeroInput, err := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}{1, stageZero.Query, stageZero.Offset, stageZero.Limit})
	if err != nil {
		t.Fatalf("encode stage-zero input: %v", err)
	}
	stageZeroID = uuid.New()
	providerID, sourceID, identity := source.ProviderID, source.ID, config.Identity
	stageZeroOperation = &persistence.Operation{ID: stageZeroID, Kind: providerKind, State: "queued", Stage: "queued", InputSnapshot: stageZeroInput,
		ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderConfigurationIdentity: &identity,
		ProviderEndpoint: source.Endpoint, ProviderCacheKey: &stageZeroKey, Attempt: 1}
	parentID := uuid.New()
	var provider persistence.MatchingProviderRequest
	if stageZeroOperation != nil {
		provider = persistence.MatchingProviderRequest{Operation: stageZeroOperation, Args: service.ProviderFetchOperationArgs{OperationID: stageZeroID}}
	}
	admitted, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(ctx, client, persistence.AutomaticFallbackMatchingAdmission{
		Parent:     &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1},
		ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Provider:   provider,
		Source:     persistence.AutomaticFallbackProviderSource{ID: source.ID, ProviderID: source.ProviderID, Endpoint: source.Endpoint, ConfigurationIdentity: config.Identity},
		GroupID:    groups[0].ID, ExpectedGroupRevision: groups[0].Revision, PolicyVersion: service.MatchingPolicyVersion,
		Plan: plan, EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: event.HighestDeliveryAttempt, EventDeliveryEpoch: eventEpoch,
		RequestGeneration: requestGeneration,
	})
	if err != nil {
		t.Fatalf("admit automatic fallback run: %v", err)
	}
	parentID = admitted.OriginalRunID
	if stageZeroOperation == nil {
		t.Fatal("nonempty fallback plan did not register stage zero")
	}
	stageZeroOperation, err = fixture.repository.GetOperation(ctx, stageZeroID)
	if err != nil || stageZeroOperation.RiverJobID == nil {
		t.Fatalf("read registered stage-zero provider operation: operation=%+v err=%v", stageZeroOperation, err)
	}
	if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: *stageZeroOperation.RiverJobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: stageZeroID}}); err != nil {
		t.Fatalf("run real stage-zero provider worker: %v", err)
	}
	stageZeroReceipt, err := fixture.repository.GetMatchingFallbackSearchReceipt(ctx, parentID, stageZeroID)
	if err != nil || stageZeroReceipt == nil || stageZeroReceipt.StageIndex != 0 || !jsonEqual(stageZeroReceipt.Payload, stageZeroPayload) {
		t.Fatalf("read real stage-zero receipt: receipt=%+v err=%v", stageZeroReceipt, err)
	}

	claimWake := func() (int64, persistence.MatchingDeliveryClaim) {
		t.Helper()
		var wakeID int64
		if err := fixture.db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL ORDER BY river_job_id DESC LIMIT 1`, parentID).Scan(ctx, &wakeID); err != nil {
			t.Fatalf("read registered parent wake: %v", err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, wakeID).Exec(ctx); err != nil {
			t.Fatalf("mark parent wake running: %v", err)
		}
		claim, outcome, err := fixture.repository.ClaimMatchingDelivery(ctx, parentID, 1, wakeID, 1)
		if err != nil || outcome != persistence.MatchingDeliveryClaimed {
			t.Fatalf("claim parent wake: claim=%+v outcome=%v err=%v", claim, outcome, err)
		}
		return wakeID, claim
	}
	advance := func(wakeID int64, claim persistence.MatchingDeliveryClaim) {
		t.Helper()
		changed, handled, err := acquisition.Advance(ctx, parentID, 1, wakeID, claim)
		if err != nil || !handled || !changed {
			t.Fatalf("advance service pipeline: changed=%v handled=%v err=%v", changed, handled, err)
		}
	}
	wakeID, claim := claimWake()
	advance(wakeID, claim)
	wakeID, claim = claimWake()
	advance(wakeID, claim)

	stageOneSnapshot, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || stageOneSnapshot.Phase != "fallback_search" {
		t.Fatalf("stage-zero calculation did not hand off to stage one: snapshot=%+v exists=%v err=%v", stageOneSnapshot, exists, err)
	}
	var handoff struct {
		StageIndex int       `json:"stage_index"`
		RequestID  uuid.UUID `json:"request_id"`
	}
	if err := json.Unmarshal(stageOneSnapshot.Input, &handoff); err != nil {
		t.Fatalf("decode stage-one search phase input: %v", err)
	}
	var output struct {
		RequestID uuid.UUID `json:"request_id"`
	}
	if err := json.Unmarshal(stageOneSnapshot.Output, &output); err != nil {
		t.Fatalf("decode stage-one search phase output: %v", err)
	}
	handoff.RequestID = output.RequestID
	if handoff.StageIndex != 1 || handoff.RequestID == uuid.Nil {
		t.Fatalf("stage-one search was not service-registered: input=%s output=%s handoff=%+v", stageOneSnapshot.Input, stageOneSnapshot.Output, handoff)
	}
	stageOneOperation, err := fixture.repository.GetOperation(ctx, handoff.RequestID)
	if err != nil || stageOneOperation.RiverJobID == nil || stageOneOperation.Kind != "provider_release_search" {
		t.Fatalf("read actual stage-one registration: operation=%+v err=%v", stageOneOperation, err)
	}
	var stageOneRequest struct {
		Query  string `json:"query"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(stageOneOperation.InputSnapshot, &stageOneRequest); err != nil {
		t.Fatalf("decode stage-one operation input: %v", err)
	}
	stageOnePayload := json.RawMessage(fmt.Sprintf(`{"created":"2026-10-12T00:00:00Z","count":1,"offset":0,"releases":[{"id":%q,"title":"Captured Album"}]}`, releaseID))
	stageOneKey := *stageOneOperation.ProviderCacheKey
	stageOneTicket, err := cache.BeginRefresh(ctx, source.ID, stageOneKey, config.Identity)
	if err != nil {
		t.Fatalf("reserve stage-one response: %v", err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: stageOneTicket, FetchedAt: time.Now().UTC(), Payload: stageOnePayload}); err != nil {
		t.Fatalf("accept stage-one response: %v", err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *stageOneOperation.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("mark registered stage-one provider job running: %v", err)
	}
	if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: *stageOneOperation.RiverJobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: handoff.RequestID}}); err != nil {
		t.Fatalf("run real stage-one provider worker: %v", err)
	}
	stageOneReceipt, err := fixture.repository.GetMatchingFallbackSearchReceipt(ctx, parentID, handoff.RequestID)
	if err != nil || stageOneReceipt == nil || stageOneReceipt.StageIndex != 1 || stageOneReceipt.RequestID == stageZeroReceipt.RequestID {
		t.Fatalf("read actual stage-one receipt: receipt=%+v err=%v", stageOneReceipt, err)
	}
	wakeID, claim = claimWake()
	advance(wakeID, claim)

	hydration, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || hydration.Phase != persistence.MatchingAcquisitionFallbackHydration {
		t.Fatalf("stage-one search did not register lookup hydration: snapshot=%+v exists=%v err=%v", hydration, exists, err)
	}
	var hydrationInput struct {
		StageIndex int `json:"stage_index"`
	}
	var hydrationOutput struct {
		StageIndex int `json:"stage_index"`
		Lookups    []struct {
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"lookups"`
	}
	if err := json.Unmarshal(hydration.Input, &hydrationInput); err != nil {
		t.Fatalf("decode stage-one hydration input: %v", err)
	}
	if err := json.Unmarshal(hydration.Output, &hydrationOutput); err != nil || hydrationInput.StageIndex != 1 || hydrationOutput.StageIndex != 1 || len(hydrationOutput.Lookups) != 1 {
		t.Fatalf("expected stage-one registered lookup: input=%s output=%s manifest=%+v err=%v", hydration.Input, hydration.Output, hydrationOutput, err)
	}
	var lookupRegistrationStage int
	if err := fixture.db.NewRaw(`SELECT stage_index FROM matching_fallback_request_registration WHERE run_id=? AND request_id=? AND role='lookup'`, parentID, hydrationOutput.Lookups[0].RequestID).Scan(ctx, &lookupRegistrationStage); err != nil || lookupRegistrationStage != 1 {
		t.Fatalf("stage-one lookup registration has wrong stage: stage=%d err=%v", lookupRegistrationStage, err)
	}
	lookup := hydrationOutput.Lookups[0]
	lookupOperation, err := fixture.repository.GetOperation(ctx, lookup.RequestID)
	if err != nil || lookupOperation.RiverJobID == nil {
		t.Fatalf("read registered release lookup: operation=%+v err=%v", lookupOperation, err)
	}
	lookupPayload := json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Captured Album","artist-credit":[{"name":"Artist","artist":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Artist"}}],"media":[{"position":1,"tracks":[{"id":%q,"number":"1","length":180000,"recording":{"id":%q,"title":"Receipt Track","artist-credit":[{"name":"Artist","artist":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Artist"}}]}}]}]}`, releaseID, trackID, recordingID))
	if uniquePublication {
		lookupPayload = json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Captured Album","artist-credit":[{"name":"Release Artist","artist":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Release Artist"}}],"media":[{"position":1,"tracks":[{"id":"90000000-0000-4000-8000-0000000000fa","number":"1","title":"Track Override Alpha","artist-credit":[{"name":"Track Artist","artist":{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","name":"Track Artist"}}],"length":180000,"recording":{"id":"90000000-0000-4000-8000-0000000000fb","title":"Original Recording Alpha","artist-credit":[{"name":"Recording Artist","artist":{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"Recording Artist"}}]}},{"id":"90000000-0000-4000-8000-0000000000fc","number":"2","title":"Track Override Beta","artist-credit":[{"name":"Track Artist","artist":{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","name":"Track Artist"}}],"length":180000,"recording":{"id":"90000000-0000-4000-8000-0000000000fd","title":"Original Recording Beta","artist-credit":[{"name":"Recording Artist","artist":{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"Recording Artist"}}]}}]}]}`, releaseID))
		uniqueLookupHTTPPayload = lookupPayload
	}
	if !uniquePublication {
		lookupGraph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: lookupPayload}})
		if err != nil {
			t.Fatalf("normalize accepted release lookup response: %v", err)
		}
		lookupTicket, err := cache.BeginRefresh(ctx, source.ID, lookup.CacheKey, config.Identity)
		if err != nil {
			t.Fatalf("reserve release lookup response: %v", err)
		}
		if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: lookupTicket, FetchedAt: time.Now().UTC(), Payload: lookupPayload, Graph: lookupGraph}); err != nil {
			t.Fatalf("accept release lookup response: %v", err)
		}
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *lookupOperation.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("mark registered lookup job running: %v", err)
	}
	if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: *lookupOperation.RiverJobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: lookup.RequestID}}); err != nil {
		t.Fatalf("run real release lookup worker: %v", err)
	}
	lookupOperation, err = fixture.repository.GetOperation(ctx, lookup.RequestID)
	if err != nil || lookupOperation.State != "succeeded" {
		t.Fatalf("real release lookup operation did not succeed: operation=%+v err=%v", lookupOperation, err)
	}
	lookupReceipt, err := fixture.repository.GetMatchingFallbackLookupReceipt(ctx, parentID, lookup.RequestID)
	if err != nil || lookupReceipt == nil {
		t.Fatalf("read real lookup receipt: receipt=%+v err=%v", lookupReceipt, err)
	}
	var totalLookupDependencies, stageOneLookupDependencies int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency dependency
		JOIN operation ON operation.id=dependency.request_id WHERE dependency.parent_run_id=? AND operation.kind='provider_release_lookup'`, parentID).
		Scan(ctx, &totalLookupDependencies); err != nil {
		t.Fatalf("count run-wide release lookup dependencies: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency dependency
		JOIN matching_fallback_request_registration registration ON registration.run_id=dependency.parent_run_id
			AND registration.request_id=dependency.request_id AND registration.stage_index=1 AND registration.role='lookup'
		JOIN operation ON operation.id=dependency.request_id WHERE dependency.parent_run_id=? AND operation.kind='provider_release_lookup'`, parentID).
		Scan(ctx, &stageOneLookupDependencies); err != nil {
		t.Fatalf("count stage-one release lookup dependencies: %v", err)
	}

	wakeID, claim = claimWake()
	advance(wakeID, claim)

	applicability, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || applicability.Phase != persistence.MatchingAcquisitionFallbackApplicability {
		t.Fatalf("lookup receipt did not finish hydration: snapshot=%+v exists=%v err=%v", applicability, exists, err)
	}
	wakeID, claim = claimWake()
	advance(wakeID, claim)
	final, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || final.Phase != persistence.MatchingAcquisitionFallbackEvaluationSealed {
		t.Fatalf("real fallback calculation did not continue qualified applicability: snapshot=%+v exists=%v err=%v", final, exists, err)
	}
	waitRevision := final.Revision - 1
	if currentWaitRevision, found, err := fixture.repository.GetCurrentMatchingFallbackEvaluationWaitRevision(ctx, parentID); err != nil || !found || currentWaitRevision != waitRevision {
		t.Fatalf("current sealed wait revision=%d found=%v, want %d: %v", currentWaitRevision, found, waitRevision, err)
	}
	var receipt struct {
		SchemaVersion     int             `json:"schema_version"`
		CalculatorVersion int             `json:"calculator_version"`
		DigestVersion     int             `json:"digest_version"`
		StageIndex        int             `json:"stage_index"`
		InputDigest       string          `json:"input_digest"`
		Edges             json.RawMessage `json:"edge_calculations"`
	}
	if err := fixture.db.NewRaw(`SELECT schema_version,calculator_version,digest_version,stage_index,encode(input_digest,'hex'),edge_calculations
		FROM matching_fallback_calculation_receipt WHERE run_id=? AND acquisition_revision=?`, parentID, applicability.Revision).
		Scan(ctx, &receipt.SchemaVersion, &receipt.CalculatorVersion, &receipt.DigestVersion, &receipt.StageIndex, &receipt.InputDigest, &receipt.Edges); err != nil {
		t.Fatalf("read persisted stage-one calculation receipt: %v", err)
	}
	var edges []struct {
		MemberID        uuid.UUID `json:"member_id"`
		WorkID          uuid.UUID `json:"work_id"`
		ProviderTrackID uuid.UUID `json:"provider_track_id"`
		RecordingID     string    `json:"recording_id"`
		LookupRequestID uuid.UUID `json:"lookup_request_id"`
		Confidence      float64   `json:"confidence"`
	}
	if receipt.SchemaVersion != 3 || receipt.CalculatorVersion != 3 || receipt.DigestVersion != 3 || receipt.StageIndex != 1 || receipt.InputDigest == "" ||
		json.Unmarshal(receipt.Edges, &edges) != nil || len(edges) == 0 {
		t.Fatalf("persisted calculation did not prove genuine qualified stage-one edge: receipt=%+v edges=%+v", receipt, edges)
	}
	memberRecording := make(map[uuid.UUID]string, len(final.Intent.CapturedMembers))
	for _, member := range final.Intent.CapturedMembers {
		var tags map[string][]string
		if err := json.Unmarshal(member.Tags, &tags); err != nil || len(tags["TITLE"]) != 1 {
			t.Fatalf("decode distinct captured source title for qualified-edge verification: member=%+v tags=%v err=%v", member, tags, err)
		}
		if uniquePublication {
			switch tags["TITLE"][0] {
			case "Track Override Alpha":
				memberRecording[member.MemberID] = "90000000-0000-4000-8000-0000000000fb"
			case "Track Override Beta":
				memberRecording[member.MemberID] = "90000000-0000-4000-8000-0000000000fd"
			default:
				t.Fatalf("unexpected captured source title in unique qualified-edge verification: %q", tags["TITLE"][0])
			}
		} else {
			memberRecording[member.MemberID] = ""
		}
	}
	tracks := make(map[string]uuid.UUID)
	qualifiedPerMember := make(map[uuid.UUID]float64, len(memberRecording))
	seenPairs := make(map[string]bool, len(edges))
	for _, edge := range edges {
		_, memberExists := memberRecording[edge.MemberID]
		identity := edge.MemberID.String() + "/" + edge.ProviderTrackID.String()
		if !memberExists || edge.WorkID == uuid.Nil || edge.ProviderTrackID == uuid.Nil || edge.RecordingID == "" || edge.LookupRequestID != lookup.RequestID || seenPairs[identity] {
			t.Fatalf("persisted Cartesian edge set is incomplete or out of scope: edge=%+v edges=%+v", edge, edges)
		}
		seenPairs[identity] = true
		if previous, exists := tracks[edge.RecordingID]; exists && previous != edge.ProviderTrackID {
			t.Fatalf("one accepted recording has multiple provider-track identities: recording=%s tracks=%v", edge.RecordingID, tracks)
		}
		tracks[edge.RecordingID] = edge.ProviderTrackID
		if edge.Confidence >= qualifiedPerMember[edge.MemberID] {
			qualifiedPerMember[edge.MemberID] = edge.Confidence
		}
	}
	if len(edges) != len(memberRecording)*len(tracks) || len(qualifiedPerMember) != len(memberRecording) {
		t.Fatalf("stage-one receipt omitted accepted member/track Cartesian edges: edges=%d members=%d tracks=%d qualified=%v", len(edges), len(memberRecording), len(tracks), qualifiedPerMember)
	}
	for memberID, confidence := range qualifiedPerMember {
		if confidence < 0.70 {
			t.Fatalf("captured file's best valid release-profile edge did not qualify: member=%s confidence=%.3f", memberID, confidence)
		}
	}
	if uniquePublication {
		seal, found, err := fixture.repository.GetMatchingFallbackAuthoritySeal(ctx, parentID, waitRevision)
		if err != nil || !found || len(seal.FullEvaluationContext) == 0 {
			t.Fatalf("read full sealed calculation context for unique inventory proof: found=%v err=%v", found, err)
		}
		var actualContext persistence.MatchingFallbackCalculationContext
		if err := json.Unmarshal(seal.FullEvaluationContext, &actualContext); err != nil || actualContext.RunID != parentID || actualContext.StageIndex != 1 ||
			actualContext.SearchReceipt.RequestID == uuid.Nil || len(actualContext.LookupReceipts) != 1 || actualContext.LookupReceipts[0].RequestID != lookup.RequestID {
			t.Fatalf("decode full current sealed context for fixed two-track universe: context=%+v err=%v", actualContext, err)
		}
		actualProviderSnapshot, err := cache.GetMusicBrainzMatchingSnapshot(ctx, source.Endpoint, config.Identity, []string{releaseID}, []string{lookup.CacheKey})
		if err != nil || len(actualProviderSnapshot.Releases) != 1 || actualProviderSnapshot.Releases[0].ProviderKey != releaseID || !actualProviderSnapshot.Releases[0].Complete || len(actualProviderSnapshot.Releases[0].Tracks) != 2 {
			t.Fatalf("read actual parsed provider release-track universe: snapshot=%+v err=%v", actualProviderSnapshot, err)
		}
		var sealedProviderSnapshot persistence.ProviderMatchingSnapshot
		if err := json.Unmarshal(actualContext.LookupReceipts[0].AcceptedProjection, &sealedProviderSnapshot); err != nil || len(sealedProviderSnapshot.Releases) != 1 || len(sealedProviderSnapshot.Releases[0].Tracks) != 2 ||
			sealedProviderSnapshot.Source.ID != source.ID || sealedProviderSnapshot.Source.ConfigurationIdentity != config.Identity {
			t.Fatalf("parse provider snapshot embedded in full sealed context: snapshot=%+v err=%v", sealedProviderSnapshot, err)
		}
		providerTracksByRecording := make(map[string]persistence.MatchingReleaseTrack, 2)
		for _, track := range actualProviderSnapshot.Releases[0].Tracks {
			providerTracksByRecording[strings.ToLower(track.RecordingProviderKey)] = track
		}
		sealedTracksByID := make(map[uuid.UUID]persistence.MatchingReleaseTrack, 2)
		for _, track := range sealedProviderSnapshot.Releases[0].Tracks {
			sealedTracksByID[track.ID] = track
		}
		for _, currentTrack := range actualProviderSnapshot.Releases[0].Tracks {
			sealedTrack, exists := sealedTracksByID[currentTrack.ID]
			if !exists || sealedTrack.Medium != currentTrack.Medium || sealedTrack.Position != currentTrack.Position ||
				!strings.EqualFold(sealedTrack.RecordingProviderKey, currentTrack.RecordingProviderKey) {
				t.Fatalf("actual provider release tracks differ from their complete frozen-context projection: current=%+v sealed=%+v", currentTrack, sealedTracksByID)
			}
		}
		alphaTrack, alphaExists := providerTracksByRecording["90000000-0000-4000-8000-0000000000fb"]
		betaTrack, betaExists := providerTracksByRecording["90000000-0000-4000-8000-0000000000fd"]
		if !alphaExists || !betaExists || alphaTrack.ID == uuid.Nil || betaTrack.ID == uuid.Nil || alphaTrack.ID == betaTrack.ID ||
			alphaTrack.Medium != 0 || alphaTrack.Position != 0 || betaTrack.Medium != 0 || betaTrack.Position != 1 ||
			alphaTrack.RecordingTitle != "Original Recording Alpha" || betaTrack.RecordingTitle != "Original Recording Beta" ||
			alphaTrack.RecordingLengthMS != nil || betaTrack.RecordingLengthMS != nil ||
			alphaTrack.ReleaseLengthMS == nil || betaTrack.ReleaseLengthMS == nil {
			t.Fatalf("parsed provider release tracks do not prove the fixed recording/track coordinate universe: tracks=%+v", actualProviderSnapshot.Releases[0].Tracks)
		}
		preparedUnique, exists, err := service.PrepareFrozenFallbackEvaluation(ctx, fixture.repository, parentID, waitRevision)
		if err != nil || !exists || preparedUnique.Calculation == nil || len(preparedUnique.Result.AllCandidates) != 4 {
			t.Fatalf("prepare pure frozen applicability result for full Cartesian proof: prepared=%+v exists=%v err=%v", preparedUnique, exists, err)
		}
		knownMemberRecording := make(map[uuid.UUID]string, 2)
		for _, member := range final.Intent.CapturedMembers {
			var tags map[string][]string
			if err := json.Unmarshal(member.Tags, &tags); err != nil || len(tags["TITLE"]) != 1 {
				t.Fatalf("decode sealed source title for complete universe assertion: member=%+v tags=%v err=%v", member, tags, err)
			}
			switch tags["TITLE"][0] {
			case "Track Override Alpha":
				knownMemberRecording[member.MemberID] = "90000000-0000-4000-8000-0000000000fb"
			case "Track Override Beta":
				knownMemberRecording[member.MemberID] = "90000000-0000-4000-8000-0000000000fd"
			default:
				t.Fatalf("unexpected sealed source title in full universe proof: %q", tags["TITLE"][0])
			}
		}
		if len(knownMemberRecording) != 2 {
			t.Fatalf("fixed provider universe does not have two known sealed members: %v", knownMemberRecording)
		}
		seenCompletePairs := make(map[string]bool, 4)
		crossBelowThreshold := make(map[uuid.UUID]bool, 2)
		releaseProfileMaximum := make(map[uuid.UUID]float64, 2)
		for _, candidate := range preparedUnique.Result.AllCandidates {
			intendedRecording, memberExists := knownMemberRecording[candidate.MemberID]
			track, trackExists := providerTracksByRecording[strings.ToLower(candidate.RecordingID)]
			pair := candidate.MemberID.String() + "/" + candidate.ProviderTrackUUID.String()
			if !memberExists || !trackExists || candidate.ProviderTrackUUID != track.ID || seenCompletePairs[pair] {
				t.Fatalf("pure applicability candidate is outside or duplicates the accepted complete member/track universe: candidate=%+v", candidate)
			}
			seenCompletePairs[pair] = true
			if candidate.Score.Profile != service.RecordingReleaseMatchingProfile {
				t.Fatalf("applicability Cartesian candidate did not use recording-release profile: candidate=%+v", candidate)
			}
			if !strings.EqualFold(candidate.RecordingID, intendedRecording) && candidate.Score.Confidence < 0.70 {
				crossBelowThreshold[candidate.MemberID] = true
			}
			if strings.EqualFold(candidate.RecordingID, intendedRecording) && candidate.Score.Confidence > releaseProfileMaximum[candidate.MemberID] {
				releaseProfileMaximum[candidate.MemberID] = candidate.Score.Confidence
			}
		}
		if len(seenCompletePairs) != 4 || len(crossBelowThreshold) != 2 || len(releaseProfileMaximum) != 2 {
			t.Fatalf("pure applicability result omitted the four complete distinct pairs or retained cross/release-profile evidence: pairs=%v cross=%v maxima=%v", seenCompletePairs, crossBelowThreshold, releaseProfileMaximum)
		}
		releaseSelectionMaximum := make(map[uuid.UUID]float64, 2)
		for _, releaseCandidate := range preparedUnique.KernelResult.ReleaseSelection.Candidates {
			for _, file := range releaseCandidate.Files {
				fileID, parseErr := uuid.Parse(file.FileID)
				_, known := knownMemberRecording[fileID]
				if parseErr != nil || !known || !file.Present {
					continue
				}
				if file.Confidence > releaseSelectionMaximum[fileID] {
					releaseSelectionMaximum[fileID] = file.Confidence
				}
			}
		}
		for memberID, confidence := range releaseProfileMaximum {
			if confidence < 0.70 {
				t.Fatalf("pure release-profile result has no qualifying intended edge: member=%s confidence=%.3f", memberID, confidence)
			}
			if releaseSelectionMaximum[memberID] < 0.70 {
				t.Fatalf("pure kernel release-selection profile has no qualifying per-file score: member=%s confidence=%.3f", memberID, releaseSelectionMaximum[memberID])
			}
		}
		for memberID, expectedRecording := range memberRecording {
			var diagonalQualified bool
			for _, edge := range edges {
				if edge.MemberID == memberID && edge.RecordingID == expectedRecording && edge.Confidence >= 0.70 {
					diagonalQualified = true
				}
			}
			if !diagonalQualified {
				t.Fatalf("captured file's intended accepted recording edge did not qualify: member=%s recording=%s edges=%+v", memberID, expectedRecording, edges)
			}
		}
	}
	unchangedStageZero, err := fixture.repository.GetMatchingFallbackSearchReceipt(ctx, parentID, stageZeroID)
	if err != nil || unchangedStageZero == nil || unchangedStageZero.StageIndex != 0 || !jsonEqual(unchangedStageZero.Payload, stageZeroReceipt.Payload) {
		t.Fatalf("stage-one handoffs changed initial stage-zero receipt: receipt=%+v err=%v", unchangedStageZero, err)
	}
	var inputCount, dependencyCount, registrationCount, searchJobCount, lookupJobCount int
	if err := fixture.db.NewRaw(`SELECT jsonb_array_length(intent->'initial_provider_inputs') FROM matching_acquisition WHERE run_id=?`, parentID).Scan(ctx, &inputCount); err != nil {
		t.Fatalf("read immutable initial provider inputs: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, parentID).Scan(ctx, &dependencyCount); err != nil {
		t.Fatalf("count provider dependencies: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?`, parentID).Scan(ctx, &registrationCount); err != nil {
		t.Fatalf("count request registrations: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind IN ('provider_release_search_v1','provider_recording_search_v1') AND args->>'operation_id' IN (SELECT request_id::text FROM matching_fallback_request_registration WHERE run_id=? AND stage_index=2)`, parentID).Scan(ctx, &searchJobCount); err != nil {
		t.Fatalf("count stage-two search jobs: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE args->>'operation_id' IN (SELECT request_id::text FROM matching_fallback_request_registration WHERE run_id=? AND role='lookup')`, parentID).Scan(ctx, &lookupJobCount); err != nil {
		t.Fatalf("count registered lookup jobs: %v", err)
	}
	expectedDependencies, expectedRegistrations, expectedLookupJobs := 3, 3, 1
	if inputCount != 1 || dependencyCount != expectedDependencies || registrationCount != expectedRegistrations || searchJobCount != 0 || lookupJobCount != expectedLookupJobs {
		t.Fatalf("finite flow wrote unexpected request/dependency work: initialInputs=%d dependencies=%d registrations=%d stage2Jobs=%d lookupJobs=%d", inputCount, dependencyCount, registrationCount, searchJobCount, lookupJobCount)
	}
	if beforePublication != nil {
		// Keep existing callers on the historical discarded-state behavior while
		// allowing the recovery matrix to exercise each terminal/missing wake.
		beforePublication(t, ctx, fixture.db, fixture.repository, inserter, parentID, waitRevision, matchingWorker)
	}

	if uniquePublication && beforePublication == nil {
		// The publication branch is exercised by the continuation test helper below;
		// don't let the legacy review-only helper silently stand in for a unique proof.
		assertFallbackUniquePublication(t, ctx, fixture, inserter, parentID, waitRevision, afterPublication)
	}
}

func assertFallbackUniquePublication(t *testing.T, ctx context.Context, fixture automaticTargetFenceFixture, inserter *fallbackHandoffRiverInserter, runID uuid.UUID, waitRevision int64, afterPublication func(uniquePublicationApplyFixture)) {
	t.Helper()
	handoff, found, err := fixture.repository.ContinueMatchingFallbackSeal(ctx, inserter, runID, 1, waitRevision)
	if err != nil || !found {
		t.Fatalf("continue unique fallback evaluation: handoff=%+v found=%v err=%v", handoff, found, err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, handoff.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("make unique continuation wake claimable: %v", err)
	}
	claim, outcome, err := fixture.repository.ClaimMatchingDelivery(ctx, runID, 1, handoff.RiverJobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim unique continuation wake: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}
	prepared, exists, err := service.PrepareFrozenFallbackEvaluation(ctx, fixture.repository, runID, waitRevision)
	if err != nil || !exists || prepared.Calculation == nil {
		t.Fatalf("prepare unique publication from sealed evidence: exists=%v calculation=%+v err=%v", exists, prepared.Calculation, err)
	}
	publication, err := service.SerializeFrozenFallbackEvaluation(prepared)
	if err != nil || publication.Evaluation.Outcome != "unique" || publication.Evaluation.Decision != "apply" ||
		len(publication.Evaluation.Assignments) != 2 || len(publication.Inventory.Members) != 2 || len(publication.Inventory.Releases) != 1 {
		t.Fatalf("frozen kernel did not derive a unique two-member assignment: evaluation=%+v kernel=%+v members=%d releases=%d err=%v", publication.Evaluation, prepared.KernelResult, len(publication.Inventory.Members), len(publication.Inventory.Releases), err)
	}
	assignments := make(map[uuid.UUID]uuid.UUID, len(publication.Evaluation.Assignments))
	tracks := make(map[uuid.UUID]bool, len(publication.Evaluation.Assignments))
	for _, assignment := range publication.Evaluation.Assignments {
		if assignment.FileMemberID == uuid.Nil || assignment.ProviderTrackID == uuid.Nil || tracks[assignment.ProviderTrackID] {
			t.Fatalf("kernel assignment is incomplete or reuses a provider variant: %+v", publication.Evaluation.Assignments)
		}
		assignments[assignment.FileMemberID] = assignment.ProviderTrackID
		tracks[assignment.ProviderTrackID] = true
	}
	if len(assignments) != 2 || len(tracks) != 2 {
		t.Fatalf("kernel assignments do not uniquely cover both captured members: assignments=%v tracks=%v", assignments, tracks)
	}

	groupID := publication.Inventory.GroupID
	noApplyRows := func(label string) {
		t.Helper()
		var requests, children, applyJobs int
		if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_apply_request WHERE parent_run_id=?`, runID).Scan(ctx, &requests); err != nil {
			t.Fatalf("%s read apply requests: %v", label, err)
		}
		if err := fixture.db.NewRaw(`SELECT count(*) FROM operation WHERE kind=?`, persistence.MatchingApplyOperationKind).Scan(ctx, &children); err != nil {
			t.Fatalf("%s read apply operations: %v", label, err)
		}
		if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='matching_apply_v1' AND args->>'operation_id' IN (SELECT id::text FROM operation WHERE kind=?)`, persistence.MatchingApplyOperationKind).Scan(ctx, &applyJobs); err != nil {
			t.Fatalf("%s read apply jobs: %v", label, err)
		}
		if requests != 0 || children != 0 || applyJobs != 0 {
			t.Fatalf("%s rejection left apply work: requests=%d children=%d jobs=%d", label, requests, children, applyJobs)
		}
	}
	reject := func(label string, candidate persistence.FrozenMatchingPublication, expected map[uuid.UUID]*string, contains string) {
		t.Helper()
		if changed, publishErr := fixture.repository.PublishFrozenMatchingEvaluationForDelivery(ctx, runID, 1, handoff.RiverJobID, claim, candidate, expected); publishErr == nil || changed {
			t.Fatalf("%s was accepted: changed=%v err=%v", label, changed, publishErr)
		} else if contains != "" && !strings.Contains(publishErr.Error(), contains) {
			t.Fatalf("%s error=%q does not contain %q", label, publishErr, contains)
		}
		noApplyRows(label)
	}
	duplicateFolderPosition := publication
	duplicateFolderPosition.Evaluation.Assignments = append([]persistence.FrozenMatchingAssignment(nil), publication.Evaluation.Assignments...)
	duplicateFolderPosition.Evaluation.Assignments[1].ProviderTrackID = duplicateFolderPosition.Evaluation.Assignments[0].ProviderTrackID
	duplicateFolderPosition.Evaluation.Assignments[1].Medium = duplicateFolderPosition.Evaluation.Assignments[0].Medium
	duplicateFolderPosition.Evaluation.Assignments[1].Position = duplicateFolderPosition.Evaluation.Assignments[0].Position
	var evaluationBefore string
	if err := fixture.db.NewRaw(`SELECT COALESCE((SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=?), '')`, groupID).Scan(ctx, &evaluationBefore); err != nil {
		t.Fatalf("read evaluation revision before folder-position rejection: %v", err)
	}
	reject("same provider position in one captured folder", duplicateFolderPosition, map[uuid.UUID]*string{groupID: nil}, "same source folder")
	var evaluationAfter string
	var deliveryConsumed bool
	if err := fixture.db.NewRaw(`SELECT COALESCE((SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=?), '')`, groupID).Scan(ctx, &evaluationAfter); err != nil {
		t.Fatalf("read evaluation revision after folder-position rejection: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, runID, handoff.RiverJobID).Scan(ctx, &deliveryConsumed); err != nil {
		t.Fatalf("read delivery after folder-position rejection: %v", err)
	}
	if evaluationBefore != evaluationAfter || deliveryConsumed {
		t.Fatalf("folder-position rejection changed evaluation or consumed delivery: before=%+v after=%+v consumed=%v", evaluationBefore, evaluationAfter, deliveryConsumed)
	}

	wrongDecision := publication
	wrongDecision.Evaluation.Decision = "review"
	reject("wrong unique decision", wrongDecision, map[uuid.UUID]*string{groupID: nil}, "unique outcome and apply decision must agree")
	foreignMember := publication
	foreignMember.Evaluation.Assignments = append([]persistence.FrozenMatchingAssignment(nil), publication.Evaluation.Assignments...)
	foreignMember.Evaluation.Assignments[0].FileMemberID = uuid.New()
	reject("foreign assignment member", foreignMember, map[uuid.UUID]*string{groupID: nil}, "assignment")
	partial := publication
	partial.Evaluation.Assignments = append([]persistence.FrozenMatchingAssignment(nil), publication.Evaluation.Assignments[:1]...)
	reject("partial assignment", partial, map[uuid.UUID]*string{groupID: nil}, "decision does not cover original group")
	wrongSeal := publication
	wrongSeal.Authority.SealDigest = strings.Repeat("0", len(wrongSeal.Authority.SealDigest))
	reject("wrong seal digest", wrongSeal, map[uuid.UUID]*string{groupID: nil}, "persisted seal digest")
	staleCAS := "stale-evaluation-revision"
	reject("stale evaluation CAS", publication, map[uuid.UUID]*string{groupID: &staleCAS}, "")

	changed, err := fixture.repository.PublishFrozenMatchingEvaluationForDelivery(ctx, runID, 1, handoff.RiverJobID, claim, publication, map[uuid.UUID]*string{groupID: nil})
	if err != nil || !changed {
		t.Fatalf("publish unique frozen fallback evaluation: changed=%v err=%v", changed, err)
	}
	var request struct {
		OperationID  uuid.UUID `bun:"apply_operation_id"`
		Schema       int       `bun:"schema_version"`
		ParentEpoch  int64     `bun:"parent_execution_epoch"`
		WaitRevision int64     `bun:"seal_wait_revision"`
		SealDigest   []byte    `bun:"seal_digest"`
	}
	if err := fixture.db.NewRaw(`SELECT apply_operation_id,schema_version,parent_execution_epoch,seal_wait_revision,seal_digest
		FROM matching_apply_request WHERE parent_run_id=?`, runID).Scan(ctx, &request); err != nil || request.OperationID == uuid.Nil || request.Schema != 2 ||
		request.ParentEpoch != claim.ExecutionEpoch || request.WaitRevision != waitRevision || fmt.Sprintf("%x", request.SealDigest) != handoff.SealDigest {
		t.Fatalf("schema-2 frozen apply request is not bound to the parent execution and seal: request=%+v handoff=%+v err=%v", request, handoff, err)
	}
	var child persistence.Operation
	if err := fixture.db.NewSelect().Model(&child).Where("id = ?", request.OperationID).Scan(ctx); err != nil || child.Kind != persistence.MatchingApplyOperationKind {
		t.Fatalf("read one registered apply child: child=%+v err=%v", child, err)
	}
	var rawArgs []byte
	var args struct {
		OperationID uuid.UUID `json:"operation_id"`
	}
	if err := fixture.db.NewRaw(`SELECT args FROM river_job WHERE id=? AND kind='matching_apply_v1'`, *child.RiverJobID).Scan(ctx, &rawArgs); err != nil || json.Unmarshal(rawArgs, &args) != nil || args.OperationID != child.ID {
		t.Fatalf("registered River child args are not ID-only: args=%+v child=%+v err=%v", args, child, err)
	}
	snapshot, err := persistence.DecodeMatchingApplyInputSnapshotV2(child.InputSnapshot)
	if err != nil || snapshot.SchemaVersion != 2 || snapshot.Authority.ParentRunID != runID || len(snapshot.Assignments) != 2 {
		t.Fatalf("decode registered schema-2 frozen snapshot: snapshot=%+v err=%v", snapshot, err)
	}
	for _, assignment := range snapshot.Assignments {
		if assignment.SourceSHA256 != "" {
			t.Fatalf("digestless source unexpectedly gained a captured digest: assignment=%+v", assignment)
		}
	}
	var requestCount, childCount int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_apply_request WHERE parent_run_id=?`, runID).Scan(ctx, &requestCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM operation WHERE kind=? AND id IN (SELECT apply_operation_id FROM matching_apply_request WHERE parent_run_id=?)`, persistence.MatchingApplyOperationKind, runID).Scan(ctx, &childCount); err != nil {
		t.Fatal(err)
	}
	if requestCount != 1 || childCount != 1 {
		t.Fatalf("unique publish was not atomic one-request/one-child: requests=%d children=%d", requestCount, childCount)
	}
	var operationState, runState string
	var consumed bool
	if err := fixture.db.NewRaw(`SELECT operation.state,run.state,delivery.consumed_at IS NOT NULL
		FROM operation JOIN matching_run run ON run.id=operation.id JOIN matching_delivery delivery ON delivery.run_id=run.id
		WHERE run.id=? AND delivery.river_job_id=?`, runID, handoff.RiverJobID).Scan(ctx, &operationState, &runState, &consumed); err != nil || operationState != "succeeded" || runState != "succeeded" || !consumed {
		t.Fatalf("unique publication did not settle its parent delivery: operation=%q run=%q consumed=%v err=%v", operationState, runState, consumed, err)
	}
	if afterPublication != nil {
		afterPublication(uniquePublicationApplyFixture{
			Context: ctx, Database: fixture.db, Repository: fixture.repository, ParentOperationID: runID,
			ChildOperation: child, Source: snapshot.Source, InputSnapshot: snapshot,
		})
	}
}
