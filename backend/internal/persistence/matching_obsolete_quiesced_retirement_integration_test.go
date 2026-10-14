//go:build integration

package persistence_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const (
	matchingQuiescenceSchemaMigration   = "20261211000000"
	obsoleteMatchingRetirementMigration = "20261212000000"
	failedObsoleteDisposalMigration     = "20261213000000"
)

func TestDeleteObsoleteQuiescedMatchingRunPreservesCurrentGenerationAndReceiptPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.Open(t)
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	applyMatchingMigrationsThrough(t, ctx, db, collection, matchingQuiescenceSchemaMigration)
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
		t.Fatalf("admit automatic V2 matching run: %v", err)
	}
	if child.RiverJobID == nil || admission.Operation == nil || admission.Operation.RiverJobID == nil {
		t.Fatal("automatic admission did not register parent and provider jobs")
	}
	var receiptBefore []byte
	if err := db.NewRaw(`SELECT fingerprint FROM incoming_matching_automatic_admission WHERE original_run_id=?`, admission.OriginalRunID).Scan(ctx, &receiptBefore); err != nil {
		t.Fatalf("capture immutable semantic admission receipt: %v", err)
	}
	var intentVersion int
	if err := db.NewRaw(`SELECT intent_version FROM matching_acquisition WHERE run_id=?`, admission.OriginalRunID).Scan(ctx, &intentVersion); err != nil || intentVersion != 2 {
		t.Fatalf("acquisition intent version=%d err=%v, want real automatic V2", intentVersion, err)
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
	secondChild := &persistence.Operation{
		ID: uuid.New(), Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
		ProviderSourceID: &fixture.source, ProviderID: &providerSource.ProviderID,
		ProviderConfigurationIdentity: &providerSource.Identity, ProviderEndpoint: providerSource.Endpoint,
		ProviderCacheKey: &cacheKey, Attempt: 1,
	}
	secondParent := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	newAdmission, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: secondParent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: secondParent.ID},
		Provider: persistence.MatchingProviderRequest{Operation: secondChild, Args: service.ProviderFetchOperationArgs{OperationID: secondChild.ID}},
		GroupID:  fixture.group.ID, ExpectedGroupRevision: fixture.group.Revision,
		PolicyVersion: "matching-rapidfuzz-v1", ArtistQueryPlanVersion: 1, QueryDescriptor: query,
		EventID: newerEvent.ID, EventRiverJobID: *newerEvent.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: newerEpoch,
		RequestGeneration: newerGeneration,
	})
	if err != nil {
		t.Fatalf("admit current-generation automatic run: %v", err)
	}
	if newAdmission.OriginalRunID == admission.OriginalRunID {
		t.Fatal("new generation reused the obsolete run")
	}
	var highWaterBeforeRetirement int64
	if err := db.NewRaw(`SELECT high_water FROM incoming_matching_generation WHERE group_id=?`, fixture.group.ID).Scan(ctx, &highWaterBeforeRetirement); err != nil {
		t.Fatalf("read current run high-water before retirement: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='completed',finalized_at=transaction_timestamp() WHERE id=?`, *admission.Operation.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("prepare obsolete parent delivery: %v", err)
	}
	var secondWakeID int64
	var completedWakeID int64
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		inserted, err := client.InsertTx(ctx, tx.Tx, persistence.MatchingEvaluationWakeArgs{OperationID: admission.OriginalOperationID}, nil)
		if err != nil {
			return err
		}
		if inserted == nil || inserted.Job == nil {
			return errors.New("River returned no second obsolete wake")
		}
		secondWakeID = inserted.Job.ID
		_, err = tx.NewRaw(`INSERT INTO matching_delivery(run_id,river_job_id,operation_attempt) VALUES (?,?,1)`, admission.OriginalRunID, secondWakeID).Exec(ctx)
		if err != nil {
			return err
		}
		completed, err := client.InsertTx(ctx, tx.Tx, persistence.MatchingEvaluationWakeArgs{OperationID: admission.OriginalOperationID}, nil)
		if err != nil || completed == nil || completed.Job == nil {
			return errors.New("River did not register completed obsolete wake")
		}
		completedWakeID = completed.Job.ID
		_, err = tx.NewRaw(`INSERT INTO matching_delivery(run_id,river_job_id,operation_attempt) VALUES (?,?,1)`, admission.OriginalRunID, completedWakeID).Exec(ctx)
		return err
	}); err != nil {
		t.Fatalf("register second obsolete wake: %v", err)
	}
	interruptedAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.NewRaw(`UPDATE operation SET state='failed',stage='interrupted',safe_error='interrupted before runtime recovery',finished_at=?,updated_at=? WHERE id=?`, interruptedAt, interruptedAt, admission.OriginalOperationID).Exec(ctx); err != nil {
		t.Fatalf("seed interrupted failed parent: %v", err)
	}
	for _, jobID := range []int64{*admission.Operation.RiverJobID, secondWakeID} {
		if _, err := db.NewRaw(`UPDATE river_job SET state='available',attempt=1,finalized_at=NULL WHERE id=?`, jobID).Exec(ctx); err != nil {
			t.Fatalf("prepare available parent wake %d: %v", jobID, err)
		}
	}
	// Keep the registration immutable, but corrupt the second mutable River row.
	// Validation must finish for every registered job before canceling the valid
	// first wake (which sorts first by ID).
	if _, err := db.NewRaw(`UPDATE river_job SET kind='wrong_matching_kind',args=?::jsonb WHERE id=?`, `{"operation_id":"`+uuid.NewString()+`"}`, secondWakeID).Exec(ctx); err != nil {
		t.Fatalf("tamper later registered wake kind and arguments: %v", err)
	}
	var deliverySnapshotBefore, deliverySnapshotAfter string
	snapshotQuery := `SELECT jsonb_build_object(
		'jobs',(SELECT jsonb_agg(jsonb_build_object('id',job.id,'kind',job.kind,'args',job.args,'attempt',job.attempt,
			'state',job.state,'finalized_at',job.finalized_at,'metadata',job.metadata) ORDER BY job.id)
			FROM matching_delivery delivery JOIN river_job job ON job.id=delivery.river_job_id WHERE delivery.run_id=?),
		'deliveries',(SELECT jsonb_agg(jsonb_build_object('river_job_id',river_job_id,'consumed_at',consumed_at) ORDER BY river_job_id)
			FROM matching_delivery WHERE run_id=?),
		'run',(SELECT jsonb_build_object('state',state,'quiesced_at',quiesced_at,'active_river_job_id',active_river_job_id,
			'active_river_attempt',active_river_attempt,'active_execution_epoch',active_execution_epoch) FROM matching_run WHERE id=?),
		'operation',(SELECT jsonb_build_object('state',state,'stage',stage,'attempt',attempt,'safe_error',safe_error,'finished_at',finished_at)
			FROM operation WHERE id=?))::text`
	if err := db.NewRaw(snapshotQuery, admission.OriginalRunID, admission.OriginalRunID, admission.OriginalRunID, admission.OriginalOperationID).Scan(ctx, &deliverySnapshotBefore); err != nil {
		t.Fatalf("snapshot registered deliveries before invalid-proof preflight: %v", err)
	}
	if err := setup.QuiesceObsoleteAutomaticMatchingRuns(ctx); err != nil {
		t.Fatalf("preflight invalid registered delivery: %v", err)
	}
	if err := db.NewRaw(snapshotQuery, admission.OriginalRunID, admission.OriginalRunID, admission.OriginalRunID, admission.OriginalOperationID).Scan(ctx, &deliverySnapshotAfter); err != nil {
		t.Fatalf("snapshot registered deliveries after invalid-proof preflight: %v", err)
	}
	if deliverySnapshotAfter != deliverySnapshotBefore {
		t.Fatalf("invalid later registration partially changed obsolete run: before=%s after=%s", deliverySnapshotBefore, deliverySnapshotAfter)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET kind=?,args=?::jsonb,state='retryable',attempt=5,finalized_at=NULL WHERE id=?`, (persistence.MatchingEvaluationWakeArgs{}).Kind(), `{"operation_id":"`+admission.OriginalOperationID.String()+`"}`, secondWakeID).Exec(ctx); err != nil {
		t.Fatalf("restore later registered wake: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='retryable',attempt=5,finalized_at=NULL WHERE id=?`, *admission.Operation.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("prepare retryable parent wake: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='completed',attempt=1,finalized_at=transaction_timestamp() WHERE id=?`, completedWakeID).Exec(ctx); err != nil {
		t.Fatalf("prepare unconsumed completed parent wake: %v", err)
	}
	// A refreshed/replaced target may no longer match the admission-time capture.
	// Obsolescence must use the immutable admission proof plus the positive new
	// generation proof, not require the historical capture to equal current data.
	if _, err := db.NewRaw(`UPDATE incoming_group_member SET ready=NOT ready WHERE group_id=? AND variant_id=(
		SELECT variant_id FROM incoming_group_member WHERE group_id=? ORDER BY variant_id LIMIT 1)`, fixture.group.ID, fixture.group.ID).Exec(ctx); err != nil {
		t.Fatalf("advance current target capture: %v", err)
	}
	if err := setup.QuiesceObsoleteAutomaticMatchingRuns(ctx); err != nil {
		t.Fatalf("quiesce obsolete run: %v", err)
	}
	var canceledDeliveries int
	if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE id IN (?,?) AND state='cancelled' AND finalized_at IS NOT NULL AND metadata ? 'cancel_attempted_at'`, *admission.Operation.RiverJobID, secondWakeID).Scan(ctx, &canceledDeliveries); err != nil || canceledDeliveries != 2 {
		t.Fatalf("obsolete attempted wakes canceled=%d err=%v, want both", canceledDeliveries, err)
	}
	var completedRegistrationConsumed int
	if err := db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND river_job_id=? AND consumed_at IS NOT NULL`, admission.OriginalRunID, completedWakeID).Scan(ctx, &completedRegistrationConsumed); err != nil || completedRegistrationConsumed != 1 {
		t.Fatalf("terminal completed registration disposed=%d err=%v, want one", completedRegistrationConsumed, err)
	}
	var activeOwners int
	if err := db.NewRaw(`SELECT count(*) FROM matching_run WHERE id=? AND
		(active_river_job_id IS NOT NULL OR active_river_attempt IS NOT NULL OR active_execution_epoch IS NOT NULL)`, admission.OriginalRunID).Scan(ctx, &activeOwners); err != nil || activeOwners != 0 {
		t.Fatalf("quiesced obsolete run still has active owners=%d err=%v", activeOwners, err)
	}
	// This is an upgrade-fixture seed: schema 11 already records quiescence, but
	// schema 12's timestamp guard is not installed yet. It represents an already
	// old production marker; production code never backdates quiescence timestamps.
	oldMarker := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)
	if _, err := db.NewRaw(`UPDATE matching_run SET quiesced_at=? WHERE id=?`, oldMarker, admission.OriginalRunID).Exec(ctx); err != nil {
		t.Fatalf("seed historical quiescence marker before timestamp guard migration: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE matching_delivery SET consumed_at=? WHERE run_id=? AND consumed_at IS NULL`, time.Now().UTC(), admission.OriginalRunID).Exec(ctx); err != nil {
		t.Fatalf("seed completed historical parent-delivery acknowledgments: %v", err)
	}
	applyMatchingMigration(t, ctx, db, collection, obsoleteMatchingRetirementMigration)
	applyMatchingMigration(t, ctx, db, collection, failedObsoleteDisposalMigration)
	var statusBeforeSettlement struct {
		State      string     `bun:"state"`
		Stage      string     `bun:"stage"`
		Attempt    int        `bun:"attempt"`
		SafeError  string     `bun:"safe_error"`
		FinishedAt *time.Time `bun:"finished_at"`
		RunState   string     `bun:"run_state"`
	}
	if err := db.NewRaw(`SELECT operation.state,operation.stage,operation.attempt,operation.safe_error,operation.finished_at,run.state AS run_state
		FROM operation JOIN matching_run run ON run.operation_id=operation.id WHERE run.id=?`, admission.OriginalRunID).Scan(ctx, &statusBeforeSettlement); err != nil {
		t.Fatalf("read parent status before provider settlement: %v", err)
	}
	if changed, err := setup.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, child.ID, 1, childJobID, childEpoch, client, func(operation *persistence.Operation) error {
		operation.State, operation.Stage = "succeeded", "complete"
		finished := time.Now().UTC().Add(-25 * time.Hour)
		operation.FinishedAt = &finished
		return nil
	}); err != nil || !changed {
		t.Fatalf("settle successful provider delivery: changed=%v err=%v", changed, err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='completed',finalized_at=transaction_timestamp() WHERE id=?`, childJobID).Exec(ctx); err != nil {
		t.Fatalf("settle successful provider River job: %v", err)
	}
	var childState, dependencyOutcome string
	if err := db.NewRaw(`SELECT operation.state,dependency.terminal_outcome FROM operation JOIN matching_provider_dependency dependency ON dependency.request_id=operation.id WHERE operation.id=? AND dependency.parent_run_id=?`, child.ID, admission.OriginalRunID).Scan(ctx, &childState, &dependencyOutcome); err != nil || childState != "succeeded" || dependencyOutcome != "succeeded" {
		t.Fatalf("provider dependency not genuinely settled: state=%q outcome=%q err=%v", childState, dependencyOutcome, err)
	}
	var settledJobs int
	if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE id IN (?,?) AND state='completed'`, *admission.Operation.RiverJobID, childJobID).Scan(ctx, &settledJobs); err != nil || settledJobs != 1 {
		t.Fatalf("obsolete provider registered job completed=%d err=%v, want only provider completion", settledJobs, err)
	}
	var statusBefore struct {
		State      string     `bun:"state"`
		Stage      string     `bun:"stage"`
		Attempt    int        `bun:"attempt"`
		SafeError  string     `bun:"safe_error"`
		FinishedAt *time.Time `bun:"finished_at"`
		RunState   string     `bun:"run_state"`
		Marker     time.Time  `bun:"quiesced_at"`
	}
	if err := db.NewRaw(`SELECT operation.state,operation.stage,operation.attempt,operation.safe_error,operation.finished_at,run.state AS run_state,run.quiesced_at FROM operation JOIN matching_run run ON run.operation_id=operation.id WHERE run.id=?`, admission.OriginalRunID).Scan(ctx, &statusBefore); err != nil {
		t.Fatalf("read obsolete status before retirement: %v", err)
	}
	if !statusBefore.Marker.Equal(oldMarker) {
		t.Fatalf("historical marker=%s, want %s", statusBefore.Marker, oldMarker)
	}
	if statusBefore.State != statusBeforeSettlement.State || statusBefore.Stage != statusBeforeSettlement.Stage ||
		statusBefore.Attempt != statusBeforeSettlement.Attempt || statusBefore.SafeError != "interrupted before runtime recovery" ||
		statusBefore.FinishedAt == nil || !statusBefore.FinishedAt.Equal(interruptedAt) || statusBefore.RunState != statusBeforeSettlement.RunState {
		t.Fatalf("provider success reinterpreted quiesced parent status: before=%+v after=%+v", statusBeforeSettlement, statusBefore)
	}
	var headerReferences int
	if err := db.NewRaw(`SELECT count(*) FROM incoming_match_evaluation WHERE target_run_id=? OR frozen_run_id=?`, admission.OriginalRunID, admission.OriginalRunID).Scan(ctx, &headerReferences); err != nil || headerReferences != 0 {
		t.Fatalf("obsolete run still has evaluation-header references=%d err=%v", headerReferences, err)
	}
	var cachePayloadBefore []byte
	var cacheRevisionBefore string
	if err := db.NewRaw(`SELECT payload,revision FROM provider_response_cache WHERE provider_source_id=? AND cache_key=?`, fixture.source, fixture.cacheKey).Scan(ctx, &cachePayloadBefore, &cacheRevisionBefore); err != nil {
		t.Fatalf("capture accepted provider cache before retirement: %v", err)
	}
	cutoff := time.Now().UTC()
	if err := setup.DeleteObsoleteQuiescedMatchingRuns(ctx, cutoff); err != nil {
		t.Fatalf("retire aged obsolete V2 run: %v", err)
	}
	var obsoleteCount int
	if err := db.NewRaw(`SELECT count(*) FROM matching_run WHERE id=?`, admission.OriginalRunID).Scan(ctx, &obsoleteCount); err != nil || obsoleteCount != 0 {
		t.Fatalf("obsolete run count=%d err=%v, want deletion", obsoleteCount, err)
	}
	var graphRows struct {
		Acquisitions int `bun:"acquisitions"`
		Targets      int `bun:"targets"`
		Dependencies int `bun:"dependencies"`
		Phases       int `bun:"phases"`
	}
	if err := db.NewRaw(`SELECT
		(SELECT count(*) FROM matching_acquisition WHERE run_id=?) AS acquisitions,
		(SELECT count(*) FROM matching_run_target WHERE run_id=?) AS targets,
		(SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?) AS dependencies,
		(SELECT count(*) FROM matching_acquisition_phase WHERE run_id=?) AS phases`, admission.OriginalRunID, admission.OriginalRunID, admission.OriginalRunID, admission.OriginalRunID).Scan(ctx, &graphRows); err != nil || graphRows != (struct {
		Acquisitions int `bun:"acquisitions"`
		Targets      int `bun:"targets"`
		Dependencies int `bun:"dependencies"`
		Phases       int `bun:"phases"`
	}{}) {
		t.Fatalf("obsolete matching graph rows remain: %+v err=%v", graphRows, err)
	}
	var currentRun uuid.UUID
	var highWater int64
	if err := db.NewRaw(`SELECT current_run_id,high_water FROM incoming_matching_generation WHERE group_id=?`, fixture.group.ID).Scan(ctx, &currentRun, &highWater); err != nil || currentRun != newAdmission.OriginalRunID || highWater != highWaterBeforeRetirement {
		t.Fatalf("current generation changed during retirement: run=%s expected=%s high-water=%d expectedHighWater=%d err=%v", currentRun, newAdmission.OriginalRunID, highWater, highWaterBeforeRetirement, err)
	}
	var retainedReceipt []byte
	var retainedRun, retainedOperation uuid.UUID
	if err := db.NewRaw(`SELECT fingerprint,original_run_id,original_operation_id FROM incoming_matching_automatic_admission WHERE fingerprint=?`, receiptBefore).Scan(ctx, &retainedReceipt, &retainedRun, &retainedOperation); err != nil {
		t.Fatalf("read retained semantic receipt: %v", err)
	}
	if string(retainedReceipt) != string(receiptBefore) || retainedRun != admission.OriginalRunID || retainedOperation != admission.OriginalOperationID {
		t.Fatalf("semantic receipt changed: run=%s operation=%s", retainedRun, retainedOperation)
	}
	var retainedSource, retainedCache int
	if err := db.NewRaw(`SELECT (SELECT count(*) FROM provider_source WHERE id=?),(SELECT count(*) FROM provider_response_cache WHERE provider_source_id=?)`, fixture.source, fixture.source).Scan(ctx, &retainedSource, &retainedCache); err != nil || retainedSource != 1 || retainedCache == 0 {
		t.Fatalf("provider source/cache was pruned: source=%d cache=%d err=%v", retainedSource, retainedCache, err)
	}
	var cachePayloadAfter []byte
	var cacheRevisionAfter string
	if err := db.NewRaw(`SELECT payload,revision FROM provider_response_cache WHERE provider_source_id=? AND cache_key=?`, fixture.source, fixture.cacheKey).Scan(ctx, &cachePayloadAfter, &cacheRevisionAfter); err != nil {
		t.Fatalf("read accepted provider cache after retirement: %v", err)
	}
	if !bytes.Equal(cachePayloadAfter, cachePayloadBefore) || cacheRevisionAfter != cacheRevisionBefore {
		t.Fatalf("accepted provider cache changed during retirement: revision=%q want %q", cacheRevisionAfter, cacheRevisionBefore)
	}
	var oldOperationCount int
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE id=?`, admission.OriginalOperationID).Scan(ctx, &oldOperationCount); err != nil || oldOperationCount != 0 {
		t.Fatalf("obsolete parent operation count=%d err=%v, want removed without status reinterpretation", oldOperationCount, err)
	}
	// Replaying the old positive generation after the fresh request exists must
	// conflict. Do not reset the generation or fabricate a legacy generation-zero
	// request just to make the historical receipt replay appear current.
	replay := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	replayChild := &persistence.Operation{ID: uuid.New(), Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
		ProviderSourceID: &fixture.source, ProviderID: &providerSource.ProviderID, ProviderConfigurationIdentity: &providerSource.Identity,
		ProviderEndpoint: providerSource.Endpoint, ProviderCacheKey: &cacheKey, Attempt: 1}
	replayed, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: replay, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: replay.ID},
		Provider: persistence.MatchingProviderRequest{Operation: replayChild, Args: service.ProviderFetchOperationArgs{OperationID: replayChild.ID}},
		GroupID:  fixture.group.ID, ExpectedGroupRevision: fixture.group.Revision,
		PolicyVersion: "matching-rapidfuzz-v1", ArtistQueryPlanVersion: 1, QueryDescriptor: query,
		EventID: event.ID, EventRiverJobID: *dispatched.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: eventEpoch,
		RequestGeneration: generation,
	})
	if !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("old-generation semantic replay result=%+v err=%v, want conflict against fresh generation", replayed, err)
	}
	var generationAfterReplay int64
	if err := db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, fixture.group.ID).Scan(ctx, &generationAfterReplay); err != nil || generationAfterReplay != newerGeneration {
		t.Fatalf("stale replay reset current request generation: before=%d after=%d err=%v", newerGeneration, generationAfterReplay, err)
	}
}

func applyMatchingMigrationsThrough(t *testing.T, ctx context.Context, db *bun.DB, collection *migrate.Migrations, through string) {
	t.Helper()
	partial := migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		if migration.Name > through {
			break
		}
		partial.Add(migration)
	}
	applyMatchingMigrationCollection(t, ctx, db, partial)
}

func applyMatchingMigration(t *testing.T, ctx context.Context, db *bun.DB, collection *migrate.Migrations, name string) {
	t.Helper()
	partial := migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		if migration.Name == name {
			partial.Add(migration)
			break
		}
	}
	if len(partial.Sorted()) != 1 {
		t.Fatalf("migration %s not found", name)
	}
	applyMatchingMigrationCollection(t, ctx, db, partial)
}

func applyMatchingMigrationCollection(t *testing.T, ctx context.Context, db *bun.DB, collection *migrate.Migrations) {
	t.Helper()
	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize matching test migrations: %v", err)
	}
	if _, err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("apply matching test migrations: %v", err)
	}
}
