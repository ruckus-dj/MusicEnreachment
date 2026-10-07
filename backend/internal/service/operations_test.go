package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type memoryOperations struct {
	values map[uuid.UUID]*persistence.Operation
}

type fakeSourceAnalysisRetryer struct {
	called int
	target uuid.UUID
	result *persistence.Operation
}

func (retryer *fakeSourceAnalysisRetryer) RetryOperation(_ context.Context, id uuid.UUID) (*persistence.Operation, error) {
	retryer.called++
	retryer.target = id
	return retryer.result, nil
}

type riverMemoryOperations struct {
	*memoryOperations
	genericRetries int
}

func (*riverMemoryOperations) CreateOperationAndEnqueue(context.Context, *persistence.Operation, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) error {
	return nil
}

func (repository *riverMemoryOperations) RetryOperationAndEnqueue(_ context.Context, id uuid.UUID, _ persistence.RiverInserter, _ river.JobArgs, _ *river.InsertOpts) (*persistence.Operation, error) {
	repository.genericRetries++
	return repository.values[id], nil
}

func analysisRetryOperation(mode string) *persistence.Operation {
	workID := uuid.New()
	step := string(persistence.SourceStepSHA256)
	no := false
	snapshot := persistence.SourceAnalysisOperationSnapshot{
		SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion,
		Mode:          mode, WorkIDs: []uuid.UUID{workID}, TargetWorkID: &workID, TargetStep: &step,
		RerunTarget: &no, SHA256Enabled: &no, CacheOnlyReuse: &no,
		ToolsReadRequired: false, Tools: []persistence.SourceAnalysisToolSelection{},
	}
	var operationWorkID *uuid.UUID
	var operationStep *string
	if mode == persistence.SourceAnalysisModeBatch {
		snapshot.TargetWorkID = nil
		snapshot.TargetStep = nil
	} else {
		operationWorkID = &workID
		operationStep = &step
	}
	raw, _ := json.Marshal(snapshot)
	finished := time.Now().UTC()
	return &persistence.Operation{
		ID: uuid.New(), Kind: SourceAnalysisOperationKind, State: "failed", Stage: "failed",
		InputSnapshot: raw, SourceAnalysisMode: mode, TargetWorkID: operationWorkID, TargetStep: operationStep,
		RerunTarget: false, ToolsReadRequired: false, FinishedAt: &finished,
	}
}

func TestNormalizedSingleStepAnalysisRetryDelegatesAndNotifiesNewID(t *testing.T) {
	ctx := context.Background()
	existing := analysisRetryOperation(persistence.SourceAnalysisModeSingleStep)
	retried := &persistence.Operation{ID: uuid.New(), Kind: SourceAnalysisOperationKind, State: "queued"}
	retryer := &fakeSourceAnalysisRetryer{result: retried}
	repository := &riverMemoryOperations{memoryOperations: &memoryOperations{values: map[uuid.UUID]*persistence.Operation{existing.ID: existing}}}
	operations := NewOperationsWithRiver(repository, nil, retryer)
	oldWake, unsubscribeOld := operations.Subscribe(existing.ID)
	defer unsubscribeOld()
	newWake, unsubscribeNew := operations.Subscribe(retried.ID)
	defer unsubscribeNew()

	got, err := operations.Retry(ctx, existing.ID)
	if err != nil {
		t.Fatalf("retry analysis: %v", err)
	}
	if got != retried || retryer.called != 1 || retryer.target != existing.ID {
		t.Fatalf("retry delegation = (%p, calls %d, target %s), want (%p, 1, %s)", got, retryer.called, retryer.target, retried, existing.ID)
	}
	select {
	case <-newWake:
	default:
		t.Fatal("new operation subscriber was not notified")
	}
	select {
	case <-oldWake:
		t.Fatal("old operation subscriber was notified")
	default:
	}
	if repository.genericRetries != 0 {
		t.Fatalf("generic retry count = %d, want 0", repository.genericRetries)
	}
}

func TestAnalysisRetryRefusesBatchAndMalformedSnapshotsWithoutEnqueue(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation *persistence.Operation
	}{
		{name: "batch", operation: analysisRetryOperation(persistence.SourceAnalysisModeBatch)},
		{name: "legacy snapshot", operation: &persistence.Operation{ID: uuid.New(), Kind: SourceAnalysisOperationKind, State: "failed", InputSnapshot: []byte(`{"target":"old"}`)}},
	} {
		for _, withRiver := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/river=%t", test.name, withRiver), func(t *testing.T) {
				existing := *test.operation
				repository := &riverMemoryOperations{memoryOperations: &memoryOperations{values: map[uuid.UUID]*persistence.Operation{existing.ID: &existing}}}
				retryer := &fakeSourceAnalysisRetryer{result: &persistence.Operation{ID: uuid.New()}}
				var operations *Operations
				if withRiver {
					operations = NewOperationsWithRiver(repository, nil, retryer)
				} else {
					operations = NewOperations(repository)
					operations.analysisRetry = retryer
				}
				if _, err := operations.Retry(context.Background(), existing.ID); err == nil {
					t.Fatal("Retry succeeded for unsupported analysis shape")
				}
				if retryer.called != 0 || repository.genericRetries != 0 {
					t.Fatalf("retry calls = source %d, generic %d; want neither", retryer.called, repository.genericRetries)
				}
			})
		}
	}
}

