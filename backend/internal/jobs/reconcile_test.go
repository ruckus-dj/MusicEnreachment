package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func TestReconcilePublishedInstallBeforeReadyCompletesWithoutActivation(t *testing.T) {
	operationID, installationID := uuid.New(), uuid.New()
	root := t.TempDir()
	const release = "1.6.1"
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		TargetIdentity: "fpcalc:chromaprint:" + release + ":linux:amd64", SchemaVersion: 1,
		PackageKind: tools.PackageFPCalc, SourceName: "chromaprint", ReleaseIdentity: release,
		ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{
		ID: operationID, Kind: "install", State: "running", Stage: "files_materialized",
		InputSnapshot: snapshot, TargetInstallationID: &installationID,
	}
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: string(tools.PackageFPCalc), PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: release, RelativePath: "fpcalc/" + release, State: "preparing",
	}
	repository := &workerRepository{operation: operation, installation: installation}
	staging, err := tools.EnsureOperationStaging(root, operationID)
	if err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(staging, "candidate", installation.RelativePath, "fpcalc")
	if err := os.MkdirAll(filepath.Dir(candidate), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := []byte("verified executable witness")
	if err := os.WriteFile(candidate, contents, 0o755); err != nil {
		t.Fatal(err)
	}
	digest, err := tools.SHA256File(candidate)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, installation.RelativePath, "fpcalc")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(candidate, target); err != nil {
		t.Fatal(err)
	}
	publication := struct {
		OperationID    uuid.UUID `json:"operation_id"`
		InstallationID uuid.UUID `json:"installation_id"`
		Root           string    `json:"root"`
		PackageKind    string    `json:"package_kind"`
		Release        string    `json:"release"`
		Confirmed      []string  `json:"confirmed"`
		Files          []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}{
		OperationID: operationID, InstallationID: installationID, Root: root,
		PackageKind: string(tools.PackageFPCalc), Release: release,
		Files: []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		}{{Name: "fpcalc", SHA256: digest}},
	}
	journal, err := json.Marshal(publication)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "publication.json"), journal, 0o600); err != nil {
		t.Fatal(err)
	}

	operations := service.NewOperations(repository)
	if err := jobs.ReconcileInterruptedOperations(context.Background(), repository, operations,
		func(context.Context, *int64) (bool, error) { return false, nil }, workerSettings{root: root}, tools.NewLifecycle(workerCommandRunner{})); err != nil {
		t.Fatalf("reconcile published installation: %v", err)
	}
	if operation.State != "succeeded" || installation.State != "ready" {
		t.Fatalf("reconciled operation/installation = %s/%s; want succeeded/ready", operation.State, installation.State)
	}
	if repository.activated {
		t.Fatal("reconciliation changed the active installation selection")
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Fatalf("completed install retained publication staging: %v", err)
	}
	if info, err := os.Stat(target); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("verified managed executable was not retained: info=%v err=%v", info, err)
	}
	if repository.installation.ID != installationID {
		t.Fatalf("reconciliation changed installation identity: %s", repository.installation.ID)
	}
	if _, err := operations.Retry(context.Background(), operationID); err == nil {
		t.Fatal("completed published install remained retryable")
	}
	if repository.installation.ID != installationID {
		t.Fatalf("retry changed installation identity: %s", repository.installation.ID)
	}
}

