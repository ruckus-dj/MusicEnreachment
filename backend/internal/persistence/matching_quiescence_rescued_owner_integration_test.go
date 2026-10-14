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

func TestQuiescencePreservesRescuedActiveOwnerPostgreSQL(t *testing.T) {
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
	parentJobID := *admission.Operation.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, parentJobID).Exec(ctx); err != nil {
		t.Fatalf("prepare matching delivery: %v", err)
	}
	claim, outcome, err := setup.ClaimMatchingDelivery(ctx, parent.ID, 1, parentJobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed || claim.ExecutionEpoch < 1 {
		t.Fatalf("claim matching delivery: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}

	// A newer current request and automatic admission establish a positive,
	// fresh supersession proof while the original matching delivery still owns
	// its durable execution epoch.
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
	var newerGeneration int64
	if err := db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, fixture.group.ID).Scan(ctx, &newerGeneration); err != nil {
		t.Fatalf("read newer request generation: %v", err)
	}
	newChild := &persistence.Operation{
		ID: uuid.New(), Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
		ProviderSourceID: &fixture.source, ProviderID: &providerSource.ProviderID,
		ProviderConfigurationIdentity: &providerSource.Identity, ProviderEndpoint: providerSource.Endpoint,
		ProviderCacheKey: &cacheKey, Attempt: 1,
	}
	newParent := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	newAdmission, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: newParent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: newParent.ID},
		Provider: persistence.MatchingProviderRequest{Operation: newChild, Args: service.ProviderFetchOperationArgs{OperationID: newChild.ID}},
		GroupID:  fixture.group.ID, ExpectedGroupRevision: fixture.group.Revision,
		PolicyVersion: "matching-rapidfuzz-v1", ArtistQueryPlanVersion: 1, QueryDescriptor: query,
		EventID: newerEvent.ID, EventRiverJobID: *newerEvent.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: newerEpoch,
		RequestGeneration: newerGeneration,
	})
	if err != nil || newAdmission.OriginalRunID == admission.OriginalRunID {
		t.Fatalf("admit newer automatic run: original=%s newer=%s err=%v", admission.OriginalRunID, newAdmission.OriginalRunID, err)
	}

	// Simulate River rescuing this same attempt: the durable owner remains, but
	// the job is no longer running and is retryable with no finalized timestamp.
	if _, err := db.NewRaw(`UPDATE river_job SET state='retryable',finalized_at=NULL WHERE id=?`, parentJobID).Exec(ctx); err != nil {
		t.Fatalf("mark matching delivery retryable: %v", err)
	}
	type runSnapshot struct {
		State       string     `bun:"state"`
		Epoch       int64      `bun:"execution_epoch"`
		ActiveJob   *int64     `bun:"active_river_job_id"`
		ActiveTry   *int       `bun:"active_river_attempt"`
		ActiveEpoch *int64     `bun:"active_execution_epoch"`
		CurrentJob  *int64     `bun:"current_river_job_id"`
		QuiescedAt  *time.Time `bun:"quiesced_at"`
	}
	var beforeRun runSnapshot
	if err := db.NewRaw(`SELECT state,execution_epoch,active_river_job_id,active_river_attempt,active_execution_epoch,current_river_job_id,quiesced_at FROM matching_run WHERE id=?`, admission.OriginalRunID).Scan(ctx, &beforeRun); err != nil {
		t.Fatalf("read original run owner: %v", err)
	}
	if beforeRun.ActiveJob == nil || *beforeRun.ActiveJob != parentJobID || beforeRun.ActiveTry == nil || *beforeRun.ActiveTry != 1 || beforeRun.ActiveEpoch == nil || *beforeRun.ActiveEpoch != claim.ExecutionEpoch || beforeRun.Epoch != claim.ExecutionEpoch || beforeRun.CurrentJob == nil || *beforeRun.CurrentJob != parentJobID || beforeRun.QuiescedAt != nil {
		t.Fatalf("matching delivery did not retain expected active owner: run=%+v claim=%+v", beforeRun, claim)
	}
	type operationSnapshot struct {
		State      string     `bun:"state"`
		Stage      string     `bun:"stage"`
		FinishedAt *time.Time `bun:"finished_at"`
		SafeError  *string    `bun:"safe_error"`
	}
	var beforeOperation operationSnapshot
	if err := db.NewRaw(`SELECT state,stage,finished_at,safe_error FROM operation WHERE id=?`, parent.ID).Scan(ctx, &beforeOperation); err != nil {
		t.Fatalf("read original parent business state: %v", err)
	}

	if err := setup.QuiesceObsoleteAutomaticMatchingRuns(ctx); err != nil {
		t.Fatalf("quiesce obsolete automatic runs: %v", err)
	}
	var afterRun runSnapshot
	if err := db.NewRaw(`SELECT state,execution_epoch,active_river_job_id,active_river_attempt,active_execution_epoch,current_river_job_id,quiesced_at FROM matching_run WHERE id=?`, admission.OriginalRunID).Scan(ctx, &afterRun); err != nil {
		t.Fatalf("read original run after quiescence: %v", err)
	}
	if afterRun.QuiescedAt != nil || afterRun.State != beforeRun.State || afterRun.Epoch != beforeRun.Epoch || !equalNullableInt64(afterRun.ActiveJob, beforeRun.ActiveJob) || !equalNullableInt(afterRun.ActiveTry, beforeRun.ActiveTry) || !equalNullableInt64(afterRun.ActiveEpoch, beforeRun.ActiveEpoch) || !equalNullableInt64(afterRun.CurrentJob, beforeRun.CurrentJob) {
		t.Fatalf("quiescence changed an active owner or marked the run: before=%+v after=%+v", beforeRun, afterRun)
	}
	var afterOperation operationSnapshot
	if err := db.NewRaw(`SELECT state,stage,finished_at,safe_error FROM operation WHERE id=?`, parent.ID).Scan(ctx, &afterOperation); err != nil {
		t.Fatalf("read original parent after quiescence: %v", err)
	}
	if afterOperation.State != beforeOperation.State || afterOperation.Stage != beforeOperation.Stage || !equalNullableTime(afterOperation.FinishedAt, beforeOperation.FinishedAt) || !equalNullableString(afterOperation.SafeError, beforeOperation.SafeError) {
		t.Fatalf("quiescence changed original parent business state: before=%+v after=%+v", beforeOperation, afterOperation)
	}
	var currentRunID uuid.UUID
	var generationAfter int64
	if err := db.NewRaw(`SELECT current_run_id,requested_generation FROM incoming_matching_generation WHERE group_id=?`, fixture.group.ID).Scan(ctx, &currentRunID, &generationAfter); err != nil {
		t.Fatalf("read current generation after quiescence: %v", err)
	}
	if currentRunID != newAdmission.OriginalRunID || generationAfter != newerGeneration {
		t.Fatalf("quiescence changed the newer current run/generation: run=%s generation=%d, want run=%s generation=%d", currentRunID, generationAfter, newAdmission.OriginalRunID, newerGeneration)
	}
}

func equalNullableInt64(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalNullableInt(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalNullableTime(left, right *time.Time) bool {
	return left == nil && right == nil || left != nil && right != nil && left.Equal(*right)
}

func equalNullableString(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
