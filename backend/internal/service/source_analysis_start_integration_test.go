//go:build integration

package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type sourceAnalysisStartIntegration struct {
	starter        *service.SourceAnalysisOperations
	inventory      *persistence.SourceInventoryRepository
	setup          *persistence.SetupManagerRepository
	registry       *settings.Registry
	client         *river.Client[*sql.Tx]
	database       *bun.DB
	platform       settings.PlatformState
	root           *persistence.SourceRoot
	location       persistence.SourceLocation
	installationID uuid.UUID
}

// newSourceAnalysisStartIntegration builds one analysis-start fixture over real
// PostgreSQL and a real River job table: a non-stale enabled root with one audio
// location in its applied generation, and, unless disabled, one ready FFmpeg
// installation activated for this process platform.
func newSourceAnalysisStartIntegration(t *testing.T, completeSetup, activate bool) sourceAnalysisStartIntegration {
	t.Helper()
	ctx := context.Background()
	database := testpostgres.OpenMigrated(t)
	client := openSourceScanRiver(t, database)
	registry := settings.New(persistence.NewSettingsRepository(database), nil)
	if completeSetup {
		if err := registry.CompleteSetup(ctx); err != nil {
			t.Fatalf("complete setup: %v", err)
		}
	}
	inventory := persistence.NewSourceInventoryRepository(database)
	setup := persistence.NewSetupManagerRepository(database)
	platform := settings.PlatformState{Platform: settings.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}}

	root := &persistence.SourceRoot{ConfiguredPath: "/srv/analysis-start", DisplayName: "Music", Enabled: true}
	if err := inventory.CreateSourceRoot(ctx, root); err != nil {
		t.Fatalf("create the analysis root: %v", err)
	}
	location := persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: root.ID, RelativePath: "album/track.flac",
		SizeBytes: 2048, Mtime: time.Now().UTC().Truncate(time.Microsecond),
		LastSeenScanGeneration: 1, ProbeStatus: persistence.SourceProbeStatusAudio,
	}
	if _, err := database.NewInsert().Model(&location).Exec(ctx); err != nil {
		t.Fatalf("insert the analysis location: %v", err)
	}
	establishAnalysisStartInventory(t, ctx, database, root)

	installationID := uuid.Nil
	if activate {
		installationID = insertReadyAnalysisFFmpeg(t, ctx, database, platform)
		if err := setup.ActivateInstallation(ctx, installationID, "ffmpeg", platform.Platform.GOOS, platform.Platform.GOARCH, settings.ActiveFFmpegInstallationKey); err != nil {
			t.Fatalf("activate the analysis installation: %v", err)
		}
	}
	return sourceAnalysisStartIntegration{
		starter:   service.NewSourceAnalysisOperations(persistence.NewSourceAnalysisStartStore(database), registry, registry, platform, client),
		inventory: inventory, setup: setup, registry: registry, client: client, database: database,
		platform: platform, root: root, location: location, installationID: installationID,
	}
}

// TestSourceAnalysisStartEnqueuesSnapshotAndHoldsWithPostgreSQL proves a service
// start records the queued operation with its immutable snapshot, its River job
// and both read holds, and touches neither the inventory row nor the source.
func TestSourceAnalysisStartEnqueuesSnapshotAndHoldsWithPostgreSQL(t *testing.T) {
	fixture := newSourceAnalysisStartIntegration(t, true, true)
	ctx := context.Background()

	operation, err := fixture.starter.Start(ctx, service.SourceAnalysisStartRequest{
		RootID: fixture.root.ID, LocationID: fixture.location.ID,
		ExpectedSizeBytes: fixture.location.SizeBytes, ExpectedMtime: fixture.location.Mtime,
	})
	if err != nil {
		t.Fatalf("start an analysis: %v", err)
	}
	if operation.Kind != service.SourceAnalysisOperationKind || operation.State != "queued" || operation.Stage != service.SourceAnalysisStageQueued {
		t.Fatalf("returned operation = %+v, want a queued analysis", operation)
	}
	if operation.RiverJobID == nil {
		t.Fatal("started analysis has no River job")
	}
	stored, err := fixture.setup.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the stored operation: %v", err)
	}
	if stored.Attempt != 1 || stored.TargetInstallationID != nil {
		t.Fatalf("stored operation = %+v, want attempt 1 and no mutation target", stored)
	}
	if stored.AnalysisInstallationID == nil || *stored.AnalysisInstallationID != fixture.installationID {
		t.Fatalf("installation hold = %v, want %s", stored.AnalysisInstallationID, fixture.installationID)
	}
	if stored.AnalysisMediaVariantID != nil {
		t.Fatalf("variant hold = %v, want NULL for a never-analyzed location", stored.AnalysisMediaVariantID)
	}
	snapshot, err := persistence.DecodeSourceAnalysisSnapshot(stored.InputSnapshot)
	if err != nil {
		t.Fatalf("decode the stored snapshot: %v", err)
	}
	want := persistence.SourceAnalysisSnapshot{
		SchemaVersion: persistence.SourceAnalysisSnapshotVersion, SourceRootID: fixture.root.ID,
		SourceLocationID: fixture.location.ID, ConfiguredPath: fixture.root.ConfiguredPath,
		InventoryPath: *fixture.root.InventoryPath, RelativePath: fixture.location.RelativePath,
		SizeBytes: fixture.location.SizeBytes, Mtime: fixture.location.Mtime,
		AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion, AnalysisInstallationID: fixture.installationID,
	}
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatalf("stored snapshot = %+v, want %+v", snapshot, want)
	}
	jobKind, jobArgs := readAnalysisStartJob(t, ctx, fixture.database, *stored.RiverJobID)
	if jobKind != service.SourceAnalysisJobKind || !reflect.DeepEqual(jobArgs, map[string]any{"operation_id": operation.ID.String()}) {
		t.Fatalf("River job = %s %v, want the analysis kind and the operation id alone", jobKind, jobArgs)
	}
	storedLocation, err := fixture.inventory.GetSourceLocation(ctx, fixture.root.ID, fixture.location.ID)
	if err != nil {
		t.Fatalf("read the analyzed location: %v", err)
	}
	if !reflect.DeepEqual(storedLocation.Mtime.UTC(), fixture.location.Mtime.UTC()) || storedLocation.MediaVariantID != nil {
		t.Fatalf("location after the start = %+v, want it untouched", storedLocation)
	}
	if variants := countAnalysisStartRows(t, ctx, fixture.database, "SELECT count(*) FROM media_variant"); variants != 0 {
		t.Fatalf("media variants after the start = %d, want the start to write none", variants)
	}
}

