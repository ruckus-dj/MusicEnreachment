//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/migrate"
)

const sourceAnalysisExecutionIdentityMigration = "20261020000000"

func TestSourceAnalysisExecutionIdentityBackfillsArtifactsAndGuardsRollbackWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	migration := migrationNamed(t, collection, sourceAnalysisExecutionIdentityMigration)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, sourceAnalysisExecutionIdentityMigration))

	rootID, locationID, workID, operationID, artifactID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_root (id, configured_path, display_name)
		VALUES (?, '/srv/execution-identity-backfill', 'execution identity backfill')`, rootID); err != nil {
		t.Fatalf("insert source root: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_location
		(id, source_root_id, relative_path, size_bytes, mtime, last_seen_scan_generation, probe_status)
		VALUES (?, ?, 'track.flac', 12, now(), 1, 'audio')`, locationID, rootID); err != nil {
		t.Fatalf("insert source location: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work
		(id, location_id, source_root_id, configured_path, inventory_path, relative_path, size_bytes, mtime, sha256_enabled, origin_scan_operation_id)
		VALUES (?, ?, ?, '/srv/execution-identity-backfill', '/srv/execution-identity-backfill', 'track.flac', 12, now(), true, ?)`,
		workID, locationID, rootID, uuid.New()); err != nil {
		t.Fatalf("insert source work: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO operation
		(id, kind, state, stage, input_snapshot, attempt, river_job_id, created_at, updated_at, finished_at)
		VALUES (?, 'analyze_source', 'succeeded', 'complete', '{}', 1, 901, now(), now(), now())`, operationID); err != nil {
		t.Fatalf("insert artifact owner operation: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_artifact
		(id, work_id, relative_output_path, source_size_bytes, source_mtime, owner_operation_id, owner_operation_attempt, owner_job_id, state)
		VALUES (?, ?, ?, 12, now(), ?, 1, 901, 'acquiring')`, artifactID, workID,
		"analysis/staging/"+rootID.String()+"/"+workID.String()+"/"+artifactID.String(), operationID); err != nil {
		t.Fatalf("insert pre-migration artifact: %v", err)
	}

	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
	var mode string
	if err := database.NewRaw(`SELECT processing_mode FROM source_analysis_work_execution
		WHERE work_id=? AND operation_id=? AND operation_attempt=1 AND job_id=901`, workID, operationID).Scan(ctx, &mode); err != nil {
		t.Fatalf("read backfilled execution identity: %v", err)
	}
	if mode != "staged" {
		t.Fatalf("backfilled processing mode = %q, want staged", mode)
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_work_execution
		SET processing_mode='in_place' WHERE work_id=? AND operation_id=?`, workID, operationID); err == nil {
		t.Fatal("updating a captured execution identity succeeded")
	}

	if _, err := database.ExecContext(ctx, `UPDATE operation SET attempt=2, river_job_id=902 WHERE id=?`, operationID); err != nil {
		t.Fatalf("change mutable operation delivery for rollback guard: %v", err)
	}
	migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize execution identity rollback: %v", err)
	}
	if _, err := migrator.Rollback(ctx); err == nil {
		t.Fatal("rollback with a backfilled artifact whose mutable operation tuple changed succeeded")
	}
	var retained int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE id=?`, artifactID).Scan(ctx, &retained); err != nil {
		t.Fatalf("check artifact after refused rollback: %v", err)
	}
	if retained != 1 {
		t.Fatalf("artifact rows after refused rollback = %d, want 1", retained)
	}
}
