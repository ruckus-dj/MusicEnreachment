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

// TestAutomaticArtistManualReviewParkCompletesOperation proves that the
// acquisition park is a durable handoff: the operation ends successfully with
// its review evidence, while the matching run keeps waiting for policy. It also
// checks the startup ownership proof and the current-operations projection do
// not treat the finished operation as pending background work.
func TestAutomaticArtistManualReviewParkCompletesOperation(t *testing.T) {
	t.Parallel()
	accepted := json.RawMessage(`{"count":0,"offset":0,"created":"2025-01-01T00:00:00Z","artists":[]}`)
	run := prepareAutomaticArtistReviewDelivery(t, accepted)
	runID := run.admission.OriginalRunID

	changed, acquired, err := run.acquisition.Advance(run.ctx, runID, 1, run.jobID, run.claim)
	if err != nil || !changed || !acquired {
		t.Fatalf("park empty artist search: changed=%t acquired=%t err=%v", changed, acquired, err)
	}

	operation, err := run.repository.GetOperation(run.ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != "succeeded" || operation.Stage != "manual_review" ||
		operation.FinishedAt == nil || operation.SafeError != nil {
		t.Fatalf("parked operation state/stage/finished/safe_error=%s/%s/%v/%v; want succeeded/manual_review with finish time and no error",
			operation.State, operation.Stage, operation.FinishedAt, operation.SafeError)
	}

	var matchingRun struct {
		State    string     `bun:"state"`
		Current  *int64     `bun:"current_river_job_id"`
		Active   *int64     `bun:"active_river_job_id"`
		Attempt  *int       `bun:"active_river_attempt"`
		Epoch    *int64     `bun:"active_execution_epoch"`
		Quiesced *time.Time `bun:"quiesced_at"`
	}
	if err := run.db.NewRaw(`SELECT state,current_river_job_id,active_river_job_id,
			active_river_attempt,active_execution_epoch,quiesced_at
		FROM matching_run WHERE id=?`, runID).Scan(run.ctx, &matchingRun.State, &matchingRun.Current,
		&matchingRun.Active, &matchingRun.Attempt, &matchingRun.Epoch, &matchingRun.Quiesced); err != nil {
		t.Fatal(err)
	}
	if matchingRun.State != "waiting_policy" || matchingRun.Quiesced != nil || matchingRun.Active != nil || matchingRun.Attempt != nil || matchingRun.Epoch != nil ||
		matchingRun.Current == nil || *matchingRun.Current != run.jobID {
		t.Fatalf("parked run = %+v; want waiting_policy retaining the consumed delivery job %d and no active execution", matchingRun, run.jobID)
	}

	snapshot, exists, err := run.repository.GetMatchingAcquisition(run.ctx, runID)
	if err != nil || !exists || snapshot.Phase != "waiting_policy" {
		t.Fatalf("parked acquisition phase=%q exists=%t err=%v; want waiting_policy", snapshot.Phase, exists, err)
	}
	var disposition struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(snapshot.Output, &disposition); err != nil ||
		disposition.State != "waiting_policy" || disposition.Reason != "artist_search_no_results" {
		t.Fatalf("parked acquisition output=%s err=%v; want waiting_policy/artist_search_no_results", snapshot.Output, err)
	}

	// The review is the durable handoff evidence. It stays readable after the
	// operation completes and carries the accepted empty-search receipt.
	var evidence []byte
	if err := run.db.NewRaw(`SELECT evaluation_evidence FROM incoming_match_evaluation
		WHERE group_id=? AND target_run_id=?`, run.source.group.ID, runID).Scan(run.ctx, &evidence); err != nil {
		t.Fatalf("read persisted artist review evidence: %v", err)
	}
	var reviewEvidence struct {
		Proposal json.RawMessage `json:"review_proposal"`
		Accepted struct {
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"accepted_artist_search"`
	}
	if err := json.Unmarshal(evidence, &reviewEvidence); err != nil || len(reviewEvidence.Proposal) == 0 ||
		reviewEvidence.Accepted.RequestID == uuid.Nil || reviewEvidence.Accepted.CacheKey == "" {
		t.Fatalf("persisted artist review evidence is incomplete: %s err=%v", evidence, err)
	}
	review, err := service.NewMatchingReviewService(persistence.NewIncomingMatchCandidateRepository(run.db)).Get(run.ctx, run.source.group.ID)
	if err != nil || !review.Evaluated || !review.ProposalAvailable {
		t.Fatalf("read completed artist review: evaluated=%t proposal=%t err=%v", review.Evaluated, review.ProposalAvailable, err)
	}

	// The handoff has no live background work, so the startup ownership proof
	// must still recognize the durable review wait even though the operation is
	// terminal, and the current-operations projection must not list it.
	owned, err := run.repository.IsMatchingRuntimeScannerOwnedOperation(run.ctx, runID)
	if err != nil || !owned {
		t.Fatalf("parked review ownership=%t err=%v; want the durable review wait to be recognized", owned, err)
	}
	rows, _, err := run.repository.ListCurrentOperations(run.ctx, persistence.CurrentOperationsFilter{Kind: "matching", Status: "all"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == runID {
			t.Fatalf("completed manual-review handoff is still listed as a current operation: %+v", row.Operation)
		}
	}
	// The cache-only review refresh only rebuilds sealed automatic evaluations.
	// A manual-review handoff keeps its run waiting for policy, so it must not be
	// re-evaluated or replaced by that detector.
	refreshCandidates, err := run.repository.ListArtistMatchingReviewRefreshCandidates(run.ctx, uuid.Nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range refreshCandidates {
		if candidate.RunID == runID {
			t.Fatalf("manual-review handoff was exposed to the cache-only review refresh: %+v", candidate)
		}
	}
	var consumed bool
	if err := run.db.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery
		WHERE run_id=? AND river_job_id=?`, runID, run.jobID).Scan(run.ctx, &consumed); err != nil || !consumed {
		t.Fatalf("parked review delivery consumed=%t err=%v; want the claimed handoff consumed", consumed, err)
	}
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
