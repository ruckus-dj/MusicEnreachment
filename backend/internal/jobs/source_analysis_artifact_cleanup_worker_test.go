package jobs

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type cleanupWorkerRepository struct {
	service.OperationRepository
	operation *persistence.Operation
	item      bool
	settings  *cleanupWorkerSettings
}

func (repository *cleanupWorkerRepository) GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error) {
	return repository.operation, nil
}

func (repository *cleanupWorkerRepository) TransitionOperationForDelivery(_ context.Context, _ uuid.UUID, _ int, _ int64, transition func(*persistence.Operation) error) (bool, error) {
	return true, transition(repository.operation)
}

func (repository *cleanupWorkerRepository) RunSourceAnalysisArtifactCleanupDelivery(ctx context.Context, _ persistence.SourceAnalysisArtifactCleanupFence, executor persistence.SourceAnalysisArtifactCleanupExecutor) error {
	// Simulate the output root changing while the shared output gate is being
	// acquired. The executor must resolve settings only after entering this callback.
	repository.settings.gateAcquired = true
	repository.settings.outputRoot = repository.settings.newOutputRoot
	if !repository.item {
		return nil
	}
	_, err := executor(ctx, persistence.SourceAnalysisArtifact{RelativeOutputPath: "staged/missing.bin"})
	return err
}

type cleanupWorkerSettings struct {
	gateAcquired  bool
	outputRoot    string
	newOutputRoot string
	reads         int
	readAfterGate bool
}

func (runtime *cleanupWorkerSettings) ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error) {
	runtime.reads++
	runtime.readAfterGate = runtime.gateAcquired
	return settings.RuntimeSettings{OutputDirectory: runtime.outputRoot}, nil
}

type cleanupServiceRepository struct{}

func (cleanupServiceRepository) ListSourceAnalysisArtifactCleanupCandidates(context.Context, int) ([]*persistence.SourceAnalysisArtifact, error) {
	return nil, nil
}

func (cleanupServiceRepository) AdmitSourceAnalysisArtifactCleanupWithArgsFactory(context.Context, []uuid.UUID, persistence.RiverInserter, func(uuid.UUID) river.JobArgs, *river.InsertOpts) (*persistence.Operation, error) {
	return nil, nil
}

func (cleanupServiceRepository) ListSourceAnalysisArtifactCleanupItems(context.Context, uuid.UUID) ([]persistence.SourceAnalysisArtifactCleanupItem, error) {
	return nil, nil
}

func TestSourceAnalysisArtifactCleanupResolvesOutputAfterGateAndOnlyForItems(t *testing.T) {
	for _, hasItem := range []bool{true, false} {
		t.Run(map[bool]string{true: "item", false: "no items"}[hasItem], func(t *testing.T) {
			oldRoot, newRoot := t.TempDir(), t.TempDir()
			settings := &cleanupWorkerSettings{outputRoot: oldRoot, newOutputRoot: newRoot}
			operationID := uuid.New()
			jobID := int64(17)
			repository := &cleanupWorkerRepository{
				operation: &persistence.Operation{
					ID: operationID, Kind: persistence.SourceAnalysisArtifactCleanupOperationKind,
					State: "queued", Attempt: 1, RiverJobID: &jobID,
				},
				item: hasItem, settings: settings,
			}
			cleanup := service.NewSourceAnalysisArtifactCleanup(cleanupServiceRepository{}, settings, nil)
			worker := NewSourceAnalysisArtifactCleanupWorker(repository, service.NewOperations(repository), cleanup)
			if err := worker.Work(context.Background(), &river.Job[service.SourceAnalysisArtifactCleanupJobArgs]{
				JobRow: &rivertype.JobRow{ID: jobID}, Args: service.SourceAnalysisArtifactCleanupJobArgs{OperationID: operationID},
			}); err != nil {
				t.Fatal(err)
			}
			wantReads := 0
			if hasItem {
				wantReads = 1
			}
			if settings.reads != wantReads {
				t.Fatalf("runtime settings reads=%d, want %d", settings.reads, wantReads)
			}
			if hasItem && (!settings.readAfterGate || settings.outputRoot != newRoot) {
				t.Fatalf("output settings were not resolved after gate acquisition: %+v", settings)
			}
		})
	}
}
