//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const sourceScanToolReaderShapeMigration = "20261011000000"

func TestSourceScanToolReaderShapeMigrationWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, database, migrationsThrough(t, collection, "20261008120000"))
	migration := migrationNamed(t, collection, sourceScanToolReaderShapeMigration)
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

	legacyScanID := insertSourceScanReaderOperation(t, ctx, database, false)
	scanID := insertSourceScanReaderOperation(t, ctx, database, true)
	assertNonScanToolReaderOperationRejected(t, ctx, database)

	if _, err := newSourceScanReaderRollbackMigrator(database, migration).Rollback(ctx); err == nil {
		t.Fatal("rollback with a retained scan reader flag succeeded")
	}
	assertSourceScanReaderShapeExists(t, database)
	if _, err := database.ExecContext(ctx, `UPDATE operation SET tools_read_required=false WHERE id=?`, scanID); err != nil {
		t.Fatalf("clear scan reader flag: %v", err)
	}
	insertSourceScanReaderHold(t, ctx, database, scanID)
	if _, err := newSourceScanReaderRollbackMigrator(database, migration).Rollback(ctx); err == nil {
		t.Fatal("rollback with a retained scan reader hold succeeded")
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM operation_tool_read_hold WHERE operation_id=?`, scanID); err != nil {
		t.Fatalf("clear scan reader hold: %v", err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM operation WHERE id=?`, scanID); err != nil {
		t.Fatalf("delete cleared scan reader operation: %v", err)
	}
	rollbackMigration(t, ctx, database, migration)
	assertLegacyScanReaderFlagIsFalse(t, ctx, database, legacyScanID)
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
	reappliedScanID := insertSourceScanReaderOperation(t, ctx, database, true)
	assertSourceScanReaderFlag(t, ctx, database, reappliedScanID)
}

func insertSourceScanReaderOperation(t *testing.T, ctx context.Context, database *bun.DB, toolsReadRequired bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := database.ExecContext(ctx, `
		INSERT INTO operation (id, kind, state, stage, input_snapshot, tools_read_required, finished_at)
		VALUES (?, 'scan_source', 'succeeded', 'complete', '{}'::jsonb, ?, ?)
	`, id, toolsReadRequired, time.Now().UTC()); err != nil {
		t.Fatalf("insert source scan reader operation: %v", err)
	}
	return id
}

func assertNonScanToolReaderOperationRejected(t *testing.T, ctx context.Context, database *bun.DB) {
	t.Helper()
	_, err := database.ExecContext(ctx, `
		INSERT INTO operation (id, kind, state, stage, input_snapshot, tools_read_required, finished_at)
		VALUES (?, 'move_tools_root', 'succeeded', 'complete', '{}'::jsonb, true, ?)
	`, uuid.New(), time.Now().UTC())
	if err == nil {
		t.Fatal("non-scan operation accepted a NULL-mode tool reader flag")
	}
}

func insertSourceScanReaderHold(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID) {
	t.Helper()
	installationID := uuid.New()
	releaseIdentity := uuid.NewString()
	if _, err := database.ExecContext(ctx, `
		INSERT INTO tool_installation (id, package_kind, platform_goos, platform_goarch, source_name, release_identity, relative_path, state, verified_at)
		VALUES (?, 'ffmpeg', 'linux', 'amd64', 'test', ?, ?, 'ready', ?)
	`, installationID, releaseIdentity, "ffmpeg/"+releaseIdentity, time.Now().UTC()); err != nil {
		t.Fatalf("insert source scan reader tool installation: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO operation_tool_read_hold (operation_id, installation_id) VALUES (?, ?)`, operationID, installationID); err != nil {
		t.Fatalf("insert source scan reader hold: %v", err)
	}
}

func assertSourceScanReaderShapeExists(t *testing.T, database *bun.DB) {
	t.Helper()
	var constraint string
	if err := database.NewRaw(`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='operation_source_analysis_target_shape'`).Scan(context.Background(), &constraint); err != nil {
		t.Fatalf("read source scan reader shape constraint: %v", err)
	}
	if !strings.Contains(constraint, "scan_source") {
		t.Fatalf("source scan reader shape constraint = %q, want scan_source allowance", constraint)
	}
}

func assertLegacyScanReaderFlagIsFalse(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID) {
	t.Helper()
	var required bool
	if err := database.NewRaw(`SELECT tools_read_required FROM operation WHERE id=?`, operationID).Scan(ctx, &required); err != nil {
		t.Fatalf("read legacy scan reader flag: %v", err)
	}
	if required {
		t.Fatal("legacy scan reader flag unexpectedly changed")
	}
}

func assertSourceScanReaderFlag(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID) {
	t.Helper()
	var mode sql.NullString
	var targetWorkID uuid.NullUUID
	var targetStep sql.NullString
	var required bool
	if err := database.NewRaw(`SELECT source_analysis_mode, target_work_id, target_step, tools_read_required FROM operation WHERE id=?`, operationID).Scan(ctx, &mode, &targetWorkID, &targetStep, &required); err != nil {
		t.Fatalf("read source scan reader selectors: %v", err)
	}
	if mode.Valid || targetWorkID.Valid || targetStep.Valid || !required {
		t.Fatalf("source scan reader selectors/flag = %v/%v/%v/%v, want NULL/NULL/NULL/true", mode, targetWorkID, targetStep, required)
	}
}

func newSourceScanReaderRollbackMigrator(database *bun.DB, migration *migrate.Migration) *migrate.Migrator {
	collection := migrate.NewMigrations()
	collection.Add(*migration)
	return migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
}
