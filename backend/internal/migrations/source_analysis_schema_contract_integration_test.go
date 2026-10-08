//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const normalizedSourceAnalysisContractMigration = "20261009000000"
const perFileSourceAnalysisMigration = "20261018000000"
const minimalSourceAnalysisIntentMigration = "20261019000000"

func TestNormalizedSourceAnalysisContractMigrationWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	ctx := context.Background()
	collection := mustMigrations(t)
	migration := migrationNamed(t, collection, normalizedSourceAnalysisContractMigration)
	testpostgres.Reset(t, database)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, normalizedSourceAnalysisContractMigration))
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

	if constraintDefinition(t, database, "operation_normalized_analysis_shape") == "" {
		t.Fatal("normalized analysis shape constraint is missing")
	}
	if migrationConstraintExists(t, database, "operation_active_analysis_has_target_location") ||
		migrationConstraintExists(t, database, "operation_active_analysis_has_installation") {
		t.Fatal("legacy analysis selector constraints remain")
	}

	rootID := newVariantRoot(t, ctx, database, "/srv/normalized-schema-contract")
	locationID := newVariantLocation(t, ctx, database, rootID, "album/track.flac")
	installationID := newVariantInstallation(t, ctx, database, "normalized-schema-contract")
	_, err := database.ExecContext(ctx, `INSERT INTO operation
		(id,kind,state,stage,input_snapshot,attempt,created_at,updated_at,target_source_root_id,target_source_location_id,analysis_installation_id)
		VALUES (?, 'analyze_source','queued','queued','{}',1,now(),now(),?,?,?)`,
		uuid.New(), rootID, locationID, installationID)
	assertMigrationConstraint(t, err, "operation_normalized_analysis_shape")

	if _, err := database.ExecContext(ctx, `INSERT INTO operation
		(id,kind,state,stage,input_snapshot,attempt,created_at,updated_at,finished_at,source_analysis_mode)
		VALUES (?, 'analyze_source','succeeded','completed','{}',1,now(),now(),now(),'batch')`, uuid.New()); err != nil {
		t.Fatalf("insert terminal normalized analysis operation: %v", err)
	}
}

func TestPerFileSourceAnalysisMigrationAllowsDistinctWorkWithMinimalSnapshots(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	migration := migrationNamed(t, collection, perFileSourceAnalysisMigration)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, perFileSourceAnalysisMigration))
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

	rootID := newVariantRoot(t, ctx, database, "/srv/per-file-analysis-contract")
	locationA := newVariantLocation(t, ctx, database, rootID, "album/a.flac")
	locationB := newVariantLocation(t, ctx, database, rootID, "album/b.flac")
	workA := insertGuardClosureWork(t, ctx, database, rootID, locationA)
	workB := insertGuardClosureWork(t, ctx, database, rootID, locationB)

	insertPerFileAnalysisHold(t, ctx, database, rootID, workA)
	insertPerFileAnalysisHold(t, ctx, database, rootID, workB)
	if err := tryInsertPerFileAnalysisHold(ctx, database, rootID, workA); err == nil {
		t.Fatal("duplicate active hold for the same work was accepted")
	}
}

func TestPerFileSourceAnalysisRollbackRefusesConcurrentRootOperations(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	migration := migrationNamed(t, collection, perFileSourceAnalysisMigration)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, perFileSourceAnalysisMigration))
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

	rootID := newVariantRoot(t, ctx, database, "/srv/per-file-analysis-rollback")
	for _, path := range []string{"album/a.flac", "album/b.flac"} {
		locationID := newVariantLocation(t, ctx, database, rootID, path)
		workID := insertGuardClosureWork(t, ctx, database, rootID, locationID)
		insertPerFileAnalysisHold(t, ctx, database, rootID, workID)
	}
	if _, err := newSourceScanReaderRollbackMigrator(database, migration).Rollback(ctx); err == nil {
		t.Fatal("rollback with concurrent per-file operations succeeded")
	}
	if !indexExists(t, database, "operation_source_work_hold_one_active_work") ||
		!indexExists(t, database, "operation_one_active_source_root_scan") {
		t.Fatal("schema changed despite refusing rollback with concurrent operations")
	}
}

