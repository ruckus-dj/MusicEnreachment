//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const inventoryMigration = "20261003000000"

func TestSourceInventoryMigrationRollsBackWithStoredScanAndReappliesWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, inventoryMigration))
	inventory := migrationNamed(t, collection, inventoryMigration)
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{inventory})

	rootID, scanID, toolsOperationID := insertRollbackFixture(t, ctx, database)
	assertSourceRootWasRemoved(t, ctx, database, rootID)
	assertTerminalScanRetainedAfterRootDeletion(t, ctx, database, scanID)
	rollbackMigration(t, ctx, database, inventory)
	assertInventoryRolledBack(t, ctx, database, scanID, toolsOperationID)

	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{inventory})
	assertInventorySchemaExists(t, ctx, database)
}

func TestSourceInventoryRollbackChainWithStoredScanAndPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, database, migrationPointers(collection.Sorted()))

	rootID, scanID, toolsOperationID := insertRollbackFixture(t, ctx, database)
	assertSourceRootWasRemoved(t, ctx, database, rootID)
	assertTerminalScanRetainedAfterRootDeletion(t, ctx, database, scanID)
	sorted := collection.Sorted()
	for index := len(sorted) - 1; index >= 0; index-- {
		migration := &sorted[index]
		if migration.Name == "20261006000000" {
			break
		}
		rollbackMigration(t, ctx, database, migration)
	}
	for _, name := range []string{"20261006000000", "20261005000000", "20261004000000", inventoryMigration} {
		rollbackMigration(t, ctx, database, migrationNamed(t, collection, name))
	}
	assertInventoryRolledBack(t, ctx, database, scanID, toolsOperationID)

	applyMigrationsOneAtATime(t, ctx, database, migrationsFrom(t, collection, inventoryMigration))
	assertInventorySchemaExists(t, ctx, database)
}

func TestNormalizedSourceAnalysisMigrationRollbackPreflightWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	ctx := context.Background()
	collection := mustMigrations(t)
	latest := migrationNamed(t, collection, "20261009000000")

	t.Run("clean schema rolls back", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, "20261009000000"))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{latest})
		rollbackMigration(t, ctx, database, latest)
		if constraintExists(t, database, "operation_normalized_analysis_shape") {
			t.Fatal("normalized analysis shape constraint remains after clean rollback")
		}
		if constraintDefinition(t, database, "operation_active_analysis_has_target_location") == "" ||
			constraintDefinition(t, database, "operation_active_analysis_has_installation") == "" {
			t.Fatal("legacy active-analysis guards were not restored after rollback")
		}
	})

	t.Run("persisted operation prevents rollback before DDL", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, "20261009000000"))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{latest})
		id := uuid.New()
		if _, err := database.ExecContext(ctx, `INSERT INTO operation
			(id,kind,state,stage,input_snapshot,attempt,created_at,updated_at,finished_at,source_analysis_mode,tools_read_required,rerun_target)
			VALUES (?, 'analyze_source','succeeded','completed','{"schema_version":1,"mode":"batch","work_ids":[],"tools":[]}',1,now(),now(),now(),'batch',false,false)`, id); err != nil {
			t.Fatalf("insert persisted normalized operation: %v", err)
		}
		migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
		if _, err := migrator.Rollback(ctx); err == nil {
			t.Fatal("rollback with a persisted normalized operation succeeded")
		}
		var count int
		if err := database.NewRaw(`SELECT count(*) FROM information_schema.columns WHERE table_name='operation' AND column_name='source_analysis_mode'`).Scan(ctx, &count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatal("failed rollback dropped normalized selector schema")
		}
	})
}

