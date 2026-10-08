//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestStartNormalizedSourceAnalysisDeliveryReplacesAdmissionToolHoldsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	t.Run("current selection replaces admission selection", func(t *testing.T) {
		database := testpostgres.OpenMigrated(t)
		ctx := context.Background()
		repository := persistence.NewSourceInventoryRepository(database)
		_, operation, admissionTool := queuedToolAnalysis(t, ctx, database, repository, "/srv/analysis-delivery-reselect")

		currentToolID := insertAnalysisInstallation(t, ctx, database, "delivery-current")
		if err := persistence.NewSettingsRepository(database).Set(ctx, "active_ffmpeg_installation_id", currentToolID.String()); err != nil {
			t.Fatalf("select current ffmpeg installation: %v", err)
		}

		started, selections, _, err := repository.StartNormalizedSourceAnalysisDelivery(ctx, operation.ID,
			persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID}, "darwin", "arm64")
		if err != nil {
			t.Fatalf("start normalized source analysis delivery: %v", err)
		}
		if started.State != "running" || len(selections) != 1 || selections[0].InstallationID != currentToolID {
			t.Fatalf("started operation/selections = %v / %+v, want running with current installation %s", started.State, selections, currentToolID)
		}
		assertDeliveryToolHolds(t, ctx, database, operation.ID, currentToolID)
		if currentToolID == admissionTool.InstallationID {
			t.Fatal("fixture admission and execution installations unexpectedly match")
		}
	})

	t.Run("staged root resolves current tools and records staged execution", func(t *testing.T) {
		database := testpostgres.OpenMigrated(t)
		ctx := context.Background()
		repository := persistence.NewSourceInventoryRepository(database)
		root, operation, admissionTool := queuedToolAnalysis(t, ctx, database, repository, "/srv/analysis-delivery-staged")
		if _, err := database.NewRaw(`UPDATE source_root SET processing_mode='staged' WHERE id=?`, root.ID).Exec(ctx); err != nil {
			t.Fatalf("switch source root to staged mode: %v", err)
		}

		started, selections, mode, err := repository.StartNormalizedSourceAnalysisDelivery(ctx, operation.ID,
			persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID}, "darwin", "arm64")
		if err != nil {
			t.Fatalf("start staged source analysis delivery: %v", err)
		}
		if started.State != "running" || mode != "staged" || len(selections) != 1 ||
			selections[0].PackageKind != "ffmpeg" || selections[0].InstallationID != admissionTool.InstallationID || !started.ToolsReadRequired {
			t.Fatalf("staged delivery = state %q, mode %q, selections %+v, tools_read_required %t", started.State, mode, selections, started.ToolsReadRequired)
		}
		assertDeliveryToolHolds(t, ctx, database, operation.ID, admissionTool.InstallationID)
		var stagedExecutions int
		if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work_execution WHERE operation_id=? AND processing_mode='staged'`, operation.ID).Scan(ctx, &stagedExecutions); err != nil {
			t.Fatalf("read staged execution records: %v", err)
		}
		if stagedExecutions != 1 {
			t.Fatalf("staged execution records = %d, want 1", stagedExecutions)
		}
	})
}

func queuedToolAnalysis(
	t *testing.T,
	ctx context.Context,
	database *bun.DB,
	repository *persistence.SourceInventoryRepository,
	path string,
) (*persistence.SourceRoot, *persistence.Operation, persistence.SourceAnalysisToolSelection) {
	t.Helper()
	root := createInventoryRoot(t, ctx, repository, path)
	if _, err := database.NewRaw(`UPDATE source_root SET processing_mode='in_place' WHERE id=?`, root.ID).Exec(ctx); err != nil {
		t.Fatalf("set in-place source processing: %v", err)
	}
	root.ProcessingMode = "in_place"
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, false,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"})
	installationID := insertAnalysisInstallation(t, ctx, database, "delivery-admission-"+uuid.NewString())
	installation := new(persistence.ToolInstallation)
	if err := database.NewSelect().Model(installation).Where("id=?", installationID).Scan(ctx); err != nil {
		t.Fatalf("read admission installation: %v", err)
	}
	selection := persistence.SourceAnalysisToolSelection{
		PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: installation.RelativePath,
		Executable: "ffprobe", Version: "7.1.2", VersionBanner: "ffprobe version 7.1.2",
	}
	if err := persistence.NewSettingsRepository(database).Set(ctx, "active_ffmpeg_installation_id", installationID.String()); err != nil {
		t.Fatalf("select admission ffmpeg installation: %v", err)
	}
	operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, false, false, []persistence.SourceAnalysisToolSelection{selection})
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, openScanEnqueueRiver(t, database),
		service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("admit normalized source analysis: %v", err)
	}
	assertDeliveryToolHolds(t, ctx, database, operation.ID, installationID)
	return root, operation, selection
}

func assertDeliveryToolHolds(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID, want ...uuid.UUID) {
	t.Helper()
	var held []uuid.UUID
	if err := database.NewRaw(`SELECT installation_id FROM operation_tool_read_hold WHERE operation_id=? ORDER BY installation_id`, operationID).Scan(ctx, &held); err != nil {
		t.Fatalf("read operation tool holds: %v", err)
	}
	if len(held) != len(want) {
		t.Fatalf("operation tool holds = %v, want %v", held, want)
	}
	for index := range want {
		if held[index] != want[index] {
			t.Fatalf("operation tool holds = %v, want %v", held, want)
		}
	}
}
