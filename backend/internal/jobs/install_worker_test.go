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
	operation    *persistence.Operation
	installation *persistence.ToolInstallation
	activated    bool
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
	installation, err := repository.GetInstallation(context.Background(), id)
	if err != nil {
		return err
	}
	installation.State = "ready"
	installation.ExecutableVersions = versions
	installation.VerifiedAt = &verifiedAt
	return nil
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

type workerCommandRunner struct{}

func (workerCommandRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	if len(args) != 1 || args[0] != "--version" {
		return nil, fmt.Errorf("unexpected command arguments")
	}
	if !strings.Contains(filepath.Base(executable), "fpcalc") {
		return nil, fmt.Errorf("unexpected executable")
	}
	return []byte("fpcalc version 1.6.1"), nil
}

func TestInstallationWorkerDownloadsVerifiesMaterializesAndActivatesDuringSetup(t *testing.T) {
	ctx := context.Background()
	operationID := uuid.New()
	installationID := uuid.New()
	release := tools.Release{Identity: "v1.6.1", Artifacts: []tools.Artifact{{Name: "fpcalc.zip", URL: "https://github.com/acoustid/fpcalc.zip"}}}
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		TargetIdentity: "fpcalc:chromaprint:v1.6.1:linux:amd64", SchemaVersion: 1,
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
	root := t.TempDir()
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

func TestInstallationWorkerStoresOnlySafeFailureAndAllowsUserRetry(t *testing.T) {
	ctx := context.Background()
	operationID := uuid.New()
	installationID := uuid.New()
	release := tools.Release{Identity: "v1.6.1", Artifacts: []tools.Artifact{{Name: "fpcalc.zip"}}}
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		TargetIdentity: "fpcalc:chromaprint:v1.6.1:linux:amd64", SchemaVersion: 1,
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
		workerSettings{root: t.TempDir()}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(workerCommandRunner{}))

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
