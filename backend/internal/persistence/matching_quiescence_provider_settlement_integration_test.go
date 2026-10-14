//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestQuiescedObsoleteAutomaticRunSettlesProviderWithoutParentWakePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	fixture := newIncomingMatchFixture(t, ctx, db)
	setup := persistence.NewSetupManagerRepository(db)
	inventory := persistence.NewSourceInventoryRepository(db)
	client := openScanEnqueueRiver(t, db)
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return persistence.RequestIncomingMatchingReconciliationWith(ctx, tx, []uuid.UUID{fixture.group.ID})
	}); err != nil {
		t.Fatalf("request initial current reconciliation: %v", err)
	}

	var event persistence.IncomingMatchingReconcileEvent
	if err := db.NewSelect().Model(&event).Where("reason = ?", "current").Where("state = ?", "pending").Order("created_at ASC").Order("id ASC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read initial reconciliation event: %v", err)
	}
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch initial reconciliation: %v", err)
	}
	dispatched, err := inventory.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil || dispatched.RegisteredJobID == nil {
		t.Fatalf("read registered reconciliation: event=%+v err=%v", dispatched, err)
	}
	eventEpoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *dispatched.RegisteredJobID, 1)
	if err != nil || !claimed {
		t.Fatalf("claim reconciliation event: epoch=%d claimed=%v err=%v", eventEpoch, claimed, err)
	}
	var generation int64
	if err := db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, fixture.group.ID).Scan(ctx, &generation); err != nil {
		t.Fatalf("read initial request generation: %v", err)
	}
	var providerSource struct {
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := db.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &providerSource); err != nil {
		t.Fatalf("read provider fence: %v", err)
	}
	query := json.RawMessage(`{"schema_version":1,"query":"Fixture Artist","offset":0,"limit":10}`)
	cacheKey := "artist-search:0:10:Fixture Artist"
	parent := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	child := &persistence.Operation{
		ID: uuid.New(), Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
		ProviderSourceID: &fixture.source, ProviderID: &providerSource.ProviderID,
		ProviderConfigurationIdentity: &providerSource.Identity, ProviderEndpoint: providerSource.Endpoint,
		ProviderCacheKey: &cacheKey, Attempt: 1,
	}
	admission, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parent.ID},
		Provider: persistence.MatchingProviderRequest{Operation: child, Args: service.ProviderFetchOperationArgs{OperationID: child.ID}},
		GroupID:  fixture.group.ID, ExpectedGroupRevision: fixture.group.Revision,
		PolicyVersion: "matching-rapidfuzz-v1", ArtistQueryPlanVersion: 1, QueryDescriptor: query,
		EventID: event.ID, EventRiverJobID: *dispatched.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: eventEpoch,
		RequestGeneration: generation,
	})
	if err != nil {
		t.Fatalf("admit automatic artist matching run: %v", err)
	}
	if child.RiverJobID == nil || admission.Operation == nil || admission.Operation.RiverJobID == nil {
		t.Fatal("automatic admission did not register parent and provider jobs")
	}
	childJobID := *child.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, childJobID).Exec(ctx); err != nil {
		t.Fatalf("prepare provider delivery: %v", err)
	}
	childEpoch, childClaimed, err := setup.ClaimProviderOperationDelivery(ctx, child.ID, 1, childJobID)
	if err != nil || !childClaimed {
		t.Fatalf("claim provider delivery: epoch=%d claimed=%v err=%v", childEpoch, childClaimed, err)
	}
	if changed, err := setup.TransitionProviderOperationForDelivery(ctx, child.ID, 1, childJobID, childEpoch, func(operation *persistence.Operation) error {
		operation.State, operation.Stage = "running", "fetching"
		return nil
	}); err != nil || !changed {
		t.Fatalf("start provider delivery: changed=%v err=%v", changed, err)
	}

	// A newer coalesced current-state request makes this automatic run obsolete.
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return persistence.RequestIncomingMatchingReconciliationWith(ctx, tx, []uuid.UUID{fixture.group.ID})
	}); err != nil {
		t.Fatalf("request newer generation: %v", err)
	}
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch newer reconciliation: %v", err)
	}
	var newerEvent persistence.IncomingMatchingReconcileEvent
	if err := db.NewSelect().Model(&newerEvent).Where("reason = ?", "current").Where("state = ?", "dispatched").Where("id <> ?", event.ID).Order("created_at DESC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read newer reconciliation event: %v", err)
	}
	newerEpoch, newerClaimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, newerEvent.ID, *newerEvent.RegisteredJobID, 1)
	if err != nil || !newerClaimed {
		t.Fatalf("claim newer reconciliation: epoch=%d claimed=%v err=%v", newerEpoch, newerClaimed, err)
	}
	var secondGeneration int64
	if err := db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, fixture.group.ID).Scan(ctx, &secondGeneration); err != nil {
		t.Fatalf("read newer request generation: %v", err)
	}
	secondChild := &persistence.Operation{
		ID: uuid.New(), Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
		ProviderSourceID: &fixture.source, ProviderID: &providerSource.ProviderID,
		ProviderConfigurationIdentity: &providerSource.Identity, ProviderEndpoint: providerSource.Endpoint,
		ProviderCacheKey: &cacheKey, Attempt: 1,
	}
	secondParent := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	if _, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: secondParent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: secondParent.ID},
		Provider: persistence.MatchingProviderRequest{Operation: secondChild, Args: service.ProviderFetchOperationArgs{OperationID: secondChild.ID}},
		GroupID:  fixture.group.ID, ExpectedGroupRevision: fixture.group.Revision,
		PolicyVersion: "matching-rapidfuzz-v1", ArtistQueryPlanVersion: 1, QueryDescriptor: query,
		EventID: newerEvent.ID, EventRiverJobID: *newerEvent.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: newerEpoch,
		RequestGeneration: secondGeneration,
	}); err != nil {
		t.Fatalf("admit newer automatic run: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='completed',finalized_at=transaction_timestamp() WHERE id=?`, *admission.Operation.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("settle original parent job: %v", err)
	}
	if err := setup.QuiesceObsoleteAutomaticMatchingRuns(ctx); err != nil {
		t.Fatalf("quiesce obsolete run: %v", err)
	}
	var quiesced bool
	if err := db.NewRaw(`SELECT quiesced_at IS NOT NULL FROM matching_run WHERE id=?`, admission.OriginalRunID).Scan(ctx, &quiesced); err != nil || !quiesced {
		var proof struct {
			IntentVersion int    `bun:"intent_version"`
			Mode          string `bun:"mode"`
			State         string `bun:"state"`
			OpState       string `bun:"operation_state"`
			TargetGen     int64  `bun:"target_generation"`
			Requested     int64  `bun:"requested_generation"`
			Reconciled    *int64 `bun:"reconciliation_generation"`
			LiveJobs      bool   `bun:"live_jobs"`
			LiveOwner     bool   `bun:"live_owner"`
		}
		_ = db.NewRaw(`SELECT acquisition.intent_version,run.immutable_plan->>'mode' AS mode,run.state,parent.state AS operation_state,
			target.target_generation,generation.requested_generation,target.reconciliation_generation,
			EXISTS(SELECT 1 FROM matching_delivery delivery JOIN river_job job ON job.id=delivery.river_job_id WHERE delivery.run_id=run.id AND job.state IN ('available','pending','running','retryable','scheduled')) AS live_jobs,
			run.active_river_job_id IS NOT NULL AS live_owner
			FROM matching_run run JOIN operation parent ON parent.id=run.operation_id JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
			JOIN matching_run_target target ON target.run_id=run.id JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
			WHERE run.id=?`, admission.OriginalRunID).Scan(ctx, &proof)
		t.Fatalf("obsolete run was not quiesced: quiesced=%v proof=%+v err=%v", quiesced, proof, err)
	}
	var parentJobsBefore int
	if err := db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, admission.OriginalRunID).Scan(ctx, &parentJobsBefore); err != nil {
		t.Fatalf("count parent deliveries before child settlement: %v", err)
	}
	var parentState, runState, stage string
	var attempt int
	if err := db.NewRaw(`SELECT operation.state,operation.stage,operation.attempt,run.state FROM operation JOIN matching_run run ON run.operation_id=operation.id WHERE run.id=?`, admission.OriginalRunID).Scan(ctx, &parentState, &stage, &attempt, &runState); err != nil {
		t.Fatalf("read parent status before child settlement: %v", err)
	}

	if changed, err := setup.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, child.ID, 1, childJobID, childEpoch, client, func(operation *persistence.Operation) error {
		operation.State, operation.Stage = "succeeded", "complete"
		finished := time.Now().UTC()
		operation.FinishedAt = &finished
		return nil
	}); err != nil || !changed {
		t.Fatalf("settle successful provider delivery: changed=%v err=%v", changed, err)
	}
	var outcome string
	if err := db.NewRaw(`SELECT terminal_outcome FROM matching_provider_dependency WHERE request_id=? AND parent_run_id=?`, child.ID, admission.OriginalRunID).Scan(ctx, &outcome); err != nil || outcome != "succeeded" {
		t.Fatalf("provider outcome was not truthfully settled: outcome=%q err=%v", outcome, err)
	}
	var parentJobsAfter int
	if err := db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, admission.OriginalRunID).Scan(ctx, &parentJobsAfter); err != nil || parentJobsAfter != parentJobsBefore {
		t.Fatalf("quiesced parent received a new wake: deliveries=%d before=%d err=%v", parentJobsAfter, parentJobsBefore, err)
	}
	var parentStateAfter, runStateAfter, stageAfter string
	var attemptAfter int
	if err := db.NewRaw(`SELECT operation.state,operation.stage,operation.attempt,run.state FROM operation JOIN matching_run run ON run.operation_id=operation.id WHERE run.id=?`, admission.OriginalRunID).Scan(ctx, &parentStateAfter, &stageAfter, &attemptAfter, &runStateAfter); err != nil {
		t.Fatalf("read parent status after child settlement: %v", err)
	}
	if parentStateAfter != parentState || stageAfter != stage || attemptAfter != attempt || runStateAfter != runState {
		t.Fatalf("provider settlement altered parent status: before=%s/%s/%d/%s after=%s/%s/%d/%s", parentState, stage, attempt, runState, parentStateAfter, stageAfter, attemptAfter, runStateAfter)
	}
	var generationAfter int64
	if err := db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, fixture.group.ID).Scan(ctx, &generationAfter); err != nil || generationAfter != secondGeneration {
		t.Fatalf("new request generation changed unexpectedly: expected=%d after=%d err=%v", secondGeneration, generationAfter, err)
	}

	// The provider child has settled, but a newly-authored quiescence marker is
	// still too young to authorize retirement. Cleanup must preserve the
	// incomplete parent status and the immutable timestamp rather than treating
	// quiescence itself as permission to delete the run.
	var markerBefore time.Time
	if err := db.NewRaw(`SELECT quiesced_at FROM matching_run WHERE id=?`, admission.OriginalRunID).Scan(ctx, &markerBefore); err != nil {
		t.Fatalf("read quiescence marker before retirement hold: %v", err)
	}
	if err := setup.DeleteObsoleteQuiescedMatchingRuns(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("cleanup while quiescence marker is younger than 24 hours: %v", err)
	}
	var retained int
	if err := db.NewRaw(`SELECT count(*) FROM matching_run run JOIN operation parent ON parent.id=run.operation_id
		WHERE run.id=? AND parent.state=? AND parent.stage=? AND parent.attempt=? AND run.state=?`,
		admission.OriginalRunID, parentState, stage, attempt, runState).Scan(ctx, &retained); err != nil || retained != 1 {
		t.Fatalf("young quiesced matching proof/status retained=%d err=%v, want one unchanged run", retained, err)
	}
	if _, err := db.NewRaw(`UPDATE matching_run SET quiesced_at=NULL WHERE id=?`, admission.OriginalRunID).Exec(ctx); err == nil {
		t.Fatal("quiescence timestamp was cleared; marker must be write-once")
	}
	var markerAfter time.Time
	if err := db.NewRaw(`SELECT quiesced_at FROM matching_run WHERE id=?`, admission.OriginalRunID).Scan(ctx, &markerAfter); err != nil || !markerAfter.Equal(markerBefore) {
		t.Fatalf("quiescence marker changed after rejected reset: before=%s after=%s err=%v", markerBefore, markerAfter, err)
	}
	var authorizations int
	if err := db.NewRaw(`SELECT count(*) FROM matching_run_retirement_authorization WHERE run_id=?`, admission.OriginalRunID).Scan(ctx, &authorizations); err != nil || authorizations != 0 {
		t.Fatalf("young run retirement authorizations=%d err=%v, want none", authorizations, err)
	}
}