func TestSourceAnalysisStepInputsRollbackPreflightWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	ctx := context.Background()
	collection := mustMigrations(t)
	migrationName := "20261008120000"
	migration := migrationNamed(t, collection, migrationName)
	testpostgres.Reset(t, database)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, migrationName))
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

	rootID := newVariantRoot(t, ctx, database, "/srv/step-input-preflight")
	locationID := newVariantLocation(t, ctx, database, rootID, "album/track.flac")
	workID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work
		(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id)
		VALUES (?,?,?,'/srv/step-input-preflight','/srv/step-input-preflight','album/track.flac',1,now(),true,?)`,
		workID, locationID, rootID, uuid.New()); err != nil {
		t.Fatalf("insert durable analysis work: %v", err)
	}
	stepInput := `{"sha256_enabled":true,"cache_only_reuse":false,"rerun_target":false}`
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,input_snapshot)
		VALUES (?, 'sha256', 'pending', ?::jsonb)`, workID, stepInput); err != nil {
		t.Fatalf("insert durable step input: %v", err)
	}

	rollback := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	if _, err := rollback.Rollback(ctx); err == nil {
		t.Fatal("rollback with durable source-analysis step inputs succeeded")
	}
	if !columnExists(t, database, "source_analysis_step", "input_snapshot") {
		t.Fatal("failed rollback dropped durable step input schema")
	}
	var preserved int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step
		WHERE work_id=? AND step='sha256' AND input_snapshot=?::jsonb`, workID, stepInput).Scan(ctx, &preserved); err != nil {
		t.Fatalf("read durable step input after refused rollback: %v", err)
	}
	if preserved != 1 {
		t.Fatal("durable step input was removed despite rollback refusal")
	}
}

func TestObsoleteAnalysisHoldsMigrationSchemaAndPreflightWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	ctx := context.Background()
	collection := mustMigrations(t)
	name := "20261012000000"
	migration := migrationNamed(t, collection, name)

	t.Run("legacy hold refuses migration before DDL", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, name))
		installationID := newVariantInstallation(t, ctx, database, "obsolete-hold-preflight")
		if _, err := database.ExecContext(ctx, `INSERT INTO operation
			(id,kind,state,stage,input_snapshot,attempt,created_at,updated_at,finished_at,analysis_installation_id)
			VALUES (?, 'analyze_source','succeeded','applying','{}',1,now(),now(),now(),?)`, uuid.New(), installationID); err != nil {
			t.Fatalf("insert legacy analysis hold: %v", err)
		}
		migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
		if _, err := migrator.Migrate(ctx); err == nil {
			t.Fatal("migration with a persisted legacy analysis hold succeeded")
		}
		if !columnExists(t, database, "operation", "analysis_installation_id") ||
			constraintDefinition(t, database, "operation_normalized_analysis_shape") == "" {
			t.Fatal("failed migration changed the legacy analysis schema")
		}
	})

	t.Run("up removes holds and down restores the prior schema", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, name))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
		for _, column := range []string{"analysis_installation_id", "analysis_media_variant_id"} {
			if columnExists(t, database, "operation", column) {
				t.Fatalf("obsolete operation.%s column remains after migration", column)
			}
		}
		shape := constraintDefinition(t, database, "operation_normalized_analysis_shape")
		if shape == "" || strings.Contains(shape, "analysis_installation_id") || strings.Contains(shape, "analysis_media_variant_id") {
			t.Fatalf("normalized analysis shape still depends on obsolete columns: %s", shape)
		}

		rollbackMigration(t, ctx, database, migration)
		for _, column := range []string{"analysis_installation_id", "analysis_media_variant_id"} {
			if !columnExists(t, database, "operation", column) {
				t.Fatalf("operation.%s was not restored on rollback", column)
			}
		}
		if constraintDefinition(t, database, "operation_analysis_installation_id_fkey") == "" ||
			constraintDefinition(t, database, "operation_analysis_media_variant_id_fkey") == "" ||
			!indexExists(t, database, "operation_analysis_installation_idx") {
			t.Fatal("legacy analysis hold foreign keys or index were not restored")
		}
		shape = constraintDefinition(t, database, "operation_normalized_analysis_shape")
		if !strings.Contains(shape, "analysis_installation_id IS NULL") || !strings.Contains(shape, "analysis_media_variant_id IS NULL") {
			t.Fatalf("prior normalized analysis shape was not restored: %s", shape)
		}
	})
}

func mustMigrations(t *testing.T) *migrate.Migrations {
	t.Helper()
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	return collection
}

func migrationsBefore(t *testing.T, collection *migrate.Migrations, name string) []*migrate.Migration {
	t.Helper()
	var selected []*migrate.Migration
	for _, migration := range collection.Sorted() {
		if migration.Name == name {
			return selected
		}
		selected = append(selected, &migration)
	}
	t.Fatalf("migration %q not found", name)
	return nil
}

func migrationsFrom(t *testing.T, collection *migrate.Migrations, name string) []*migrate.Migration {
	t.Helper()
	var selected []*migrate.Migration
	found := false
	for _, migration := range collection.Sorted() {
		if migration.Name == name {
			found = true
		}
		if found {
			selected = append(selected, &migration)
		}
	}
	if !found {
		t.Fatalf("migration %q not found", name)
	}
	return selected
}

func migrationPointers(sorted migrate.MigrationSlice) []*migrate.Migration {
	pointers := make([]*migrate.Migration, 0, len(sorted))
	for index := range sorted {
		pointers = append(pointers, &sorted[index])
	}
	return pointers
}

func migrationNamed(t *testing.T, collection *migrate.Migrations, name string) *migrate.Migration {
	t.Helper()
	for _, migration := range collection.Sorted() {
		if migration.Name == name {
			return &migration
		}
	}
	t.Fatalf("migration %q not found", name)
	return nil
}

func applyMigrationsOneAtATime(t *testing.T, ctx context.Context, database *bun.DB, selected []*migrate.Migration) {
	t.Helper()
	for _, migration := range selected {
		collection := migrate.NewMigrations()
		collection.Add(*migration)
		migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
		if err := migrator.Init(ctx); err != nil {
			t.Fatalf("initialize migrator for %s: %v", migration.Name, err)
		}
		group, err := migrator.Migrate(ctx)
		if err != nil {
			t.Fatalf("apply migration %s: %v", migration.Name, err)
		}
		if group == nil || len(group.Migrations) != 1 || group.Migrations[0].Name != migration.Name {
			t.Fatalf("migration %s was not recorded as one migrator-applied migration", migration.Name)
		}
	}
}

func rollbackMigration(t *testing.T, ctx context.Context, database *bun.DB, migration *migrate.Migration) {
	t.Helper()
	collection := mustMigrations(t)
	migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	status, err := migrator.MigrationsWithStatus(ctx)
	if err != nil {
		t.Fatalf("read migration status before rolling back %s: %v", migration.Name, err)
	}
	latest := status.LastGroup()
	if latest == nil || len(latest.Migrations) != 1 || latest.Migrations[0].Name != migration.Name {
		t.Fatalf("latest applied migration group = %#v, want only migration %s", latest, migration.Name)
	}
	group, err := migrator.Rollback(ctx)
	if err != nil {
		t.Fatalf("rollback migration %s: %v", migration.Name, err)
	}
	if group == nil || len(group.Migrations) != 1 || group.Migrations[0].Name != migration.Name {
		t.Fatalf("rollback group = %#v, want migration %s", group, migration.Name)
	}
}

func insertRollbackFixture(t *testing.T, ctx context.Context, database *bun.DB) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	rootID, scanID, toolsOperationID := uuid.New(), uuid.New(), uuid.New()
	configuredPath := "/srv/rollback-inventory"
	if _, err := database.ExecContext(ctx, `
		INSERT INTO source_root (id, configured_path, display_name)
		VALUES (?, ?, 'Rollback inventory')`, rootID, configuredPath); err != nil {
		t.Fatalf("insert inventory root: %v", err)
	}
	snapshot := `{"schema_version":1,"source_root_id":"` + rootID.String() + `","configured_path":"` + configuredPath + `"}`
	if _, err := database.ExecContext(ctx, `
		INSERT INTO operation (id, kind, state, stage, input_snapshot, target_source_root_id, finished_at)
		VALUES (?, 'scan_source', 'succeeded', 'succeeded', ?::jsonb, ?, now())`, scanID, snapshot, rootID); err != nil {
		t.Fatalf("insert production-shaped terminal scan: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO operation (id, kind, state, stage, input_snapshot, finished_at)
		VALUES (?, 'move_tools_root', 'succeeded', 'succeeded', '{}'::jsonb, now())`, toolsOperationID); err != nil {
		t.Fatalf("insert retained tools operation: %v", err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM source_root WHERE id = ?`, rootID); err != nil {
		t.Fatalf("remove root while retaining terminal scan: %v", err)
	}
	return rootID, scanID, toolsOperationID
}

func assertSourceRootWasRemoved(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID) {
	t.Helper()
	var count int
	if err := database.NewRaw(`SELECT count(*) FROM source_root WHERE id = ?`, rootID).Scan(ctx, &count); err != nil {
		t.Fatalf("check removed source root: %v", err)
	}
	if count != 0 {
		t.Fatalf("source root rows = %d, want 0", count)
	}
}

func assertTerminalScanRetainedAfterRootDeletion(t *testing.T, ctx context.Context, database *bun.DB, scanID uuid.UUID) {
	t.Helper()
	var count int
	if err := database.NewRaw(`
		SELECT count(*) FROM operation
		WHERE id = ? AND kind = 'scan_source' AND state = 'succeeded'
		AND target_source_root_id IS NULL
		AND NOT (input_snapshot ? 'target_identity')`, scanID).Scan(ctx, &count); err != nil {
		t.Fatalf("check terminal scan retained after root deletion: %v", err)
	}
	if count != 1 {
		t.Fatalf("production-shaped terminal scan rows after root deletion = %d, want 1", count)
	}
}

func assertInventoryRolledBack(t *testing.T, ctx context.Context, database *bun.DB, scanID, toolsOperationID uuid.UUID) {
	t.Helper()
	for _, table := range []string{"source_root", "source_location", "source_scan_candidate"} {
		var relation *string
		if err := database.NewRaw(`SELECT to_regclass(?)`, "public."+table).Scan(ctx, &relation); err != nil {
			t.Fatalf("check rolled back table %s: %v", table, err)
		}
		if relation != nil {
			t.Errorf("inventory table remains after rollback: %s", table)
		}
	}
	var scanCount, toolsCount int
	if err := database.NewRaw(`SELECT count(*) FROM operation WHERE id = ?`, scanID).Scan(ctx, &scanCount); err != nil {
		t.Fatalf("check deleted scan operation: %v", err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM operation WHERE id = ? AND kind = 'move_tools_root'`, toolsOperationID).Scan(ctx, &toolsCount); err != nil {
		t.Fatalf("check retained tools operation: %v", err)
	}
	if scanCount != 0 || toolsCount != 1 {
		t.Fatalf("operation rows after rollback: scan=%d tools=%d, want scan=0 tools=1", scanCount, toolsCount)
	}
	var restoredGuardCount int
	if err := database.NewRaw(`
		SELECT count(*) FROM pg_constraint
		WHERE conname IN (
			'operation_target_identity',
			'operation_kind_check',
			'operation_mutation_has_target_installation',
			'operation_active_tools_operations_exclusive'
		) AND convalidated`).Scan(ctx, &restoredGuardCount); err != nil {
		t.Fatalf("check restored operation guards: %v", err)
	}
	if restoredGuardCount != 4 {
		t.Fatalf("validated restored operation guards = %d, want 4", restoredGuardCount)
	}
}