func (m *memoryOperations) CreateOperation(_ context.Context, o *persistence.Operation) error {
	if m.values == nil {
		m.values = map[uuid.UUID]*persistence.Operation{}
	}
	m.values[o.ID] = o
	return nil
}
func (m *memoryOperations) GetOperation(_ context.Context, id uuid.UUID) (*persistence.Operation, error) {
	if o := m.values[id]; o != nil {
		return o, nil
	}
	return nil, fmt.Errorf("missing")
}
func (m *memoryOperations) ListOperations(context.Context, ...string) ([]persistence.Operation, error) {
	operations := make([]persistence.Operation, 0, len(m.values))
	for _, operation := range m.values {
		operations = append(operations, *operation)
	}
	return operations, nil
}
func (m *memoryOperations) UpdateOperation(_ context.Context, o *persistence.Operation) error {
	m.values[o.ID] = o
	return nil
}
func (m *memoryOperations) TransitionOperation(_ context.Context, id uuid.UUID, transition func(*persistence.Operation) error) error {
	o, err := m.GetOperation(context.Background(), id)
	if err != nil {
		return err
	}
	return transition(o)
}
func (*memoryOperations) DismissOperation(context.Context, uuid.UUID) error      { return nil }
func (*memoryOperations) DeleteSucceededBefore(context.Context, time.Time) error { return nil }
func TestOperationTransitionsAndRetry(t *testing.T) {
	ctx := context.Background()
	operations := NewOperations(&memoryOperations{})
	operation, err := operations.Start(ctx, "install", "download", map[string]string{"target_identity": "fpcalc"})
	if err != nil {
		t.Fatal(err)
	}
	if err := operations.Running(ctx, operation.ID, "download"); err != nil {
		t.Fatal(err)
	}
	if err := operations.Fail(ctx, operation.ID, "verify", "safe failure"); err != nil {
		t.Fatal(err)
	}
	retried, err := operations.Retry(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.State != "queued" || retried.Stage != "retry:verify" {
		t.Fatalf("first retry state/stage = %s/%s; want queued/retry:verify", retried.State, retried.Stage)
	}
	if err := operations.Fail(ctx, operation.ID, retried.Stage, "safe failure"); err != nil {
		t.Fatal(err)
	}
	retriedAgain, err := operations.Retry(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retriedAgain.Stage != "retry:verify" {
		t.Fatalf("repeated retry stage = %s; want retry:verify without a duplicate prefix", retriedAgain.Stage)
	}
}

// TestOperationSnapshotExposesTheScanTargetSourceRootID pins the correlation
// contract a root page needs: a scan exposes the source root it traverses, so a
// page opened for one root can tell its scan apart from a scan of another root,
// while an install or move operation, which has no source root, reports none.
func TestOperationSnapshotExposesTheScanTargetSourceRootID(t *testing.T) {
	ctx := context.Background()
	rootID := uuid.New()
	scanID, installID := uuid.New(), uuid.New()
	repository := &memoryOperations{values: map[uuid.UUID]*persistence.Operation{
		scanID: {
			ID: scanID, Kind: SourceScanOperationKind, State: "running", Stage: SourceScanStageTraversing,
			InputSnapshot:      []byte(`{"source_root_id":"` + rootID.String() + `"}`),
			TargetSourceRootID: &rootID,
		},
		installID: {ID: installID, Kind: "install", State: "running", Stage: "download"},
	}}
	operations := NewOperations(repository)

	scan, err := operations.Snapshot(ctx, scanID)
	if err != nil {
		t.Fatalf("read the scan snapshot: %v", err)
	}
	if scan.TargetSourceRootID == nil || *scan.TargetSourceRootID != rootID {
		t.Fatalf("scan snapshot target source root = %v, want %s", scan.TargetSourceRootID, rootID)
	}

	install, err := operations.Snapshot(ctx, installID)
	if err != nil {
		t.Fatalf("read the install snapshot: %v", err)
	}
	if install.TargetSourceRootID != nil {
		t.Fatalf("install snapshot target source root = %v, want none", install.TargetSourceRootID)
	}

	listed, err := operations.ListSnapshots(ctx)
	if err != nil {
		t.Fatalf("list the operation snapshots: %v", err)
	}
	seen := map[uuid.UUID]bool{}
	for _, snapshot := range listed {
		seen[snapshot.ID] = true
		switch snapshot.ID {
		case scanID:
			if snapshot.TargetSourceRootID == nil || *snapshot.TargetSourceRootID != rootID {
				t.Fatalf("listed scan target source root = %v, want %s", snapshot.TargetSourceRootID, rootID)
			}
		case installID:
			if snapshot.TargetSourceRootID != nil {
				t.Fatalf("listed install target source root = %v, want none", snapshot.TargetSourceRootID)
			}
		}
	}
	if !seen[scanID] || !seen[installID] {
		t.Fatalf("listed operations omit the fixtures: %v", seen)
	}
}
