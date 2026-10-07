//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

func TestSourceScanToolHoldsUseVerifiedSelectionsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	setup := persistence.NewSetupManagerRepository(database)
	client := openScanEnqueueRiver(t, database)

	toolsRoot, err := settings.NormalizePath(t.TempDir())
	if err != nil {
		t.Fatalf("normalize tools directory: %v", err)
	}
	if err := persistence.NewSettingsRepository(database).Set(ctx, "tools_directory", toolsRoot); err != nil {
		t.Fatalf("set tools directory: %v", err)
	}
	root := createInventoryRoot(t, ctx, inventory, filepath.Join(t.TempDir(), "music"))
	selections := make([]persistence.SourceAnalysisToolSelection, 0, 2)
	for _, tool := range []struct {
		kind, executable, banner string
	}{
		{kind: "ffmpeg", executable: "ffprobe", banner: "ffprobe version 7.1.2"},
		{kind: "fpcalc", executable: "fpcalc", banner: "fpcalc version 1.5.0"},
	} {
		selection := insertScanHoldInstallation(t, ctx, database, toolsRoot, tool.kind, tool.executable, tool.banner)
		selections = append(selections, selection)
	}

	snapshot, err := json.Marshal(struct {
		SchemaVersion  int                                       `json:"schema_version"`
		SourceRootID   uuid.UUID                                 `json:"source_root_id"`
		ConfiguredPath string                                    `json:"configured_path"`
		ScanGeneration int64                                     `json:"scan_generation"`
		SHA256Enabled  *bool                                     `json:"sha256_enabled"`
		Tools          []persistence.SourceAnalysisToolSelection `json:"tools"`
	}{persistence.SourceScanSnapshotVersion, root.ID, root.ConfiguredPath, root.ScanGeneration, boolPointer(true), selections})
	if err != nil {
		t.Fatalf("encode scan snapshot: %v", err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "scan_source", State: "queued", Stage: service.SourceScanStageQueued,
		InputSnapshot: snapshot, TargetSourceRootID: &root.ID,
	}
	if err := inventory.CreateSourceScanOperationAndEnqueue(ctx, operation, client,
		service.ScanSourceJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("enqueue queued scan with explicit SHA and tools: %v", err)
	}
	var delivery struct {
		Kind string `bun:"kind"`
		Args string `bun:"args"`
	}
	if err := database.NewRaw(`SELECT kind, args::text AS args FROM river_job WHERE id=?`, *operation.RiverJobID).Scan(ctx, &delivery); err != nil {
		t.Fatalf("read persisted River delivery: %v", err)
	}
	var delivered service.ScanSourceJobArgs
	if delivery.Kind != delivered.Kind() || json.Unmarshal([]byte(delivery.Args), &delivered) != nil || delivered.OperationID != operation.ID {
		t.Fatalf("persisted scan delivery = %#v, want the source-scan job with JSON arguments", delivery)
	}
	if _, err := database.NewRaw(`UPDATE operation SET state='running', stage=?, started_at=now() WHERE id=?`, service.SourceScanStageTraversing, operation.ID).Exec(ctx); err != nil {
		t.Fatalf("mark queued scan running: %v", err)
	}

	releases := make([]func() error, 0, len(selections))
	for _, selection := range selections {
		held, release, err := inventory.AcquireSourceScanToolHold(ctx, operation.ID, root.ID, root.ConfiguredPath,
			operation.Attempt, *operation.RiverJobID, selection)
		if err != nil {
			t.Fatalf("acquire verified %s scan hold: %v", selection.PackageKind, err)
		}
		if held.Installation.ID != selection.InstallationID || held.ExecutablePath == "" || held.ToolsRoot != toolsRoot {
			t.Fatalf("held verified metadata = %#v, does not match pinned selection %#v", held, selection)
		}
		if info, err := os.Stat(held.ExecutablePath); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("protected executable path %q stat = %v, want regular file", held.ExecutablePath, err)
		}
		releases = append(releases, release)
	}
	var activeFlag bool
	if err := database.NewRaw(`SELECT tools_read_required FROM operation WHERE id=?`, operation.ID).Scan(ctx, &activeFlag); err != nil {
		t.Fatal(err)
	}
	if !activeFlag {
		t.Fatal("active scan with tool holds has tools_read_required=false")
	}
	if _, err := database.NewRaw(`UPDATE operation SET tools_read_required=false WHERE id=?`, operation.ID).Exec(ctx); err == nil {
		t.Fatal("SQL guard accepted an active scan whose tool-read flag disagreed with its holds")
	}

	blockedMove := &persistence.Operation{
		ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued",
		InputSnapshot: json.RawMessage(`{"schema_version":1,"old_root":"` + toolsRoot + `","new_root":"` + filepath.Join(t.TempDir(), "tools-blocked") + `","files":[]}`),
	}
	if err := setup.CreateToolsMoveOperationAndEnqueue(ctx, blockedMove, client,
		serviceOperationArgs{OperationID: blockedMove.ID}, nil); !errors.Is(err, persistence.ErrToolsInstallationHeldByAnalysis) {
		t.Fatalf("tools move during active scan tool holds = %v, want ErrToolsInstallationHeldByAnalysis", err)
	}

	for _, release := range releases {
		if err := release(); err != nil {
			t.Fatalf("release scan tool hold: %v", err)
		}
	}
	var heldCount int
	if err := database.NewRaw(`SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`, operation.ID).Scan(ctx, &heldCount); err != nil {
		t.Fatal(err)
	}
	if heldCount != 0 {
		t.Fatalf("scan tool holds after release = %d, want 0", heldCount)
	}
	var releasedFlag bool
	if err := database.NewRaw(`SELECT tools_read_required FROM operation WHERE id=?`, operation.ID).Scan(ctx, &releasedFlag); err != nil {
		t.Fatal(err)
	}
	if releasedFlag {
		t.Fatal("scan tool release left tools_read_required=true")
	}
	if _, err := database.NewRaw(`INSERT INTO operation_tool_read_hold(operation_id,installation_id) VALUES (?,?)`, operation.ID, selections[0].InstallationID).Exec(ctx); err == nil {
		t.Fatal("SQL guard accepted a scan tool hold without setting tools_read_required")
	}
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO operation_tool_read_hold(operation_id,installation_id) VALUES (?,?)`, operation.ID, selections[0].InstallationID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE operation SET tools_read_required=true,state='succeeded',stage='succeeded',finished_at=now() WHERE id=?`, operation.ID).Exec(ctx); err != nil {
			return err
		}
		return nil
	}); err == nil {
		t.Fatal("SQL guard accepted terminal scan retaining a tool hold")
	}
	if _, err := database.NewRaw(`UPDATE operation SET state='succeeded', stage='succeeded', finished_at=now() WHERE id=?`, operation.ID).Exec(ctx); err != nil {
		t.Fatalf("finish scan after releasing tool holds: %v", err)
	}
	var terminalFlag bool
	if err := database.NewRaw(`SELECT tools_read_required FROM operation WHERE id=?`, operation.ID).Scan(ctx, &terminalFlag); err != nil {
		t.Fatal(err)
	}
	if terminalFlag || heldCount != 0 {
		t.Fatalf("terminal scan holds: flag=%t count=%d, want false/0", terminalFlag, heldCount)
	}

	_, err = database.NewRaw(`INSERT INTO operation (id,kind,state,stage,input_snapshot,tools_read_required)
		VALUES (?, 'install', 'succeeded', 'succeeded', '{"target_identity":"unrelated"}'::jsonb, true)`, uuid.New()).Exec(ctx)
	if err == nil {
		t.Fatal("non-scan operation with tools_read_required=true passed the operation shape constraint")
	}
}

