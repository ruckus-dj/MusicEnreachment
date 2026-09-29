package jobs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

type moveWorkerRepository struct {
	*workerRepository
	root         string
	beforeCommit func() error
	afterCommit  func()
	rollbackErr  error
	finishErr    error
}

func (repository *moveWorkerRepository) ListInstallations(context.Context, string, string, string) ([]persistence.ToolInstallation, error) {
	return []persistence.ToolInstallation{*repository.installation}, nil
}

func (repository *moveWorkerRepository) CommitToolsRootMove(_ context.Context, operationID uuid.UUID, oldRoot, newRoot string) error {
	if repository.root != oldRoot {
		return errors.New("stale tools root")
	}
	if repository.beforeCommit != nil {
		if err := repository.beforeCommit(); err != nil {
			return err
		}
	}
	repository.root = newRoot
	repository.operation.Stage = "switched"
	if repository.afterCommit != nil {
		repository.afterCommit()
	}
	return nil
}

func (repository *moveWorkerRepository) FinishToolsRootMove(_ context.Context, operationID uuid.UUID) error {
	if repository.finishErr != nil {
		err := repository.finishErr
		repository.finishErr = nil
		return err
	}
	if repository.operation.ID != operationID || repository.operation.Stage != "switched" {
		return errors.New("move has not switched")
	}
	repository.operation.State = "succeeded"
	now := time.Now().UTC()
	repository.operation.FinishedAt = &now
	return nil
}

