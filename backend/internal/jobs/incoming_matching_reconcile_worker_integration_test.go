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

	// A successful real analysis writes the outbox event and the selected metadata
	// used below; the reconciliation consumer receives no hand-built projection.
	analysis := fixture.startWithMetadata(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *analysis.RiverJobID)

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

	var eventID uuid.UUID
	if err := fixture.database.NewRaw(`SELECT id FROM incoming_matching_reconcile_event WHERE source_operation_id=? AND reason='analysis_success'`, analysis.ID).Scan(ctx, &eventID); err != nil {
		t.Fatalf("read successful-analysis outbox event: %v", err)
	}
	if eventID == uuid.Nil {
		t.Fatal("successful analysis did not produce an outbox event")
	}

	registry := fixture.registry
	cache := persistence.NewProviderCacheRepository(fixture.database)
	factory := service.NewProviderFactory(registry, nil, nil, nil, nil)
	provider := service.NewProviderFetchService(registry, factory, cache)
	inserter := &incomingMatchingReconcileInserterSlot{}
	consumer := service.NewIncomingMatchingReconciliation(groups, groupingRepository, provider, fixture.setup, inserter)
	worker := NewIncomingMatchingReconcileWorker(fixture.inventory, consumer)
	client, listenerPool, err := PrepareWithWorkers(ctx, fixture.databaseURL, fixture.database.DB, func(workers *river.Workers) {
		river.AddWorker(workers, worker)
		river.AddWorker(workers, &ProviderFetchWorker{})
		river.AddWorker(workers, &MatchingEvaluationWorker{})
	})
	if err != nil {
		t.Fatalf("prepare River with actual reconciliation, provider, and matching workers: %v", err)
	}
	t.Cleanup(func() { listenerPool.Close() })
	inserter.inserter = client
	if err := fixture.inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch successful-analysis reconciliation event: %v", err)
	}
	var event persistence.IncomingMatchingReconcileEvent
	if err := fixture.database.NewSelect().Model(&event).Where("id = ?", eventID).Scan(ctx); err != nil {
		t.Fatalf("read dispatched reconciliation event: %v", err)
	}
	if event.State != "dispatched" || event.RegisteredJobID == nil || *event.RegisteredJobID < 1 {
		t.Fatalf("event was not registered to a River job: %+v", event)
	}
	job := &river.Job[persistence.IncomingMatchingReconcileJobArgs]{
		JobRow: &rivertype.JobRow{ID: *event.RegisteredJobID, Attempt: 1},
		Args:   persistence.IncomingMatchingReconcileJobArgs{EventID: eventID},
	}
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("run the registered real incoming matching reconciliation worker: %v", err)
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

	// Re-dispatching after completion and replaying the durable event must not
	// create another queue job, admission receipt, target generation, or run.
	var beforeJobs, beforeGenerations int
	if err := fixture.database.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, persistence.IncomingMatchingReconcileJobKind).Scan(ctx, &beforeJobs); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.NewRaw(`SELECT COALESCE(sum(high_water),0) FROM incoming_matching_generation`).Scan(ctx, &beforeGenerations); err != nil {
		t.Fatal(err)
	}
	if err := fixture.inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
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
	if afterJobs != beforeJobs || afterGenerations != beforeGenerations || afterReceipts != projectionCounts.Receipts || afterParents != projectionCounts.Parents {
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
