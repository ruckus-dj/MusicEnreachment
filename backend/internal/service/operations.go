// Package service coordinates application use cases without HTTP concerns.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type OperationRepository interface {
	CreateOperation(context.Context, *persistence.Operation) error
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	ListOperations(context.Context, ...string) ([]persistence.Operation, error)
	UpdateOperation(context.Context, *persistence.Operation) error
	TransitionOperation(context.Context, uuid.UUID, func(*persistence.Operation) error) error
	DismissOperation(context.Context, uuid.UUID) error
	DeleteSucceededBefore(context.Context, time.Time) error
}

type operationDeliveryTransitionRepository interface {
	TransitionOperationForDelivery(context.Context, uuid.UUID, int, int64, func(*persistence.Operation) error) (bool, error)
}

type OperationSnapshot struct {
	ID                     uuid.UUID
	Kind                   string
	State                  string
	Stage                  string
	TargetInstallationID   *uuid.UUID
	TargetSourceRootID     *uuid.UUID
	TargetSourceLocationID *uuid.UUID
	TargetIdentity         string
	BytesCompleted         int64
	BytesTotal             *int64
	SafeError              *string
	CreatedAt              time.Time
	StartedAt              *time.Time
	FinishedAt             *time.Time
	UpdatedAt              time.Time
}

type operationEnqueuingRepository interface {
	OperationRepository
	CreateOperationAndEnqueue(context.Context, *persistence.Operation, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) error
	RetryOperationAndEnqueue(context.Context, uuid.UUID, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) (*persistence.Operation, error)
}

// sourceScanRetryEnqueuer is the scan-specific half of a retry. A scan is
// delivered under its own River kind, so the generic operation retry would hand
// its job to the install/move dispatcher; the repository that owns the operation
// table implements this, and NewOperationsWithRiver discovers it once.
type sourceScanRetryEnqueuer interface {
	RetrySourceScanOperationAndEnqueue(context.Context, uuid.UUID, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) (*persistence.Operation, error)
}

// sourceAnalysisRetryer recreates a normalized single-step retry through the
// source-analysis service, which owns current-work validation and atomic admission.
type sourceAnalysisRetryer interface {
	RetryOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
}

type sourceAnalysisPendingAdmitter interface {
	AdmitPending(context.Context, uuid.UUID) (*persistence.Operation, error)
}

// operationArgs carries only the durable operation ID. Workers always reload
// their immutable inputs from the operation snapshot.
type OperationJobArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (OperationJobArgs) Kind() string { return "operation_v1" }

// Operations provides the REST source of truth; Subscribe is only a wake-up
// signal, so reconnecting clients must re-read the operation snapshot.
type Operations struct {
	repository    OperationRepository
	enqueuer      operationEnqueuingRepository
	scanRetry     sourceScanRetryEnqueuer
	analysisRetry sourceAnalysisRetryer
	pending       sourceAnalysisPendingAdmitter
	river         persistence.RiverInserter
	mu            sync.Mutex
	watchers      map[uuid.UUID]map[chan struct{}]struct{}
	now           func() time.Time
}

// SetPendingDispatcher wires event-driven pending analysis admission. It is
// optional so Operations remains usable by isolated state-machine callers.
func (s *Operations) SetPendingDispatcher(dispatcher sourceAnalysisPendingAdmitter) {
	s.pending = dispatcher
}

func NewOperations(repository OperationRepository) *Operations {
	return &Operations{repository: repository, watchers: map[uuid.UUID]map[chan struct{}]struct{}{}, now: time.Now}
}

// NewOperationsWithRiver makes production operation creation and retry atomic
// with their River jobs. The simpler constructor remains useful for pure state
// machine tests.
func NewOperationsWithRiver(repository operationEnqueuingRepository, client persistence.RiverInserter, analysisRetry ...sourceAnalysisRetryer) *Operations {
	operations := NewOperations(repository)
	operations.enqueuer = repository
	operations.river = client
	if scans, ok := repository.(sourceScanRetryEnqueuer); ok {
		operations.scanRetry = scans
	}
	if len(analysisRetry) > 0 {
		operations.analysisRetry = analysisRetry[0]
	}
	return operations
}

func (s *Operations) Start(ctx context.Context, kind, stage string, snapshot any) (*persistence.Operation, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	operation := &persistence.Operation{ID: uuid.New(), Kind: kind, State: "queued", Stage: stage, InputSnapshot: raw}
	if s.enqueuer != nil {
		err = s.enqueuer.CreateOperationAndEnqueue(ctx, operation, s.river, OperationJobArgs{OperationID: operation.ID}, nil)
	} else {
		err = s.repository.CreateOperation(ctx, operation)
	}
	if err != nil {
		return nil, err
	}
	s.notify(operation.ID)
	return operation, nil
}
func (s *Operations) Get(ctx context.Context, id uuid.UUID) (*persistence.Operation, error) {
	return s.repository.GetOperation(ctx, id)
}

func (s *Operations) Snapshot(ctx context.Context, id uuid.UUID) (OperationSnapshot, error) {
	operation, err := s.repository.GetOperation(ctx, id)
	if err != nil {
		return OperationSnapshot{}, err
	}
	return operationSnapshot(operation), nil
}

