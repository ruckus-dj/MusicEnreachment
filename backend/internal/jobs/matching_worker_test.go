package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type matchingWorkerTestRepository struct {
	operation                *persistence.Operation
	outcome                  persistence.MatchingDeliveryClaimOutcome
	claimCalls, consumeCalls int
	failCalls                int
	consume                  bool
}

func (r *matchingWorkerTestRepository) GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error) {
	return r.operation, nil
}
func (r *matchingWorkerTestRepository) ClaimMatchingDelivery(context.Context, uuid.UUID, int, int64, int) (persistence.MatchingDeliveryClaim, persistence.MatchingDeliveryClaimOutcome, error) {
	r.claimCalls++
	return persistence.MatchingDeliveryClaim{ExecutionEpoch: 7}, r.outcome, nil
}
func (r *matchingWorkerTestRepository) ConsumePendingMatchingDelivery(context.Context, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim) (bool, error) {
	r.consumeCalls++
	return r.consume, nil
}
func (r *matchingWorkerTestRepository) FailMatchingDelivery(context.Context, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim, string) (bool, error) {
	r.failCalls++
	return true, nil
}
func (*matchingWorkerTestRepository) PublishMatchingEvaluationForDelivery(context.Context, uuid.UUID, int, int64, int64, persistence.IncomingMatchEvaluation, map[uuid.UUID]*string) (bool, error) {
	return false, nil
}

type matchingOperationTestRepository struct{ op *persistence.Operation }

func (r *matchingOperationTestRepository) CreateOperation(context.Context, *persistence.Operation) error {
	return errors.New("unexpected create")
}
func (r *matchingOperationTestRepository) GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error) {
	return r.op, nil
}
func (r *matchingOperationTestRepository) ListOperations(context.Context, ...string) ([]persistence.Operation, error) {
	return nil, nil
}
func (r *matchingOperationTestRepository) UpdateOperation(context.Context, *persistence.Operation) error {
	return nil
}
func (r *matchingOperationTestRepository) TransitionOperation(context.Context, uuid.UUID, func(*persistence.Operation) error) error {
	return nil
}
func (r *matchingOperationTestRepository) DismissOperation(context.Context, uuid.UUID) error {
	return nil
}
func (r *matchingOperationTestRepository) DeleteSucceededBefore(context.Context, time.Time) error {
	return nil
}

func matchingWorkerTestOperation() *persistence.Operation {
	id, jobID := uuid.New(), int64(41)
	return &persistence.Operation{ID: id, Kind: (service.MatchingEvaluationOperationArgs{}).Kind(), State: "running", Attempt: 2, RiverJobID: &jobID,
		InputSnapshot: []byte(`{"schema_version":1,"plan":{"schema_version":2,"policy_version":"` + service.MatchingPolicyVersion + `","targets":[{"group_id":"00000000-0000-4000-8000-000000000001","revision":"r1"}],"evaluation_query_scope":{"group_ids":["00000000-0000-4000-8000-000000000001"]}}}`)}
}

func TestMatchingWorkerNotClaimedDoesNotConsume(t *testing.T) {
	op := matchingWorkerTestOperation()
	repo := &matchingWorkerTestRepository{operation: op, outcome: persistence.MatchingDeliveryNotClaimed}
	worker := NewMatchingEvaluationWorker(repo, &service.MatchingEvaluationService{}, service.NewOperations(&matchingOperationTestRepository{op: op}))
	job := &river.Job[service.MatchingEvaluationOperationArgs]{JobRow: &rivertype.JobRow{ID: *op.RiverJobID, Attempt: 1}, Args: service.MatchingEvaluationOperationArgs{OperationID: op.ID}}
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if repo.claimCalls != 1 || repo.consumeCalls != 0 {
		t.Fatalf("claim=%d consume=%d; not-claimed must not consume", repo.claimCalls, repo.consumeCalls)
	}
}

func TestMatchingWorkerConsumesPendingDeliveryBeforeAcquisition(t *testing.T) {
	op := matchingWorkerTestOperation()
	repo := &matchingWorkerTestRepository{operation: op, outcome: persistence.MatchingDeliveryClaimed, consume: true}
	worker := NewMatchingEvaluationWorker(repo, &service.MatchingEvaluationService{}, service.NewOperations(&matchingOperationTestRepository{op: op})).
		WithAcquisition(&service.MatchingAcquisitionService{})
	job := &river.Job[service.MatchingEvaluationOperationArgs]{JobRow: &rivertype.JobRow{ID: *op.RiverJobID, Attempt: 1}, Args: service.MatchingEvaluationOperationArgs{OperationID: op.ID}}
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if repo.consumeCalls != 1 || repo.failCalls != 0 {
		t.Fatalf("consume=%d fail=%d; pending delivery should be consumed before acquisition", repo.consumeCalls, repo.failCalls)
	}
}

func TestMatchingWorkerBusySnoozesWithoutConsuming(t *testing.T) {
	op := matchingWorkerTestOperation()
	repo := &matchingWorkerTestRepository{operation: op, outcome: persistence.MatchingDeliveryBusy}
	worker := NewMatchingEvaluationWorker(repo, &service.MatchingEvaluationService{}, service.NewOperations(&matchingOperationTestRepository{op: op}))
	job := &river.Job[service.MatchingEvaluationOperationArgs]{JobRow: &rivertype.JobRow{ID: *op.RiverJobID, Attempt: 1}, Args: service.MatchingEvaluationOperationArgs{OperationID: op.ID}}
	err := worker.Work(context.Background(), job)
	var snooze *river.JobSnoozeError
	if !errors.As(err, &snooze) {
		t.Fatalf("busy delivery error=%v, want snooze", err)
	}
	if repo.claimCalls != 1 || repo.consumeCalls != 0 {
		t.Fatalf("claim=%d consume=%d; busy must preserve same delivery", repo.claimCalls, repo.consumeCalls)
	}
}

func TestMatchingWorkerFailsUnsupportedPolicyBeforeEvaluation(t *testing.T) {
	op := matchingWorkerTestOperation()
	op.InputSnapshot = []byte(`{"schema_version":1,"plan":{"schema_version":2,"targets":[{"group_id":"00000000-0000-4000-8000-000000000001","revision":"r1"}],"policy_version":"matching-go-v1","evaluation_query_scope":{"group_ids":["00000000-0000-4000-8000-000000000001"]}}}`)
	repo := &matchingWorkerTestRepository{operation: op, outcome: persistence.MatchingDeliveryClaimed}
	worker := NewMatchingEvaluationWorker(repo, &service.MatchingEvaluationService{}, service.NewOperations(&matchingOperationTestRepository{op: op}))
	job := &river.Job[service.MatchingEvaluationOperationArgs]{JobRow: &rivertype.JobRow{ID: *op.RiverJobID, Attempt: 1}, Args: service.MatchingEvaluationOperationArgs{OperationID: op.ID}}
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if repo.failCalls != 1 || repo.consumeCalls != 0 {
		t.Fatalf("fail=%d consume=%d; unsupported policy must fail before consuming/evaluating", repo.failCalls, repo.consumeCalls)
	}
}
