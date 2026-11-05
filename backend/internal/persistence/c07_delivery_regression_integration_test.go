//go:build integration

package persistence_test

import (
	"context"

	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// Fixtures in these tests are synthetic database rows; they do not assert that
// any executable was downloaded or verified.

func TestC07StaleDeliveryCannotFailOrFinalizeRetryTarget(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	repo := persistence.NewSetupManagerRepository(db)
	client := c07RiverClient(t, ctx, db)
	root := c07ToolsRoot(t, ctx, db)
	op := c07Admission(t, ctx, repo, client, root, "ffmpeg", "8.0")
	oldJob, oldAttempt := *op.RiverJobID, op.Attempt
	newJob := oldJob + 999
	if _, err := db.ExecContext(ctx, "UPDATE operation SET attempt = attempt + 1, river_job_id = ? WHERE id = ?", newJob, op.ID); err != nil {
		t.Fatal(err)
	}
	changed, err := repo.FailInstallationDelivery(ctx, op.ID, oldAttempt, oldJob, *op.TargetInstallationID, "download", "safe")
	if err != nil || changed {
		t.Fatalf("stale fail changed=%v err=%v", changed, err)
	}
	activated, err := repo.FinalizeInstallation(ctx, op.ID, oldAttempt, oldJob, *op.TargetInstallationID, "ffmpeg", "linux", "amd64", settings.ActiveFFmpegInstallationKey, tools.VerificationResult{Version: "ffmpeg synthetic + ffprobe synthetic", Evidence: map[string]string{"ffmpeg": "ffmpeg version synthetic", "ffprobe": "ffprobe version synthetic"}}, time.Now().UTC())
	if err == nil || activated {
		t.Fatalf("stale finalize activated=%v err=%v", activated, err)
	}
	installation, err := repo.GetInstallation(ctx, *op.TargetInstallationID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := repo.GetOperation(ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if installation.State != "preparing" || current.State != "queued" || *current.RiverJobID != newJob {
		t.Fatalf("stale delivery mutated state: installation=%s operation=%s job=%d", installation.State, current.State, *current.RiverJobID)
	}
}
