package jobs_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type workerRepository struct {
	operation           *persistence.Operation
	installation        *persistence.ToolInstallation
	activated           bool
	readyError          error
	readyCommittedError error
}

func (repository *workerRepository) CreateOperation(context.Context, *persistence.Operation) error {
	return nil
}

func (repository *workerRepository) GetOperation(_ context.Context, id uuid.UUID) (*persistence.Operation, error) {
	if repository.operation == nil || repository.operation.ID != id {
		return nil, errors.New("missing operation")
	}
	return repository.operation, nil
}

func (repository *workerRepository) ListOperations(context.Context, ...string) ([]persistence.Operation, error) {
	if repository.operation == nil {
		return nil, nil
	}
	return []persistence.Operation{*repository.operation}, nil
}

func (repository *workerRepository) UpdateOperation(_ context.Context, operation *persistence.Operation) error {
	repository.operation = operation
	return nil
}

func (repository *workerRepository) TransitionOperation(_ context.Context, id uuid.UUID, transition func(*persistence.Operation) error) error {
	operation, err := repository.GetOperation(context.Background(), id)
	if err != nil {
		return err
	}
	return transition(operation)
}

func (*workerRepository) DismissOperation(context.Context, uuid.UUID) error      { return nil }
func (*workerRepository) DeleteSucceededBefore(context.Context, time.Time) error { return nil }

func (repository *workerRepository) GetInstallation(_ context.Context, id uuid.UUID) (*persistence.ToolInstallation, error) {
	if repository.installation == nil || repository.installation.ID != id {
		return nil, errors.New("missing installation")
	}
	return repository.installation, nil
}

func (repository *workerRepository) MarkInstallationReady(_ context.Context, id uuid.UUID, versions json.RawMessage, verifiedAt time.Time) error {
	if repository.readyError != nil {
		return repository.readyError
	}
	installation, err := repository.GetInstallation(context.Background(), id)
	if err != nil {
		return err
	}
	installation.State = "ready"
	installation.ExecutableVersions = versions
	installation.VerifiedAt = &verifiedAt
	return repository.readyCommittedError
}

func (repository *workerRepository) MarkInstallationFailed(_ context.Context, id uuid.UUID) error {
	installation, err := repository.GetInstallation(context.Background(), id)
	if err != nil {
		return err
	}
	installation.State = "failed"
	return nil
}

func (repository *workerRepository) ActivateInstallationDuringSetup(_ context.Context, id uuid.UUID, _, _, _, _ string) (bool, error) {
	installation, err := repository.GetInstallation(context.Background(), id)
	if err != nil {
		return false, err
	}
	if installation.State != "ready" {
		return false, errors.New("installation not ready")
	}
	repository.activated = true
	return true, nil
}

type workerCatalog struct {
	release  tools.Release
	archive  []byte
	checksum string
}

func (catalog workerCatalog) Resolve(context.Context, tools.PackageKind, tools.Platform, string) (tools.Release, error) {
	return catalog.release, nil
}

func (catalog workerCatalog) Download(_ context.Context, _ tools.PackageKind, _ tools.Platform, _, name string, destination io.Writer, progress func(int64)) (tools.Artifact, int64, error) {
	artifact, ok := findWorkerArtifact(catalog.release, name)
	if !ok {
		return tools.Artifact{}, 0, errors.New("artifact missing")
	}
	written, err := destination.Write(catalog.archive)
	if err != nil {
		return tools.Artifact{}, int64(written), err
	}
	if progress != nil {
		progress(int64(written))
	}
	return artifact, int64(written), nil
}

func (catalog workerCatalog) Checksum(context.Context, tools.PackageKind, tools.Platform, string, string) (string, error) {
	return catalog.checksum, nil
}

func findWorkerArtifact(release tools.Release, name string) (tools.Artifact, bool) {
	for _, artifact := range release.Artifacts {
		if artifact.Name == name {
			return artifact, true
		}
	}
	return tools.Artifact{}, false
}

type workerSettings struct {
	root      string
	completed bool
}

func (settings workerSettings) GetToolsDirectory(context.Context) (string, bool, error) {
	return settings.root, true, nil
}

func (settings workerSettings) SetupCompleted(context.Context) (bool, error) {
	return settings.completed, nil
}

type rootReadFlipSettings struct {
	root      string
	afterRead func()
}

func (settings *rootReadFlipSettings) GetToolsDirectory(context.Context) (string, bool, error) {
	root := settings.root
	if settings.afterRead != nil {
		settings.afterRead()
	}
	return root, true, nil
}

