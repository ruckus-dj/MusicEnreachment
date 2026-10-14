package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	// Manual plans have their own immutable schema; do not decode them as
	// automatic acquisition intents while selecting the frozen fallback path.
	var mode struct {
		Mode string `json:"mode"`
	}
	if json.Unmarshal(operation.InputSnapshot, &mode) == nil && mode.Mode == "manual_review" {
		return worker.workManualReleaseLookup(ctx, operation, claim, job.ID)
	}
	if reader, ok := worker.repository.(interface {
		GetMatchingAcquisition(context.Context, uuid.UUID) (persistence.MatchingAcquisitionSnapshot, bool, error)
	}); ok {
		snapshot, exists, err := reader.GetMatchingAcquisition(ctx, operation.ID)
		if err != nil {
			return err
		}
		if exists && snapshot.Phase == persistence.MatchingAcquisitionFallbackEvaluationSealed {
			return worker.workFrozenFallbackEvaluation(ctx, operation, claim, job.ID, snapshot)
		}
	}
	var runFence persistence.MatchingRunFenceSnapshot
	var hasRunFence bool
	if reader, ok := worker.repository.(interface {
		GetMatchingRunFence(context.Context, uuid.UUID) (persistence.MatchingRunFenceSnapshot, bool, error)
	}); ok {
		var fenceErr error
		runFence, hasRunFence, fenceErr = reader.GetMatchingRunFence(ctx, operation.ID)
		if fenceErr != nil {
			return worker.failInvalidPlan(ctx, operation, claim, job.ID)
		}
	}
	if hasRunFence && runFence.Version == 2 && worker.acquisition == nil {
		return worker.failInvalidPlan(ctx, operation, claim, job.ID)
	}
	if worker.acquisition != nil {
		sealed, exists, err := worker.acquisition.EvaluationSeal(ctx, operation.ID)
		if err != nil {
			changed, failErr := worker.repository.FailMatchingDelivery(ctx, operation.ID, operation.Attempt, job.ID, claim, "The sealed matching evaluation input is invalid or unavailable.")
			if failErr != nil {
				return failErr
			}
			if changed {
				worker.operations.Notify(operation.ID)
			}
			return nil
		}
		if exists {
			if consumed, err := worker.repository.ConsumePendingMatchingDelivery(ctx, operation.ID, operation.Attempt, job.ID, claim); err != nil {
				return err
			} else if consumed {
				worker.operations.Notify(operation.ID)
				return nil
			}
			var prepared service.PreparedMatchingEvaluation
			if reader, ok := worker.repository.(interface {
				GetMatchingRunFence(context.Context, uuid.UUID) (persistence.MatchingRunFenceSnapshot, bool, error)
			}); ok {
				fence, exists, fenceErr := reader.GetMatchingRunFence(ctx, operation.ID)
				if fenceErr != nil {
					return fenceErr
				}
				if exists && fence.Version == 2 {
					prepared, err = worker.evaluator.PrepareSealedForRun(ctx, reader, operation.ID, sealed)
				} else {
					prepared, err = worker.evaluator.PrepareSealed(ctx, sealed)
				}
			} else {
				prepared, err = worker.evaluator.PrepareSealed(ctx, sealed)
			}
			if err != nil {
				if errors.Is(err, service.ErrMatchingEvaluationDeferred) {
					changed, failErr := worker.repository.FailMatchingDelivery(ctx, operation.ID, operation.Attempt, job.ID, claim, "The sealed matching input is no longer ready for evaluation.")
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
			changed, err := worker.evaluator.PublishForDelivery(ctx, worker.repository, prepared, operation.ID, operation.Attempt, job.ID, claim.ExecutionEpoch)
			if err != nil {
				return err
			}
			if changed {
				worker.operations.Notify(operation.ID)
			}
			return nil
		}
	}

	if hasRunFence && runFence.Version == 2 {
		if consumed, err := worker.repository.ConsumePendingMatchingDelivery(ctx, operation.ID, operation.Attempt, job.ID, claim); err != nil {
			return err
		} else if consumed {
			worker.operations.Notify(operation.ID)
			return nil
		}
		changed, acquired, err := worker.acquisition.Advance(ctx, operation.ID, operation.Attempt, job.ID, claim)
		if err != nil {
			var safeError string
			switch {
			case errors.Is(err, service.ErrInvalidMatchingAcquisitionResponse):
				slog.WarnContext(ctx, "accepted matching acquisition response is invalid", "operation", operation.ID, "cause", err)
				safeError = "The accepted provider response is invalid for matching."
			case errors.Is(err, service.ErrMatchingAcquisitionProviderFailed):
				safeError = "A required provider response failed, so matching cannot continue."
			default:
				return err
			}
			changed, err = worker.repository.FailMatchingDelivery(ctx, operation.ID, operation.Attempt, job.ID, claim, safeError)
			if err != nil {
				return err
			}
			if changed {
				worker.operations.Notify(operation.ID)
			}
			return nil
		}
		if changed {
			worker.operations.Notify(operation.ID)
		}
		if !acquired {
			return worker.failInvalidPlan(ctx, operation, claim, job.ID)
		}
		return nil
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
				slog.WarnContext(ctx, "accepted matching acquisition response is invalid", "operation", operation.ID, "cause", err)
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

func (worker *MatchingEvaluationWorker) workFrozenFallbackEvaluation(
	ctx context.Context,
	operation *persistence.Operation,
	claim persistence.MatchingDeliveryClaim,
	jobID int64,
	snapshot persistence.MatchingAcquisitionSnapshot,
) error {
	if worker.acquisition == nil || snapshot.Revision < 2 {
		return fmt.Errorf("sealed fallback evaluation is unavailable")
	}
	current, ok := worker.repository.(interface {
		IsMatchingDeliveryCurrent(context.Context, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim) (bool, error)
	})
	if !ok {
		return fmt.Errorf("sealed fallback delivery fence is unavailable")
	}
	valid, err := current.IsMatchingDeliveryCurrent(ctx, operation.ID, operation.Attempt, jobID, claim)
	if err != nil || !valid {
		return err
	}
	sealReader, ok := worker.repository.(interface {
		GetMatchingFallbackAuthoritySeal(context.Context, uuid.UUID, int64) (persistence.MatchingFallbackSealAuthority, bool, error)
	})
	if !ok {
		return fmt.Errorf("sealed fallback authority reader is unavailable")
	}
	waitReader, ok := worker.repository.(interface {
		GetCurrentMatchingFallbackEvaluationWaitRevision(context.Context, uuid.UUID) (int64, bool, error)
	})
	if !ok {
		return fmt.Errorf("current fallback evaluation wait reader is unavailable")
	}
	waitRevision, exists, err := waitReader.GetCurrentMatchingFallbackEvaluationWaitRevision(ctx, operation.ID)
	if err != nil {
		return err
	}
	if !exists || waitRevision+1 != snapshot.Revision {
		return fmt.Errorf("sealed fallback evaluation wait is not current")
	}
	prepared, exists, err := service.PrepareFrozenFallbackEvaluation(ctx, sealReader, operation.ID, waitRevision)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("sealed fallback authority is unavailable")
	}
	publication, err := service.SerializeFrozenFallbackEvaluation(prepared)
	if err != nil {
		return err
	}
	groupID := publication.Authority.GroupID
	revisionReader, ok := worker.repository.(interface {
		Get(context.Context, uuid.UUID) (*persistence.IncomingMatchEvaluationHeader, []persistence.IncomingMatchCandidate, error)
	})
	if !ok {
		return fmt.Errorf("sealed fallback evaluation revision reader is unavailable")
	}
	header, _, err := revisionReader.Get(ctx, groupID)
	if err != nil {
		return err
	}
	expected := map[uuid.UUID]*string{groupID: nil}
	if header != nil {
		revision := header.EvaluationRevision
		expected[groupID] = &revision
	}
	publisher, ok := worker.repository.(interface {
		PublishFrozenMatchingEvaluationForDelivery(context.Context, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim, persistence.FrozenMatchingPublication, map[uuid.UUID]*string) (bool, error)
	})
	if !ok {
		return fmt.Errorf("sealed fallback publication persistence is unavailable")
	}
	changed, err := publisher.PublishFrozenMatchingEvaluationForDelivery(ctx, operation.ID, operation.Attempt, jobID, claim, publication, expected)
	if err != nil {
		return err
	}
	if changed {
		worker.operations.Notify(operation.ID)
	}
	return nil
}

func (worker *MatchingEvaluationWorker) workManualReleaseLookup(ctx context.Context, operation *persistence.Operation, claim persistence.MatchingDeliveryClaim, jobID int64) error {
	var input struct {
		SchemaVersion int             `json:"schema_version"`
		Mode          string          `json:"mode"`
		RequestID     uuid.UUID       `json:"request_id"`
		GroupID       uuid.UUID       `json:"group_id"`
		GroupRevision string          `json:"group_revision"`
		Evaluation    *string         `json:"evaluation_revision"`
		ReleaseMBID   uuid.UUID       `json:"selected_release_mbid"`
		Plan          json.RawMessage `json:"plan"`
		GroupContext  json.RawMessage `json:"group_context"`
		SourceContext json.RawMessage `json:"source_context"`
		LookupID      uuid.UUID       `json:"lookup_operation_id"`
	}
	fail := func() error {
		changed, err := worker.repository.FailMatchingDelivery(ctx, operation.ID, operation.Attempt, jobID, claim, "The saved manual matching lookup is invalid or no longer current.")
		if err != nil {
			return err
		}
		if changed {
			worker.operations.Notify(operation.ID)
		}
		return nil
	}
	if decodeMatchingJSON(operation.InputSnapshot, &input) != nil || input.SchemaVersion != 1 || input.Mode != "manual_review" ||
		input.RequestID == uuid.Nil || input.GroupID == uuid.Nil || input.GroupRevision == "" || input.Evaluation != nil && *input.Evaluation == "" ||
		input.ReleaseMBID == uuid.Nil || input.LookupID == uuid.Nil || !json.Valid(input.Plan) || !json.Valid(input.GroupContext) || !json.Valid(input.SourceContext) {
		return fail()
	}
	getter, ok := worker.repository.(interface {
		GetManualReleaseMatchingRun(context.Context, uuid.UUID) (persistence.ManualMatchingRunSnapshot, bool, error)
	})
	if !ok {
		return fail()
	}
	snapshot, exists, err := getter.GetManualReleaseMatchingRun(ctx, input.RequestID)
	if err != nil {
		return err
	}
	if !exists || snapshot.RunID != operation.ID || snapshot.GroupID != input.GroupID || snapshot.LookupOperationID != input.LookupID || snapshot.SelectedReleaseMBID != input.ReleaseMBID || snapshot.GroupRevision != input.GroupRevision || !manualRevisionMatches(snapshot.ExpectedEvaluationRevision, input.Evaluation) {
		return fail()
	}
	if snapshot.Pending {
		consumed, err := worker.repository.ConsumePendingMatchingDelivery(ctx, operation.ID, operation.Attempt, jobID, claim)
		if err != nil {
			return err
		}
		if consumed {
			worker.operations.Notify(operation.ID)
		}
		return nil
	}
	prepared, err := worker.evaluator.PrepareManualReleaseLookup(ctx, snapshot)
	if err != nil {
		return fail()
	}
	changed, err := worker.evaluator.PublishForDelivery(ctx, worker.repository, prepared, operation.ID, operation.Attempt, jobID, claim.ExecutionEpoch)
	if err != nil {
		return err
	}
	if changed {
		worker.operations.Notify(operation.ID)
	}
	return nil
}

func manualRevisionMatches(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
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
