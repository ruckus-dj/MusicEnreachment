//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// These PostgreSQL fixtures represent database state only. They do not claim
// that any executable was downloaded or verified.
func TestC07FailedInstallRetryAllowsIncompleteInstallHistory(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repo := persistence.NewSetupManagerRepository(db)
	client := c07RiverClient(t, ctx, db)
	root := c07ToolsRoot(t, ctx, db)
	failed := c07Admission(t, ctx, repo, client, root, "ffmpeg", "8.0")
	if failed == nil {
		t.Fatal("initial synthetic install was refused")
	}
	failedID := *failed.TargetInstallationID
	if _, err := db.NewUpdate().Model((*persistence.ToolInstallation)(nil)).Set("state = 'failed'").Where("id = ?", failedID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewUpdate().Model((*persistence.Operation)(nil)).Set("state = 'failed'").Set("stage = 'download'").Set("safe_error = 'synthetic failure'").Set("finished_at = ?", time.Now().UTC()).Where("id = ?", failed.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	retried, err := repo.RetryOperationAndEnqueue(ctx, failed.ID, client, service.OperationJobArgs{OperationID: failed.ID}, nil)
	if err != nil {
		t.Fatalf("retry failed install with only preparing/failed history: %v", err)
	}
	if retried.State != "queued" || retried.Attempt != failed.Attempt+1 || retried.RiverJobID == nil {
		t.Fatalf("retried operation = %#v; want next queued attempt with River job", retried)
	}
	installation, err := repo.GetInstallation(ctx, failedID)
	if err != nil || installation.State != "preparing" {
		t.Fatalf("retry target = %#v, %v; want preparing", installation, err)
	}
	var installations, operations int
	if err := db.NewRaw("SELECT count(*) FROM tool_installation").Scan(ctx, &installations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw("SELECT count(*) FROM operation").Scan(ctx, &operations); err != nil {
		t.Fatal(err)
	}
	if installations != 1 || operations != 1 {
		t.Fatalf("installations/operations = %d/%d, want 1/1", installations, operations)
	}
	var jobs int
	if err := db.NewRaw("SELECT count(*) FROM river_job WHERE kind = ?", "operation_v1").Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 2 {
		t.Fatalf("install River jobs = %d, want original and retry", jobs)
	}
}

func TestC07OldFailedRetryRefusedAfterDifferentReadyVersion(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repo := persistence.NewSetupManagerRepository(db)
	client := c07RiverClient(t, ctx, db)
	root := c07ToolsRoot(t, ctx, db)
	failed := c07Admission(t, ctx, repo, client, root, "ffmpeg", "8.0")
	if failed == nil {
		t.Fatal("initial synthetic install was refused")
	}
	if _, err := db.NewUpdate().Model((*persistence.ToolInstallation)(nil)).Set("state = 'failed'").Where("id = ?", *failed.TargetInstallationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewUpdate().Model((*persistence.Operation)(nil)).Set("state = 'failed'").Set("safe_error = 'synthetic failure'").Set("finished_at = ?", time.Now().UTC()).Where("id = ?", failed.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ready := c07Admission(t, ctx, repo, client, root, "ffmpeg", "8.1")
	if ready == nil {
		t.Fatal("new version was not admitted after failed installation")
	}
	if err := repo.MarkInstallationReady(ctx, *ready.TargetInstallationID, json.RawMessage(`{"ffmpeg":"8.1"}`), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var beforeState string
	var beforeAttempt int
	var beforeJobID int64
	if err := db.NewRaw("SELECT state, attempt, river_job_id FROM operation WHERE id = ?", failed.ID).Scan(ctx, &beforeState, &beforeAttempt, &beforeJobID); err != nil {
		t.Fatal(err)
	}
	var beforeJobs int
	if err := db.NewRaw("SELECT count(*) FROM river_job WHERE kind = ?", "operation_v1").Scan(ctx, &beforeJobs); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RetryOperationAndEnqueue(ctx, failed.ID, client, service.OperationJobArgs{OperationID: failed.ID}, nil); err == nil {
		t.Fatal("retry of old failed install succeeded despite a different ready same-package version")
	}
	var state string
	var attempt int
	var jobID int64
	if err := db.NewRaw("SELECT state, attempt, river_job_id FROM operation WHERE id = ?", failed.ID).Scan(ctx, &state, &attempt, &jobID); err != nil {
		t.Fatal(err)
	}
	var jobs int
	if err := db.NewRaw("SELECT count(*) FROM river_job WHERE kind = ?", "operation_v1").Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if state != beforeState || attempt != beforeAttempt || jobID != beforeJobID || jobs != beforeJobs {
		t.Fatalf("refused retry changed state/attempt/job/jobs: %s/%d/%d/%d; before %s/%d/%d/%d", state, attempt, jobID, jobs, beforeState, beforeAttempt, beforeJobID, beforeJobs)
	}
	assertC07OnlyRows(t, ctx, db, 2, 2)
}

func TestC07LegacyMultipleReadyInstallationsPreservedAndBlockAdmission(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repo := persistence.NewSetupManagerRepository(db)
	client := c07RiverClient(t, ctx, db)
	root := c07ToolsRoot(t, ctx, db)
	delivery := c07Admission(t, ctx, repo, client, root, "ffmpeg", "8.2")
	if delivery == nil {
		t.Fatal("pending synthetic ffmpeg install was refused")
	}
	firstID := c07SeedLegacyReadyInstallation(t, ctx, repo, "8.0")
	secondID := c07SeedLegacyReadyInstallation(t, ctx, repo, "8.1")
	beforeFirst, err := repo.GetInstallation(ctx, firstID)
	if err != nil {
		t.Fatal(err)
	}
	beforeSecond, err := repo.GetInstallation(ctx, secondID)
	if err != nil {
		t.Fatal(err)
	}
	if operation := c07Admission(t, ctx, repo, client, root, "ffmpeg", "8.3"); operation != nil {
		t.Fatalf("admission chose among multiple legacy ready versions: %s", operation.ID)
	}
	if _, err := repo.ActivateInstallationDuringSetup(ctx, firstID, "ffmpeg", "linux", "amd64", settings.ActiveFFmpegInstallationKey); err == nil {
		t.Fatal("setup activation selected one of multiple legacy ready installations")
	}
	var activeCount int
	if err := db.NewRaw("SELECT count(*) FROM app_setting WHERE setting_name = ?", settings.ActiveFFmpegInstallationKey).Scan(ctx, &activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 0 {
		t.Fatalf("active installation setting count = %d, want none", activeCount)
	}
	for _, expected := range []struct {
		id     uuid.UUID
		before *persistence.ToolInstallation
	}{{firstID, beforeFirst}, {secondID, beforeSecond}} {
		actual, err := repo.GetInstallation(ctx, expected.id)
		if err != nil || actual.State != "ready" || actual.ID != expected.before.ID {
			t.Fatalf("legacy ready installation %s changed/deleted: %#v, %v", expected.id, actual, err)
		}
	}
	if _, err := repo.FinalizeInstallation(ctx, delivery.ID, delivery.Attempt, *delivery.RiverJobID, *delivery.TargetInstallationID, "ffmpeg", "linux", "amd64", settings.ActiveFFmpegInstallationKey, json.RawMessage(`{"ffmpeg":"synthetic"}`), time.Now().UTC()); err == nil {
		t.Fatal("finalization selected among multiple legacy ready installations")
	}
	assertC07OnlyRows(t, ctx, db, 3, 1)
}

func c07SeedLegacyReadyInstallation(t *testing.T, ctx context.Context, repo *persistence.SetupManagerRepository, version string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	relative, err := tools.ManagedRelativePath(tools.PackageFFmpeg, version)
	if err != nil {
		t.Fatal(err)
	}
	installation := &persistence.ToolInstallation{
		ID: id, PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "legacy-test", ReleaseIdentity: version, RelativePath: relative,
		State: "preparing", ExecutableVersions: json.RawMessage(`{"ffmpeg":"synthetic"}`), ArtifactIdentities: json.RawMessage(`[]`),
	}
	if err := repo.CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("seed synthetic legacy installation: %v", err)
	}
	if err := repo.MarkInstallationReady(ctx, id, json.RawMessage(`{"ffmpeg":"synthetic"}`), time.Now().UTC()); err != nil {
		t.Fatalf("mark synthetic legacy installation ready: %v", err)
	}
	return id
}