func (settings *rootReadFlipSettings) SetupCompleted(context.Context) (bool, error) { return true, nil }

type workerCommandRunner struct{}

func (workerCommandRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	if len(args) != 1 || args[0] != "-version" {
		return nil, fmt.Errorf("unexpected command arguments")
	}
	if !strings.Contains(filepath.Base(executable), "fpcalc") {
		return nil, fmt.Errorf("unexpected executable")
	}
	return []byte("fpcalc version 1.6.1"), nil
}

func TestInstallationWorkerDownloadsVerifiesMaterializesAndActivatesDuringSetup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	operationID := uuid.New()
	installationID := uuid.New()
	root := t.TempDir()
	release := tools.Release{Identity: "v1.6.1", Artifacts: []tools.Artifact{{Name: "fpcalc.zip", URL: "https://github.com/acoustid/fpcalc.zip"}}}
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		TargetIdentity: "fpcalc:chromaprint:v1.6.1:linux:amd64", SchemaVersion: 2, ToolsRoot: root,
		PackageKind: tools.PackageFPCalc, SourceName: "chromaprint", ReleaseIdentity: "v1.6.1",
		ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{ID: operationID, Kind: "install", State: "queued", Stage: "queued", InputSnapshot: snapshot, TargetInstallationID: &installationID}
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: release.Identity, RelativePath: "fpcalc/v1.6.1", State: "preparing",
	}
	repository := &workerRepository{operation: operation, installation: installation}
	operations := service.NewOperations(repository)
	worker := jobs.NewInstallationWorker(repository, operations, workerCatalog{release: release, archive: zipWithExecutable(t, "fpcalc", []byte("fpcalc"))},
		workerSettings{root: root}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(workerCommandRunner{}))
	job := &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: operationID}}

	if err := worker.Work(ctx, job); err != nil {
		t.Fatal(err)
	}
	if operation.State != "succeeded" || installation.State != "ready" || !repository.activated {
		t.Fatalf("operation=%s installation=%s activated=%v", operation.State, installation.State, repository.activated)
	}
	if operation.BytesCompleted == 0 || operation.Stage != "succeeded" {
		t.Fatalf("operation progress/stage = %d/%s", operation.BytesCompleted, operation.Stage)
	}
	if _, err := os.Stat(filepath.Join(root, installation.RelativePath, "fpcalc")); err != nil {
		t.Fatalf("materialized executable missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, ".staging", operationID.String())); !os.IsNotExist(err) {
		t.Fatalf("operation staging remains: %v", err)
	}
}

