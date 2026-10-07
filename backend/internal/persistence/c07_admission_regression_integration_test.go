//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// These tests exercise the actual PostgreSQL admission transaction and River's
// InsertTx. The installation rows are synthetic: no download/verification is
// claimed by this fixture.
func TestC07AdmissionAllowsOneInitialVersionPerPackage(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repo := persistence.NewSetupManagerRepository(db)
	client := c07RiverClient(t, ctx, db)
	root := c07ToolsRoot(t, ctx, db)

	first := c07Admission(t, ctx, repo, client, root, "ffmpeg", "8.0")
	if first == nil {
		t.Fatal("first initial package admission was refused")
	}
	if err := repo.MarkInstallationReady(ctx, *first.TargetInstallationID, json.RawMessage(`{"ffmpeg":"8.0"}`), time.Now().UTC()); err != nil {
		t.Fatalf("mark synthetic installation ready: %v", err)
	}
	if activated, err := repo.ActivateInstallationDuringSetup(ctx, *first.TargetInstallationID, "ffmpeg", "linux", "amd64", settings.ActiveFFmpegInstallationKey); err != nil || !activated {
		t.Fatalf("activate first synthetic ready version: activated=%t err=%v", activated, err)
	}

	if operation := c07Admission(t, ctx, repo, client, root, "ffmpeg", "8.1"); operation != nil {
		t.Fatalf("second version of same package admitted: %s", operation.ID)
	}
	assertC07OnlyRows(t, ctx, db, 1, 1)
	var active string
	if err := db.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", settings.ActiveFFmpegInstallationKey).Scan(ctx, &active); err != nil {
		t.Fatal(err)
	}
	if active != first.TargetInstallationID.String() {
		t.Fatalf("active installation = %q, want first %q", active, first.TargetInstallationID)
	}

	if operation := c07Admission(t, ctx, repo, client, root, "fpcalc", "1.5"); operation == nil {
		t.Fatal("independent package admission was refused")
	}
	assertC07OnlyRows(t, ctx, db, 2, 2)
}

func TestC07ConcurrentSamePackageAdmissionLeavesNoOrphans(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repo := persistence.NewSetupManagerRepository(db)
	client := c07RiverClient(t, ctx, db)
	root := c07ToolsRoot(t, ctx, db)
	start := make(chan struct{})
	results := make(chan *persistence.Operation, 2)
	var wg sync.WaitGroup
	for _, version := range []string{"8.0", "8.1"} {
		version := version
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- c07Admission(t, ctx, repo, client, root, "ffmpeg", version)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	admitted := 0
	for operation := range results {
		if operation != nil {
			admitted++
		}
	}
	if admitted != 1 {
		t.Fatalf("concurrent admissions = %d, want exactly one", admitted)
	}
	assertC07OnlyRows(t, ctx, db, 1, 1)
}

func c07RiverClient(t *testing.T, ctx context.Context, db *bun.DB) persistence.RiverInserter {
	t.Helper()
	driver := riverdatabasesql.New(db.DB)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	client, err := river.NewClient(driver, &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func c07ToolsRoot(t *testing.T, ctx context.Context, db *bun.DB) string {
	t.Helper()
	root, err := settings.NormalizePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO app_setting (setting_name, setting_value, updated_at) VALUES (?, ?, now())", "tools_directory", root); err != nil {
		t.Fatal(err)
	}
	return root
}

func c07Admission(t *testing.T, ctx context.Context, repo *persistence.SetupManagerRepository, client persistence.RiverInserter, root, kind, version string) *persistence.Operation {
	t.Helper()
	installationID, operationID := uuid.New(), uuid.New()
	identity := kind + ":test:" + version + ":linux:amd64"
	snapshot, err := json.Marshal(map[string]any{"target_identity": identity, "schema_version": 2, "tools_root": root})
	if err != nil {
		t.Fatal(err)
	}
	relative, err := tools.ManagedRelativePath(tools.PackageKind(kind), version)
	if err != nil {
		t.Fatal(err)
	}
	installation := &persistence.ToolInstallation{ID: installationID, PackageKind: kind, PlatformGOOS: "linux", PlatformGOARCH: "amd64", SourceName: "test", ReleaseIdentity: version, RelativePath: relative, State: "preparing", ArtifactIdentities: []byte(`[]`)}
	operation := &persistence.Operation{ID: operationID, Kind: "install", State: "queued", Stage: "queued", InputSnapshot: snapshot, TargetInstallationID: &installationID}
	err = repo.CreateInstallationOperationAndEnqueue(ctx, root, installation, operation, client, service.OperationJobArgs{OperationID: operationID}, nil)
	if err != nil {
		return nil
	}
	return operation
}

func assertC07OnlyRows(t *testing.T, ctx context.Context, db *bun.DB, installations, operations int) {
	t.Helper()
	var gotInstallations, gotOperations, jobs int
	if err := db.NewRaw("SELECT count(*) FROM tool_installation").Scan(ctx, &gotInstallations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw("SELECT count(*) FROM operation").Scan(ctx, &gotOperations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw("SELECT count(*) FROM river_job WHERE kind = ?", "operation_v1").Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if gotInstallations != installations || gotOperations != operations || jobs != operations {
		t.Fatalf("persisted installations/operations/install jobs = %d/%d/%d; want %d/%d/%d", gotInstallations, gotOperations, jobs, installations, operations, operations)
	}
}
