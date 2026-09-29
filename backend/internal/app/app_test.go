package app

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type workerOperationRepository struct {
	*persistence.SetupManagerRepository
	operation    *persistence.Operation
	installation *persistence.ToolInstallation
}

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
			InputSnapshot: []byte("{"), TargetInstallationID: &installationID,
		},
		installation: &persistence.ToolInstallation{ID: installationID, State: "failed"},
	}
	workerOperations, apiOperations, _ := newOperationServices(repository)
	changed, unsubscribe := apiOperations.Subscribe(operationID)
	defer unsubscribe()
	other, unsubscribeOther := apiOperations.Subscribe(uuid.New())
	defer unsubscribeOther()

	// When an actual installation worker records its failed transition.
	worker := jobs.NewInstallationWorker(repository, workerOperations, tools.NewDefaultCatalog(nil),
		settings.New(workerSettingsStore{root: t.TempDir()}, nil), tools.Platform{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	job := &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: operationID}}
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