func TestInstallationWorkerPinsWritesToSnapshotRootAcrossRootRead(t *testing.T) {
	t.Parallel()
	for _, changeBeforeRead := range []bool{true, false} {
		name := "root_changes_after_read"
		if changeBeforeRead {
			name = "root_changed_before_read"
		}
		t.Run(name, func(t *testing.T) {
			oldRoot, newRoot := t.TempDir(), t.TempDir()
			id, installationID := uuid.New(), uuid.New()
			raw, err := json.Marshal(service.InstallInputSnapshot{
				TargetIdentity: "fpcalc:chromaprint:v1.6.1:linux:amd64", SchemaVersion: 2, ToolsRoot: oldRoot,
				PackageKind: tools.PackageFPCalc, SourceName: "chromaprint", ReleaseIdentity: "v1.6.1",
				ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			operation := &persistence.Operation{ID: id, Kind: "install", State: "queued", Stage: "queued", InputSnapshot: raw, TargetInstallationID: &installationID}
			installation := &persistence.ToolInstallation{ID: installationID, PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64", SourceName: "chromaprint", ReleaseIdentity: "v1.6.1", RelativePath: "fpcalc/v1.6.1", State: "preparing"}
			repository := &workerRepository{operation: operation, installation: installation}
			runtime := &rootReadFlipSettings{root: oldRoot}
			if changeBeforeRead {
				runtime.root = newRoot
			} else {
				runtime.afterRead = func() { runtime.root = newRoot }
			}
			worker := jobs.NewInstallationWorker(repository, service.NewOperations(repository),
				workerCatalog{release: tools.Release{Identity: "v1.6.1", Artifacts: []tools.Artifact{{Name: "fpcalc.zip"}}}, archive: zipWithExecutable(t, "fpcalc", []byte("fpcalc"))},
				runtime, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(workerCommandRunner{}))
			if err := worker.Work(context.Background(), &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: id}}); err != nil {
				t.Fatal(err)
			}
			if changeBeforeRead {
				if operation.State != "failed" {
					t.Fatalf("mismatched root operation state=%s", operation.State)
				}
				for _, root := range []string{oldRoot, newRoot} {
					if _, err := os.Lstat(filepath.Join(root, installation.RelativePath)); !os.IsNotExist(err) {
						t.Fatalf("worker wrote under %s before rejecting stale root: %v", root, err)
					}
				}
				return
			}
			if operation.State != "succeeded" {
				t.Fatalf("operation state=%s", operation.State)
			}
			if _, err := os.Stat(filepath.Join(oldRoot, installation.RelativePath, "fpcalc")); err != nil {
				t.Fatalf("pinned root missing executable: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(newRoot, installation.RelativePath)); !os.IsNotExist(err) {
				t.Fatalf("worker wrote under post-read root: %v", err)
			}
		})
	}
}

func TestInstallationWorkerRequiresExactConflictConfirmation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		stage     string
		confirmed bool
		want      string
		content   string
	}{
		{"unconfirmed", "queued", false, "failed", "operator file"},
		{"confirmed", "queued", true, "succeeded", "fpcalc"},
		{"interrupted_before_publish", "materialize", false, "failed", "operator file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, installationID := uuid.New(), uuid.New()
			root := t.TempDir()
			target := filepath.Join(root, "fpcalc", "v1.6.1", "fpcalc")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("operator file"), 0o755); err != nil {
				t.Fatal(err)
			}
			snapshot := service.InstallInputSnapshot{
				SchemaVersion: 2, ToolsRoot: root, PackageKind: tools.PackageFPCalc, SourceName: "chromaprint",
				ReleaseIdentity:    "v1.6.1",
				ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip"}},
			}
			if test.confirmed {
				snapshot.ConfirmedConflicts = []string{target}
			}
			raw, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			state := "queued"
			if test.stage == "materialize" {
				state = "running"
			}
			operation := &persistence.Operation{ID: id, Kind: "install", State: state, Stage: test.stage, InputSnapshot: raw, TargetInstallationID: &installationID}
			installation := &persistence.ToolInstallation{
				ID: installationID, PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
				SourceName: "chromaprint", ReleaseIdentity: "v1.6.1", RelativePath: "fpcalc/v1.6.1", State: "preparing",
			}
			repository := &workerRepository{operation: operation, installation: installation}
			release := tools.Release{Identity: "v1.6.1", Artifacts: []tools.Artifact{{Name: "fpcalc.zip"}}}
			worker := jobs.NewInstallationWorker(repository, service.NewOperations(repository),
				workerCatalog{release: release, archive: zipWithExecutable(t, "fpcalc", []byte("fpcalc"))},
				workerSettings{root: root}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(workerCommandRunner{}))
			if err := worker.Work(context.Background(), &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: id}}); err != nil {
				t.Fatal(err)
			}
			if operation.State != test.want {
				t.Fatalf("operation state=%s, want %s", operation.State, test.want)
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != test.content {
				t.Fatalf("target=%q, want %q: %v", got, test.content, err)
			}
		})
	}
}

func TestInstallationWorkerStoresOnlySafeFailureAndAllowsUserRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	operationID := uuid.New()
	installationID := uuid.New()
	root := t.TempDir()
	release := tools.Release{Identity: "v1.6.1", Artifacts: []tools.Artifact{{Name: "fpcalc.zip"}}}
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		TargetIdentity: "fpcalc:chromaprint:v1.6.1:linux:amd64", SchemaVersion: 2, ToolsRoot: root,
		PackageKind: tools.PackageFPCalc, SourceName: "chromaprint", ReleaseIdentity: release.Identity,
		ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip", ChecksumAvailable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{ID: operationID, Kind: "install", State: "queued", Stage: "queued", InputSnapshot: snapshot, TargetInstallationID: &installationID}
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: release.Identity, RelativePath: "fpcalc/v1.6.1", State: "preparing",
	}
	repository := &workerRepository{operation: operation, installation: installation}
	worker := jobs.NewInstallationWorker(repository, service.NewOperations(repository),
		workerCatalog{release: release, archive: zipWithExecutable(t, "fpcalc", []byte("fpcalc")), checksum: strings.Repeat("0", 64)},
		workerSettings{root: root}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(workerCommandRunner{}))

	if err := worker.Work(ctx, &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: operationID}}); err != nil {
		t.Fatal(err)
	}
	if operation.State != "failed" || installation.State != "failed" || operation.SafeError == nil {
		t.Fatalf("failed operation state=%s installation=%s error=%v", operation.State, installation.State, operation.SafeError)
	}
	if strings.Contains(*operation.SafeError, "000000") || strings.Contains(*operation.SafeError, "sha256") {
		t.Fatalf("raw verification detail leaked: %s", *operation.SafeError)
	}
}

