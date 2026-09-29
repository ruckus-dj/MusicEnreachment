package jobs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

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
