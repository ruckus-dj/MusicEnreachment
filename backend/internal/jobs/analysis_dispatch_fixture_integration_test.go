//go:build integration

package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
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
	settings       *persistence.SettingsRepository
	platform       settings.PlatformState
	operations     *service.Operations
	worker         *SourceAnalysisWorker
	client         *river.Client[*sql.Tx]
	events         <-chan *river.Event
	toolsRoot      string
	probeLog       string
	source         string
	root           service.SourceRoot
	track          persistence.SourceLocation
	work           persistence.SourceAnalysisWork
	installationID uuid.UUID
	helperPath     string
	fpcalcID       uuid.UUID
}

func newAnalysisDispatchFixture(t *testing.T) analysisDispatchFixture {
	return newAnalysisDispatchFixtureWithPreparer(t, nil)
}

func newAnalysisDispatchFixtureWithPreparer(t *testing.T, preparer service.SourceAnalysisPreparing) analysisDispatchFixture {
	t.Helper()
	ctx := context.Background()
	database := testpostgres.OpenMigrated(t)
	databaseURL := testpostgres.URL(t, database)
	settingsRepository := persistence.NewSettingsRepository(database)
	toolsRoot := t.TempDir()
	setRuntimeRoots(t, ctx, settingsRepository, toolsRoot)
	registry := settings.New(settingsRepository, nil)
	if err := registry.CompleteSetup(ctx); err != nil {
		t.Fatalf("complete setup for analysis dispatch: %v", err)
	}
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
	work := persistence.SourceAnalysisWork{
		ID: uuid.New(), LocationID: track.ID, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, InventoryPath: root.ConfiguredPath,
		RelativePath: track.RelativePath, SizeBytes: track.SizeBytes, Mtime: track.Mtime,
		SHA256Enabled: true, OriginScanOperationID: uuid.New(),
	}
	if err := inventory.StoreSourceAnalysisWork(ctx, &work, []persistence.SourceAnalysisStepInput{
		{Step: persistence.SourceStepSHA256, State: "pending"},
		{Step: persistence.SourceStepProbe, State: "pending"},
		{Step: persistence.SourceStepFingerprint, State: "not_requested"},
	}); err != nil {
		t.Fatalf("store current normalized work: %v", err)
	}

	slot := &analysisDispatchRiverSlot{}
	operations := service.NewOperationsWithRiver(setup, slot)
	worker := NewSourceAnalysisWorker(
		analysisDispatchRepository{SetupManagerRepository: setup, SourceInventoryRepository: inventory},
		operations, registry, registry, platform,
	)
	if preparer != nil {
		worker.WithPreparer(preparer)
	}
	client, listenerPool := startAnalysisDispatchRiver(t, databaseURL, database, worker)
	t.Cleanup(func() { listenerPool.Close() })
	t.Cleanup(func() { stopRiverClient(t, client) })
	slot.RiverInserter = client
	events, cancelEvents := client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(cancelEvents)

	return analysisDispatchFixture{
		database: database, databaseURL: databaseURL, inventory: inventory, setup: setup, registry: registry, settings: settingsRepository,
		platform: platform, operations: operations, worker: worker, client: client, events: events,
		toolsRoot: toolsRoot, probeLog: probeLog, source: source, root: root, track: track, work: work, installationID: installationID, helperPath: helperPath,
	}
}

// installAnalysisDispatchFPCalc adds a verified fake fpcalc executable to the
// fixture. It copies the native fixture program, so verification uses the same
// executable behavior as the managed ffmpeg fixture without external tools.
func (fixture *analysisDispatchFixture) installAnalysisDispatchFPCalc(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	const release = analysisDispatchRelease
	installationID := uuid.New()
	directory := filepath.Join(fixture.toolsRoot, "fpcalc", release)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create the managed fpcalc directory: %v", err)
	}
	contents, err := os.ReadFile(fixture.helperPath)
	if err != nil {
		t.Fatalf("read fake analysis executable: %v", err)
	}
	names := tools.ExpectedExecutables(tools.PackageFPCalc, runtime.GOOS)
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(directory, name), contents, 0o755); err != nil {
			t.Fatalf("write managed fpcalc executable: %v", err)
		}
	}
	versions, err := json.Marshal(map[string]string{names[0]: "fpcalc version " + release})
	if err != nil {
		t.Fatalf("marshal fake fpcalc versions: %v", err)
	}
	verifiedAt := time.Now().UTC()
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: string(tools.PackageFPCalc),
		PlatformGOOS: fixture.platform.Platform.GOOS, PlatformGOARCH: fixture.platform.Platform.GOARCH,
		SourceName: "analysis-dispatch-test", ReleaseIdentity: release,
		RelativePath: filepath.Join("fpcalc", release), State: "ready",
		ExecutableVersions: versions, ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verifiedAt,
	}
	if err := fixture.setup.CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create managed fpcalc installation: %v", err)
	}
	if err := fixture.settings.Set(ctx, settings.ActiveFPCalcInstallationKey, installationID.String()); err != nil {
		t.Fatalf("activate managed fpcalc installation: %v", err)
	}
	fixture.fpcalcID = installationID
	return installationID
}

// analysisDispatchRiverSlot lets the operations service be built before the
// River client exists, exactly as the composition root wires the application.
type analysisDispatchRiverSlot struct{ persistence.RiverInserter }

func (fixture *analysisDispatchFixture) start(t *testing.T, ctx context.Context) *persistence.Operation {
	return fixture.startWithSchedule(t, ctx, false)
}

