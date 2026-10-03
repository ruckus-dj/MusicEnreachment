//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// TestSourceAnalysisEnqueueCommitsAndRollsBackWithPostgreSQL proves the durable
// contract of an analysis start against real PostgreSQL and a real River job
// table: a successful enqueue stores the operation with both read holds and its
// job, and an enqueue whose operation insert fails takes the already inserted
// job with it, so no River job survives without the operation that owns it.
func TestSourceAnalysisEnqueueCommitsAndRollsBackWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)

	root := createInventoryRoot(t, ctx, inventory, "/srv/analysis-commit")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	installationID := insertAnalysisInstallation(t, ctx, database, "commit")
	setActiveAnalysisFFmpeg(t, ctx, database, installationID)

	committed := analysisEnqueueOperation(root, location, installationID, nil)
	if err := enqueueAnalysis(t, ctx, inventory, committed, client); err != nil {
		t.Fatalf("enqueue analysis: %v", err)
	}
	if committed.RiverJobID == nil {
		t.Fatal("committed analysis operation has no River job")
	}
	stored, err := persistence.NewSetupManagerRepository(database).GetOperation(ctx, committed.ID)
	if err != nil {
		t.Fatalf("read the committed operation: %v", err)
	}
	if stored.Kind != "analyze_source" || stored.State != "queued" || stored.Stage != service.SourceAnalysisStageQueued || stored.Attempt != 1 {
		t.Fatalf("committed operation = %+v, want a first-attempt queued analysis", stored)
	}
	if stored.TargetSourceRootID == nil || *stored.TargetSourceRootID != root.ID ||
		stored.TargetSourceLocationID == nil || *stored.TargetSourceLocationID != location.ID {
		t.Fatalf("committed operation targets = %+v, want the analyzed location", stored)
	}
	if stored.TargetInstallationID != nil {
		t.Fatalf("analysis operation target_installation_id = %v, want NULL", stored.TargetInstallationID)
	}
	if stored.AnalysisInstallationID == nil || *stored.AnalysisInstallationID != installationID {
		t.Fatalf("analysis installation hold = %v, want %s", stored.AnalysisInstallationID, installationID)
	}
	if stored.AnalysisMediaVariantID != nil {
		t.Fatalf("analysis variant hold = %v, want NULL for a never-analyzed location", stored.AnalysisMediaVariantID)
	}
	if !reflect.DeepEqual(mustDecodeSnapshot(t, stored.InputSnapshot), mustDecodeSnapshot(t, committed.InputSnapshot)) {
		t.Fatalf("stored snapshot = %s, want %s", stored.InputSnapshot, committed.InputSnapshot)
	}
	kind, args := readAnalysisJob(t, ctx, database, *stored.RiverJobID)
	if kind != service.SourceAnalysisJobKind {
		t.Fatalf("stored River job kind = %q, want %q", kind, service.SourceAnalysisJobKind)
	}
	if !reflect.DeepEqual(args, map[string]any{"operation_id": committed.ID.String()}) {
		t.Fatalf("stored River args = %v, want the operation id alone", args)
	}
	if countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM media_variant") != 0 {
		t.Fatal("an analysis start wrote a media variant")
	}

	// A second enqueue on another root reuses the committed operation id, so its
	// River job is inserted and its operation insert then fails on the primary
	// key. The job must roll back with the operation.
	otherRoot := createInventoryRoot(t, ctx, inventory, "/srv/analysis-rollback")
	otherLocation := insertAnalysisLocation(t, ctx, database, otherRoot.ID, "album/other.flac", 4096, probeMtime())
	establishInventory(t, ctx, database, otherRoot)
	collision := analysisEnqueueOperation(otherRoot, otherLocation, installationID, nil)
	collision.ID = committed.ID
	requirePostgresError(t, enqueueAnalysis(t, ctx, inventory, collision, client), "23505", "operation_pkey")
	if operations := countScanEnqueueRows(t, ctx, database,
		"SELECT count(*) FROM operation WHERE kind = 'analyze_source'"); operations != 1 {
		t.Fatalf("analysis operations after the rollback = %d, want the committed one alone", operations)
	}
	if jobs := analysisJobs(t, ctx, database); jobs != 1 {
		t.Fatalf("analysis River jobs after the rollback = %d, want no orphan job", jobs)
	}
}

