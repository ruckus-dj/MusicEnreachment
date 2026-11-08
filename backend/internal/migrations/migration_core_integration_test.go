//go:build integration

package migrations_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

// The fresh-instance schema is represented by ordered baseline migrations.
var baselineMigrationNames = []string{
	"20261015000001",
	"20261015000002",
	"20261015000003",
	"20261015000004",
	"20261015000005",
	"20261108000001",
	"20261108000002",
	"20261108000003",
}

var currentMigrationNames = append([]string(nil), baselineMigrationNames...)

var preSourceRepublishResultMigrationNames = []string{
	"20261015000001",
	"20261015000002",
	"20261015000003",
	"20261015000004",
	"20261015000005",
}

func TestCurrentMigrationsApplyIdempotentlyAndRollbackAsOneGroup(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE river_job (id bigint PRIMARY KEY, state text NOT NULL)`); err != nil {
		t.Fatalf("create River-owned table fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO river_job(id,state) VALUES(1,'available')`); err != nil {
		t.Fatalf("insert River-owned row: %v", err)
	}
	collection := loadMigrations(t)
	assertMigrationInventory(t, collection, currentMigrationNames)

	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize current migrations: %v", err)
	}
	group, err := migrator.Migrate(ctx)
	if err != nil {
		t.Fatalf("apply current migrations: %v", err)
	}
	assertMigrationGroup(t, group, currentMigrationNames, "applied")
	assertCurrentSchema(t, db)
	if group, err = migrator.Migrate(ctx); err != nil || group == nil || len(group.Migrations) != 0 {
		t.Fatalf("reapplying current schema = %#v, %v; want an empty migration group", group, err)
	}

	rolledBack, err := migrator.Rollback(ctx)
	if err != nil {
		t.Fatalf("rollback clean migration group: %v", err)
	}
	assertMigrationGroup(t, rolledBack, currentMigrationNames, "rolled back")
	for _, table := range []string{"app_setting", "operation", "source_root", "media_metadata_result", "release_track", "publication_manual_lyrics_receipt"} {
		if relationName(t, db, table) != nil {
			t.Errorf("application table %s remains after current rollback", table)
		}
	}
	var riverState string
	if err := db.NewRaw(`SELECT state FROM river_job WHERE id=1`).Scan(ctx, &riverState); err != nil || riverState != "available" {
		t.Fatalf("River data after clean rollback = %q, %v; want unchanged", riverState, err)
	}
	group, err = migrator.Migrate(ctx)
	if err != nil {
		t.Fatalf("reapply current migration group: %v", err)
	}
	assertMigrationGroup(t, group, currentMigrationNames, "reapplied")
	assertCurrentSchema(t, db)
}

