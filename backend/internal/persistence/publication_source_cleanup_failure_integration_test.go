//go:build integration

package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFailPublicationSourceCleanupDeliveryRetainsMixedItemsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, guard, operationID, itemID, _ := setupStartedCleanupRemoval(t)
	if outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) error {
		return errors.New("injected incomplete unlink")
	}); err == nil || outcome != PublicationSourceCleanupRemovalBlocked {
		t.Fatalf("start incomplete first item = %v, %v", outcome, err)
	}
	secondItemID, secondOwnerID := uuid.New(), uuid.New()
	secondBorrowerRevision := uuid.New()
	secondRootID, secondLocationID, secondWorkID := uuid.New(), uuid.New(), uuid.New()
	if _, err := fixture.db.ExecContext(ctx, `INSERT INTO source_cleanup_item
		(id,operation_id,position_id,epoch,publication_generation,state,source_snapshot,borrower_revision)
		SELECT ?,operation_id,position_id,epoch,publication_generation,'claimed',
		jsonb_set(jsonb_set(jsonb_set(source_snapshot,'{source_root_id}',to_jsonb(?::text)),'{source_location_id}',to_jsonb(?::text)),'{work_id}',to_jsonb(?::text)),?
		FROM source_cleanup_item WHERE id=?`, secondItemID, secondRootID, secondLocationID, secondWorkID, secondBorrowerRevision, itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(ctx, `INSERT INTO source_cleanup_owner
		(item_id,operation_id,application_attempt,river_job_id,owner_uuid,source_root_id,source_location_id,work_id)
		SELECT ?,operation_id,application_attempt,river_job_id,?,?,?,? FROM source_cleanup_owner WHERE item_id=?`,
		secondItemID, secondOwnerID, secondRootID, secondLocationID, secondWorkID, itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE source_cleanup_item SET mutation_claim_uuid=?,mutation_started_at=now(),mutation_completed_at=now() WHERE id=?`, guard.ClaimUUID, secondItemID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	thirdItemID, thirdRootID, thirdLocationID, thirdWorkID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := fixture.db.NewRaw(`INSERT INTO source_cleanup_item(id,operation_id,position_id,epoch,publication_generation,state,source_snapshot,borrower_revision)
		SELECT ?,operation_id,position_id,epoch,publication_generation,'claimed',
		jsonb_set(jsonb_set(jsonb_set(source_snapshot,'{source_root_id}',to_jsonb(?::text)),'{source_location_id}',to_jsonb(?::text)),'{work_id}',to_jsonb(?::text)),?
		FROM source_cleanup_item WHERE id=?`, thirdItemID, thirdRootID, thirdLocationID, thirdWorkID, uuid.New(), itemID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.NewRaw(`INSERT INTO source_cleanup_owner(item_id,operation_id,application_attempt,river_job_id,owner_uuid,source_root_id,source_location_id,work_id)
		SELECT ?,operation_id,application_attempt,river_job_id,?,?,?,? FROM source_cleanup_owner WHERE item_id=?`, thirdItemID, uuid.New(), thirdRootID, thirdLocationID, thirdWorkID, itemID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(ctx, `CREATE FUNCTION fail_cleanup_delivery_operation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='failed' THEN RAISE EXCEPTION 'injected cleanup delivery failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_cleanup_delivery_operation BEFORE UPDATE ON operation FOR EACH ROW EXECUTE FUNCTION fail_cleanup_delivery_operation()`); err != nil {
		t.Fatal(err)
	}
	if failed, err := repository.FailPublicationSourceCleanupDelivery(ctx, guard, "another item failed"); err == nil || failed || !strings.Contains(err.Error(), "injected cleanup delivery failure") {
		t.Fatalf("injected failure transition=%v,%v", failed, err)
	}
	var operationState, initialItemState, initialClaimState string
	if err := fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, operationID).Scan(ctx, &operationState); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT state FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &initialItemState); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, operationID).Scan(ctx, &initialClaimState); err != nil {
		t.Fatal(err)
	}
	if operationState != "running" || initialItemState != "claimed" || initialClaimState != "executing" {
		t.Fatalf("failed transition rollback left operation/item/claim=%q/%q/%q", operationState, initialItemState, initialClaimState)
	}
	if _, err := fixture.db.ExecContext(ctx, `DROP TRIGGER fail_cleanup_delivery_operation ON operation; DROP FUNCTION fail_cleanup_delivery_operation()`); err != nil {
		t.Fatal(err)
	}
	failed, err := repository.FailPublicationSourceCleanupDelivery(ctx, guard, "another item failed")
	if err != nil || !failed {
		t.Fatalf("fail mixed cleanup = %v, %v", failed, err)
	}
	var completedState, unstartedState, claimID string
	var completedAt, unstartedAt any
	if err := fixture.db.NewRaw(`SELECT state,mutation_completed_at FROM source_cleanup_item WHERE id=?`, secondItemID).Scan(ctx, &completedState, &completedAt); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT state,mutation_started_at FROM source_cleanup_item WHERE id=?`, thirdItemID).Scan(ctx, &unstartedState, &unstartedAt); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, operationID).Scan(ctx, &claimID); err != nil {
		t.Fatal(err)
	}
	if completedState != "claimed" || completedAt == nil || unstartedState != "claimed" || unstartedAt != nil || claimID != "recovery_required" {
		t.Fatalf("mixed item states completed=%q/%v unstarted=%q/%v claim=%q", completedState, completedAt, unstartedState, unstartedAt, claimID)
	}
	var incompleteRecovery bool
	if err := fixture.db.NewRaw(`SELECT state='recovery_required' AND safe_error IS NOT NULL AND mutation_started_at IS NOT NULL AND mutation_completed_at IS NULL FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &incompleteRecovery); err != nil || !incompleteRecovery {
		t.Fatalf("incomplete item recovery evidence = %t, %v", incompleteRecovery, err)
	}
	var count int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, operationID).Scan(ctx, &count); err != nil || count != 3 {
		t.Fatalf("retained owners=%d, %v", count, err)
	}
}
