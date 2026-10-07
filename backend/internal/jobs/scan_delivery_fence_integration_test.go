//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestSourceScanDeliveryFencePreservesCurrentRetryPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	manager := persistence.NewSetupManagerRepository(database)
	inventory := persistence.NewSourceInventoryRepository(database)
	settingStore := persistence.NewSettingsRepository(database)
	toolsRoot := t.TempDir()
	setRuntimeRoots(t, ctx, settingStore, toolsRoot)
	writeScanDispatchFFmpeg(t, ctx, database, settingStore, toolsRoot)
	root := &persistence.SourceRoot{ID: uuid.New(), ConfiguredPath: t.TempDir(), DisplayName: "Music", Enabled: true}
	if err := inventory.CreateSourceRoot(ctx, root); err != nil {
		t.Fatal(err)
	}
	shaEnabled := true
	snapshot := service.ScanSourceSnapshot{SchemaVersion: service.SourceScanSnapshotVersion, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, ScanGeneration: 0, SHA256Enabled: &shaEnabled,
		Tools: []persistence.SourceAnalysisToolSelection{}}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "queued",
		Stage: service.SourceScanStageQueued, InputSnapshot: raw, TargetSourceRootID: &root.ID, Attempt: 2,
		RiverJobID: int64Pointer(202), ToolsReadRequired: true}
	if err := manager.CreateOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	installation, err := manager.GetInstallation(ctx, mustActiveFFmpegID(t, ctx, settingStore))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.NewRaw("INSERT INTO operation_tool_read_hold (operation_id, installation_id) VALUES (?, ?)", operation.ID, installation.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ReplaceSourceScanCandidates(ctx, operation.ID, []persistence.SourceScanCandidateInput{{
		RelativePath: "new-attempt.flac", SizeBytes: 17, Mtime: time.Now().UTC(), ProbeStatus: persistence.SourceProbeStatusAudio,
	}}); err != nil {
		t.Fatal(err)
	}
	// A's failure arrives after the durable operation has moved to attempt/job B.
	worker := NewSourceScanWorker(scanDispatchRepository{manager, inventory}, nil, nil, nil, settings.PlatformState{}, nil)
	oldDelivery := &river.Job[service.ScanSourceJobArgs]{
		JobRow: &rivertype.JobRow{ID: 101}, Args: service.ScanSourceJobArgs{OperationID: operation.ID},
	}
	err = worker.Work(ctx, oldDelivery)
	if !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("old River delivery Work error = %v, want ErrSourceAnalysisStale", err)
	}
	err = inventory.FinishSourceScanDelivery(ctx, operation.ID, 1, 101, "failed", "traversing", "safe failure")
	if !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("stale delivery finish error = %v, want ErrSourceAnalysisStale", err)
	}
	var current persistence.Operation
	if err := database.NewRaw("SELECT * FROM operation WHERE id = ?", operation.ID).Scan(ctx, &current); err != nil {
		t.Fatal(err)
	}
	if current.State != "queued" || current.Attempt != 2 || current.RiverJobID == nil || *current.RiverJobID != 202 || !current.ToolsReadRequired {
		t.Fatalf("current attempt was changed by stale finish: %+v", current)
	}
	requireScanDispatchCandidates(t, ctx, database, operation.ID, 1)
	var holds int
	if err := database.NewRaw("SELECT count(*) FROM operation_tool_read_hold WHERE operation_id = ?", operation.ID).Scan(ctx, &holds); err != nil || holds != 1 {
		t.Fatalf("tool holds = %d, read error = %v; want the current hold intact", holds, err)
	}
}

