package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type moveWorkerRepository struct {
	*workerRepository
	root string
}

func (repository *moveWorkerRepository) ListInstallations(context.Context, string, string, string) ([]persistence.ToolInstallation, error) {
	return []persistence.ToolInstallation{*repository.installation}, nil
}

func (repository *moveWorkerRepository) CommitToolsRootMove(_ context.Context, operationID uuid.UUID, oldRoot, newRoot string) error {
	if repository.root != oldRoot {
		return errors.New("stale tools root")
	}
	repository.root = newRoot
	repository.operation.Stage = "switched"
	return nil
}

func (repository *moveWorkerRepository) FinishToolsRootMove(_ context.Context, operationID uuid.UUID) error {
	if repository.operation.ID != operationID || repository.operation.Stage != "switched" {
		return errors.New("move has not switched")
	}
	repository.operation.State = "succeeded"
	now := time.Now().UTC()
	repository.operation.FinishedAt = &now
	return nil
}

type moveWorkerSettings struct {
	root   string
	output string
}

func (settings moveWorkerSettings) GetToolsDirectory(context.Context) (string, bool, error) {
	return settings.root, true, nil
}

func (settings moveWorkerSettings) GetOutputDirectory(context.Context) (string, bool, error) {
	return settings.output, settings.output != "", nil
}

type moveCommandRunner struct {
	failName string
}

func (runner moveCommandRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	name := filepath.Base(executable)
	if len(args) != 1 || args[0] != "--version" {
		return nil, errors.New("unexpected verification command")
	}
	if name == runner.failName {
		return nil, errors.New("verification failed")
	}
	return []byte(strings.TrimSuffix(name, filepath.Ext(name)) + " version 8.0"), nil
}

func TestMoveWorkerCopiesOnlyManagedFilesAndKeepsUnknownData(t *testing.T) {
	oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, false, false)
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: oldRoot}
	ops := service.NewOperations(repository)
	worker := jobs.NewMoveWorker(repository, ops, moveWorkerSettings{root: oldRoot}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(moveCommandRunner{}))

	if err := worker.Work(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if repository.root != newRoot || operation.State != "succeeded" {
		t.Fatalf("move result root=%s operation=%s", repository.root, operation.State)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		target := filepath.Join(newRoot, installation.RelativePath, name)
		if digest, err := tools.SHA256File(target); err != nil || digest != fileIdentity(snapshot, name).SHA256 {
			t.Errorf("moved %s identity=%s err=%v", name, digest, err)
		}
	}
	if content, err := os.ReadFile(filepath.Join(newRoot, "operator-note.txt")); err != nil || string(content) != "keep" {
		t.Fatalf("unknown target-root file changed: %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(oldRoot, installation.RelativePath, "ffmpeg")); err != nil {
		t.Fatalf("old files were removed without the requested cleanup: %v", err)
	}
}

func TestMoveWorkerRestoresRootsAndConfirmedConflictsOnVerificationFailure(t *testing.T) {
	oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, true, true)
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: oldRoot}
	worker := jobs.NewMoveWorker(repository, service.NewOperations(repository), moveWorkerSettings{root: oldRoot}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(moveCommandRunner{failName: "ffprobe"}))

	if err := worker.Work(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if repository.root != oldRoot || operation.State != "failed" {
		t.Fatalf("failed move switched root: root=%s operation=%s", repository.root, operation.State)
	}
	conflict := snapshot.ConfirmedConflicts[0]
	if content, err := os.ReadFile(conflict); err != nil || string(content) != "old unknown target" {
		t.Fatalf("confirmed target was not restored: %q, %v", content, err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := os.Stat(filepath.Join(oldRoot, installation.RelativePath, name)); err != nil {
			t.Fatalf("old managed file %s was removed: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(newRoot, installation.RelativePath, "ffprobe")); !os.IsNotExist(err) {
		t.Fatalf("partial move target was not rolled back: %v", err)
	}
}

func TestMoveWorkerCanRemoveOnlyOldManagedFilesAfterSwitch(t *testing.T) {
	oldRoot, newRoot, operation, installation, _ := newMoveFixture(t, false, true)
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: oldRoot}
	worker := jobs.NewMoveWorker(repository, service.NewOperations(repository), moveWorkerSettings{root: oldRoot}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(moveCommandRunner{}))

	if err := worker.Work(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if repository.root != newRoot || operation.State != "succeeded" {
		t.Fatalf("move result root=%s operation=%s", repository.root, operation.State)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := os.Lstat(filepath.Join(oldRoot, installation.RelativePath, name)); !os.IsNotExist(err) {
			t.Errorf("old managed executable %s remains: %v", name, err)
		}
	}
	if content, err := os.ReadFile(filepath.Join(oldRoot, installation.RelativePath, "operator-note.txt")); err != nil || string(content) != "old note" {
		t.Fatalf("old unknown file changed: %q, %v", content, err)
	}
}

func newMoveFixture(t *testing.T, confirmed, removeOld bool) (string, string, *persistence.Operation, *persistence.ToolInstallation, service.MoveSnapshot) {
	t.Helper()
	oldRoot, err := settings.NormalizePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	newRoot, err := settings.NormalizePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "btbn", ReleaseIdentity: "8.0", RelativePath: "ffmpeg/8.0", State: "ready",
	}
	sourceDir := filepath.Join(oldRoot, installation.RelativePath)
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := make([]service.MoveFileIdentity, 0, 2)
	for _, name := range tools.ExpectedExecutables(tools.PackageFFmpeg, "linux") {
		source := filepath.Join(sourceDir, name)
		content := []byte(name + " version 8.0")
		if err := os.WriteFile(source, content, 0o755); err != nil {
			t.Fatal(err)
		}
		digest, err := tools.SHA256File(source)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, service.MoveFileIdentity{
			InstallationID: installation.ID, PackageKind: tools.PackageFFmpeg, ReleaseIdentity: "8.0",
			RelativePath: installation.RelativePath, Executable: name,
			SourcePath: source, TargetPath: filepath.Join(newRoot, installation.RelativePath, name),
			Size: int64(len(content)), SHA256: digest,
		})
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "operator-note.txt"), []byte("old note"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newRoot, "operator-note.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := service.MoveSnapshot{SchemaVersion: 1, OldRoot: oldRoot, NewRoot: newRoot, Files: files, RemoveOldFiles: removeOld}
	if confirmed {
		conflict := files[0].TargetPath
		if err := os.MkdirAll(filepath.Dir(conflict), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(conflict, []byte("old unknown target"), 0o644); err != nil {
			t.Fatal(err)
		}
		snapshot.ConfirmedConflicts = []string{conflict}
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued", InputSnapshot: raw}
	return oldRoot, newRoot, operation, installation, snapshot
}

func fileIdentity(snapshot service.MoveSnapshot, name string) service.MoveFileIdentity {
	for _, file := range snapshot.Files {
		if file.Executable == name {
			return file
		}
	}
	return service.MoveFileIdentity{}
}
