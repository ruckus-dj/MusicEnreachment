//go:build integration

package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// scanDispatchRepository is the production pair of repositories a scan worker
// reads from: the operation and installation records and the source inventory.
type scanDispatchRepository struct {
	*persistence.SetupManagerRepository
	*persistence.SourceInventoryRepository
}

type scanDispatchLocation struct {
	ID           uuid.UUID `bun:"id,type:uuid"`
	RelativePath string    `bun:"relative_path"`
	ProbeStatus  string    `bun:"probe_status"`
	SafeError    *string   `bun:"safe_error"`
}

// TestSourceScanWorkerRiverDispatchPostgreSQL drives the scan worker through the
// real River dispatcher and real PostgreSQL: a delivery scans, applies and
// succeeds; a duplicate delivery does not walk the tree again; a traversal that
// fails, a missing managed ffprobe and a disabled root each fail the operation
// with a safe reason while the previous inventory stays exactly as it was.
func TestSourceScanWorkerRiverDispatchPostgreSQL(t *testing.T) {
	database, databaseURL := openDispatchDatabase(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	settingsRepository := persistence.NewSettingsRepository(database)
	toolsRoot := t.TempDir()
	// The worker reloads the managed tools itself, so the active ffmpeg
	// installation of this platform has to exist before the first delivery.
	setRuntimeRoots(t, ctx, settingsRepository, toolsRoot)
	registry := settings.New(settingsRepository, nil)
	platform := settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}
	writeScanDispatchFFmpeg(t, ctx, database, settingsRepository, toolsRoot)

	setupManager := persistence.NewSetupManagerRepository(database)
	inventory := persistence.NewSourceInventoryRepository(database)
	roots := service.NewSourceRoots(inventory, registry)
	source := t.TempDir()
	writeScanDispatchFile(t, filepath.Join(source, "album", "track.flac"), "audio bytes")
	writeScanDispatchFile(t, filepath.Join(source, "album", "silent.mka"), "video only bytes")
	writeScanDispatchFile(t, filepath.Join(source, "album", "broken.wav"), "unreadable bytes")
	root, err := roots.Create(ctx, "Music", source)
	if err != nil {
		t.Fatalf("create the source root: %v", err)
	}

	operations := service.NewOperations(setupManager)
	worker := NewSourceScanWorker(scanDispatchRepository{setupManager, inventory}, operations, roots, registry, platform, tools.NewLifecycle(nil))
	riverClient, listenerPool := startScanDispatchRiver(t, databaseURL, database, worker)
	defer listenerPool.Close()
	defer stopRiverClient(t, riverClient)
	events, cancelEvents := riverClient.Subscribe(river.EventKindJobCompleted)
	defer cancelEvents()
	probes := filepath.Join(toolsRoot, "probe-log")

	// Given a registered root of three approved files whose managed ffprobe
	// confirms one, finds no audio in another and fails on the third...
	scans := service.NewSourceScanOperations(inventory, roots, registry, platform, riverClient)
	first, err := scans.Start(ctx, root.ID)
	if err != nil {
		t.Fatalf("start the first scan: %v", err)
	}
	awaitRiverCompletion(t, ctx, events, *first.RiverJobID)

	// Then the delivery applied the snapshot and succeeded.
	assertOperationStage(t, ctx, setupManager, first.ID, "succeeded", "succeeded")
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 1)
	locations := readScanDispatchLocations(t, ctx, database, root.ID)
	requireScanDispatchStatus(t, locations, "album/broken.wav", persistence.SourceProbeStatusProbeError)
	requireScanDispatchStatus(t, locations, "album/silent.mka", persistence.SourceProbeStatusNoAudio)
	requireScanDispatchStatus(t, locations, "album/track.flac", persistence.SourceProbeStatusAudio)
	if locations["album/broken.wav"].SafeError == nil || *locations["album/broken.wav"].SafeError == "" {
		t.Fatal("a probe_error location carries no safe reason")
	}
	requireScanDispatchCandidates(t, ctx, database, first.ID, 0)
	if count := scanDispatchProbeCount(t, probes); count != 3 {
		t.Fatalf("probes after the first scan = %d, want one per approved file", count)
	}

	// Given the same job delivered a second time...
	duplicate := deliverScanDispatchJob(t, ctx, database, riverClient, first.ID)
	awaitRiverCompletion(t, ctx, events, duplicate)

	// Then the repeated delivery is a no-op and the tree is not walked again.
	assertOperationStage(t, ctx, setupManager, first.ID, "succeeded", "succeeded")
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 1)
	if count := scanDispatchProbeCount(t, probes); count != 3 {
		t.Fatalf("probes after the duplicate delivery = %d, want no second traversal", count)
	}

	// Given a subtree the traversal cannot read...
	if err := os.MkdirAll(filepath.Join(source, "locked"), 0o755); err != nil {
		t.Fatalf("create the locked directory: %v", err)
	}
	writeScanDispatchFile(t, filepath.Join(source, "locked", "notes.txt"), "not audio")
	if err := os.Chmod(filepath.Join(source, "locked"), 0o000); err != nil {
		t.Fatalf("lock the source subtree: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(source, "locked"), 0o755) })
	second, err := scans.Start(ctx, root.ID)
	if err != nil {
		t.Fatalf("start the failing scan: %v", err)
	}
	awaitRiverCompletion(t, ctx, events, *second.RiverJobID)

	// Then the scan fails with a safe reason before any apply, and the previous
	// generation and its locations are exactly what the first scan left.
	assertOperationStage(t, ctx, setupManager, second.ID, "failed", service.SourceScanStageTraversing)
	requireScanDispatchSafeError(t, readScanDispatchOperation(t, ctx, setupManager, second.ID), scanSafeTraversal)
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 1)
	requireScanDispatchLocations(t, readScanDispatchLocations(t, ctx, database, root.ID), locations)
	requireScanDispatchCandidates(t, ctx, database, second.ID, 0)

	// Given the subtree is readable again...
	beforeRecovery := scanDispatchProbeCount(t, probes)
	if err := os.Chmod(filepath.Join(source, "locked"), 0o755); err != nil {
		t.Fatalf("unlock the source subtree: %v", err)
	}
	third, err := scans.Start(ctx, root.ID)
	if err != nil {
		t.Fatalf("start the recovery scan: %v", err)
	}
	awaitRiverCompletion(t, ctx, events, *third.RiverJobID)

	// Then only the file whose probe failed is probed again: the unchanged audio
	// and no_audio locations keep their stored status. The failed traversal may
	// itself have probed before it failed, so the recovery scan is measured on its
	// own.
	assertOperationStage(t, ctx, setupManager, third.ID, "succeeded", "succeeded")
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 2)
	requireScanDispatchLocations(t, readScanDispatchLocations(t, ctx, database, root.ID), locations)
	if delta := scanDispatchProbeCount(t, probes) - beforeRecovery; delta != 1 {
		t.Fatalf("probes during the recovery scan = %d, want the probe_error file rechecked alone", delta)
	}

	// Given the managed ffprobe is gone...
	beforeToolFailure := scanDispatchProbeCount(t, probes)
	if err := os.Remove(filepath.Join(toolsRoot, "ffmpeg", "1.6.1", "ffprobe")); err != nil {
		t.Fatalf("remove the managed ffprobe: %v", err)
	}
	fourth, err := scans.Start(ctx, root.ID)
	if err != nil {
		t.Fatalf("start the scan without a managed ffprobe: %v", err)
	}
	awaitRiverCompletion(t, ctx, events, *fourth.RiverJobID)

	// Then the whole scan fails once, with the tool reason, instead of turning
	// every traversed file into a probe_error.
	assertOperationStage(t, ctx, setupManager, fourth.ID, "failed", service.SourceScanStageQueued)
	requireScanDispatchSafeError(t, readScanDispatchOperation(t, ctx, setupManager, fourth.ID), scanSafeTool)
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 2)
	requireScanDispatchLocations(t, readScanDispatchLocations(t, ctx, database, root.ID), locations)
	requireScanDispatchCandidates(t, ctx, database, fourth.ID, 0)
	if delta := scanDispatchProbeCount(t, probes) - beforeToolFailure; delta != 0 {
		t.Fatalf("probes during the tool failure = %d, want none without a working tool", delta)
	}

	// Given a root the operator disabled while a scan of it was already queued...
	disabled := false
	if _, err := roots.Edit(ctx, root.ID, service.SourceRootEdit{Enabled: &disabled}); err != nil {
		t.Fatalf("disable the source root: %v", err)
	}
	queued := createScanDispatchOperation(t, ctx, setupManager, root.ID, root.ConfiguredPath)
	awaitRiverCompletion(t, ctx, events, deliverScanDispatchJob(t, ctx, database, riverClient, queued.ID))

	// Then the worker re-checks the flag it reloaded and refuses the scan, and the
	// inventory of the disabled root stays visible.
	assertOperationStage(t, ctx, setupManager, queued.ID, "failed", service.SourceScanStageQueued)
	requireScanDispatchSafeError(t, readScanDispatchOperation(t, ctx, setupManager, queued.ID), scanSafeDisabled)
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 2)
	requireScanDispatchLocations(t, readScanDispatchLocations(t, ctx, database, root.ID), locations)
}