func TestInstallationWorkerClearsInterruptedStagingOnResolveFailure(t *testing.T) {
	t.Parallel()
	id, installationID := uuid.New(), uuid.New()
	root := t.TempDir()
	staging := filepath.Join(root, ".staging", id.String())
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "partial-download"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		SchemaVersion: 2, ToolsRoot: root, PackageKind: tools.PackageFPCalc, SourceName: "chromaprint",
		ReleaseIdentity: "v1.6.1", ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{ID: id, Kind: "install", State: "running", Stage: "download", InputSnapshot: snapshot, TargetInstallationID: &installationID}
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: "v1.6.1", RelativePath: "fpcalc/v1.6.1", State: "preparing",
	}
	repository := &workerRepository{operation: operation, installation: installation}
	worker := jobs.NewInstallationWorker(repository, service.NewOperations(repository),
		workerCatalog{release: tools.Release{Identity: "v1.6.1"}},
		workerSettings{root: root}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(workerCommandRunner{}))
	if err := worker.Work(context.Background(), &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: id}}); err != nil {
		t.Fatal(err)
	}
	if operation.State != "failed" {
		t.Fatalf("operation state=%s, want failed", operation.State)
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Fatalf("interrupted staging remains: %v", err)
	}
}

func TestInstallationWorkerCleanupNeverUsesCurrentRootForUntrustedOrStaleSnapshot(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		state         string
		schemaVersion int
		pinnedRoot    bool
	}{
		{name: "legacy snapshot", state: "queued", schemaVersion: 1},
		{name: "stale snapshot", state: "queued", schemaVersion: 2, pinnedRoot: true},
		{name: "failed redelivery", state: "failed", schemaVersion: 2, pinnedRoot: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, installationID := uuid.New(), uuid.New()
			currentRoot, pinnedRoot := t.TempDir(), t.TempDir()
			snapshotRoot := ""
			if test.pinnedRoot {
				snapshotRoot = pinnedRoot
			}
			snapshot, err := json.Marshal(service.InstallInputSnapshot{
				SchemaVersion: test.schemaVersion, ToolsRoot: snapshotRoot, PackageKind: tools.PackageFPCalc,
				SourceName: "chromaprint", ReleaseIdentity: "v1.6.1",
				ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			installationState := "preparing"
			if test.state == "failed" {
				installationState = "failed"
			}
			operation := &persistence.Operation{
				ID: id, Kind: "install", State: test.state, Stage: "download", InputSnapshot: snapshot,
				TargetInstallationID: &installationID,
			}
			installation := &persistence.ToolInstallation{
				ID: installationID, PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
				SourceName: "chromaprint", ReleaseIdentity: "v1.6.1", RelativePath: "fpcalc/v1.6.1", State: installationState,
			}
			repository := &workerRepository{operation: operation, installation: installation}
			worker := jobs.NewInstallationWorker(repository, service.NewOperations(repository), workerCatalog{},
				workerSettings{root: currentRoot}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(workerCommandRunner{}))
			writeStagingMarker := func(root string) string {
				t.Helper()
				staging := filepath.Join(root, ".staging", id.String())
				if err := os.MkdirAll(staging, 0o700); err != nil {
					t.Fatal(err)
				}
				marker := filepath.Join(staging, "keep-me")
				if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
					t.Fatal(err)
				}
				return marker
			}
			currentMarker := writeStagingMarker(currentRoot)
			var pinnedMarker string
			if test.pinnedRoot {
				pinnedMarker = writeStagingMarker(pinnedRoot)
			}

			if err := worker.Work(context.Background(), &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: id}}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(currentMarker); err != nil {
				t.Fatalf("staging marker under current tools root was removed: %v", err)
			}
			if pinnedMarker != "" {
				if _, err := os.Stat(pinnedMarker); !os.IsNotExist(err) {
					t.Fatalf("validated snapshot staging marker remains: %v", err)
				}
			}
		})
	}
}

type ffmpegWorkerRunner struct{}

