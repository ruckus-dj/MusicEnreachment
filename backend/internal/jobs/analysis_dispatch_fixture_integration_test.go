//go:build integration

package jobs

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type analysisDispatchRepository struct {
	*persistence.SetupManagerRepository
	*persistence.SourceInventoryRepository
}

type analysisDispatchFixture struct {
	database       *bun.DB
	databaseURL    string
	inventory      *persistence.SourceInventoryRepository
	setup          *persistence.SetupManagerRepository
	registry       *settings.Registry
	platform       settings.PlatformState
	operations     *service.Operations
	client         *river.Client[*sql.Tx]
	events         <-chan *river.Event
	toolsRoot      string
	probeLog       string
	source         string
	root           service.SourceRoot
	track          persistence.SourceLocation
	installationID uuid.UUID
	helperPath     string
}

func newAnalysisDispatchFixture(t *testing.T) analysisDispatchFixture {
	t.Helper()
	ctx := context.Background()
	database := testpostgres.OpenMigrated(t)
	databaseURL := testpostgres.URL(t, database)
	settingsRepository := persistence.NewSettingsRepository(database)
	toolsRoot := t.TempDir()
	setRuntimeRoots(t, ctx, settingsRepository, toolsRoot)
	registry := settings.New(settingsRepository, nil)
	platform := settings.PlatformState{Platform: settings.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}}
	if _, err := registry.InitializePlatform(ctx, platform.Platform); err != nil {
		t.Fatalf("initialize the instance platform: %v", err)
	}
	probeLog := filepath.Join(toolsRoot, "probe-log")
	installationID, helperPath := writeAnalysisDispatchFFmpeg(t, ctx, database, settingsRepository, toolsRoot, platform)

	setup := persistence.NewSetupManagerRepository(database)
	inventory := persistence.NewSourceInventoryRepository(database)
	roots := service.NewSourceRoots(inventory, registry)
	source := t.TempDir()
	writeScanDispatchFile(t, filepath.Join(source, "album", "track.flac"), "audio bytes")
	root, err := roots.Create(ctx, "Music", source)
	if err != nil {
		t.Fatalf("create the source root: %v", err)
	}
	track := registerAnalysisDispatchLocation(t, ctx, database, root, "album/track.flac", 1)

	slot := &analysisDispatchRiverSlot{}
	operations := service.NewOperationsWithRiver(setup, slot)
	worker := NewSourceAnalysisWorker(
		analysisDispatchRepository{SetupManagerRepository: setup, SourceInventoryRepository: inventory},
		operations, registry, registry, platform,
	)
	client, listenerPool := startAnalysisDispatchRiver(t, databaseURL, database, worker)
	t.Cleanup(func() { listenerPool.Close() })
	t.Cleanup(func() { stopRiverClient(t, client) })
	slot.RiverInserter = client
	events, cancelEvents := client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(cancelEvents)

	return analysisDispatchFixture{
		database: database, databaseURL: databaseURL, inventory: inventory, setup: setup, registry: registry,
		platform: platform, operations: operations, client: client, events: events,
		toolsRoot: toolsRoot, probeLog: probeLog, source: source, root: root, track: track, installationID: installationID, helperPath: helperPath,
	}
}

// analysisDispatchRiverSlot lets the operations service be built before the
// River client exists, exactly as the composition root wires the application.
type analysisDispatchRiverSlot struct{ persistence.RiverInserter }

func (fixture *analysisDispatchFixture) start(t *testing.T, ctx context.Context) *persistence.Operation {
	t.Helper()
	operation, err := fixture.scans().Start(ctx, service.SourceAnalysisStartRequest{
		RootID: fixture.root.ID, LocationID: fixture.track.ID,
		ExpectedSizeBytes: fixture.track.SizeBytes, ExpectedMtime: fixture.track.Mtime,
	})
	if err != nil {
		t.Fatalf("start an analysis: %v", err)
	}
	return operation
}

func (fixture *analysisDispatchFixture) scans() *service.SourceAnalysisOperations {
	return service.NewSourceAnalysisOperations(persistence.NewSourceAnalysisStartStore(fixture.database),
		fixture.registry, fixture.registry, fixture.platform, fixture.client)
}

func (fixture *analysisDispatchFixture) deliver(t *testing.T, ctx context.Context, operationID uuid.UUID) int64 {
	t.Helper()
	tx, err := fixture.database.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin analysis delivery: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	inserted, err := fixture.client.InsertTx(ctx, tx,
		service.SourceAnalysisJobArgs{OperationID: operationID}, &river.InsertOpts{Queue: service.SourceAnalysisQueue})
	if err != nil {
		t.Fatalf("insert the analysis delivery: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit the analysis delivery: %v", err)
	}
	return inserted.Job.ID
}

