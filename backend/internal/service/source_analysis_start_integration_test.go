//go:build integration

package service_test

import (
	"context"
	"encoding/json"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type sourceAnalysisStartIntegration struct {
	starter   *service.SourceAnalysisOperations
	inventory *persistence.SourceInventoryRepository
	setup     *persistence.SetupManagerRepository
	registry  *settings.Registry
	database  *bun.DB
	platform  settings.PlatformState
	root      *persistence.SourceRoot
	location  persistence.SourceLocation
	work      *persistence.SourceAnalysisWork
}

// newSourceAnalysisStartIntegration builds a current-work fixture with one
// failed, tool-free SHA step. A retry therefore exercises normalized admission
// without pretending that a legacy whole-analysis snapshot still exists.
func newSourceAnalysisStartIntegration(t *testing.T, completeSetup bool) sourceAnalysisStartIntegration {
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
	failed := "previous digest failure"
	work := &persistence.SourceAnalysisWork{
		ID: uuid.New(), LocationID: location.ID, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, InventoryPath: *root.InventoryPath,
		RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime,
		SHA256Enabled: true, OriginScanOperationID: uuid.New(),
	}
	if err := inventory.StoreSourceAnalysisWork(ctx, work, []persistence.SourceAnalysisStepInput{{
		Step: persistence.SourceStepSHA256, State: "failed", SafeError: &failed,
	}}); err != nil {
		t.Fatalf("store current analysis work: %v", err)
	}
	return sourceAnalysisStartIntegration{
		starter:   service.NewSourceAnalysisOperations(persistence.NewSourceAnalysisStartStore(database), registry, registry, platform, client),
		inventory: inventory, setup: setup, registry: registry, database: database,
		platform: platform, root: root, location: location, work: work,
	}
}

func TestSourceAnalysisRetryStepEnqueuesNormalizedWorkWithPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newSourceAnalysisStartIntegration(t, true)
	ctx := context.Background()
	operation, err := fixture.starter.RetryStep(ctx, service.SourceAnalysisStepRequest{
		RootID: fixture.root.ID, LocationID: fixture.location.ID, Step: persistence.SourceStepSHA256,
		ExpectedSizeBytes: fixture.location.SizeBytes, ExpectedMtime: fixture.location.Mtime,
	})
	if err != nil {
		t.Fatalf("retry the failed SHA step: %v", err)
	}
	if operation.State != "queued" || operation.SourceAnalysisMode != persistence.SourceAnalysisModeSingleStep ||
		operation.TargetWorkID == nil || *operation.TargetWorkID != fixture.work.ID || operation.TargetStep == nil ||
		*operation.TargetStep != string(persistence.SourceStepSHA256) || operation.ToolsReadRequired || operation.RiverJobID == nil {
		t.Fatalf("retry operation = %+v, want a queued tool-free SHA step", operation)
	}
	stored, err := fixture.setup.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the stored operation: %v", err)
	}
	if stored.Attempt != 1 || stored.SourceAnalysisMode != persistence.SourceAnalysisModeSingleStep || stored.ToolsReadRequired {
		t.Fatalf("stored operation = %+v, want normalized step admission", stored)
	}
	snapshot, err := persistence.DecodeSourceAnalysisOperationSnapshot(stored.InputSnapshot)
	if err != nil {
		t.Fatalf("decode normalized snapshot: %v", err)
	}
	want := persistence.SourceAnalysisOperationSnapshot{
		SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion,
		Mode:          persistence.SourceAnalysisModeSingleStep, WorkIDs: []uuid.UUID{fixture.work.ID},
		TargetWorkID: &fixture.work.ID, TargetStep: stored.TargetStep,
		RerunTarget: boolPointer(false),
	}
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatalf("stored snapshot = %+v, want %+v", snapshot, want)
	}
	var queued int
	if err := fixture.database.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE work_id=? AND step='sha256' AND state='queued' AND execution_operation_id=?`, fixture.work.ID, operation.ID).Scan(ctx, &queued); err != nil {
		t.Fatalf("read claimed step: %v", err)
	}
	if queued != 1 {
		t.Fatalf("queued target step count = %d, want 1", queued)
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

func boolPointer(value bool) *bool { return &value }

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
