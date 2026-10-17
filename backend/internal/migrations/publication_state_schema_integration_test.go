//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/migrate"
)

const publicationStateMigrationName = "20261017000005"

// TestFilePublicationCurrentSchemaWithPostgreSQL applies every migration before
// the file publication change, then applies only the embedded target migration
// through the migration framework. It verifies the publication schema contract,
// the namespace claim constraints, and the guarded rollback that must refuse
// while live state exists and drop only the target schema afterwards.
func TestFilePublicationCurrentSchemaWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()

	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	prior, target := publicationStateCollections(t, collection)

	priorMigrator := migrate.NewMigrator(database, prior, migrate.WithMarkAppliedOnSuccess(true))
	if err := priorMigrator.Init(ctx); err != nil {
		t.Fatalf("initialize prior migrations: %v", err)
	}
	group, err := priorMigrator.Migrate(ctx)
	if err != nil {
		t.Fatalf("apply prior migrations: %v", err)
	}
	if applied := publicationStateGroupNames(group); len(applied) == 0 || applied[len(applied)-1] != "20261017000004" {
		t.Fatalf("prior migration group = %v, want it to end with 20261017000004", applied)
	}
	var preexisting *string
	if err := database.NewRaw(`SELECT to_regclass('public.publication_namespace_state')::text`).Scan(ctx, &preexisting); err != nil || preexisting != nil {
		t.Fatalf("publication namespace state before target migration = %v, %v; want none", preexisting, err)
	}

	targetMigrator := migrate.NewMigrator(database, target, migrate.WithMarkAppliedOnSuccess(true))
	if err := targetMigrator.Init(ctx); err != nil {
		t.Fatalf("initialize publication state migration: %v", err)
	}
	group, err = targetMigrator.Migrate(ctx)
	if err != nil {
		t.Fatalf("apply publication state migration: %v", err)
	}
	if names := publicationStateGroupNames(group); len(names) != 1 || names[0] != publicationStateMigrationName {
		t.Fatalf("applied migration group = %v, want only %s", names, publicationStateMigrationName)
	}

	for _, table := range []string{
		"publication_namespace_state", "track_publication", "track_publication_attempt",
		"publication_execution_claim", "publication_namespace_claim",
		"publication_namespace_reservation", "source_cleanup_item",
	} {
		var exists bool
		if err := database.NewRaw(`SELECT to_regclass(?) IS NOT NULL`, "public."+table).Scan(ctx, &exists); err != nil || !exists {
			t.Fatalf("publication table %s exists = %t, %v", table, exists, err)
		}
	}
	var epoch uuid.UUID
	if err := database.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton`).Scan(ctx, &epoch); err != nil || epoch == uuid.Nil {
		t.Fatalf("publication output epoch = %s, %v", epoch, err)
	}
	var singletonCount int
	if err := database.NewRaw(`SELECT count(*) FROM publication_namespace_state`).Scan(ctx, &singletonCount); err != nil || singletonCount != 1 {
		t.Fatalf("publication namespace seed count = %d, %v; want one", singletonCount, err)
	}

	var forbiddenSourceColumns int
	if err := database.NewRaw(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema='public' AND table_name='track_publication'
		AND column_name IN ('source_id','source_path','source_location_id','media_variant_id')`).Scan(ctx, &forbiddenSourceColumns); err != nil || forbiddenSourceColumns != 0 {
		t.Fatalf("current publication has %d historical source columns, %v", forbiddenSourceColumns, err)
	}
	var publicationJSONColumns, nativeMatroskaColumns int
	if err := database.NewRaw(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema='public' AND (
			(table_name='track_publication' AND column_name IN ('ownership','actual_audio','actual_metadata','actual_assets') AND data_type='jsonb')
			OR (table_name='track_publication_attempt' AND column_name IN ('frozen_inputs','namespace_manifest','mutation_journal') AND data_type='jsonb')
			OR (table_name='source_cleanup_item' AND column_name='source_snapshot' AND data_type='jsonb')
		)`).Scan(ctx, &publicationJSONColumns); err != nil || publicationJSONColumns != 8 {
		t.Fatalf("publication JSONB columns = %d, %v; want 8", publicationJSONColumns, err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema='public' AND table_name='media_metadata_result' AND column_name='native_matroska'`).Scan(ctx, &nativeMatroskaColumns); err != nil || nativeMatroskaColumns != 0 {
		t.Fatalf("native Matroska columns = %d, %v; want none", nativeMatroskaColumns, err)
	}

	// Seed a real release position so the owner foreign key of the namespace
	// claim can be satisfied independently of the owner/scope shape check.
	releaseID, trackID := uuid.New(), uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO local_release (id) VALUES (?)`, releaseID); err != nil {
		t.Fatalf("insert local release: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO local_track (id) VALUES (?)`, trackID); err != nil {
		t.Fatalf("insert local track: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO release_track
		(release_id, "position", track_id, recording_id) VALUES (?, 1, ?, NULL)`, releaseID, trackID); err != nil {
		t.Fatalf("insert release track: %v", err)
	}
	var positionID uuid.UUID
	if err := database.NewRaw(`SELECT id FROM release_track WHERE release_id = ?`, releaseID).Scan(ctx, &positionID); err != nil {
		t.Fatalf("read release track id: %v", err)
	}

	// A directory claim scoped to a library has no owner, which is the valid
	// dual of the owner-position shape used by file claims.
	claimID := uuid.New()
	digest := make([]byte, 32)
	digest[0] = 1
	if _, err := database.ExecContext(ctx, `INSERT INTO publication_namespace_claim
		(id,epoch,key_digest,canonical_key,relative_path,node_kind,directory_scope_key)
		VALUES (?,?,?,'artist/release','artist/release','dir','library')`, claimID, epoch, digest); err != nil {
		t.Fatalf("insert namespace claim: %v", err)
	}
	for _, test := range []struct {
		name string
		stmt string
		args []any
	}{
		{
			name: "duplicate epoch digest",
			stmt: `INSERT INTO publication_namespace_claim (id,epoch,key_digest,canonical_key,relative_path,node_kind,directory_scope_key) VALUES (?,?,?,'other/dir','other/dir','dir','library')`,
			args: []any{uuid.New(), epoch, digest},
		},
		{
			name: "incorrect digest length",
			stmt: `INSERT INTO publication_namespace_claim (id,epoch,key_digest,canonical_key,relative_path,node_kind,directory_scope_key) VALUES (?,?,?,'other/dir','other/dir','dir','library')`,
			args: []any{uuid.New(), epoch, []byte{1}},
		},
		{
			name: "ambiguous owner and directory scope",
			stmt: `INSERT INTO publication_namespace_claim (id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id,directory_scope_key) VALUES (?,?,?,'other/dir','other/dir','dir',?,'library')`,
			args: []any{uuid.New(), epoch, make([]byte, 32), positionID},
		},
		{
			name: "missing owner and directory scope",
			stmt: `INSERT INTO publication_namespace_claim (id,epoch,key_digest,canonical_key,relative_path,node_kind) VALUES (?,?,?,'other/dir','other/dir','dir')`,
			args: []any{uuid.New(), epoch, make([]byte, 32)},
		},
		{
			name: "missing release position foreign key",
			stmt: `INSERT INTO publication_namespace_claim (id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id) VALUES (?,?,?,'other/track.flac','other/track.flac','file',?)`,
			args: []any{uuid.New(), epoch, make([]byte, 32), uuid.New()},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := database.ExecContext(ctx, test.stmt, test.args...); err == nil {
				t.Fatalf("accepted invalid namespace claim (%s)", test.name)
			}
		})
	}

	if _, err := targetMigrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "cannot roll back file publication state") {
		t.Fatalf("rollback with a live namespace claim = %v, want a refusal", err)
	}
	var retained bool
	if err := database.NewRaw(`SELECT to_regclass('public.publication_namespace_claim') IS NOT NULL`).Scan(ctx, &retained); err != nil || !retained {
		t.Fatalf("refused rollback retained publication tables = %t, %v", retained, err)
	}

	if _, err := database.ExecContext(ctx, `DELETE FROM publication_namespace_claim WHERE id=?`, claimID); err != nil {
		t.Fatalf("remove namespace claim fixture: %v", err)
	}
	rolledBack, err := targetMigrator.Rollback(ctx)
	if err != nil {
		t.Fatalf("rollback publication state migration: %v", err)
	}
	if names := publicationStateGroupNames(rolledBack); len(names) != 1 || names[0] != publicationStateMigrationName {
		t.Fatalf("rolled back migration group = %v, want only %s", names, publicationStateMigrationName)
	}
	var dropped *string
	if err := database.NewRaw(`SELECT to_regclass('public.publication_namespace_state')::text`).Scan(ctx, &dropped); err != nil || dropped != nil {
		t.Fatalf("publication namespace state after rollback = %v, %v; want none", dropped, err)
	}
	var priorTable *string
	if err := database.NewRaw(`SELECT to_regclass('public.release_track')::text`).Scan(ctx, &priorTable); err != nil || priorTable == nil {
		t.Fatalf("release_track after rollback = %v, %v; want the prior schema preserved", priorTable, err)
	}
}

// publicationStateCollections splits the embedded migrations into everything
// older than the file publication change and the single target migration.
func publicationStateCollections(t *testing.T, collection *migrate.Migrations) (*migrate.Migrations, *migrate.Migrations) {
	t.Helper()
	prior := migrate.NewMigrations()
	target := migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		switch {
		case migration.Name < publicationStateMigrationName:
			prior.Add(migration)
		case migration.Name == publicationStateMigrationName:
			target.Add(migration)
		}
	}
	if names := publicationStateMigrationNames(target.Sorted()); len(names) != 1 || names[0] != publicationStateMigrationName {
		t.Fatalf("target collection = %v, want only %s", names, publicationStateMigrationName)
	}
	return prior, target
}

func publicationStateGroupNames(group *migrate.MigrationGroup) []string {
	if group == nil {
		return nil
	}
	names := make([]string, len(group.Migrations))
	for index, migration := range group.Migrations {
		names[index] = migration.Name
	}
	return names
}

func publicationStateMigrationNames(list migrate.MigrationSlice) []string {
	names := make([]string, len(list))
	for index, migration := range list {
		names[index] = migration.Name
	}
	return names
}