// writeScanDispatchFFmpeg materializes a fake managed ffmpeg whose ffprobe
// answers by file name: a file whose path contains "silent" has no audio, a path
// containing "broken" fails, and every other file carries audio. Each probe is
// appended to probe-log, so a test observes exactly how often the tree was
// walked.
func writeScanDispatchFFmpeg(t *testing.T, ctx context.Context, database *bun.DB, repository *persistence.SettingsRepository, toolsRoot string) {
	t.Helper()
	directory := filepath.Join(toolsRoot, "ffmpeg", "1.6.1")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create the managed ffmpeg directory: %v", err)
	}
	script := `#!/bin/sh
if [ "$1" = "-version" ]; then
  echo "$(basename "$0") version 1.6.1"
  exit 0
fi
echo probe >> ` + filepath.Join(toolsRoot, "probe-log") + `
case "$*" in
  *silent*) printf '{"streams":[{"codec_type":"video"}]}' ;;
  *broken*) printf 'the probe failed\n' >&2; exit 1 ;;
  *) printf '{"streams":[{"codec_type":"audio"}]}' ;;
esac
`
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(script), 0o755); err != nil {
			t.Fatalf("write the managed %s: %v", name, err)
		}
	}
	versions, err := json.Marshal(map[string]string{"ffmpeg": "ffmpeg version 1.6.1", "ffprobe": "ffprobe version 1.6.1"})
	if err != nil {
		t.Fatal(err)
	}
	verifiedAt := time.Now().UTC()
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: string(tools.PackageFFmpeg), PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "btbn", ReleaseIdentity: "1.6.1", RelativePath: filepath.Join("ffmpeg", "1.6.1"),
		State: "ready", ExecutableVersions: versions, VerifiedAt: &verifiedAt,
	}
	if err := persistence.NewSetupManagerRepository(database).CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create the managed ffmpeg installation: %v", err)
	}
	if err := repository.Set(ctx, settings.ActiveFFmpegInstallationKey, installation.ID.String()); err != nil {
		t.Fatalf("activate the managed ffmpeg installation: %v", err)
	}
}

func writeScanDispatchFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func startScanDispatchRiver(t *testing.T, databaseURL string, database *bun.DB, worker *SourceScanWorker) (*river.Client[*sql.Tx], *pgxpool.Pool) {
	t.Helper()
	client, listenerPool, err := StartWithWorkers(context.Background(), databaseURL, database.DB, func(workers *river.Workers) {
		river.AddWorker(workers, worker)
	})
	if err != nil {
		t.Fatalf("start River with the scan worker: %v", err)
	}
	return client, listenerPool
}

// deliverScanDispatchJob inserts the River job of an operation in its own
// transaction, which is how a duplicate delivery or an operation created outside
// the start service reaches the worker.
func deliverScanDispatchJob(t *testing.T, ctx context.Context, database *bun.DB, client *river.Client[*sql.Tx], operationID uuid.UUID) int64 {
	t.Helper()
	tx, err := database.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin scan delivery transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	inserted, err := client.InsertTx(ctx, tx, service.ScanSourceJobArgs{OperationID: operationID}, nil)
	if err != nil {
		t.Fatalf("insert the scan delivery: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit the scan delivery: %v", err)
	}
	return inserted.Job.ID
}

func createScanDispatchOperation(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, rootID uuid.UUID, configuredPath string) *persistence.Operation {
	t.Helper()
	snapshot, err := json.Marshal(service.ScanSourceSnapshot{
		SchemaVersion: service.SourceScanSnapshotVersion, SourceRootID: rootID, ConfiguredPath: configuredPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "queued", Stage: service.SourceScanStageQueued,
		InputSnapshot: snapshot, TargetSourceRootID: &rootID,
	}
	if err := repository.CreateOperation(ctx, operation); err != nil {
		t.Fatalf("create the scan operation: %v", err)
	}
	return operation
}

func readScanDispatchOperation(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, id uuid.UUID) *persistence.Operation {
	t.Helper()
	operation, err := repository.GetOperation(ctx, id)
	if err != nil {
		t.Fatalf("read operation %s: %v", id, err)
	}
	return operation
}

func readScanDispatchLocations(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID) map[string]scanDispatchLocation {
	t.Helper()
	locations := make([]scanDispatchLocation, 0)
	if err := database.NewRaw(`SELECT id, relative_path, probe_status, safe_error
		FROM source_location WHERE source_root_id = ? ORDER BY relative_path`, rootID).Scan(ctx, &locations); err != nil {
		t.Fatalf("read the source locations: %v", err)
	}
	byPath := make(map[string]scanDispatchLocation, len(locations))
	for _, location := range locations {
		byPath[location.RelativePath] = location
	}
	return byPath
}

func requireScanDispatchLocations(t *testing.T, got, previous map[string]scanDispatchLocation) {
	t.Helper()
	if len(got) != len(previous) {
		t.Fatalf("source locations = %d, want the previous %d", len(got), len(previous))
	}
	for path, want := range previous {
		current, ok := got[path]
		if !ok || current.ID != want.ID || current.ProbeStatus != want.ProbeStatus {
			t.Fatalf("location %q = %#v, want the unchanged %#v", path, current, want)
		}
	}
}

func requireScanDispatchStatus(t *testing.T, locations map[string]scanDispatchLocation, relativePath, status string) {
	t.Helper()
	location, ok := locations[relativePath]
	if !ok || location.ProbeStatus != status {
		t.Fatalf("location %q = %#v, want status %q", relativePath, location, status)
	}
}

func requireScanDispatchGeneration(t *testing.T, ctx context.Context, inventory *persistence.SourceInventoryRepository, rootID uuid.UUID, generation int64) {
	t.Helper()
	root, err := inventory.GetSourceRoot(ctx, rootID)
	if err != nil {
		t.Fatalf("read the source root: %v", err)
	}
	if root.ScanGeneration != generation {
		t.Fatalf("scan generation = %d, want %d", root.ScanGeneration, generation)
	}
}

func requireScanDispatchSafeError(t *testing.T, operation *persistence.Operation, want string) {
	t.Helper()
	if operation.SafeError == nil || *operation.SafeError != want {
		t.Fatalf("operation %s safe error = %v, want %q", operation.ID, operation.SafeError, want)
	}
}

func requireScanDispatchCandidates(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID, want int) {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM source_scan_candidate WHERE operation_id = ?", operationID).Scan(ctx, &count); err != nil {
		t.Fatalf("count scan candidates: %v", err)
	}
	if count != want {
		t.Fatalf("scan candidates of %s = %d, want %d", operationID, count, want)
	}
}

func assertOperationStage(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, id uuid.UUID, wantState, wantStage string) {
	t.Helper()
	operation, err := repository.GetOperation(ctx, id)
	state, stage := operationStateStage(operation)
	if err != nil || state != wantState || stage != wantStage {
		t.Fatalf("operation %s = %s/%s, lookup error = %v; want %s/%s", id, state, stage, err, wantState, wantStage)
	}
}

// scanDispatchProbeCount counts the ffprobe invocations the fake managed tool
// recorded, so a test asserts the tree was not walked again where it must not be.
func scanDispatchProbeCount(t *testing.T, logPath string) int {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read the probe log: %v", err)
	}
	return strings.Count(string(data), "probe\n")
}

var _ scanWorkerRepository = scanDispatchRepository{}