// TestSourceAnalysisEnqueueSharesInstallationAcrossRootsWithPostgreSQL proves a
// read hold is not a mutation target: two roots may analyze with the same FFmpeg
// installation at once, and while they do, the installation cannot be deleted or
// moved, yet activating another ready version stays permitted and cannot change
// the immutable snapshot of either queued analysis.
func TestSourceAnalysisEnqueueSharesInstallationAcrossRootsWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	setup := persistence.NewSetupManagerRepository(database)
	if err := persistence.NewSettingsRepository(database).Set(ctx, "tools_directory", "/srv/tools-before"); err != nil {
		t.Fatal(err)
	}
	client := openScanEnqueueRiver(t, database)

	shared := insertAnalysisInstallation(t, ctx, database, "shared")
	setActiveAnalysisFFmpeg(t, ctx, database, shared)
	roots := make([]*persistence.SourceRoot, 0, 2)
	for _, path := range []string{"/srv/analysis-root-a", "/srv/analysis-root-b"} {
		root := createInventoryRoot(t, ctx, inventory, path)
		location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
		establishInventory(t, ctx, database, root)
		operation := analysisEnqueueOperation(root, location, shared, nil)
		if err := enqueueAnalysis(t, ctx, inventory, operation, client); err != nil {
			t.Fatalf("enqueue analysis of %s: %v", path, err)
		}
		roots = append(roots, root)
	}
	if operations := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE kind = 'analyze_source'"); operations != 2 {
		t.Fatalf("analysis operations = %d, want both roots accepted with one installation", operations)
	}
	if jobs := analysisJobs(t, ctx, database); jobs != 2 {
		t.Fatalf("analysis River jobs = %d, want 2", jobs)
	}

	move := &persistence.Operation{
		ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued",
		InputSnapshot: json.RawMessage(`{"schema_version":1,"old_root":"/srv/tools-before","new_root":"/srv/tools-after"}`),
	}
	if err := setup.CreateToolsMoveOperationAndEnqueue(ctx, move, client,
		serviceOperationArgs{OperationID: move.ID}, nil); !errors.Is(err, persistence.ErrToolsInstallationHeldByAnalysis) {
		t.Fatalf("tools move with a held installation = %v, want ErrToolsInstallationHeldByAnalysis", err)
	}

	replacement := insertAnalysisInstallation(t, ctx, database, "replacement")
	if err := setup.ActivateInstallation(ctx, replacement, "ffmpeg", analysisTestGOOS, analysisTestGOARCH, settings.ActiveFFmpegInstallationKey); err != nil {
		t.Fatalf("activate another ready ffmpeg version during active analyses: %v", err)
	}
	// The shared installation is no longer active, so only the analyses' read
	// holds can prevent its deletion.
	removed := false
	err := setup.DeleteInstallation(ctx, shared, "ffmpeg", analysisTestGOOS, analysisTestGOARCH, settings.ActiveFFmpegInstallationKey,
		func(*persistence.ToolInstallation, string) error { removed = true; return nil })
	if err == nil || removed {
		t.Fatalf("delete of a non-active held installation = %v, removed files = %v; want a refusal before file removal", err, removed)
	}
	for _, root := range roots {
		operations := make([]persistence.Operation, 0, 1)
		if err := database.NewSelect().Model(&operations).Where("target_source_root_id = ?", root.ID).Scan(ctx); err != nil {
			t.Fatalf("read the queued analyses of %s: %v", root.ConfiguredPath, err)
		}
		if len(operations) != 1 {
			t.Fatalf("analyses of %s = %d, want 1", root.ConfiguredPath, len(operations))
		}
		snapshot := mustDecodeSnapshot(t, operations[0].InputSnapshot)
		if snapshot.AnalysisInstallationID != shared {
			t.Fatalf("snapshot installation of %s = %s, want the pinned %s", root.ConfiguredPath, snapshot.AnalysisInstallationID, shared)
		}
	}
}

