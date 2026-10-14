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

const matchingRunQuiescenceMigration = "20261211000000"

func TestMatchingRunQuiescenceMigrationRollbackWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	ctx := context.Background()
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	migration := migrationNamed(t, collection, matchingRunQuiescenceMigration)

	t.Run("empty schema rolls back and reapplies", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, matchingRunQuiescenceMigration))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

		rollbackMigration(t, ctx, database, migration)
		if columnExists(t, database, "matching_run", "quiesced_at") {
			t.Fatal("quiesced_at remains after clean rollback")
		}
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
		if !columnExists(t, database, "matching_run", "quiesced_at") {
			t.Fatal("quiesced_at is missing after reapplying migration")
		}
	})

	t.Run("stored marker refuses rollback without changing operation", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, matchingRunQuiescenceMigration))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
		operationID := insertQuiescedMatchingRunRollbackFixture(t, ctx, database)

		migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
		if _, err := migrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "cannot roll back matching run quiescence") {
			t.Fatalf("rollback with a quiesced run error = %v, want quiescence guard", err)
		}
		if !columnExists(t, database, "matching_run", "quiesced_at") {
			t.Fatal("refused rollback dropped quiesced_at")
		}
		var preserved struct {
			State    string `bun:"state"`
			Stage    string `bun:"stage"`
			Quiesced bool   `bun:"quiesced"`
		}
		if err := database.NewRaw(`SELECT operation.state,operation.stage,matching_run.quiesced_at IS NOT NULL AS quiesced
			FROM operation JOIN matching_run ON matching_run.operation_id=operation.id WHERE operation.id=?`, operationID).Scan(ctx, &preserved); err != nil {
			t.Fatalf("read retained matching operation after refused rollback: %v", err)
		}
		if preserved.State != "running" || preserved.Stage != "evaluating" || !preserved.Quiesced {
			t.Fatalf("refused rollback changed matching operation: %+v", preserved)
		}
	})
}

func TestMatchingRunQuiescenceTimestampGuardsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()

	t.Run("insert rejects preexisting marker", func(t *testing.T) {
		runID := insertUnquiescedMatchingRunTimestampFixture(t, ctx, database)
		if _, err := database.NewRaw(`INSERT INTO matching_run(
			id,operation_id,plan_admission_key,plan_digest,immutable_plan,grouping_revision,grouping_epoch,
			provider_source_id,provider_configuration_identity,policy_version,state,fence_version,quiesced_at
		) SELECT ?,operation_id,plan_admission_key,plan_digest,immutable_plan,grouping_revision,grouping_epoch,
			provider_source_id,provider_configuration_identity,policy_version,state,fence_version,transaction_timestamp()-interval '1 day'
		  FROM matching_run WHERE id=?`, uuid.New(), runID).Exec(ctx); err == nil || !strings.Contains(err.Error(), "cannot be inserted already quiesced") {
			t.Fatalf("insert of an already-quiesced run error = %v, want insert guard", err)
		}
	})

	t.Run("backdated update is rejected and database timestamp is permanent", func(t *testing.T) {
		runID := insertUnquiescedMatchingRunTimestampFixture(t, ctx, database)
		if _, err := database.NewRaw(`UPDATE matching_run SET quiesced_at=transaction_timestamp()-interval '1 day' WHERE id=?`, runID).Exec(ctx); err == nil || !strings.Contains(err.Error(), "database-authored") {
			t.Fatalf("backdated quiescence update error = %v, want database-authored guard", err)
		}
		if _, err := database.NewRaw(`UPDATE matching_run SET quiesced_at=transaction_timestamp() WHERE id=?`, runID).Exec(ctx); err != nil {
			t.Fatalf("set database-authored quiescence timestamp: %v", err)
		}
		var markerPresent bool
		if err := database.NewRaw(`SELECT quiesced_at IS NOT NULL FROM matching_run WHERE id=?`, runID).Scan(ctx, &markerPresent); err != nil {
			t.Fatalf("read quiescence timestamp: %v", err)
		}
		if !markerPresent {
			t.Fatal("database-authored quiescence timestamp was not stored")
		}
		for _, update := range []string{
			`UPDATE matching_run SET quiesced_at=NULL WHERE id=?`,
			`UPDATE matching_run SET quiesced_at=quiesced_at+interval '1 second' WHERE id=?`,
		} {
			if _, err := database.NewRaw(update, runID).Exec(ctx); err == nil || !strings.Contains(err.Error(), "write-once") {
				t.Fatalf("change to permanent quiescence timestamp using %q error = %v, want write-once guard", update, err)
			}
		}
	})
}

