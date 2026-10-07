package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type multipleOperationRepository struct {
	*workerRepository
	rows []*persistence.Operation
}

func (repository *multipleOperationRepository) ListOperations(context.Context, ...string) ([]persistence.Operation, error) {
	rows := make([]persistence.Operation, 0, len(repository.rows))
	for _, operation := range repository.rows {
		rows = append(rows, *operation)
	}
	return rows, nil
}

func (repository *multipleOperationRepository) GetOperation(_ context.Context, id uuid.UUID) (*persistence.Operation, error) {
	for _, operation := range repository.rows {
		if operation.ID == id {
			return operation, nil
		}
	}
	return nil, errors.New("missing operation")
}

func (repository *multipleOperationRepository) TransitionOperation(ctx context.Context, id uuid.UUID, transition func(*persistence.Operation) error) error {
	operation, err := repository.GetOperation(ctx, id)
	if err != nil {
		return err
	}
	return transition(operation)
}

func (repository *multipleOperationRepository) FailUndeliveredInstallationRecovery(ctx context.Context, id uuid.UUID, attempt int, snapshot json.RawMessage, stage, safe string) (bool, error) {
	operation, err := repository.GetOperation(ctx, id)
	if err != nil {
		return false, err
	}
	return (&workerRepository{operation: operation}).FailUndeliveredInstallationRecovery(ctx, id, attempt, snapshot, stage, safe)
}

type recordedSetupActivation struct {
	installationID uuid.UUID
	packageKind    string
	goos           string
	goarch         string
	setting        string
}

type setupActivationRepository struct {
	*workerRepository
	calls []recordedSetupActivation
}

func (repository *setupActivationRepository) ActivateInstallationDuringSetup(ctx context.Context, id uuid.UUID, kind, goos, goarch, setting string) (bool, error) {
	repository.calls = append(repository.calls, recordedSetupActivation{
		installationID: id, packageKind: kind, goos: goos, goarch: goarch, setting: setting,
	})
	return repository.workerRepository.ActivateInstallationDuringSetup(ctx, id, kind, goos, goarch, setting)
}