func (ffmpegWorkerRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	if len(args) != 1 || args[0] != "-version" {
		return nil, errors.New("unexpected command arguments")
	}
	return []byte(filepath.Base(executable) + " version 8.0"), nil
}

func TestInstallationWorkerResumesPartiallyPublishedFFmpeg(t *testing.T) {
	t.Parallel()
	id, installationID := uuid.New(), uuid.New()
	root := t.TempDir()
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		SchemaVersion: 2, ToolsRoot: root, PackageKind: tools.PackageFFmpeg, SourceName: "btbn",
		ReleaseIdentity: "8.0", ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "ffmpeg.zip"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{ID: id, Kind: "install", State: "queued", Stage: "queued", InputSnapshot: snapshot, TargetInstallationID: &installationID}
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "btbn", ReleaseIdentity: "8.0", RelativePath: "ffmpeg/8.0", State: "preparing",
	}
	repository := &workerRepository{operation: operation, installation: installation, readyError: errors.New("ready write unavailable")}
	release := tools.Release{Identity: "8.0", Artifacts: []tools.Artifact{{Name: "ffmpeg.zip"}}}
	archive := zipWithExecutables(t, "ffmpeg", "ffprobe")
	worker := jobs.NewInstallationWorker(repository, service.NewOperations(repository),
		workerCatalog{release: release, archive: archive},
		workerSettings{root: root}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(ffmpegWorkerRunner{}))
	job := &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: id}}
	if err := worker.Work(context.Background(), job); err == nil {
		t.Fatal("ready write should fail after both executables are published")
	}
	first := filepath.Join(root, installation.RelativePath, "ffmpeg")
	second := filepath.Join(root, installation.RelativePath, "ffprobe")
	candidate := filepath.Join(root, ".staging", id.String(), "candidate", installation.RelativePath, "ffmpeg")
	firstInfo, err := os.Lstat(first)
	if err != nil {
		t.Fatal(err)
	}
	candidateInfo, err := os.Lstat(candidate)
	if err != nil || !os.SameFile(firstInfo, candidateInfo) {
		t.Fatalf("first publication lacks a retained ownership witness: %v", err)
	}
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	repository.readyError = nil
	worker = jobs.NewInstallationWorker(repository, service.NewOperations(repository),
		workerCatalog{}, workerSettings{root: root},
		tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(ffmpegWorkerRunner{}))
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("resume without upstream catalog failed: %v", err)
	}
	if operation.State != "succeeded" || installation.State != "ready" || !repository.activated {
		t.Fatalf("resumed state=%s installation=%s activated=%v", operation.State, installation.State, repository.activated)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("second executable was not republished: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, ".staging", id.String())); !os.IsNotExist(err) {
		t.Fatalf("publication evidence remains after success: %v", err)
	}
}

func TestInstallationWorkerRecoversCommittedReadyAfterLostResponse(t *testing.T) {
	t.Parallel()
	id, installationID := uuid.New(), uuid.New()
	root := t.TempDir()
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		SchemaVersion: 2, ToolsRoot: root, PackageKind: tools.PackageFPCalc, SourceName: "chromaprint",
		ReleaseIdentity: "v1.6.1", ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{ID: id, Kind: "install", State: "queued", Stage: "queued", InputSnapshot: snapshot, TargetInstallationID: &installationID}
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: "v1.6.1", RelativePath: "fpcalc/v1.6.1", State: "preparing",
	}
	repository := &workerRepository{
		operation: operation, installation: installation,
		readyCommittedError: errors.New("ready response lost"),
	}
	release := tools.Release{Identity: "v1.6.1", Artifacts: []tools.Artifact{{Name: "fpcalc.zip"}}}
	worker := jobs.NewInstallationWorker(repository, service.NewOperations(repository),
		workerCatalog{release: release, archive: zipWithExecutable(t, "fpcalc", []byte("fpcalc"))},
		workerSettings{root: root}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(workerCommandRunner{}))
	job := &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: id}}
	if err := worker.Work(context.Background(), job); err == nil {
		t.Fatal("expected lost ready response")
	}
	if installation.State != "ready" || operation.State != "running" {
		t.Fatalf("ambiguous ready result installation=%s operation=%s", installation.State, operation.State)
	}
	repository.readyCommittedError = nil
	worker = jobs.NewInstallationWorker(repository, service.NewOperations(repository),
		workerCatalog{}, workerSettings{root: root},
		tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(workerCommandRunner{}))
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("ready recovery tried to reinstall: %v", err)
	}
	if operation.State != "succeeded" || !repository.activated {
		t.Fatalf("recovered operation=%s activated=%v", operation.State, repository.activated)
	}
	if _, err := os.Lstat(filepath.Join(root, ".staging", id.String())); !os.IsNotExist(err) {
		t.Fatalf("publication journal remains after success: %v", err)
	}
}

