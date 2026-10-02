//go:build integration

package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// TestSourceAnalysisStartRefusalsWithPostgreSQL pins the service-level refusals
// of a start: a changed identity, a disabled root, an unfinished Setup and a
// missing active installation each create no operation and no River job.
func TestSourceAnalysisStartRefusalsWithPostgreSQL(t *testing.T) {
	ctx := context.Background()

	expectRefusal := func(t *testing.T, fixture sourceAnalysisStartIntegration, request service.SourceAnalysisStartRequest, want error) {
		t.Helper()
		if _, err := fixture.starter.Start(ctx, request); !errors.Is(err, want) {
			t.Fatalf("start = %v, want %v", err, want)
		}
		if operations := countAnalysisStartRows(t, ctx, fixture.database, "SELECT count(*) FROM operation WHERE kind = 'analyze_source'"); operations != 0 {
			t.Fatalf("analysis operations after the refusal = %d, want 0", operations)
		}
		if jobs := countAnalysisStartRows(t, ctx, fixture.database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceAnalysisJobKind); jobs != 0 {
			t.Fatalf("analysis River jobs after the refusal = %d, want 0", jobs)
		}
	}

	t.Run("changed_identity", func(t *testing.T) {
		fixture := newSourceAnalysisStartIntegration(t, true, true)
		expectRefusal(t, fixture, service.SourceAnalysisStartRequest{
			RootID: fixture.root.ID, LocationID: fixture.location.ID,
			ExpectedSizeBytes: fixture.location.SizeBytes + 1, ExpectedMtime: fixture.location.Mtime,
		}, persistence.ErrSourceAnalysisStale)
	})

	t.Run("disabled_root", func(t *testing.T) {
		fixture := newSourceAnalysisStartIntegration(t, true, true)
		if _, err := fixture.database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
			Set("enabled = false").Where("id = ?", fixture.root.ID).Exec(ctx); err != nil {
			t.Fatalf("disable the root: %v", err)
		}
		expectRefusal(t, fixture, service.SourceAnalysisStartRequest{
			RootID: fixture.root.ID, LocationID: fixture.location.ID,
			ExpectedSizeBytes: fixture.location.SizeBytes, ExpectedMtime: fixture.location.Mtime,
		}, service.ErrSourceAnalysisDisabled)
	})

	t.Run("setup_incomplete", func(t *testing.T) {
		fixture := newSourceAnalysisStartIntegration(t, false, true)
		expectRefusal(t, fixture, service.SourceAnalysisStartRequest{
			RootID: fixture.root.ID, LocationID: fixture.location.ID,
			ExpectedSizeBytes: fixture.location.SizeBytes, ExpectedMtime: fixture.location.Mtime,
		}, service.ErrSourceAnalysisNotReady)
	})

	t.Run("no_active_installation", func(t *testing.T) {
		fixture := newSourceAnalysisStartIntegration(t, true, false)
		expectRefusal(t, fixture, service.SourceAnalysisStartRequest{
			RootID: fixture.root.ID, LocationID: fixture.location.ID,
			ExpectedSizeBytes: fixture.location.SizeBytes, ExpectedMtime: fixture.location.Mtime,
		}, service.ErrSourceAnalysisToolUnavailable)
	})
}

// TestSourceAnalysisStartMoveAndActivationWithPostgreSQL proves the two
// directions of the tools boundary at the service level: a queued move refuses a
// start, and once the move is terminal a new start succeeds and keeps its pinned
// installation even when another ready version becomes active.
func TestSourceAnalysisStartMoveAndActivationWithPostgreSQL(t *testing.T) {
	fixture := newSourceAnalysisStartIntegration(t, true, true)
	ctx := context.Background()

	move := &persistence.Operation{
		ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued",
		InputSnapshot: []byte(`{}`),
	}
	if err := fixture.setup.CreateOperation(ctx, move); err != nil {
		t.Fatalf("create a queued tools move: %v", err)
	}
	request := service.SourceAnalysisStartRequest{
		RootID: fixture.root.ID, LocationID: fixture.location.ID,
		ExpectedSizeBytes: fixture.location.SizeBytes, ExpectedMtime: fixture.location.Mtime,
	}
	if _, err := fixture.starter.Start(ctx, request); !errors.Is(err, service.ErrSourceAnalysisMoveActive) {
		t.Fatalf("start with an active tools move = %v, want ErrSourceAnalysisMoveActive", err)
	}
	if operations := countAnalysisStartRows(t, ctx, fixture.database, "SELECT count(*) FROM operation WHERE kind = 'analyze_source'"); operations != 0 {
		t.Fatalf("analysis operations after the move refusal = %d, want 0", operations)
	}
	if _, err := fixture.database.NewUpdate().Model((*persistence.Operation)(nil)).
		Set("state = 'succeeded'").Set("finished_at = now()").Set("updated_at = now()").
		Where("id = ?", move.ID).Exec(ctx); err != nil {
		t.Fatalf("finish the tools move: %v", err)
	}

	operation, err := fixture.starter.Start(ctx, request)
	if err != nil {
		t.Fatalf("start after the move finished: %v", err)
	}
	replacement := insertReadyAnalysisFFmpeg(t, ctx, fixture.database, fixture.platform)
	if err := fixture.setup.ActivateInstallation(ctx, replacement, "ffmpeg",
		fixture.platform.Platform.GOOS, fixture.platform.Platform.GOARCH, settings.ActiveFFmpegInstallationKey); err != nil {
		t.Fatalf("activate another ready ffmpeg version during the queued analysis: %v", err)
	}
	stored, err := fixture.setup.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the queued analysis: %v", err)
	}
	snapshot, err := persistence.DecodeSourceAnalysisSnapshot(stored.InputSnapshot)
	if err != nil {
		t.Fatalf("decode the queued snapshot: %v", err)
	}
	if snapshot.AnalysisInstallationID != fixture.installationID ||
		stored.AnalysisInstallationID == nil || *stored.AnalysisInstallationID != fixture.installationID {
		t.Fatalf("queued analysis installation = %s, want the pinned %s", snapshot.AnalysisInstallationID, fixture.installationID)
	}
	runtimeSettings, err := fixture.registry.ReadRuntimeSettings(ctx)
	if err != nil {
		t.Fatalf("read runtime settings: %v", err)
	}
	if runtimeSettings.ActiveFFmpegInstallation != replacement.String() {
		t.Fatalf("active installation = %s, want the newly activated %s", runtimeSettings.ActiveFFmpegInstallation, replacement)
	}
}
