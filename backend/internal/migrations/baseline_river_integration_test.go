//go:build integration

package migrations_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestCurrentSchemaRollbackPreservesRiverAndUnrelatedData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := testpostgres.Open(t)
	if err := migrations.Apply(ctx, database); err != nil {
		t.Fatal(err)
	}
	var tableCount int
	if err := database.NewRaw(`SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname='public' AND c.relkind='r' AND c.relname NOT LIKE 'bun\_%' ESCAPE '\'`).Scan(ctx, &tableCount); err != nil {
		t.Fatal(err)
	}
	const currentApplicationTables = 120
	if tableCount != currentApplicationTables {
		t.Fatalf("current schema contains %d application tables, want %d", tableCount, currentApplicationTables)
	}
	river, err := rivermigrate.New(riverdatabasesql.New(database.DB), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := river.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := database.NewRaw(`INSERT INTO river_job(kind,args) VALUES ('baseline_rollback_probe','{}') RETURNING id`).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `CREATE TABLE unrelated_data(id integer PRIMARY KEY); INSERT INTO unrelated_data VALUES (7)`); err != nil {
		t.Fatal(err)
	}
	var riverHistoryBefore int
	if err := database.NewRaw(`SELECT count(*) FROM river_migration`).Scan(ctx, &riverHistoryBefore); err != nil {
		t.Fatal(err)
	}
	if riverHistoryBefore == 0 {
		t.Fatal("River migration fixture has no history")
	}
	if _, err := migrations.RollbackLastGroup(ctx, database); err != nil {
		t.Fatalf("roll back empty current application schema alongside River: %v", err)
	}
	var jobs, riverHistoryAfter, unrelated int
	if err := database.NewRaw(`SELECT count(*) FROM river_job WHERE id=?`, jobID).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM river_migration`).Scan(ctx, &riverHistoryAfter); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM unrelated_data WHERE id=7`).Scan(ctx, &unrelated); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || unrelated != 1 || riverHistoryAfter != riverHistoryBefore {
		t.Fatalf("application rollback changed other data: jobs=%d unrelated=%d River history=%d (was %d)", jobs, unrelated, riverHistoryAfter, riverHistoryBefore)
	}
	if err := migrations.Apply(ctx, database); err != nil {
		t.Fatalf("reapply current application schema alongside River: %v", err)
	}
}

func TestBaselineRollbackRefusesExternalDependency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.Open(t)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE VIEW unrelated_settings_view AS SELECT setting_name FROM public.app_setting`); err != nil {
		t.Fatal(err)
	}

	_, err := migrations.RollbackLastGroup(ctx, db)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "2BP01" {
		t.Fatalf("rollback with external view = %v; want dependent objects error", err)
	}
	if relationName(t, db, "unrelated_settings_view") == nil || relationName(t, db, "app_setting") == nil {
		t.Fatal("refused rollback removed the external view or its table")
	}
	assertBaselineHistory(t, db, 1)

	if _, err := db.ExecContext(ctx, `DROP VIEW unrelated_settings_view`); err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.RollbackLastGroup(ctx, db); err != nil {
		t.Fatalf("roll back baseline after removing external dependency: %v", err)
	}
}

func TestBaselineRollbackChecksWritesCommittedWhileWaiting(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback() }()
	var writerPID int
	if err := writer.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &writerPID); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(ctx, `INSERT INTO public.app_setting (setting_name, setting_value) VALUES ('rollback-race', 'retain')`); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := migrations.RollbackLastGroup(ctx, db)
		result <- err
	}()

	// Wait for rollback to block on the writer before making the row visible.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := db.NewRaw(`SELECT EXISTS (
			SELECT FROM pg_stat_activity WHERE datname = current_database()
			AND ? = ANY (pg_blocking_pids(pid))
		)`, writerPID).Scan(ctx, &waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("rollback finished instead of waiting for the writer: %v", err)
		case <-ctx.Done():
			t.Fatalf("waiting for rollback to block on the writer: %v", ctx.Err())
		case <-ticker.C:
		}
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("rollback succeeded after the writer committed a populated row")
		}
	case <-ctx.Done():
		t.Fatalf("waiting for rollback to refuse the populated database: %v", ctx.Err())
	}
	var value string
	if err := db.NewRaw(`SELECT setting_value FROM public.app_setting WHERE setting_name = 'rollback-race'`).Scan(ctx, &value); err != nil || value != "retain" {
		t.Fatalf("concurrently committed setting after refused rollback = %q, %v", value, err)
	}
	assertBaselineHistory(t, db, 1)
}