func TestHistoricalSourceScanRetryUsesCurrentGenerationPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	databaseURL := testpostgres.URL(t, database)
	ctx := context.Background()
	manager := persistence.NewSetupManagerRepository(database)
	inventory := persistence.NewSourceInventoryRepository(database)
	settingsRepository := persistence.NewSettingsRepository(database)
	toolsRoot := t.TempDir()
	setRuntimeRoots(t, ctx, settingsRepository, toolsRoot)
	registry := settings.New(settingsRepository, nil)
	platform := settings.PlatformState{Platform: settings.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}}
	writeScanDispatchFFmpeg(t, ctx, database, settingsRepository, toolsRoot)
	root := &persistence.SourceRoot{ID: uuid.New(), ConfiguredPath: t.TempDir(), DisplayName: "Music", Enabled: true}
	if err := inventory.CreateSourceRoot(ctx, root); err != nil {
		t.Fatal(err)
	}
	selection := persistence.SourceAnalysisToolSelection{InstallationID: uuid.New(), PackageKind: "ffmpeg", RelativePath: "ffmpeg/1.6.1", Executable: "ffprobe", Version: "1.6.1", VersionBanner: "ffprobe version 1.6.1"}
	shaEnabled := true
	// A failed historical attempt pins generation 1 and the approved SHA/tools.
	if _, err := database.NewRaw("UPDATE source_root SET scan_generation = 1 WHERE id = ?", root.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := service.ScanSourceSnapshot{SchemaVersion: service.SourceScanSnapshotVersion, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, ScanGeneration: 1, SHA256Enabled: &shaEnabled, Tools: []persistence.SourceAnalysisToolSelection{selection}}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Now().UTC()
	safeFailure := "safe scan failure"
	operation := &persistence.Operation{ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "failed", Stage: "traversing",
		InputSnapshot: raw, TargetSourceRootID: &root.ID, Attempt: 1, FinishedAt: &finished, SafeError: &safeFailure}
	if err := manager.CreateOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	// A later successful scan B publishes the same path and advances the root to
	// generation 2 before historical A is retried.
	b := &persistence.Operation{ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "running", Stage: "traversing",
		InputSnapshot: raw, TargetSourceRootID: &root.ID, Attempt: 1, RiverJobID: int64Pointer(303)}
	if err := manager.CreateOperation(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ReplaceSourceScanCandidates(ctx, b.ID, []persistence.SourceScanCandidateInput{{
		RelativePath: "current.flac", SizeBytes: 22, Mtime: time.Now().UTC(), ProbeStatus: persistence.SourceProbeStatusAudio,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{OperationID: b.ID, ExpectedAttempt: 1, ExpectedJobID: 303,
		ExpectedConfiguredPath: root.ConfiguredPath}); err != nil {
		t.Fatalf("apply successful scan B: %v", err)
	}
	if err := inventory.FinishSourceScanDelivery(ctx, b.ID, 1, 303, "succeeded", "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	worker := NewSourceScanWorker(scanDispatchRepository{manager, inventory}, service.NewOperations(manager), nil, registry, platform, nil)
	client, listenerPool := startScanDispatchRiver(t, databaseURL, database, worker)
	defer listenerPool.Close()
	defer stopRiverClient(t, client)
	retried, err := manager.RetrySourceScanOperationAndEnqueue(ctx, operation.ID, client, service.ScanSourceJobArgs{OperationID: operation.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var refreshed service.ScanSourceSnapshot
	if err := json.Unmarshal(retried.InputSnapshot, &refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.ScanGeneration != 2 || refreshed.SHA256Enabled == nil || !*refreshed.SHA256Enabled || len(refreshed.Tools) != 1 || refreshed.Tools[0] != selection {
		t.Fatalf("refreshed retry snapshot = %+v, want generation 2 and unchanged approved SHA/tools", refreshed)
	}
	if retried.Attempt != 2 || retried.RiverJobID == nil || retried.State != "queued" {
		t.Fatalf("retry record = %+v, want queued attempt 2 with its new River job", retried)
	}
}

func mustActiveFFmpegID(t *testing.T, ctx context.Context, repository *persistence.SettingsRepository) uuid.UUID {
	t.Helper()
	value, ok, err := repository.Get(ctx, settings.ActiveFFmpegInstallationKey)
	if err != nil || !ok {
		t.Fatalf("read active ffmpeg installation: present=%v err=%v", ok, err)
	}
	id, err := uuid.Parse(value)
	if err != nil {
		t.Fatalf("parse active ffmpeg installation: %v", err)
	}
	return id
}
