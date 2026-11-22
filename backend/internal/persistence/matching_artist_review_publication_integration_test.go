//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"testing"
	"time"
)

func TestArtistMatchingKeepsAcceptedResponseAfterCacheReplacement(t *testing.T) {
	t.Parallel()
	accepted := json.RawMessage(`{"count":0,"offset":0,"created":"2025-01-01T00:00:00Z","artists":[]}`)
	run := prepareAutomaticArtistReviewDelivery(t, accepted)
	snapshot, found, err := run.repository.GetMatchingAcquisition(run.ctx, run.admission.OriginalRunID)
	if err != nil || !found || len(snapshot.Intent.InitialProviderInputs) != 1 {
		t.Fatalf("read accepted artist run: found=%t err=%v", found, err)
	}
	request := snapshot.Intent.InitialProviderInputs[0]
	if _, err := run.db.NewRaw(`UPDATE provider_response_cache SET payload=?::jsonb,revision='artist-search-r2'
		WHERE provider_source_id=? AND cache_key=?`, `{"count":1}`, run.source.source, request.CacheKey).Exec(run.ctx); err != nil {
		t.Fatal(err)
	}
	receipt, err := run.repository.GetMatchingArtistResponse(run.ctx, run.admission.OriginalRunID, request.OperationID)
	if err != nil || receipt == nil || !sameJSON(receipt.Payload, accepted) {
		t.Fatalf("cache replacement changed accepted artist response: receipt=%+v err=%v", receipt, err)
	}
	if _, err := run.db.NewRaw(`UPDATE matching_provider_dependency SET accepted_artist_response=NULL
		WHERE parent_run_id=? AND request_id=?`, run.admission.OriginalRunID, request.OperationID).Exec(run.ctx); err == nil {
		t.Fatal("accepted artist response was mutable")
	}
	changed, acquired, err := run.acquisition.Advance(run.ctx, run.admission.OriginalRunID, 1, run.jobID, run.claim)
	if !errors.Is(err, persistence.ErrIncomingMatchConflict) || changed || !acquired {
		t.Fatalf("reject stale empty search after cache replacement: changed=%t acquired=%t err=%v", changed, acquired, err)
	}
	snapshot, found, err = run.repository.GetMatchingAcquisition(run.ctx, run.admission.OriginalRunID)
	if err != nil || !found || snapshot.Phase != "admitted" {
		t.Fatalf("stale search advanced the acquisition: phase=%q found=%t err=%v", snapshot.Phase, found, err)
	}
}

