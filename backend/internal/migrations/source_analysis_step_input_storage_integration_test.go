//go:build integration

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const sourceAnalysisStepInputStorageMigration = "20261008120000"

func TestSourceAnalysisStepInputStorageMigrationWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	ctx := context.Background()
	collection := mustMigrations(t)
	migration := migrationNamed(t, collection, sourceAnalysisStepInputStorageMigration)

	t.Run("clean rollback and reapply preserve nullable model behavior", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, sourceAnalysisStepInputStorageMigration))

		beforeWorkID := insertStorageWork(t, ctx, database)
		if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id, step, state) VALUES (?, 'sha256', 'pending')`, beforeWorkID); err != nil {
			t.Fatalf("insert pre-storage analysis step: %v", err)
		}
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
		before := persistence.SourceAnalysisStep{WorkID: beforeWorkID, Step: "sha256"}
		assertStorageStepInputIsNull(t, ctx, database, before)

		after := insertStorageStep(t, ctx, database, "probe")
		assertStorageStepInputIsNull(t, ctx, database, after)

		rollbackMigration(t, ctx, database, migration)
		if columnExists(t, database, "source_analysis_step", "input_snapshot") {
			t.Fatal("input_snapshot remains after clean storage rollback")
		}
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
		assertStorageStepInputIsNull(t, ctx, database, before)
		assertStorageStepInputIsNull(t, ctx, database, after)
	})

	t.Run("non-null input refuses rollback without dropping data", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, sourceAnalysisStepInputStorageMigration))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
		step := insertStorageStep(t, ctx, database, "sha256")
		input := `{"sha256_enabled":true}`
		if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET input_snapshot=?::jsonb WHERE work_id=? AND step='sha256'`, input, step.WorkID); err != nil {
			t.Fatalf("store step input: %v", err)
		}

		rollback := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
		if _, err := rollback.Rollback(ctx); err == nil {
			t.Fatal("rollback with stored step input succeeded")
		}
		if !columnExists(t, database, "source_analysis_step", "input_snapshot") {
			t.Fatal("refused rollback dropped input_snapshot")
		}
		var preserved int
		if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE work_id=? AND step='sha256' AND input_snapshot=?::jsonb`, step.WorkID, input).Scan(ctx, &preserved); err != nil {
			t.Fatalf("read step input after refused rollback: %v", err)
		}
		if preserved != 1 {
			t.Fatal("stored step input was not preserved after refused rollback")
		}
	})
}

func insertStorageStep(t *testing.T, ctx context.Context, database *bun.DB, step string) persistence.SourceAnalysisStep {
	t.Helper()
	workID := insertStorageWork(t, ctx, database)
	row := persistence.SourceAnalysisStep{WorkID: workID, Step: step, State: "pending"}
	if _, err := database.NewInsert().Model(&row).Exec(ctx); err != nil {
		t.Fatalf("insert analysis step: %v", err)
	}
	return row
}

func insertStorageWork(t *testing.T, ctx context.Context, database *bun.DB) uuid.UUID {
	t.Helper()
	path := "/srv/step-input-storage-" + uuid.NewString()
	rootID := newVariantRoot(t, ctx, database, path)
	locationID := newVariantLocation(t, ctx, database, rootID, "album/track.flac")
	workID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id) VALUES(?,?,?,?,?,?,1,?,false,?)`,
		workID, locationID, rootID, path, path, "album/track.flac", time.Now().UTC(), uuid.New()); err != nil {
		t.Fatalf("insert analysis work: %v", err)
	}
	return workID
}

func assertStorageStepInputIsNull(t *testing.T, ctx context.Context, database *bun.DB, key persistence.SourceAnalysisStep) {
	t.Helper()
	var found persistence.SourceAnalysisStep
	if err := database.NewSelect().Model(&found).Where("work_id = ?", key.WorkID).Where("step = ?", key.Step).Scan(ctx); err != nil {
		t.Fatalf("load analysis step: %v", err)
	}
	if len(found.InputSnapshot) != 0 {
		t.Fatalf("step input = %s, want SQL NULL", found.InputSnapshot)
	}
}