func TestReconcileUnverifiedPublishedInstallRollsBackOwnershipAndRetryReusesTarget(t *testing.T) {
	operationID, installationID := uuid.New(), uuid.New()
	root := t.TempDir()
	const releaseIdentity = "1.6.1"
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		TargetIdentity: "fpcalc:chromaprint:" + releaseIdentity + ":linux:amd64", SchemaVersion: 1,
		PackageKind: tools.PackageFPCalc, SourceName: "chromaprint", ReleaseIdentity: releaseIdentity,
		ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{
		ID: operationID, Kind: "install", State: "running", Stage: "files_materialized",
		InputSnapshot: snapshot, TargetInstallationID: &installationID,
	}
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: string(tools.PackageFPCalc), PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: releaseIdentity, RelativePath: "fpcalc/" + releaseIdentity, State: "preparing",
	}
	repository := &workerRepository{operation: operation, installation: installation}
	staging, err := tools.EnsureOperationStaging(root, operationID)
	if err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(staging, "candidate", installation.RelativePath, "fpcalc")
	if err := os.MkdirAll(filepath.Dir(candidate), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("published but invalid executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest, err := tools.SHA256File(candidate)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, installation.RelativePath, "fpcalc")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(candidate, target); err != nil {
		t.Fatal(err)
	}
	journal, err := json.Marshal(struct {
		OperationID    uuid.UUID `json:"operation_id"`
		InstallationID uuid.UUID `json:"installation_id"`
		Root           string    `json:"root"`
		PackageKind    string    `json:"package_kind"`
		Release        string    `json:"release"`
		Confirmed      []string  `json:"confirmed"`
		Files          []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}{
		OperationID: operationID, InstallationID: installationID, Root: root,
		PackageKind: string(tools.PackageFPCalc), Release: releaseIdentity,
		Files: []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		}{{Name: "fpcalc", SHA256: digest}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "publication.json"), journal, 0o600); err != nil {
		t.Fatal(err)
	}

	operations := service.NewOperations(repository)
	if err := jobs.ReconcileInterruptedOperations(context.Background(), repository, operations,
		func(context.Context, *int64) (bool, error) { return false, nil }, workerSettings{root: root}, tools.NewLifecycle(rejectedInstallRecoveryRunner{})); err != nil {
		t.Fatalf("reconcile unverified published installation: %v", err)
	}
	if operation.State != "failed" || installation.State != "failed" || operation.SafeError == nil {
		t.Fatalf("unverified publication result = operation %s, installation %s, error %v; want safe failure", operation.State, installation.State, operation.SafeError)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("unverified owned managed target remains: %v", err)
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Fatalf("rollback retained completed publication staging: %v", err)
	}

	retried, err := operations.Retry(context.Background(), operationID)
	if err != nil {
		t.Fatalf("retry rolled-back installation: %v", err)
	}
	if retried.TargetInstallationID == nil || *retried.TargetInstallationID != installationID {
		t.Fatalf("retry changed installation target: %#v", retried)
	}
	worker := jobs.NewInstallationWorker(repository, operations,
		workerCatalog{release: tools.Release{Identity: releaseIdentity, Artifacts: []tools.Artifact{{Name: "fpcalc.zip"}}}, archive: zipWithExecutable(t, "fpcalc", []byte("fpcalc"))},
		workerSettings{root: root, completed: true}, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(workerCommandRunner{}))
	if err := worker.Work(context.Background(), &river.Job[service.OperationJobArgs]{Args: service.OperationJobArgs{OperationID: operationID}}); err != nil {
		t.Fatalf("run retry after publication rollback: %v", err)
	}
	if operation.State != "succeeded" || installation.State != "ready" || repository.installation.ID != installationID {
		t.Fatalf("retry result operation=%s installation=%s id=%s; want succeeded/ready on original row", operation.State, installation.State, repository.installation.ID)
	}
	if repository.activated {
		t.Fatal("retry changed the active installation selection")
	}
}

type rejectedInstallRecoveryRunner struct{}

func (rejectedInstallRecoveryRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("executable rejected for recovery test")
}

func TestReconcileInterruptedInstallMarksPreparingTargetFailed(t *testing.T) {
	operationID, installationID := uuid.New(), uuid.New()
	root := t.TempDir()
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		SchemaVersion: 1, PackageKind: tools.PackageFPCalc, SourceName: "chromaprint", ReleaseIdentity: "1.6.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{
		ID: operationID, Kind: "install", State: "running", Stage: "materialize",
		InputSnapshot: snapshot, TargetInstallationID: &installationID,
	}
	installation := &persistence.ToolInstallation{ID: installationID, State: "preparing"}
	repository := &workerRepository{operation: operation, installation: installation}
	staging := filepath.Join(root, ".staging", operationID.String())
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := jobs.ReconcileInterruptedOperations(context.Background(), repository, service.NewOperations(repository),
		func(context.Context, *int64) (bool, error) { return false, nil }, workerSettings{root: root}); err != nil {
		t.Fatalf("reconcile interrupted install: %v", err)
	}
	if operation.State != "failed" || operation.SafeError == nil {
		t.Fatalf("operation state/error = %q/%v; want failed with a safe error", operation.State, operation.SafeError)
	}
	if installation.State != "failed" {
		t.Fatalf("preparing target installation state = %q; want failed and deletable", installation.State)
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Fatalf("interrupted install staging remains: %v", err)
	}
}
