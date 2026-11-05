//go:build integration

package persistence_test

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
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/uptrace/bun"
)

func runSourceAnalysisDeletionCleanup(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)

	root := createInventoryRoot(t, ctx, inventory, canonicalAnalysisRootPath(t, "analysis-delete"))
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 1024, probeMtime())
	variantID := insertMediaVariantRow(t, ctx, database, 1024, uuid.New())
	linkLocationVariant(t, ctx, database, location.ID, variantID)
	if err := deleteInventoryRoot(ctx, inventory, root.ID); err != nil {
		t.Fatalf("delete a root with a linked variant: %v", err)
	}
	if mediaVariantExists(t, ctx, database, variantID) {
		t.Fatal("root deletion left the linked variant behind")
	}
	if _, err := inventory.GetSourceLocation(ctx, root.ID, location.ID); !errors.Is(err, persistence.ErrSourceLocationNotFound) {
		t.Fatalf("location read after root deletion = %v, want ErrSourceLocationNotFound", err)
	}

	activeRoot := createInventoryRoot(t, ctx, inventory, canonicalAnalysisRootPath(t, "analysis-active-delete"))
	activeLocation := insertAnalysisLocation(t, ctx, database, activeRoot.ID, "album/track.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, activeRoot)
	activeVariant := insertMediaVariantRow(t, ctx, database, 1024, uuid.New())
	linkLocationVariant(t, ctx, database, activeLocation.ID, activeVariant)
	installationID := insertAnalysisInstallation(t, ctx, database, "active-delete")
	insertRunningAnalysisOperation(t, ctx, database, activeRoot, activeLocation, installationID, &activeVariant)

	if err := deleteInventoryRoot(ctx, inventory, activeRoot.ID); !errors.Is(err, persistence.ErrSourceRootActiveScan) {
		t.Fatalf("root deletion during an active analysis = %v, want ErrSourceRootActiveScan", err)
	}
	if !mediaVariantExists(t, ctx, database, activeVariant) {
		t.Fatal("a refused root deletion lost the active analysis variant")
	}
	if _, err := inventory.GetSourceRoot(ctx, activeRoot.ID); err != nil {
		t.Fatalf("a refused root deletion removed the root: %v", err)
	}

	otherRoot := createInventoryRoot(t, ctx, inventory, canonicalAnalysisRootPath(t, "analysis-other-root"))
	if _, err := inventory.GetSourceLocation(ctx, otherRoot.ID, activeLocation.ID); !errors.Is(err, persistence.ErrSourceLocationNotFound) {
		t.Fatalf("reading a location through another root = %v, want ErrSourceLocationNotFound", err)
	}
	if _, err := inventory.GetSourceLocation(ctx, activeRoot.ID, activeLocation.ID); err != nil {
		t.Fatalf("reading an owned location: %v", err)
	}
	if _, err := inventory.GetMediaVariant(ctx, uuid.New()); !errors.Is(err, persistence.ErrMediaVariantNotFound) {
		t.Fatalf("reading a missing variant = %v, want ErrMediaVariantNotFound", err)
	}
}