// TestAutomaticArtistTerminalOutageWakesOnlyAdmittedGeneration proves that a
// terminal provider outage issues exactly one new reconciliation obligation for
// the run's admitted generation and that a later independent request cannot
// consume that stale outage.
func TestAutomaticArtistTerminalOutageWakesOnlyAdmittedGeneration(t *testing.T) {
	t.Parallel()
	run := prepareAutomaticArtistOutageDelivery(t)
	groupID := run.source.group.ID
	var admitted int64
	if err := run.db.NewRaw(`SELECT reconciliation_generation FROM matching_run_target WHERE run_id=? AND group_id=?`,
		run.admission.OriginalRunID, groupID).Scan(run.ctx, &admitted); err != nil || admitted < 1 {
		t.Fatalf("read admitted target generation: generation=%d err=%v", admitted, err)
	}
	var requested int64
	if err := run.db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, groupID).Scan(run.ctx, &requested); err != nil {
		t.Fatalf("read admitted request generation: %v", err)
	}
	if requested != admitted {
		t.Fatalf("fixture request generation = %d, admitted target generation = %d", requested, admitted)
	}
	var pendingBefore int
	if err := run.db.NewRaw(`SELECT count(*) FROM incoming_matching_reconcile_event WHERE reason='current' AND state='pending'`).Scan(run.ctx, &pendingBefore); err != nil {
		t.Fatalf("count pending current wakes before outage: %v", err)
	}
	if pendingBefore != 0 {
		t.Fatalf("fixture already had %d pending current wakes before the outage", pendingBefore)
	}

	changed, err := run.repository.FailMatchingDelivery(run.ctx, run.admission.OriginalRunID, 1, run.jobID, run.claim,
		"MusicBrainz is temporarily unavailable.")
	if err != nil || !changed {
		t.Fatalf("fail terminal artist delivery: changed=%t err=%v", changed, err)
	}

	state := struct {
		Requested int64   `bun:"requested_generation"`
		State     string  `bun:"state"`
		Stage     string  `bun:"stage"`
		SafeError *string `bun:"safe_error"`
	}{}
	if err := run.db.NewRaw(`SELECT generation.requested_generation,parent.state,parent.stage,parent.safe_error
		FROM incoming_matching_generation generation
		JOIN operation parent ON parent.id=?
		WHERE generation.group_id=?`, run.admission.OriginalRunID, groupID).Scan(run.ctx, &state); err != nil {
		t.Fatalf("read terminal outage state: %v", err)
	}
	if state.Requested != admitted+1 {
		t.Fatalf("terminal outage moved request generation to %d, want %d", state.Requested, admitted+1)
	}
	if state.State != "failed" || state.Stage != "local_fallback_pending" || state.SafeError == nil || *state.SafeError == "" {
		t.Fatalf("terminal outage parent = state %q stage %q safe_error %v", state.State, state.Stage, state.SafeError)
	}
	var pendingAfter int
	if err := run.db.NewRaw(`SELECT count(*) FROM incoming_matching_reconcile_event WHERE reason='current' AND state='pending'`).Scan(run.ctx, &pendingAfter); err != nil {
		t.Fatalf("count pending current wakes after outage: %v", err)
	}
	if pendingAfter != 1 {
		t.Fatalf("terminal outage pending current wakes = %d, want 1", pendingAfter)
	}

	outages := persistence.NewIncomingMatchCandidateRepository(run.db)
	outageRunID, err := outages.CurrentMatchingMusicBrainzOutage(run.ctx, groupID)
	if err != nil || outageRunID != run.admission.OriginalRunID {
		t.Fatalf("outage evidence = %s err=%v, want %s", outageRunID, err, run.admission.OriginalRunID)
	}
	fallbackRunID, err := outages.CurrentMatchingMusicBrainzOutageFallback(run.ctx, groupID, admitted+1)
	if err != nil || fallbackRunID != run.admission.OriginalRunID {
		t.Fatalf("admitted fallback generation run = %s err=%v, want %s", fallbackRunID, err, run.admission.OriginalRunID)
	}

	// A later independent request advances beyond the dedicated fallback
	// generation without consuming or refreshing the original outage.
	if err := run.db.RunInTx(run.ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return persistence.RequestIncomingMatchingReconciliationWith(ctx, tx, []uuid.UUID{groupID})
	}); err != nil {
		t.Fatalf("request later reconciliation generation: %v", err)
	}
	var later int64
	if err := run.db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, groupID).Scan(run.ctx, &later); err != nil {
		t.Fatalf("read later request generation: %v", err)
	}
	if later != admitted+2 {
		t.Fatalf("later request generation = %d, want %d", later, admitted+2)
	}
	fallbackRunID, err = outages.CurrentMatchingMusicBrainzOutageFallback(run.ctx, groupID, later)
	if err != nil || fallbackRunID != uuid.Nil {
		t.Fatalf("later request consumed stale outage: run=%s err=%v", fallbackRunID, err)
	}
	outageRunID, err = outages.CurrentMatchingMusicBrainzOutage(run.ctx, groupID)
	if err != nil || outageRunID != run.admission.OriginalRunID {
		t.Fatalf("later request changed outage evidence: run=%s err=%v", outageRunID, err)
	}
}

// This provider exposes only a previously accepted cache response. In
// particular, acquisition never makes a provider request while advancing.
type artistReviewCachedProvider struct {
	cache    *persistence.ProviderCacheRepository
	sourceID uuid.UUID
}

