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

const obsoleteQuiescedRetirementMigration = "20261212000000"
const failedObsoleteDisposalMigration = "20261213000000"

// TestObsoleteQuiescedMatchingRetirementRollbackWithPostgreSQL exercises the
// migration-12 down guard that refuses any quiesced matching run (and any
// obsolete-quiesced authorization) before dropping the write-once timestamp
// guard and the authorization kind schema. The empty and marked cases reuse
// the migration-11 rollback template and the quiescence fixtures.
func TestObsoleteQuiescedMatchingRetirementRollbackWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	ctx := context.Background()
	collection := mustMigrations(t)
	migration := migrationNamed(t, collection, obsoleteQuiescedRetirementMigration)

	t.Run("empty schema rolls back and reapplies", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, obsoleteQuiescedRetirementMigration))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

		rollbackMigration(t, ctx, database, migration)
		if columnExists(t, database, "matching_run_retirement_authorization", "authorization_kind") {
			t.Fatal("authorization_kind remains after clean rollback")
		}
		if constraintExists(t, database, "matching_run_retirement_authorization_obsolete_v2") {
			t.Fatal("obsolete authorization constraint remains after clean rollback")
		}
		if triggerExists(t, database, "matching_run_quiescence_timestamp_immutable") {
			t.Fatal("quiescence timestamp trigger remains after clean rollback")
		}
		if functionExists(t, database, "protect_matching_run_quiescence_timestamp") {
			t.Fatal("quiescence timestamp function remains after clean rollback")
		}
		if restored := matchingRunRetirementAuthorizationFunction(t, ctx, database); strings.Contains(restored, "obsolete_quiesced") {
			t.Fatalf("prior retirement authorization guard was not restored on rollback: %s", restored)
		}

		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
		if !columnExists(t, database, "matching_run_retirement_authorization", "authorization_kind") {
			t.Fatal("authorization_kind is missing after reapplying migration")
		}
		if !constraintExists(t, database, "matching_run_retirement_authorization_obsolete_v2") {
			t.Fatal("obsolete authorization constraint is missing after reapplying migration")
		}
		if !triggerExists(t, database, "matching_run_quiescence_timestamp_immutable") {
			t.Fatal("quiescence timestamp trigger is missing after reapplying migration")
		}
		if !functionExists(t, database, "protect_matching_run_quiescence_timestamp") {
			t.Fatal("quiescence timestamp function is missing after reapplying migration")
		}
		if reapplied := matchingRunRetirementAuthorizationFunction(t, ctx, database); !strings.Contains(reapplied, "obsolete_quiesced") {
			t.Fatalf("obsolete quiesced authorization guard was not reapplied: %s", reapplied)
		}
	})

	t.Run("quiesced run refuses rollback and preserves marker, status, and schema", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, obsoleteQuiescedRetirementMigration))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
		runID := insertQuiescedMatchingRunRollbackFixture(t, ctx, database)
		markerBefore, stateBefore, stageBefore := readQuiescedRunRollbackState(t, ctx, database, runID)
		if markerBefore == "" || stateBefore != "running" || stageBefore != "evaluating" {
			t.Fatalf("rollback fixture state = marker:%q state:%q stage:%q", markerBefore, stateBefore, stageBefore)
		}

		migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
		if _, err := migrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "cannot roll back obsolete quiesced matching retirement while quiesced runs remain") {
			t.Fatalf("rollback with a quiesced run error = %v, want quiesced-run guard", err)
		}

		markerAfter, stateAfter, stageAfter := readQuiescedRunRollbackState(t, ctx, database, runID)
		if markerAfter == "" || markerAfter != markerBefore {
			t.Fatalf("refused rollback changed quiescence marker: before=%q after=%q", markerBefore, markerAfter)
		}
		if stateAfter != stateBefore || stageAfter != stageBefore {
			t.Fatalf("refused rollback changed matching operation status: before=%q/%q after=%q/%q", stateBefore, stageBefore, stateAfter, stageAfter)
		}
		if !triggerExists(t, database, "matching_run_quiescence_timestamp_immutable") ||
			!functionExists(t, database, "protect_matching_run_quiescence_timestamp") {
			t.Fatal("refused rollback dropped the quiescence timestamp guard")
		}
		if !columnExists(t, database, "matching_run_retirement_authorization", "authorization_kind") ||
			!constraintExists(t, database, "matching_run_retirement_authorization_obsolete_v2") {
			t.Fatal("refused rollback changed the retirement authorization schema")
		}

		if _, err := database.NewRaw(`UPDATE matching_run SET quiesced_at=NULL WHERE id=?`, runID).Exec(ctx); err == nil || !strings.Contains(err.Error(), "write-once") {
			t.Fatalf("clearing quiescence after refused rollback error = %v, want write-once guard", err)
		}
	})

	t.Run("migration 13 down restores migration 12 authorization function", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, obsoleteQuiescedRetirementMigration))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
		before := matchingRunRetirementAuthorizationFunction(t, ctx, database)

		migration13 := migrationNamed(t, collection, failedObsoleteDisposalMigration)
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration13})
		rollbackMigration(t, ctx, database, migration13)
		after := matchingRunRetirementAuthorizationFunction(t, ctx, database)
		if normalizeFunctionDefinition(after) != normalizeFunctionDefinition(before) {
			t.Fatalf("migration 13 down did not restore migration 12 authorization function verbatim:\nbefore: %s\nafter:  %s", before, after)
		}
	})

	t.Run("migration 13 down refuses retained failed quiesced runs", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, obsoleteQuiescedRetirementMigration))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
		migration13 := migrationNamed(t, collection, failedObsoleteDisposalMigration)
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration13})
		runID := insertQuiescedMatchingRunRollbackFixture(t, ctx, database)
		if _, err := database.NewRaw(`UPDATE operation SET state='failed',stage='interrupted',safe_error='fixture failure',finished_at=transaction_timestamp() WHERE id=?`, runID).Exec(ctx); err != nil {
			t.Fatalf("seed retained failed quiesced operation: %v", err)
		}
		functionBefore := matchingRunRetirementAuthorizationFunction(t, ctx, database)

		migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
		if _, err := migrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "cannot roll back failed obsolete matching disposal while failed quiesced runs remain") {
			t.Fatalf("rollback with failed quiesced run error = %v, want failed-run preflight", err)
		}
		if functionAfter := matchingRunRetirementAuthorizationFunction(t, ctx, database); normalizeFunctionDefinition(functionAfter) != normalizeFunctionDefinition(functionBefore) {
			t.Fatalf("refused migration 13 rollback changed retirement authorization function:\nbefore: %s\nafter: %s", functionBefore, functionAfter)
		}
		marker, state, _ := readQuiescedRunRollbackState(t, ctx, database, runID)
		if marker == "" || state != "failed" {
			t.Fatalf("refused migration 13 rollback changed retained failed execution marker/status: marker=%q state=%q", marker, state)
		}
	})
}