func TestUnsafeBaselineRollbackIsAtomic(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	ctx := context.Background()
	// The complete fresh-instance baseline has one atomic rollback group.
	collection := loadMigrations(t)
	assertMigrationInventory(t, collection, baselineMigrationNames)
	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize baseline migrations: %v", err)
	}
	if _, err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("apply baseline migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE river_job (id bigint PRIMARY KEY, state text NOT NULL)`); err != nil {
		t.Fatalf("create River-owned table fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO river_job(id,state) VALUES(1,'available')`); err != nil {
		t.Fatalf("insert River-owned row: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value) VALUES('rollback-guard','preserve')`); err != nil {
		t.Fatalf("insert rollback-guard data: %v", err)
	}
	artistID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_artist(id,name,metadata) VALUES(?,?,?::jsonb)`, artistID, "Rollback Guard Artist", `{"local_metadata":{"version":1,"scope":"artist","desired":{"genre":{"state":"explicit_empty","authority":"manual","provenance":{"kind":"manual"}}}}}`).Exec(ctx); err != nil {
		t.Fatalf("insert edited local artist: %v", err)
	}
	statusBefore, err := migrator.MigrationsWithStatus(ctx)
	if err != nil {
		t.Fatalf("read migration status before unsafe rollback: %v", err)
	}
	var schemaTablesBefore int
	if err := db.NewRaw(`SELECT count(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(ctx, &schemaTablesBefore); err != nil {
		t.Fatalf("count application tables before unsafe rollback: %v", err)
	}
	if _, err := migrator.Rollback(ctx); err == nil {
		t.Fatal("rollback with live application data succeeded")
	}
	var setting string
	if err := db.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='rollback-guard'`).Scan(ctx, &setting); err != nil || setting != "preserve" {
		t.Fatalf("rollback-guard data after refusal = %q, %v; want unchanged", setting, err)
	}
	var artistGenreState string
	if err := db.NewRaw(`SELECT metadata->'local_metadata'->'desired'->'genre'->>'state' FROM local_artist WHERE id=?`, artistID).Scan(ctx, &artistGenreState); err != nil || artistGenreState != "explicit_empty" {
		t.Fatalf("edited artist genre state after refusal = %q, %v; want preserved", artistGenreState, err)
	}
	var riverState string
	if err := db.NewRaw(`SELECT state FROM river_job WHERE id=1`).Scan(ctx, &riverState); err != nil || riverState != "available" {
		t.Fatalf("River data after refusal = %q, %v; want unchanged", riverState, err)
	}
	var riverTable string
	if err := db.NewRaw(`SELECT to_regclass('public.river_job')::text`).Scan(ctx, &riverTable); err != nil || riverTable == "" {
		t.Fatalf("River table after refusal = %q, %v; want it intact", riverTable, err)
	}
	statusAfter, err := migrator.MigrationsWithStatus(ctx)
	if err != nil {
		t.Fatalf("read migration status after unsafe rollback: %v", err)
	}
	if fmt.Sprint(statusAfter) != fmt.Sprint(statusBefore) {
		t.Fatalf("migration status changed after refused rollback: before=%#v after=%#v", statusBefore, statusAfter)
	}
	var schemaTablesAfter int
	if err := db.NewRaw(`SELECT count(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(ctx, &schemaTablesAfter); err != nil {
		t.Fatalf("count application tables after unsafe rollback: %v", err)
	}
	if schemaTablesAfter != schemaTablesBefore {
		t.Fatalf("schema table count after refused rollback = %d, want unchanged count %d", schemaTablesAfter, schemaTablesBefore)
	}
}

func TestSourceRepublishCoverPreparationRollbackRetainsOutcome(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	ctx := context.Background()
	collection := loadMigrations(t)
	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'cover rollback')`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'cover rollback')`, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, positionID, releaseID, trackID); err != nil {
		t.Fatal(err)
	}
	coverProjection, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,cover_embedding,quality_policy_version,validated_at)
		VALUES (?,?,?,'Track.flac','{}','{}','{}','{}',jsonb_build_object(
		'version',2,'generation',?::text,'receipt_operation_id',?::text,'receipt_release_id',?::text,
		'receipt_revision',1,'receipt_mode','automatic','requested_generation',?::text,
		'requested_sha256',repeat('a',64),'outcome','preparation_failed','warning_code','decode_failed'),1,now())`,
		positionID, uuid.New(), uuid.New(), uuid.New(), uuid.New(), releaseID, uuid.New())
	if err != nil {
		t.Fatalf("insert retained preparation failure projection: %v", err)
	}
	if _, err := coverProjection.RowsAffected(); err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Rollback(ctx); err == nil {
		t.Fatal("rollback discarded a retained cover preparation failure outcome")
	}
	var retained bool
	if err := db.NewRaw(`SELECT cover_embedding->>'warning_code'='decode_failed' FROM track_publication WHERE position_id=?`, positionID).Scan(ctx, &retained); err != nil || !retained {
		t.Fatalf("retained preparation failure after rollback refusal = %t, %v", retained, err)
	}
}

func TestRecoveredCoverProjectionConstraintAndRollback(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	ctx := context.Background()
	collection := loadMigrations(t)
	prior, target := migrate.NewMigrations(), migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		if migration.Name < "20261108000003" {
			prior.Add(migration)
		} else if migration.Name == "20261108000003" {
			target.Add(migration)
		}
	}
	if len(target.Sorted()) != 1 {
		t.Fatalf("recovered optional projection migrations = %d, want one", len(target.Sorted()))
	}
	priorMigrator := migrate.NewMigrator(db, prior, migrate.WithMarkAppliedOnSuccess(true))
	if err := priorMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := priorMigrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	targetMigrator := migrate.NewMigrator(db, target, migrate.WithMarkAppliedOnSuccess(true))
	if err := targetMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := targetMigrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	insertProjection := func(positionID, releaseID uuid.UUID, projection string) error {
		trackID := uuid.New()
		if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'recovered cover')`, releaseID); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'recovered cover')`, trackID); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, positionID, releaseID, trackID); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,cover_embedding,quality_policy_version,validated_at)
			VALUES (?,?,?,'Track.flac','{}','{}','{}','{}',?::jsonb,1,now())`, positionID, uuid.New(), uuid.New(), projection)
		return err
	}

	// Version 1 keeps its pre-recovery contract.
	v1Position := uuid.New()
	v1Projection := fmt.Sprintf(`{"version":1,"generation":%q,"receipt_operation_id":%q,"receipt_release_id":%q,"receipt_revision":1,"receipt_mode":"automatic","requested_generation":%q,"requested_sha256":"%s","outcome":"mismatch","warning_code":"cover_mismatch"}`,
		uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64))
	if err := insertProjection(v1Position, uuid.New(), v1Projection); err != nil {
		t.Fatalf("insert legacy v1 cover projection: %v", err)
	}

	// The new v2 outcome requires all projection keys and its exact diagnostic.
	recoveredPosition, recoveredRelease := uuid.New(), uuid.New()
	recoveredProjection := fmt.Sprintf(`{"version":2,"generation":%q,"receipt_operation_id":%q,"receipt_release_id":%q,"receipt_revision":1,"receipt_mode":"automatic","requested_generation":%q,"requested_sha256":"%s","outcome":"absent_after_interruption","warning_code":"cover_absent_after_interruption"}`,
		uuid.NewString(), uuid.NewString(), recoveredRelease.String(), uuid.NewString(), strings.Repeat("b", 64))
	if err := insertProjection(recoveredPosition, recoveredRelease, recoveredProjection); err != nil {
		t.Fatalf("insert recovered cover absence projection: %v", err)
	}

	malformedPosition := uuid.New()
	malformed := fmt.Sprintf(`{"version":2,"generation":%q,"receipt_operation_id":%q,"receipt_release_id":%q,"receipt_revision":1,"receipt_mode":"automatic","requested_generation":%q,"requested_sha256":"%s","outcome":"absent_after_interruption"}`,
		uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), strings.Repeat("c", 64))
	if err := insertProjection(malformedPosition, uuid.New(), malformed); err == nil {
		t.Fatal("cover projection with missing required warning_code was accepted")
	}

	if _, err := targetMigrator.Rollback(ctx); err == nil {
		t.Fatal("rollback discarded retained recovered cover outcome")
	}
	var retained bool
	if err := db.NewRaw(`SELECT cover_embedding->>'warning_code'='cover_absent_after_interruption' FROM track_publication WHERE position_id=?`, recoveredPosition).Scan(ctx, &retained); err != nil || !retained {
		t.Fatalf("recovered cover outcome retained after rollback refusal = %t, %v", retained, err)
	}
	var legacyRetained bool
	if err := db.NewRaw(`SELECT cover_embedding->>'outcome'='mismatch' FROM track_publication WHERE position_id=?`, v1Position).Scan(ctx, &legacyRetained); err != nil || !legacyRetained {
		t.Fatalf("legacy v1 cover outcome retained = %t, %v", legacyRetained, err)
	}
}

