//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type analysisRetryFixture struct {
	database          *bun.DB
	inventory         *persistence.SourceInventoryRepository
	setup             *persistence.SetupManagerRepository
	client            *river.Client[*sql.Tx]
	root              *persistence.SourceRoot
	location          persistence.SourceLocation
	installationID    uuid.UUID
	previousVariantID uuid.UUID
}

// TestSourceAnalysisRetryRefusesStaleSnapshotWithPostgreSQL is the mandatory
// sequence: a failed analysis A pins variant v0 in its snapshot; a fresh analysis
// B replaces the variant of the unchanged file with v1 and orphans v0; retrying A
// now conflicts because v0 is no longer the current variant, and changes nothing.
func newAnalysisRetryFixture(t *testing.T, withPrevious bool) analysisRetryFixture {
	t.Helper()
	ctx := context.Background()
	database := testpostgres.OpenMigrated(t)
	inventory := persistence.NewSourceInventoryRepository(database)
	setup := persistence.NewSetupManagerRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/analysis-retry")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	installationID := insertAnalysisInstallation(t, ctx, database, "retry")
	setActiveAnalysisFFmpeg(t, ctx, database, installationID)
	setAnalysisRetryPlatform(t, ctx, database)
	fixture := analysisRetryFixture{
		database: database, inventory: inventory, setup: setup, client: openScanEnqueueRiver(t, database),
		root: root, location: location, installationID: installationID,
	}
	if withPrevious {
		variantID := insertMediaVariantRow(t, ctx, database, location.SizeBytes, uuid.New())
		linkLocationVariant(t, ctx, database, location.ID, variantID)
		fixture.location.MediaVariantID = &variantID
		fixture.previousVariantID = variantID
	}
	return fixture
}

func (fixture analysisRetryFixture) retry(t *testing.T, ctx context.Context, id uuid.UUID) (*persistence.Operation, error) {
	t.Helper()
	return fixture.setup.RetrySourceAnalysisOperationAndEnqueue(ctx, id, fixture.client,
		service.SourceAnalysisJobArgs{OperationID: id}, &river.InsertOpts{Queue: service.SourceAnalysisQueue})
}

func insertFailedAnalysisOperation(t *testing.T, ctx context.Context, fixture analysisRetryFixture) *persistence.Operation {
	t.Helper()
	reason := "The managed ffprobe is unavailable or failed verification. Repair the managed tools and retry the analysis."
	operation := analysisEnqueueOperation(fixture.root, fixture.location, fixture.installationID, previousOrNil(fixture))
	operation.State = "failed"
	operation.Stage = "probing"
	operation.SafeError = &reason
	finishedAt := time.Now().UTC()
	operation.FinishedAt = &finishedAt
	operation.AnalysisInstallationID = nil
	operation.AnalysisMediaVariantID = nil
	if _, err := fixture.database.NewInsert().Model(operation).Exec(ctx); err != nil {
		t.Fatalf("insert the failed analysis: %v", err)
	}
	return operation
}

func insertAnalysisOperation(t *testing.T, ctx context.Context, fixture analysisRetryFixture, previous *uuid.UUID, state, stage string) *persistence.Operation {
	t.Helper()
	operation := analysisEnqueueOperation(fixture.root, fixture.location, fixture.installationID, previous)
	operation.State = state
	operation.Stage = stage
	if _, err := fixture.database.NewInsert().Model(operation).Exec(ctx); err != nil {
		t.Fatalf("insert the analysis operation: %v", err)
	}
	return operation
}

func previousOrNil(fixture analysisRetryFixture) *uuid.UUID {
	if fixture.previousVariantID == uuid.Nil {
		return nil
	}
	variant := fixture.previousVariantID
	return &variant
}

