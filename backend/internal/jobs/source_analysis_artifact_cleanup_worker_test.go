package jobs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type cleanupWorkerRepository struct {
	service.OperationRepository
	operation *persistence.Operation
	artifact  *persistence.SourceAnalysisArtifact
	outcomes  []persistence.SourceAnalysisArtifactCleanupOutcome
}

func (repository *cleanupWorkerRepository) GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error) {
	return repository.operation, nil
}

func (repository *cleanupWorkerRepository) TransitionOperationForDelivery(_ context.Context, _ uuid.UUID, _ int, _ int64, transition func(*persistence.Operation) error) (bool, error) {
	return true, transition(repository.operation)
}

func (repository *cleanupWorkerRepository) RunSourceAnalysisArtifactCleanupDelivery(ctx context.Context, _ persistence.SourceAnalysisArtifactCleanupFence, executor persistence.SourceAnalysisArtifactCleanupExecutor) error {
	if repository.artifact == nil {
		return nil
	}
	outcome, err := executor(ctx, *repository.artifact)
	if err != nil {
		return err
	}
	repository.outcomes = append(repository.outcomes, outcome)
	return nil
}

func TestSourceAnalysisArtifactCleanupUsesFrozenWorkDirectory(t *testing.T) {
	operationID, jobID := uuid.New(), int64(17)
	newWorker := func(repository *cleanupWorkerRepository) *SourceAnalysisArtifactCleanupWorker {
		return NewSourceAnalysisArtifactCleanupWorker(repository, service.NewOperations(repository))
	}
	run := func(t *testing.T, repository *cleanupWorkerRepository) {
		t.Helper()
		if err := newWorker(repository).Work(context.Background(), &river.Job[service.SourceAnalysisArtifactCleanupJobArgs]{
			JobRow: &rivertype.JobRow{ID: jobID}, Args: service.SourceAnalysisArtifactCleanupJobArgs{OperationID: operationID},
		}); err != nil {
			t.Fatal(err)
		}
	}
	operation := func() *persistence.Operation {
		return &persistence.Operation{
			ID: operationID, Kind: persistence.SourceAnalysisArtifactCleanupOperationKind,
			State: "queued", Attempt: 1, RiverJobID: &jobID,
		}
	}

	t.Run("registered artifact is removed beneath the frozen stage directory", func(t *testing.T) {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		relative := "analysis/staging/" + uuid.NewString() + "/" + uuid.NewString() + "/" + uuid.NewString()
		staged := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(staged, []byte("staged"), 0o600); err != nil {
			t.Fatal(err)
		}
		repository := &cleanupWorkerRepository{operation: operation(), artifact: &persistence.SourceAnalysisArtifact{
			RelativeOutputPath: relative, CleanupWorkDirectory: root,
		}}
		run(t, repository)
		if len(repository.outcomes) != 1 || !repository.outcomes[0].Succeeded {
			t.Fatalf("cleanup outcomes = %+v, want one succeeded", repository.outcomes)
		}
		if _, err := os.Stat(staged); !os.IsNotExist(err) {
			t.Fatalf("staged artifact still present after cleanup: %v", err)
		}
	})

	t.Run("unavailable frozen stage directory records a safe failure", func(t *testing.T) {
		repository := &cleanupWorkerRepository{operation: operation(), artifact: &persistence.SourceAnalysisArtifact{
			RelativeOutputPath: "analysis/staging/" + uuid.NewString() + "/" + uuid.NewString() + "/" + uuid.NewString(),
		}}
		run(t, repository)
		if len(repository.outcomes) != 1 || repository.outcomes[0].Succeeded || repository.outcomes[0].SafeError == "" {
			t.Fatalf("cleanup outcomes = %+v, want one safe failure", repository.outcomes)
		}
	})

	t.Run("no selected items never invokes the artifact callback", func(t *testing.T) {
		repository := &cleanupWorkerRepository{operation: operation()}
		run(t, repository)
		if len(repository.outcomes) != 0 {
			t.Fatalf("cleanup outcomes = %+v, want none", repository.outcomes)
		}
	})
}