func assertInventorySchemaExists(t *testing.T, ctx context.Context, database *bun.DB) {
	t.Helper()
	var inventoryTables int
	if err := database.NewRaw(`
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public'
		AND table_name IN ('source_root', 'source_location', 'source_scan_candidate')`).Scan(ctx, &inventoryTables); err != nil {
		t.Fatalf("check reapplied inventory schema: %v", err)
	}
	if inventoryTables != 3 {
		t.Fatalf("reapplied inventory tables = %d, want 3", inventoryTables)
	}
}

func constraintExists(t *testing.T, database *bun.DB, name string) bool {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM pg_constraint WHERE conname = ?", name).
		Scan(context.Background(), &count); err != nil {
		t.Fatalf("read constraint %s: %v", name, err)
	}
	return count == 1
}

func TestMigrationsApplyAndRollbackWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	var tableCount int
	if err := database.NewRaw(`
		SELECT count(*)
		FROM information_schema.tables
		WHERE table_schema = 'public'
		  AND table_name IN ('app_setting', 'tool_installation', 'operation')
	`).Scan(ctx, &tableCount); err != nil {
		t.Fatalf("count setup-manager tables: %v", err)
	}
	if tableCount != 3 {
		t.Fatalf("setup-manager table count = %d, want 3", tableCount)
	}
	var validatedConstraintCount int
	if err := database.NewRaw(`
		SELECT count(*)
		FROM pg_constraint
		WHERE conname IN (
			'tool_installation_ready_is_verified',
			'operation_mutation_has_target_installation'
		)
		  AND convalidated
	`).Scan(ctx, &validatedConstraintCount); err != nil {
		t.Fatalf("count validated setup-manager constraints: %v", err)
	}
	if validatedConstraintCount != 2 {
		t.Fatalf("validated setup-manager constraints = %d, want 2", validatedConstraintCount)
	}
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	group, err := migrator.Rollback(ctx)
	if err != nil {
		t.Fatalf("rollback setup-manager migration: %v", err)
	}
	if group == nil {
		t.Fatal("rollback setup-manager migration returned no migration group")
	}
	var operationTable *string
	if err := database.NewRaw(`SELECT to_regclass('public.operation')`).Scan(ctx, &operationTable); err != nil {
		t.Fatalf("check rolled back operation table: %v", err)
	}
	if operationTable != nil {
		t.Fatalf("operation table remains after rollback: %s", *operationTable)
	}
}
