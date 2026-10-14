//go:build integration

package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type incomingMatchingReconcileInserterSlot struct {
	inserter persistence.RiverInserter
}

func (slot *incomingMatchingReconcileInserterSlot) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if slot.inserter == nil {
		return nil, fmt.Errorf("incoming matching reconciliation inserter is not ready")
	}
	return slot.inserter.InsertTx(ctx, tx, args, options)
}

func TestIncomingMatchingReconcileWorkerProcessesAnalysisEventPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAnalysisDispatchFixtureWithMetadata(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// A successful real analysis writes the saved metadata used below. Delete only
	// its undispatched outbox row to model a pre-outbox ready database fixture;
	// production startup never performs this cleanup or synthesizes source IDs.
	analysis := fixture.startWithMetadata(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *analysis.RiverJobID)
	stopRiverClient(t, fixture.client)

	if err := fixture.registry.SetMusicBrainzConfig(ctx, "self-hosted", "http://127.0.0.1:9/ws/2"); err != nil {
		t.Fatalf("save MusicBrainz provider configuration: %v", err)
	}
	groupingRepository := persistence.NewIncomingGroupingRepository(fixture.database)
	groups := service.NewIncomingGroups(groupingRepository)
	if err := groups.Refresh(ctx); err != nil {
		t.Fatalf("persist incoming grouping projection: %v", err)
	}
	snapshot, err := groupingRepository.MatchingReconcileSnapshot(ctx)
	if err != nil {
		t.Fatalf("read saved incoming matching projection: %v", err)
	}
	plan, err := service.PlanIncomingMatchingReconcile(snapshot)
	if err != nil || len(plan.PlannedGroups) == 0 {
		t.Fatalf("plan saved groups: planned=%d err=%v", len(plan.PlannedGroups), err)
	}
	for _, target := range plan.PlannedGroups {
		if !strings.Contains(target.Query, `artist:"Fixture Artist"`) {
			t.Fatalf("query %q did not use the persisted analysis artist", target.Query)
		}
	}

	var sourceEventID uuid.UUID
	if err := fixture.database.NewRaw(`SELECT id FROM incoming_matching_reconcile_event WHERE source_operation_id=? AND reason='analysis_success'`, analysis.ID).Scan(ctx, &sourceEventID); err != nil {
		t.Fatalf("read successful-analysis outbox event: %v", err)
	}
	if sourceEventID == uuid.Nil {
		t.Fatal("successful analysis did not produce an outbox event")
	}
	ordinaryEvent, err := fixture.inventory.GetIncomingMatchingReconciliationEvent(ctx, sourceEventID)
	if err != nil || ordinaryEvent.SourceOperationID == nil || *ordinaryEvent.SourceOperationID != analysis.ID || ordinaryEvent.OriginalAttempt == nil || *ordinaryEvent.OriginalAttempt != analysis.Attempt {
		t.Fatalf("ordinary source event origin did not round-trip: event=%+v err=%v", ordinaryEvent, err)
	}
	eventID := sourceEventID
	var currentRequests []persistence.IncomingMatchingCurrentRequest
	if err := fixture.database.NewRaw(`SELECT group_id, requested_generation, reconciled_generation
		FROM incoming_matching_generation WHERE requested_generation > reconciled_generation ORDER BY group_id`).Scan(ctx, &currentRequests); err != nil {
		t.Fatalf("read pending current generations before source-event dispatch: %v", err)
	}
	if len(currentRequests) == 0 {
		t.Fatal("ready analysis fixture did not produce a pending current-generation request")
	}
	requestedGenerations := make(map[uuid.UUID]int64, len(currentRequests))
	for _, request := range currentRequests {
		if request.RequestedGeneration < 1 || request.ReconciledGeneration >= request.RequestedGeneration {
			t.Fatalf("invalid pending current-generation request before dispatch: %+v", request)
		}
		requestedGenerations[request.GroupID] = request.RequestedGeneration
	}

	registry := fixture.registry
	cache := persistence.NewProviderCacheRepository(fixture.database)
	factory := service.NewProviderFactory(registry, nil, nil, nil, nil)
	provider := service.NewProviderFetchService(registry, factory, cache)
	inserter := &incomingMatchingReconcileInserterSlot{}
	consumer := service.NewIncomingMatchingReconciliation(groups, groupingRepository, provider, fixture.setup, inserter, fixture.inventory)
	worker := NewIncomingMatchingReconcileWorker(fixture.inventory, consumer)
	_, listenerPool, err := PrepareWithWorkers(ctx, fixture.databaseURL, fixture.database.DB, nil)
	if err != nil {
		t.Fatalf("prepare River with actual reconciliation, provider, and matching workers: %v", err)
	}
	driver := riverdatabasesql.NewWithPgxListener(fixture.database.DB, listenerPool)
	workers := river.NewWorkers()
	river.AddWorker(workers, worker)
	client, err := river.NewClient(driver, &river.Config{
		Workers: workers,
		Queues:  map[string]river.QueueConfig{"matching": {MaxWorkers: 1}},
	})
	if err != nil {
		listenerPool.Close()
		t.Fatalf("create isolated reconciliation consumer: %v", err)
	}
	enqueueClient, err := river.NewClient(driver, &river.Config{})
	if err != nil {
		listenerPool.Close()
		t.Fatalf("create insert-only reconciliation client: %v", err)
	}
	clientLifetime, cancelClientLifetime := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancelClientLifetime()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Stop(shutdown); err != nil {
			t.Errorf("stop live River matching consumer: %v", err)
		}
		listenerPool.Close()
	})
	inserter.inserter = enqueueClient
	if err := fixture.inventory.DispatchPendingMatchingReconciliations(ctx, enqueueClient); err != nil {
		t.Fatalf("dispatch startup reconciliation event: %v", err)
	}
	var event persistence.IncomingMatchingReconcileEvent
	if err := fixture.database.NewSelect().Model(&event).Where("id = ?", eventID).Scan(ctx); err != nil {
		t.Fatalf("read dispatched reconciliation event: %v", err)
	}
	if event.State != "dispatched" || event.RegisteredJobID == nil || *event.RegisteredJobID < 1 {
		t.Fatalf("event was not registered to a River job: %+v", event)
	}
	epoch, claimed, err := fixture.inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, eventID, *event.RegisteredJobID, 1)
	if err != nil || !claimed {
		t.Fatalf("claim dispatched startup event before consumer starts: claimed=%v err=%v", claimed, err)
	}
	claimedEvent, err := fixture.inventory.GetIncomingMatchingReconciliationEvent(ctx, eventID)
	if err != nil || claimedEvent.ID != eventID || claimedEvent.RegisteredJobID == nil || *claimedEvent.RegisteredJobID != *event.RegisteredJobID || claimedEvent.HighestDeliveryAttempt != 1 || claimedEvent.DeliveryEpoch != epoch || !claimedEvent.DeliveryClaimed {
		t.Fatalf("ensure replaced or changed claimed dispatched event: event=%+v err=%v", claimedEvent, err)
	}
	if _, err := fixture.inventory.ReleaseIncomingMatchingReconciliationClaim(ctx, eventID, *event.RegisteredJobID, 1, epoch); err != nil {
		t.Fatalf("release pre-consumer startup claim: %v", err)
	}
	if err := client.Start(clientLifetime); err != nil {
		t.Fatalf("start live River matching consumer: %v", err)
	}
	awaitIncomingMatchingReconcileEventDone(t, ctx, fixture.inventory, eventID)

	var projectionCounts struct {
		Parents      int `bun:"parents"`
		Providers    int `bun:"providers"`
		Dependencies int `bun:"dependencies"`
		Receipts     int `bun:"receipts"`
		Targets      int `bun:"targets"`
	}
	if err := fixture.database.NewRaw(`SELECT
		(SELECT count(*) FROM operation WHERE kind='matching_evaluation_v1') AS parents,
		(SELECT count(*) FROM operation WHERE kind='provider_artist_search') AS providers,
		(SELECT count(*) FROM matching_provider_dependency) AS dependencies,
		(SELECT count(*) FROM incoming_matching_automatic_admission) AS receipts,
		(SELECT count(*) FROM matching_run_target) AS targets`).Scan(ctx, &projectionCounts); err != nil {
		t.Fatalf("count admitted matching projection: %v", err)
	}
	if projectionCounts.Parents != len(plan.PlannedGroups) || projectionCounts.Providers != len(plan.PlannedGroups) ||
		projectionCounts.Dependencies != len(plan.PlannedGroups) || projectionCounts.Receipts != len(plan.PlannedGroups) || projectionCounts.Targets != len(plan.PlannedGroups) {
		t.Fatalf("matching admission counts do not cover planned groups: planned=%d counts=%+v", len(plan.PlannedGroups), projectionCounts)
	}
	var planVersion int
	if err := fixture.database.NewRaw(`SELECT (immutable_plan->>'schema_version')::int FROM matching_run ORDER BY created_at LIMIT 1`).Scan(ctx, &planVersion); err != nil || planVersion != 4 {
		t.Fatalf("automatic parent plan schema version = %d, err=%v; want 4", planVersion, err)
	}
	var eventState string
	if err := fixture.database.NewRaw(`SELECT state FROM incoming_matching_reconcile_event WHERE id=?`, eventID).Scan(ctx, &eventState); err != nil || eventState != "done" {
		t.Fatalf("reconciliation event state = %q, err=%v; want done", eventState, err)
	}

	for groupID, requestedGeneration := range requestedGenerations {
		var reconciledGeneration int64
		if err := fixture.database.NewRaw(`SELECT reconciled_generation FROM incoming_matching_generation WHERE group_id=?`, groupID).Scan(ctx, &reconciledGeneration); err != nil || reconciledGeneration != requestedGeneration {
			t.Fatalf("source-event generation acknowledgment for %s = %d, err=%v; want %d", groupID, reconciledGeneration, err, requestedGeneration)
		}
	}
	// A subsequent startup wake must not duplicate the completed admission.
	var beforeJobs, beforeGenerations int
	if err := fixture.database.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, persistence.IncomingMatchingReconcileJobKind).Scan(ctx, &beforeJobs); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.NewRaw(`SELECT COALESCE(sum(high_water),0) FROM incoming_matching_generation`).Scan(ctx, &beforeGenerations); err != nil {
		t.Fatal(err)
	}
	secondStartup, err := fixture.inventory.EnsureStartupIncomingMatchingReconciliation(ctx)
	if err != nil || secondStartup == nil || secondStartup.ID == eventID {
		t.Fatalf("completed startup event was not replaced by a fresh boot event: event=%+v err=%v", secondStartup, err)
	}
	if err := fixture.inventory.DispatchPendingMatchingReconciliations(ctx, enqueueClient); err != nil {
		t.Fatalf("dispatch second startup reconciliation: %v", err)
	}
	awaitIncomingMatchingReconcileEventDone(t, ctx, fixture.inventory, secondStartup.ID)
	if err := fixture.inventory.DispatchPendingMatchingReconciliations(ctx, enqueueClient); err != nil {
		t.Fatalf("replay pending reconciliation dispatch: %v", err)
	}
	var afterJobs, afterGenerations, afterReceipts, afterParents int
	if err := fixture.database.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, persistence.IncomingMatchingReconcileJobKind).Scan(ctx, &afterJobs); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.NewRaw(`SELECT COALESCE(sum(high_water),0) FROM incoming_matching_generation`).Scan(ctx, &afterGenerations); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.NewRaw(`SELECT count(*) FROM incoming_matching_automatic_admission`).Scan(ctx, &afterReceipts); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.NewRaw(`SELECT count(*) FROM operation WHERE kind='matching_evaluation_v1'`).Scan(ctx, &afterParents); err != nil {
		t.Fatal(err)
	}
	if afterJobs != beforeJobs+1 || afterGenerations != beforeGenerations || afterReceipts != projectionCounts.Receipts || afterParents != projectionCounts.Parents {
		t.Fatalf("replay duplicated work: jobs %d->%d generations %d->%d receipts=%d parents=%d", beforeJobs, afterJobs, beforeGenerations, afterGenerations, afterReceipts, afterParents)
	}
}

func awaitIncomingMatchingReconcileEventDone(t *testing.T, ctx context.Context, repository *persistence.SourceInventoryRepository, eventID uuid.UUID) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		event, err := repository.GetIncomingMatchingReconciliationEvent(ctx, eventID)
		if err != nil {
			t.Fatalf("read reconciliation event while awaiting worker: %v", err)
		}
		if event.State == "done" {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("reconciliation worker did not complete event (state=%s): %v", event.State, ctx.Err())
		case <-ticker.C:
		}
	}
}