func (fixture *analysisDispatchFixture) startScheduled(t *testing.T, ctx context.Context) *persistence.Operation {
	return fixture.startWithSchedule(t, ctx, true)
}

func (fixture *analysisDispatchFixture) startWithSchedule(t *testing.T, ctx context.Context, scheduled bool) *persistence.Operation {
	t.Helper()
	// The location has one current normalized work row. Re-arm its pending steps for
	// this delivery rather than inserting another work identity for that location.
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_analysis_step SET state='pending',step_attempt=0,
		safe_error=NULL,skip_reason=NULL,input_snapshot=NULL,execution_operation_id=NULL,
		execution_operation_attempt=NULL,execution_job_id=NULL,last_operation_id=NULL,
		success_probe_variant_id=NULL,success_reuse_origin=NULL,updated_at=now()
		WHERE work_id=? AND step IN ('sha256','probe')`, fixture.work.ID); err != nil {
		t.Fatalf("reset current analysis work for a fresh delivery: %v", err)
	}
	shaEnabled, rerun, cacheOnly := true, false, false
	tool := persistence.SourceAnalysisToolSelection{
		PackageKind: "ffmpeg", InstallationID: fixture.installationID,
		RelativePath: filepath.Join("ffmpeg", analysisDispatchRelease), Executable: "ffprobe",
		Version: analysisDispatchRelease, VersionBanner: "ffprobe version " + analysisDispatchRelease,
	}
	selectedTools := []persistence.SourceAnalysisToolSelection{tool}
	if fixture.fpcalcID != uuid.Nil {
		selectedTools = append(selectedTools, persistence.SourceAnalysisToolSelection{
			PackageKind: "fpcalc", InstallationID: fixture.fpcalcID,
			RelativePath: filepath.Join("fpcalc", analysisDispatchRelease), Executable: "fpcalc",
			Version: analysisDispatchRelease, VersionBanner: "fpcalc version " + analysisDispatchRelease,
		})
	}
	rawSnapshot, err := json.Marshal(persistence.SourceAnalysisOperationSnapshot{
		SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion,
		Mode:          persistence.SourceAnalysisModeBatch, WorkIDs: []uuid.UUID{fixture.work.ID},
		SHA256Enabled: &shaEnabled, RerunTarget: &rerun, CacheOnlyReuse: &cacheOnly,
		ToolsReadRequired: true, Tools: selectedTools,
	})
	if err != nil {
		t.Fatalf("marshal normalized current-work snapshot: %v", err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceAnalysisOperationKind, State: "queued", Stage: service.SourceAnalysisStageQueued,
		InputSnapshot: rawSnapshot, Attempt: 1, SourceAnalysisMode: persistence.SourceAnalysisModeBatch,
		TargetSourceRootID: &fixture.root.ID, ToolsReadRequired: true,
	}
	insertOptions := &river.InsertOpts{Queue: service.SourceAnalysisQueue}
	if scheduled {
		insertOptions.ScheduledAt = time.Now().Add(time.Hour)
	}
	if err := fixture.inventory.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, fixture.client,
		service.SourceAnalysisJobArgs{OperationID: operation.ID}, insertOptions); err != nil {
		t.Fatalf("admit normalized current analysis work: %v", err)
	}
	if operation.RiverJobID == nil {
		t.Fatal("pending analysis was not admitted with a River delivery")
	}
	snapshot, err := persistence.DecodeSourceAnalysisOperationSnapshot(operation.InputSnapshot)
	if err != nil {
		t.Fatalf("decode admitted current-work snapshot: %v", err)
	}
	if len(snapshot.WorkIDs) != 1 || snapshot.WorkIDs[0] != fixture.work.ID {
		t.Fatalf("admitted work selection = %v, want current work %s", snapshot.WorkIDs, fixture.work.ID)
	}
	return operation
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
	if _, err := fixture.database.ExecContext(ctx,
		"UPDATE operation SET river_job_id = ? WHERE id = ? AND river_job_id IS NULL", inserted.Job.ID, operationID); err != nil {
		t.Fatalf("attach actual analysis delivery: %v", err)
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

func requireAnalysisHolds(t *testing.T, ctx context.Context, fixture analysisDispatchFixture, operationID uuid.UUID, expectedInstallations, expectedWork []uuid.UUID) {
	t.Helper()
	var installations []uuid.UUID
	if err := fixture.database.NewRaw(`SELECT installation_id FROM operation_tool_read_hold WHERE operation_id = ? ORDER BY installation_id`, operationID).Scan(ctx, &installations); err != nil {
		t.Fatalf("read tool holds for operation %s: %v", operationID, err)
	}
	if !sameUUIDsForTest(installations, expectedInstallations) {
		t.Fatalf("tool holds for operation %s = %v, want %v", operationID, installations, expectedInstallations)
	}
	var workIDs []uuid.UUID
	if err := fixture.database.NewRaw(`SELECT work_id FROM operation_source_work_hold WHERE operation_id = ? ORDER BY work_id`, operationID).Scan(ctx, &workIDs); err != nil {
		t.Fatalf("read work holds for operation %s: %v", operationID, err)
	}
	if !sameUUIDsForTest(workIDs, expectedWork) {
		t.Fatalf("work holds for operation %s = %v, want %v", operationID, workIDs, expectedWork)
	}
}

func sameUUIDsForTest(left, right []uuid.UUID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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