func insertScanHoldInstallation(t *testing.T, ctx context.Context, database *bun.DB, toolsRoot, kind, executable, banner string) persistence.SourceAnalysisToolSelection {
	t.Helper()
	release := "scan-hold-" + uuid.NewString()
	relativePath := filepath.ToSlash(filepath.Join(kind, release))
	executableName := executable
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	executablePath := filepath.Join(toolsRoot, filepath.FromSlash(relativePath), executableName)
	if err := os.MkdirAll(filepath.Dir(executablePath), 0o755); err != nil {
		t.Fatalf("create managed tool directory: %v", err)
	}
	if err := os.WriteFile(executablePath, []byte("protected fixture"), 0o755); err != nil {
		t.Fatalf("create protected executable fixture: %v", err)
	}
	verified := time.Now().UTC()
	versions, err := json.Marshal(map[string]string{executableName: banner})
	if err != nil {
		t.Fatalf("encode verified executable versions: %v", err)
	}
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: kind, PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH,
		SourceName: "scan-hold-test", ReleaseIdentity: release, RelativePath: relativePath,
		State: "ready", ExecutableVersions: versions, ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verified,
	}
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		t.Fatalf("insert verified %s installation: %v", kind, err)
	}
	version := "7.1.2"
	if kind == "fpcalc" {
		version = "1.5.0"
	}
	return persistence.SourceAnalysisToolSelection{
		PackageKind: kind, InstallationID: installation.ID, RelativePath: relativePath,
		Executable: executable, Version: version, VersionBanner: banner,
	}
}

func boolPointer(value bool) *bool { return &value }
