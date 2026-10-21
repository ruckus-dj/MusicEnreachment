//go:build integration

package persistence

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func completeSourceCleanupForSettlement(t *testing.T) (context.Context, publicationExecutionFixture, *SetupManagerRepository, PublicationSourceCleanupDeliveryGuard, uuid.UUID) {
	t.Helper()
	ctx, fixture, repository, guard, operationID, itemID, _ := setupStartedCleanupRemoval(t)
	outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return nil })
	if err != nil || outcome != PublicationSourceCleanupRemovalCompleted {
		t.Fatalf("complete source cleanup item = %s, %v", outcome, err)
	}
	return ctx, fixture, repository, guard, operationID
}

func TestSettlePublicationSourceCleanupRetainsInventoryAndLinksPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, guard, operationID := completeSourceCleanupForSettlement(t)
	var before [3]int
	for i, table := range []string{"source_location", "source_analysis_work", "track_source"} {
		if err := fixture.db.NewRaw("SELECT count(*) FROM "+table).Scan(ctx, &before[i]); err != nil {
			t.Fatal(err)
		}
	}
	settled, err := repository.SettlePublicationSourceCleanup(ctx, guard)
	if err != nil || !settled {
		t.Fatalf("settle source cleanup = %t, %v", settled, err)
	}
	var operationState, operationStage string
	if err := fixture.db.NewRaw(`SELECT state,stage FROM operation WHERE id=?`, operationID).Scan(ctx, &operationState, &operationStage); err != nil {
		t.Fatal(err)
	}
	if operationState != "succeeded" || operationStage != "finished" {
		t.Fatalf("operation terminal state = %q/%q", operationState, operationStage)
	}
	for i, table := range []string{"source_location", "source_analysis_work", "track_source"} {
		var after int
		if err := fixture.db.NewRaw("SELECT count(*) FROM "+table).Scan(ctx, &after); err != nil || after != before[i] {
			t.Fatalf("%s count after settle = %d, %v; before=%d", table, after, err, before[i])
		}
	}
	for _, query := range []string{
		`SELECT count(*) FROM source_cleanup_item WHERE operation_id=?`,
		`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`,
		`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`,
	} {
		var count int
		if err := fixture.db.NewRaw(query, operationID).Scan(ctx, &count); err != nil || count != 0 {
			t.Fatalf("settled cleanup rows count = %d, %v for %s", count, err, query)
		}
	}
}

func TestSettlePublicationSourceCleanupRemovesAllCompletedItemsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, guard, operationID := completeSourceCleanupForSettlement(t)
	var firstItemID uuid.UUID
	if err := fixture.db.NewRaw(`SELECT id FROM source_cleanup_item WHERE operation_id=?`, operationID).Scan(ctx, &firstItemID); err != nil {
		t.Fatal(err)
	}
	secondItemID, secondOwnerID := uuid.New(), uuid.New()
	secondRootID, secondLocationID, secondWorkID := uuid.New(), uuid.New(), uuid.New()
	if _, err := fixture.db.ExecContext(ctx, `INSERT INTO source_cleanup_item
		(id,operation_id,position_id,epoch,publication_generation,state,source_snapshot,borrower_revision,mutation_claim_uuid,mutation_started_at,mutation_completed_at)
		SELECT ?,operation_id,position_id,epoch,publication_generation,state,
		jsonb_set(jsonb_set(jsonb_set(source_snapshot,'{source_root_id}',to_jsonb(?::text)),'{source_location_id}',to_jsonb(?::text)),'{work_id}',to_jsonb(?::text)),
		borrower_revision,mutation_claim_uuid,mutation_started_at,mutation_completed_at
		FROM source_cleanup_item WHERE id=?`, secondItemID, secondRootID, secondLocationID, secondWorkID, firstItemID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(ctx, `INSERT INTO source_cleanup_owner
		(item_id,operation_id,application_attempt,river_job_id,owner_uuid,source_root_id,source_location_id,work_id)
		SELECT ?,operation_id,application_attempt,river_job_id,?,?,?,? FROM source_cleanup_owner WHERE item_id=?`,
		secondItemID, secondOwnerID, secondRootID, secondLocationID, secondWorkID, firstItemID); err != nil {
		t.Fatal(err)
	}
	settled, err := repository.SettlePublicationSourceCleanup(ctx, guard)
	if err != nil || !settled {
		t.Fatalf("settle two completed items = %t, %v", settled, err)
	}
	for _, table := range []string{"source_cleanup_item", "source_cleanup_owner"} {
		var count int
		if err := fixture.db.NewRaw("SELECT count(*) FROM "+table+" WHERE operation_id=?", operationID).Scan(ctx, &count); err != nil || count != 0 {
			t.Fatalf("two-item settlement retained %s=%d, %v", table, count, err)
		}
	}
}