func TestProviderBaselineRollbackRefusesFinishedGenreCatalogEvidence(t *testing.T) {
	for name, update := range map[string]string{
		"successful refresh": `UPDATE genre_catalog SET items='["retained"]'::jsonb, updated_at=now(), last_attempt_at=now(), configuration_identity='config-A', refresh_request='{"status":"finished"}'::jsonb WHERE singleton=true`,
		"failed refresh":     `UPDATE genre_catalog SET last_attempt_at=now(), last_error='provider unavailable', configuration_identity='config-A', refresh_request='{"status":"finished"}'::jsonb WHERE singleton=true`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			db := testpostgres.Open(t)
			testpostgres.Reset(t, db)
			ctx := context.Background()
			collection := loadMigrations(t)
			migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
			if err := migrator.Init(ctx); err != nil {
				t.Fatalf("initialize baseline migrations: %v", err)
			}
			if _, err := migrator.Migrate(ctx); err != nil {
				t.Fatalf("apply baseline migrations: %v", err)
			}
			if _, err := db.ExecContext(ctx, update); err != nil {
				t.Fatalf("seed finished catalog evidence: %v", err)
			}
			if _, err := migrator.Rollback(ctx); err == nil {
				t.Fatal("rollback discarded finished genre catalog evidence")
			}
			for _, table := range []string{"release_medium", "track_publication"} {
				var present bool
				if err := db.NewRaw(`SELECT to_regclass(?) IS NOT NULL`, "public."+table).Scan(ctx, &present); err != nil || !present {
					t.Fatalf("baseline table %s survived refused rollback = %v, %v", table, present, err)
				}
			}
			var applied int
			if err := db.NewRaw(`SELECT count(*) FROM bun_migrations WHERE name IN ('20261015000001', '20261015000002', '20261015000003', '20261015000004', '20261015000005')`).Scan(ctx, &applied); err != nil || applied != len(preSourceRepublishResultMigrationNames) {
				t.Fatalf("pre-source-republish migrations retained after baseline rollback refusal = %d, %v; want %d", applied, err, len(preSourceRepublishResultMigrationNames))
			}
			var newMigrationApplied bool
			if err := db.NewRaw(`SELECT EXISTS (SELECT 1 FROM bun_migrations WHERE name='20261108000001')`).Scan(ctx, &newMigrationApplied); err != nil || newMigrationApplied {
				t.Fatalf("new optional-result migration remains applied after its successful rollback = %t, %v", newMigrationApplied, err)
			}
			var retained bool
			if err := db.NewRaw(`SELECT EXISTS (SELECT 1 FROM genre_catalog WHERE refresh_request->>'status'='finished')`).Scan(ctx, &retained); err != nil || !retained {
				t.Fatalf("finished catalog evidence retained = %v, %v", retained, err)
			}
		})
	}
}