func (provider artistReviewCachedProvider) CachedResponse(ctx context.Context, key string) (*persistence.ProviderCachedResponse, error) {
	return nil, fmt.Errorf("unexpected mutable cache read for %s", key)
}

func (artistReviewCachedProvider) PrepareOperation(context.Context, string, string, int, int, bool) (*persistence.Operation, error) {
	return nil, fmt.Errorf("unexpected provider browse preparation")
}

func automaticArtistReviewOwnershipState(t *testing.T, ctx context.Context, db *bun.DB, operationID uuid.UUID) string {
	t.Helper()
	var state struct {
		OperationState            string `bun:"operation_state"`
		OperationStage            string `bun:"operation_stage"`
		RunState                  string `bun:"run_state"`
		AcquisitionPhase          string `bun:"acquisition_phase"`
		PhaseName                 string `bun:"phase_name"`
		Reason                    string `bun:"reason"`
		ProvenanceCount           int    `bun:"provenance_count"`
		CurrentRun                bool   `bun:"current_run"`
		JobBinding                bool   `bun:"job_binding"`
		GenerationMatch           bool   `bun:"generation_match"`
		RequestedMatch            bool   `bun:"requested_match"`
		AuthorityRevision         int64  `bun:"authority_revision"`
		CapturedAuthorityRevision int64  `bun:"captured_authority_revision"`
		DependencyReady           bool   `bun:"dependency_ready"`
		DeliveryConsumed          bool   `bun:"delivery_consumed"`
	}
	err := db.NewRaw(`SELECT operation.state AS operation_state,operation.stage AS operation_stage,run.state AS run_state,
		acquisition.current_phase AS acquisition_phase,phase.phase AS phase_name,phase.output->>'reason' AS reason,
		jsonb_array_length(phase.response_provenance) AS provenance_count,
		generation.current_run_id=run.id AS current_run,
		run.current_river_job_id=operation.river_job_id AS job_binding,
		generation.high_water=target.target_generation AS generation_match,
		(target.reconciliation_generation IS NULL OR generation.requested_generation=target.reconciliation_generation) AS requested_match,
		evaluation.matching_authority_revision AS authority_revision,
		(target.target_capture->>'authority_revision')::bigint AS captured_authority_revision,
		EXISTS (SELECT 1 FROM matching_provider_dependency dependency WHERE dependency.parent_run_id=run.id AND dependency.terminal_outcome='succeeded') AS dependency_ready,
		EXISTS (SELECT 1 FROM matching_delivery delivery WHERE delivery.run_id=run.id AND delivery.river_job_id=run.current_river_job_id AND delivery.consumed_at IS NOT NULL) AS delivery_consumed
		FROM matching_run run JOIN operation ON operation.id=run.operation_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		JOIN matching_acquisition_phase phase ON phase.run_id=run.id AND phase.revision=acquisition.revision
		JOIN matching_run_target target ON target.run_id=run.id
		JOIN incoming_match_evaluation evaluation ON evaluation.group_id=target.group_id
		JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
		WHERE operation.id=?`, operationID).Scan(ctx, &state)
	if err != nil {
		t.Fatalf("read bounded automatic artist ownership diagnostics: %v", err)
	}
	return fmt.Sprintf("op=%s/%s run=%s phase=%s/%s reason=%s provenance=%d current=%t job=%t generation=%t request=%t authority=%d/%d dependency=%t consumed=%t",
		state.OperationState, state.OperationStage, state.RunState, state.AcquisitionPhase, state.PhaseName, state.Reason,
		state.ProvenanceCount, state.CurrentRun, state.JobBinding, state.GenerationMatch, state.RequestedMatch,
		state.AuthorityRevision, state.CapturedAuthorityRevision, state.DependencyReady, state.DeliveryConsumed)
}