func TestSettlePublicationSourceCleanupDoesNotRequirePermissionAfterCompletionPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, guard, _ := completeSourceCleanupForSettlement(t)
	if _, err := fixture.db.ExecContext(ctx, `UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`); err != nil {
		t.Fatal(err)
	}
	settled, err := repository.SettlePublicationSourceCleanup(ctx, guard)
	if err != nil || !settled {
		t.Fatalf("settle after permission disabled = %t, %v", settled, err)
	}
}

func TestSettlePublicationSourceCleanupRejectsIncompleteItemAtomicallyPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, guard, operationID := completeSourceCleanupForSettlement(t)
	if _, err := fixture.db.ExecContext(ctx, `UPDATE source_cleanup_item SET mutation_completed_at=NULL WHERE operation_id=?`, operationID); err != nil {
		t.Fatal(err)
	}
	settled, err := repository.SettlePublicationSourceCleanup(ctx, guard)
	if err != nil || settled {
		t.Fatalf("settle incomplete cleanup = %t, %v; want false,nil", settled, err)
	}
	assertCleanupRemovalRetained(t, ctx, fixture, operationID)
	var state string
	if err := fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, operationID).Scan(ctx, &state); err != nil || state != "running" {
		t.Fatalf("incomplete cleanup operation state = %q, %v", state, err)
	}
}

func TestSettlePublicationSourceCleanupRejectsStaleGuardPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, guard, operationID := completeSourceCleanupForSettlement(t)
	guard.ClaimUUID = uuid.New()
	settled, err := repository.SettlePublicationSourceCleanup(ctx, guard)
	if err != nil || settled {
		t.Fatalf("settle with stale guard = %t, %v; want false,nil", settled, err)
	}
	assertCleanupRemovalRetained(t, ctx, fixture, operationID)
}

func TestSettlePublicationSourceCleanupRollsBackDeletesOnLateOperationFailurePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, guard, operationID := completeSourceCleanupForSettlement(t)
	if _, err := fixture.db.ExecContext(ctx, `CREATE FUNCTION fail_cleanup_settlement_operation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='succeeded' THEN RAISE EXCEPTION 'injected cleanup settlement failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_cleanup_settlement_operation BEFORE UPDATE ON operation FOR EACH ROW EXECUTE FUNCTION fail_cleanup_settlement_operation()`); err != nil {
		t.Fatal(err)
	}
	settled, err := repository.SettlePublicationSourceCleanup(ctx, guard)
	if err == nil || settled || !strings.Contains(err.Error(), "injected cleanup settlement failure") {
		t.Fatalf("settlement failure = %t, %v", settled, err)
	}
	assertCleanupRemovalRetained(t, ctx, fixture, operationID)
	var itemCount, ownerCount int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_item WHERE operation_id=?`, operationID).Scan(ctx, &itemCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, operationID).Scan(ctx, &ownerCount); err != nil {
		t.Fatal(err)
	}
	if itemCount == 0 || ownerCount == 0 {
		t.Fatalf("late settlement failure retained items=%d owners=%d", itemCount, ownerCount)
	}
	if _, err := fixture.db.ExecContext(ctx, `DROP TRIGGER fail_cleanup_settlement_operation ON operation; DROP FUNCTION fail_cleanup_settlement_operation()`); err != nil {
		t.Fatal(err)
	}
}
