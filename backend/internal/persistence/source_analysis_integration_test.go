//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// TestSourceScanReconcilesVariantLinksWithPostgreSQL proves scan reconciliation:
// an unchanged audio file keeps its link, a file whose size or mtime moved or
// that is no longer audio loses it, an unseen file takes its orphan with it, a
// configured-path change invalidates every link of the previous path, and a
// failed apply leaves both inventory and links untouched.
func TestSourceScanReconcilesVariantLinksWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	operations := service.NewOperations(persistence.NewSetupManagerRepository(database))

	seedLinked := func(t *testing.T, path string, size int64, mtime time.Time) (*persistence.SourceRoot, persistence.SourceLocation, uuid.UUID) {
		t.Helper()
		root := createInventoryRoot(t, ctx, inventory, path)
		// The seeded location belongs to an already applied generation, so the
		// next apply advances the root past it and a missing file is pruned.
		if _, err := database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
			Set("scan_generation = 1").Where("id = ?", root.ID).Exec(ctx); err != nil {
			t.Fatalf("seed the root generation: %v", err)
		}
		root.ScanGeneration = 1
		location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", size, mtime)
		variantID := insertMediaVariantRow(t, ctx, database, size, uuid.New())
		linkLocationVariant(t, ctx, database, location.ID, variantID)
		return root, location, variantID
	}
	applyScan := func(t *testing.T, root *persistence.SourceRoot, candidates ...persistence.SourceScanCandidateInput) {
		t.Helper()
		operation := createRunningAnalysisScan(t, ctx, inventory, client, operations, root)
		if err := inventory.ReplaceSourceScanCandidates(ctx, operation.ID, candidates); err != nil {
			t.Fatalf("store scan candidates: %v", err)
		}
		if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
			OperationID: operation.ID, ExpectedConfiguredPath: root.ConfiguredPath,
			ExpectedAttempt: operation.Attempt, ExpectedJobID: *operation.RiverJobID,
		}); err != nil {
			t.Fatalf("apply scan: %v", err)
		}
		if err := operations.Succeed(ctx, operation.ID, "applied"); err != nil {
			t.Fatalf("finish applied scan: %v", err)
		}
	}

	t.Run("unchanged_keeps_link", func(t *testing.T) {
		mtime := probeMtime()
		root, location, variantID := seedLinked(t, "/srv/reconcile-unchanged", 1024, mtime)
		applyScan(t, root, sourceCandidate("album/track.flac", 1024, mtime))
		if link := readLocationByID(t, ctx, database, location.ID).MediaVariantID; link == nil || *link != variantID {
			t.Fatalf("unchanged file link = %v, want %s", link, variantID)
		}
		if !mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("unchanged scan deleted the variant")
		}
	})

	t.Run("changed_size_detaches", func(t *testing.T) {
		mtime := probeMtime()
		root, location, variantID := seedLinked(t, "/srv/reconcile-size", 1024, mtime)
		applyScan(t, root, sourceCandidate("album/track.flac", 2048, mtime))
		if link := readLocationByID(t, ctx, database, location.ID).MediaVariantID; link != nil {
			t.Fatalf("changed size kept variant link %s", *link)
		}
		if mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("changed size left an orphan variant")
		}
	})

	t.Run("changed_mtime_detaches", func(t *testing.T) {
		mtime := probeMtime()
		root, location, variantID := seedLinked(t, "/srv/reconcile-mtime", 1024, mtime)
		applyScan(t, root, sourceCandidate("album/track.flac", 1024, mtime.Add(time.Second)))
		if link := readLocationByID(t, ctx, database, location.ID).MediaVariantID; link != nil {
			t.Fatalf("changed mtime kept variant link %s", *link)
		}
		if mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("changed mtime left an orphan variant")
		}
	})

	t.Run("no_audio_detaches", func(t *testing.T) {
		mtime := probeMtime()
		root, location, variantID := seedLinked(t, "/srv/reconcile-no-audio", 1024, mtime)
		candidate := sourceCandidate("album/track.flac", 1024, mtime)
		candidate.ProbeStatus = persistence.SourceProbeStatusNoAudio
		applyScan(t, root, candidate)
		if link := readLocationByID(t, ctx, database, location.ID).MediaVariantID; link != nil {
			t.Fatalf("a no_audio file kept variant link %s", *link)
		}
		if mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("a no_audio file left an orphan variant")
		}
	})

	t.Run("pruned_location_deletes_variant", func(t *testing.T) {
		mtime := probeMtime()
		root, location, variantID := seedLinked(t, "/srv/reconcile-prune", 1024, mtime)
		applyScan(t, root, sourceCandidate("album/other.flac", 512, probeMtime()))
		if _, err := inventory.GetSourceLocation(ctx, root.ID, location.ID); !errors.Is(err, persistence.ErrSourceLocationNotFound) {
			t.Fatalf("pruned location read = %v, want ErrSourceLocationNotFound", err)
		}
		if mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("pruning a location left its variant behind")
		}
	})

	t.Run("configured_path_change_invalidates_links", func(t *testing.T) {
		root := createInventoryRoot(t, ctx, inventory, "/srv/reconcile-path-a")
		seedMtime := probeMtime()
		first := newSourceScanOperation(t, ctx, database, root, "running")
		if err := inventory.ReplaceSourceScanCandidates(ctx, first.ID, []persistence.SourceScanCandidateInput{
			sourceCandidate("album/track.flac", 1024, seedMtime),
		}); err != nil {
			t.Fatalf("store the first scan: %v", err)
		}
		if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
			OperationID: first.ID, ExpectedConfiguredPath: root.ConfiguredPath,
			ExpectedAttempt: first.Attempt, ExpectedJobID: *first.RiverJobID,
		}); err != nil {
			t.Fatalf("apply the first scan: %v", err)
		}
		setOperationState(t, ctx, database, first.ID, "succeeded")
		location, err := inventory.GetSourceLocation(ctx, root.ID, locationID(t, ctx, database, root.ID, "album/track.flac"))
		if err != nil {
			t.Fatalf("read the seeded location: %v", err)
		}
		variantID := insertMediaVariantRow(t, ctx, database, 1024, uuid.New())
		linkLocationVariant(t, ctx, database, location.ID, variantID)

		stored, err := inventory.GetSourceRoot(ctx, root.ID)
		if err != nil {
			t.Fatalf("read the root before the path change: %v", err)
		}
		stored.ConfiguredPath = "/srv/reconcile-path-b"
		if err := inventory.UpdateSourceRoot(ctx, stored); err != nil {
			t.Fatalf("change the configured path: %v", err)
		}
		changed, err := inventory.GetSourceRoot(ctx, root.ID)
		if err != nil || !changed.Stale() {
			t.Fatalf("root after the path change = %+v, %v; want stale", changed, err)
		}
		newRootScan := newSourceScanOperation(t, ctx, database, changed, "running")
		if err := inventory.ReplaceSourceScanCandidates(ctx, newRootScan.ID, []persistence.SourceScanCandidateInput{
			sourceCandidate("album/track.flac", 1024, seedMtime),
		}); err != nil {
			t.Fatalf("store the new path scan: %v", err)
		}
		if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
			OperationID: newRootScan.ID, ExpectedConfiguredPath: changed.ConfiguredPath,
			ExpectedAttempt: newRootScan.Attempt, ExpectedJobID: *newRootScan.RiverJobID,
		}); err != nil {
			t.Fatalf("apply the new path scan: %v", err)
		}
		setOperationState(t, ctx, database, newRootScan.ID, "succeeded")
		if link := readLocationByID(t, ctx, database, location.ID).MediaVariantID; link != nil {
			t.Fatalf("a new inventory path kept the link of the previous path: %s", *link)
		}
		if mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("a new inventory path left the previous variant behind")
		}
	})

	t.Run("failed_apply_keeps_link", func(t *testing.T) {
		mtime := probeMtime()
		root, location, variantID := seedLinked(t, "/srv/reconcile-failed", 1024, mtime)
		operation := createRunningAnalysisScan(t, ctx, inventory, client, operations, root)
		if err := inventory.ReplaceSourceScanCandidates(ctx, operation.ID, []persistence.SourceScanCandidateInput{
			sourceCandidate("album/track.flac", 4096, probeMtime()),
		}); err != nil {
			t.Fatalf("store the failing scan candidates: %v", err)
		}
		if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
			OperationID: operation.ID, ExpectedConfiguredPath: root.ConfiguredPath + "/moved",
			ExpectedAttempt: operation.Attempt, ExpectedJobID: *operation.RiverJobID,
		}); err == nil {
			t.Fatal("a scan apply with a moved configured path was accepted")
		}
		if link := readLocationByID(t, ctx, database, location.ID).MediaVariantID; link == nil || *link != variantID {
			t.Fatalf("failed scan link = %v, want the preserved %s", link, variantID)
		}
		if !mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("a failed scan deleted the linked variant")
		}
		if candidateCount := countScanCandidates(t, ctx, database, operation.ID); candidateCount != 1 {
			t.Fatalf("candidates after the failed scan = %d, want the durable 1", candidateCount)
		}
	})
}

