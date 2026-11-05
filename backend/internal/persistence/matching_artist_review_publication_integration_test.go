//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"testing"
	"time"
)

// This provider exposes only a previously accepted cache response. In
// particular, acquisition never makes a provider request while advancing.
type artistReviewCachedProvider struct {
	cache    *persistence.ProviderCacheRepository
	sourceID uuid.UUID
}

func (provider artistReviewCachedProvider) CachedResponse(ctx context.Context, key string) (*persistence.ProviderCachedResponse, error) {
	return provider.cache.GetCachedResponse(ctx, provider.sourceID, key)
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