func (s *Operations) ListSnapshots(ctx context.Context, states ...string) ([]OperationSnapshot, error) {
	operations, err := s.repository.ListOperations(ctx, states...)
	if err != nil {
		return nil, err
	}
	snapshots := make([]OperationSnapshot, 0, len(operations))
	for index := range operations {
		snapshots = append(snapshots, operationSnapshot(&operations[index]))
	}
	return snapshots, nil
}

func operationSnapshot(operation *persistence.Operation) OperationSnapshot {
	var inputs struct {
		TargetIdentity string `json:"target_identity"`
	}
	_ = json.Unmarshal(operation.InputSnapshot, &inputs)
	return OperationSnapshot{
		ID: operation.ID, Kind: operation.Kind, State: operation.State, Stage: operation.Stage,
		TargetInstallationID: operation.TargetInstallationID, TargetSourceRootID: operation.TargetSourceRootID,
		TargetSourceLocationID: operation.TargetSourceLocationID,
		TargetIdentity:         inputs.TargetIdentity,
		BytesCompleted:         operation.BytesCompleted, BytesTotal: operation.BytesTotal, SafeError: operation.SafeError,
		CreatedAt: operation.CreatedAt, StartedAt: operation.StartedAt, FinishedAt: operation.FinishedAt, UpdatedAt: operation.UpdatedAt,
	}
}
func (s *Operations) Running(ctx context.Context, id uuid.UUID, stage string) error {
	return s.transition(ctx, id, "running", stage, "", nil)
}

// RunningForDelivery and its siblings fence stale River deliveries by the
// durable operation attempt and job ID. They are used by installation workers,
// whose job args intentionally contain only the operation ID.
func (s *Operations) RunningForDelivery(ctx context.Context, operation *persistence.Operation, stage string) error {
	return s.transitionForDelivery(ctx, operation, "running", stage, "", nil)
}

func (s *Operations) ProgressForDelivery(ctx context.Context, operation *persistence.Operation, stage string, completed int64, total *int64) error {
	return s.transitionForDelivery(ctx, operation, "running", stage, "", func(o *persistence.Operation) {
		o.BytesCompleted, o.BytesTotal = completed, total
	})
}

func (s *Operations) FailForDelivery(ctx context.Context, operation *persistence.Operation, stage, safe string) error {
	if safe == "" {
		return fmt.Errorf("operation failures require a safe error")
	}
	return s.transitionForDelivery(ctx, operation, "failed", stage, safe, nil)
}

