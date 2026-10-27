//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/migrate"
)

const publicationJournalMigration = "20261030000007"

func TestPublicationMutationJournalRetirementPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	all, err := migrations.Collection()
	if err != nil {
		t.Fatal(err)
	}
	prior, target := migrate.NewMigrations(), migrate.NewMigrations()
	for _, migration := range all.Sorted() {
		if migration.Name < publicationJournalMigration {
			prior.Add(migration)
		}
		if migration.Name == publicationJournalMigration {
			target.Add(migration)
		}
	}
	if len(target.Sorted()) != 1 {
		t.Fatal("journal retirement migration is missing")
	}
	priorMigrator := migrate.NewMigrator(db, prior, migrate.WithMarkAppliedOnSuccess(true))
	if err := priorMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := priorMigrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	river, err := rivermigrate.New(riverdatabasesql.New(db.DB), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := river.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	position, operation, release, track, epoch := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	var jobID int64
	if err := db.NewRaw(`INSERT INTO river_job(kind,args,attempt,state,attempted_at,finalized_at)
		VALUES ('publish_track_v1',jsonb_build_object('operation_id',?::text),1,'discarded',now(),now()) RETURNING id`, operation).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,'attempt')`, []any{release}},
		{`INSERT INTO local_track(id,title) VALUES (?,'attempt')`, []any{track}},
		{`INSERT INTO release_track(id,release_id,track_id,medium,position) VALUES (?,?,?,0,0)`, []any{position, release, track}},
		{`INSERT INTO operation(id,kind,state,stage,input_snapshot,river_job_id,safe_error,finished_at)
			VALUES (?,'publish_track_v1','failed','fixture',jsonb_build_object('version',1,'position_id',?::text,'output_epoch',?::text,'intent_kind','audio'),?,'fixture',now())`, []any{operation, position, epoch, jobID}},
	} {
		if _, err := db.ExecContext(ctx, seed.query, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
	insertAttempt := func(phase, journal string, frozen any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO track_publication_attempt
			(operation_id,position_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest,mutation_journal)
			VALUES (?,?,?,?,'audio',?,?::jsonb,'{"target":"track.flac"}'::jsonb,?::jsonb)`, operation, position, epoch, uuid.New(), phase, frozen, journal); err != nil {
			t.Fatalf("valid pre-migration fixture phase=%s: %v", phase, err)
		}
	}
	apply := func() error {
		_, err := migrate.NewMigrator(db, target, migrate.WithMarkAppliedOnSuccess(true)).Migrate(ctx)
		return err
	}
	rollback := func() {
		t.Helper()
		if _, err := migrate.NewMigrator(db, target, migrate.WithMarkAppliedOnSuccess(true)).Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}
	readJSON := func(query string) string {
		t.Helper()
		var value string
		if err := db.NewRaw(query).Scan(ctx, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	assertJSON := func(query, expected string) {
		t.Helper()
		var same bool
		if err := db.NewRaw(`SELECT (`+query+`) = ?::jsonb`, expected).Scan(ctx, &same); err != nil || !same {
			t.Fatalf("retained evidence changed: same=%v err=%v", same, err)
		}
	}
	attemptJSON := `SELECT to_jsonb(a)-'mutation_journal' FROM track_publication_attempt a`
	claimJSON := `SELECT to_jsonb(c) FROM publication_execution_claim c`
	historyJSON := `SELECT COALESCE(jsonb_agg(to_jsonb(m) ORDER BY id),'[]'::jsonb) FROM bun_migrations m`
	for _, phase := range []string{"queued", "preparing", "staged", "validated", "exposing"} {
		t.Run("preserve/"+phase, func(t *testing.T) {
			insertAttempt(phase, "[]", `{"preserved":true}`)
			if _, err := db.ExecContext(ctx, `INSERT INTO publication_execution_claim
				(operation_id,application_attempt,river_job_id,river_attempt,claim_uuid,boot_uuid,delivery_token,epoch,state)
				VALUES (?,1,?,1,?,?,'retained recovery',?,'recovery_required')`, operation, jobID, uuid.New(), uuid.New(), epoch); err != nil {
				t.Fatal(err)
			}
			before, claim := readJSON(attemptJSON), readJSON(claimJSON)
			if err := apply(); err != nil {
				t.Fatalf("upgrade valid %s: %v", phase, err)
			}
			var exists bool
			if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='track_publication_attempt' AND column_name='mutation_journal')`).Scan(ctx, &exists); err != nil || exists {
				t.Fatalf("journal column remains: %v, %v", exists, err)
			}
			assertJSON(attemptJSON, before)
			assertJSON(claimJSON, claim)
			for _, invalid := range []any{nil, `null`, `[]`, `"text"`} {
				if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=?::jsonb WHERE operation_id=?`, invalid, operation); err == nil || !strings.Contains(err.Error(), "track_publication_attempt_frozen_inputs_phase_check") {
					t.Fatalf("accepted invalid frozen inputs %v: %v", invalid, err)
				}
			}
			rollback()
			assertJSON(attemptJSON, before)
			assertJSON(claimJSON, claim)
			var neutral bool
			if err := db.NewRaw(`SELECT mutation_journal='[]'::jsonb FROM track_publication_attempt`).Scan(ctx, &neutral); err != nil || !neutral {
				t.Fatalf("restored journal is not neutral: %v %v", neutral, err)
			}
			var noDefault bool
			if err := db.NewRaw(`SELECT column_default IS NULL FROM information_schema.columns WHERE table_schema='public' AND table_name='track_publication_attempt' AND column_name='mutation_journal'`).Scan(ctx, &noDefault); err != nil || !noDefault {
				t.Fatalf("rollback changed original column default: %v %v", noDefault, err)
			}
			if err := apply(); err != nil {
				t.Fatalf("reapply journal retirement: %v", err)
			}
			assertJSON(attemptJSON, before)
			assertJSON(claimJSON, claim)
			rollback()
			for _, query := range []string{`DELETE FROM publication_execution_claim`, `DELETE FROM track_publication_attempt`} {
				if _, err := db.ExecContext(ctx, query); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	for _, scenario := range []struct {
		phase, journal string
		frozen         any
	}{
		{"queued", `[{"mutation":"unsafe"}]`, `{}`},
		{"committed", `[]`, nil}, {"cleaning", `[]`, nil}, {"cleanup_pending", `[]`, nil}, {"recovery_required", `[]`, `{}`},
	} {
		t.Run("refuse/"+scenario.phase, func(t *testing.T) {
			insertAttempt(scenario.phase, scenario.journal, scenario.frozen)
			row, history := readJSON(`SELECT to_jsonb(a) FROM track_publication_attempt a`), readJSON(historyJSON)
			if err := apply(); err == nil || !strings.Contains(err.Error(), "cannot retire publication mutation journal") {
				t.Fatalf("unsafe upgrade error=%v", err)
			}
			assertJSON(`SELECT to_jsonb(a) FROM track_publication_attempt a`, row)
			assertJSON(historyJSON, history)
			if _, err := db.ExecContext(ctx, `DELETE FROM track_publication_attempt`); err != nil {
				t.Fatal(err)
			}
		})
	}
}