func insertUnquiescedMatchingRunTimestampFixture(t *testing.T, ctx context.Context, database *bun.DB) uuid.UUID {
	t.Helper()
	runID := uuid.New()
	providerID, sourceID := uuid.New(), uuid.New()
	configuration := "quiescence-timestamp-fixture"
	if _, err := database.NewRaw(`INSERT INTO provider(id,code) VALUES (?,?)`, providerID, "migration-test-"+providerID.String()).Exec(ctx); err != nil {
		t.Fatalf("insert matching provider: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO provider_source(id,provider_id,namespace,endpoint,configuration_identity) VALUES (?,?,?,?,?)`,
		sourceID, providerID, "migration-test", "https://example.invalid", configuration).Exec(ctx); err != nil {
		t.Fatalf("insert matching provider source: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,created_at,updated_at)
		VALUES (?, 'matching_evaluation_v1','running','evaluating','{}'::jsonb,1,now(),now())`, runID).Exec(ctx); err != nil {
		t.Fatalf("insert matching operation: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_run(
			id,operation_id,plan_admission_key,plan_digest,immutable_plan,grouping_revision,grouping_epoch,
			provider_source_id,provider_configuration_identity,policy_version,state,fence_version
		) VALUES (?,?,?,?, '{}'::jsonb,0,0,?,?,?,'queued',2)`, runID, runID, "migration-test-"+runID.String(),
		make([]byte, 32), sourceID, configuration, "migration-test-policy").Exec(ctx); err != nil {
		t.Fatalf("insert unquiesced version-2 matching run: %v", err)
	}
	return runID
}

// This fixture exercises the migration-11 rollback preflight with a valid run.
// It is not a quiescence proof; owned-run behavior is covered with an admitted
// run in the persistence integration tests.
func insertQuiescedMatchingRunRollbackFixture(t *testing.T, ctx context.Context, database *bun.DB) uuid.UUID {
	t.Helper()
	runID := uuid.New()
	providerID, sourceID := uuid.New(), uuid.New()
	configuration := "quiescence-rollback-fixture"
	if _, err := database.NewRaw(`INSERT INTO provider(id,code) VALUES (?,?)`, providerID, "migration-test-"+providerID.String()).Exec(ctx); err != nil {
		t.Fatalf("insert matching provider: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO provider_source(id,provider_id,namespace,endpoint,configuration_identity) VALUES (?,?,?,?,?)`,
		sourceID, providerID, "migration-test", "https://example.invalid", configuration).Exec(ctx); err != nil {
		t.Fatalf("insert matching provider source: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,created_at,updated_at)
		VALUES (?, 'matching_evaluation_v1','running','evaluating','{}'::jsonb,1,now(),now())`, runID).Exec(ctx); err != nil {
		t.Fatalf("insert matching operation: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_run(
			id,operation_id,plan_admission_key,plan_digest,immutable_plan,grouping_revision,grouping_epoch,
			provider_source_id,provider_configuration_identity,policy_version,state,fence_version,quiesced_at
		) VALUES (?,?,?,?, '{}'::jsonb,0,0,?,?,?,'queued',2,NULL)`, runID, runID, "migration-test-"+runID.String(),
		make([]byte, 32), sourceID, configuration, "migration-test-policy").Exec(ctx); err != nil {
		t.Fatalf("insert unquiesced version-2 matching run: %v", err)
	}
	if _, err := database.NewRaw(`UPDATE matching_run SET quiesced_at=transaction_timestamp() WHERE id=?`, runID).Exec(ctx); err != nil {
		t.Fatalf("mark version-2 matching run quiesced: %v", err)
	}
	return runID
}
