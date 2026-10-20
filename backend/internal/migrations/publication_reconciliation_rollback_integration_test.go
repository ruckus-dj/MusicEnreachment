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

func TestPublicationReconciliationRollbackRefusesPopulatedOperationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatal(err)
	}
	prior, target := migrate.NewMigrations(), migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		if migration.Name < "20261020000001" {
			prior.Add(migration)
		} else if migration.Name == "20261020000001" {
			target.Add(migration)
		}
	}
	if len(target.Sorted()) != 1 {
		t.Fatalf("reconciliation target migrations = %d, want one", len(target.Sorted()))
	}
	priorMigrator := migrate.NewMigrator(database, prior, migrate.WithMarkAppliedOnSuccess(true))
	if err := priorMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := priorMigrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	targetMigrator := migrate.NewMigrator(database, target, migrate.WithMarkAppliedOnSuccess(true))
	if err := targetMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := targetMigrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	positionID, epochID, operationID := uuid.New(), uuid.New(), uuid.New()
	snapshot := `{"version":1,"position_id":"` + positionID.String() + `","output_epoch":"` + epochID.String() + `"}`
	if err := insertReconciliationOperation(ctx, database, operationID, snapshot); err != nil {
		t.Fatalf("insert populated reconciliation operation: %v", err)
	}
	if _, err := targetMigrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "publication reconciliation contains live data; refusing rollback") {
		t.Fatalf("rollback with populated reconciliation operation = %v, want guarded refusal", err)
	}
	var retained bool
	if err := database.NewRaw(`SELECT EXISTS (SELECT 1 FROM operation WHERE id=? AND kind='publication_reconcile_v1' AND state='failed' AND stage='failed' AND finished_at IS NOT NULL AND safe_error='test terminal failure')`, operationID).Scan(ctx, &retained); err != nil || !retained {
		t.Fatalf("reconciliation operation retained after refused rollback = %t, %v", retained, err)
	}
}