func TestReconcilePublishedInstallBeforeReadyHonorsSetupActivation(t *testing.T) {
	for _, test := range []struct {
		name          string
		setupComplete bool
	}{
		{name: "setup_incomplete_activates", setupComplete: false},
		{name: "setup_complete_preserves_active_ids", setupComplete: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			operationID, installationID := uuid.New(), uuid.New()
			root := t.TempDir()
			const release = "1.6.1"
			snapshot, err := json.Marshal(service.InstallInputSnapshot{
				TargetIdentity: "fpcalc:chromaprint:" + release + ":linux:amd64", SchemaVersion: 2, ToolsRoot: root,
				PackageKind: tools.PackageFPCalc, SourceName: "chromaprint", ReleaseIdentity: release,
				ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			operation := &persistence.Operation{
				ID: operationID, Kind: "install", State: "running", Stage: "materialize", Attempt: 1, RiverJobID: workerDeliveryID(),
				InputSnapshot: snapshot, TargetInstallationID: &installationID,
			}
			installation := &persistence.ToolInstallation{
				ID: installationID, PackageKind: string(tools.PackageFPCalc), PlatformGOOS: "linux", PlatformGOARCH: "amd64",
				SourceName: "chromaprint", ReleaseIdentity: release, RelativePath: "fpcalc/" + release, State: "preparing",
			}
			repository := &setupActivationRepository{workerRepository: &workerRepository{operation: operation, installation: installation}}
			repository.setupCompleted = test.setupComplete
			staging, err := tools.EnsureOperationStaging(root, operationID)
			if err != nil {
				t.Fatal(err)
			}
			candidate := filepath.Join(staging, "candidate", installation.RelativePath, "fpcalc")
			if err := os.MkdirAll(filepath.Dir(candidate), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(candidate, []byte("verified executable witness"), 0o755); err != nil {
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
				PackageKind: string(tools.PackageFPCalc), Release: release,
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

			if err := jobs.ReconcileInterruptedOperations(context.Background(), repository, service.NewOperations(repository),
				func(context.Context, *int64) (bool, error) { return false, nil }, workerSettings{root: root, completed: test.setupComplete}, tools.NewLifecycle(workerCommandRunner{})); err != nil {
				t.Fatalf("reconcile published installation: %v", err)
			}
			if operation.State != "succeeded" || installation.State != "ready" {
				t.Fatalf("reconciled operation/installation = %s/%s; want succeeded/ready", operation.State, installation.State)
			}
			if _, err := os.Lstat(staging); !os.IsNotExist(err) {
				t.Fatalf("completed install retained publication staging: %v", err)
			}
			if info, err := os.Stat(target); err != nil || !info.Mode().IsRegular() {
				t.Fatalf("verified managed executable was not retained: info=%v err=%v", info, err)
			}
			if test.setupComplete {
				if len(repository.calls) != 0 {
					t.Fatalf("completed Setup unexpectedly activated an installation: %#v", repository.calls)
				}
			} else if len(repository.calls) != 0 {
				t.Fatalf("finalization unexpectedly invoked separate setup activation: %#v", repository.calls)
			}
			if repository.activated != !test.setupComplete {
				t.Fatalf("active-selection mutation = %v; setup complete=%v", repository.activated, test.setupComplete)
			}
			if _, err := service.NewOperations(repository).Retry(context.Background(), operationID); err == nil {
				t.Fatal("completed published install remained retryable")
			}
			if repository.installation.ID != installationID {
				t.Fatalf("reconciliation changed installation identity: %s", repository.installation.ID)
			}
		})
	}
}

func TestReconcileUnverifiedPublishedInstallRollsBackOwnershipAndRetryReusesTarget(t *testing.T) {
	operationID, installationID := uuid.New(), uuid.New()
	root := t.TempDir()
	const releaseIdentity = "1.6.1"
	target := filepath.Join(root, "fpcalc", releaseIdentity, "fpcalc")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	operatorContents := []byte("confirmed original executable")
	if err := os.WriteFile(target, operatorContents, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		TargetIdentity: "fpcalc:chromaprint:" + releaseIdentity + ":linux:amd64", SchemaVersion: 2, ToolsRoot: root,
		PackageKind: tools.PackageFPCalc, SourceName: "chromaprint", ReleaseIdentity: releaseIdentity,
		ConfirmedConflicts: []string{target}, ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{
		ID: operationID, Kind: "install", State: "running", Stage: "materialize", Attempt: 1, RiverJobID: workerDeliveryID(),
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
	backup := filepath.Join(staging, "backups", "fpcalc")
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(target, backup); err != nil {
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
		PackageKind: string(tools.PackageFPCalc), Release: releaseIdentity, Confirmed: []string{target},
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
	if restored, err := os.ReadFile(target); err != nil || string(restored) != string(operatorContents) {
		t.Fatalf("confirmed original target was not restored: %q %v", restored, err)
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
	repository.setupCompleted = true
	if err := worker.Work(context.Background(), workerJob(operationID)); err != nil {
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

func TestReconcileInvalidInstallPublicationFailsLocallyAndContinues(t *testing.T) {
	operationID, installationID, untouchedID := uuid.New(), uuid.New(), uuid.New()
	root := t.TempDir()
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		TargetIdentity: "fpcalc:chromaprint:1.6.1:linux:amd64", SchemaVersion: 2, ToolsRoot: root,
		PackageKind: tools.PackageFPCalc, SourceName: "chromaprint", ReleaseIdentity: "1.6.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{
		ID: operationID, Kind: "install", State: "running", Stage: "materialize", Attempt: 1, RiverJobID: workerDeliveryID(),
		InputSnapshot: snapshot, TargetInstallationID: &installationID,
	}
	untouched := &persistence.Operation{ID: untouchedID, Kind: "install", State: "queued", Stage: "materialize"}
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: "1.6.1", RelativePath: "fpcalc/1.6.1", State: "preparing",
	}
	repository := &multipleOperationRepository{
		workerRepository: &workerRepository{operation: operation, installation: installation},
		rows:             []*persistence.Operation{operation, untouched},
	}
	staging, err := tools.EnsureOperationStaging(root, operationID)
	if err != nil {
		t.Fatal(err)
	}
	invalidJournal := []byte(`{"operation_id":"` + uuid.NewString() + `"}`)
	journalPath := filepath.Join(staging, "publication.json")
	if err := os.WriteFile(journalPath, invalidJournal, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := jobs.ReconcileInterruptedOperations(context.Background(), repository, service.NewOperations(repository),
		func(context.Context, *int64) (bool, error) { return false, nil }, workerSettings{root: root}); err != nil {
		t.Fatalf("reconcile operations with invalid publication evidence: %v", err)
	}
	const invalidEvidenceError = "The interrupted tool operation has invalid publication evidence. Resolve the installation before retrying the operation."
	if operation.State != "failed" || operation.Stage != "materialize" || operation.SafeError == nil || *operation.SafeError != invalidEvidenceError {
		t.Fatalf("invalid-journal operation = %s/%s safe error %v; want failed at materialize with scoped guidance", operation.State, operation.Stage, operation.SafeError)
	}
	if installation.State != "failed" {
		t.Fatalf("invalid-journal installation state = %s; want failed/deletable", installation.State)
	}
	if repository.activated {
		t.Fatal("invalid publication recovery changed the active installation selection")
	}
	if untouched.State != "failed" || untouched.SafeError == nil || *untouched.SafeError != "The installation snapshot predates tools-directory pinning and cannot be safely recovered. Start a new installation." {
		t.Fatalf("later operation = %s safe error %v; want generic reconciliation to continue", untouched.State, untouched.SafeError)
	}
	if _, err := os.Stat(staging); err != nil {
		t.Fatalf("invalid publication staging was removed: %v", err)
	}
	if contents, err := os.ReadFile(journalPath); err != nil || string(contents) != string(invalidJournal) {
		t.Fatalf("invalid ownership evidence was not preserved: %q %v", contents, err)
	}
}

func TestReconcileInterruptedInstallMarksPreparingTargetFailed(t *testing.T) {
	operationID, installationID := uuid.New(), uuid.New()
	root := t.TempDir()
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		SchemaVersion: 2, ToolsRoot: root, PackageKind: tools.PackageFPCalc, SourceName: "chromaprint", ReleaseIdentity: "1.6.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{
		ID: operationID, Kind: "install", State: "running", Stage: "materialize", Attempt: 1, RiverJobID: workerDeliveryID(),
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