func (s *Operations) transitionForDelivery(ctx context.Context, expected *persistence.Operation, state, stage, safe string, modify func(*persistence.Operation)) error {
	if expected == nil || expected.RiverJobID == nil {
		return fmt.Errorf("operation delivery identity is unavailable")
	}
	repository, ok := s.repository.(operationDeliveryTransitionRepository)
	if !ok {
		return fmt.Errorf("operation delivery fencing is unavailable")
	}
	now := s.now()
	changed, err := repository.TransitionOperationForDelivery(ctx, expected.ID, expected.Attempt, *expected.RiverJobID, func(operation *persistence.Operation) error {
		if operation.State == "succeeded" || operation.State == "failed" {
			return fmt.Errorf("operation is already final")
		}
		operation.State, operation.Stage = state, stage
		if state == "running" && operation.StartedAt == nil {
			operation.StartedAt = &now
		}
		if state == "failed" {
			operation.SafeError = &safe
			operation.FinishedAt = &now
		}
		if modify != nil {
			modify(operation)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !changed {
		return fmt.Errorf("operation delivery is no longer current")
	}
	s.notify(expected.ID)
	return nil
}
func (s *Operations) Progress(ctx context.Context, id uuid.UUID, stage string, completed int64, total *int64) error {
	return s.transition(ctx, id, "running", stage, "", func(o *persistence.Operation) { o.BytesCompleted = completed; o.BytesTotal = total })
}
func (s *Operations) Succeed(ctx context.Context, id uuid.UUID, stage string) error {
	return s.transition(ctx, id, "succeeded", stage, "", nil)
}
func (s *Operations) Fail(ctx context.Context, id uuid.UUID, stage, safe string) error {
	if safe == "" {
		return fmt.Errorf("operation failures require a safe error")
	}
	return s.transition(ctx, id, "failed", stage, safe, nil)
}
func (s *Operations) Retry(ctx context.Context, id uuid.UUID) (*persistence.Operation, error) {
	if s.enqueuer != nil {
		return s.retryEnqueued(ctx, id)
	}
	existing, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing.State != "failed" {
		return nil, fmt.Errorf("only failed operations can be retried")
	}
	if existing.Kind == SourceAnalysisArtifactCleanupOperationKind {
		return nil, fmt.Errorf("cleanup failures require a new explicit artifact selection")
	}
	if existing.Kind == SourceAnalysisOperationKind {
		return s.retryAnalysis(ctx, existing)
	}
	existing.State = "queued"
	if !strings.HasPrefix(existing.Stage, "retry:") {
		existing.Stage = "retry:" + existing.Stage
	}
	existing.SafeError = nil
	existing.StartedAt = nil
	existing.FinishedAt = nil
	existing.BytesCompleted = 0
	if err := s.repository.UpdateOperation(ctx, existing); err != nil {
		return nil, err
	}
	s.notify(id)
	return existing, nil
}

// retryEnqueued re-queues one failed operation with a new River job. The kind of
// an operation never changes, so reading it here routes the retry; whether a
// retry is allowed at all is decided again inside the transaction that does the
// work.
func (s *Operations) retryEnqueued(ctx context.Context, id uuid.UUID) (*persistence.Operation, error) {
	existing, err := s.repository.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	operation, err := s.enqueueRetry(ctx, existing)
	if err != nil {
		return nil, err
	}
	s.notify(operation.ID)
	return operation, nil
}

// enqueueRetry keeps the generic retry for every kind but a scan and an
// analysis. A scan retry is a new complete traversal of its root; normalized
// analysis retries are delegated to the source-analysis service.
func (s *Operations) enqueueRetry(ctx context.Context, existing *persistence.Operation) (*persistence.Operation, error) {
	switch existing.Kind {
	case SourceScanOperationKind:
		if s.scanRetry == nil {
			return nil, fmt.Errorf("retry operation: the repository cannot re-queue a source scan")
		}
		return s.scanRetry.RetrySourceScanOperationAndEnqueue(ctx, existing.ID, s.river, ScanSourceJobArgs{OperationID: existing.ID}, nil)
	case SourceAnalysisOperationKind:
		return s.retryAnalysis(ctx, existing)
	case SourceAnalysisArtifactCleanupOperationKind:
		return nil, fmt.Errorf("cleanup failures require a new explicit artifact selection")
	default:
		return s.enqueuer.RetryOperationAndEnqueue(ctx, existing.ID, s.river, OperationJobArgs{OperationID: existing.ID}, nil)
	}
}

func (s *Operations) retryAnalysis(ctx context.Context, existing *persistence.Operation) (*persistence.Operation, error) {
	if existing.State != "failed" {
		return nil, fmt.Errorf("only failed operations can be retried")
	}
	snapshot, err := persistence.ValidateSourceAnalysisOperationContract(existing)
	if err != nil {
		return nil, fmt.Errorf("retry operation: %w", err)
	}
	if snapshot.Mode != persistence.SourceAnalysisModeSingleStep {
		return nil, fmt.Errorf("retry operation: source analysis batch operations cannot be retried")
	}
	if s.analysisRetry == nil {
		return nil, fmt.Errorf("retry operation: source analysis retry is unavailable")
	}
	return s.analysisRetry.RetryOperation(ctx, existing.ID)
}
func (s *Operations) Dismiss(ctx context.Context, id uuid.UUID) error {
	return s.repository.DismissOperation(ctx, id)
}
func (s *Operations) Cleanup(ctx context.Context) error {
	return s.repository.DeleteSucceededBefore(ctx, s.now().Add(-24*time.Hour))
}
func (s *Operations) transition(ctx context.Context, id uuid.UUID, state, stage, safe string, modify func(*persistence.Operation)) error {
	var pendingRootID uuid.UUID
	err := s.repository.TransitionOperation(ctx, id, func(o *persistence.Operation) error {
		if o.State == "succeeded" || o.State == "failed" {
			return fmt.Errorf("operation is already final")
		}
		now := s.now()
		if (state == "succeeded" || state == "failed") && o.TargetSourceRootID != nil {
			pendingRootID = *o.TargetSourceRootID
		}
		o.State = state
		o.Stage = stage
		if state == "running" && o.StartedAt == nil {
			o.StartedAt = &now
		}
		if state == "failed" {
			o.SafeError = &safe
			o.FinishedAt = &now
		}
		if state == "succeeded" {
			o.FinishedAt = &now
		}
		if modify != nil {
			modify(o)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.notify(id)
	if pendingRootID != uuid.Nil && s.pending != nil {
		if _, err := s.pending.AdmitPending(context.WithoutCancel(ctx), pendingRootID); err != nil {
			slog.WarnContext(ctx, "pending source analysis admission failed after operation settled",
				"operation", id, "root", pendingRootID, "cause", err)
		}
	}
	return nil
}
func (s *Operations) Subscribe(id uuid.UUID) (<-chan struct{}, func()) {
	channel := make(chan struct{}, 1)
	s.mu.Lock()
	if s.watchers[id] == nil {
		s.watchers[id] = map[chan struct{}]struct{}{}
	}
	s.watchers[id][channel] = struct{}{}
	s.mu.Unlock()
	return channel, func() {
		s.mu.Lock()
		delete(s.watchers[id], channel)
		if len(s.watchers[id]) == 0 {
			delete(s.watchers, id)
		}
		close(channel)
		s.mu.Unlock()
	}
}

func (s *Operations) Notify(id uuid.UUID) {
	s.notify(id)
}

func (s *Operations) notify(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for channel := range s.watchers[id] {
		select {
		case channel <- struct{}{}:
		default:
		}
	}
}