func TestMinimalSourceAnalysisStepIntentMigrationAndRollbackGuardsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	migration := migrationNamed(t, collection, minimalSourceAnalysisIntentMigration)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, minimalSourceAnalysisIntentMigration))

	rootID := newVariantRoot(t, ctx, database, "/srv/minimal-step-intent")
	locationID := newVariantLocation(t, ctx, database, rootID, "album/track.flac")
	workID := insertGuardClosureWork(t, ctx, database, rootID, locationID)
	operationID := uuid.New()
	const jobID int64 = 987654321
	legacy := `{"sha256_enabled":true,"cache_only_reuse":false,"rerun_target":false}`
	operationSnapshot := `{"schema_version":1,"mode":"batch","work_ids":["` + workID.String() + `"],"selected_steps":[{"work_id":"` + workID.String() + `","step":"sha256"},{"work_id":"` + workID.String() + `","step":"probe"}],"rerun_target":false}`
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO operation
			(id,kind,state,stage,input_snapshot,target_source_root_id,attempt,river_job_id,started_at,source_analysis_mode)
			VALUES (?, 'analyze_source','running','probing',?::jsonb,?,1,?,now(),'batch')`,
			operationID, operationSnapshot, rootID, jobID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO operation_source_work_hold(operation_id,work_id) VALUES (?,?)`, operationID, workID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`INSERT INTO source_analysis_step
			(work_id,step,state,input_snapshot,execution_operation_id,execution_operation_attempt,execution_job_id)
			VALUES (?,'sha256','running',?::jsonb,?,1,?)`, workID, legacy, operationID, jobID).Exec(ctx)
		return err
	}); err != nil {
		t.Fatalf("insert legacy active step fixture: %v", err)
	}
	if _, err := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true)).Migrate(ctx); err == nil {
		t.Fatal("upgrade with an incompatible active step snapshot succeeded")
	}
	if !strings.Contains(constraintDefinition(t, database, "source_analysis_step_active_input_snapshot"), "sha256_enabled") {
		t.Fatal("failed upgrade changed the active step snapshot constraint")
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET state='pending' WHERE work_id=? AND step='sha256'`, workID); err != nil {
		t.Fatalf("settle legacy active step: %v", err)
	}
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

	valid := `{"schema_version":1,"mode":"single_step","work_ids":["` + workID.String() + `"],"target_work_id":"` + workID.String() + `","target_step":"probe","rerun_target":false,"analysis_policy_version":1}`
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step
		(work_id,step,state,input_snapshot,execution_operation_id,execution_operation_attempt,execution_job_id)
		VALUES (?,'probe','running',?::jsonb,?,1,?)`, workID, valid, operationID, jobID); err != nil {
		t.Fatalf("insert minimal active step intent: %v", err)
	}
	otherWorkID := uuid.New()
	invalidIntents := []struct {
		name    string
		workID  uuid.UUID
		step    string
		prepare func(string) string
		edit    func(string) string
	}{
		{name: "work_ids disagree with target_work_id", workID: otherWorkID, step: "probe", prepare: func(snapshot string) string {
			return strings.Replace(snapshot, `"target_work_id":"`+workID.String()+`"`, `"target_work_id":"`+otherWorkID.String()+`"`, 1)
		}, edit: func(snapshot string) string { return snapshot }},
		{name: "multiple work ids", workID: otherWorkID, step: "probe", prepare: func(snapshot string) string {
			snapshot = strings.Replace(snapshot, `"work_ids":["`+workID.String()+`"]`, `"work_ids":["`+workID.String()+`","`+otherWorkID.String()+`"]`, 1)
			return strings.Replace(snapshot, `"target_work_id":"`+workID.String()+`"`, `"target_work_id":"`+otherWorkID.String()+`"`, 1)
		}, edit: func(snapshot string) string { return snapshot }},
		{name: "target step disagrees with row", workID: workID, step: "fingerprint", edit: func(snapshot string) string {
			return strings.Replace(snapshot, `"target_step":"fingerprint"`, `"target_step":"probe"`, 1)
		}},
		{name: "missing single-step mode", workID: workID, step: "sha256", edit: func(snapshot string) string {
			return strings.Replace(snapshot, `"mode":"single_step",`, "", 1)
		}},
	}
	for _, invalid := range invalidIntents {
		t.Run(invalid.name, func(t *testing.T) {
			snapshot := strings.Replace(valid, `"target_step":"probe"`, `"target_step":"`+invalid.step+`"`, 1)
			if invalid.prepare != nil {
				snapshot = invalid.prepare(snapshot)
			}
			snapshot = invalid.edit(snapshot)
			if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step
				(work_id,step,state,input_snapshot,execution_operation_id,execution_operation_attempt,execution_job_id)
				VALUES (?,?,'running',?::jsonb,?,1,?)`, invalid.workID, invalid.step, snapshot, operationID, jobID); err == nil {
				t.Fatalf("active step with invalid intent %q was accepted", invalid.name)
			}
		})
	}

	rollback := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	if _, err := rollback.Rollback(ctx); err == nil {
		t.Fatal("rollback with a minimal active step snapshot succeeded")
	}
	if !strings.Contains(constraintDefinition(t, database, "source_analysis_step_active_input_snapshot"), "analysis_policy_version") {
		t.Fatal("refused rollback changed the minimal intent constraint")
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET state='pending' WHERE work_id=? AND step='probe'`, workID); err != nil {
		t.Fatalf("settle minimal active step: %v", err)
	}
	rollbackMigration(t, ctx, database, migration)
	if !strings.Contains(constraintDefinition(t, database, "source_analysis_step_active_input_snapshot"), "sha256_enabled") {
		t.Fatal("rollback did not restore the previous active step snapshot constraint")
	}
}

func insertPerFileAnalysisHold(t *testing.T, ctx context.Context, database *bun.DB, rootID, workID uuid.UUID) {
	t.Helper()
	if err := tryInsertPerFileAnalysisHold(ctx, database, rootID, workID); err != nil {
		t.Fatalf("insert active analysis hold for work %s: %v", workID, err)
	}
}

func tryInsertPerFileAnalysisHold(ctx context.Context, database *bun.DB, rootID, workID uuid.UUID) error {
	operationID := uuid.New()
	snapshot := `{"schema_version":1,"mode":"batch","work_ids":["` + workID.String() + `"],"rerun_target":false}`
	return database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO operation
			(id,kind,state,stage,input_snapshot,target_source_root_id,attempt,source_analysis_mode)
			VALUES (?, 'analyze_source','queued','queued',?::jsonb,?,1,'batch')`, operationID, snapshot, rootID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`INSERT INTO operation_source_work_hold(operation_id,work_id) VALUES (?,?)`, operationID, workID).Exec(ctx)
		return err
	})
}

func assertMigrationConstraint(t *testing.T, err error, constraint string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), constraint) {
		t.Fatalf("error = %v, want a %s violation", err, constraint)
	}
}

func migrationConstraintExists(t *testing.T, database *bun.DB, name string) bool {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM pg_constraint WHERE conname = ?", name).
		Scan(context.Background(), &count); err != nil {
		t.Fatalf("read constraint %s: %v", name, err)
	}
	return count != 0
}