// TestSourceAnalysisStartAcceptsTwoRootsSharingInstallationWithPostgreSQL proves
// the analysis hold is not a globally exclusive mutation target: two roots may
// queue an analysis with the same installation at the same time.
func TestSourceAnalysisStartAcceptsTwoRootsSharingInstallationWithPostgreSQL(t *testing.T) {
	fixture := newSourceAnalysisStartIntegration(t, true, true)
	ctx := context.Background()
	second := &persistence.SourceRoot{ConfiguredPath: "/srv/analysis-start-b", DisplayName: "Music B", Enabled: true}
	if err := fixture.inventory.CreateSourceRoot(ctx, second); err != nil {
		t.Fatalf("create the second root: %v", err)
	}
	location := persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: second.ID, RelativePath: "album/track.flac",
		SizeBytes: 4096, Mtime: time.Now().UTC().Truncate(time.Microsecond),
		LastSeenScanGeneration: 1, ProbeStatus: persistence.SourceProbeStatusAudio,
	}
	if _, err := fixture.database.NewInsert().Model(&location).Exec(ctx); err != nil {
		t.Fatalf("insert the second location: %v", err)
	}
	establishAnalysisStartInventory(t, ctx, fixture.database, second)

	first, err := fixture.starter.Start(ctx, service.SourceAnalysisStartRequest{
		RootID: fixture.root.ID, LocationID: fixture.location.ID,
		ExpectedSizeBytes: fixture.location.SizeBytes, ExpectedMtime: fixture.location.Mtime,
	})
	if err != nil {
		t.Fatalf("start the first analysis: %v", err)
	}
	other, err := fixture.starter.Start(ctx, service.SourceAnalysisStartRequest{
		RootID: second.ID, LocationID: location.ID,
		ExpectedSizeBytes: location.SizeBytes, ExpectedMtime: location.Mtime,
	})
	if err != nil {
		t.Fatalf("start the second analysis with the same installation: %v", err)
	}
	if first.ID == other.ID {
		t.Fatal("two roots share an analysis operation")
	}
	if operations := countAnalysisStartRows(t, ctx, fixture.database, "SELECT count(*) FROM operation WHERE kind = 'analyze_source'"); operations != 2 {
		t.Fatalf("analysis operations = %d, want both roots accepted", operations)
	}
	if jobs := countAnalysisStartRows(t, ctx, fixture.database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceAnalysisJobKind); jobs != 2 {
		t.Fatalf("analysis River jobs = %d, want 2", jobs)
	}
}

func establishAnalysisStartInventory(t *testing.T, ctx context.Context, database *bun.DB, root *persistence.SourceRoot) {
	t.Helper()
	if _, err := database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
		Set("scan_generation = 1").Set("inventory_path = ?", root.ConfiguredPath).
		Set("last_successful_scan_at = now()").Set("status = ?", persistence.SourceRootStatusAvailable).
		Where("id = ?", root.ID).Exec(ctx); err != nil {
		t.Fatalf("establish the inventory of %s: %v", root.ConfiguredPath, err)
	}
	root.ScanGeneration = 1
	inventoryPath := root.ConfiguredPath
	root.InventoryPath = &inventoryPath
}

func insertReadyAnalysisFFmpeg(t *testing.T, ctx context.Context, database *bun.DB, platform settings.PlatformState) uuid.UUID {
	t.Helper()
	now := time.Now().UTC()
	identity := strings.ReplaceAll(uuid.NewString(), "-", "")
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg",
		PlatformGOOS: platform.Platform.GOOS, PlatformGOARCH: platform.Platform.GOARCH,
		SourceName: "analysis-start-test", ReleaseIdentity: identity, RelativePath: "ffmpeg/" + identity,
		State: "ready", ExecutableVersions: json.RawMessage(`{}`),
		ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &now,
	}
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		t.Fatalf("insert a ready ffmpeg installation: %v", err)
	}
	return installation.ID
}

func readAnalysisStartJob(t *testing.T, ctx context.Context, database *bun.DB, id int64) (string, map[string]any) {
	t.Helper()
	var job struct {
		Kind string `bun:"kind"`
		Args string `bun:"args"`
	}
	if err := database.NewRaw("SELECT kind, args::text AS args FROM river_job WHERE id = ?", id).Scan(ctx, &job); err != nil {
		t.Fatalf("read River job %d: %v", id, err)
	}
	args := map[string]any{}
	if err := json.Unmarshal([]byte(job.Args), &args); err != nil {
		t.Fatalf("decode River args %s: %v", job.Args, err)
	}
	return job.Kind, args
}

func countAnalysisStartRows(t *testing.T, ctx context.Context, database *bun.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := database.NewRaw(query, args...).Scan(ctx, &count); err != nil {
		t.Fatalf("count rows (%s): %v", query, err)
	}
	return count
}