func loadMigrations(t *testing.T) *migrate.Migrations {
	t.Helper()
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load application migrations: %v", err)
	}
	return collection
}

func assertMigrationInventory(t *testing.T, collection *migrate.Migrations, want []string) {
	t.Helper()
	got := collection.Sorted()
	if len(got) != len(want) {
		t.Fatalf("discovered %d migrations, want %d: %#v", len(got), len(want), got)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("discovered migration %d = %q, want %q", i, got[i].Name, name)
		}
	}
}

func assertMigrationGroup(t *testing.T, group *migrate.MigrationGroup, want []string, direction string) {
	t.Helper()
	if group == nil {
		t.Fatalf("%s migration group is nil", direction)
	}
	got := make([]string, 0, len(group.Migrations))
	for _, migration := range group.Migrations {
		got = append(got, migration.Name)
	}
	// Bun returns rollback groups in reverse execution order.
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("%s migration names = %v, want %v", direction, got, want)
	}
}

func assertCurrentSchema(t *testing.T, database *bun.DB) {
	t.Helper()
	var nativeColumns, nativeConstraints, identityColumns, identityConstraints, positionMetadataColumns, artistMetadataRevision, artistMetadataConstraint, coverObserved, coverConstraint, sourceMetadataReference, coverPreparationDiagnostic, publicationResumeMode, publicationResumeModeConstraint int
	ctx := context.Background()
	checks := []struct {
		query  string
		result *int
		want   int
	}{
		{`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='media_metadata_result' AND column_name='native_matroska'`, &nativeColumns, 0},
		{`SELECT count(*) FROM pg_constraint WHERE conrelid='public.media_metadata_result'::regclass AND conname='media_metadata_result_native_matroska_check'`, &nativeConstraints, 0},
		{`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='release_track' AND column_name='id' AND data_type='uuid' AND is_nullable='NO'`, &identityColumns, 1},
		{`SELECT count(*) FROM pg_constraint WHERE conrelid='public.release_track'::regclass AND conname='release_track_id_key' AND contype='u'`, &identityConstraints, 1},
		{`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='release_track' AND column_name='metadata' AND data_type='jsonb' AND is_nullable='NO'`, &positionMetadataColumns, 1},
		{`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='local_artist' AND column_name='metadata_revision' AND data_type='bigint' AND is_nullable='NO' AND column_default IS NOT NULL`, &artistMetadataRevision, 1},
		{`SELECT count(*) FROM pg_constraint WHERE conrelid='public.local_artist'::regclass AND conname='local_artist_metadata_revision_check' AND contype='c'`, &artistMetadataConstraint, 1},
		{`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='media_metadata_result' AND column_name='cover_observed' AND is_nullable='NO'`, &coverObserved, 1},
		{`SELECT count(*) FROM pg_constraint WHERE conrelid='public.media_metadata_result'::regclass AND conname='media_metadata_result_cover_facts_check'`, &coverConstraint, 1},
		{`SELECT count(*) FROM pg_constraint WHERE conrelid='public.track_source'::regclass AND conname='track_source_source_metadata_result_fk' AND confdeltype='r'`, &sourceMetadataReference, 1},
		{`SELECT count(*) FROM pg_constraint WHERE conrelid='public.track_publication'::regclass AND conname='track_publication_cover_embedding_projection_check' AND pg_get_constraintdef(oid) LIKE '%unsupported_source%' AND pg_get_constraintdef(oid) LIKE '%decode_failed%' AND pg_get_constraintdef(oid) LIKE '%derivative_failed%' AND pg_get_constraintdef(oid) LIKE '%source_unavailable%'`, &coverPreparationDiagnostic, 1},
		{`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='track_publication_attempt' AND column_name='resume_mode' AND data_type='text' AND is_nullable='NO' AND column_default LIKE '%prepare%'`, &publicationResumeMode, 1},
		{`SELECT count(*) FROM pg_constraint WHERE conrelid='public.track_publication_attempt'::regclass AND conname='track_publication_attempt_resume_mode_check' AND contype='c'`, &publicationResumeModeConstraint, 1},
	}
	for _, check := range checks {
		if err := database.NewRaw(check.query).Scan(ctx, check.result); err != nil {
			t.Fatalf("inspect current schema: %v", err)
		}
		if *check.result != check.want {
			t.Fatalf("schema check %q = %d, want %d", check.query, *check.result, check.want)
		}
	}
}