func requireAnalysisRetryUntouched(t *testing.T, ctx context.Context, fixture analysisRetryFixture, operationID uuid.UUID, wantAttempt int) {
	t.Helper()
	stored, err := fixture.setup.GetOperation(ctx, operationID)
	if err != nil {
		t.Fatalf("read the refused operation: %v", err)
	}
	if stored.State != "failed" || stored.Attempt != wantAttempt {
		t.Fatalf("refused operation = %+v, want failed on attempt %d", stored, wantAttempt)
	}
	if stored.AnalysisInstallationID != nil || stored.AnalysisMediaVariantID != nil {
		t.Fatalf("refused operation kept a hold: %+v", stored)
	}
	if jobs := countAnalysisRetryJobs(t, ctx, fixture.database, operationID); jobs != 0 {
		t.Fatalf("River jobs of the refused operation = %d, want 0", jobs)
	}
}

// restoreQueuedRetryHold applies the queued transition and the restored read
// holds of a retry inside a caller-owned transaction, mirroring
// RetrySourceAnalysisOperationAndEnqueue without opening its own transaction. A
// race test uses it as the competing transaction that holds the operation table
// lock and prepares the retry uncommitted while the real move/delete action waits
// on the same lock.
func restoreQueuedRetryHold(ctx context.Context, tx bun.Tx, operation *persistence.Operation, variant *uuid.UUID, installationID uuid.UUID, client persistence.RiverInserter) error {
	result, err := client.InsertTx(ctx, tx.Tx, service.SourceAnalysisJobArgs{OperationID: operation.ID}, &river.InsertOpts{Queue: service.SourceAnalysisQueue})
	if err != nil {
		return err
	}
	_, err = tx.NewRaw(`UPDATE operation SET state = 'queued', stage = 'retry:probing', safe_error = NULL,
		analysis_installation_id = ?, analysis_media_variant_id = ?, attempt = attempt + 1,
		started_at = NULL, finished_at = NULL, river_job_id = ?, updated_at = now() WHERE id = ?`,
		installationID, variant, result.Job.ID, operation.ID).Exec(ctx)
	return err
}

func countAnalysisRetryJobs(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID) int {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM river_job WHERE args ->> 'operation_id' = ?", operationID.String()).Scan(ctx, &count); err != nil {
		t.Fatalf("count River jobs of %s: %v", operationID, err)
	}
	return count
}

func readAnalysisRetryJob(t *testing.T, ctx context.Context, database *bun.DB, id int64) (string, string) {
	t.Helper()
	var job struct {
		Kind  string `bun:"kind"`
		Queue string `bun:"queue"`
	}
	if err := database.NewRaw("SELECT kind, queue FROM river_job WHERE id = ?", id).Scan(ctx, &job); err != nil {
		t.Fatalf("read River job %d: %v", id, err)
	}
	return job.Kind, job.Queue
}

func installationExists(t *testing.T, ctx context.Context, database *bun.DB, id uuid.UUID) bool {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM tool_installation WHERE id = ?", id).Scan(ctx, &count); err != nil {
		t.Fatalf("read installation %s: %v", id, err)
	}
	return count == 1
}

func execAnalysisRetrySQL(t *testing.T, ctx context.Context, database *bun.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("execute (%s): %v", query, err)
	}
}

// setAnalysisRetryPlatform records the instance platform the persistence retry
// fences the pinned installation against.
func setAnalysisRetryPlatform(t *testing.T, ctx context.Context, database *bun.DB) {
	t.Helper()
	for _, setting := range []persistence.AppSetting{
		{Name: "instance.goos", Value: analysisTestGOOS},
		{Name: "instance.goarch", Value: analysisTestGOARCH},
	} {
		if _, err := database.NewInsert().Model(&setting).
			On("CONFLICT (setting_name) DO UPDATE").Set("setting_value = EXCLUDED.setting_value").Exec(ctx); err != nil {
			t.Fatalf("record the instance platform %s: %v", setting.Name, err)
		}
	}
}

func insertOperationRow(t *testing.T, ctx context.Context, database *bun.DB, operation *persistence.Operation) {
	t.Helper()
	operation.Attempt = 1
	if _, err := database.NewInsert().Model(operation).Exec(ctx); err != nil {
		t.Fatalf("insert operation %s: %v", operation.ID, err)
	}
}

func sameOptionalUUIDRetry(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
