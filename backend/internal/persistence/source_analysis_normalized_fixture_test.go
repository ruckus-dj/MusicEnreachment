//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

func setActiveAnalysisFFmpeg(t *testing.T, ctx context.Context, database *bun.DB, installationID uuid.UUID) {
	t.Helper()
	if err := persistence.NewSettingsRepository(database).Set(ctx, settings.ActiveFFmpegInstallationKey, installationID.String()); err != nil {
		t.Fatalf("set active analysis ffmpeg installation: %v", err)
	}
}

func decodeAnalysisSnapshot(t *testing.T, raw json.RawMessage, expectedMode string) persistence.SourceAnalysisOperationSnapshot {
	t.Helper()
	snapshot, err := persistence.DecodeSourceAnalysisOperationSnapshot(raw)
	if err != nil {
		t.Fatalf("decode normalized source analysis snapshot: %v", err)
	}
	if snapshot.Mode != expectedMode {
		t.Fatalf("source analysis snapshot mode = %q, want %q", snapshot.Mode, expectedMode)
	}
	return snapshot
}

// insertRunningAnalysisOperation admits real normalized work and its River job.
// It then uses the regular operation transition to become running; no fake
// legacy snapshot or fabricated operation hold is written.
func insertRunningAnalysisOperation(t *testing.T, ctx context.Context, database *bun.DB, root *persistence.SourceRoot, location persistence.SourceLocation, installationID uuid.UUID, _ *uuid.UUID) *persistence.Operation {
	t.Helper()
	repository := persistence.NewSourceInventoryRepository(database)
	setActiveAnalysisFFmpeg(t, ctx, database, installationID)
	tool := normalizedToolSelection(t, ctx, database, installationID, "ffprobe")
	work := normalizedWork(t, ctx, repository, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"})
	operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, true, false,
		[]persistence.SourceAnalysisToolSelection{tool})
	if err := enqueueNormalizedAnalysis(t, ctx, repository, openScanEnqueueRiver(t, database), operation); err != nil {
		t.Fatalf("admit normalized running-analysis fixture: %v", err)
	}
	operations := service.NewOperations(persistence.NewSetupManagerRepository(database))
	if err := operations.Running(ctx, operation.ID, "probing"); err != nil {
		t.Fatalf("mark admitted analysis fixture running: %v", err)
	}
	return operation
}