func TestInstallationWorkerDoesNotAdoptIdenticalUnknownFileOnRetry(t *testing.T) {
	t.Parallel()
	id, installationID := uuid.New(), uuid.New()
	root := t.TempDir()
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		SchemaVersion: 2, ToolsRoot: root, PackageKind: tools.PackageFPCalc, SourceName: "chromaprint",
		ReleaseIdentity: "v1.6.1", ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{ID: id, Kind: "install", State: "queued", Stage: "queued", InputSnapshot: snapshot, TargetInstallationID: &installationID}
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: "v1.6.1", RelativePath: "fpcalc/v1.6.1", State: "preparing",
	}
	repository := &workerRepository{operation: operation, installation: installation, readyError: errors.New("ready write unavailable")}
	release := tools.Release{Identity: "v1.6.1", Artifacts: []tools.Artifact{{Name: "fpcalc.zip"}}}
	worker := jobs.NewInstallationWorker(repository, service.NewOperations(repository),
		workerCatalog{release: release, archive: zipWithExecutable(t, "fpcalc", []byte("fpcalc"))},
		workerSettings{root: root}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(workerCommandRunner{}))
	job := &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: id}}
	if err := worker.Work(context.Background(), job); err == nil {
		t.Fatal("ready write should fail after publication")
	}
	target := filepath.Join(root, installation.RelativePath, "fpcalc")
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, content, 0o755); err != nil {
		t.Fatal(err)
	}
	repository.readyError = nil
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if operation.State != "failed" || installation.State != "failed" {
		t.Fatalf("unknown target was not rejected: operation=%s installation=%s", operation.State, installation.State)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("unknown target changed: %q %v", got, err)
	}
}

func TestInstallationWorkerResumesConfirmedBackupBeforeLink(t *testing.T) {
	t.Parallel()
	id, installationID := uuid.New(), uuid.New()
	root := t.TempDir()
	target := filepath.Join(root, "fpcalc", "v1.6.1", "fpcalc")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("confirmed original"), 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		SchemaVersion: 2, ToolsRoot: root, PackageKind: tools.PackageFPCalc, SourceName: "chromaprint",
		ReleaseIdentity: "v1.6.1", ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip"}},
		ConfirmedConflicts: []string{target},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{ID: id, Kind: "install", State: "queued", Stage: "queued", InputSnapshot: snapshot, TargetInstallationID: &installationID}
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: "v1.6.1", RelativePath: "fpcalc/v1.6.1", State: "preparing",
	}
	repository := &workerRepository{operation: operation, installation: installation, readyError: errors.New("ready write unavailable")}
	release := tools.Release{Identity: "v1.6.1", Artifacts: []tools.Artifact{{Name: "fpcalc.zip"}}}
	worker := jobs.NewInstallationWorker(repository, service.NewOperations(repository),
		workerCatalog{release: release, archive: zipWithExecutable(t, "fpcalc", []byte("fpcalc"))},
		workerSettings{root: root}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(workerCommandRunner{}))
	job := &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: id}}
	if err := worker.Work(context.Background(), job); err == nil {
		t.Fatal("ready write should fail after confirmed publication")
	}
	backup := filepath.Join(root, ".staging", id.String(), "backups", "fpcalc")
	if content, err := os.ReadFile(backup); err != nil || string(content) != "confirmed original" {
		t.Fatalf("confirmed original was not retained: %q %v", content, err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	repository.readyError = nil
	worker = jobs.NewInstallationWorker(repository, service.NewOperations(repository),
		workerCatalog{}, workerSettings{root: root},
		tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(workerCommandRunner{}))
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("resume after original-to-backup boundary failed: %v", err)
	}
	if operation.State != "succeeded" || installation.State != "ready" {
		t.Fatalf("resumed operation=%s installation=%s", operation.State, installation.State)
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != "fpcalc" {
		t.Fatalf("confirmed target was not published: %q %v", content, err)
	}
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Fatalf("confirmed backup remains after success: %v", err)
	}
}

func zipWithExecutables(t *testing.T, names ...string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func zipWithExecutable(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
