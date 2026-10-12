//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const fallbackAuthoritySealMigration = "20261201000000"

func TestFallbackAuthoritySealMigrationRollbackRefusesStoredSealWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, fallbackAuthoritySealMigration))
	migration := migrationNamed(t, collection, fallbackAuthoritySealMigration)
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
	runID, _ := insertFallbackReceiptMigrationScope(t, ctx, database)
	if _, err := database.NewRaw(`INSERT INTO matching_fallback_authority_seal
		(run_id,wait_revision,schema_version,authority,authority_digest)
		VALUES (?,1,3,'{"schema_version":3}'::jsonb,?)`, runID, make([]byte, 32)).Exec(ctx); err != nil {
		t.Fatalf("insert stored authority seal: %v", err)
	}

	if err := rollbackFallbackAuthoritySealMigration(t, ctx, database, collection); err == nil || !strings.Contains(err.Error(), "cannot remove populated matching fallback authority seals") {
		t.Fatalf("rollback error = %v, want populated-seal safety guard", err)
	}
	var retained int
	if err := database.NewRaw(`SELECT count(*) FROM matching_fallback_authority_seal WHERE run_id=? AND wait_revision=1`, runID).Scan(ctx, &retained); err != nil || retained != 1 {
		t.Fatalf("failed rollback changed populated seal table: count=%d err=%v", retained, err)
	}
}

func rollbackFallbackAuthoritySealMigration(t *testing.T, ctx context.Context, database *bun.DB, collection *migrate.Migrations) error {
	t.Helper()
	migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	_, err := migrator.Rollback(ctx)
	return err
}
