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

const PublicationSourceCleanupRetryJobKind = "retry_publication_sources_v1"

// PublicationSourceCleanupRetryArgs contains only the durable operation identity.
type PublicationSourceCleanupRetryArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (PublicationSourceCleanupRetryArgs) Kind() string { return PublicationSourceCleanupRetryJobKind }

type PublicationSourceCleanupRetryExecutor interface {
	ExecutePublicationSourceCleanupExplicitRetry(context.Context, uuid.UUID, int64, int) (service.PublicationSourceCleanupRunOutcome, error)
}

// PublicationSourceCleanupRetryWorker delegates all guarded work and settlement
// to the explicit retry service executor. Its recovery acknowledgement is valid
// only after that synchronous call returns.
type PublicationSourceCleanupRetryWorker struct {
	river.WorkerDefaults[PublicationSourceCleanupRetryArgs]
	executor PublicationSourceCleanupRetryExecutor
}

func NewPublicationSourceCleanupRetryWorker(executor PublicationSourceCleanupRetryExecutor) *PublicationSourceCleanupRetryWorker {
	return &PublicationSourceCleanupRetryWorker{executor: executor}
}

func (worker *PublicationSourceCleanupRetryWorker) Work(ctx context.Context, job *river.Job[PublicationSourceCleanupRetryArgs]) error {
	if worker == nil || worker.executor == nil || ctx == nil || job == nil || job.JobRow == nil || job.Args.OperationID == uuid.Nil || job.ID < 1 || job.Attempt < 1 {
		return errors.New("publication source cleanup retry worker or delivery identity is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	outcome, executeErr := worker.executor.ExecutePublicationSourceCleanupExplicitRetry(ctx, job.Args.OperationID, job.ID, job.Attempt)
	switch outcome {
	case service.PublicationSourceCleanupCompleted:
		if executeErr != nil {
			return fmt.Errorf("execute publication source cleanup retry delivery: %w", executeErr)
		}
		return nil
	case service.PublicationSourceCleanupCancelled, service.PublicationSourceCleanupFailed:
		if executeErr != nil {
			slog.WarnContext(ctx, "publication source cleanup retry reached a durable terminal outcome with a diagnostic", "operation_id", job.Args.OperationID, "outcome", outcome, "error", executeErr)
		}
		return nil
	case service.PublicationSourceCleanupRecoveryRequired:
		if executeErr != nil {
			slog.WarnContext(ctx, "publication source cleanup retry requires recovery after native execution quiesced", "operation_id", job.Args.OperationID, "error", executeErr)
		}
		return nil
	case service.PublicationSourceCleanupBlocked:
		return publicationSourceCleanupRetryBlockedError(executeErr)
	default:
		return fmt.Errorf("publication source cleanup retry executor returned unknown outcome %q: %w", outcome, publicationSourceCleanupRetryDiagnostic(executeErr))
	}
}

func publicationSourceCleanupRetryBlockedError(err error) error {
	if err != nil {
		return fmt.Errorf("publication source cleanup retry delivery remains blocked: %w", err)
	}
	return errors.New("publication source cleanup retry delivery remains blocked")
}

func publicationSourceCleanupRetryDiagnostic(err error) error {
	if err != nil {
		return err
	}
	return errors.New("executor returned no diagnostic")
}