type automaticArtistReviewDelivery struct {
	ctx         context.Context
	db          *bun.DB
	source      *incomingMatchFixture
	repository  *persistence.SetupManagerRepository
	cache       *persistence.ProviderCacheRepository
	admission   persistence.AutomaticArtistMatchingAdmissionResult
	jobID       int64
	claim       persistence.MatchingDeliveryClaim
	acquisition *service.MatchingAcquisitionService
}

func prepareAutomaticArtistReviewDelivery(t *testing.T, payload json.RawMessage) *automaticArtistReviewDelivery {
	t.Helper()
	return prepareAutomaticArtistDelivery(t, payload, false)
}

func prepareAutomaticArtistOutageDelivery(t *testing.T) *automaticArtistReviewDelivery {
	t.Helper()
	return prepareAutomaticArtistDelivery(t, nil, true)
}

func prepareAutomaticArtistDelivery(t *testing.T, payload json.RawMessage, outage bool) *automaticArtistReviewDelivery {
	t.Helper()
	ctx := context.Background()
	db := testpostgres.OpenMigratedWithRiver(t)
	sourceFixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewSetupManagerRepository(db)
	inventory := persistence.NewSourceInventoryRepository(db)
	client := openScanEnqueueRiver(t, db)

	var event persistence.IncomingMatchingReconcileEvent
	if err := db.NewSelect().Model(&event).Where("reason = ?", "analysis_success").Order("created_at ASC").Order("id ASC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read analysis-success reconciliation event: %v", err)
	}
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch reconciliation event: %v", err)
	}
	requests, err := inventory.ReadPendingCurrentRequests(ctx)
	if err != nil {
		t.Fatalf("read observed current matching requests: %v", err)
	}
	var requestGeneration int64
	for _, request := range requests {
		if request.GroupID == sourceFixture.group.ID {
			requestGeneration = request.RequestedGeneration
			break
		}
	}
	if requestGeneration < 1 {
		t.Fatalf("fixture current request generation = %d, want an observed positive generation", requestGeneration)
	}
	dispatched, err := inventory.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil || dispatched.RegisteredJobID == nil {
		t.Fatalf("read dispatched reconciliation event: event=%+v err=%v", dispatched, err)
	}
	epoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *dispatched.RegisteredJobID, 1)
	if err != nil || !claimed {
		t.Fatalf("claim reconciliation delivery: epoch=%d claimed=%v err=%v", epoch, claimed, err)
	}
	var source struct {
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := db.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, sourceFixture.source).Scan(ctx, &source); err != nil {
		t.Fatalf("read provider source fence: %v", err)
	}
	query := json.RawMessage(`{"schema_version":1,"query":"Fixture Artist","offset":0,"limit":10}`)
	cacheKey := "artist-search:0:10:Fixture Artist"
	parent := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	providerOperation := &persistence.Operation{
		ID: uuid.New(), Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
		ProviderSourceID: &sourceFixture.source, ProviderID: &source.ProviderID,
		ProviderConfigurationIdentity: &source.Identity, ProviderEndpoint: source.Endpoint,
		ProviderCacheKey: &cacheKey, Attempt: 1,
	}
	admission, err := repository.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parent.ID},
		Provider: persistence.MatchingProviderRequest{Operation: providerOperation, Args: service.ProviderFetchOperationArgs{OperationID: providerOperation.ID}},
		GroupID:  sourceFixture.group.ID, ExpectedGroupRevision: sourceFixture.group.Revision,
		PolicyVersion: "matching-rapidfuzz-v1", ArtistQueryPlanVersion: 1, QueryDescriptor: query,
		EventID: event.ID, EventRiverJobID: *dispatched.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: epoch,
		RequestGeneration: requestGeneration,
	})
	if err != nil {
		t.Fatalf("admit real automatic artist matching run: %v", err)
	}
	if admission.Operation == nil || admission.Operation.RiverJobID == nil || providerOperation.RiverJobID == nil {
		t.Fatal("automatic admission did not return registered operations")
	}
	providerJobID := *providerOperation.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, providerJobID).Exec(ctx); err != nil {
		t.Fatalf("prepare provider River delivery: %v", err)
	}
	providerEpoch, providerClaimed, err := repository.ClaimProviderOperationDelivery(ctx, providerOperation.ID, 1, providerJobID)
	if err != nil || !providerClaimed {
		t.Fatalf("claim provider operation delivery: epoch=%d claimed=%v err=%v", providerEpoch, providerClaimed, err)
	}
	if changed, err := repository.TransitionProviderOperationForDelivery(ctx, providerOperation.ID, 1, providerJobID, providerEpoch, func(operation *persistence.Operation) error {
		operation.State, operation.Stage = "running", "fetching"
		return nil
	}); err != nil || !changed {
		t.Fatalf("start provider operation delivery: changed=%v err=%v", changed, err)
	}
	cache := persistence.NewProviderCacheRepository(db)
	if outage {
		if changed, err := repository.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, providerOperation.ID, 1, providerJobID, providerEpoch, client, func(operation *persistence.Operation) error {
			message := "MusicBrainz is temporarily unavailable."
			operation.State, operation.Stage, operation.SafeError = "failed", persistence.MusicBrainzUnavailableStage, &message
			finished := time.Now().UTC()
			operation.FinishedAt = &finished
			return nil
		}); err != nil || !changed {
			t.Fatalf("settle provider outage and wake matching: changed=%v err=%v", changed, err)
		}
	} else {
		ticket, err := cache.BeginRefresh(ctx, sourceFixture.source, cacheKey, source.Identity)
		if err != nil {
			t.Fatalf("begin accepted artist-search cache generation: %v", err)
		}
		if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
			Ticket: ticket, FetchedAt: time.Now().UTC(), Revision: "artist-search-r1", Payload: payload,
			Delivery: &persistence.ProviderDeliveryFence{OperationID: providerOperation.ID, Attempt: 1, RiverJobID: providerJobID, ExecutionEpoch: providerEpoch},
		}); err != nil {
			t.Fatalf("persist accepted artist-search response: %v", err)
		}
		if changed, err := repository.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, providerOperation.ID, 1, providerJobID, providerEpoch, client, func(operation *persistence.Operation) error {
			operation.State, operation.Stage = "succeeded", "complete"
			finished := time.Now().UTC()
			operation.FinishedAt = &finished
			return nil
		}); err != nil || !changed {
			t.Fatalf("settle accepted provider delivery and wake matching: changed=%v err=%v", changed, err)
		}
		accepted, err := repository.GetMatchingArtistResponse(ctx, admission.OriginalRunID, providerOperation.ID)
		if err != nil || accepted == nil || accepted.Kind != "provider_artist_search" ||
			accepted.CacheKey != cacheKey || !sameJSON(accepted.Payload, payload) ||
			accepted.Revision == nil || *accepted.Revision != "artist-search-r1" {
			t.Fatalf("read accepted artist response: receipt=%+v err=%v", accepted, err)
		}
	}
	jobID := *admission.Operation.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatalf("prepare registered matching River delivery: %v", err)
	}
	claim, outcome, err := repository.ClaimMatchingDelivery(ctx, admission.OriginalRunID, 1, jobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim admitted evaluation delivery: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}
	return &automaticArtistReviewDelivery{
		ctx: ctx, db: db, source: sourceFixture, repository: repository, cache: cache,
		admission: admission, jobID: jobID, claim: claim,
		acquisition: service.NewMatchingAcquisitionService(repository, artistReviewCachedProvider{cache: cache, sourceID: sourceFixture.source}, client),
	}
}

func readAutomaticArtistAcquisitionCursor(t *testing.T, run *automaticArtistReviewDelivery) (string, int64) {
	t.Helper()
	var phase string
	var revision int64
	if err := run.db.NewRaw(`SELECT current_phase,revision FROM matching_acquisition WHERE run_id=?`, run.admission.OriginalRunID).Scan(run.ctx, &phase, &revision); err != nil {
		t.Fatalf("read matching acquisition cursor: %v", err)
	}
	return phase, revision
}
