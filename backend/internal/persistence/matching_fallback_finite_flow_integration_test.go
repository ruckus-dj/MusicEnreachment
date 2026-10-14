//go:build integration

package persistence_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestAutomaticFallbackUniquePublicationCreatesOneFrozenApplyRequestPostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t, false, false, false, true, false, true)
}

type fallbackSealWriteState struct {
	Phase          string
	Revision       int64
	Operation      string
	OperationStage string
	RunState       string
	Deliveries     int
	RiverJobs      int
	Dependencies   int
	Registrations  int
}

func snapshotFallbackSealWriteState(t *testing.T, ctx context.Context, database *bun.DB, runID uuid.UUID) fallbackSealWriteState {
	t.Helper()
	var state fallbackSealWriteState
	if err := database.NewRaw(`SELECT current_phase,revision FROM matching_acquisition WHERE run_id=?`, runID).
		Scan(ctx, &state.Phase, &state.Revision); err != nil {
		t.Fatalf("read fallback seal acquisition cursor: %v", err)
	}
	if err := database.NewRaw(`SELECT operation.state,operation.stage,run.state
		FROM operation JOIN matching_run run ON run.id=operation.id WHERE operation.id=?`, runID).
		Scan(ctx, &state.Operation, &state.OperationStage, &state.RunState); err != nil {
		t.Fatalf("read fallback continuation owner state: %v", err)
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

func testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t *testing.T, historicalStageLookup, emptyPlan, exhaustPlan, continueSeal, publishReview bool, publishUnique ...bool) {
	testAutomaticFallbackFiniteFlowWithPublicationCallback(t, historicalStageLookup, emptyPlan, exhaustPlan, continueSeal, publishReview, nil, publishUnique...)
}

type uniquePublicationApplyFixture struct {
	Context           context.Context
	Database          *bun.DB
	Repository        *persistence.SetupManagerRepository
	ParentOperationID uuid.UUID
	ChildOperation    persistence.Operation
	Source            persistence.MatchingApplySnapshotSourceV2
	InputSnapshot     persistence.MatchingApplyInputSnapshotV2
}

func testAutomaticFallbackFiniteFlowWithPublicationCallback(t *testing.T, historicalStageLookup, emptyPlan, exhaustPlan, continueSeal, publishReview bool, afterPublication func(uniquePublicationApplyFixture), publishUnique ...bool) {
	testAutomaticFallbackFiniteFlowWithRecoveryCallback(t, historicalStageLookup, emptyPlan, exhaustPlan, continueSeal, publishReview, afterPublication, nil, publishUnique...)
}

func testAutomaticFallbackFiniteFlowWithRecoveryCallback(t *testing.T, historicalStageLookup, emptyPlan, exhaustPlan, continueSeal, publishReview bool, afterPublication func(uniquePublicationApplyFixture), beforePublication func(*testing.T, context.Context, *bun.DB, *persistence.SetupManagerRepository, *fallbackHandoffRiverInserter, uuid.UUID, int64, *jobs.MatchingEvaluationWorker), publishUnique ...bool) {
	testAutomaticFallbackFiniteFlowWithDatabase(t, historicalStageLookup, emptyPlan, exhaustPlan, continueSeal, publishReview, afterPublication, beforePublication, nil, nil, publishUnique...)
}

func testAutomaticFallbackFiniteFlowWithDatabase(t *testing.T, historicalStageLookup, emptyPlan, exhaustPlan, continueSeal, publishReview bool, afterPublication func(uniquePublicationApplyFixture), beforePublication func(*testing.T, context.Context, *bun.DB, *persistence.SetupManagerRepository, *fallbackHandoffRiverInserter, uuid.UUID, int64, *jobs.MatchingEvaluationWorker), database *bun.DB, beforeWorkerClaim func(*testing.T, context.Context, *bun.DB, uuid.UUID, int64, int64) bool, publishUnique ...bool) {
	if len(publishUnique) < 3 || publishUnique[2] {
		t.Parallel()
	}
	uniquePublication := len(publishUnique) > 0 && publishUnique[0]
	rollbackEmptySettlement := len(publishUnique) > 1 && publishUnique[1]
	fixture := newAutomaticMatchingTargetFenceFixtureWithDatabase(t, database)
	beforeWorkerClaimExecuted := false
	checkpoint := func(ctx context.Context, db *bun.DB, runID uuid.UUID, waitRevision, wakeID int64) bool {
		beforeWorkerClaimExecuted = true
		if beforeWorkerClaim == nil {
			return true
		}
		return beforeWorkerClaim(t, ctx, db, runID, waitRevision, wakeID)
	}
	ctx, cancel := context.WithTimeout(fixture.ctx, 60*time.Second)
	defer cancel()

	metadata := `{"ALBUM":["Captured Album"],"TITLE":["Receipt Track"]}`
	if emptyPlan {
		metadata = `{}`
	}
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
		if rollbackEmptySettlement {
			before := snapshotFallbackSealWriteState(t, ctx, fixture.db, parentID)
			if _, err := fixture.db.ExecContext(ctx, `CREATE FUNCTION reject_test_fallback_handoff() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test handoff insert failure'; END $$`); err != nil {
				t.Fatalf("create handoff rollback fault: %v", err)
			}
			if _, err := fixture.db.ExecContext(ctx, `CREATE TRIGGER reject_test_fallback_handoff BEFORE INSERT ON matching_fallback_evaluation_handoff FOR EACH ROW EXECUTE FUNCTION reject_test_fallback_handoff()`); err != nil {
				t.Fatalf("install handoff rollback fault: %v", err)
			}
			if changed, handled, advanceErr := acquisition.Advance(ctx, parentID, 1, wakeID, claim); advanceErr == nil || changed || !handled || !strings.Contains(advanceErr.Error(), "test handoff insert failure") {
				t.Fatalf("injected empty-plan handoff failure: changed=%v handled=%v err=%v", changed, handled, advanceErr)
			}
			if _, err := fixture.db.ExecContext(ctx, `DROP TRIGGER reject_test_fallback_handoff ON matching_fallback_evaluation_handoff`); err != nil {
				t.Fatalf("remove handoff rollback fault: %v", err)
			}
			if _, err := fixture.db.ExecContext(ctx, `DROP FUNCTION reject_test_fallback_handoff()`); err != nil {
				t.Fatalf("drop handoff rollback fault: %v", err)
			}
			if after := snapshotFallbackSealWriteState(t, ctx, fixture.db, parentID); after != before {
				t.Fatalf("empty-plan settlement failure partially committed: before=%+v after=%+v", before, after)
			}
			var consumed bool
			if err := fixture.db.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, parentID, wakeID).Scan(ctx, &consumed); err != nil || consumed {
				t.Fatalf("failed empty-plan settlement consumed the original claim: consumed=%v err=%v", consumed, err)
			}
			var seals, handoffs int
			if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_authority_seal WHERE run_id=?`, parentID).Scan(ctx, &seals); err != nil {
				t.Fatalf("read seals after failed empty-plan settlement: %v", err)
			}
			if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_evaluation_handoff WHERE run_id=?`, parentID).Scan(ctx, &handoffs); err != nil || seals != 0 || handoffs != 0 {
				t.Fatalf("failed empty-plan settlement left seal or handoff rows: seals=%d handoffs=%d err=%v", seals, handoffs, err)
			}
		}
		changed, handled, err := acquisition.Advance(ctx, parentID, 1, wakeID, claim)
		if err != nil || !handled || !changed {
			t.Fatalf("complete empty fallback plan: changed=%v handled=%v err=%v", changed, handled, err)
		}
		parked, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
		if err != nil || !exists || parked.Phase != persistence.MatchingAcquisitionFallbackEvaluationSealed {
			t.Fatalf("empty fallback plan did not seal its evaluation: snapshot=%+v exists=%v err=%v", parked, exists, err)
		}
		waitRevision := parked.Revision - 1
		var waitOutput []byte
		if err := fixture.db.NewRaw(`SELECT output FROM matching_acquisition_phase WHERE run_id=? AND revision=?`, parentID, waitRevision).Scan(ctx, &waitOutput); err != nil {
			t.Fatalf("read historical empty-plan review evidence: %v", err)
		}
		var evidence struct {
			Reason   string            `json:"reason"`
			Receipts []json.RawMessage `json:"calculation_receipts"`
		}
		if err := json.Unmarshal(waitOutput, &evidence); err != nil || evidence.Reason != "fallback_plan_empty" || len(evidence.Receipts) != 0 {
			t.Fatalf("empty fallback review evidence is invalid: output=%s evidence=%+v err=%v", waitOutput, evidence, err)
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
		if len(parked.Intent.InitialProviderInputs) != 0 || len(parked.Intent.CapturedMembers) == 0 || dependencyCount != 0 || registrationCount != 0 || calculationCount != 0 || phaseWakeCount != 2 {
			t.Fatalf("empty plan fabricated provider work or lost its original/continuation delivery: inputs=%d members=%d dependencies=%d registrations=%d calculations=%d deliveries=%d", len(parked.Intent.InitialProviderInputs), len(parked.Intent.CapturedMembers), dependencyCount, registrationCount, calculationCount, phaseWakeCount)
		}
		authority, found, err := fixture.repository.GetMatchingFallbackAuthoritySeal(ctx, parentID, waitRevision)
		if err != nil || !found {
			t.Fatalf("read no-query fallback authority seal: authority=%+v found=%v err=%v", authority, found, err)
		}
		var authorityPlan policy.TypedFallbackQueryPlan
		if err := json.Unmarshal(authority.Plan, &authorityPlan); err != nil || authority.SchemaVersion != 3 || authority.Disposition != "no_query" ||
			authority.RunID != parentID || authority.WaitRevision != waitRevision || len(authorityPlan.Stages) != 0 ||
			authority.SourceID == uuid.Nil || authority.ProviderID == uuid.Nil || authority.Endpoint == "" || authority.SourceConfiguration == "" ||
			len(authority.CapturedMetadata) != len(parked.Intent.CapturedMembers) || len(authority.ProofHistory) != 0 ||
			authority.SelectedCalculation != nil || len(authority.FullEvaluationContext) != 0 || authority.Digest == "" {
			t.Fatalf("no-query authority omitted captured intent or fabricated calculation evidence: authority=%+v plan=%+v err=%v", authority, authorityPlan, err)
		}
		readSeal, readFound, err := fixture.repository.GetMatchingFallbackAuthoritySeal(ctx, parentID, waitRevision)
		if err != nil || !readFound || readSeal.Digest != authority.Digest || readSeal.Disposition != "no_query" {
			t.Fatalf("read persisted no-query authority: authority=%+v found=%v err=%v", readSeal, readFound, err)
		}
		assertFallbackAuthoritySealImmutable(t, ctx, fixture.db, parentID, waitRevision, authority.Digest)
		originalAuthority := authority
		if _, err := fixture.db.NewRaw(`UPDATE provider_source SET endpoint=?,configuration_identity=? WHERE id=?`, "https://changed.invalid/ws/2", "changed-configuration", authority.SourceID).Exec(ctx); err != nil {
			t.Fatalf("change mutable provider source registry row: %v", err)
		}
		refreshedAuthority, found, err := fixture.repository.GetMatchingFallbackAuthoritySeal(ctx, parentID, waitRevision)
		if err != nil || !found || refreshedAuthority.Digest != originalAuthority.Digest ||
			refreshedAuthority.SourceID != originalAuthority.SourceID || refreshedAuthority.ProviderID != originalAuthority.ProviderID ||
			refreshedAuthority.Endpoint != originalAuthority.Endpoint || refreshedAuthority.SourceConfiguration != originalAuthority.SourceConfiguration {
			t.Fatalf("no-query authority changed with mutable provider source registry: before=%+v after=%+v found=%v err=%v", originalAuthority, refreshedAuthority, found, err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE provider_source SET endpoint=?,configuration_identity=? WHERE id=?`, originalAuthority.Endpoint, originalAuthority.SourceConfiguration, originalAuthority.SourceID).Exec(ctx); err != nil {
			t.Fatalf("restore frozen provider source after authority check: %v", err)
		}
		assertFallbackEvaluationContinuation(t, ctx, fixture, inserter, parentID, waitRevision, publishReview, checkpoint)
		if beforeWorkerClaim != nil && !beforeWorkerClaimExecuted {
			t.Fatal("pre-claim checkpoint was not executed")
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
		if err != nil || !exists || parked.Phase != persistence.MatchingAcquisitionFallbackEvaluationSealed {
			t.Fatalf("three-stage exhausted plan did not seal its evaluation: snapshot=%+v exists=%v err=%v", parked, exists, err)
		}
		waitRevision := parked.Revision - 1
		var waitOutput []byte
		if err := fixture.db.NewRaw(`SELECT output FROM matching_acquisition_phase WHERE run_id=? AND revision=?`, parentID, waitRevision).Scan(ctx, &waitOutput); err != nil {
			t.Fatalf("read historical exhausted wait evidence: %v", err)
		}
		var evidence struct {
			Reason   string `json:"reason"`
			Receipts []struct {
				StageIndex int    `json:"stage_index"`
				Digest     string `json:"input_digest"`
			} `json:"calculation_receipts"`
		}
		if err := json.Unmarshal(waitOutput, &evidence); err != nil || evidence.Reason != "review_required" || len(evidence.Receipts) != 3 {
			t.Fatalf("exhaustion receipt manifest is incomplete: output=%s evidence=%+v err=%v", waitOutput, evidence, err)
		}
		for index, receipt := range evidence.Receipts {
			if receipt.StageIndex != index || receipt.Digest == "" {
				t.Fatalf("calculation receipt order or binding is invalid: %+v", evidence.Receipts)
			}
		}
		authority, found, err := fixture.repository.GetMatchingFallbackAuthoritySeal(ctx, parentID, waitRevision)
		if err != nil || !found {
			t.Fatalf("read exhausted fallback authority seal: authority=%+v found=%v err=%v", authority, found, err)
		}
		var authorityPlan policy.TypedFallbackQueryPlan
		if err := json.Unmarshal(authority.Plan, &authorityPlan); err != nil || authority.SchemaVersion != 3 || authority.Disposition != "review_exhausted" ||
			authority.RunID != parentID || authority.WaitRevision != waitRevision || len(authorityPlan.Stages) != 3 ||
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
		readSeal, readFound, err := fixture.repository.GetMatchingFallbackAuthoritySeal(ctx, parentID, waitRevision)
		if err != nil || !readFound || readSeal.Digest != authority.Digest || readSeal.Disposition != "review_exhausted" ||
			readSeal.SelectedCalculation == nil || readSeal.SelectedCalculation.StageIndex != 2 || len(readSeal.FullEvaluationContext) == 0 {
			t.Fatalf("read persisted exhausted authority: authority=%+v found=%v err=%v", readSeal, readFound, err)
		}
		assertFallbackEvaluationContinuation(t, ctx, fixture, inserter, parentID, waitRevision, publishReview, checkpoint)
		if beforeWorkerClaim != nil && !beforeWorkerClaimExecuted {
			t.Fatal("pre-claim checkpoint was not executed")
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
	if historicalStageLookup {
		expectedDependencies, expectedRegistrations, expectedLookupJobs = 4, 4, 2
	} else if publishReview {
		expectedDependencies, expectedRegistrations, expectedLookupJobs = 4, 4, 2
	}
	if inputCount != 1 || dependencyCount != expectedDependencies || registrationCount != expectedRegistrations || searchJobCount != 0 || lookupJobCount != expectedLookupJobs {
		t.Fatalf("finite flow wrote unexpected request/dependency work: initialInputs=%d dependencies=%d registrations=%d stage2Jobs=%d lookupJobs=%d", inputCount, dependencyCount, registrationCount, searchJobCount, lookupJobCount)
	}
	if beforePublication != nil {
		// Keep existing callers on the historical discarded-state behavior while
		// allowing the recovery matrix to exercise each terminal/missing wake.
		beforePublication(t, ctx, fixture.db, fixture.repository, inserter, parentID, waitRevision, matchingWorker)
	}
	if !uniquePublication {
		assertFallbackEvaluationContinuation(t, ctx, fixture, inserter, parentID, waitRevision, publishReview, checkpoint)
		if beforeWorkerClaim != nil && !beforeWorkerClaimExecuted {
			t.Fatal("pre-claim checkpoint was not executed")
		}
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

func assertClaimedSealedWakeRecovery(
	t *testing.T,
	ctx context.Context,
	database *bun.DB,
	repository *persistence.SetupManagerRepository,
	inserter *fallbackHandoffRiverInserter,
	runID uuid.UUID,
	waitRevision int64,
	worker *jobs.MatchingEvaluationWorker,
) {
	assertClaimedSealedWakeRecoveryWithState(t, ctx, database, repository, inserter, runID, waitRevision, worker)
}

func assertClaimedSealedWakeRecoveryWithState(
	t *testing.T,
	ctx context.Context,
	database *bun.DB,
	repository *persistence.SetupManagerRepository,
	inserter *fallbackHandoffRiverInserter,
	runID uuid.UUID,
	waitRevision int64,
	worker *jobs.MatchingEvaluationWorker,
	deadStates ...string,
) {
	deadState := "discarded"
	if len(deadStates) > 0 && deadStates[0] != "" {
		deadState = deadStates[0]
	}
	assertClaimedSealedWakeRecoveryWithStateAndHook(t, ctx, database, repository, inserter, runID, waitRevision, worker, deadState, nil)
}

func assertClaimedSealedWakeRecoveryWithStateAndHook(
	t *testing.T,
	ctx context.Context,
	database *bun.DB,
	repository *persistence.SetupManagerRepository,
	inserter *fallbackHandoffRiverInserter,
	runID uuid.UUID,
	waitRevision int64,
	worker *jobs.MatchingEvaluationWorker,
	deadState string,
	afterReplacement func(),
) {
	t.Helper()
	// The fixture uses real worker instances for explicit delivery calls, but
	// leave newly inserted replacement jobs under test control instead of letting
	// River race the assertions with an asynchronous poll.
	if stopper, ok := inserter.client.(interface{ Stop(context.Context) error }); ok {
		if err := stopper.Stop(ctx); err != nil {
			t.Fatalf("stop automatic River polling for deterministic recovery assertions: %v", err)
		}
	}
	if deadState != "discarded" && deadState != "completed" && deadState != "cancelled" && deadState != "missing" {
		t.Fatalf("unsupported dead wake state %q", deadState)
	}
	var handoff struct {
		Digest []byte `bun:"seal_digest"`
		JobID  int64  `bun:"river_job_id"`
		Wait   int64  `bun:"wait_revision"`
	}
	if err := database.NewRaw(`SELECT seal_digest,river_job_id,wait_revision FROM matching_fallback_evaluation_handoff WHERE run_id=?`, runID).Scan(ctx, &handoff); err != nil {
		t.Fatalf("read original immutable fallback handoff: %v", err)
	}
	var before struct {
		Epoch int64 `bun:"execution_epoch"`
		JobID int64 `bun:"current_river_job_id"`
	}
	if err := database.NewRaw(`SELECT execution_epoch,current_river_job_id FROM matching_run WHERE id=?`, runID).Scan(ctx, &before); err != nil || before.JobID != handoff.JobID || handoff.Wait != waitRevision {
		t.Fatalf("read original sealed execution identity: run=%+v handoff=%+v err=%v", before, handoff, err)
	}
	var originalArgs []byte
	if err := database.NewRaw(`SELECT args FROM river_job WHERE id=?`, handoff.JobID).Scan(ctx, &originalArgs); err != nil {
		t.Fatalf("read original wake args: %v", err)
	}
	var originalJobArgs map[string]json.RawMessage
	var originalOperationID uuid.UUID
	if err := json.Unmarshal(originalArgs, &originalJobArgs); err != nil || len(originalJobArgs) != 1 || originalJobArgs["operation_id"] == nil || json.Unmarshal(originalJobArgs["operation_id"], &originalOperationID) != nil || originalOperationID != runID {
		t.Fatalf("original River wake args are not exactly operation-ID-only: args=%s operation=%s err=%v", originalArgs, originalOperationID, err)
	}
	originalOperation, err := repository.GetOperation(ctx, runID)
	if err != nil {
		t.Fatalf("read original parent operation snapshot: %v", err)
	}
	originalInput := append([]byte(nil), originalOperation.InputSnapshot...)
	var originalAcquisitionIntent, originalRunPlan []byte
	var originalSourceID uuid.UUID
	var originalRunConfiguration string
	if err := database.NewRaw(`SELECT acquisition.intent,run.immutable_plan,run.provider_source_id,run.provider_configuration_identity
		FROM matching_acquisition acquisition JOIN matching_run run ON run.id=acquisition.run_id WHERE run.id=?`, runID).
		Scan(ctx, &originalAcquisitionIntent, &originalRunPlan, &originalSourceID, &originalRunConfiguration); err != nil {
		t.Fatalf("read original parent intent, source, and finite plan: %v", err)
	}
	for _, state := range []string{"available", "pending", "retryable", "running", "scheduled"} {
		if _, err := database.NewRaw(`UPDATE river_job SET state=? WHERE id=?`, state, handoff.JobID).Exec(ctx); err != nil {
			t.Fatalf("set original wake live state %q: %v", state, err)
		}
		if err := repository.RecoverMatchingRuntime(ctx, nil); err != nil {
			t.Fatalf("sweep live original wake in state %q: %v", state, err)
		}
		var jobState string
		var current struct {
			Epoch int64 `bun:"execution_epoch"`
			JobID int64 `bun:"current_river_job_id"`
		}
		if err := database.NewRaw(`SELECT state FROM river_job WHERE id=?`, handoff.JobID).Scan(ctx, &jobState); err != nil {
			t.Fatalf("read live original wake state %q: %v", state, err)
		}
		if err := database.NewRaw(`SELECT execution_epoch,current_river_job_id FROM matching_run WHERE id=?`, runID).Scan(ctx, &current); err != nil || current != before || jobState != state {
			t.Fatalf("scanner changed live original wake state %q: run=%+v want=%+v jobState=%q err=%v", state, current, before, jobState, err)
		}
	}
	if _, err := database.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, handoff.JobID).Exec(ctx); err != nil {
		t.Fatalf("claimable original continuation wake: %v", err)
	}
	oldClaim, outcome, err := repository.ClaimMatchingDelivery(ctx, runID, 1, handoff.JobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed || oldClaim.RiverAttempt != 1 {
		t.Fatalf("claim original sealed wake before simulated worker loss: claim=%+v outcome=%v err=%v", oldClaim, outcome, err)
	}
	var claimedJob, claimedEpoch int64
	if err := database.NewRaw(`SELECT active_river_job_id,active_execution_epoch FROM matching_run WHERE id=?`, runID).Scan(ctx, &claimedJob, &claimedEpoch); err != nil || claimedJob != handoff.JobID || claimedEpoch != oldClaim.ExecutionEpoch {
		t.Fatalf("real delivery claim did not persist active execution fence: job=%d epoch=%d claim=%+v err=%v", claimedJob, claimedEpoch, oldClaim, err)
	}
	// Claiming advances the execution epoch; use that durable identity for the
	// live-work sweep assertion and later recovery expectation.
	before.Epoch = claimedEpoch
	// A durable running claim is live work, not a stale delivery. Sweep before
	// simulating worker loss and ensure neither the claim nor its fence changes.
	if err := repository.RecoverMatchingRuntime(ctx, nil); err != nil {
		t.Fatalf("sweep durably claimed running wake: %v", err)
	}
	var liveState string
	var liveRun struct {
		Epoch int64 `bun:"execution_epoch"`
		JobID int64 `bun:"current_river_job_id"`
	}
	if err := database.NewRaw(`SELECT state FROM river_job WHERE id=?`, handoff.JobID).Scan(ctx, &liveState); err != nil {
		t.Fatalf("read claimed wake state after sweep: %v", err)
	}
	if err := database.NewRaw(`SELECT execution_epoch,current_river_job_id FROM matching_run WHERE id=?`, runID).Scan(ctx, &liveRun); err != nil || liveRun != before || liveState != "running" {
		t.Fatalf("recovery swept a live claimed delivery: run=%+v want=%+v state=%q err=%v", liveRun, before, liveState, err)
	}
	// Model a worker that was durably claimed and then lost: River owns the
	// terminal state, while the active claim remains as stale durable evidence.
	if deadState == "missing" {
		if _, err := database.NewRaw(`DELETE FROM river_job WHERE id=?`, handoff.JobID).Exec(ctx); err != nil {
			t.Fatalf("remove lost original continuation delivery: %v", err)
		}
	} else if _, err := database.NewRaw(`UPDATE river_job SET state=?,finalized_at=now() WHERE id=?`, deadState, handoff.JobID).Exec(ctx); err != nil {
		t.Fatalf("mark lost original continuation delivery %q: %v", deadState, err)
	}
	var sweep sync.WaitGroup
	sweepErrors := make(chan error, 2)
	for range 2 {
		sweep.Add(1)
		go func() {
			defer sweep.Done()
			sweepErrors <- repository.RecoverMatchingRuntime(ctx, nil)
		}()
	}
	sweep.Wait()
	close(sweepErrors)
	for err := range sweepErrors {
		if err != nil {
			t.Fatalf("concurrent recovery sweep: %v", err)
		}
	}
	var replaced struct {
		Epoch int64 `bun:"execution_epoch"`
		JobID int64 `bun:"current_river_job_id"`
	}
	if err := database.NewRaw(`SELECT execution_epoch,current_river_job_id FROM matching_run WHERE id=?`, runID).Scan(ctx, &replaced); err != nil {
		t.Fatalf("read recovered execution fence: %v", err)
	}
	operation, err := repository.GetOperation(ctx, runID)
	if err != nil || (operation.State != "queued" && operation.State != "running") || operation.Stage != "queued" || operation.RiverJobID == nil || *operation.RiverJobID != replaced.JobID {
		t.Fatalf("recovery did not install current queued operation delivery: operation=%+v err=%v", operation, err)
	}
	var replacementRegistrations int
	if err := database.NewRaw(`SELECT count(*) FROM matching_delivery delivery JOIN river_job job ON job.id=delivery.river_job_id
		WHERE delivery.run_id=? AND delivery.operation_attempt=1 AND delivery.river_job_id=? AND delivery.consumed_at IS NULL
		AND job.args->>'operation_id'=? AND job.state IN ('available','pending','retryable','scheduled')`, runID, replaced.JobID, runID.String()).Scan(ctx, &replacementRegistrations); err != nil || replacementRegistrations != 1 {
		t.Fatalf("recovered replacement lacks exactly one live registered operation-ID-only delivery: count=%d err=%v", replacementRegistrations, err)
	}
	if replaced.Epoch != before.Epoch+1 || replaced.JobID < 1 || replaced.JobID == handoff.JobID {
		t.Fatalf("recovery did not advance exactly one execution epoch: before=%+v after=%+v", before, replaced)
	}
	var activeJob, activeEpoch *int64
	if err := database.NewRaw(`SELECT active_river_job_id,active_execution_epoch FROM matching_run WHERE id=?`, runID).Scan(ctx, &activeJob, &activeEpoch); err != nil || (activeJob != nil && (*activeJob != replaced.JobID || activeEpoch == nil || *activeEpoch != replaced.Epoch)) {
		t.Fatalf("recovery retained an unrelated active claim: job=%v epoch=%v replacement=%+v err=%v", activeJob, activeEpoch, replaced, err)
	}
	if err := repository.RecoverMatchingRuntime(ctx, nil); err != nil {
		t.Fatalf("repeat sealed continuation recovery: %v", err)
	}
	var afterRepeat struct {
		Epoch int64 `bun:"execution_epoch"`
		JobID int64 `bun:"current_river_job_id"`
	}
	if err := database.NewRaw(`SELECT execution_epoch,current_river_job_id FROM matching_run WHERE id=?`, runID).Scan(ctx, &afterRepeat); err != nil || afterRepeat != replaced {
		t.Fatalf("second sweep changed already recovered delivery: before=%+v after=%+v err=%v", replaced, afterRepeat, err)
	}
	var persistedHandoff struct {
		Digest []byte `bun:"seal_digest"`
		JobID  int64  `bun:"river_job_id"`
		Wait   int64  `bun:"wait_revision"`
	}
	if err := database.NewRaw(`SELECT seal_digest,river_job_id,wait_revision FROM matching_fallback_evaluation_handoff WHERE run_id=?`, runID).Scan(ctx, &persistedHandoff); err != nil || persistedHandoff.JobID != handoff.JobID || persistedHandoff.Wait != waitRevision || !bytes.Equal(persistedHandoff.Digest, handoff.Digest) {
		t.Fatalf("recovery rewrote immutable original handoff: before=%+v after=%+v err=%v", handoff, persistedHandoff, err)
	}
	var operationID uuid.UUID
	var args []byte
	if err := database.NewRaw(`SELECT operation.id,river_job.args FROM operation JOIN river_job ON river_job.id=operation.river_job_id WHERE operation.id=? AND river_job.id=?`, runID, replaced.JobID).Scan(ctx, &operationID, &args); err != nil {
		t.Fatalf("read installed replacement job registration: %v", err)
	}
	var jobArgs map[string]json.RawMessage
	var replacementOperationID uuid.UUID
	if err := json.Unmarshal(args, &jobArgs); err != nil || len(jobArgs) != 1 || jobArgs["operation_id"] == nil ||
		json.Unmarshal(jobArgs["operation_id"], &replacementOperationID) != nil || operationID != runID || replacementOperationID != runID {
		t.Fatalf("replacement River args are not operation-ID-only: operation=%s args=%s err=%v", operationID, args, err)
	}
	if current, err := repository.IsMatchingDeliveryCurrent(ctx, runID, 1, handoff.JobID, oldClaim); err != nil || current {
		t.Fatalf("old claimed delivery remains current after epoch advance: current=%v err=%v", current, err)
	}
	if afterReplacement != nil {
		afterReplacement()
	}
	if err := worker.Work(ctx, &river.Job[service.MatchingEvaluationOperationArgs]{JobRow: &rivertype.JobRow{ID: handoff.JobID, Attempt: 1}, Args: service.MatchingEvaluationOperationArgs{OperationID: runID}}); err != nil {
		t.Fatalf("old terminal worker delivery was not rejected as stale: %v", err)
	}
	prepared, exists, err := service.PrepareFrozenFallbackEvaluation(ctx, repository, runID, waitRevision)
	if err != nil || !exists {
		t.Fatalf("prepare original sealed result for stale-publication rejection: exists=%v err=%v", exists, err)
	}
	publication, err := service.SerializeFrozenFallbackEvaluation(prepared)
	if err != nil {
		t.Fatalf("serialize original frozen evaluation: %v", err)
	}
	if changed, publishErr := repository.PublishFrozenMatchingEvaluationForDelivery(ctx, runID, 1, handoff.JobID, oldClaim, publication, map[uuid.UUID]*string{publication.Inventory.GroupID: nil}); publishErr == nil || changed || !strings.Contains(publishErr.Error(), "operation delivery is no longer current") {
		t.Fatalf("old claimed publication was not specifically fenced: changed=%v err=%v", changed, publishErr)
	}
	var evaluationHeaders, evaluationMembers, candidates, applyRequests, children, receipts int
	readPublicationArtifactCounts := func() [15]int {
		t.Helper()
		counts := [15]int{}
		queries := []struct {
			query string
			args  []any
		}{
			{`SELECT count(*) FROM incoming_match_evaluation`, nil},
			{`SELECT count(*) FROM incoming_match_evaluation_member`, nil},
			{`SELECT count(*) FROM incoming_match_candidate`, nil},
			{`SELECT count(*) FROM matching_apply_request`, nil},
			{`SELECT count(*) FROM matching_apply_receipt`, nil},
			{`SELECT count(*) FROM operation WHERE kind=?`, []any{persistence.MatchingApplyOperationKind}},
			{`SELECT count(*) FROM river_job WHERE kind=?`, []any{(persistence.MatchingApplyOperationArgs{}).Kind()}},
			{`SELECT count(*) FROM track_source`, nil},
			{`SELECT count(*) FROM provider_local_track`, nil},
			{`SELECT count(*) FROM provider_local_release`, nil},
			{`SELECT count(*) FROM local_release`, nil},
			{`SELECT count(*) FROM local_recording`, nil},
			{`SELECT count(*) FROM local_track`, nil},
			{`SELECT count(*) FROM local_artist`, nil},
			{`SELECT count(*) FROM incoming_match_evaluation WHERE group_id=?`, []any{publication.Inventory.GroupID}},
		}
		for index, check := range queries {
			if err := database.NewRaw(check.query, check.args...).Scan(ctx, &counts[index]); err != nil {
				t.Fatalf("read publication artifact count %d: %v", index, err)
			}
		}
		return counts
	}
	artifactsBeforeRejectedPublish := readPublicationArtifactCounts()
	var currentCandidatesBefore int
	if err := database.NewRaw(`SELECT count(*) FROM incoming_match_candidate WHERE group_id=?`, publication.Inventory.GroupID).Scan(ctx, &currentCandidatesBefore); err != nil {
		t.Fatalf("read current candidates before rejected stale publication: %v", err)
	}
	var currentHeaderRevisionBefore string
	if err := database.NewRaw(`SELECT COALESCE((SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=?), '')`, publication.Inventory.GroupID).Scan(ctx, &currentHeaderRevisionBefore); err != nil {
		t.Fatalf("read current header revision before rejected stale publication: %v", err)
	}
	for _, check := range []struct {
		query       string
		args        []any
		destination *int
	}{
		{`SELECT count(*) FROM incoming_match_evaluation WHERE group_id=?`, []any{publication.Inventory.GroupID}, &evaluationHeaders},
		{`SELECT count(*) FROM incoming_match_evaluation_member WHERE group_id=?`, []any{publication.Inventory.GroupID}, &evaluationMembers},
		{`SELECT count(*) FROM incoming_match_candidate WHERE group_id=?`, []any{publication.Inventory.GroupID}, &candidates},
		{`SELECT count(*) FROM matching_apply_request WHERE parent_run_id=?`, []any{runID}, &applyRequests},
		{`SELECT count(*) FROM operation WHERE kind=? AND id IN (SELECT apply_operation_id FROM matching_apply_request WHERE parent_run_id=?)`, []any{persistence.MatchingApplyOperationKind, runID}, &children},
		{`SELECT count(*) FROM matching_apply_receipt WHERE apply_operation_id IN (SELECT apply_operation_id FROM matching_apply_request WHERE parent_run_id=?)`, []any{runID}, &receipts},
	} {
		if err := database.NewRaw(check.query, check.args...).Scan(ctx, check.destination); err != nil {
			t.Fatalf("check stale publication side effects: %v", err)
		}
	}
	if evaluationHeaders != 0 || evaluationMembers != 0 || candidates != 0 || applyRequests != 0 || children != 0 || receipts != 0 {
		t.Fatalf("stale publication wrote evaluation or apply artifacts: headers=%d members=%d candidates=%d requests=%d children=%d receipts=%d", evaluationHeaders, evaluationMembers, candidates, applyRequests, children, receipts)
	}
	artifactsAfterRejectedPublish := readPublicationArtifactCounts()
	var currentCandidatesAfter int
	if err := database.NewRaw(`SELECT count(*) FROM incoming_match_candidate WHERE group_id=?`, publication.Inventory.GroupID).Scan(ctx, &currentCandidatesAfter); err != nil {
		t.Fatalf("read current candidates after rejected stale publication: %v", err)
	}
	var currentHeaderRevisionAfter string
	if err := database.NewRaw(`SELECT COALESCE((SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=?), '')`, publication.Inventory.GroupID).Scan(ctx, &currentHeaderRevisionAfter); err != nil {
		t.Fatalf("read current header revision after rejected stale publication: %v", err)
	}
	if artifactsAfterRejectedPublish != artifactsBeforeRejectedPublish || currentCandidatesAfter != currentCandidatesBefore || currentHeaderRevisionAfter != currentHeaderRevisionBefore {
		t.Fatalf("stale publication changed independently counted artifacts, current header revision, or candidates: before=%v after=%v revision=%q->%q candidates=%d->%d", artifactsBeforeRejectedPublish, artifactsAfterRejectedPublish, currentHeaderRevisionBefore, currentHeaderRevisionAfter, currentCandidatesBefore, currentCandidatesAfter)
	}
	if _, err := database.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, replaced.JobID).Exec(ctx); err != nil {
		t.Fatalf("claim recovered replacement delivery: %v", err)
	}
	if err := worker.Work(ctx, &river.Job[service.MatchingEvaluationOperationArgs]{JobRow: &rivertype.JobRow{ID: replaced.JobID, Attempt: 1}, Args: service.MatchingEvaluationOperationArgs{OperationID: runID}}); err != nil {
		t.Fatalf("execute recovered replacement through real matching worker: %v", err)
	}
	var operationState, runState string
	if err := database.NewRaw(`SELECT operation.state,run.state FROM operation JOIN matching_run run ON run.id=operation.id WHERE operation.id=?`, runID).Scan(ctx, &operationState, &runState); err != nil || operationState != "succeeded" || runState != "succeeded" {
		t.Fatalf("replacement worker did not publish the unique frozen result: operation=%q run=%q err=%v", operationState, runState, err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM matching_apply_request WHERE parent_run_id=?`, runID).Scan(ctx, &applyRequests); err != nil || applyRequests != 1 {
		t.Fatalf("replacement worker did not publish exactly one unique apply request: count=%d err=%v", applyRequests, err)
	}
	currentOperation, err := repository.GetOperation(ctx, runID)
	if err != nil || !bytes.Equal(currentOperation.InputSnapshot, originalInput) {
		t.Fatalf("replacement publication changed original parent request input: original=%s current=%+v err=%v", originalInput, currentOperation, err)
	}
	var currentAcquisitionIntent, currentRunPlan []byte
	var currentSourceID uuid.UUID
	var currentRunConfiguration string
	if err := database.NewRaw(`SELECT acquisition.intent,run.immutable_plan,run.provider_source_id,run.provider_configuration_identity
		FROM matching_acquisition acquisition JOIN matching_run run ON run.id=acquisition.run_id WHERE run.id=?`, runID).
		Scan(ctx, &currentAcquisitionIntent, &currentRunPlan, &currentSourceID, &currentRunConfiguration); err != nil ||
		!bytes.Equal(currentAcquisitionIntent, originalAcquisitionIntent) || !bytes.Equal(currentRunPlan, originalRunPlan) || currentSourceID != originalSourceID || currentRunConfiguration != originalRunConfiguration {
		t.Fatalf("replacement publication changed frozen parent intent/source/plan: source=%s->%s config=%q->%q err=%v", originalSourceID, currentSourceID, originalRunConfiguration, currentRunConfiguration, err)
	}
	var finalHandoff struct {
		Digest []byte `bun:"seal_digest"`
		JobID  int64  `bun:"river_job_id"`
		Wait   int64  `bun:"wait_revision"`
	}
	if err := database.NewRaw(`SELECT seal_digest,river_job_id,wait_revision FROM matching_fallback_evaluation_handoff WHERE run_id=?`, runID).Scan(ctx, &finalHandoff); err != nil ||
		finalHandoff.JobID != handoff.JobID || finalHandoff.Wait != waitRevision || !bytes.Equal(finalHandoff.Digest, handoff.Digest) {
		t.Fatalf("replacement publication changed original frozen run/wait/seal handoff: before=%+v after=%+v err=%v", handoff, finalHandoff, err)
	}
	artifactsAfterReplacementPublish := readPublicationArtifactCounts()
	if artifactsAfterReplacementPublish[7] != artifactsBeforeRejectedPublish[7] || artifactsAfterReplacementPublish[8] != artifactsBeforeRejectedPublish[8] || artifactsAfterReplacementPublish[9] != artifactsBeforeRejectedPublish[9] ||
		artifactsAfterReplacementPublish[10] != artifactsBeforeRejectedPublish[10] || artifactsAfterReplacementPublish[11] != artifactsBeforeRejectedPublish[11] || artifactsAfterReplacementPublish[12] != artifactsBeforeRejectedPublish[12] || artifactsAfterReplacementPublish[13] != artifactsBeforeRejectedPublish[13] {
		t.Fatalf("evaluation parent materialized or linked local entities before registered child execution: before=%v after=%v", artifactsBeforeRejectedPublish, artifactsAfterReplacementPublish)
	}
	var header struct {
		Revision string    `bun:"evaluation_revision"`
		RunID    uuid.UUID `bun:"target_run_id"`
		Evidence []byte    `bun:"evaluation_evidence"`
		Fences   []byte    `bun:"provider_fences"`
	}
	if err := database.NewRaw(`SELECT evaluation_revision,target_run_id,evaluation_evidence,provider_fences FROM incoming_match_evaluation WHERE group_id=?`, publication.Inventory.GroupID).
		Scan(ctx, &header.Revision, &header.RunID, &header.Evidence, &header.Fences); err != nil || header.Revision == "" || header.RunID != runID {
		t.Fatalf("replacement publication lacks a unique current header bound to its frozen run: header=%+v err=%v", header, err)
	}
	var headerEvidence struct {
		Publication struct {
			Evaluation struct {
				Outcome string `json:"outcome"`
			} `json:"evaluation"`
		} `json:"frozen_publication"`
	}
	var headerFences struct {
		RunID        uuid.UUID `json:"run_id"`
		WaitRevision int64     `json:"wait_revision"`
		SealDigest   string    `json:"seal_digest"`
	}
	if err := json.Unmarshal(header.Evidence, &headerEvidence); err != nil || json.Unmarshal(header.Fences, &headerFences) != nil || headerEvidence.Publication.Evaluation.Outcome != "unique" ||
		headerFences.RunID != runID || headerFences.WaitRevision != waitRevision || headerFences.SealDigest != fmt.Sprintf("%x", handoff.Digest) {
		t.Fatalf("replacement header does not retain unique outcome and actual root/wait/seal authority: evidence=%s fences=%s publication=%+v fencesDecoded=%+v err=%v", header.Evidence, header.Fences, headerEvidence.Publication, headerFences, err)
	}
	var replacementConsumed int
	if err := database.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND river_job_id=? AND consumed_at IS NOT NULL`, runID, replaced.JobID).Scan(ctx, &replacementConsumed); err != nil || replacementConsumed != 1 {
		t.Fatalf("replacement matching delivery was not consumed exactly once: count=%d err=%v", replacementConsumed, err)
	}
	var applyRequest struct {
		OperationID uuid.UUID `bun:"apply_operation_id"`
		Schema      int       `bun:"schema_version"`
		ParentEpoch int64     `bun:"parent_execution_epoch"`
		InputSHA    []byte    `bun:"input_sha256"`
	}
	if err := database.NewRaw(`SELECT apply_operation_id,schema_version,parent_execution_epoch,input_sha256 FROM matching_apply_request WHERE parent_run_id=?`, runID).
		Scan(ctx, &applyRequest.OperationID, &applyRequest.Schema, &applyRequest.ParentEpoch, &applyRequest.InputSHA); err != nil {
		t.Fatalf("read replacement apply request: %v", err)
	}
	var replacementEpoch int64
	if err := database.NewRaw(`SELECT execution_epoch FROM matching_run WHERE id=?`, runID).Scan(ctx, &replacementEpoch); err != nil || applyRequest.Schema != 2 || applyRequest.ParentEpoch != replacementEpoch || replacementEpoch != replaced.Epoch+1 || len(applyRequest.InputSHA) != sha256.Size {
		t.Fatalf("replacement request is not bound to actual replacement claim and canonical input digest: request=%+v replacement=%+v err=%v", applyRequest, replaced, err)
	}
	var child persistence.Operation
	if err := database.NewSelect().Model(&child).Where("id = ?", applyRequest.OperationID).Scan(ctx); err != nil || child.Kind != persistence.MatchingApplyOperationKind || child.RiverJobID == nil {
		t.Fatalf("read registered schema-2 replacement child: child=%+v err=%v", child, err)
	}
	childSnapshot, err := persistence.DecodeMatchingApplyInputSnapshotV2(child.InputSnapshot)
	if err != nil || childSnapshot.SchemaVersion != 2 || childSnapshot.Authority.ParentRunID != runID || len(childSnapshot.Assignments) != 2 {
		t.Fatalf("replacement child is not the validated canonical schema-2 request: snapshot=%+v err=%v", childSnapshot, err)
	}
	canonicalChildDigest, err := persistence.MatchingApplyInputSnapshotV2Digest(childSnapshot)
	if err != nil || canonicalChildDigest != fmt.Sprintf("%x", applyRequest.InputSHA) {
		t.Fatalf("replacement child canonical digest does not match request binding; decoded snapshot fields differ from the canonical request (not merely JSONB ordering/whitespace): computed=%s stored=%x err=%v", canonicalChildDigest, applyRequest.InputSHA, err)
	}
	var registeredApplyJobs int
	if err := database.NewRaw(`SELECT count(*) FROM river_job WHERE id=? AND kind=? AND args->>'operation_id'=?`, *child.RiverJobID, (persistence.MatchingApplyOperationArgs{}).Kind(), child.ID.String()).Scan(ctx, &registeredApplyJobs); err != nil || registeredApplyJobs != 1 {
		t.Fatalf("replacement child is not registered with exactly one operation-ID-only apply job: count=%d err=%v", registeredApplyJobs, err)
	}
}

func assertFallbackEvaluationContinuation(t *testing.T, ctx context.Context, fixture automaticTargetFenceFixture, inserter *fallbackHandoffRiverInserter, runID uuid.UUID, waitRevision int64, publishReview bool, beforeWorkerClaim func(context.Context, *bun.DB, uuid.UUID, int64, int64) bool) {
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
	if initialSeals != 1 || initialHandoffs != 1 {
		t.Fatalf("service advancement did not atomically create one seal and handoff: seals=%d handoffs=%d", initialSeals, initialHandoffs)
	}
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
	wakeCalls := inserter.wakeCalls
	handoff, found, err := fixture.repository.ContinueMatchingFallbackSeal(ctx, inserter, runID, 1, waitRevision)
	if err != nil || !found || handoff.RunID != runID || handoff.WaitRevision != waitRevision || handoff.OperationAttempt != 1 || handoff.RiverJobID < 1 || handoff.SealDigest == "" {
		t.Fatalf("replay service-created fallback continuation: handoff=%+v found=%v err=%v", handoff, found, err)
	}
	if inserter.wakeCalls != wakeCalls {
		t.Fatalf("exact continuation replay inserted another wake: calls=%d want=%d", inserter.wakeCalls-wakeCalls, 0)
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
	var wakeState string
	if err := fixture.db.NewRaw(`SELECT state FROM river_job WHERE id=?`, handoff.RiverJobID).Scan(ctx, &wakeState); err != nil || wakeState != "available" {
		t.Fatalf("registered continuation wake is not available before worker claim: state=%q err=%v", wakeState, err)
	}
	if beforeWorkerClaim != nil {
		if !beforeWorkerClaim(ctx, fixture.db, runID, waitRevision, handoff.RiverJobID) {
			return
		}
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