func normalizeFunctionDefinition(definition string) string {
	return strings.Join(strings.Fields(definition), " ")
}

func readQuiescedRunRollbackState(t *testing.T, ctx context.Context, database *bun.DB, runID uuid.UUID) (string, string, string) {
	t.Helper()
	var state struct {
		Marker string `bun:"marker"`
		State  string `bun:"state"`
		Stage  string `bun:"stage"`
	}
	if err := database.NewRaw(`SELECT COALESCE(matching_run.quiesced_at::text,'') AS marker,operation.state,operation.stage
		FROM matching_run JOIN operation ON operation.id=matching_run.operation_id WHERE matching_run.id=?`, runID).Scan(ctx, &state); err != nil {
		t.Fatalf("read quiesced run rollback state: %v", err)
	}
	return state.Marker, state.State, state.Stage
}

func matchingRunRetirementAuthorizationFunction(t *testing.T, ctx context.Context, database *bun.DB) string {
	t.Helper()
	var definition string
	if err := database.NewRaw(`SELECT pg_get_functiondef('validate_matching_run_retirement_authorization()'::regprocedure)`).Scan(ctx, &definition); err != nil {
		t.Fatalf("read retirement authorization function: %v", err)
	}
	return definition
}

func triggerExists(t *testing.T, database *bun.DB, name string) bool {
	t.Helper()
	var count int
	if err := database.NewRaw(`SELECT count(*) FROM pg_trigger WHERE tgname=? AND NOT tgisinternal`, name).
		Scan(context.Background(), &count); err != nil {
		t.Fatalf("read trigger %s: %v", name, err)
	}
	return count == 1
}

func functionExists(t *testing.T, database *bun.DB, name string) bool {
	t.Helper()
	var count int
	if err := database.NewRaw(`SELECT count(*) FROM pg_proc WHERE proname=?`, name).
		Scan(context.Background(), &count); err != nil {
		t.Fatalf("read function %s: %v", name, err)
	}
	return count == 1
}
