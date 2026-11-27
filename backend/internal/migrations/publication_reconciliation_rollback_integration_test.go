//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestPublicationReconciliationRollbackRefusesPopulatedOperationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	if err := migrations.Apply(ctx, database); err != nil {
		t.Fatalf("apply baseline migrations: %v", err)
	}
	positionID, epochID, operationID := uuid.New(), uuid.New(), uuid.New()
	snapshot := `{"version":1,"position_id":"` + positionID.String() + `","output_epoch":"` + epochID.String() + `"}`
	if err := insertReconciliationOperation(ctx, database, operationID, snapshot); err != nil {
		t.Fatalf("insert populated reconciliation operation: %v", err)
	}
	if _, err := migrations.RollbackLastGroup(ctx, database); err == nil {
		t.Fatal("rollback with a populated reconciliation operation succeeded")
	}
	var retained bool
	if err := database.NewRaw(`SELECT EXISTS (SELECT 1 FROM operation WHERE id=? AND kind='publication_reconcile_v1' AND state='failed' AND stage='failed' AND finished_at IS NOT NULL AND safe_error='test terminal failure')`, operationID).Scan(ctx, &retained); err != nil || !retained {
		t.Fatalf("reconciliation operation retained after refused rollback = %t, %v", retained, err)
	}
}
