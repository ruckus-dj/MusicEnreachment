//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/migrate"
)

const metadataStepMigration = "20261028000000"

// These checks exercise the real PostgreSQL constraints and both directions of
// the migration, rather than asserting fragments of the SQL source.
func TestMetadataStepMigrationGuardsAndRoundTripWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := testpostgres.Open(t)
	collection := mustMigrations(t)
	migration := migrationNamed(t, collection, metadataStepMigration)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, metadataStepMigration))
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

	rollbackMigration(t, ctx, database, migration)
	if relation := relationName(t, database, "media_metadata_result"); relation != nil {
		t.Fatalf("metadata table survived rollback: %s", *relation)
	}
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
	if relation := relationName(t, database, "media_metadata_result"); relation == nil {
		t.Fatal("metadata table missing after reapplying migration")
	}
}
