package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type matchingApplyWorkerRepository interface {
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	ClaimProviderOperationDelivery(context.Context, uuid.UUID, int, int64) (int64, bool, error)
	ApplyMatchingOperation(context.Context, uuid.UUID, int64, int64) (bool, error)
	ReleaseMatchingApplyDelivery(context.Context, uuid.UUID, int, int64, int64) error
}

// MatchingApplyWorker reloads the immutable operation intent; the River args
// contain only its durable operation ID. Execution epochs fence expired leases.
type MatchingApplyWorker struct {
	river.WorkerDefaults[persistence.MatchingApplyOperationArgs]
	repository matchingApplyWorkerRepository
	operations *service.Operations
}

func NewMatchingApplyWorker(repository matchingApplyWorkerRepository, operations *service.Operations) *MatchingApplyWorker {
	return &MatchingApplyWorker{repository: repository, operations: operations}
}

func (worker *MatchingApplyWorker) Work(ctx context.Context, job *river.Job[persistence.MatchingApplyOperationArgs]) error {
	if worker == nil || worker.repository == nil || worker.operations == nil || job == nil || job.Args.OperationID == uuid.Nil || job.ID < 1 || job.Attempt < 1 {
		return fmt.Errorf("matching apply worker received an invalid job")
	}
	operation, err := worker.repository.GetOperation(ctx, job.Args.OperationID)
	if err != nil {
		return err
	}
	if operation.Kind != persistence.MatchingApplyOperationKind || operation.RiverJobID == nil || *operation.RiverJobID != int64(job.ID) || (operation.State != "queued" && operation.State != "running") {
		return nil
	}
	var discriminator struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(operation.InputSnapshot, &discriminator); err != nil {
		return fmt.Errorf("matching apply worker received an invalid snapshot: %w", err)
	}
	if discriminator.SchemaVersion != 1 && discriminator.SchemaVersion != 2 {
		return fmt.Errorf("matching apply worker received an unsupported snapshot schema")
	}
	epoch, claimed, err := worker.repository.ClaimProviderOperationDelivery(ctx, operation.ID, operation.Attempt, int64(job.ID))
	if err != nil {
		return err
	}
	if !claimed {
		return river.JobSnooze(time.Second)
	}
	changed, err := worker.repository.ApplyMatchingOperation(ctx, operation.ID, int64(job.ID), epoch)
	if err != nil {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if releaseErr := worker.repository.ReleaseMatchingApplyDelivery(releaseCtx, operation.ID, operation.Attempt, int64(job.ID), epoch); releaseErr != nil {
			return fmt.Errorf("release failed matching apply delivery: %v: %w", releaseErr, err)
		}
		return err
	}
	if changed {
		worker.operations.Notify(operation.ID)
	}
	return nil
}

var _ river.Worker[persistence.MatchingApplyOperationArgs] = (*MatchingApplyWorker)(nil)
