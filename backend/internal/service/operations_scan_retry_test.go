package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// operationRetryRepositoryFixture plays the generic operation repository
// contract: the stored rows and the retry every install/move kind goes through.
type operationRetryRepositoryFixture struct {
	operations  map[uuid.UUID]*persistence.Operation
	genericArgs []river.JobArgs
	genericErr  error
}

func newOperationRetryRepositoryFixture(operations ...*persistence.Operation) *operationRetryRepositoryFixture {
	fixture := &operationRetryRepositoryFixture{operations: map[uuid.UUID]*persistence.Operation{}}
	for _, operation := range operations {
		fixture.operations[operation.ID] = operation
	}
	return fixture
}

func (fixture *operationRetryRepositoryFixture) CreateOperation(_ context.Context, operation *persistence.Operation) error {
	fixture.operations[operation.ID] = operation
	return nil
}

func (fixture *operationRetryRepositoryFixture) GetOperation(_ context.Context, id uuid.UUID) (*persistence.Operation, error) {
	if operation := fixture.operations[id]; operation != nil {
		return operation, nil
	}
	return nil, fmt.Errorf("get operation: no rows in result set")
}

func (fixture *operationRetryRepositoryFixture) ListOperations(context.Context, ...string) ([]persistence.Operation, error) {
	return nil, nil
}

func (fixture *operationRetryRepositoryFixture) UpdateOperation(context.Context, *persistence.Operation) error {
	return nil
}

func (fixture *operationRetryRepositoryFixture) TransitionOperation(context.Context, uuid.UUID, func(*persistence.Operation) error) error {
	return nil
}

func (fixture *operationRetryRepositoryFixture) DismissOperation(context.Context, uuid.UUID) error {
	return nil
}

func (fixture *operationRetryRepositoryFixture) DeleteSucceededBefore(context.Context, time.Time) error {
	return nil
}

func (fixture *operationRetryRepositoryFixture) CreateOperationAndEnqueue(context.Context, *persistence.Operation, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) error {
	return nil
}

func (fixture *operationRetryRepositoryFixture) RetryOperationAndEnqueue(_ context.Context, id uuid.UUID, _ persistence.RiverInserter, args river.JobArgs, _ *river.InsertOpts) (*persistence.Operation, error) {
	fixture.genericArgs = append(fixture.genericArgs, args)
	if fixture.genericErr != nil {
		return nil, fixture.genericErr
	}
	return fixture.requeued(id)
}

func (fixture *operationRetryRepositoryFixture) requeued(id uuid.UUID) (*persistence.Operation, error) {
	operation := fixture.operations[id]
	if operation == nil {
		return nil, fmt.Errorf("get operation: no rows in result set")
	}
	operation.State = "queued"
	operation.Stage = "retry:" + operation.Stage
	operation.Attempt++
	operation.SafeError = nil
	return operation, nil
}

// scanRetryRepositoryFixture adds the scan-specific retry the production
// repository implements, so a test can see which retry an operation kind was
// routed to and which River args it carried.
type scanRetryRepositoryFixture struct {
	*operationRetryRepositoryFixture
	scanArgs []river.JobArgs
	scanErr  error
}

func (fixture *scanRetryRepositoryFixture) RetrySourceScanOperationAndEnqueue(_ context.Context, id uuid.UUID, _ persistence.RiverInserter, args river.JobArgs, _ *river.InsertOpts) (*persistence.Operation, error) {
	fixture.scanArgs = append(fixture.scanArgs, args)
	if fixture.scanErr != nil {
		return nil, fixture.scanErr
	}
	return fixture.requeued(id)
}

func scanRetrySafeError(text string) *string { return &text }

// failedScanRetryOperation is the durable shape of a scan that failed before a
// retry: a stored snapshot of its root, a safe reason and a finish time, which
// is what both the state machine and the database require of a failed scan.
func failedScanRetryOperation(root *persistence.SourceRoot, stage string) *persistence.Operation {
	finished := time.Now().UTC()
	return &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "failed", Stage: stage,
		InputSnapshot: []byte(`{"schema_version":1,"source_root_id":"` + root.ID.String() +
			`","configured_path":"` + root.ConfiguredPath + `"}`),
		TargetSourceRootID: &root.ID, Attempt: 1, SafeError: scanRetrySafeError("The traversal failed."),
		FinishedAt: &finished,
	}
}

