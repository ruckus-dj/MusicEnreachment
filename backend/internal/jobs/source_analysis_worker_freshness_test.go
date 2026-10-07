package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestVerifyAbsoluteSourceStillCurrentDetectsReplacedInventoryRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not allow renaming an inventory root while an open descendant file handle exists")
	}

	ctx := context.Background()
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "inventory")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	rootPath, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		t.Fatalf("resolve the inventory root: %v", err)
	}
	sourcePath := filepath.Join(rootPath, "track.flac")
	if err := os.WriteFile(sourcePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	opener := sourcefs.NewOpener()
	pinnedRoot, err := opener.OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pinnedRoot.Close() }()
	original, err := pinnedRoot.OpenRegular(ctx, "track.flac")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = original.Close() }()
	work := &persistence.SourceAnalysisWork{InventoryPath: rootPath, RelativePath: "track.flac", SizeBytes: info.Size(), Mtime: info.ModTime()}
	worker := &SourceAnalysisWorker{opener: opener}

	// Model a preparer replacing the namespace while the original pinned handles
	// remain valid and an otherwise-successful analysis result is being produced.
	if err := os.Rename(rootPath, filepath.Join(parent, "previous-inventory")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "track.flac"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := worker.verifyAbsoluteSourceStillCurrent(ctx, work, original, pinnedRoot); err == nil {
		t.Fatal("replacement absolute inventory root was accepted")
	}
	if _, err := os.Stat(filepath.Join(parent, "previous-inventory", "track.flac")); err != nil {
		t.Fatalf("pinned original inventory was not left available: %v", err)
	}
}

type interruptedDeliveryRepository struct {
	analysisWorkerRepository
	operation *persistence.Operation
	delivery  persistence.SourceAnalysisOperationDelivery
	recovered bool
	settled   bool
}

func (repository *interruptedDeliveryRepository) GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error) {
	return repository.operation, nil
}

func (repository *interruptedDeliveryRepository) RecoverNormalizedSourceAnalysisDelivery(_ context.Context, _ uuid.UUID, delivery persistence.SourceAnalysisOperationDelivery, _ string) error {
	repository.delivery = delivery
	repository.recovered = true
	repository.operation.State = "failed"
	repository.operation.Stage = "recovered"
	repository.operation.TargetSourceRootID = nil
	return nil
}

func (repository *interruptedDeliveryRepository) SettleNormalizedSourceAnalysisDelivery(context.Context, uuid.UUID, persistence.SourceAnalysisOperationDelivery, string, string, string) error {
	repository.settled = true
	return errors.New("unexpected terminal settlement")
}

type recoveredRootDispatcher struct {
	rootID uuid.UUID
	calls  int
}

func (dispatcher *recoveredRootDispatcher) AdmitPending(_ context.Context, rootID uuid.UUID) (*persistence.Operation, error) {
	dispatcher.rootID = rootID
	dispatcher.calls++
	return nil, nil
}

func TestSourceAnalysisWorkerRecoversSameRunningDeliveryWithoutTerminalSettlement(t *testing.T) {
	rootID := uuid.New()
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceAnalysisOperationKind, State: "running", Attempt: 4,
		RiverJobID: int64Pointer(82), TargetSourceRootID: &rootID,
	}
	repository := &interruptedDeliveryRepository{operation: operation}
	dispatcher := &recoveredRootDispatcher{}
	worker := NewSourceAnalysisWorker(repository, service.NewOperations(nil), nil, nil, settings.PlatformState{})
	worker.SetPendingDispatcher(dispatcher)
	job := &river.Job[service.SourceAnalysisJobArgs]{JobRow: &rivertype.JobRow{ID: 82}, Args: service.SourceAnalysisJobArgs{OperationID: operation.ID}}

	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("recover same running delivery: %v", err)
	}
	if !repository.recovered || repository.delivery != (persistence.SourceAnalysisOperationDelivery{Attempt: 4, JobID: 82}) {
		t.Fatalf("recovery did not use the captured delivery fence: %+v", repository)
	}
	if repository.settled {
		t.Fatal("running redelivery was settled as a terminal failure")
	}
	if dispatcher.calls != 1 || dispatcher.rootID != rootID {
		t.Fatalf("pending dispatcher = (%d calls, %s), want one admission for %s", dispatcher.calls, dispatcher.rootID, rootID)
	}
}

func int64Pointer(value int64) *int64 { return &value }
