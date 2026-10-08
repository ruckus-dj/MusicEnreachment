package jobs

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type sourceAnalysisArtifactCleanupRepository interface {
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	RunSourceAnalysisArtifactCleanupDelivery(context.Context, persistence.SourceAnalysisArtifactCleanupFence, persistence.SourceAnalysisArtifactCleanupExecutor) error
}

type SourceAnalysisArtifactCleanupWorker struct {
	river.WorkerDefaults[service.SourceAnalysisArtifactCleanupJobArgs]
	repository sourceAnalysisArtifactCleanupRepository
	operations *service.Operations
	cleanup    *service.SourceAnalysisArtifactCleanup
	cleaner    sourcefs.Cleaner
}

func NewSourceAnalysisArtifactCleanupWorker(repository sourceAnalysisArtifactCleanupRepository, operations *service.Operations, cleanup *service.SourceAnalysisArtifactCleanup) *SourceAnalysisArtifactCleanupWorker {
	return &SourceAnalysisArtifactCleanupWorker{repository: repository, operations: operations, cleanup: cleanup, cleaner: sourcefs.NewCleaner()}
}

func (worker *SourceAnalysisArtifactCleanupWorker) Work(ctx context.Context, job *river.Job[service.SourceAnalysisArtifactCleanupJobArgs]) error {
	operation, err := worker.repository.GetOperation(ctx, job.Args.OperationID)
	if err != nil {
		return err
	}
	if operation.Kind != persistence.SourceAnalysisArtifactCleanupOperationKind || operation.RiverJobID == nil || *operation.RiverJobID != job.ID {
		return errCleanupDeliveryFence
	}
	fence := persistence.SourceAnalysisArtifactCleanupFence{OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: job.ID}
	if err := worker.operations.RunningForDelivery(ctx, operation, "cleaning_artifacts"); err != nil {
		return err
	}
	var outputRoot string
	var settingsErr error
	settingsResolved := false
	err = worker.repository.RunSourceAnalysisArtifactCleanupDelivery(ctx, fence, func(ctx context.Context, artifact persistence.SourceAnalysisArtifact) (persistence.SourceAnalysisArtifactCleanupOutcome, error) {
		if !settingsResolved {
			outputRoot, settingsErr = worker.cleanup.OutputDirectory(ctx)
			settingsResolved = true
		}
		if settingsErr != nil || outputRoot == "" {
			return persistence.SourceAnalysisArtifactCleanupOutcome{SafeError: "The managed output directory is unavailable."}, nil
		}
		_, err := worker.cleaner.RemoveRegistered(ctx, outputRoot, artifact.RelativeOutputPath)
		if err != nil {
			return persistence.SourceAnalysisArtifactCleanupOutcome{SafeError: "The staged analysis artifact could not be cleaned up."}, nil
		}
		return persistence.SourceAnalysisArtifactCleanupOutcome{Succeeded: true}, nil
	})
	if err == nil {
		worker.operations.Notify(operation.ID)
	}
	return err
}

var errCleanupDeliveryFence = errors.New("source analysis artifact cleanup delivery fence is stale")

var _ river.Worker[service.SourceAnalysisArtifactCleanupJobArgs] = (*SourceAnalysisArtifactCleanupWorker)(nil)
