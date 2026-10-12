//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t, false, false, false)
}

func TestAutomaticFallbackHistoricalStageLookupsDoNotBlockStageOnePostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t, true, false, false)
}

func TestAutomaticFallbackEmptyPlanParksForReviewPostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t, false, true, false)
}

func TestAutomaticFallbackFiniteThreeStageExhaustionParksForReviewPostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t, false, false, true)
}

func testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t *testing.T, historicalStageLookup, emptyPlan, exhaustPlan bool) {
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
		stageTwoPayload := json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":1,"offset":0,"recordings":[{"id":"90000000-0000-4000-8000-0000000000f7","title":"Unrelated Recording","releases":[]}]}`)
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
		if err != nil || !exists || stageTwoApplicability.Phase != persistence.MatchingAcquisitionFallbackApplicability {
			t.Fatalf("final-stage search did not reach applicability: snapshot=%+v exists=%v err=%v", stageTwoApplicability, exists, err)
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
			len(fullContext.LookupReceipts) != 0 || !jsonEqual(fullContext.SearchReceipt.Payload, stageTwoPayload) ||
			len(fullContext.Intent.CapturedMembers) != len(parked.Intent.CapturedMembers) {
			t.Fatalf("exhausted authority dropped the actual final-stage inventory/evidence: context=%+v err=%v", fullContext, err)
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
	}
	if inputCount != 1 || dependencyCount != expectedDependencies || registrationCount != expectedRegistrations || searchJobCount != 0 || lookupJobCount != expectedLookupJobs {
		t.Fatalf("finite flow wrote unexpected request/dependency work: initialInputs=%d dependencies=%d registrations=%d stage2Jobs=%d lookupJobs=%d", inputCount, dependencyCount, registrationCount, searchJobCount, lookupJobCount)
	}
}