func canonicalAnalysisRootPath(t *testing.T, name string) string {
	t.Helper()
	path, err := os.MkdirTemp(t.TempDir(), name+"-")
	if err != nil {
		t.Fatalf("create source analysis root path: %v", err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalize source analysis root path: %v", err)
	}
	return canonical
}

// establishInventory makes a freshly created root the non-stale root of an
// applied generation, which is what an analysis snapshot requires.
func establishInventory(t *testing.T, ctx context.Context, database *bun.DB, root *persistence.SourceRoot) {
	t.Helper()
	if _, err := database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
		Set("scan_generation = 1").Set("inventory_path = ?", root.ConfiguredPath).
		Set("last_successful_scan_at = now()").Set("status = ?", persistence.SourceRootStatusAvailable).
		Where("id = ?", root.ID).Exec(ctx); err != nil {
		t.Fatalf("establish the root inventory: %v", err)
	}
	root.ScanGeneration = 1
	inventoryPath := root.ConfiguredPath
	root.InventoryPath = &inventoryPath
}

func createRunningAnalysisScan(
	t *testing.T,
	ctx context.Context,
	inventory *persistence.SourceInventoryRepository,
	client persistence.RiverInserter,
	operations *service.Operations,
	root *persistence.SourceRoot,
) *persistence.Operation {
	t.Helper()
	snapshot, err := json.Marshal(service.ScanSourceSnapshot{
		SchemaVersion: service.SourceScanSnapshotVersion, SourceRootID: root.ID,
	})
	if err != nil {
		t.Fatalf("encode scan snapshot: %v", err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "queued", Stage: "queued",
		InputSnapshot: snapshot, TargetSourceRootID: &root.ID,
	}
	if err := inventory.CreateSourceScanOperationAndEnqueue(ctx, operation, client,
		service.ScanSourceJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("enqueue scan fixture: %v", err)
	}
	if operation.RiverJobID == nil {
		t.Fatal("enqueued scan fixture has no River job")
	}
	if err := operations.Running(ctx, operation.ID, "applying"); err != nil {
		t.Fatalf("mark scan fixture running: %v", err)
	}
	operation.State, operation.Stage = "running", "applying"
	return operation
}

// triggerScanCleanup applies an empty scan generation to a root, which runs the
// same orphan-variant cleanup a real reconciliation runs.
func triggerScanCleanup(t *testing.T, ctx context.Context, database *bun.DB, inventory *persistence.SourceInventoryRepository, root *persistence.SourceRoot) {
	t.Helper()
	operation := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
		OperationID: operation.ID, ExpectedConfiguredPath: root.ConfiguredPath,
		ExpectedAttempt: operation.Attempt, ExpectedJobID: *operation.RiverJobID,
	}); err != nil {
		t.Fatalf("apply the cleanup scan: %v", err)
	}
	setOperationState(t, ctx, database, operation.ID, "succeeded")
}

// installFailingSucceededTrigger installs a BEFORE UPDATE trigger on operation
// that raises for the running -> succeeded transition of an analysis, then
// schedules its removal through test cleanup. It exists only to fail the
// apply's terminal update after the variant insert and location relink have
// already run in the same transaction.
func installFailingSucceededTrigger(t *testing.T, ctx context.Context, database *bun.DB) {
	t.Helper()
	if _, err := database.ExecContext(ctx, `
		CREATE FUNCTION fail_analysis_succeeded() RETURNS trigger AS $$
		BEGIN
			IF OLD.state = 'running' AND NEW.state = 'succeeded' THEN
				RAISE EXCEPTION 'injected terminal failure';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create the failing succeeded trigger function: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		CREATE TRIGGER fail_analysis_succeeded
		BEFORE UPDATE ON operation
		FOR EACH ROW EXECUTE FUNCTION fail_analysis_succeeded()`); err != nil {
		t.Fatalf("install the failing succeeded trigger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := database.ExecContext(context.Background(),
			`DROP TRIGGER IF EXISTS fail_analysis_succeeded ON operation`); err != nil {
			t.Errorf("drop the failing succeeded trigger: %v", err)
		}
		if _, err := database.ExecContext(context.Background(),
			`DROP FUNCTION IF EXISTS fail_analysis_succeeded()`); err != nil {
			t.Errorf("drop the failing succeeded trigger function: %v", err)
		}
	})
}

func insertAnalysisLocation(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID, relativePath string, sizeBytes int64, mtime time.Time) persistence.SourceLocation {
	t.Helper()
	location := persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: rootID, RelativePath: relativePath, SizeBytes: sizeBytes,
		Mtime: mtime.Truncate(time.Microsecond), LastSeenScanGeneration: 1, ProbeStatus: persistence.SourceProbeStatusAudio,
	}
	if _, err := database.NewInsert().Model(&location).Exec(ctx); err != nil {
		t.Fatalf("insert analysis location %s: %v", relativePath, err)
	}
	return location
}

func insertAnalysisInstallation(t *testing.T, ctx context.Context, database *bun.DB, identity string) uuid.UUID {
	t.Helper()
	now := time.Now().UTC()
	relativePath := testManagedRelativePath(t, tools.PackageFFmpeg, "darwin", identity)
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "darwin", PlatformGOARCH: "arm64",
		SourceName: "analysis-test", ReleaseIdentity: identity, RelativePath: relativePath,
		State: "ready", ExecutableVerificationOutput: verifiedExecutableVersionMetadata(t, tools.PackageFFmpeg, "darwin", "7.1.2"),
		Version:            canonicalVerifiedFixtureVersion(t, tools.PackageFFmpeg, "7.1.2"),
		ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &now,
	}
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		t.Fatalf("insert analysis installation %s: %v", identity, err)
	}
	return installation.ID
}