// analysisTestGOOS/analysisTestGOARCH is the platform the persistence analysis
// fixtures install, so the enqueue's platform check is exercised without
// depending on the host.
const (
	analysisTestGOOS   = "darwin"
	analysisTestGOARCH = "arm64"
)

// setActiveAnalysisFFmpeg records the installation the active FFmpeg setting
// names, which is what an analysis start fences its snapshot against under the
// activation lock.
func setActiveAnalysisFFmpeg(t *testing.T, ctx context.Context, database *bun.DB, installationID uuid.UUID) {
	t.Helper()
	if _, err := database.NewInsert().Model(&persistence.AppSetting{
		Name: settings.ActiveFFmpegInstallationKey, Value: installationID.String(),
	}).On("CONFLICT (setting_name) DO UPDATE").
		Set("setting_value = EXCLUDED.setting_value").Set("updated_at = now()").Exec(ctx); err != nil {
		t.Fatalf("record the active ffmpeg installation: %v", err)
	}
}

// enqueueAnalysis drives the analysis enqueue with the fixture platform and the
// active FFmpeg setting name, so tests do not repeat the plumbing.
func enqueueAnalysis(t *testing.T, ctx context.Context, inventory *persistence.SourceInventoryRepository, operation *persistence.Operation, client persistence.RiverInserter) error {
	t.Helper()
	return inventory.CreateSourceAnalysisOperationAndEnqueue(ctx, operation, analysisTestGOOS, analysisTestGOARCH, settings.ActiveFFmpegInstallationKey, client, service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil)
}

func analysisEnqueueOperation(root *persistence.SourceRoot, location persistence.SourceLocation, installationID uuid.UUID, previous *uuid.UUID) *persistence.Operation {
	raw, err := json.Marshal(persistence.SourceAnalysisSnapshot{
		SchemaVersion:          persistence.SourceAnalysisSnapshotVersion,
		SourceRootID:           root.ID,
		SourceLocationID:       location.ID,
		ConfiguredPath:         root.ConfiguredPath,
		InventoryPath:          *root.InventoryPath,
		RelativePath:           location.RelativePath,
		SizeBytes:              location.SizeBytes,
		Mtime:                  location.Mtime,
		PreviousVariantID:      previous,
		AnalysisPolicyVersion:  persistence.SourceAnalysisPolicyVersion,
		AnalysisInstallationID: installationID,
	})
	if err != nil {
		panic(err)
	}
	return &persistence.Operation{
		ID: uuid.New(), Kind: "analyze_source", State: "queued", Stage: "queued", Attempt: 1,
		InputSnapshot:          raw,
		TargetSourceRootID:     &root.ID,
		TargetSourceLocationID: &location.ID,
		AnalysisInstallationID: &installationID,
		AnalysisMediaVariantID: previous,
	}
}

func analysisJobs(t *testing.T, ctx context.Context, database *bun.DB) int {
	t.Helper()
	return countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceAnalysisJobKind)
}

func mustDecodeSnapshot(t *testing.T, raw []byte) persistence.SourceAnalysisSnapshot {
	t.Helper()
	snapshot, err := persistence.DecodeSourceAnalysisSnapshot(raw)
	if err != nil {
		t.Fatalf("decode source analysis snapshot %s: %v", raw, err)
	}
	return snapshot
}

func readAnalysisJob(t *testing.T, ctx context.Context, database *bun.DB, id int64) (string, map[string]any) {
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