// TestSourceAnalysisDeletionCleanupWithPostgreSQL proves the deletion boundary:
// deleting a root removes its locations and their now orphaned variants, an
// active analysis blocks the deletion and keeps its variant, and typed reads are
// scoped to the owning root.
func TestSourceAnalysisDeletionCleanupWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)

	root := createInventoryRoot(t, ctx, inventory, "/srv/analysis-delete")
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

	activeRoot := createInventoryRoot(t, ctx, inventory, "/srv/analysis-active-delete")
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

	otherRoot := createInventoryRoot(t, ctx, inventory, "/srv/analysis-other-root")
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
	shaEnabled := true
	snapshot, err := json.Marshal(service.ScanSourceSnapshot{
		SchemaVersion: service.SourceScanSnapshotVersion, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, ScanGeneration: root.ScanGeneration,
		SHA256Enabled: &shaEnabled, Tools: []persistence.SourceAnalysisToolSelection{},
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
	relativePath, err := tools.ManagedRelativePath(tools.PackageFFmpeg, identity)
	if err != nil {
		t.Fatalf("managed analysis installation path: %v", err)
	}
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "darwin", PlatformGOARCH: "arm64",
		SourceName: "analysis-test", ReleaseIdentity: identity, RelativePath: relativePath,
		State: "ready", ExecutableVersions: verifiedExecutableVersionMetadata(t, tools.PackageFFmpeg, "darwin", "7.1.2"),
		ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &now,
	}
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		t.Fatalf("insert analysis installation %s: %v", identity, err)
	}
	return installation.ID
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