func (repository *moveWorkerRepository) RollbackToolsRootMove(_ context.Context, operationID uuid.UUID, oldRoot, newRoot string) error {
	if repository.rollbackErr != nil {
		err := repository.rollbackErr
		repository.rollbackErr = nil
		return err
	}
	if repository.operation.ID != operationID ||
		(repository.operation.Stage != "switched" && repository.operation.Stage != "rollback_pending") ||
		repository.root != newRoot {
		return errors.New("move cannot roll back from its current state")
	}
	repository.root = oldRoot
	repository.operation.Stage = "rolled_back"
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

func (moveWorkerSettings) SetupCompleted(context.Context) (bool, error) { return false, nil }

type moveCommandRunner struct {
	failName string
}

func (runner moveCommandRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	name := filepath.Base(executable)
	if len(args) != 1 || args[0] != "-version" {
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

func TestMoveWorkerResumesAfterCleanupBeforeSuccessRecord(t *testing.T) {
	oldRoot, newRoot, operation, installation, _ := newMoveFixture(t, false, true)
	repository := &moveWorkerRepository{
		workerRepository: &workerRepository{operation: operation, installation: installation},
		root:             oldRoot,
		finishErr:        errors.New("stop after source cleanup before success transaction"),
	}
	operations := service.NewOperations(repository)
	platform := tools.Platform{GOOS: "linux", GOARCH: "amd64"}
	worker := jobs.NewMoveWorker(repository, operations, moveWorkerSettings{root: oldRoot}, platform, tools.NewLifecycle(moveCommandRunner{}))
	if err := worker.Work(context.Background(), operation); err == nil {
		t.Fatal("success transaction interruption was not reached")
	}
	if operation.State != "running" || operation.Stage != "switched" || repository.root != newRoot {
		t.Fatalf("interrupted move state=%s stage=%s root=%s", operation.State, operation.Stage, repository.root)
	}
	worker = jobs.NewMoveWorker(repository, operations, moveWorkerSettings{root: newRoot}, platform, tools.NewLifecycle(moveCommandRunner{}))
	if err := worker.Work(context.Background(), operation); err != nil {
		t.Fatalf("interrupted move did not finish: %v", err)
	}
	if operation.State != "succeeded" || repository.root != newRoot {
		t.Fatalf("resumed move state=%s root=%s", operation.State, repository.root)
	}
}

func TestMoveWorkerRestoresOldRootWhenPostSwitchCleanupFails(t *testing.T) {
	oldRoot, _, operation, installation, snapshot := newMoveFixture(t, false, true)
	repository := &moveWorkerRepository{
		workerRepository: &workerRepository{operation: operation, installation: installation},
		root:             oldRoot,
		afterCommit: func() {
			if err := os.WriteFile(snapshot.Files[1].SourcePath, []byte("changed during cleanup"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}
	worker := jobs.NewMoveWorker(repository, service.NewOperations(repository),
		moveWorkerSettings{root: oldRoot}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(moveCommandRunner{}))
	if err := worker.Work(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if repository.root != oldRoot || operation.State != "failed" {
		t.Fatalf("failed move left root=%s operation=%s", repository.root, operation.State)
	}
	for _, file := range snapshot.Files {
		if digest, err := tools.SHA256File(file.SourcePath); err != nil || digest != file.SHA256 {
			t.Errorf("old managed executable not restored: %s hash=%s err=%v", file.Executable, digest, err)
		}
	}
	if _, err := os.Lstat(snapshot.Files[0].TargetPath); !os.IsNotExist(err) {
		t.Fatalf("new target remains after rollback: %v", err)
	}
}

func TestMoveWorkerFinishesInterruptedRollback(t *testing.T) {
	oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, false, true)
	operation.State, operation.Stage = "running", "rolled_back"
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: oldRoot}
	worker := jobs.NewMoveWorker(repository, service.NewOperations(repository),
		moveWorkerSettings{root: oldRoot}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(moveCommandRunner{}))
	if err := worker.Work(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if repository.root != oldRoot || operation.State != "failed" {
		t.Fatalf("resumed rollback root=%s operation=%s", repository.root, operation.State)
	}
	if _, err := os.Lstat(filepath.Join(newRoot, ".staging", operation.ID.String())); !os.IsNotExist(err) {
		t.Fatalf("rollback staging remains: %v", err)
	}
	for _, file := range snapshot.Files {
		if digest, err := tools.SHA256File(file.SourcePath); err != nil || digest != file.SHA256 {
			t.Errorf("old managed executable changed during rollback recovery: %s hash=%s err=%v", file.Executable, digest, err)
		}
	}
}

func TestReconcilePartialMovePublicationRestoresConfirmedTargetOnRetry(t *testing.T) {
	oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, true, true)
	operation.State, operation.Stage = "running", "commit_targets"
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: oldRoot}
	staging, err := tools.EnsureOperationStaging(newRoot, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	publication := struct {
		OperationID uuid.UUID `json:"operation_id"`
		NewRoot     string    `json:"new_root"`
		Files       []struct {
			Target string `json:"target"`
			SHA256 string `json:"sha256"`
			Owned  bool   `json:"owned"`
		} `json:"files"`
	}{OperationID: operation.ID, NewRoot: newRoot}
	for _, file := range snapshot.Files {
		contents, err := os.ReadFile(file.SourcePath)
		if err != nil {
			t.Fatal(err)
		}
		payload := filepath.Join(staging, "payload", file.RelativePath, file.Executable)
		if err := os.MkdirAll(filepath.Dir(payload), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(payload, contents, 0o755); err != nil {
			t.Fatal(err)
		}
		digest, err := tools.SHA256File(payload)
		if err != nil {
			t.Fatal(err)
		}
		publication.Files = append(publication.Files, struct {
			Target string `json:"target"`
			SHA256 string `json:"sha256"`
			Owned  bool   `json:"owned"`
		}{Target: filepath.Clean(file.TargetPath), SHA256: digest})
	}
	journal, err := json.Marshal(publication)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "publication.json"), journal, 0o600); err != nil {
		t.Fatal(err)
	}
	conflict := snapshot.Files[0]
	backup := filepath.Join(staging, "target-backups", "0")
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(conflict.TargetPath, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(staging, "payload", conflict.RelativePath, conflict.Executable), conflict.TargetPath); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}

	operations := service.NewOperations(repository)
	if err := jobs.ReconcileInterruptedOperations(context.Background(), repository, operations,
		func(context.Context, *int64) (bool, error) { return false, nil }, moveWorkerSettings{root: oldRoot}); err != nil {
		t.Fatalf("reconcile partial move publication: %v", err)
	}
	if operation.State != "failed" || operation.Stage != "commit_targets" {
		t.Fatalf("reconciled move = %s/%s; want retryable failed/commit_targets", operation.State, operation.Stage)
	}
	if restoredBackup, err := os.ReadFile(backup); err != nil || !bytes.Equal(restoredBackup, original) {
		t.Fatalf("reconciliation lost confirmed-target backup: %q %v", restoredBackup, err)
	}

	retried, err := operations.Retry(context.Background(), operation.ID)
	if err != nil {
		t.Fatalf("retry partially published move: %v", err)
	}
	if retried.Stage != "retry:commit_targets" {
		t.Fatalf("retry stage = %s; want retry:commit_targets", retried.Stage)
	}
	worker := jobs.NewMoveWorker(repository, operations, moveWorkerSettings{root: oldRoot},
		tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(moveCommandRunner{}))
	if err := worker.Work(context.Background(), retried); err != nil {
		t.Fatalf("rollback partial publication on retry: %v", err)
	}
	if repository.root != oldRoot || retried.State != "failed" || retried.SafeError == nil ||
		*retried.SafeError != "The tools directory move failed. The current tools directory is unchanged." {
		t.Fatalf("partial publication rollback root=%s state=%s error=%v; want old root and truthful failure", repository.root, retried.State, retried.SafeError)
	}
	if restored, err := os.ReadFile(conflict.TargetPath); err != nil || !bytes.Equal(restored, original) {
		t.Fatalf("confirmed operator target was not restored: %q %v", restored, err)
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Fatalf("partial publication staging remains after rollback: %v", err)
	}
	if repository.installation.ID != installation.ID {
		t.Fatalf("rollback changed installation identity from %s to %s", installation.ID, repository.installation.ID)
	}
}

func TestReconcilePostSwitchMovePreservesStagingAndRetryRollsBack(t *testing.T) {
	oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, false, true)
	repository := &moveWorkerRepository{
		workerRepository: &workerRepository{operation: operation, installation: installation},
		root:             oldRoot,
		finishErr:        errors.New("simulated process interruption after root switch"),
	}
	operations := service.NewOperations(repository)
	platform := tools.Platform{GOOS: "linux", GOARCH: "amd64"}
	worker := jobs.NewMoveWorker(repository, operations, moveWorkerSettings{root: oldRoot}, platform, tools.NewLifecycle(moveCommandRunner{}))
	if err := worker.Work(context.Background(), operation); err == nil {
		t.Fatal("expected interruption after the atomic root switch")
	}
	if operation.State != "running" || operation.Stage != "switched" || repository.root != newRoot {
		t.Fatalf("post-switch interruption = %s/%s root=%s; want running/switched at new root", operation.State, operation.Stage, repository.root)
	}
	staging := filepath.Join(newRoot, ".staging", operation.ID.String())
	if _, err := os.Stat(filepath.Join(staging, "publication.json")); err != nil {
		t.Fatalf("post-switch recovery publication missing before reconciliation: %v", err)
	}

	if err := jobs.ReconcileInterruptedOperations(context.Background(), repository, operations,
		func(context.Context, *int64) (bool, error) { return false, nil }, moveWorkerSettings{root: newRoot}); err != nil {
		t.Fatalf("reconcile post-switch move: %v", err)
	}
	if operation.State != "failed" || operation.Stage != "switched" || operation.SafeError == nil || !strings.Contains(*operation.SafeError, "Retry") {
		t.Fatalf("reconciled move = %s/%s safe error %v; want retryable failed/switched", operation.State, operation.Stage, operation.SafeError)
	}
	if _, err := os.Stat(filepath.Join(staging, "publication.json")); err != nil {
		t.Fatalf("reconciliation discarded rollback publication: %v", err)
	}
	if repository.root != newRoot {
		t.Fatalf("reconciliation changed switched root to %q", repository.root)
	}

	retried, err := operations.Retry(context.Background(), operation.ID)
	if err != nil {
		t.Fatalf("retry post-switch move: %v", err)
	}
	if retried.State != "queued" || retried.Stage != "retry:switched" {
		t.Fatalf("retry stage/state = %s/%s; want queued/retry:switched", retried.State, retried.Stage)
	}
	worker = jobs.NewMoveWorker(repository, operations, moveWorkerSettings{root: newRoot}, platform, tools.NewLifecycle(moveCommandRunner{}))
	if err := worker.Work(context.Background(), retried); err != nil {
		t.Fatalf("rollback retry failed: %v", err)
	}
	if repository.root != oldRoot || retried.State != "failed" || retried.SafeError == nil ||
		*retried.SafeError != "The tools directory move failed. The current tools directory is unchanged." {
		t.Fatalf("rollback result root=%s operation=%s error=%v; want old root and truthful failed state", repository.root, retried.State, retried.SafeError)
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Fatalf("rollback staging remains: %v", err)
	}
	if repository.installation.ID != installation.ID {
		t.Fatalf("rollback changed installation identity from %s to %s", installation.ID, repository.installation.ID)
	}
	for _, file := range snapshot.Files {
		if digest, err := tools.SHA256File(file.SourcePath); err != nil || digest != file.SHA256 {
			t.Errorf("old managed executable not restored: %s hash=%s err=%v", file.Executable, digest, err)
		}
		if _, err := os.Lstat(file.TargetPath); !os.IsNotExist(err) {
			t.Errorf("new-root target survived rollback: %s error=%v", file.TargetPath, err)
		}
	}
}

func TestMoveWorkerResumesPendingRollbackBeforeFilesystemChanges(t *testing.T) {
	oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, false, true)
	operation.State, operation.Stage = "running", "rollback_pending"
	seedMovePublication(t, newRoot, operation.ID, snapshot, true)
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: newRoot}
	worker := jobs.NewMoveWorker(repository, service.NewOperations(repository),
		moveWorkerSettings{root: newRoot}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(moveCommandRunner{}))
	if err := worker.Work(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if repository.root != oldRoot || operation.State != "failed" {
		t.Fatalf("resumed rollback root=%s operation=%s", repository.root, operation.State)
	}
	for _, file := range snapshot.Files {
		if digest, err := tools.SHA256File(file.SourcePath); err != nil || digest != file.SHA256 {
			t.Errorf("old source changed during rollback recovery: %s hash=%s err=%v", file.Executable, digest, err)
		}
	}
}

func TestMoveWorkerRollbackPreservesByteIdenticalTargetAddedAfterCommitStage(t *testing.T) {
	oldRoot, _, operation, installation, snapshot := newMoveFixture(t, false, false)
	operation.State, operation.Stage = "running", "commit_targets"
	unknownTarget := snapshot.Files[0].TargetPath
	if err := os.MkdirAll(filepath.Dir(unknownTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	sourceBytes, err := os.ReadFile(snapshot.Files[0].SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unknownTarget, sourceBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	laterUnknownTarget := snapshot.Files[1].TargetPath
	if err := os.MkdirAll(filepath.Dir(laterUnknownTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(laterUnknownTarget, []byte("different unknown target"), 0o755); err != nil {
		t.Fatal(err)
	}
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: oldRoot}
	worker := jobs.NewMoveWorker(repository, service.NewOperations(repository),
		moveWorkerSettings{root: oldRoot}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(moveCommandRunner{}))

	if err := worker.Work(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(unknownTarget); err != nil || !bytes.Equal(content, sourceBytes) {
		t.Fatalf("byte-identical unknown target was not preserved: %q, %v", content, err)
	}
	if operation.State != "failed" || repository.root != oldRoot {
		t.Fatalf("failed publication result root=%s operation=%s", repository.root, operation.State)
	}
}

func TestInstallationWorkerRollsBackPublishedTargetAfterResumedVerificationFailure(t *testing.T) {
	oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, false, false)
	operation.State, operation.Stage = "running", "commit_targets"
	staging := seedMovePublication(t, newRoot, operation.ID, snapshot, true)
	first := snapshot.Files[0]
	if err := os.Link(filepath.Join(staging, "payload", first.RelativePath, first.Executable), first.TargetPath); err != nil {
		t.Fatal(err)
	}
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: oldRoot}
	operations := service.NewOperations(repository)
	platform := tools.Platform{GOOS: "linux", GOARCH: "amd64"}
	runtimeSettings := moveWorkerSettings{root: oldRoot}
	dispatcher := jobs.NewInstallationWorker(repository, operations, nil, runtimeSettings, platform, nil)
	dispatcher.SetMoveWorker(jobs.NewMoveWorker(repository, operations, runtimeSettings, platform,
		tools.NewLifecycle(moveCommandRunner{failName: "ffprobe"})))

	if err := dispatcher.Work(context.Background(), &river.Job[service.OperationJobArgs]{
		Args: service.OperationJobArgs{OperationID: operation.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if operation.State != "failed" || repository.root != oldRoot {
		t.Fatalf("resumed failure state=%s root=%s", operation.State, repository.root)
	}
	if _, err := os.Lstat(first.TargetPath); !os.IsNotExist(err) {
		t.Fatalf("operation-owned target survived failed resumed verification: %v", err)
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Fatalf("rollback staging remains: %v", err)
	}
}

func TestInstallationWorkerResumesRollbackAfterEachConfirmedOriginalRestore(t *testing.T) {
	oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, true, true)
	secondConflict := snapshot.Files[1].TargetPath
	if err := os.WriteFile(secondConflict, []byte("second confirmed unknown target"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot.ConfirmedConflicts = append(snapshot.ConfirmedConflicts, secondConflict)
	rawSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	operation.InputSnapshot = rawSnapshot
	repository := &moveWorkerRepository{
		workerRepository: &workerRepository{operation: operation, installation: installation},
		root:             oldRoot,
		rollbackErr:      errors.New("interrupt before old-root transaction"),
		afterCommit: func() {
			if err := os.WriteFile(snapshot.Files[1].SourcePath, []byte("changed during cleanup"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}
	operations := service.NewOperations(repository)
	platform := tools.Platform{GOOS: "linux", GOARCH: "amd64"}
	worker := jobs.NewMoveWorker(repository, operations, moveWorkerSettings{root: oldRoot}, platform, tools.NewLifecycle(moveCommandRunner{}))
	if err := worker.Work(context.Background(), operation); err == nil {
		t.Fatal("move unexpectedly committed after interruption at root rollback")
	}
	if repository.root != newRoot || operation.Stage != "rollback_pending" {
		t.Fatalf("interrupted rollback root=%s stage=%s", repository.root, operation.Stage)
	}
	for index, file := range snapshot.Files {
		backup := filepath.Join(newRoot, ".staging", operation.ID.String(), "target-backups", strconv.Itoa(index))
		target, targetErr := os.Lstat(file.TargetPath)
		original, backupErr := os.Lstat(backup)
		if targetErr != nil || backupErr != nil || !os.SameFile(target, original) {
			t.Fatalf("restored target %s does not retain backup ownership: target=%v backup=%v errors=%v/%v", file.Executable, target, original, targetErr, backupErr)
		}
	}
	dispatcher := jobs.NewInstallationWorker(repository, operations, nil, moveWorkerSettings{root: newRoot}, platform, nil)
	dispatcher.SetMoveWorker(jobs.NewMoveWorker(repository, operations, moveWorkerSettings{root: newRoot}, platform, tools.NewLifecycle(moveCommandRunner{})))
	if err := dispatcher.Work(context.Background(), &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: operation.ID}}); err != nil {
		t.Fatalf("dispatcher could not resume rollback after restored original: %v", err)
	}
	if repository.root != oldRoot || operation.State != "failed" {
		t.Fatalf("resumed rollback root=%s state=%s", repository.root, operation.State)
	}
	if content, err := os.ReadFile(snapshot.Files[0].TargetPath); err != nil || string(content) != "old unknown target" {
		t.Fatalf("confirmed original was not preserved: %q, %v", content, err)
	}
}

func TestInstallationWorkerRedeliveryCleansInterruptedOldSourceRestoreTemporary(t *testing.T) {
	for _, checkpoint := range []string{
		"original_journaled", "original_linked", "source_allocated", "partial_copy", "copy_complete", "ready_recorded",
		"source_removed", "source_published", "before_old_root_transaction", "rolled_back",
		"original_witness_cleaned", "witnesses_cleaned", "main_staging_cleaned",
	} {
		t.Run(checkpoint, func(t *testing.T) {
			// Given exact pre-switch preparation or post-switch publication images.
			oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, false, true)
			staging := seedMovePublication(t, newRoot, operation.ID, snapshot, true)
			var witnesses []string
			for index, file := range snapshot.Files {
				if err := os.Link(filepath.Join(staging, "payload", file.RelativePath, file.Executable), file.TargetPath); err != nil {
					t.Fatal(err)
				}
				record, err := jobs.LoadMoveRestoreRecordForTest(filepath.Join(staging, fmt.Sprintf("old-source-restore-%d.json", index)))
				if err != nil {
					t.Fatal(err)
				}
				original, backup := jobs.MoveRestorePathsForTest(file.SourcePath, record.Token)
				witnesses = append(witnesses, original, backup)
			}
			file := snapshot.Files[0]
			beforeSource, err := os.Lstat(file.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			sourceBytes, err := os.ReadFile(file.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(staging, "old-source-restore-0.json")
			record, err := jobs.LoadMoveRestoreRecordForTest(journal)
			if err != nil {
				t.Fatal(err)
			}
			original, backup := jobs.MoveRestorePathsForTest(file.SourcePath, record.Token)
			root := newRoot
			operation.State, operation.Stage = "running", "rollback_pending"
			preSwitch := false
			switch checkpoint {
			case "original_journaled", "original_linked", "source_allocated", "partial_copy", "copy_complete", "ready_recorded":
				preSwitch = true
				root, operation.Stage = oldRoot, "prepare_restore"
				if checkpoint != "ready_recorded" {
					record.Ready = false
				}
				switch checkpoint {
				case "original_journaled", "original_linked", "source_allocated":
					record.Identity = nil
					if err := os.Remove(backup); err != nil {
						t.Fatal(err)
					}
					if checkpoint == "original_journaled" {
						if err := os.Remove(original); err != nil {
							t.Fatal(err)
						}
					}
					if checkpoint == "source_allocated" {
						// O_EXCL allocates only the sibling witness, never SourcePath.
						candidate, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o700)
						if err != nil {
							t.Fatal(err)
						}
						if err := candidate.Close(); err != nil {
							t.Fatal(err)
						}
					}
				case "partial_copy":
					if err := os.WriteFile(backup, sourceBytes[:1], 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if err := jobs.SaveMoveRestoreRecordForTest(journal, record); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Remove(file.SourcePath); err != nil {
					t.Fatal(err)
				}
				if checkpoint != "source_removed" {
					if err := os.Link(backup, file.SourcePath); err != nil {
						t.Fatal(err)
					}
				}
			}
			if checkpoint == "before_old_root_transaction" || checkpoint == "rolled_back" || checkpoint == "original_witness_cleaned" || checkpoint == "witnesses_cleaned" || checkpoint == "main_staging_cleaned" {
				for _, file := range snapshot.Files {
					if err := os.Remove(file.TargetPath); err != nil {
						t.Fatal(err)
					}
				}
			}
			if checkpoint == "rolled_back" || checkpoint == "original_witness_cleaned" || checkpoint == "witnesses_cleaned" || checkpoint == "main_staging_cleaned" {
				root, operation.Stage = oldRoot, "rolled_back"
			}
			if checkpoint == "original_witness_cleaned" {
				if err := os.Remove(original); err != nil {
					t.Fatal(err)
				}
			}
			if checkpoint == "witnesses_cleaned" || checkpoint == "main_staging_cleaned" {
				for _, path := range witnesses {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
			}
			if checkpoint == "main_staging_cleaned" {
				if err := tools.CleanupOperationStaging(newRoot, operation.ID); err != nil {
					t.Fatal(err)
				}
			}
			unrelated := filepath.Join(filepath.Dir(file.SourcePath), ".melotrove-restore-unrelated")
			if err := os.WriteFile(unrelated, []byte("operator data"), 0o600); err != nil {
				t.Fatal(err)
			}
			var ambiguous os.FileInfo
			if checkpoint == "source_allocated" {
				ambiguous, err = os.Lstat(backup)
				if err != nil {
					t.Fatal(err)
				}
			}
			repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: root}

			// When the real dispatcher redelivers, then repeats the terminal delivery.
			if err := dispatchMove(t, repository, root); err != nil {
				t.Fatal(err)
			}
			if err := dispatchMove(t, repository, repository.root); err != nil {
				t.Fatal(err)
			}

			// Then no clean process-stop image is left running/rollback_pending.
			wantState, wantRoot := "failed", oldRoot
			if preSwitch && checkpoint != "source_allocated" {
				wantState, wantRoot = "succeeded", newRoot
			}
			if operation.State != wantState || repository.root != wantRoot {
				t.Fatalf("redelivery did not resolve: state=%s stage=%s root=%s", operation.State, operation.Stage, repository.root)
			}
			for _, file := range snapshot.Files {
				path := file.SourcePath
				if wantRoot == newRoot {
					path = file.TargetPath
				}
				if digest, err := tools.SHA256File(path); err != nil || digest != file.SHA256 {
					t.Fatalf("acting root executable invalid: %s %s %v", path, digest, err)
				}
			}
			if checkpoint == "source_allocated" {
				if after, err := os.Lstat(file.SourcePath); err != nil || !os.SameFile(beforeSource, after) {
					t.Fatalf("pre-switch allocation modified the active source: %v", err)
				}
				if after, err := os.Lstat(backup); err != nil || !os.SameFile(ambiguous, after) || after.Size() != 0 {
					t.Fatalf("unproven witness was adopted or deleted: %v", err)
				}
			}
			for _, path := range append(witnesses, staging) {
				if checkpoint == "source_allocated" && path == backup {
					continue
				}
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("owned artifact remains: %s %v", path, err)
				}
			}
			if got, err := os.ReadFile(unrelated); err != nil || string(got) != "operator data" {
				t.Fatalf("unrelated data changed: %q %v", got, err)
			}
		})
	}
}

func TestInstallationWorkerOldSourceStagingCollisionPreservesUnknownData(t *testing.T) {
	for _, collisionIndex := range []int{0, 1} {
		t.Run(fmt.Sprintf("source_%d", collisionIndex), func(t *testing.T) {
			// Given the two independent queued-move collision images from R5.
			// Source 0 needs restoration; source 1 is never restored.
			oldRoot, _, operation, installation, snapshot := newMoveFixture(t, false, true)
			collision := filepath.Join(filepath.Dir(snapshot.Files[collisionIndex].SourcePath),
				fmt.Sprintf(".melotrove-restore-%s-%d.staging", operation.ID, collisionIndex))
			if err := os.Mkdir(collision, 0o755); err != nil {
				t.Fatal(err)
			}
			unknown := filepath.Join(collision, "operator-data.txt")
			if err := os.WriteFile(unknown, []byte("not created by this operation"), 0o644); err != nil {
				t.Fatal(err)
			}
			repository := &moveWorkerRepository{
				workerRepository: &workerRepository{operation: operation, installation: installation},
				root:             oldRoot,
				afterCommit: func() {
					if err := os.WriteFile(snapshot.Files[0].SourcePath, []byte("changed during cleanup"), 0o755); err != nil {
						t.Fatal(err)
					}
				},
			}

			// When the production dispatcher runs the queued move and rollback.
			err := dispatchMove(t, repository, oldRoot)

			// Then neither a used nor an unused derived path grants ownership.
			got, readErr := os.ReadFile(unknown)
			if readErr != nil || string(got) != "not created by this operation" {
				t.Fatalf("unknown old-root data deleted: collisionIndex=%d neededRestore=%t workerError=%v state=%s oldRootRestored=%t readError=%v",
					collisionIndex, collisionIndex == 0, err, operation.State, repository.root == oldRoot, readErr)
			}
			if err != nil || operation.State != "failed" || repository.root != oldRoot {
				t.Fatalf("unrelated directory blocked prepared restoration: state=%s root=%s error=%v", operation.State, repository.root, err)
			}
		})
	}
}

func dispatchMove(t *testing.T, repository *moveWorkerRepository, root string) error {
	t.Helper()
	operations := service.NewOperations(repository)
	platform := tools.Platform{GOOS: "linux", GOARCH: "amd64"}
	runtimeSettings := moveWorkerSettings{root: root}
	dispatcher := jobs.NewInstallationWorker(repository, operations, nil, runtimeSettings, platform, nil)
	dispatcher.SetMoveWorker(jobs.NewMoveWorker(repository, operations, runtimeSettings, platform, tools.NewLifecycle(moveCommandRunner{})))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return dispatcher.Work(ctx, &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: repository.operation.ID}})
}

func TestInstallationWorkerPreparesSourceWitnessesBeforeSwitch(t *testing.T) {
	// Given a queued move whose root transaction observes the actual disk state.
	oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, false, true)
	initialSources := make([]os.FileInfo, len(snapshot.Files))
	for index, file := range snapshot.Files {
		info, err := os.Lstat(file.SourcePath)
		if err != nil {
			t.Fatal(err)
		}
		initialSources[index] = info
	}
	checked := false
	repository := &moveWorkerRepository{
		workerRepository: &workerRepository{operation: operation, installation: installation},
		root:             oldRoot,
		beforeCommit: func() error {
			checked = true
			for index, file := range snapshot.Files {
				journal := filepath.Join(newRoot, ".staging", operation.ID.String(), fmt.Sprintf("old-source-restore-%d.json", index))
				raw, err := os.ReadFile(journal)
				if err != nil {
					return err
				}
				var record struct {
					Token    uuid.UUID       `json:"token"`
					Source   json.RawMessage `json:"source"`
					Identity json.RawMessage `json:"identity"`
					Ready    bool            `json:"ready"`
				}
				if err := json.Unmarshal(raw, &record); err != nil {
					return err
				}
				if !record.Ready || len(record.Source) == 0 || len(record.Identity) == 0 {
					t.Errorf("root switch reached without durable source-local restoration proof for %d: %s", index, raw)
					return errors.New("source restoration proof is not ready")
				}
				sourceInfo, err := os.Lstat(file.SourcePath)
				if err != nil {
					return err
				}
				if !os.SameFile(initialSources[index], sourceInfo) {
					return errors.New("active source was replaced before root switch")
				}
				original, backup := jobs.MoveRestorePathsForTest(file.SourcePath, record.Token)
				originalInfo, err := os.Lstat(original)
				if err != nil || !os.SameFile(sourceInfo, originalInfo) {
					t.Errorf("original source has no live inode witness before switch: %v", err)
					return errors.New("original source witness is missing")
				}
				backupInfo, err := os.Lstat(backup)
				if err != nil || os.SameFile(sourceInfo, backupInfo) {
					t.Errorf("independent same-volume recovery copy missing before switch: %v", err)
					return errors.New("independent recovery copy is missing")
				}
				if err := tools.VerifySHA256(backup, file.SHA256); err != nil {
					return err
				}
			}
			return nil
		},
	}

	// When the real dispatcher processes the complete queued move.
	if err := dispatchMove(t, repository, oldRoot); err != nil {
		t.Fatal(err)
	}

	// Then no root switch was allowed to precede either durable witness.
	if !checked || operation.State != "succeeded" || repository.root != newRoot {
		t.Fatalf("prepared move did not finish: checked=%t state=%s root=%s", checked, operation.State, repository.root)
	}
}

func TestInstallationWorkerFailsBeforeSwitchForUnprovenWitness(t *testing.T) {
	for _, index := range []int{0, 1} {
		for _, witnessKind := range []string{"original", "copy"} {
			for _, collisionKind := range []string{"equal_bytes", "directory"} {
				t.Run(fmt.Sprintf("source_%d/%s/%s", index, witnessKind, collisionKind), func(t *testing.T) {
					oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, false, true)
					operation.State, operation.Stage = "running", "prepare_restore"
					staging := seedMovePublication(t, newRoot, operation.ID, snapshot, true)
					file := snapshot.Files[index]
					journal := filepath.Join(staging, fmt.Sprintf("old-source-restore-%d.json", index))
					record, err := jobs.LoadMoveRestoreRecordForTest(journal)
					if err != nil {
						t.Fatal(err)
					}
					original, backup := jobs.MoveRestorePathsForTest(file.SourcePath, record.Token)
					collision := original
					if witnessKind == "copy" {
						collision = backup
						record.Identity, record.Ready = nil, false
						if err := jobs.SaveMoveRestoreRecordForTest(journal, record); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Rename(collision, collision+".retained"); err != nil {
						t.Fatal(err)
					}
					want, err := os.ReadFile(file.SourcePath)
					if err != nil {
						t.Fatal(err)
					}
					unknown := collision
					if collisionKind == "directory" {
						if err := os.Mkdir(collision, 0o700); err != nil {
							t.Fatal(err)
						}
						unknown = filepath.Join(collision, "operator-data")
						want = []byte("operator data")
					}
					if err := os.WriteFile(unknown, want, 0o600); err != nil {
						t.Fatal(err)
					}
					before, err := os.Lstat(unknown)
					if err != nil {
						t.Fatal(err)
					}
					switched := false
					repository := &moveWorkerRepository{
						workerRepository: &workerRepository{operation: operation, installation: installation},
						root:             oldRoot,
						afterCommit:      func() { switched = true },
					}

					// A pre-switch allocation without identity must resolve, not wait.
					if err := dispatchMove(t, repository, oldRoot); err != nil {
						t.Fatal(err)
					}
					if err := dispatchMove(t, repository, oldRoot); err != nil {
						t.Fatal(err)
					}
					if switched || operation.State != "failed" || repository.root != oldRoot {
						t.Fatalf("unproven preparation did not terminate before switch: %t %s %s", switched, operation.State, repository.root)
					}
					for _, file := range snapshot.Files {
						if digest, err := tools.SHA256File(file.SourcePath); err != nil || digest != file.SHA256 {
							t.Fatalf("active old root was modified: %s %v", digest, err)
						}
					}

					// The persisted input of an explicit retry uses a fresh nonce;
					// the same operation can complete without clearing operator data.
					operation.State, operation.Stage = "queued", "queued"
					operation.StartedAt, operation.FinishedAt, operation.SafeError = nil, nil, nil
					if err := dispatchMove(t, repository, oldRoot); err != nil {
						t.Fatal(err)
					}
					if operation.State != "succeeded" || repository.root != newRoot {
						t.Fatalf("retry was blocked by abandoned unproven names: %s %s", operation.State, repository.root)
					}
					if got, err := os.ReadFile(unknown); err != nil || !bytes.Equal(got, want) {
						t.Fatalf("operator contents changed: %q %v", got, err)
					}
					if after, err := os.Lstat(unknown); err != nil || !os.SameFile(before, after) {
						t.Fatalf("operator inode changed: %v", err)
					}
				})
			}
		}
	}
}

func TestInstallationWorkerPreservesIndependentSourceAfterRootSwitch(t *testing.T) {
	for _, index := range []int{0, 1} {
		t.Run(fmt.Sprintf("source_%d", index), func(t *testing.T) {
			// Given an independent operator inode replacing a source after switch.
			oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, false, true)
			source := snapshot.Files[index].SourcePath
			alias := filepath.Join(newRoot, "operator-alias")
			var unknownInfo os.FileInfo
			repository := &moveWorkerRepository{
				workerRepository: &workerRepository{operation: operation, installation: installation},
				root:             oldRoot,
				afterCommit: func() {
					if err := os.Rename(source, source+".original"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(alias, []byte("operator replacement"), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Link(alias, source); err != nil {
						t.Fatal(err)
					}
					var err error
					unknownInfo, err = os.Lstat(source)
					if err != nil {
						t.Fatal(err)
					}
				},
			}

			// When the production dispatcher reaches post-switch rollback.
			workerErr := dispatchMove(t, repository, oldRoot)

			// Then source and alias stay the same inode and rollback stays pending.
			for _, path := range []string{source, alias} {
				info, err := os.Lstat(path)
				if err != nil || !os.SameFile(unknownInfo, info) {
					t.Errorf("operator source inode was replaced at %s: %v", path, err)
				}
				if got, err := os.ReadFile(path); err != nil || string(got) != "operator replacement" {
					t.Errorf("operator source content changed at %s: %q, %v", path, got, err)
				}
			}
			if workerErr == nil || operation.State != "running" || operation.Stage != "rollback_pending" || repository.root != newRoot {
				t.Fatalf("unknown source was adopted: state=%s stage=%s root=%s error=%v", operation.State, operation.Stage, repository.root, workerErr)
			}
		})
	}
}

func TestInstallationWorkerPreservesSourceWhenOwnershipEvidenceIsMissing(t *testing.T) {
	for _, test := range []struct {
		content string
		intent  bool
	}{
		{"", false},
		{"", true},
		{"operator file after allocation crash", false},
		{"operator file after allocation crash", true},
		{"ffmpeg version 8.0", false},
		{"ffmpeg version 8.0", true},
	} {
		content, intent := test.content, test.intent
		t.Run(fmt.Sprintf("bytes_%d_intent_%t", len(content), intent), func(t *testing.T) {
			// Given missing/corrupted evidence and an independent operator source.
			_, newRoot, operation, installation, snapshot := newMoveFixture(t, false, true)
			operation.State, operation.Stage = "running", "rollback_pending"
			staging := seedMovePublication(t, newRoot, operation.ID, snapshot, true)
			journal := filepath.Join(staging, "old-source-restore-0.json")
			if err := os.Remove(journal); err != nil {
				t.Fatal(err)
			}
			intentBytes := []byte(`{"allocating":true,"linked":false}`)
			if intent {
				if err := os.WriteFile(journal, intentBytes, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			source := snapshot.Files[0].SourcePath
			if err := os.Rename(source, source+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(source)
			if err != nil {
				t.Fatal(err)
			}
			repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: newRoot}

			// When redelivery cannot distinguish an old allocation from an operator.
			for delivery := range 2 {
				workerErr := dispatchMove(t, repository, newRoot)
				if workerErr == nil || operation.State != "running" || operation.Stage != "rollback_pending" || repository.root != newRoot {
					t.Fatalf("delivery %d adopted unjournaled source: state=%s stage=%s root=%s error=%v", delivery, operation.State, operation.Stage, repository.root, workerErr)
				}
			}

			// Then no journal or pathname can retroactively claim this inode.
			after, err := os.Lstat(source)
			if err != nil || !os.SameFile(before, after) {
				t.Errorf("unjournaled source inode was replaced: %v", err)
			}
			if got, err := os.ReadFile(source); err != nil || string(got) != content {
				t.Errorf("unjournaled source bytes changed: %q, %v", got, err)
			}
			if intent {
				if got, err := os.ReadFile(journal); err != nil || !bytes.Equal(got, intentBytes) {
					t.Errorf("allocation intent adopted an unknown inode: %q, %v", got, err)
				}
			} else if _, err := os.Lstat(journal); !os.IsNotExist(err) {
				t.Errorf("unknown allocation was journaled as owned: %v", err)
			}
		})
	}
}

func TestInstallationWorkerResumesSourceCleanupAfterEachUnlink(t *testing.T) {
	for _, removed := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("after_%d_unlinks", removed), func(t *testing.T) {
			// Given a real switched image with immutable ownership witnesses.
			_, newRoot, operation, installation, snapshot := newMoveFixture(t, false, true)
			operation.State, operation.Stage = "running", "switched"
			staging := seedMovePublication(t, newRoot, operation.ID, snapshot, true)
			var witnesses []string
			for index, file := range snapshot.Files {
				if err := os.Link(filepath.Join(staging, "payload", file.RelativePath, file.Executable), file.TargetPath); err != nil {
					t.Fatal(err)
				}
				record, err := jobs.LoadMoveRestoreRecordForTest(filepath.Join(staging, fmt.Sprintf("old-source-restore-%d.json", index)))
				if err != nil {
					t.Fatal(err)
				}
				original, backup := jobs.MoveRestorePathsForTest(file.SourcePath, record.Token)
				witnesses = append(witnesses, original, backup)
				if index < removed {
					if err := os.Remove(file.SourcePath); err != nil {
						t.Fatal(err)
					}
				}
			}
			repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: newRoot}

			// When the dispatcher redelivers across each source unlink seam.
			if err := dispatchMove(t, repository, newRoot); err != nil {
				t.Fatal(err)
			}
			if err := dispatchMove(t, repository, newRoot); err != nil {
				t.Fatal(err)
			}

			// Then no pre-unlink revocation can strand an otherwise valid move.
			if operation.State != "succeeded" || repository.root != newRoot {
				t.Fatalf("cleanup did not terminate: %s %s", operation.State, repository.root)
			}
			for _, file := range snapshot.Files {
				if _, err := os.Lstat(file.SourcePath); !os.IsNotExist(err) {
					t.Fatalf("old source remains: %v", err)
				}
				if digest, err := tools.SHA256File(file.TargetPath); err != nil || digest != file.SHA256 {
					t.Fatalf("new root executable invalid: %s %v", digest, err)
				}
			}
			for _, path := range append(witnesses, staging) {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("owned witness remains: %s %v", path, err)
				}
			}
		})
	}
}

func TestInstallationWorkerPreservesAliasOfPreparedRestoreWitness(t *testing.T) {
	// Given an operator alias of a prepared copy after source publication.
	oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, false, true)
	operation.State, operation.Stage = "running", "rollback_pending"
	staging := seedMovePublication(t, newRoot, operation.ID, snapshot, true)
	file := snapshot.Files[0]
	record, err := jobs.LoadMoveRestoreRecordForTest(filepath.Join(staging, "old-source-restore-0.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, backup := jobs.MoveRestorePathsForTest(file.SourcePath, record.Token)
	if err := os.Remove(file.SourcePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(backup, file.SourcePath); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(file.SourcePath), "operator-alias")
	if err := os.Link(backup, alias); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alias, []byte("operator alias data"), 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(alias)
	if err != nil {
		t.Fatal(err)
	}
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: newRoot}

	// When recovery selects the intact original witness, without copying/truncating.
	if err := dispatchMove(t, repository, newRoot); err != nil {
		t.Fatal(err)
	}

	// Then the source recovers, but the operator alias is the same unmodified inode.
	if got, err := os.ReadFile(alias); err != nil || string(got) != "operator alias data" {
		t.Fatalf("operator alias changed: %q %v", got, err)
	}
	if after, err := os.Lstat(alias); err != nil || !os.SameFile(before, after) {
		t.Fatalf("operator alias inode changed: %v", err)
	}
	if digest, err := tools.SHA256File(file.SourcePath); err != nil || digest != file.SHA256 {
		t.Fatalf("source not restored: %s %v", digest, err)
	}
	if operation.State != "failed" || repository.root != oldRoot {
		t.Fatalf("recovery did not terminate at old root: %s %s", operation.State, repository.root)
	}
}

func TestInstallationWorkerFailsSafelyForAliasedPartialWitness(t *testing.T) {
	// Given a pre-switch copy that became aliased before its interrupted write.
	oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, false, true)
	operation.State, operation.Stage = "running", "prepare_restore"
	staging := seedMovePublication(t, newRoot, operation.ID, snapshot, true)
	journal := filepath.Join(staging, "old-source-restore-0.json")
	record, err := jobs.LoadMoveRestoreRecordForTest(journal)
	if err != nil {
		t.Fatal(err)
	}
	record.Ready = false
	if err := jobs.SaveMoveRestoreRecordForTest(journal, record); err != nil {
		t.Fatal(err)
	}
	_, backup := jobs.MoveRestorePathsForTest(snapshot.Files[0].SourcePath, record.Token)
	alias := filepath.Join(oldRoot, "operator-alias")
	if err := os.Link(backup, alias); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alias, []byte("operator partial-copy data"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(alias)
	if err != nil {
		t.Fatal(err)
	}
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: oldRoot}

	// When re-copying the witness would overwrite its operator alias.
	if err := dispatchMove(t, repository, oldRoot); err != nil {
		t.Fatal(err)
	}

	// Then preparation fails terminally without affecting the active old root.
	if operation.State != "failed" || repository.root != oldRoot {
		t.Fatalf("aliased preparation did not resolve: %s %s", operation.State, repository.root)
	}
	if got, err := os.ReadFile(alias); err != nil || string(got) != "operator partial-copy data" {
		t.Fatalf("operator alias overwritten: %q %v", got, err)
	}
	if after, err := os.Lstat(alias); err != nil || !os.SameFile(before, after) {
		t.Fatalf("operator alias replaced: %v", err)
	}
	for _, file := range snapshot.Files {
		if digest, err := tools.SHA256File(file.SourcePath); err != nil || digest != file.SHA256 {
			t.Fatalf("old root source changed: %s %v", digest, err)
		}
	}
}

func TestInstallationWorkerRejectsUnknownOldSourceRestoreEvidence(t *testing.T) {
	for _, checkpoint := range []string{"restore_pending", "source_published", "rolled_back"} {
		for _, spoof := range []string{"equal_bytes", "directory_marker"} {
			t.Run(checkpoint+"/"+spoof, func(t *testing.T) {
				// Given an independent replacement at the actual recorded witness path.
				oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, false, true)
				operation.State, operation.Stage = "running", "rollback_pending"
				staging := seedMovePublication(t, newRoot, operation.ID, snapshot, true)
				file := snapshot.Files[0]
				sourceBytes, err := os.ReadFile(file.SourcePath)
				if err != nil {
					t.Fatal(err)
				}
				journal := filepath.Join(staging, "old-source-restore-0.json")
				record, err := jobs.LoadMoveRestoreRecordForTest(journal)
				if err != nil {
					t.Fatal(err)
				}
				_, backup := jobs.MoveRestorePathsForTest(file.SourcePath, record.Token)
				if checkpoint == "restore_pending" {
					// Neither the original nor its link still has the expected bytes.
					if err := os.WriteFile(file.SourcePath, []byte("changed before recovery"), 0o755); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Remove(file.SourcePath); err != nil {
						t.Fatal(err)
					}
					if err := os.Link(backup, file.SourcePath); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Rename(backup, backup+".retained"); err != nil {
					t.Fatal(err)
				}
				unknown, want := backup, sourceBytes
				if spoof == "directory_marker" {
					if err := os.Mkdir(backup, 0o700); err != nil {
						t.Fatal(err)
					}
					unknown = filepath.Join(backup, "owner.json")
					want, err = os.ReadFile(journal)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(unknown, want, 0o600); err != nil {
					t.Fatal(err)
				}
				before, err := os.Lstat(unknown)
				if err != nil {
					t.Fatal(err)
				}
				root := newRoot
				if checkpoint == "rolled_back" {
					operation.Stage, root = "rolled_back", oldRoot
				}
				repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: root}

				// When redelivery restores or cleans only proven witnesses.
				err = dispatchMove(t, repository, root)

				// Then unknown bytes/markers never acquire ownership. A valid old
				// source permits terminal rollback; actual operator corruption does not.
				if checkpoint == "restore_pending" {
					if err == nil || operation.Stage != "rollback_pending" || repository.root != newRoot {
						t.Fatalf("unknown witness adopted: %v", err)
					}
					if _, err := os.Lstat(journal); err != nil {
						t.Fatalf("recovery evidence lost: %v", err)
					}
				} else if err != nil || operation.State != "failed" || repository.root != oldRoot {
					t.Fatalf("safe old-root rollback did not terminate: %s %s %v", operation.State, repository.root, err)
				}
				if got, err := os.ReadFile(unknown); err != nil || !bytes.Equal(got, want) {
					t.Fatalf("unknown witness data changed: %q %v", got, err)
				}
				if after, err := os.Lstat(unknown); err != nil || !os.SameFile(before, after) {
					t.Fatalf("unknown witness inode changed: %v", err)
				}
			})
		}
	}
}

func TestInstallationWorkerPreservesReusedWitnessInodeNumber(t *testing.T) {
	// Given an old cleanup record whose device/inode number now names another
	// incarnation. Seed the reused numbers directly, without timing inode reuse.
	oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, false, false)
	operation.State, operation.Stage = "running", "rolled_back"
	staging := seedMovePublication(t, newRoot, operation.ID, snapshot, true)
	journal := filepath.Join(staging, "old-source-restore-0.json")
	record, err := jobs.LoadMoveRestoreRecordForTest(journal)
	if err != nil {
		t.Fatal(err)
	}
	_, backup := jobs.MoveRestorePathsForTest(snapshot.Files[0].SourcePath, record.Token)
	if err := os.Remove(backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("operator replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := jobs.InspectMoveRestoreIdentityForTest(backup)
	if err != nil {
		t.Fatal(err)
	}
	identity.Created--
	record.Identity = &identity
	if err := jobs.SaveMoveRestoreRecordForTest(journal, record); err != nil {
		t.Fatal(err)
	}
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: oldRoot}

	// When cleanup resumes after an unlink and a reused native inode number.
	if err := dispatchMove(t, repository, oldRoot); err != nil {
		t.Fatal(err)
	}

	// Then creation identity, not the stale number, prevents deleting operator data.
	if got, err := os.ReadFile(backup); err != nil || string(got) != "operator replacement" {
		t.Fatalf("reused inode was deleted: %q %v", got, err)
	}
	if operation.State != "failed" || repository.root != oldRoot {
		t.Fatalf("cleanup did not terminate: %s %s", operation.State, repository.root)
	}
}

func TestInstallationWorkerRestorationPreservesOperatorHardlinkAlias(t *testing.T) {
	// Given an operator hardlink to a source that changes after the root switch.
	oldRoot, _, operation, installation, snapshot := newMoveFixture(t, false, true)
	source := snapshot.Files[0].SourcePath
	alias := filepath.Join(filepath.Dir(source), "operator-alias")
	if err := os.Link(source, alias); err != nil {
		t.Fatal(err)
	}
	repository := &moveWorkerRepository{
		workerRepository: &workerRepository{operation: operation, installation: installation},
		root:             oldRoot,
		afterCommit: func() {
			if err := os.WriteFile(source, []byte("changed during cleanup"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}

	// When rollback restores the managed pathname from the retained payload.
	if err := dispatchMove(t, repository, oldRoot); err != nil {
		t.Fatal(err)
	}

	// Then its fresh allocation must not overwrite the operator's alias.
	if got, err := os.ReadFile(alias); err != nil || string(got) != "changed during cleanup" {
		t.Fatalf("operator hardlink alias changed: %q, %v", got, err)
	}
	if digest, err := tools.SHA256File(source); err != nil || digest != snapshot.Files[0].SHA256 {
		t.Fatalf("managed source was not restored: %s, %v", digest, err)
	}
	if operation.State != "failed" || repository.root != oldRoot {
		t.Fatalf("rollback state=%s root=%s", operation.State, repository.root)
	}
}

func TestInstallationWorkerPreservesByteEqualUnknownConfirmedRestoreTemporary(t *testing.T) {
	// Given a confirmed backup and an independent, byte-equal restore temporary.
	_, newRoot, operation, installation, snapshot := newMoveFixture(t, true, false)
	operation.State, operation.Stage = "running", "rollback_pending"
	staging := seedMovePublication(t, newRoot, operation.ID, snapshot, true)
	target := snapshot.Files[0].TargetPath
	backup := filepath.Join(staging, "target-backups", "0")
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(target, backup); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(filepath.Dir(target), fmt.Sprintf(".melotrove-restore-%s-0", operation.ID))
	if err := os.WriteFile(temporary, original, 0o644); err != nil {
		t.Fatal(err)
	}
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: newRoot}

	// When the actual dispatcher reaches the existing confirmed-restore guard.
	err = dispatchMove(t, repository, newRoot)

	// Then R4 must still reject content equality as ownership.
	if err == nil || operation.Stage != "rollback_pending" || repository.root != newRoot {
		t.Fatalf("unknown confirmed temporary accepted: stage=%s root=%s error=%v", operation.Stage, repository.root, err)
	}
	if got, err := os.ReadFile(temporary); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("unknown confirmed temporary changed: %q, %v", got, err)
	}
	if _, err := os.Lstat(staging); err != nil {
		t.Fatalf("confirmed recovery staging lost: %v", err)
	}
}

func TestInstallationWorkerRedeliveryCleansInterruptedRestoreTemporary(t *testing.T) {
	for _, restoredCount := range []int{1, 2} {
		t.Run(fmt.Sprintf("after_%d_confirmed_originals", restoredCount), func(t *testing.T) {
			oldRoot, newRoot, operation, installation, snapshot := newMoveFixture(t, true, false)
			secondConflict := snapshot.Files[1].TargetPath
			if err := os.WriteFile(secondConflict, []byte("second confirmed original"), 0o644); err != nil {
				t.Fatal(err)
			}
			snapshot.ConfirmedConflicts = append(snapshot.ConfirmedConflicts, secondConflict)
			raw, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			operation.InputSnapshot = raw
			operation.State, operation.Stage = "running", "rollback_pending"
			staging := seedMovePublication(t, newRoot, operation.ID, snapshot, true)
			backupDir := filepath.Join(staging, "target-backups")
			if err := os.MkdirAll(backupDir, 0o700); err != nil {
				t.Fatal(err)
			}
			backups := make([]string, len(snapshot.Files))
			for index, file := range snapshot.Files {
				backups[index] = filepath.Join(backupDir, strconv.Itoa(index))
				if err := os.Rename(file.TargetPath, backups[index]); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(filepath.Join(staging, "payload", file.RelativePath, file.Executable), file.TargetPath); err != nil {
					t.Fatal(err)
				}
			}

			// Simulate a process stop immediately after Link and before Rename
			// during reverse-order restore. Production owns this name via staging.
			index := len(snapshot.Files) - 1
			target := snapshot.Files[index].TargetPath
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			temporary := filepath.Join(filepath.Dir(target),
				fmt.Sprintf(".melotrove-restore-%s-%d", operation.ID, index))
			if err := os.Link(backups[index], temporary); err != nil {
				t.Fatal(err)
			}
			temporaryInfo, err := os.Lstat(temporary)
			if err != nil {
				t.Fatal(err)
			}
			backupInfo, err := os.Lstat(backups[index])
			if err != nil || !os.SameFile(temporaryInfo, backupInfo) {
				t.Fatalf("interrupted restore temporary is not the backup inode: temporary=%v backup=%v error=%v", temporaryInfo, backupInfo, err)
			}
			unrelatedTemporary := filepath.Join(filepath.Dir(target), ".melotrove-restore-unrelated")
			if err := os.WriteFile(unrelatedTemporary, []byte("leave this file alone"), 0o600); err != nil {
				t.Fatal(err)
			}
			for n := 1; n < restoredCount; n++ {
				index = len(snapshot.Files) - 1 - n
				target = snapshot.Files[index].TargetPath
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(backups[index], target); err != nil {
					t.Fatal(err)
				}
			}

			repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: newRoot}
			operations := service.NewOperations(repository)
			platform := tools.Platform{GOOS: "linux", GOARCH: "amd64"}
			runtimeSettings := moveWorkerSettings{root: newRoot}
			dispatcher := jobs.NewInstallationWorker(repository, operations, nil, runtimeSettings, platform, nil)
			dispatcher.SetMoveWorker(jobs.NewMoveWorker(repository, operations, runtimeSettings, platform, tools.NewLifecycle(moveCommandRunner{})))
			if err := dispatcher.Work(context.Background(), &river.Job[service.OperationJobArgs]{
				Args: service.OperationJobArgs{OperationID: operation.ID},
			}); err != nil {
				t.Fatal(err)
			}
			if operation.State != "failed" || repository.root != oldRoot {
				t.Fatalf("rollback state=%s root=%s", operation.State, repository.root)
			}
			for index, want := range []string{"old unknown target", "second confirmed original"} {
				got, err := os.ReadFile(snapshot.Files[index].TargetPath)
				if err != nil || string(got) != want {
					t.Errorf("confirmed original %d: %q, %v", index, got, err)
				}
			}
			if _, err := os.Lstat(staging); !os.IsNotExist(err) {
				t.Fatalf("operation staging remains after terminal rollback: %v", err)
			}
			if _, err := os.Lstat(temporary); !os.IsNotExist(err) {
				t.Errorf("owned restore temporary survived terminal rollback: %s: %v", temporary, err)
			}
			if content, err := os.ReadFile(unrelatedTemporary); err != nil || string(content) != "leave this file alone" {
				t.Errorf("unrelated similarly prefixed file was changed: %q, %v", content, err)
			}
		})
	}
}

func TestInstallationWorkerRollbackDoesNotAdoptByteIdenticalUnknownConflictTarget(t *testing.T) {
	_, newRoot, operation, installation, snapshot := newMoveFixture(t, true, false)
	operation.State, operation.Stage = "running", "rollback_pending"
	staging := seedMovePublication(t, newRoot, operation.ID, snapshot, true)
	target := snapshot.Files[0].TargetPath
	backup := filepath.Join(staging, "target-backups", "0")
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(target, backup); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range snapshot.Files {
		if err := os.Link(filepath.Join(staging, "payload", file.RelativePath, file.Executable), file.TargetPath); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: newRoot}
	platform := tools.Platform{GOOS: "linux", GOARCH: "amd64"}
	runtimeSettings := moveWorkerSettings{root: newRoot}
	dispatcher := jobs.NewInstallationWorker(repository, service.NewOperations(repository), nil, runtimeSettings, platform, nil)
	dispatcher.SetMoveWorker(jobs.NewMoveWorker(repository, service.NewOperations(repository), runtimeSettings, platform, tools.NewLifecycle(moveCommandRunner{})))

	err = dispatcher.Work(context.Background(), &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: operation.ID}})
	if err == nil || !strings.Contains(err.Error(), "unknown target blocks restoration") {
		t.Fatalf("byte-identical unknown target was adopted: %v", err)
	}
	if operation.State != "running" || repository.root != newRoot {
		t.Fatalf("unknown target failure changed operation/root: state=%s root=%s", operation.State, repository.root)
	}
	if content, err := os.ReadFile(target); err != nil || !bytes.Equal(content, original) {
		t.Fatalf("unknown target was overwritten: %q, %v", content, err)
	}
}

func seedMovePublication(t *testing.T, root string, operationID uuid.UUID, snapshot service.MoveSnapshot, owned bool) string {
	t.Helper()
	staging, err := tools.EnsureOperationStaging(root, operationID)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]jobsMovePublicationFile, len(snapshot.Files))
	for index, file := range snapshot.Files {
		if err := os.MkdirAll(filepath.Dir(file.TargetPath), 0o755); err != nil {
			t.Fatal(err)
		}
		payload := filepath.Join(staging, "payload", file.RelativePath, file.Executable)
		if err := os.MkdirAll(filepath.Dir(payload), 0o700); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(file.SourcePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(payload, data, 0o755); err != nil {
			t.Fatal(err)
		}
		files[index] = jobsMovePublicationFile{Target: filepath.Clean(file.TargetPath), SHA256: file.SHA256, Owned: owned}
	}
	raw, err := json.Marshal(struct {
		OperationID uuid.UUID                 `json:"operation_id"`
		NewRoot     string                    `json:"new_root"`
		Files       []jobsMovePublicationFile `json:"files"`
	}{OperationID: operationID, NewRoot: root, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "publication.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := jobs.RecordMoveSourcesForTest(snapshot, staging); err != nil {
		t.Fatal(err)
	}
	return staging
}

type jobsMovePublicationFile struct {
	Target string `json:"target"`
	SHA256 string `json:"sha256"`
	Owned  bool   `json:"owned"`
}

func TestInstallationWorkerRedeliveryCleansSucceededMoveStaging(t *testing.T) {
	_, newRoot, operation, installation, _ := newMoveFixture(t, false, false)
	operation.State, operation.Stage = "succeeded", "succeeded"
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: newRoot}
	staging, err := tools.EnsureOperationStaging(newRoot, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "interrupted-cleanup"), []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	moveWorker := jobs.NewMoveWorker(repository, service.NewOperations(repository),
		moveWorkerSettings{root: newRoot}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(moveCommandRunner{}))
	installationWorker := jobs.NewInstallationWorker(repository, service.NewOperations(repository), nil,
		moveWorkerSettings{root: newRoot}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, nil)
	installationWorker.SetMoveWorker(moveWorker)

	if err := installationWorker.Work(context.Background(), &river.Job[service.OperationJobArgs]{
		Args: service.OperationJobArgs{OperationID: operation.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Fatalf("succeeded move staging remains after dispatcher redelivery: %v", err)
	}
}

func TestInstallationWorkerSurfacesAndRecoversSucceededMoveCleanupFailure(t *testing.T) {
	_, newRoot, operation, installation, _ := newMoveFixture(t, false, false)
	operation.State, operation.Stage = "succeeded", "succeeded"
	repository := &moveWorkerRepository{workerRepository: &workerRepository{operation: operation, installation: installation}, root: newRoot}
	staging, err := tools.EnsureOperationStaging(newRoot, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(staging, "interrupted-cleanup")
	if err := os.WriteFile(marker, []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	moveWorker := jobs.NewMoveWorker(repository, service.NewOperations(repository),
		moveWorkerSettings{root: newRoot}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(moveCommandRunner{}))
	installationWorker := jobs.NewInstallationWorker(repository, service.NewOperations(repository), nil,
		moveWorkerSettings{root: newRoot}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, nil)
	installationWorker.SetMoveWorker(moveWorker)

	stagingRoot := filepath.Join(newRoot, ".staging")
	savedStagingRoot := filepath.Join(newRoot, ".staging-saved")
	if err := os.Rename(stagingRoot, savedStagingRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagingRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = installationWorker.Work(context.Background(), &river.Job[service.OperationJobArgs]{
		Args: service.OperationJobArgs{OperationID: operation.ID},
	})
	if err == nil {
		t.Fatal("dispatcher hid succeeded move staging cleanup failure")
	}
	if err := os.Remove(stagingRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(savedStagingRoot, stagingRoot); err != nil {
		t.Fatal(err)
	}

	if err := installationWorker.Work(context.Background(), &river.Job[service.OperationJobArgs]{
		Args: service.OperationJobArgs{OperationID: operation.ID},
	}); err != nil {
		t.Fatalf("dispatcher could not retry staging cleanup: %v", err)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("staging remained after successful cleanup retry: %v", err)
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
