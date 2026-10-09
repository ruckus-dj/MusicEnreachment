package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type appSourceAnalysisRetryer struct {
	called int
	target uuid.UUID
	result *persistence.Operation
}

func (retryer *appSourceAnalysisRetryer) RetryOperation(_ context.Context, id uuid.UUID) (*persistence.Operation, error) {
	retryer.called++
	retryer.target = id
	return retryer.result, nil
}

type workerOperationRepository struct {
	*persistence.SetupManagerRepository
	operation     *persistence.Operation
	installation  *persistence.ToolInstallation
	claimAttempt  int
	claimJobID    int64
	claimBorrower string
	claimHeld     bool
}

func appWorkerDeliveryID() *int64 { id := int64(1); return &id }

func (repository *workerOperationRepository) GetOperation(_ context.Context, _ uuid.UUID) (*persistence.Operation, error) {
	return repository.operation, nil
}

func (repository *workerOperationRepository) GetInstallation(_ context.Context, _ uuid.UUID) (*persistence.ToolInstallation, error) {
	return repository.installation, nil
}

func (repository *workerOperationRepository) TransitionOperation(_ context.Context, id uuid.UUID, transition func(*persistence.Operation) error) error {
	if id != repository.operation.ID {
		return context.Canceled
	}
	return transition(repository.operation)
}

func (repository *workerOperationRepository) TransitionOperationForDelivery(_ context.Context, id uuid.UUID, attempt int, jobID int64, transition func(*persistence.Operation) error) (bool, error) {
	operation := repository.operation
	if operation == nil || operation.ID != id || operation.Attempt != attempt || operation.RiverJobID == nil || *operation.RiverJobID != jobID {
		return false, nil
	}
	if err := transition(operation); err != nil {
		return false, err
	}
	return true, nil
}

func (repository *workerOperationRepository) ClaimToolsExecutionDelivery(_ context.Context, id uuid.UUID, attempt int, jobID int64, borrower string) (bool, error) {
	operation := repository.operation
	if operation == nil || operation.ID != id || operation.Kind != borrower || operation.Attempt != attempt ||
		operation.RiverJobID == nil || *operation.RiverJobID != jobID ||
		(operation.State != "queued" && operation.State != "running" && operation.State != "succeeded" && operation.State != "failed") {
		return false, nil
	}
	if repository.claimHeld {
		return false, persistence.ErrToolsExecutionClaimHeld
	}
	repository.claimAttempt, repository.claimJobID, repository.claimBorrower = attempt, jobID, borrower
	repository.claimHeld = true
	if operation.State == "queued" {
		operation.State = "running"
	}
	return true, nil
}

func (repository *workerOperationRepository) ReleaseToolsExecutionDelivery(_ context.Context, id uuid.UUID, attempt int, jobID int64, borrower string) error {
	if repository.operation != nil && repository.operation.ID == id && repository.claimHeld &&
		repository.claimAttempt == attempt && repository.claimJobID == jobID && repository.claimBorrower == borrower {
		repository.claimHeld = false
	}
	return nil
}

func (repository *workerOperationRepository) AbandonToolsExecutionDelivery(_ context.Context, id uuid.UUID, attempt int, jobID int64, borrower string) error {
	if repository.operation != nil && repository.operation.ID == id && repository.claimHeld &&
		repository.claimAttempt == attempt && repository.claimJobID == jobID && repository.claimBorrower == borrower {
		repository.claimHeld = false
	}
	return nil
}

func (repository *workerOperationRepository) FailInstallationDelivery(_ context.Context, id uuid.UUID, attempt int, jobID int64, target uuid.UUID, stage, safe string) (bool, error) {
	op := repository.operation
	if op == nil || op.ID != id || op.Attempt != attempt || op.RiverJobID == nil || *op.RiverJobID != jobID ||
		op.TargetInstallationID == nil || *op.TargetInstallationID != target || op.State == "failed" || op.State == "succeeded" {
		return false, nil
	}
	if repository.installation == nil || repository.installation.ID != target {
		return false, context.Canceled
	}
	repository.installation.State = "failed"
	op.State, op.Stage, op.SafeError = "failed", stage, &safe
	return true, nil
}

