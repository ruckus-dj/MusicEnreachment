//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func TestSourceAnalysisLiveCapacityUsesAdditionalConsumerPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	barrier := &liveCapacityPreparer{firstStarted: make(chan struct{}), secondStarted: make(chan struct{}), releaseFirst: make(chan struct{})}
	fixture := newAnalysisDispatchFixtureWithPreparer(t, barrier)
	if err := fixture.registry.SetSourceFileConcurrency(ctx, 1); err != nil {
		t.Fatalf("set initial source file concurrency: %v", err)
	}

	first := enqueueLiveCapacityOperation(t, ctx, fixture, fixture.work.ID)
	select {
	case <-barrier.firstStarted:
	case <-ctx.Done():
		t.Fatalf("first operation did not reach its preparer: %v", ctx.Err())
	}

	secondWork := insertLiveCapacityWork(t, ctx, fixture)
	if err := fixture.registry.SetSourceFileConcurrency(ctx, 2); err != nil {
		t.Fatalf("raise source file concurrency: %v", err)
	}
	second := enqueueLiveCapacityOperation(t, ctx, fixture, secondWork.ID)

	secondClient, secondListener, err := StartAnalysisConsumer(ctx, fixture.databaseURL, fixture.database.DB, func(workers *river.Workers) {
		river.AddWorker(workers, fixture.worker)
	}, 1)
	if err != nil {
		t.Fatalf("start supplemental analysis consumer: %v", err)
	}
	secondEvents, cancelSecondEvents := secondClient.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(cancelSecondEvents)
	t.Cleanup(func() {
		stopRiverClient(t, secondClient)
		secondListener.Close()
	})

	select {
	case <-barrier.secondStarted:
		// The second delivery entered actual source preparation while the first
		// remained blocked, demonstrating that the live shared limit was refreshed.
	case <-ctx.Done():
		close(barrier.releaseFirst)
		t.Fatalf("second operation did not start before the first was released: %v", ctx.Err())
	}

	firstState := fixture.readOperation(t, ctx, first.ID)
	secondState := fixture.readOperation(t, ctx, second.ID)
	if firstState.State != "running" || secondState.State != "running" {
		t.Fatalf("operation states while both preparations overlap: first=%q second=%q, want both running", firstState.State, secondState.State)
	}
	close(barrier.releaseFirst)

	awaitRiverCompletion(t, ctx, fixture.events, *first.RiverJobID)
	awaitRiverCompletion(t, ctx, secondEvents, *second.RiverJobID)
	for _, operation := range []*persistence.Operation{first, second} {
		if got := fixture.readOperation(t, ctx, operation.ID).State; got != "succeeded" {
			t.Errorf("operation %s state = %q, want succeeded", operation.ID, got)
		}
	}
}

type liveCapacityPreparer struct {
	calls         atomic.Int32
	firstStarted  chan struct{}
	secondStarted chan struct{}
	releaseFirst  chan struct{}
}

func (preparer *liveCapacityPreparer) Prepare(ctx context.Context, request service.SourceAnalysisPrepareRequest) service.SourceAnalysisPreparation {
	switch preparer.calls.Add(1) {
	case 1:
		close(preparer.firstStarted)
		select {
		case <-preparer.releaseFirst:
		case <-ctx.Done():
		}
	case 2:
		close(preparer.secondStarted)
	}
	return service.NewSourceAnalysisPreparer(service.SourceAnalysisPreparerConfig{}).Prepare(ctx, request)
}

func insertLiveCapacityWork(t *testing.T, ctx context.Context, fixture analysisDispatchFixture) *persistence.SourceAnalysisWork {
	t.Helper()
	const relativePath = "album/second.flac"
	if err := os.WriteFile(filepath.Join(fixture.source, relativePath), []byte("second audio bytes"), 0o600); err != nil {
		t.Fatalf("write second source file: %v", err)
	}
	location := registerAnalysisDispatchLocation(t, ctx, fixture.database, fixture.root, relativePath, 1)
	work := &persistence.SourceAnalysisWork{
		ID: uuid.New(), LocationID: location.ID, SourceRootID: fixture.root.ID,
		ConfiguredPath: fixture.root.ConfiguredPath, InventoryPath: fixture.root.ConfiguredPath,
		RelativePath: relativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime,
		SHA256Enabled: true, OriginScanOperationID: uuid.New(),
	}
	if err := fixture.inventory.StoreSourceAnalysisWork(ctx, work, []persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}}); err != nil {
		t.Fatalf("store second source analysis work: %v", err)
	}
	return work
}

func enqueueLiveCapacityOperation(t *testing.T, ctx context.Context, fixture analysisDispatchFixture, workID uuid.UUID) *persistence.Operation {
	t.Helper()
	shaStep := string(persistence.SourceStepSHA256)
	rerun := false
	snapshot, err := json.Marshal(persistence.SourceAnalysisOperationSnapshot{
		SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion,
		Mode:          persistence.SourceAnalysisModeBatch,
		WorkIDs:       []uuid.UUID{workID},
		RerunTarget:   &rerun,
		SelectedSteps: []persistence.SourceAnalysisStepSelection{{WorkID: workID, Step: persistence.SourceStepName(shaStep)}},
	})
	if err != nil {
		t.Fatalf("marshal source analysis intent: %v", err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceAnalysisOperationKind, State: "queued", Stage: service.SourceAnalysisStageQueued,
		InputSnapshot: snapshot, Attempt: 1, SourceAnalysisMode: persistence.SourceAnalysisModeBatch,
		TargetSourceRootID: &fixture.root.ID,
	}
	if err := fixture.inventory.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, fixture.client,
		service.SourceAnalysisJobArgs{OperationID: operation.ID}, &river.InsertOpts{Queue: service.SourceAnalysisQueue}); err != nil {
		t.Fatalf("enqueue source analysis operation: %v", err)
	}
	if operation.RiverJobID == nil {
		t.Fatal("source analysis operation has no River job")
	}
	return operation
}
