//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// Fixtures in these tests are synthetic database rows; they do not assert that
// any executable was downloaded or verified.
func TestC07ExplicitActivationRequiresCompletionAndReadyTarget(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repo := persistence.NewSetupManagerRepository(db)
	client := c07RiverClient(t, ctx, db)
	root := c07ToolsRoot(t, ctx, db)
	op := c07Admission(t, ctx, repo, client, root, "ffmpeg", "8.0")
	id := *op.TargetInstallationID
	if err := repo.ActivateInstallation(ctx, id, "ffmpeg", "linux", "amd64", settings.ActiveFFmpegInstallationKey); err == nil {
		t.Fatal("explicit activation before completion succeeded")
	}
	if err := repo.MarkInstallationReady(ctx, id, json.RawMessage(`{"ffmpeg":"synthetic"}`), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewInsert().Model(&persistence.AppSetting{Name: "setup_completed_at", Value: "done"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.ActivateInstallation(ctx, id, "ffmpeg", "linux", "amd64", settings.ActiveFFmpegInstallationKey); err != nil {
		t.Fatalf("activate ready target after completion: %v", err)
	}
	if err := repo.ActivateInstallation(ctx, id, "fpcalc", "linux", "amd64", settings.ActiveFFmpegInstallationKey); err == nil {
		t.Fatal("wrong package activated")
	}
}

func TestC07FinalizeActivatesOnlyBeforeSetupCompletion(t *testing.T) {
	for _, completed := range []bool{false, true} {
		completed := completed
		t.Run(map[bool]string{false: "initial", true: "completed"}[completed], func(t *testing.T) {
			t.Parallel()
			db := testpostgres.OpenMigrated(t)
			ctx := context.Background()
			repo := persistence.NewSetupManagerRepository(db)
			client := c07RiverClient(t, ctx, db)
			root := c07ToolsRoot(t, ctx, db)
			op := c07Admission(t, ctx, repo, client, root, "ffmpeg", "8.0")
			if completed {
				if _, err := db.NewInsert().Model(&persistence.AppSetting{Name: "setup_completed_at", Value: "done"}).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}
			settled, err := repo.FinalizeInstallation(ctx, op.ID, op.Attempt, *op.RiverJobID, *op.TargetInstallationID, "ffmpeg", "linux", "amd64", settings.ActiveFFmpegInstallationKey, json.RawMessage(`{"ffmpeg":"synthetic"}`), time.Now().UTC())
			if err != nil {
				t.Fatalf("finalize: %v", err)
			}
			if !settled {
				t.Fatal("current delivery did not settle")
			}
			var active string
			activeErr := db.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", settings.ActiveFFmpegInstallationKey).Scan(ctx, &active)
			if completed {
				if !errors.Is(activeErr, sql.ErrNoRows) {
					t.Fatalf("post-setup download changed active setting: %q, %v", active, activeErr)
				}
			} else if activeErr != nil || active != op.TargetInstallationID.String() {
				t.Fatalf("first successful version was not activated: %q, %v", active, activeErr)
			}
			got, err := repo.GetOperation(ctx, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != "succeeded" {
				t.Fatalf("state = %q", got.State)
			}
		})
	}
}

func TestC07StaleDeliveryCannotFailOrFinalizeRetryTarget(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
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
	activated, err := repo.FinalizeInstallation(ctx, op.ID, oldAttempt, oldJob, *op.TargetInstallationID, "ffmpeg", "linux", "amd64", settings.ActiveFFmpegInstallationKey, json.RawMessage(`{"ffmpeg":"synthetic"}`), time.Now().UTC())
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
