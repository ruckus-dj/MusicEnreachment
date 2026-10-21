package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

const PublicationSourceCleanupJobKind = "cleanup_publication_sources_v1"

// PublicationSourceCleanupArgs contains only the durable operation identity.
type PublicationSourceCleanupArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (PublicationSourceCleanupArgs) Kind() string { return PublicationSourceCleanupJobKind }

type PublicationSourceCleanupExecutor interface {
	ExecutePublicationSourceCleanupDelivery(context.Context, uuid.UUID, int64, int) (service.PublicationSourceCleanupRunOutcome, error)
}

// PublicationSourceCleanupWorker delegates all guarded work and settlement to
// the service executor. Its recovery acknowledgement is valid only after that
// synchronous call returns.
type PublicationSourceCleanupWorker struct {
	river.WorkerDefaults[PublicationSourceCleanupArgs]
	executor PublicationSourceCleanupExecutor
}

func NewPublicationSourceCleanupWorker(executor PublicationSourceCleanupExecutor) *PublicationSourceCleanupWorker {
	return &PublicationSourceCleanupWorker{executor: executor}
}

func (worker *PublicationSourceCleanupWorker) Work(ctx context.Context, job *river.Job[PublicationSourceCleanupArgs]) error {
	if worker == nil || worker.executor == nil || ctx == nil || job == nil || job.JobRow == nil || job.Args.OperationID == uuid.Nil || job.ID < 1 || job.Attempt < 1 {
		return errors.New("publication source cleanup worker or delivery identity is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	outcome, executeErr := worker.executor.ExecutePublicationSourceCleanupDelivery(ctx, job.Args.OperationID, job.ID, job.Attempt)
	switch outcome {
	case service.PublicationSourceCleanupCompleted:
		if executeErr != nil {
			return fmt.Errorf("execute publication source cleanup delivery: %w", executeErr)
		}
		return nil
	case service.PublicationSourceCleanupCancelled, service.PublicationSourceCleanupFailed:
		if executeErr != nil {
			slog.WarnContext(ctx, "publication source cleanup reached a durable terminal outcome with a diagnostic", "operation_id", job.Args.OperationID, "outcome", outcome, "error", executeErr)
		}
		return nil
	case service.PublicationSourceCleanupRecoveryRequired:
		if executeErr != nil {
			slog.WarnContext(ctx, "publication source cleanup requires recovery after native execution quiesced", "operation_id", job.Args.OperationID, "error", executeErr)
		}
		return nil
	case service.PublicationSourceCleanupBlocked:
		return cleanupWorkerBlockedError(executeErr)
	default:
		return fmt.Errorf("publication source cleanup executor returned unknown outcome %q: %w", outcome, cleanupWorkerDiagnostic(executeErr))
	}
}

func cleanupWorkerBlockedError(err error) error {
	if err != nil {
		return fmt.Errorf("publication source cleanup delivery remains blocked: %w", err)
	}
	return errors.New("publication source cleanup delivery remains blocked")
}

func cleanupWorkerDiagnostic(err error) error {
	if err != nil {
		return err
	}
	return errors.New("executor returned no diagnostic")
}