// TestScanRetryRoutesToTheScanJobKind pins the routing a retry of a failed scan
// depends on: the retry goes to the scan-specific enqueue, which delivers the
// job under the scan kind, and never to the generic retry whose job the
// install/move dispatcher would receive.
func TestScanRetryRoutesToTheScanJobKind(t *testing.T) {
	root := &persistence.SourceRoot{ID: uuid.New(), ConfiguredPath: "/srv/unit-retry"}
	scan := failedScanRetryOperation(root, service.SourceScanStageTraversing)
	fixture := &scanRetryRepositoryFixture{operationRetryRepositoryFixture: newOperationRetryRepositoryFixture(scan)}
	operations := service.NewOperationsWithRiver(fixture, nil)
	wake, unsubscribe := operations.Subscribe(scan.ID)
	defer unsubscribe()

	retried, err := operations.Retry(context.Background(), scan.ID)
	if err != nil {
		t.Fatalf("retry a failed scan: %v", err)
	}
	if len(fixture.genericArgs) != 0 {
		t.Fatalf("the scan retry carried the generic operation args %#v, want the scan-specific retry", fixture.genericArgs)
	}
	if len(fixture.scanArgs) != 1 {
		t.Fatalf("scan retry args = %#v, want exactly one", fixture.scanArgs)
	}
	args, ok := fixture.scanArgs[0].(service.ScanSourceJobArgs)
	if !ok || args.OperationID != scan.ID {
		t.Fatalf("scan retry args = %#v, want the operation id alone", fixture.scanArgs[0])
	}
	if kind := args.Kind(); kind != service.SourceScanJobKind {
		t.Fatalf("scan retry job kind = %q, want %q", kind, service.SourceScanJobKind)
	}
	if retried.ID != scan.ID {
		t.Fatalf("retried operation = %s, want the same operation %s", retried.ID, scan.ID)
	}
	select {
	case <-wake:
	default:
		t.Fatal("the retry did not wake the subscribers of the operation")
	}
}

// TestScanRetryNeverFallsBackToTheGenericJobKind covers a repository that
// cannot retry a scan: the retry must fail instead of enqueuing a job kind no
// scan worker handles.
func TestScanRetryNeverFallsBackToTheGenericJobKind(t *testing.T) {
	scan := failedScanRetryOperation(&persistence.SourceRoot{ID: uuid.New(), ConfiguredPath: "/srv/unit-retry"}, service.SourceScanStageApplying)
	fixture := newOperationRetryRepositoryFixture(scan)
	operations := service.NewOperationsWithRiver(fixture, nil)

	retried, err := operations.Retry(context.Background(), scan.ID)
	if err == nil {
		t.Fatalf("retry of a scan on a repository without the scan retry = %+v, want a refusal", retried)
	}
	if retried != nil {
		t.Fatalf("refused scan retry returned %+v, want no operation", retried)
	}
	if len(fixture.genericArgs) != 0 {
		t.Fatalf("the scan retry fell back to the generic operation args %#v", fixture.genericArgs)
	}
}

// TestScanRetryReportsTheTransactionRefusal pins that a refusal decided under
// the root lock (a disabled root or an active scan) reaches the caller unchanged
// and wakes nobody: nothing was requeued.
func TestScanRetryReportsTheTransactionRefusal(t *testing.T) {
	scan := failedScanRetryOperation(&persistence.SourceRoot{ID: uuid.New(), ConfiguredPath: "/srv/unit-retry"}, service.SourceScanStageTraversing)
	refusal := fmt.Errorf("retry source scan: the root %q is disabled: %w", "Music", persistence.ErrSourceRootDisabled)
	fixture := &scanRetryRepositoryFixture{
		operationRetryRepositoryFixture: newOperationRetryRepositoryFixture(scan),
		scanErr:                         refusal,
	}
	operations := service.NewOperationsWithRiver(fixture, nil)
	wake, unsubscribe := operations.Subscribe(scan.ID)
	defer unsubscribe()

	if _, err := operations.Retry(context.Background(), scan.ID); !errors.Is(err, persistence.ErrSourceRootDisabled) {
		t.Fatalf("retry of a scan on a disabled root = %v, want ErrSourceRootDisabled", err)
	}
	select {
	case <-wake:
		t.Fatal("a refused retry woke the subscribers of the operation")
	default:
	}
}

// TestInstallRetryKeepsTheGenericOperationJob is the regression guard of the
// other kinds: an install retry still carries the generic operation args.
func TestInstallRetryKeepsTheGenericOperationJob(t *testing.T) {
	installationID := uuid.New()
	install := &persistence.Operation{
		ID: uuid.New(), Kind: "install", State: "failed", Stage: "verify",
		TargetInstallationID: &installationID, Attempt: 1, SafeError: scanRetrySafeError("The installation failed."),
	}
	fixture := &scanRetryRepositoryFixture{operationRetryRepositoryFixture: newOperationRetryRepositoryFixture(install)}
	operations := service.NewOperationsWithRiver(fixture, nil)

	retried, err := operations.Retry(context.Background(), install.ID)
	if err != nil {
		t.Fatalf("retry a failed install: %v", err)
	}
	if len(fixture.scanArgs) != 0 {
		t.Fatalf("the install retry went to the scan retry with %#v", fixture.scanArgs)
	}
	if len(fixture.genericArgs) != 1 {
		t.Fatalf("generic retry args = %#v, want exactly one", fixture.genericArgs)
	}
	args, ok := fixture.genericArgs[0].(service.OperationJobArgs)
	if !ok || args.OperationID != install.ID || args.Kind() != "operation_v1" {
		t.Fatalf("generic retry args = %#v, want the operation id under operation_v1", fixture.genericArgs[0])
	}
	if retried.ID != install.ID || retried.TargetInstallationID == nil || *retried.TargetInstallationID != installationID {
		t.Fatalf("retried install = %+v, want the same operation and target", retried)
	}
}
