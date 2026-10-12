//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
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
)

func TestAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t, false, false, false, false, false)
}

func TestAutomaticFallbackQualifiedContinuationSealsAndRegistersOneWakePostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t, false, false, false, true, false)
}

type fallbackSealWriteState struct {
	Phase         string
	Revision      int64
	Deliveries    int
	RiverJobs     int
	Dependencies  int
	Registrations int
}

func snapshotFallbackSealWriteState(t *testing.T, ctx context.Context, database *bun.DB, runID uuid.UUID) fallbackSealWriteState {
	t.Helper()
	var state fallbackSealWriteState
	if err := database.NewRaw(`SELECT current_phase,revision FROM matching_acquisition WHERE run_id=?`, runID).
		Scan(ctx, &state.Phase, &state.Revision); err != nil {
		t.Fatalf("read fallback seal acquisition cursor: %v", err)
	}
	queries := []struct {
		query string
		count *int
	}{
		{`SELECT count(*) FROM matching_delivery WHERE run_id=?`, &state.Deliveries},
		{`SELECT count(*) FROM river_job WHERE args->>'operation_id' IN (
			SELECT operation.id::text FROM operation WHERE operation.id=?
			UNION SELECT registration.request_id::text FROM matching_fallback_request_registration registration WHERE registration.run_id=?)`, &state.RiverJobs},
		{`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, &state.Dependencies},
		{`SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?`, &state.Registrations},
	}
	for index, item := range queries {
		var err error
		if index == 1 {
			err = database.NewRaw(item.query, runID, runID).Scan(ctx, item.count)
		} else {
			err = database.NewRaw(item.query, runID).Scan(ctx, item.count)
		}
		if err != nil {
			t.Fatalf("read fallback seal work counts: %v", err)
		}
	}
	return state
}

func TestAutomaticFallbackHistoricalStageLookupsDoNotBlockStageOnePostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t, true, false, false, false, false)
}

func TestAutomaticFallbackEmptyPlanParksForReviewPostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t, false, true, false, false, false)
}

func TestAutomaticFallbackEmptyPlanPublishesFrozenReviewPostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t, false, true, false, false, true)
}

func TestAutomaticFallbackFiniteThreeStageExhaustionParksForReviewPostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t, false, false, true, false, false)
}

func TestAutomaticFallbackExhaustedFiniteFlowPublishesFrozenReviewPostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t, false, false, true, false, true)
}

func testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t *testing.T, historicalStageLookup, emptyPlan, exhaustPlan, continueSeal, publishReview bool) {
	t.Parallel()
	fixture := newAutomaticMatchingTargetFenceFixture(t)
	ctx, cancel := context.WithTimeout(fixture.ctx, 60*time.Second)
	defer cancel()

	metadata := `{"ALBUM":["Captured Album"],"TITLE":["Receipt Track"]}`
	if emptyPlan {
		metadata = `{}`
	}
	if _, err := fixture.db.NewRaw(`UPDATE media_metadata_result SET observed_tags=?::jsonb`, metadata).Exec(ctx); err != nil {
		t.Fatalf("set fallback metadata: %v", err)
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
	planned, artistBearing := policy.PlanFallbackQueries(tagSets)
	if artistBearing || (emptyPlan && len(planned.Stages) != 0) || (!emptyPlan && (len(planned.Stages) != 3 || planned.Stages[0].Kind != "album_track" || planned.Stages[1].Kind != "album")) {
		t.Fatalf("unexpected approved fallback plan: %+v artistBearing=%v", planned, artistBearing)
	}
	plan := policy.TypedFallbackQueryPlan{SchemaVersion: planned.SchemaVersion, Stages: planned.Stages}

	var event persistence.IncomingMatchingReconcileEvent
	if err := fixture.db.NewSelect().Model(&event).Where("state = ? AND delivery_claimed", "dispatched").Order("created_at DESC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read claimed reconciliation event: %v", err)
	}
	var eventEpoch int64
	if err := fixture.db.NewRaw(`SELECT delivery_epoch FROM incoming_matching_reconcile_event WHERE id=?`, event.ID).Scan(ctx, &eventEpoch); err != nil {
		t.Fatalf("read reconciliation epoch: %v", err)
	}

	const releaseID = "90000000-0000-4000-8000-0000000000f1"
	const trackID = "90000000-0000-4000-8000-0000000000f2"
	const recordingID = "90000000-0000-4000-8000-0000000000f3"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})
	if err != nil {
		t.Fatalf("prepare River client: %v", err)
	}
	t.Cleanup(listenerPool.Close)
	inserter.client = client

	var stageZero *policy.TypedFallbackQueryStage
	var stageZeroKey string
	var stageZeroID uuid.UUID
	var stageZeroOperation *persistence.Operation
	stageZeroPayload := json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":0,"offset":0,"recordings":[]}`)
	const stageZeroReleaseID = "90000000-0000-4000-8000-0000000000f4"
	const stageZeroTrackID = "90000000-0000-4000-8000-0000000000f5"
	const stageZeroRecordingID = "90000000-0000-4000-8000-0000000000f6"
	if !emptyPlan {
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
		if historicalStageLookup {
			stageZeroPayload = json.RawMessage(fmt.Sprintf(`{"created":"2026-10-12T00:00:00Z","count":1,"offset":0,"recordings":[{"id":"11111111-1111-4111-8111-111111111111","title":"Receipt Track","releases":[{"id":%q,"title":"Wrong Album"}]}]}`, stageZeroReleaseID))
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
	}
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
	})
	if err != nil {
		t.Fatalf("admit automatic fallback run: %v", err)
	}
	parentID = admitted.OriginalRunID
	if emptyPlan {
		var wakeID int64
		if err := fixture.db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL`, parentID).Scan(ctx, &wakeID); err != nil {
			t.Fatalf("read registered empty-plan parent wake: %v", err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, wakeID).Exec(ctx); err != nil {
			t.Fatalf("mark empty-plan parent wake running: %v", err)
		}
		claim, outcome, err := fixture.repository.ClaimMatchingDelivery(ctx, parentID, 1, wakeID, 1)
		if err != nil || outcome != persistence.MatchingDeliveryClaimed {
			t.Fatalf("claim empty-plan parent wake: claim=%+v outcome=%v err=%v", claim, outcome, err)
		}
		changed, handled, err := acquisition.Advance(ctx, parentID, 1, wakeID, claim)
		if err != nil || !handled || !changed {
			t.Fatalf("complete empty fallback plan: changed=%v handled=%v err=%v", changed, handled, err)
		}
		parked, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
		if err != nil || !exists || parked.Phase != "fallback_review_wait" {
			t.Fatalf("empty fallback plan did not park for review: snapshot=%+v exists=%v err=%v", parked, exists, err)
		}
		var evidence struct {
			Reason   string            `json:"reason"`
			Receipts []json.RawMessage `json:"calculation_receipts"`
		}
		if err := json.Unmarshal(parked.Output, &evidence); err != nil || evidence.Reason != "fallback_plan_empty" || len(evidence.Receipts) != 0 {
			t.Fatalf("empty fallback review evidence is invalid: output=%s evidence=%+v err=%v", parked.Output, evidence, err)
		}
		var dependencyCount, registrationCount, calculationCount, phaseWakeCount int
		for query, destination := range map[string]*int{
			`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`:    &dependencyCount,
			`SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?`: &registrationCount,
			`SELECT count(*) FROM matching_fallback_calculation_receipt WHERE run_id=?`:  &calculationCount,
			`SELECT count(*) FROM matching_delivery WHERE run_id=?`:                      &phaseWakeCount,
		} {
			if err := fixture.db.NewRaw(query, parentID).Scan(ctx, destination); err != nil {
				t.Fatalf("read empty-plan evidence counts: %v", err)
			}
		}
		if len(parked.Intent.InitialProviderInputs) != 0 || len(parked.Intent.CapturedMembers) == 0 || dependencyCount != 0 || registrationCount != 0 || calculationCount != 0 || phaseWakeCount != 1 {
			t.Fatalf("empty plan fabricated work or lost captured intent/original wake: inputs=%d members=%d dependencies=%d registrations=%d calculations=%d deliveries=%d", len(parked.Intent.InitialProviderInputs), len(parked.Intent.CapturedMembers), dependencyCount, registrationCount, calculationCount, phaseWakeCount)
		}
		authority, found, err := fixture.repository.BuildMatchingFallbackSealAuthority(ctx, parentID, parked.Revision)
		if err != nil || !found {
			t.Fatalf("build no-query fallback seal authority: authority=%+v found=%v err=%v", authority, found, err)
		}
		var authorityPlan policy.TypedFallbackQueryPlan
		if err := json.Unmarshal(authority.Plan, &authorityPlan); err != nil || authority.SchemaVersion != 3 || authority.Disposition != "no_query" ||
			authority.RunID != parentID || authority.WaitRevision != parked.Revision || len(authorityPlan.Stages) != 0 ||
			authority.SourceID == uuid.Nil || authority.ProviderID == uuid.Nil || authority.Endpoint == "" || authority.SourceConfiguration == "" ||
			len(authority.CapturedMetadata) != len(parked.Intent.CapturedMembers) || len(authority.ProofHistory) != 0 ||
			authority.SelectedCalculation != nil || len(authority.FullEvaluationContext) != 0 || authority.Digest == "" {
			t.Fatalf("no-query authority omitted captured intent or fabricated calculation evidence: authority=%+v plan=%+v err=%v", authority, authorityPlan, err)
		}
		beforeSeal := snapshotFallbackSealWriteState(t, ctx, fixture.db, parentID)
		sealed, sealedFound, err := fixture.repository.SealMatchingFallbackAuthority(ctx, parentID, parked.Revision)
		if err != nil || !sealedFound || sealed.Digest != authority.Digest {
			t.Fatalf("persist no-query fallback authority: authority=%+v found=%v err=%v", sealed, sealedFound, err)
		}
		if afterSeal := snapshotFallbackSealWriteState(t, ctx, fixture.db, parentID); afterSeal != beforeSeal {
			t.Fatalf("no-query authority seal changed acquisition or work state: before=%+v after=%+v", beforeSeal, afterSeal)
		}
		readSeal, readFound, err := fixture.repository.GetMatchingFallbackAuthoritySeal(ctx, parentID, parked.Revision)
		if err != nil || !readFound || readSeal.Digest != sealed.Digest || readSeal.Disposition != "no_query" {
			t.Fatalf("read persisted no-query authority: authority=%+v found=%v err=%v", readSeal, readFound, err)
		}
		assertFallbackAuthoritySealImmutable(t, ctx, fixture.db, parentID, parked.Revision, sealed.Digest)
		originalAuthority := authority
		if _, err := fixture.db.NewRaw(`UPDATE provider_source SET endpoint=?,configuration_identity=? WHERE id=?`, "https://changed.invalid/ws/2", "changed-configuration", authority.SourceID).Exec(ctx); err != nil {
			t.Fatalf("change mutable provider source registry row: %v", err)
		}
		refreshedAuthority, found, err := fixture.repository.BuildMatchingFallbackSealAuthority(ctx, parentID, parked.Revision)
		if err != nil || !found || refreshedAuthority.Digest != originalAuthority.Digest ||
			refreshedAuthority.SourceID != originalAuthority.SourceID || refreshedAuthority.ProviderID != originalAuthority.ProviderID ||
			refreshedAuthority.Endpoint != originalAuthority.Endpoint || refreshedAuthority.SourceConfiguration != originalAuthority.SourceConfiguration {
			t.Fatalf("no-query authority changed with mutable provider source registry: before=%+v after=%+v found=%v err=%v", originalAuthority, refreshedAuthority, found, err)
		}
		var runState, opStage string
		var hasActiveDelivery bool
		if err := fixture.db.NewRaw(`SELECT run.state,operation.stage,
			run.active_execution_epoch IS NOT NULL OR run.active_river_job_id IS NOT NULL OR run.active_river_attempt IS NOT NULL
			FROM matching_run run JOIN operation ON operation.id=run.operation_id WHERE run.id=?`, parentID).
			Scan(ctx, &runState, &opStage, &hasActiveDelivery); err != nil || runState != "waiting_policy" || opStage != "review_pending" || hasActiveDelivery {
			t.Fatalf("empty plan did not park without manual mode or clear its active delivery: run=%s stage=%s active=%v err=%v", runState, opStage, hasActiveDelivery, err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE provider_source SET endpoint=?,configuration_identity=? WHERE id=?`, originalAuthority.Endpoint, originalAuthority.SourceConfiguration, originalAuthority.SourceID).Exec(ctx); err != nil {
			t.Fatalf("restore frozen provider source after authority check: %v", err)
		}
		assertFallbackEvaluationContinuation(t, ctx, fixture, inserter, parentID, parked.Revision, publishReview)
		return
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
	var historicalLookupRequestID uuid.UUID
	wakeID, claim := claimWake()
	advance(wakeID, claim)
	if historicalStageLookup {
		stageZeroHydration, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
		if err != nil || !exists || stageZeroHydration.Phase != persistence.MatchingAcquisitionFallbackHydration {
			t.Fatalf("nonempty stage-zero search did not register hydration: snapshot=%+v exists=%v err=%v", stageZeroHydration, exists, err)
		}
		var stageZeroLookups struct {
			Lookups []struct {
				RequestID uuid.UUID `json:"request_id"`
				CacheKey  string    `json:"cache_key"`
			} `json:"lookups"`
		}
		if err := json.Unmarshal(stageZeroHydration.Output, &stageZeroLookups); err != nil || len(stageZeroLookups.Lookups) != 1 {
			t.Fatalf("decode nonempty stage-zero lookup manifest: output=%s err=%v", stageZeroHydration.Output, err)
		}
		stageZeroLookup := stageZeroLookups.Lookups[0]
		historicalLookupRequestID = stageZeroLookup.RequestID
		stageZeroLookupOperation, err := fixture.repository.GetOperation(ctx, stageZeroLookup.RequestID)
		if err != nil || stageZeroLookupOperation.RiverJobID == nil {
			t.Fatalf("read historical stage-zero lookup: operation=%+v err=%v", stageZeroLookupOperation, err)
		}
		stageZeroLookupPayload := json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Wrong Album","artist-credit":[],"media":[{"position":1,"tracks":[{"id":%q,"number":"1","length":600000,"recording":{"id":%q,"title":"Unrelated Recording"}}]}]}`, stageZeroReleaseID, stageZeroTrackID, stageZeroRecordingID))
		stageZeroLookupGraph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: stageZeroLookupPayload}})
		if err != nil {
			t.Fatalf("normalize historical stage-zero lookup: %v", err)
		}
		stageZeroLookupTicket, err := cache.BeginRefresh(ctx, source.ID, stageZeroLookup.CacheKey, config.Identity)
		if err != nil {
			t.Fatalf("reserve historical stage-zero lookup: %v", err)
		}
		if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: stageZeroLookupTicket, FetchedAt: time.Now().UTC(), Payload: stageZeroLookupPayload, Graph: stageZeroLookupGraph}); err != nil {
			t.Fatalf("accept historical stage-zero lookup: %v", err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *stageZeroLookupOperation.RiverJobID).Exec(ctx); err != nil {
			t.Fatalf("mark historical stage-zero lookup running: %v", err)
		}
		if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: *stageZeroLookupOperation.RiverJobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: stageZeroLookup.RequestID}}); err != nil {
			t.Fatalf("run historical stage-zero lookup: %v", err)
		}
		stageZeroLookupReceipt, err := fixture.repository.GetMatchingFallbackLookupReceipt(ctx, parentID, stageZeroLookup.RequestID)
		if err != nil || stageZeroLookupReceipt == nil || stageZeroLookupReceipt.StageIndex != 0 {
			t.Fatalf("read historical stage-zero lookup receipt: receipt=%+v err=%v", stageZeroLookupReceipt, err)
		}
		wakeID, claim = claimWake()
		advance(wakeID, claim)
		stageZeroApplicability, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
		if err != nil || !exists || stageZeroApplicability.Phase != persistence.MatchingAcquisitionFallbackApplicability {
			t.Fatalf("stage-zero lookup did not reach applicability: snapshot=%+v exists=%v err=%v", stageZeroApplicability, exists, err)
		}
		wakeID, claim = claimWake()
		advance(wakeID, claim)
	} else {
		// Empty stage-zero hits first materialize an applicability phase. Its own
		// registered wake performs the real calculation and advances the saved plan.
		wakeID, claim = claimWake()
		advance(wakeID, claim)
	}

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
	if exhaustPlan {
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
	if exhaustPlan {
		stageOneApplicability, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
		if err != nil || !exists || stageOneApplicability.Phase != persistence.MatchingAcquisitionFallbackApplicability {
			t.Fatalf("empty stage-one search did not reach applicability: snapshot=%+v exists=%v err=%v", stageOneApplicability, exists, err)
		}
		wakeID, claim = claimWake()
		advance(wakeID, claim)
		stageTwoSearch, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
		if err != nil || !exists || stageTwoSearch.Phase != "fallback_search" {
			t.Fatalf("stage-one calculation did not admit final finite stage: snapshot=%+v exists=%v err=%v", stageTwoSearch, exists, err)
		}
		var stageTwoOutput struct {
			RequestID uuid.UUID `json:"request_id"`
		}
		if err := json.Unmarshal(stageTwoSearch.Output, &stageTwoOutput); err != nil || stageTwoOutput.RequestID == uuid.Nil {
			t.Fatalf("decode final-stage operation descriptor: output=%s err=%v", stageTwoSearch.Output, err)
		}
		stageTwoOperation, err := fixture.repository.GetOperation(ctx, stageTwoOutput.RequestID)
		if err != nil || stageTwoOperation.RiverJobID == nil || stageTwoOperation.Kind != "provider_recording_search" {
			t.Fatalf("read actual final-stage operation: operation=%+v err=%v", stageTwoOperation, err)
		}
		stageTwoTicket, err := cache.BeginRefresh(ctx, source.ID, *stageTwoOperation.ProviderCacheKey, config.Identity)
		if err != nil {
			t.Fatalf("reserve final-stage accepted response: %v", err)
		}
		const finalRecordingID = "90000000-0000-4000-8000-0000000000f7"
		const finalReleaseID = "90000000-0000-4000-8000-0000000000f8"
		const finalTrackID = "90000000-0000-4000-8000-0000000000f9"
		stageTwoPayload := json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":1,"offset":0,"recordings":[{"id":"90000000-0000-4000-8000-0000000000f7","title":"Unrelated Recording","releases":[]}]}`)
		if publishReview {
			stageTwoPayload = json.RawMessage(fmt.Sprintf(`{"created":"2026-10-12T00:00:00Z","count":1,"offset":0,"recordings":[{"id":%q,"title":"Completely Different Song","releases":[{"id":%q,"title":"Unrelated Release"}]}]}`, finalRecordingID, finalReleaseID))
		}
		if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: stageTwoTicket, FetchedAt: time.Now().UTC(), Payload: stageTwoPayload}); err != nil {
			t.Fatalf("accept final-stage nonempty below-threshold response: %v", err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *stageTwoOperation.RiverJobID).Exec(ctx); err != nil {
			t.Fatalf("mark final-stage provider job running: %v", err)
		}
		if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: *stageTwoOperation.RiverJobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: stageTwoOutput.RequestID}}); err != nil {
			t.Fatalf("run real final-stage provider worker: %v", err)
		}
		stageTwoReceipt, err := fixture.repository.GetMatchingFallbackSearchReceipt(ctx, parentID, stageTwoOutput.RequestID)
		if err != nil || stageTwoReceipt == nil || stageTwoReceipt.StageIndex != 2 || !jsonEqual(stageTwoReceipt.Payload, stageTwoPayload) {
			t.Fatalf("read real final-stage receipt: receipt=%+v err=%v", stageTwoReceipt, err)
		}
		wakeID, claim = claimWake()
		advance(wakeID, claim)
		stageTwoApplicability, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
		if err != nil || !exists {
			t.Fatalf("read final-stage search handoff: snapshot=%+v exists=%v err=%v", stageTwoApplicability, exists, err)
		}
		if publishReview && stageTwoApplicability.Phase != persistence.MatchingAcquisitionFallbackHydration {
			t.Fatalf("final-stage search did not register release lookup hydration: snapshot=%+v", stageTwoApplicability)
		}
		if !publishReview && stageTwoApplicability.Phase != persistence.MatchingAcquisitionFallbackApplicability {
			t.Fatalf("final-stage search did not reach applicability: snapshot=%+v exists=%v err=%v", stageTwoApplicability, exists, err)
		}
		finalLookupRequestIDs := make(map[uuid.UUID]bool)
		if publishReview {
			var finalHydration struct {
				StageIndex int `json:"stage_index"`
				Lookups    []struct {
					RequestID uuid.UUID `json:"request_id"`
					CacheKey  string    `json:"cache_key"`
				} `json:"lookups"`
			}
			if err := json.Unmarshal(stageTwoApplicability.Output, &finalHydration); err != nil || finalHydration.StageIndex != 2 || len(finalHydration.Lookups) == 0 {
				t.Fatalf("final-stage recording releases did not register release lookup hydration: output=%s manifest=%+v err=%v", stageTwoApplicability.Output, finalHydration, err)
			}
			finalLookupRequestIDs = make(map[uuid.UUID]bool, len(finalHydration.Lookups))
			for _, lookup := range finalHydration.Lookups {
				lookupOperation, err := fixture.repository.GetOperation(ctx, lookup.RequestID)
				if err != nil || lookupOperation.RiverJobID == nil || lookupOperation.Kind != "provider_release_lookup" {
					t.Fatalf("read final-stage release lookup registration: operation=%+v err=%v", lookupOperation, err)
				}
				lookupPayload := json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Unrelated Release","artist-credit":[{"name":"Artist","artist":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Artist"}}],"media":[{"position":1,"tracks":[{"id":%q,"number":"1","length":180000,"recording":{"id":%q,"title":"Completely Different Song","artist-credit":[{"name":"Artist","artist":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Artist"}}]}}]}]}`, finalReleaseID, finalTrackID, finalRecordingID))
				lookupGraph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: lookupPayload}})
				if err != nil {
					t.Fatalf("normalize final-stage release lookup: %v", err)
				}
				lookupTicket, err := cache.BeginRefresh(ctx, source.ID, lookup.CacheKey, config.Identity)
				if err != nil {
					t.Fatalf("reserve final-stage release lookup: %v", err)
				}
				if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: lookupTicket, FetchedAt: time.Now().UTC(), Payload: lookupPayload, Graph: lookupGraph}); err != nil {
					t.Fatalf("accept final-stage release lookup response: %v", err)
				}
				if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *lookupOperation.RiverJobID).Exec(ctx); err != nil {
					t.Fatalf("mark final-stage lookup job running: %v", err)
				}
				if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: *lookupOperation.RiverJobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: lookup.RequestID}}); err != nil {
					t.Fatalf("run actual final-stage release lookup worker: %v", err)
				}
				lookupReceipt, err := fixture.repository.GetMatchingFallbackLookupReceipt(ctx, parentID, lookup.RequestID)
				if err != nil || lookupReceipt == nil || lookupReceipt.StageIndex != 2 {
					t.Fatalf("read actual final-stage release lookup receipt: receipt=%+v err=%v", lookupReceipt, err)
				}
				finalLookupRequestIDs[lookup.RequestID] = true
			}
			wakeID, claim = claimWake()
			advance(wakeID, claim)
			stageTwoApplicability, exists, err = fixture.repository.GetMatchingAcquisition(ctx, parentID)
			if err != nil || !exists || stageTwoApplicability.Phase != persistence.MatchingAcquisitionFallbackApplicability {
				t.Fatalf("final-stage lookup receipts did not complete hydration: snapshot=%+v exists=%v err=%v", stageTwoApplicability, exists, err)
			}
		}
		wakeID, claim = claimWake()
		advance(wakeID, claim)
		parked, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
		if err != nil || !exists || parked.Phase != "fallback_review_wait" {
			t.Fatalf("three-stage exhausted plan did not park for review: snapshot=%+v exists=%v err=%v", parked, exists, err)
		}
		var evidence struct {
			Reason   string `json:"reason"`
			Receipts []struct {
				StageIndex int    `json:"stage_index"`
				Digest     string `json:"input_digest"`
			} `json:"calculation_receipts"`
		}
		if err := json.Unmarshal(parked.Output, &evidence); err != nil || evidence.Reason != "review_required" || len(evidence.Receipts) != 3 {
			t.Fatalf("exhaustion receipt manifest is incomplete: output=%s evidence=%+v err=%v", parked.Output, evidence, err)
		}
		for index, receipt := range evidence.Receipts {
			if receipt.StageIndex != index || receipt.Digest == "" {
				t.Fatalf("calculation receipt order or binding is invalid: %+v", evidence.Receipts)
			}
		}
		authority, found, err := fixture.repository.BuildMatchingFallbackSealAuthority(ctx, parentID, parked.Revision)
		if err != nil || !found {
			t.Fatalf("build exhausted fallback seal authority: authority=%+v found=%v err=%v", authority, found, err)
		}
		var authorityPlan policy.TypedFallbackQueryPlan
		if err := json.Unmarshal(authority.Plan, &authorityPlan); err != nil || authority.SchemaVersion != 3 || authority.Disposition != "review_exhausted" ||
			authority.RunID != parentID || authority.WaitRevision != parked.Revision || len(authorityPlan.Stages) != 3 ||
			authority.SelectedCalculation == nil || authority.SelectedCalculation.StageIndex != 2 || len(authority.ProofHistory) != 3 ||
			len(authority.FullEvaluationContext) == 0 || authority.Digest == "" {
			t.Fatalf("exhausted authority is not bound to the last saved stage: authority=%+v plan=%+v err=%v", authority, authorityPlan, err)
		}
		for index, reference := range authority.ProofHistory {
			if reference.StageIndex != index || reference.SchemaVersion != 3 || reference.CalculatorVersion != 3 || reference.DigestVersion != 3 || reference.InputDigest == "" {
				t.Fatalf("exhausted authority history has an unsupported or misordered receipt: %+v", authority.ProofHistory)
			}
		}
		if selected := authority.SelectedCalculation; selected.AcquisitionRevision != authority.ProofHistory[2].AcquisitionRevision ||
			selected.InputDigest != authority.ProofHistory[2].InputDigest {
			t.Fatalf("exhausted authority selected an earlier calculation: selected=%+v history=%+v", selected, authority.ProofHistory)
		}
		var fullContext persistence.MatchingFallbackCalculationContext
		if err := json.Unmarshal(authority.FullEvaluationContext, &fullContext); err != nil ||
			fullContext.StageIndex != 2 || fullContext.SearchReceipt.RequestID != stageTwoOutput.RequestID || fullContext.SearchReceipt.RequestID == stageZeroID ||
			len(fullContext.LookupReceipts) != len(finalLookupRequestIDs) || !jsonEqual(fullContext.SearchReceipt.Payload, stageTwoPayload) ||
			len(fullContext.Intent.CapturedMembers) != len(parked.Intent.CapturedMembers) {
			t.Fatalf("exhausted authority dropped the actual final-stage inventory/evidence: context=%+v err=%v", fullContext, err)
		}
		for _, lookupReceipt := range fullContext.LookupReceipts {
			if lookupReceipt.StageIndex != 2 || !finalLookupRequestIDs[lookupReceipt.RequestID] {
				t.Fatalf("final-stage authority contains unscoped or foreign lookup receipt: %+v", lookupReceipt)
			}
		}
		var finalCalculation struct {
			SchemaVersion     int `json:"schema_version"`
			CalculatorVersion int `json:"calculator_version"`
			DigestVersion     int `json:"digest_version"`
			StageIndex        int `json:"stage_index"`
			Edges             []struct {
				MemberID        uuid.UUID `json:"member_id"`
				WorkID          uuid.UUID `json:"work_id"`
				ProviderTrackID uuid.UUID `json:"provider_track_id"`
				LookupRequestID uuid.UUID `json:"lookup_request_id"`
				Confidence      float64   `json:"confidence"`
			} `json:"edge_calculations"`
		}
		var finalCalculationRaw json.RawMessage
		if err := fixture.db.NewRaw(`SELECT schema_version,calculator_version,digest_version,stage_index,edge_calculations
			FROM matching_fallback_calculation_receipt WHERE run_id=? AND acquisition_revision=?`, parentID, stageTwoApplicability.Revision).
			Scan(ctx, &finalCalculation.SchemaVersion, &finalCalculation.CalculatorVersion, &finalCalculation.DigestVersion, &finalCalculation.StageIndex, &finalCalculationRaw); err != nil {
			t.Fatalf("read final-stage calculation proof: %v", err)
		}
		if publishReview {
			if err := json.Unmarshal(finalCalculationRaw, &finalCalculation.Edges); err != nil || finalCalculation.SchemaVersion != 3 || finalCalculation.CalculatorVersion != 3 ||
				finalCalculation.DigestVersion != 3 || finalCalculation.StageIndex != 2 || len(finalCalculation.Edges) == 0 {
				t.Fatalf("final-stage calculation proof is not current schema-3 evidence: calculation=%+v edges=%+v err=%v", finalCalculation, finalCalculation.Edges, err)
			}
			if err := persistence.ValidateMatchingFallbackCalculationEdges(fullContext, finalCalculationRaw); err != nil {
				t.Fatalf("final-stage calculation edge set is not complete for its original accepted context: %v", err)
			}
			for _, edge := range finalCalculation.Edges {
				if edge.MemberID == uuid.Nil || edge.WorkID == uuid.Nil || edge.ProviderTrackID == uuid.Nil || !finalLookupRequestIDs[edge.LookupRequestID] || edge.Confidence >= 0.70 {
					t.Fatalf("final-stage edge is incomplete, unscoped, or unexpectedly qualifying: %+v lookupIDs=%v", edge, finalLookupRequestIDs)
				}
			}
		}
		var requestCount, calculationCount int
		if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=? AND role='search'`, parentID).Scan(ctx, &requestCount); err != nil {
			t.Fatalf("count exact executed search stages: %v", err)
		}
		if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_calculation_receipt WHERE run_id=?`, parentID).Scan(ctx, &calculationCount); err != nil {
			t.Fatalf("count exact stage calculations: %v", err)
		}
		if requestCount != 3 || calculationCount != 3 {
			t.Fatalf("finite plan did not stop at its three saved stages: searches=%d calculations=%d", requestCount, calculationCount)
		}
		beforeSeal := snapshotFallbackSealWriteState(t, ctx, fixture.db, parentID)
		sealed, sealedFound, err := fixture.repository.SealMatchingFallbackAuthority(ctx, parentID, parked.Revision)
		if err != nil || !sealedFound || sealed.Digest != authority.Digest {
			t.Fatalf("persist exhausted fallback authority: authority=%+v found=%v err=%v", sealed, sealedFound, err)
		}
		if afterSeal := snapshotFallbackSealWriteState(t, ctx, fixture.db, parentID); afterSeal != beforeSeal {
			t.Fatalf("exhausted authority seal changed acquisition or work state: before=%+v after=%+v", beforeSeal, afterSeal)
		}
		readSeal, readFound, err := fixture.repository.GetMatchingFallbackAuthoritySeal(ctx, parentID, parked.Revision)
		if err != nil || !readFound || readSeal.Digest != sealed.Digest || readSeal.Disposition != "review_exhausted" ||
			readSeal.SelectedCalculation == nil || readSeal.SelectedCalculation.StageIndex != 2 || len(readSeal.FullEvaluationContext) == 0 {
			t.Fatalf("read persisted exhausted authority: authority=%+v found=%v err=%v", readSeal, readFound, err)
		}
		if continueSeal || publishReview {
			assertFallbackEvaluationContinuation(t, ctx, fixture, inserter, parentID, parked.Revision, publishReview)
		}
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
	if historicalStageLookup {
		if totalLookupDependencies != 2 || stageOneLookupDependencies != 1 {
			t.Fatalf("stage-scoped hydration count lost historical/current membership: total=%d stage1=%d", totalLookupDependencies, stageOneLookupDependencies)
		}
		oldLookup, err := fixture.repository.GetMatchingFallbackLookupReceipt(ctx, parentID, historicalLookupRequestID)
		if err != nil || oldLookup == nil || oldLookup.StageIndex != 0 {
			t.Fatalf("stage-one hydration changed historical stage-zero lookup receipt: receipt=%+v err=%v", oldLookup, err)
		}
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
	if err != nil || !exists || final.Phase != "pending_seal" {
		t.Fatalf("real fallback calculation did not seal qualified applicability: snapshot=%+v exists=%v err=%v", final, exists, err)
	}
	if !historicalStageLookup && !continueSeal {
		// A source resolved after admission must not replace the frozen run source
		// authority when the first persistence write is attempted.
		if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", server.URL+"/changed/ws/2"); err != nil {
			t.Fatalf("change current provider configuration after admission: %v", err)
		}
		changedConfig, err := registry.GetMusicBrainzConfig(ctx)
		if err != nil {
			t.Fatalf("read changed provider configuration: %v", err)
		}
		if _, err := cache.ResolveMusicBrainzSource(ctx, changedConfig.BaseURL, changedConfig.Identity); err != nil {
			t.Fatalf("resolve replacement provider source: %v", err)
		}
		if _, found, err := fixture.repository.SealMatchingFallbackAuthority(ctx, parentID, final.Revision); err == nil || found {
			t.Fatalf("first seal accepted changed provider source: found=%v err=%v", found, err)
		}
		var sealCount int
		if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_authority_seal WHERE run_id=?`, parentID).Scan(ctx, &sealCount); err != nil || sealCount != 0 {
			t.Fatalf("stale-source rejection persisted a seal: count=%d err=%v", sealCount, err)
		}
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
		Confidence float64 `json:"confidence"`
	}
	if receipt.SchemaVersion != 3 || receipt.CalculatorVersion != 3 || receipt.DigestVersion != 3 || receipt.StageIndex != 1 || receipt.InputDigest == "" ||
		json.Unmarshal(receipt.Edges, &edges) != nil || len(edges) == 0 {
		t.Fatalf("persisted calculation did not prove genuine qualified stage-one edge: receipt=%+v edges=%+v", receipt, edges)
	}
	for _, edge := range edges {
		if edge.Confidence < 0.70 {
			t.Fatalf("persisted stage-one edge did not qualify: receipt=%+v edges=%+v", receipt, edges)
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
	if historicalStageLookup {
		expectedDependencies, expectedRegistrations, expectedLookupJobs = 4, 4, 2
	} else if publishReview {
		expectedDependencies, expectedRegistrations, expectedLookupJobs = 4, 4, 2
	}
	if inputCount != 1 || dependencyCount != expectedDependencies || registrationCount != expectedRegistrations || searchJobCount != 0 || lookupJobCount != expectedLookupJobs {
		t.Fatalf("finite flow wrote unexpected request/dependency work: initialInputs=%d dependencies=%d registrations=%d stage2Jobs=%d lookupJobs=%d", inputCount, dependencyCount, registrationCount, searchJobCount, lookupJobCount)
	}
	if continueSeal || publishReview {
		assertFallbackEvaluationContinuation(t, ctx, fixture, inserter, parentID, final.Revision, false)
	}
}

func assertFallbackEvaluationContinuation(t *testing.T, ctx context.Context, fixture automaticTargetFenceFixture, inserter *fallbackHandoffRiverInserter, runID uuid.UUID, waitRevision int64, publishReview bool) {
	t.Helper()
	before := snapshotFallbackSealWriteState(t, ctx, fixture.db, runID)
	var seals, handoffs int
	readRows := func() {
		t.Helper()
		if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_authority_seal WHERE run_id=?`, runID).Scan(ctx, &seals); err != nil {
			t.Fatal(err)
		}
		if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_evaluation_handoff WHERE run_id=?`, runID).Scan(ctx, &handoffs); err != nil {
			t.Fatal(err)
		}
	}
	readRows()
	initialSeals, initialHandoffs := seals, handoffs
	assertNoWrites := func(label string) {
		t.Helper()
		after := snapshotFallbackSealWriteState(t, ctx, fixture.db, runID)
		readRows()
		if before != after || seals != initialSeals || handoffs != initialHandoffs {
			t.Fatalf("%s changed fallback continuation state: before=%+v after=%+v seals=%d handoffs=%d", label, before, after, seals, handoffs)
		}
	}
	if _, found, err := fixture.repository.ContinueMatchingFallbackSeal(ctx, inserter, runID, 2, waitRevision); err == nil || found {
		t.Fatalf("stale operation attempt was accepted: found=%v err=%v", found, err)
	}
	assertNoWrites("stale operation attempt")
	if _, found, err := fixture.repository.ContinueMatchingFallbackSeal(ctx, inserter, runID, 1, waitRevision-1); err == nil || found {
		t.Fatalf("stale wait revision was accepted: found=%v err=%v", found, err)
	}
	assertNoWrites("stale wait revision")
	if _, err := fixture.db.ExecContext(ctx, `CREATE FUNCTION reject_test_fallback_handoff() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test handoff insert failure'; END $$`); err != nil {
		t.Fatalf("create handoff rollback fault: %v", err)
	}
	if _, err := fixture.db.ExecContext(ctx, `CREATE TRIGGER reject_test_fallback_handoff BEFORE INSERT ON matching_fallback_evaluation_handoff FOR EACH ROW EXECUTE FUNCTION reject_test_fallback_handoff()`); err != nil {
		t.Fatalf("install handoff rollback fault: %v", err)
	}
	if _, found, err := fixture.repository.ContinueMatchingFallbackSeal(ctx, inserter, runID, 1, waitRevision); err == nil || found || !strings.Contains(err.Error(), "test handoff insert failure") {
		t.Fatalf("injected handoff failure succeeded: found=%v err=%v", found, err)
	}
	if _, err := fixture.db.ExecContext(ctx, `DROP TRIGGER reject_test_fallback_handoff ON matching_fallback_evaluation_handoff`); err != nil {
		t.Fatalf("remove handoff rollback fault: %v", err)
	}
	if _, err := fixture.db.ExecContext(ctx, `DROP FUNCTION reject_test_fallback_handoff()`); err != nil {
		t.Fatalf("drop handoff rollback fault: %v", err)
	}
	assertNoWrites("handoff SQL failure")

	wakeCalls := inserter.wakeCalls
	handoff, found, err := fixture.repository.ContinueMatchingFallbackSeal(ctx, inserter, runID, 1, waitRevision)
	if err != nil || !found || handoff.RunID != runID || handoff.WaitRevision != waitRevision || handoff.OperationAttempt != 1 || handoff.RiverJobID < 1 || handoff.SealDigest == "" {
		t.Fatalf("continue qualified fallback wait: handoff=%+v found=%v err=%v", handoff, found, err)
	}
	if inserter.wakeCalls != wakeCalls+1 {
		t.Fatalf("continuation wake insert calls=%d, want exactly one", inserter.wakeCalls-wakeCalls)
	}
	var currentPhase string
	var currentRevision int64
	if err := fixture.db.NewRaw(`SELECT current_phase,revision FROM matching_acquisition WHERE run_id=?`, runID).Scan(ctx, &currentPhase, &currentRevision); err != nil || currentPhase != persistence.MatchingAcquisitionFallbackEvaluationSealed || currentRevision != waitRevision+1 {
		t.Fatalf("continuation phase=%q revision=%d err=%v", currentPhase, currentRevision, err)
	}
	var registeredJobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? AND operation_attempt=1 AND river_job_id=? AND consumed_at IS NULL`, runID, handoff.RiverJobID).Scan(ctx, &registeredJobID); err != nil || registeredJobID != handoff.RiverJobID {
		t.Fatalf("registered wake=%d handoff=%d err=%v", registeredJobID, handoff.RiverJobID, err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, handoff.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("make registered continuation wake claimable: %v", err)
	}
	claim, outcome, err := fixture.repository.ClaimMatchingDelivery(ctx, runID, 1, handoff.RiverJobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim actual continuation wake: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}
	seal, exists, err := fixture.repository.GetMatchingFallbackAuthoritySeal(ctx, runID, waitRevision)
	if err != nil || !exists || seal.Digest != handoff.SealDigest || seal.RunID != runID || seal.WaitRevision != waitRevision {
		t.Fatalf("read historical seal after cursor advancement: seal=%+v exists=%v err=%v", seal, exists, err)
	}
	prepared, exists, err := service.PrepareFrozenFallbackEvaluation(ctx, fixture.repository, runID, waitRevision)
	if err != nil || !exists || prepared.Authority.Digest != handoff.SealDigest || prepared.Authority.RunID != runID || prepared.Authority.WaitRevision != waitRevision ||
		len(prepared.FrozenInventory.MemberFences) != len(seal.CapturedMetadata) || len(prepared.FrozenInventory.Files) != len(seal.CapturedMetadata) {
		t.Fatalf("prepare full evaluation from historical seal: prepared=%+v exists=%v err=%v", prepared, exists, err)
	}
	if publishReview {
		publication, err := service.SerializeFrozenFallbackEvaluation(prepared)
		expectedEdges := 0
		if prepared.Calculation != nil {
			expectedEdges = 1
		}
		if err != nil || publication.Evaluation.Outcome != "review" || publication.Evaluation.Decision != "review" ||
			(expectedEdges == 0 && (len(publication.Edges) != 0 || len(publication.Inventory.Releases) != 0)) ||
			(expectedEdges > 0 && len(publication.Edges) == 0) {
			t.Fatalf("serialize edge-bearing frozen review: outcome=%q decision=%q edges=%d err=%v", publication.Evaluation.Outcome, publication.Evaluation.Decision, len(publication.Edges), err)
		}
		type frozenCoordinate struct {
			releaseID uuid.UUID
			medium    int
			position  int
		}
		coordinates := make(map[uuid.UUID]frozenCoordinate)
		for _, release := range publication.Inventory.Releases {
			for _, track := range release.Tracks {
				coordinates[track.ID] = frozenCoordinate{releaseID: release.ID, medium: track.Medium, position: track.Position}
			}
		}
		for _, edge := range publication.Edges {
			coordinate, ok := coordinates[edge.ProviderTrackID]
			if !ok || coordinate.releaseID != edge.ProviderReleaseID || coordinate.medium != edge.Medium+1 || coordinate.position != edge.Position+1 {
				t.Fatalf("serialized edge is outside the frozen provider graph: edge=%+v coordinate=%+v found=%v", edge, coordinate, ok)
			}
		}
		groupID := publication.Authority.GroupID
		reviewRepository := persistence.NewIncomingMatchCandidateRepository(fixture.db)
		beforeHeader, beforeCandidates, err := reviewRepository.Get(ctx, groupID)
		if err != nil || beforeHeader != nil || len(beforeCandidates) != 0 {
			t.Fatalf("initial frozen review publication state: header=%+v candidates=%d err=%v", beforeHeader, len(beforeCandidates), err)
		}
		assertFrozenReviewPublicationRejected := func(label string, candidate persistence.FrozenMatchingPublication, expected map[uuid.UUID]*string, expectedError string, expectConflict bool) {
			t.Helper()
			if changed, err := fixture.repository.PublishFrozenMatchingEvaluationForDelivery(ctx, runID, 1, handoff.RiverJobID, claim, candidate, expected); err == nil || changed {
				t.Fatalf("%s frozen review publication accepted: changed=%v err=%v", label, changed, err)
			} else if expectConflict {
				if !errors.Is(err, persistence.ErrIncomingMatchConflict) {
					t.Fatalf("%s rejection error = %v, want ErrIncomingMatchConflict", label, err)
				}
			} else if expectedError != "" && !strings.Contains(err.Error(), expectedError) {
				t.Fatalf("%s rejection error = %v, want message containing %q", label, err, expectedError)
			}
			header, candidates, err := reviewRepository.Get(ctx, groupID)
			if err != nil || header != nil || len(candidates) != 0 {
				t.Fatalf("%s rejection wrote review state: header=%+v candidates=%d err=%v", label, header, len(candidates), err)
			}
			var operationState, runState string
			var consumed bool
			if err := fixture.db.NewRaw(`SELECT operation.state,run.state,delivery.consumed_at IS NOT NULL
				FROM operation JOIN matching_run run ON run.id=operation.id JOIN matching_delivery delivery ON delivery.run_id=run.id
				WHERE run.id=? AND delivery.river_job_id=?`, runID, handoff.RiverJobID).Scan(ctx, &operationState, &runState, &consumed); err != nil || operationState != "running" || runState != "running" || consumed {
				t.Fatalf("%s rejection changed active delivery: operation=%q run=%q consumed=%v err=%v", label, operationState, runState, consumed, err)
			}
		}
		wrongDigest := publication
		wrongDigest.Authority.SealDigest = strings.Repeat("0", len(wrongDigest.Authority.SealDigest))
		assertFrozenReviewPublicationRejected("wrong seal digest", wrongDigest, map[uuid.UUID]*string{groupID: nil}, "persisted seal digest does not match caller authority", false)
		wrongWait := publication
		wrongWait.Authority.WaitRevision++
		assertFrozenReviewPublicationRejected("wrong wait revision", wrongWait, map[uuid.UUID]*string{groupID: nil}, "persisted fallback evaluation handoff is not current", false)
		if len(publication.Edges) > 0 {
			missingEdge := publication
			missingEdge.Edges = append([]persistence.FrozenMatchingScoredEdge(nil), publication.Edges[:len(publication.Edges)-1]...)
			assertFrozenReviewPublicationRejected("missing calculation edge", missingEdge, map[uuid.UUID]*string{groupID: nil}, "caller edges do not match the persisted calculation universe", false)
			foreignEdge := publication
			foreignEdge.Edges = append([]persistence.FrozenMatchingScoredEdge(nil), publication.Edges...)
			foreignEdge.Edges[0].FileMemberID = uuid.New()
			assertFrozenReviewPublicationRejected("foreign calculation edge", foreignEdge, map[uuid.UUID]*string{groupID: nil}, "caller edges do not match the persisted calculation universe", false)
			wrongCoordinate := publication
			wrongCoordinate.Edges = append([]persistence.FrozenMatchingScoredEdge(nil), publication.Edges...)
			wrongCoordinate.Edges[0].Position++
			assertFrozenReviewPublicationRejected("substituted edge coordinate", wrongCoordinate, map[uuid.UUID]*string{groupID: nil}, "caller edges do not match the persisted calculation universe", false)
		}
		if len(publication.Edges) > 0 {
			staleCAS := "stale-evaluation-revision"
			assertFrozenReviewPublicationRejected("stale evaluation CAS", publication, map[uuid.UUID]*string{groupID: &staleCAS}, "", true)
		}
		if len(publication.Inventory.Releases) != 0 {
			tamperedLocations := publication
			tamperedLocations.Inventory.Members = append([]persistence.FrozenMatchingReviewMember(nil), publication.Inventory.Members...)
			tamperedLocations.Inventory.Members[0].Locations = json.RawMessage(`[]`)
			assertFrozenReviewPublicationRejected("tampered member locations", tamperedLocations, map[uuid.UUID]*string{groupID: nil}, "frozen target inventory differs from persisted capture", false)
			for _, tamper := range []struct {
				name string
				edit func(*persistence.MatchingRelease)
			}{
				{"title", func(release *persistence.MatchingRelease) { release.Name += " forged" }},
				{"completeness", func(release *persistence.MatchingRelease) { release.Complete = !release.Complete }},
				{"credits", func(release *persistence.MatchingRelease) {
					release.Credits = append(release.Credits, persistence.MatchingReleaseCredit{Position: len(release.Credits), Name: "forged"})
				}},
				{"extra track", func(release *persistence.MatchingRelease) {
					release.Tracks = append(release.Tracks, persistence.MatchingReleaseTrack{ID: uuid.New(), Medium: 99, Position: 99})
				}},
			} {
				candidate := publication
				candidate.Inventory.Releases = append([]persistence.MatchingRelease(nil), publication.Inventory.Releases...)
				candidate.Inventory.Releases[0].Credits = append([]persistence.MatchingReleaseCredit(nil), publication.Inventory.Releases[0].Credits...)
				candidate.Inventory.Releases[0].Tracks = append([]persistence.MatchingReleaseTrack(nil), publication.Inventory.Releases[0].Tracks...)
				tamper.edit(&candidate.Inventory.Releases[0])
				assertFrozenReviewPublicationRejected("tampered provider "+tamper.name, candidate, map[uuid.UUID]*string{groupID: nil}, "review inventory differs from accepted release projections", false)
			}
		}

		changed, err := fixture.repository.PublishFrozenMatchingEvaluationForDelivery(ctx, runID, 1, handoff.RiverJobID, claim, publication, map[uuid.UUID]*string{groupID: nil})
		if err != nil || !changed {
			t.Fatalf("publish frozen fallback review: changed=%v err=%v", changed, err)
		}
		snapshot, err := reviewRepository.GetReviewSnapshot(ctx, groupID)
		if err != nil || snapshot == nil || snapshot.Header == nil || snapshot.Header.FrozenAuthorityVersion == nil || *snapshot.Header.FrozenAuthorityVersion != 3 ||
			snapshot.Header.FrozenRunID == nil || *snapshot.Header.FrozenRunID != runID || snapshot.Header.FrozenWaitRevision == nil || *snapshot.Header.FrozenWaitRevision != waitRevision ||
			fmt.Sprintf("%x", snapshot.Header.FrozenSealDigest) != handoff.SealDigest || len(snapshot.Members) != len(publication.Inventory.Members) {
			t.Fatalf("published frozen review header or inventory mismatch: snapshot=%+v err=%v", snapshot, err)
		}
		var envelope struct {
			FrozenPublication persistence.FrozenMatchingPublication `json:"frozen_publication"`
			ReviewProposal    json.RawMessage                       `json:"review_proposal"`
		}
		if err := json.Unmarshal(snapshot.Header.EvaluationEvidence, &envelope); err != nil || envelope.FrozenPublication.Authority.RunID != runID ||
			envelope.FrozenPublication.Authority.WaitRevision != waitRevision || envelope.FrozenPublication.Authority.SealDigest != handoff.SealDigest ||
			len(envelope.FrozenPublication.Edges) != len(publication.Edges) || len(publication.Edges) > 0 && len(snapshot.Candidates) == 0 {
			t.Fatalf("published review evidence lost frozen graph: evidence=%s edges=%d candidates=%d err=%v", snapshot.Header.EvaluationEvidence, len(publication.Edges), len(snapshot.Candidates), err)
		}
		expectedPublication, err := json.Marshal(publication)
		persistedPublication, marshalErr := json.Marshal(envelope.FrozenPublication)
		if err != nil || marshalErr != nil || !jsonEqual(expectedPublication, persistedPublication) {
			t.Fatalf("published frozen publication differs from the complete source publication: expected=%s actual=%s err=%v marshalErr=%v", expectedPublication, persistedPublication, err, marshalErr)
		}
		if len(snapshot.Members) != len(publication.Inventory.Members) {
			t.Fatalf("published review member scope differs from frozen group: snapshot=%v inventory=%+v", snapshot.Members, publication.Inventory.Members)
		}
		memberIDs := make(map[uuid.UUID]bool, len(publication.Inventory.Members))
		for _, member := range publication.Inventory.Members {
			memberIDs[member.FileMemberID] = true
		}
		for _, memberID := range snapshot.Members {
			if !memberIDs[memberID] {
				t.Fatalf("published review contains a member outside the frozen group: %s", memberID)
			}
			delete(memberIDs, memberID)
		}
		if len(memberIDs) != 0 {
			t.Fatalf("published review omitted frozen group members: %v", memberIDs)
		}
		type proposalLayout struct {
			ProviderReleaseID uuid.UUID        `json:"provider_release_id"`
			ReleaseID         string           `json:"release_id"`
			Title             string           `json:"title"`
			TracklistComplete bool             `json:"tracklist_complete"`
			Positions         []map[string]any `json:"positions"`
		}
		expectedLayouts := make([]proposalLayout, 0, len(publication.Inventory.Releases))
		for _, release := range publication.Inventory.Releases {
			positions := make([]map[string]any, 0, len(release.Tracks))
			for _, track := range release.Tracks {
				positions = append(positions, map[string]any{"track_id": track.ID, "medium": track.Medium, "position": track.Position,
					"displayed_number": track.DisplayedNumber, "recording_id": track.RecordingProviderKey, "title": track.RecordingTitle})
			}
			expectedLayouts = append(expectedLayouts, proposalLayout{ProviderReleaseID: release.ID, ReleaseID: release.ProviderKey,
				Title: release.Name, TracklistComplete: release.Complete, Positions: positions})
		}
		expectedFiles := make([]map[string]any, 0, len(publication.Inventory.Members))
		for _, member := range publication.Inventory.Members {
			var rawTags map[string][]string
			if err := json.Unmarshal(member.Tags, &rawTags); err != nil {
				t.Fatalf("decode frozen review member tags: %v", err)
			}
			expectedFiles = append(expectedFiles, map[string]any{"file_member_id": member.FileMemberID, "raw_tags": rawTags, "diagnostics": []any{}})
		}
		expectedProposal, err := json.Marshal(map[string]any{"version": 1, "group_id": groupID, "selected_release": nil,
			"releases": expectedLayouts, "files": expectedFiles})
		if err != nil || !jsonEqual(expectedProposal, envelope.ReviewProposal) {
			t.Fatalf("published review proposal differs from complete transformed frozen review DTO: expected=%s actual=%s err=%v", expectedProposal, envelope.ReviewProposal, err)
		}
		if len(publication.Edges) == 0 {
			reader := service.NewMatchingReviewService(reviewRepository)
			review, err := reader.Get(ctx, groupID)
			if err != nil || !review.Evaluated || !review.ProposalAvailable || len(review.Releases) != 0 || len(review.Candidates) != 0 {
				t.Fatalf("no-query frozen review was not available from the persisted reader: review=%+v err=%v", review, err)
			}
			var policyVersion string
			if err := fixture.db.NewRaw(`SELECT policy_version FROM incoming_match_evaluation WHERE group_id=?`, groupID).Scan(ctx, &policyVersion); err != nil || policyVersion != service.MatchingPolicyVersion {
				t.Fatalf("frozen header policy version=%q, want approved %q: %v", policyVersion, service.MatchingPolicyVersion, err)
			}
			var operationState, runState string
			var consumed bool
			if err := fixture.db.NewRaw(`SELECT operation.state,run.state,delivery.consumed_at IS NOT NULL
				FROM operation JOIN matching_run run ON run.id=operation.id JOIN matching_delivery delivery ON delivery.run_id=run.id
				WHERE run.id=? AND delivery.river_job_id=?`, runID, handoff.RiverJobID).Scan(ctx, &operationState, &runState, &consumed); err != nil || operationState != "succeeded" || runState != "succeeded" || !consumed {
				t.Fatalf("no-query frozen review settlement: operation=%q run=%q consumed=%v err=%v", operationState, runState, consumed, err)
			}
			return
		}
		expectedCandidateEdges := make(map[string][]persistence.FrozenMatchingScoredEdge)
		for _, edge := range publication.Edges {
			key := edge.FileMemberID.String() + "/" + edge.ProviderReleaseID.String()
			expectedCandidateEdges[key] = append(expectedCandidateEdges[key], edge)
		}
		if len(snapshot.Candidates) != len(expectedCandidateEdges) {
			t.Fatalf("published candidate identities do not cover frozen edges: candidates=%d edgeGroups=%d", len(snapshot.Candidates), len(expectedCandidateEdges))
		}
		for _, candidate := range snapshot.Candidates {
			key := candidate.FileMemberID.String() + "/" + candidate.ReleaseID.String()
			expectedEdges, ok := expectedCandidateEdges[key]
			wantEdges := len(expectedEdges)
			if !ok || len(candidate.Positions) != wantEdges {
				t.Fatalf("published candidate layout is not the frozen edge layout: candidate=%+v expectedEdges=%d found=%v", candidate, wantEdges, ok)
			}
			delete(expectedCandidateEdges, key)
			type positionIdentity struct {
				TrackID  uuid.UUID
				Medium   int
				Position int
			}
			positions := make(map[positionIdentity]bool, len(candidate.Positions))
			for _, position := range candidate.Positions {
				positions[positionIdentity{TrackID: position.TrackID, Medium: position.Medium, Position: position.Position}] = true
			}
			for _, edge := range expectedEdges {
				identity := positionIdentity{TrackID: edge.ProviderTrackID, Medium: edge.Medium + 1, Position: edge.Position + 1}
				if !positions[identity] {
					t.Fatalf("published review layout changed frozen edge coordinates: candidate=%+v expected=%+v positions=%+v", candidate, identity, candidate.Positions)
				}
				delete(positions, identity)
			}
			if len(positions) != 0 {
				t.Fatalf("published review layout contains positions outside frozen edges: candidate=%+v extra=%+v", candidate, positions)
			}
			var candidateEvidence struct {
				EdgeEvidence []struct {
					Medium            int `json:"medium"`
					Position          int `json:"position"`
					AssignmentProfile struct {
						Profile    string  `json:"profile"`
						Confidence float64 `json:"confidence"`
					} `json:"assignment_profile"`
				} `json:"edge_evidence"`
			}
			if err := json.Unmarshal(candidate.Evidence, &candidateEvidence); err != nil || len(candidateEvidence.EdgeEvidence) != wantEdges {
				t.Fatalf("published candidate evidence omitted its frozen edge coordinates: evidence=%s edges=%+v err=%v", candidate.Evidence, candidateEvidence.EdgeEvidence, err)
			}
			var maximumReleaseConfidence float64
			for _, edge := range expectedEdges {
				var releaseScore struct {
					Profile    string  `json:"profile"`
					Confidence float64 `json:"confidence"`
				}
				if err := json.Unmarshal(edge.ReleaseScore, &releaseScore); err != nil || releaseScore.Profile != "release" {
					t.Fatalf("frozen edge has unexpected release score profile: score=%s err=%v", edge.ReleaseScore, err)
				}
				if releaseScore.Confidence > maximumReleaseConfidence {
					maximumReleaseConfidence = releaseScore.Confidence
				}
				var assignmentScore struct {
					Profile    string  `json:"profile"`
					Confidence float64 `json:"confidence"`
				}
				if err := json.Unmarshal(edge.AssignmentScore, &assignmentScore); err != nil || assignmentScore.Profile != "recording_release" {
					t.Fatalf("frozen edge has unexpected assignment score profile: score=%s err=%v", edge.AssignmentScore, err)
				}
				found := false
				for _, actual := range candidateEvidence.EdgeEvidence {
					if actual.Medium == edge.Medium+1 && actual.Position == edge.Position+1 {
						if actual.AssignmentProfile.Profile != "recording_release" || actual.AssignmentProfile.Confidence != assignmentScore.Confidence {
							t.Fatalf("published edge assignment score differs from its frozen edge: actual=%+v expected=%+v", actual.AssignmentProfile, assignmentScore)
						}
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("published candidate evidence changed frozen edge coordinates: candidate=%+v edge=%+v evidence=%+v", candidate, edge, candidateEvidence.EdgeEvidence)
				}
			}
			if candidate.Score != maximumReleaseConfidence {
				t.Fatalf("published candidate release confidence=%v, want maximum frozen release confidence %v", candidate.Score, maximumReleaseConfidence)
			}
		}
		if len(expectedCandidateEdges) != 0 {
			t.Fatalf("published candidates omitted frozen edge groups: %v", expectedCandidateEdges)
		}
		review, err := service.NewMatchingReviewService(reviewRepository).Get(ctx, groupID)
		if err != nil || !review.Evaluated || !review.ProposalAvailable || len(review.Candidates) != len(snapshot.Candidates) {
			t.Fatalf("read score projections through matching review service: review=%+v err=%v", review, err)
		}
		for _, reviewedCandidate := range review.Candidates {
			var maximumReleaseConfidence float64
			for _, edge := range publication.Edges {
				if edge.FileMemberID != reviewedCandidate.FileMemberID || edge.ProviderReleaseID != reviewedCandidate.ReleaseID {
					continue
				}
				var releaseScore struct {
					Profile    string  `json:"profile"`
					Confidence float64 `json:"confidence"`
				}
				if err := json.Unmarshal(edge.ReleaseScore, &releaseScore); err != nil || releaseScore.Profile != "release" {
					t.Fatalf("frozen edge has unexpected release score profile: score=%s err=%v", edge.ReleaseScore, err)
				}
				if releaseScore.Confidence > maximumReleaseConfidence {
					maximumReleaseConfidence = releaseScore.Confidence
				}
			}
			if reviewedCandidate.ReleaseConfidence != maximumReleaseConfidence {
				t.Fatalf("matching review release confidence=%v, want maximum frozen release confidence %v", reviewedCandidate.ReleaseConfidence, maximumReleaseConfidence)
			}
			for _, position := range reviewedCandidate.Positions {
				if position.AssignmentConfidence == nil {
					t.Fatalf("matching review omitted assignment confidence for candidate position: candidate=%+v position=%+v", reviewedCandidate, position)
				}
				var matchedEdge *persistence.FrozenMatchingScoredEdge
				for i := range publication.Edges {
					edge := &publication.Edges[i]
					if edge.FileMemberID == reviewedCandidate.FileMemberID && edge.ProviderReleaseID == reviewedCandidate.ReleaseID &&
						edge.Medium+1 == position.Medium && edge.Position+1 == position.Position && edge.ProviderTrackID == position.TrackID {
						matchedEdge = edge
						break
					}
				}
				if matchedEdge == nil {
					t.Fatalf("matching review position does not identify a frozen edge: candidate=%+v position=%+v", reviewedCandidate, position)
				}
				var assignmentScore struct {
					Profile    string  `json:"profile"`
					Confidence float64 `json:"confidence"`
				}
				if err := json.Unmarshal(matchedEdge.AssignmentScore, &assignmentScore); err != nil || assignmentScore.Profile != "recording_release" || *position.AssignmentConfidence != assignmentScore.Confidence {
					t.Fatalf("matching review assignment confidence differs from frozen edge score: position=%+v score=%s err=%v", position, matchedEdge.AssignmentScore, err)
				}
			}
		}
		var operationState, runState string
		var consumed bool
		if err := fixture.db.NewRaw(`SELECT operation.state,run.state,delivery.consumed_at IS NOT NULL
			FROM operation JOIN matching_run run ON run.id=operation.id JOIN matching_delivery delivery ON delivery.run_id=run.id
			WHERE run.id=? AND delivery.river_job_id=?`, runID, handoff.RiverJobID).Scan(ctx, &operationState, &runState, &consumed); err != nil || operationState != "succeeded" || runState != "succeeded" || !consumed {
			t.Fatalf("published frozen review settlement: operation=%q run=%q consumed=%v err=%v", operationState, runState, consumed, err)
		}
		var applyJobs int
		if err := fixture.db.NewRaw(`SELECT count(*) FROM operation WHERE kind=? AND input_snapshot->>'parent_run_id'=?`, persistence.MatchingApplyOperationKind, runID.String()).Scan(ctx, &applyJobs); err != nil || applyJobs != 0 {
			t.Fatalf("review publication created apply work: jobs=%d err=%v", applyJobs, err)
		}
		var applyRequests, applyOperations, applyRiverJobs int
		if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_apply_request WHERE parent_run_id=?`, runID).Scan(ctx, &applyRequests); err != nil {
			t.Fatalf("count apply requests for published review: %v", err)
		}
		if err := fixture.db.NewRaw(`SELECT count(*) FROM operation WHERE kind=? AND
			(input_snapshot->>'parent_run_id'=? OR input_snapshot->>'run_id'=? OR input_snapshot->>'source_run_id'=? )`,
			persistence.MatchingApplyOperationKind, runID.String(), runID.String(), runID.String()).Scan(ctx, &applyOperations); err != nil {
			t.Fatalf("count apply operations referencing published review: %v", err)
		}
		if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='matching_apply_v1' AND args->>'operation_id' IN
			(SELECT id::text FROM operation WHERE kind=?)`, persistence.MatchingApplyOperationKind).Scan(ctx, &applyRiverJobs); err != nil {
			t.Fatalf("count apply River jobs for published review: %v", err)
		}
		if applyRequests != 0 || applyOperations != 0 || applyRiverJobs != 0 {
			t.Fatalf("review publication left apply request or child work: requests=%d operations=%d riverJobs=%d", applyRequests, applyOperations, applyRiverJobs)
		}
		return
	}
	wakeCalls = inserter.wakeCalls
	replayed, found, err := fixture.repository.ContinueMatchingFallbackSeal(ctx, inserter, runID, 1, waitRevision)
	if err != nil || !found || replayed != handoff || inserter.wakeCalls != wakeCalls {
		t.Fatalf("exact replay changed handoff or inserted another wake: replay=%+v original=%+v found=%v wakeCalls=%d/%d err=%v", replayed, handoff, found, inserter.wakeCalls, wakeCalls, err)
	}
	// A historical handoff remains readable, but a later target generation must
	// not authorize replaying its continuation. Mutate the persisted generation
	// directly to model a subsequent admission without coupling this test to its
	// unrelated scheduling machinery.
	var groupID uuid.UUID
	var generation int64
	if err := fixture.db.NewRaw(`SELECT generation.group_id,generation.high_water FROM incoming_matching_generation generation
		JOIN matching_run_target target ON target.group_id=generation.group_id WHERE target.run_id=?`, runID).
		Scan(ctx, &groupID, &generation); err != nil {
		t.Fatalf("read current target generation: %v", err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE incoming_matching_generation SET high_water=high_water+1 WHERE group_id=?`, groupID).Exec(ctx); err != nil {
		t.Fatalf("supersede target generation: %v", err)
	}
	before = snapshotFallbackSealWriteState(t, ctx, fixture.db, runID)
	readRows()
	initialSeals, initialHandoffs = seals, handoffs
	wakeCalls = inserter.wakeCalls
	if _, found, err := fixture.repository.ContinueMatchingFallbackSeal(ctx, inserter, runID, 1, waitRevision); err == nil || found || !strings.Contains(err.Error(), "current target authority changed") {
		t.Fatalf("superseded target generation authorized replay: found=%v err=%v", found, err)
	}
	assertNoWrites("superseded target-generation replay")
	if inserter.wakeCalls != wakeCalls {
		t.Fatalf("superseded target-generation replay inserted wake: calls=%d want=%d", inserter.wakeCalls, wakeCalls)
	}
	if _, err := fixture.db.NewRaw(`UPDATE incoming_matching_generation SET high_water=? WHERE group_id=?`, generation, groupID).Exec(ctx); err != nil {
		t.Fatalf("restore target generation after replay check: %v", err)
	}
	// Model an owner-authorized manual-mode change after the handoff. The
	// authority row is normally created by manual confirmation; this fixture
	// changes only that persisted authority, leaving the historical seal intact.
	if _, err := fixture.db.NewRaw(`INSERT INTO incoming_group_matching_authority(group_id,mode,revision,updated_at)
		VALUES (?,'manual',1,now()) ON CONFLICT (group_id) DO UPDATE SET mode='manual',revision=incoming_group_matching_authority.revision+1,updated_at=now()`, groupID).Exec(ctx); err != nil {
		t.Fatalf("promote target group to manual authority: %v", err)
	}
	before = snapshotFallbackSealWriteState(t, ctx, fixture.db, runID)
	readRows()
	initialSeals, initialHandoffs = seals, handoffs
	wakeCalls = inserter.wakeCalls
	if _, found, err := fixture.repository.ContinueMatchingFallbackSeal(ctx, inserter, runID, 1, waitRevision); err == nil || found || !strings.Contains(err.Error(), "current group authority changed") {
		t.Fatalf("manual target authority authorized replay: found=%v err=%v", found, err)
	}
	assertNoWrites("manual-authority replay")
	if inserter.wakeCalls != wakeCalls {
		t.Fatalf("manual-authority replay inserted wake: calls=%d want=%d", inserter.wakeCalls, wakeCalls)
	}
	seal, exists, err = fixture.repository.GetMatchingFallbackAuthoritySeal(ctx, runID, waitRevision)
	if err != nil || !exists || seal.Digest != handoff.SealDigest {
		t.Fatalf("read historical seal after manual authority change: seal=%+v exists=%v err=%v", seal, exists, err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE app_setting SET setting_value='stale-provider-configuration' WHERE setting_name='musicbrainz_config_identity'`).Exec(ctx); err != nil {
		t.Fatalf("change current provider configuration: %v", err)
	}
	before = snapshotFallbackSealWriteState(t, ctx, fixture.db, runID)
	readRows()
	initialSeals, initialHandoffs = seals, handoffs
	wakeCalls = inserter.wakeCalls
	if _, found, err := fixture.repository.ContinueMatchingFallbackSeal(ctx, inserter, runID, 1, waitRevision); err == nil || found || !strings.Contains(err.Error(), "provider configuration changed") {
		t.Fatalf("stale provider configuration authorized replay: found=%v err=%v", found, err)
	}
	assertNoWrites("stale provider configuration replay")
	if inserter.wakeCalls != wakeCalls {
		t.Fatalf("stale provider configuration replay inserted wake: calls=%d want=%d", inserter.wakeCalls, wakeCalls)
	}
	if _, found, err := fixture.repository.ContinueMatchingFallbackSeal(ctx, inserter, runID, 1, waitRevision-1); err == nil || found {
		t.Fatalf("stale replay revision authorized an unrelated wait: found=%v err=%v", found, err)
	}
}

func assertFallbackAuthoritySealImmutable(t *testing.T, ctx context.Context, database *bun.DB, runID uuid.UUID, revision int64, digest string) {
	t.Helper()
	for _, mutation := range []struct {
		name  string
		query string
	}{
		{name: "update", query: `UPDATE matching_fallback_authority_seal SET authority='{}'::jsonb WHERE run_id=? AND wait_revision=?`},
		{name: "delete", query: `DELETE FROM matching_fallback_authority_seal WHERE run_id=? AND wait_revision=?`},
	} {
		if _, err := database.NewRaw(mutation.query, runID, revision).Exec(ctx); err == nil {
			t.Fatalf("fallback authority seal %s was accepted", mutation.name)
		} else {
			var postgresErr *pgconn.PgError
			if !errors.As(err, &postgresErr) || postgresErr.SQLState() != "P0001" {
				t.Fatalf("fallback authority seal %s failed with unexpected error: %v", mutation.name, err)
			}
		}
		var retainedDigest []byte
		if err := database.NewRaw(`SELECT authority_digest FROM matching_fallback_authority_seal WHERE run_id=? AND wait_revision=?`, runID, revision).Scan(ctx, &retainedDigest); err != nil || fmt.Sprintf("%x", retainedDigest) != digest {
			t.Fatalf("fallback authority seal changed after rejected %s: digest=%x err=%v", mutation.name, retainedDigest, err)
		}
	}
}