func (fixture *analysisDispatchFixture) readOperation(t *testing.T, ctx context.Context, id uuid.UUID) *persistence.Operation {
	t.Helper()
	operation, err := fixture.setup.GetOperation(ctx, id)
	if err != nil {
		t.Fatalf("read operation %s: %v", id, err)
	}
	return operation
}

func (fixture *analysisDispatchFixture) requireLinkedVariant(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	location, err := fixture.inventory.GetSourceLocation(ctx, fixture.root.ID, fixture.track.ID)
	if err != nil {
		t.Fatalf("read the analyzed location: %v", err)
	}
	if location.MediaVariantID == nil {
		t.Fatal("the analyzed location has no linked variant")
	}
	return *location.MediaVariantID
}

func (fixture *analysisDispatchFixture) countVariants(t *testing.T, ctx context.Context) int {
	t.Helper()
	return countScanDispatchRows(t, ctx, fixture.database, "SELECT count(*) FROM media_variant")
}

func (fixture *analysisDispatchFixture) countJobs(t *testing.T, ctx context.Context, operationID uuid.UUID) int {
	t.Helper()
	return countScanDispatchRows(t, ctx, fixture.database,
		"SELECT count(*) FROM river_job WHERE kind = ? AND args ->> 'operation_id' = ?",
		service.SourceAnalysisJobKind, operationID.String())
}

func (fixture *analysisDispatchFixture) removeFFProbe(t *testing.T) {
	t.Helper()
	name := analysisProbeExecutableName(t, "ffprobe")
	if err := os.Remove(filepath.Join(fixture.toolsRoot, "ffmpeg", analysisDispatchRelease, name)); err != nil {
		t.Fatalf("remove the managed %s: %v", name, err)
	}
}

func (fixture *analysisDispatchFixture) restoreFFProbe(t *testing.T) {
	t.Helper()
	contents, err := os.ReadFile(fixture.helperPath)
	if err != nil {
		t.Fatalf("read the built analysis probe: %v", err)
	}
	name := analysisProbeExecutableName(t, "ffprobe")
	if err := os.WriteFile(filepath.Join(fixture.toolsRoot, "ffmpeg", analysisDispatchRelease, name), contents, 0o755); err != nil {
		t.Fatalf("restore the managed %s: %v", name, err)
	}
}

// registerAnalysisDispatchLocation writes one approved audio file, makes the root
// the non-stale root of generation one and inserts the audio location with the
// file's on-disk identity.
func registerAnalysisDispatchLocation(t *testing.T, ctx context.Context, database *bun.DB, root service.SourceRoot, relativePath string, generation int64) persistence.SourceLocation {
	t.Helper()
	if _, err := database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
		Set("scan_generation = ?", generation).Set("inventory_path = ?", root.ConfiguredPath).
		Set("last_successful_scan_at = now()").Set("status = ?", persistence.SourceRootStatusAvailable).
		Where("id = ?", root.ID).Exec(ctx); err != nil {
		t.Fatalf("establish the root inventory: %v", err)
	}
	info, err := os.Stat(filepath.Join(root.ConfiguredPath, relativePath))
	if err != nil {
		t.Fatalf("stat %q: %v", relativePath, err)
	}
	location := persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: root.ID, RelativePath: relativePath,
		SizeBytes: info.Size(), Mtime: info.ModTime().Truncate(time.Microsecond),
		LastSeenScanGeneration: generation, ProbeStatus: persistence.SourceProbeStatusAudio,
	}
	if _, err := database.NewInsert().Model(&location).Exec(ctx); err != nil {
		t.Fatalf("insert the analysis location: %v", err)
	}
	return location
}

func requireAnalysisHolds(t *testing.T, operation *persistence.Operation, variant, installation *uuid.UUID) {
	t.Helper()
	if !sameOptionalUUIDForTest(operation.AnalysisMediaVariantID, variant) {
		t.Fatalf("analysis variant hold = %v, want %v", operation.AnalysisMediaVariantID, variant)
	}
	if !sameOptionalUUIDForTest(operation.AnalysisInstallationID, installation) {
		t.Fatalf("analysis installation hold = %v, want %v", operation.AnalysisInstallationID, installation)
	}
}

func sameOptionalUUIDForTest(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func startAnalysisDispatchRiver(t *testing.T, databaseURL string, database *bun.DB, worker *SourceAnalysisWorker) (*river.Client[*sql.Tx], *pgxpool.Pool) {
	t.Helper()
	client, listenerPool, err := StartWithWorkers(context.Background(), databaseURL, database.DB, func(workers *river.Workers) {
		river.AddWorker(workers, worker)
	})
	if err != nil {
		t.Fatalf("start River with the analysis worker: %v", err)
	}
	return client, listenerPool
}

func countScanDispatchRows(t *testing.T, ctx context.Context, database *bun.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := database.NewRaw(query, args...).Scan(ctx, &count); err != nil {
		t.Fatalf("count rows (%s): %v", query, err)
	}
	return count
}
