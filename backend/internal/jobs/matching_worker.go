package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/matchingpolicy"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type matchingWorkerRepository interface {
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	ClaimMatchingDelivery(context.Context, uuid.UUID, int, int64, int) (persistence.MatchingDeliveryClaim, persistence.MatchingDeliveryClaimOutcome, error)
	ConsumePendingMatchingDelivery(context.Context, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim) (bool, error)
	FailMatchingDelivery(context.Context, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim, string) (bool, error)
	PublishMatchingEvaluationForDelivery(context.Context, uuid.UUID, int, int64, int64, persistence.IncomingMatchEvaluation, map[uuid.UUID]*string) (bool, error)
}

type MatchingEvaluationWorker struct {
	river.WorkerDefaults[service.MatchingEvaluationOperationArgs]
	repository  matchingWorkerRepository
	evaluator   *service.MatchingEvaluationService
	operations  *service.Operations
	acquisition *service.MatchingAcquisitionService
}

func NewMatchingEvaluationWorker(repository matchingWorkerRepository, evaluator *service.MatchingEvaluationService, operations *service.Operations) *MatchingEvaluationWorker {
	return &MatchingEvaluationWorker{repository: repository, evaluator: evaluator, operations: operations}
}

func (worker *MatchingEvaluationWorker) WithAcquisition(acquisition *service.MatchingAcquisitionService) *MatchingEvaluationWorker {
	worker.acquisition = acquisition
	return worker
}

func (worker *MatchingEvaluationWorker) Work(ctx context.Context, job *river.Job[service.MatchingEvaluationOperationArgs]) error {
	if worker == nil || worker.repository == nil || worker.evaluator == nil || worker.operations == nil || job == nil || job.Args.OperationID == uuid.Nil || job.ID < 1 || job.Attempt < 1 {
		return fmt.Errorf("matching evaluation worker is not configured")
	}
	operation, err := worker.repository.GetOperation(ctx, job.Args.OperationID)
	if err != nil {
		return err
	}
	if operation.Kind != (service.MatchingEvaluationOperationArgs{}).Kind() {
		return fmt.Errorf("operation is not a matching evaluation")
	}
	if operation.State == "succeeded" || operation.State == "failed" {
		return nil
	}
	claim, outcome, err := worker.repository.ClaimMatchingDelivery(ctx, operation.ID, operation.Attempt, job.ID, job.Attempt)
	if err != nil {
		return err
	}
	switch outcome {
	case persistence.MatchingDeliveryNotClaimed:
		return nil
	case persistence.MatchingDeliveryBusy:
		return river.JobSnooze(time.Second)
	case persistence.MatchingDeliveryClaimed:
	default:
		return fmt.Errorf("matching delivery claim outcome is invalid")
	}

	var intent struct {
		SchemaVersion int             `json:"schema_version"`
		Plan          json.RawMessage `json:"plan"`
	}
	if err := decodeMatchingJSON(operation.InputSnapshot, &intent); err != nil || intent.SchemaVersion != 1 {
		return worker.failInvalidPlan(ctx, operation, claim, job.ID)
	}
	var plan struct {
		SchemaVersion        int                                  `json:"schema_version"`
		GroupingEpoch        int64                                `json:"grouping_epoch"`
		Targets              []persistence.MatchingTarget         `json:"targets"`
		PolicyVersion        string                               `json:"policy_version"`
		EvaluationQueryScope service.MatchingEvaluationQueryScope `json:"evaluation_query_scope"`
		ProviderRequests     []json.RawMessage                    `json:"provider_requests"`
	}
	if len(intent.Plan) == 0 || decodeMatchingJSON(intent.Plan, &plan) != nil || (plan.SchemaVersion != 2 && plan.SchemaVersion != 3) || len(plan.Targets) == 0 || len(plan.EvaluationQueryScope.GroupIDs) == 0 ||
		(plan.SchemaVersion == 2 && len(plan.EvaluationQueryScope.AcoustIDKeysByFile) != 0) ||
		(plan.SchemaVersion == 3 && len(plan.EvaluationQueryScope.AcoustIDKeysByFile) == 0) || !matchingpolicy.Supported(plan.PolicyVersion) {
		return worker.failInvalidPlan(ctx, operation, claim, job.ID)
	}
	if consumed, err := worker.repository.ConsumePendingMatchingDelivery(ctx, operation.ID, operation.Attempt, job.ID, claim); err != nil {
		return err
	} else if consumed {
		worker.operations.Notify(operation.ID)
		return nil
	}
	if worker.acquisition != nil {
		changed, acquired, err := worker.acquisition.Advance(ctx, operation.ID, operation.Attempt, job.ID, claim)
		if err != nil {
			if errors.Is(err, service.ErrInvalidMatchingAcquisitionResponse) {
				changed, failErr := worker.repository.FailMatchingDelivery(ctx, operation.ID, operation.Attempt, job.ID, claim, "The accepted provider response is invalid for matching.")
				if failErr != nil {
					return failErr
				}
				if changed {
					worker.operations.Notify(operation.ID)
				}
				return nil
			}
			if errors.Is(err, service.ErrMatchingAcquisitionProviderFailed) {
				changed, failErr := worker.repository.FailMatchingDelivery(ctx, operation.ID, operation.Attempt, job.ID, claim, "A required provider response failed, so matching cannot continue.")
				if failErr != nil {
					return failErr
				}
				if changed {
					worker.operations.Notify(operation.ID)
				}
				return nil
			}
			return err
		}
		if acquired {
			if changed {
				worker.operations.Notify(operation.ID)
			}
			return nil
		}
	}
	prepared, err := worker.evaluator.Prepare(ctx, plan.EvaluationQueryScope)
	if err != nil {
		return err
	}
	changed, err := worker.evaluator.PublishForDelivery(ctx, worker.repository, prepared, operation.ID, operation.Attempt, job.ID, claim.ExecutionEpoch)
	if err != nil {
		return err
	}
	if changed {
		worker.operations.Notify(operation.ID)
	}
	return nil
}

func (worker *MatchingEvaluationWorker) failInvalidPlan(ctx context.Context, operation *persistence.Operation, claim persistence.MatchingDeliveryClaim, jobID int64) error {
	changed, err := worker.repository.FailMatchingDelivery(ctx, operation.ID, operation.Attempt, jobID, claim, "The saved matching evaluation plan is invalid or unsupported.")
	if err != nil {
		return err
	}
	if changed {
		worker.operations.Notify(operation.ID)
	}
	return nil
}

func decodeMatchingJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("matching plan contains trailing data")
	}
	return nil
}

var _ river.Worker[service.MatchingEvaluationOperationArgs] = (*MatchingEvaluationWorker)(nil)