// Persisted installation paths follow the recorded platform, not the test host.
func testManagedRelativePath(t *testing.T, kind tools.PackageKind, goos, identity string) string {
	t.Helper()
	relativePath, err := tools.ManagedRelativePath(kind, identity)
	if err != nil {
		t.Fatalf("managed test installation path: %v", err)
	}
	if goos == "windows" {
		return strings.ReplaceAll(relativePath, "/", `\`)
	}
	return filepath.ToSlash(relativePath)
}

func verifiedExecutableVersionMetadata(t *testing.T, kind tools.PackageKind, goos, version string) json.RawMessage {
	t.Helper()
	versions := make(map[string]string)
	for _, executable := range tools.ExpectedExecutables(kind, goos) {
		name := executable
		if goos == "windows" {
			name = strings.TrimSuffix(name, ".exe")
		}
		versions[executable] = name + " version " + version
	}
	encoded, err := json.Marshal(versions)
	if err != nil {
		t.Fatalf("encode verified executable metadata: %v", err)
	}
	return encoded
}

// canonicalVerifiedFixtureVersion pairs explicit fixture evidence with the
// canonical version expected for that same fixture. It does not synthesize
// production metadata or fill missing installation values.
func canonicalVerifiedFixtureVersion(t *testing.T, kind tools.PackageKind, version string) string {
	t.Helper()
	switch kind {
	case tools.PackageFFmpeg:
		return "ffmpeg " + version + " + ffprobe " + version
	case tools.PackageFPCalc:
		return version
	default:
		t.Fatalf("unsupported verified tool fixture kind %q", kind)
		return ""
	}
}

func insertMediaVariantRow(t *testing.T, ctx context.Context, database *bun.DB, sizeBytes int64, appliedOperationID uuid.UUID) uuid.UUID {
	t.Helper()
	variant := &persistence.MediaVariant{
		ID: uuid.New(), SizeBytes: sizeBytes, AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion, FFProbeVersion: "7.1",
		FFProbeJSON: json.RawMessage(`{}`), ObservedTags: json.RawMessage(`{}`),
		InspectedAt: time.Now().UTC(), AppliedOperationID: appliedOperationID,
	}
	if _, err := database.NewInsert().Model(variant).Exec(ctx); err != nil {
		t.Fatalf("insert media variant: %v", err)
	}
	return variant.ID
}

func linkLocationVariant(t *testing.T, ctx context.Context, database *bun.DB, locationID, variantID uuid.UUID) {
	t.Helper()
	if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
		Set("media_variant_id = ?", variantID).Set("updated_at = now()").
		Where("id = ?", locationID).Exec(ctx); err != nil {
		t.Fatalf("link location %s to variant %s: %v", locationID, variantID, err)
	}
}

func readLocationByID(t *testing.T, ctx context.Context, database *bun.DB, locationID uuid.UUID) persistence.SourceLocation {
	t.Helper()
	var location persistence.SourceLocation
	if err := database.NewSelect().Model(&location).Where("id = ?", locationID).Scan(ctx); err != nil {
		t.Fatalf("read location %s: %v", locationID, err)
	}
	return location
}

func countMediaVariants(t *testing.T, ctx context.Context, database *bun.DB) int {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM media_variant").Scan(ctx, &count); err != nil {
		t.Fatalf("count media variants: %v", err)
	}
	return count
}

func mediaVariantExists(t *testing.T, ctx context.Context, database *bun.DB, id uuid.UUID) bool {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM media_variant WHERE id = ?", id).Scan(ctx, &count); err != nil {
		t.Fatalf("read media variant %s: %v", id, err)
	}
	return count == 1
}

func countScanCandidates(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID) int {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM source_scan_candidate WHERE operation_id = ?", operationID).Scan(ctx, &count); err != nil {
		t.Fatalf("count scan candidates of %s: %v", operationID, err)
	}
	return count
}

func analysisJobs(t *testing.T, ctx context.Context, database *bun.DB) int {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM river_job").Scan(ctx, &count); err != nil {
		t.Fatalf("count River jobs: %v", err)
	}
	return count
}