type workerSettingsStore struct {
	settings.Store
	root string
}

func (store workerSettingsStore) Get(_ context.Context, key string) (string, bool, error) {
	if key == settings.ToolsDirectoryKey {
		return store.root, true, nil
	}
	return "", false, nil
}

func TestWorkerTransitionWakesAPISubscriber(t *testing.T) {
	// Given a subscriber attached through the production composition seam.
	operationID, installationID := uuid.New(), uuid.New()
	repository := &workerOperationRepository{
		operation: &persistence.Operation{
			ID: operationID, Kind: "install", State: "queued", Stage: "queued",
			Attempt: 1, RiverJobID: appWorkerDeliveryID(),
			InputSnapshot: []byte("{"), TargetInstallationID: &installationID,
		},
		installation: &persistence.ToolInstallation{ID: installationID, State: "failed"},
	}
	workerOperations, apiOperations, _ := newOperationServices(repository, &riverClientSlot{}, nil)
	changed, unsubscribe := apiOperations.Subscribe(operationID)
	defer unsubscribe()
	other, unsubscribeOther := apiOperations.Subscribe(uuid.New())
	defer unsubscribeOther()

	// When an actual installation worker records its failed transition.
	worker := jobs.NewInstallationWorker(repository, workerOperations, tools.NewDefaultCatalog(nil),
		settings.New(workerSettingsStore{root: t.TempDir()}, nil), tools.Platform{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	job := &river.Job[service.OperationJobArgs]{JobRow: &rivertype.JobRow{ID: 1}, Args: service.OperationJobArgs{OperationID: operationID}}
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("worker transition: %v", err)
	}

	// Then that operation, and no unrelated operation, wakes its API subscriber.
	select {
	case <-changed:
		if repository.operation.State != "failed" {
			t.Fatalf("operation state = %q, want failed", repository.operation.State)
		}
	case <-ctx.Done():
		t.Fatal("worker transition did not wake the API subscriber")
	}
	select {
	case <-other:
		t.Fatal("unrelated operation subscriber was woken")
	default:
	}
}

func TestOperationServicesInjectSourceAnalysisRetryer(t *testing.T) {
	workID := uuid.New()
	step := string(persistence.SourceStepSHA256)
	no := false
	snapshot, err := json.Marshal(persistence.SourceAnalysisOperationSnapshot{
		SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion,
		Mode:          persistence.SourceAnalysisModeSingleStep,
		WorkIDs:       []uuid.UUID{workID}, TargetWorkID: &workID, TargetStep: &step,
		RerunTarget: &no, SHA256Enabled: &no, CacheOnlyReuse: &no,
		ToolsReadRequired: false, Tools: []persistence.SourceAnalysisToolSelection{},
	})
	if err != nil {
		t.Fatal(err)
	}
	original := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceAnalysisOperationKind, State: "failed", InputSnapshot: snapshot,
		SourceAnalysisMode: persistence.SourceAnalysisModeSingleStep, TargetWorkID: &workID, TargetStep: &step,
	}
	repository := &workerOperationRepository{operation: original}
	retried := &persistence.Operation{ID: uuid.New(), State: "queued"}
	retryer := &appSourceAnalysisRetryer{result: retried}
	slot := &riverClientSlot{}
	operations, _, returnedSlot := newOperationServices(repository, slot, retryer)

	got, err := operations.Retry(context.Background(), original.ID)
	if err != nil {
		t.Fatalf("retry through composed operations service: %v", err)
	}
	if got != retried || retryer.called != 1 || retryer.target != original.ID {
		t.Fatalf("retry result/calls/target = (%p, %d, %s), want (%p, 1, %s)", got, retryer.called, retryer.target, retried, original.ID)
	}
	if returnedSlot != slot {
		t.Fatal("operation services did not retain the shared River client slot")
	}
}
