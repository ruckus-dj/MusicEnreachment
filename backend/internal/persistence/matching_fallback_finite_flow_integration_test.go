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

func TestExhaustedFallbackHydratesAcceptedEditionsOutsideScoringShortlist(t *testing.T) {
	testAutomaticFallbackFiniteFlowWithPublicationCallback(t, false, false, false, true, nil)
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
	if publishReview {
		stageOnePayload = json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":0,"offset":0,"releases":[]}`)
	}
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

	if publishReview {
		next := func() {
			t.Helper()
			wake, delivery := claimWake()
			advance(wake, delivery)
		}
		runAccepted := func(requestID uuid.UUID, payload json.RawMessage, graph persistence.ProviderGraph) {
			t.Helper()
			request, err := fixture.repository.GetOperation(ctx, requestID)
			if err != nil || request.RiverJobID == nil || request.ProviderCacheKey == nil {
				t.Fatalf("read review test dependency: %v", err)
			}
			ticket, err := cache.BeginRefresh(ctx, source.ID, *request.ProviderCacheKey, config.Identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payload, Graph: graph}); err != nil {
				t.Fatal(err)
			}
			jobID := *request.RiverJobID
			if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: jobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: requestID}}); err != nil {
				t.Fatal(err)
			}
		}
		readPhase := func(want string) persistence.MatchingAcquisitionSnapshot {
			t.Helper()
			snapshot, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
			if err != nil || !exists || snapshot.Phase != want {
				t.Fatalf("fallback phase = %q, want %q: %v", snapshot.Phase, want, err)
			}
			return snapshot
		}
		next() // Stage one proved none; admit the last recording search.
		lastSearch := readPhase(persistence.MatchingAcquisitionFallbackSearch)
		if err := json.Unmarshal(lastSearch.Output, &output); err != nil {
			t.Fatal(err)
		}
		var hits []map[string]string
		for n := 1; n <= 11; n++ {
			hits = append(hits, map[string]string{"id": fmt.Sprintf("c0000000-0000-4000-8000-%012d", n), "title": "Unrelated Album"})
		}
		hitsJSON, err := json.Marshal(hits)
		if err != nil {
			t.Fatal(err)
		}
		runAccepted(output.RequestID, json.RawMessage(fmt.Sprintf(`{"count":1,"offset":0,"recordings":[{"id":%q,"title":"Unrelated Song","releases":%s}]}`, recordingID, hitsJSON)), persistence.ProviderGraph{})
		next() // Admit top-ten scoring lookups only.
		runLookups := func(expected int) {
			t.Helper()
			snapshot := readPhase(persistence.MatchingAcquisitionFallbackHydration)
			var manifest struct {
				Lookups []struct {
					RequestID uuid.UUID `json:"request_id"`
					ReleaseID string    `json:"release_id"`
				} `json:"lookups"`
			}
			if err := json.Unmarshal(snapshot.Output, &manifest); err != nil || len(manifest.Lookups) != expected {
				t.Fatalf("hydration coverage=%d, want %d: %v", len(manifest.Lookups), expected, err)
			}
			for _, lookup := range manifest.Lookups {
				payload := json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Unrelated Album","media":[{"position":1,"tracks":[{"id":%q,"number":"1","recording":{"id":%q,"title":"Unrelated Song"}}]}]}`, lookup.ReleaseID, uuid.New(), recordingID))
				graph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: payload}})
				if err != nil {
					t.Fatal(err)
				}
				runAccepted(lookup.RequestID, payload, graph)
			}
		}
		runLookups(10)
		next() // Finish scoring hydration.
		score := readPhase(persistence.MatchingAcquisitionFallbackApplicability)
		next() // Persist none, then admit only the remaining lookup.
		calculation, err := fixture.repository.GetMatchingFallbackCalculationReceipt(ctx, parentID, score.Revision)
		if err != nil || calculation == nil || calculation.LayoutDecision.Status != persistence.MatchingLayoutDecisionNone {
			t.Fatalf("extra lookup did not follow a proved none: %v", err)
		}
		runLookups(1)
		next()
		final := readPhase(persistence.MatchingAcquisitionFallbackEvaluationSealed)
		prepared, exists, err := service.PrepareFrozenFallbackEvaluation(ctx, fixture.repository, parentID, final.Revision-1)
		if err != nil || !exists || prepared.Authority.Disposition != "review_exhausted" || len(prepared.Releases) != 11 ||
			prepared.LayoutDecision == nil || prepared.LayoutDecision.Status != persistence.MatchingLayoutDecisionNone {
			t.Fatalf("exhausted review has %d releases, want all 11 last-stage releases: %v", len(prepared.Releases), err)
		}
		var wake int64
		if err := fixture.db.NewRaw(`SELECT current_river_job_id FROM matching_run WHERE id=?`, parentID).Scan(ctx, &wake); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, wake).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if err := matchingWorker.Work(ctx, &river.Job[service.MatchingEvaluationOperationArgs]{
			JobRow: &rivertype.JobRow{ID: wake, Attempt: 1},
			Args:   service.MatchingEvaluationOperationArgs{OperationID: parentID},
		}); err != nil {
			t.Fatalf("publish exhausted review: %v", err)
		}
		var published, applies int
		if err := fixture.db.NewRaw(`SELECT
			(SELECT jsonb_array_length(evaluation_evidence->'review_proposal'->'releases') FROM incoming_match_evaluation WHERE group_id=?),
			(SELECT count(*) FROM matching_apply_request WHERE parent_run_id=?)`, groups[0].ID, parentID).Scan(ctx, &published, &applies); err != nil ||
			published != 11 || applies != 0 {
			operation, _ := fixture.repository.GetOperation(ctx, parentID)
			t.Fatalf("full review publication: releases=%d apply requests=%d stage=%s err=%v", published, applies, operation.Stage, err)
		}
		// The test invokes workers directly. Finalize their deliveries as River
		// would; a queued older wake must not masquerade as an idle review.
		if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='completed',finalized_at=now()
			WHERE finalized_at IS NULL AND id IN (SELECT river_job_id FROM matching_delivery WHERE run_id=?)`, parentID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		var requested int64
		if err := fixture.db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, groups[0].ID).Scan(ctx, &requested); err != nil {
			t.Fatal(err)
		}
		if requested > 0 {
			if err := persistence.NewSourceInventoryRepository(fixture.db).AcknowledgeCurrentRequest(ctx, groups[0].ID, requested); err != nil {
				t.Fatal(err)
			}
		}
		assertPublishedFallbackReviewRefresh(t, ctx, fixture.db, fixture.repository, groups[0].ID, parentID)
		return
	}

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
	selectedReceipt, err := fixture.repository.GetMatchingFallbackCalculationReceipt(ctx, parentID, applicability.Revision)
	if err != nil || selectedReceipt == nil {
		t.Fatalf("read persisted stage-one layout decision: receipt=%+v err=%v", selectedReceipt, err)
	}
	if selectedReceipt.SchemaVersion != 4 || selectedReceipt.CalculatorVersion != 4 || selectedReceipt.DigestVersion != 4 ||
		selectedReceipt.StageIndex != 1 || selectedReceipt.InputDigest == "" {
		t.Fatalf("persisted calculation did not use the layout decision receipt: receipt=%+v", selectedReceipt)
	}
	if selectedReceipt.LayoutDecision.Status != persistence.MatchingLayoutDecisionSelected ||
		selectedReceipt.LayoutDecision.ProviderReleaseID == nil ||
		len(selectedReceipt.LayoutDecision.Assignments) != len(final.Intent.CapturedMembers) {
		t.Fatalf("persisted stage-one decision is not a complete selected layout: decision=%+v", selectedReceipt.LayoutDecision)
	}
	decisionDigest, err := selectedReceipt.LayoutDecision.Digest()
	if err != nil || decisionDigest != selectedReceipt.DecisionDigest {
		t.Fatalf("persisted decision digest=%q calculated=%q err=%v", selectedReceipt.DecisionDigest, decisionDigest, err)
	}
	accepted, err := cache.GetMusicBrainzMatchingSnapshot(ctx, source.Endpoint, config.Identity, []string{releaseID}, []string{lookup.CacheKey})
	if err != nil || len(accepted.Releases) != 1 || accepted.Releases[0].ID != *selectedReceipt.LayoutDecision.ProviderReleaseID {
		t.Fatalf("selected layout does not identify the accepted provider release: %v", err)
	}
	trackByRecording := make(map[string]uuid.UUID, len(accepted.Releases[0].Tracks))
	for _, track := range accepted.Releases[0].Tracks {
		trackByRecording[track.RecordingProviderKey] = track.ID
	}
	expectedTrackByMember := make(map[uuid.UUID]uuid.UUID, len(final.Intent.CapturedMembers))
	for _, member := range final.Intent.CapturedMembers {
		var tags map[string][]string
		if err := json.Unmarshal(member.Tags, &tags); err != nil || len(tags["TITLE"]) != 1 {
			t.Fatalf("decode captured source title for layout decision verification: member=%+v tags=%v err=%v", member, tags, err)
		}
		switch tags["TITLE"][0] {
		case "Track Override Alpha":
			expectedTrackByMember[member.MemberID] = trackByRecording["90000000-0000-4000-8000-0000000000fb"]
		case "Track Override Beta":
			expectedTrackByMember[member.MemberID] = trackByRecording["90000000-0000-4000-8000-0000000000fd"]
		case "Receipt Track":
			expectedTrackByMember[member.MemberID] = trackByRecording[recordingID]
		default:
			t.Fatalf("unexpected captured source title in layout decision verification: %q", tags["TITLE"][0])
		}
	}
	for _, assignment := range selectedReceipt.LayoutDecision.Assignments {
		if assignment.ProviderTrackID != expectedTrackByMember[assignment.FileMemberID] {
			t.Fatalf("decision assigned wrong provider track: member=%s got=%s want=%s", assignment.FileMemberID, assignment.ProviderTrackID, expectedTrackByMember[assignment.FileMemberID])
		}
	}
	var priorRevision int64
	if err := fixture.db.NewRaw(`SELECT acquisition_revision FROM matching_fallback_calculation_receipt
		WHERE run_id=? AND stage_index=0 ORDER BY acquisition_revision LIMIT 1`, parentID).Scan(ctx, &priorRevision); err != nil {
		t.Fatalf("read preceding proved-none revision: %v", err)
	}
	priorReceipt, err := fixture.repository.GetMatchingFallbackCalculationReceipt(ctx, parentID, priorRevision)
	if err != nil || priorReceipt == nil || priorReceipt.StageIndex != 0 ||
		priorReceipt.LayoutDecision.Status != persistence.MatchingLayoutDecisionNone ||
		priorReceipt.LayoutDecision.ProviderReleaseID != nil || len(priorReceipt.LayoutDecision.Assignments) != 0 {
		t.Fatalf("preceding stage did not retain a proved none decision: receipt=%+v err=%v", priorReceipt, err)
	}
	priorDigest, err := priorReceipt.LayoutDecision.Digest()
	if err != nil || priorDigest != priorReceipt.DecisionDigest {
		t.Fatalf("preceding proved-none digest=%q calculated=%q err=%v", priorReceipt.DecisionDigest, priorDigest, err)
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
		if err != nil || !exists || preparedUnique.Calculation == nil || preparedUnique.LayoutDecision == nil ||
			preparedUnique.LayoutDecision.Status != persistence.MatchingLayoutDecisionSelected ||
			len(preparedUnique.LayoutDecision.Assignments) != 2 || len(preparedUnique.Releases) != 1 {
			t.Fatalf("prepare frozen selected layout: prepared=%+v exists=%v err=%v", preparedUnique, exists, err)
		}
		if seal.LayoutDecision.Status != persistence.MatchingLayoutDecisionSelected ||
			seal.SelectedCalculation == nil || seal.SelectedCalculation.DecisionDigest == "" {
			t.Fatalf("seal did not retain the selected layout decision: seal=%+v", seal)
		}
		sealDigest, err := seal.LayoutDecision.Digest()
		if err != nil || sealDigest != seal.SelectedCalculation.DecisionDigest {
			t.Fatalf("seal decision digest=%q calculated=%q err=%v", seal.SelectedCalculation.DecisionDigest, sealDigest, err)
		}
		for _, assignment := range preparedUnique.LayoutDecision.Assignments {
			if assignment.ProviderTrackID != expectedTrackByMember[assignment.FileMemberID] {
				t.Fatalf("frozen decision assigned wrong provider track: member=%s got=%s want=%s", assignment.FileMemberID, assignment.ProviderTrackID, expectedTrackByMember[assignment.FileMemberID])
			}
		}
		evidence, err := persistence.DecodeFallbackLayoutStageEvidence(actualContext, actualContext.AcceptedShortlist)
		if err != nil || evidence.ValidateDecision(*preparedUnique.LayoutDecision) != nil {
			t.Fatalf("persisted decision does not validate against full sealed layout evidence: evidence=%+v err=%v", evidence, err)
		}
		releaseTrackIDs := make(map[uuid.UUID]bool, len(preparedUnique.Releases[0].Tracks))
		for _, track := range preparedUnique.Releases[0].Tracks {
			releaseTrackIDs[track.ID] = true
		}
		if len(releaseTrackIDs) != 2 || !releaseTrackIDs[alphaTrack.ID] || !releaseTrackIDs[betaTrack.ID] {
			t.Fatalf("frozen release did not expose the accepted two exact provider tracks: release=%+v alpha=%s beta=%s", preparedUnique.Releases[0], alphaTrack.ID, betaTrack.ID)
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

func assertPublishedFallbackReviewRefresh(t *testing.T, ctx context.Context, db *bun.DB, repository *persistence.SetupManagerRepository, groupID, runID uuid.UUID) {
	t.Helper()
	candidates, err := repository.ListFrozenMatchingReviewRefreshCandidates(ctx, uuid.Nil, 10)
	if err != nil || len(candidates) != 1 || candidates[0].GroupID != groupID || candidates[0].RunID != runID {
		t.Fatalf("list current review to refresh: %v, err=%v", candidates, err)
	}
	var before, after, reconciled int64
	if err := db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, groupID).Scan(ctx, &before); err != nil {
		t.Fatal(err)
	}
	if changed, err := repository.RefreshFrozenMatchingReview(ctx, candidates[0]); changed || err != nil {
		t.Fatalf("unchanged review refresh: changed=%t err=%v", changed, err)
	}
	if err := persistence.NewSettingsRepository(db).Set(ctx, settings.MatchingPairThresholdKey, "0.80"); err != nil {
		t.Fatal(err)
	}
	if changed, err := repository.RefreshFrozenMatchingReview(ctx, candidates[0]); !changed || err != nil {
		t.Fatalf("threshold drift refresh: changed=%t err=%v", changed, err)
	}
	if changed, err := repository.RefreshFrozenMatchingReview(ctx, candidates[0]); changed || err != nil {
		t.Fatalf("repeat review refresh: changed=%t err=%v", changed, err)
	}
	if err := db.NewRaw(`SELECT requested_generation,reconciled_generation FROM incoming_matching_generation WHERE group_id=?`, groupID).
		Scan(ctx, &after, &reconciled); err != nil || after != before+1 || reconciled != before {
		t.Fatalf("review refresh generations: before=%d requested=%d reconciled=%d err=%v", before, after, reconciled, err)
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
	if err != nil || !exists || prepared.Calculation == nil || prepared.LayoutDecision == nil ||
		prepared.LayoutDecision.Status != persistence.MatchingLayoutDecisionSelected ||
		len(prepared.LayoutDecision.Assignments) != 2 || len(prepared.Releases) != 1 {
		t.Fatalf("prepare unique publication from sealed evidence: exists=%v prepared=%+v err=%v", exists, prepared, err)
	}
	seal, found, err := fixture.repository.GetMatchingFallbackAuthoritySeal(ctx, runID, waitRevision)
	if err != nil || !found || seal.SelectedCalculation == nil || seal.LayoutDecision.Status != persistence.MatchingLayoutDecisionSelected {
		t.Fatalf("read selected layout seal: seal=%+v found=%v err=%v", seal, found, err)
	}
	sealDigest, err := seal.LayoutDecision.Digest()
	if err != nil || sealDigest != seal.SelectedCalculation.DecisionDigest {
		t.Fatalf("seal decision digest=%q calculated=%q err=%v", seal.SelectedCalculation.DecisionDigest, sealDigest, err)
	}
	publication, err := service.SerializeFrozenFallbackEvaluation(prepared)
	if err != nil || publication.Evaluation.Outcome != "unique" || publication.Evaluation.Decision != "apply" ||
		len(publication.Evaluation.Assignments) != 2 || len(publication.Inventory.Members) != 2 ||
		len(publication.Inventory.Releases) != 1 || len(prepared.Releases) != 1 {
		t.Fatalf("frozen layout did not derive a unique two-member assignment: evaluation=%+v releases=%d members=%d err=%v", publication.Evaluation, len(publication.Inventory.Releases), len(publication.Inventory.Members), err)
	}
	decisionTracks := make(map[uuid.UUID]uuid.UUID, len(prepared.LayoutDecision.Assignments))
	for _, assignment := range prepared.LayoutDecision.Assignments {
		decisionTracks[assignment.FileMemberID] = assignment.ProviderTrackID
	}
	assignments := make(map[uuid.UUID]uuid.UUID, len(publication.Evaluation.Assignments))
	tracks := make(map[uuid.UUID]bool, len(publication.Evaluation.Assignments))
	for _, assignment := range publication.Evaluation.Assignments {
		if assignment.FileMemberID == uuid.Nil || assignment.ProviderTrackID == uuid.Nil ||
			decisionTracks[assignment.FileMemberID] != assignment.ProviderTrackID || tracks[assignment.ProviderTrackID] {
			t.Fatalf("serialized assignment differs from the sealed layout decision: assignment=%+v decision=%v", assignment, prepared.LayoutDecision.Assignments)
		}
		assignments[assignment.FileMemberID] = assignment.ProviderTrackID
		tracks[assignment.ProviderTrackID] = true
	}
	if len(assignments) != 2 || len(tracks) != 2 || len(decisionTracks) != 2 || seal.LayoutDecision.ProviderReleaseID == nil ||
		prepared.LayoutDecision.ProviderReleaseID == nil || *seal.LayoutDecision.ProviderReleaseID != *prepared.LayoutDecision.ProviderReleaseID {
		t.Fatalf("sealed layout does not uniquely cover both members and release: assignments=%v trackCount=%d decision=%+v seal=%+v", assignments, len(tracks), prepared.LayoutDecision, seal.LayoutDecision)
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
	substituted := publication
	substituted.Evaluation.Assignments = append([]persistence.FrozenMatchingAssignment(nil), publication.Evaluation.Assignments...)
	a, b := &substituted.Evaluation.Assignments[0], &substituted.Evaluation.Assignments[1]
	a.ProviderTrackID, b.ProviderTrackID = b.ProviderTrackID, a.ProviderTrackID
	a.Medium, b.Medium = b.Medium, a.Medium
	a.Position, b.Position = b.Position, a.Position
	var evaluationBefore string
	if err := fixture.db.NewRaw(`SELECT COALESCE((SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=?), '')`, groupID).Scan(ctx, &evaluationBefore); err != nil {
		t.Fatalf("read evaluation revision before folder-position rejection: %v", err)
	}
	reject("substituted valid positions", substituted, map[uuid.UUID]*string{groupID: nil}, "differs from sealed layout decision")
	var evaluationAfter string
	var deliveryConsumed bool
	if err := fixture.db.NewRaw(`SELECT COALESCE((SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=?), '')`, groupID).Scan(ctx, &evaluationAfter); err != nil {
		t.Fatalf("read evaluation revision after folder-position rejection: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, runID, handoff.RiverJobID).Scan(ctx, &deliveryConsumed); err != nil {
		t.Fatalf("read delivery after folder-position rejection: %v", err)
	}
	if evaluationBefore != evaluationAfter || deliveryConsumed {
		t.Fatalf("layout rejection changed evaluation or consumed delivery: before=%+v after=%+v consumed=%v", evaluationBefore, evaluationAfter, deliveryConsumed)
	}

	wrongDecision := publication
	wrongDecision.Evaluation.Decision = "review"
	reject("wrong unique decision", wrongDecision, map[uuid.UUID]*string{groupID: nil}, "differs from sealed layout decision")
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
