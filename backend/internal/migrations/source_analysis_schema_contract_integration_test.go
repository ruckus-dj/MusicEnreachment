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
