//go:build integration

package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFailPublicationSourceCleanupDeliveryRetainsStartedFrontierPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, guard, operationID, itemID, _ := setupStartedCleanupRemoval(t)
	removeErr := errors.New("unlink failed")
	if outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return removeErr }); outcome != PublicationSourceCleanupRemovalBlocked || !errors.Is(err, removeErr) {
		t.Fatalf("start failing removal = %v, %v", outcome, err)
	}
	// Current permission and source facts are not needed to record an already
	// started ambiguous mutation.
	if _, err := fixture.db.ExecContext(ctx, `UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(ctx, `UPDATE source_location SET size_bytes=size_bytes+1 WHERE id=(SELECT (source_snapshot->>'source_location_id')::uuid FROM source_cleanup_item WHERE id=?)`, itemID); err != nil {
		t.Fatalf("change current source fact: %v", err)
	}
	var beforeSnapshot []byte
	var beforeRevision uuid.UUID
	if err := fixture.db.NewRaw(`SELECT source_snapshot,borrower_revision FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &beforeSnapshot, &beforeRevision); err != nil {
		t.Fatal(err)
	}
	const safeError = "  source removal outcome is unknown  "
	failed, err := repository.FailPublicationSourceCleanupDelivery(ctx, guard, safeError)
	if err != nil || !failed {
		t.Fatalf("record cleanup failure = %v, %v", failed, err)
	}
	var itemState, itemError, claimState, operationState, operationStage, operationError string
	var afterSnapshot []byte
	var afterRevision uuid.UUID
	var started, completed any
	if err := fixture.db.NewRaw(`SELECT state,safe_error,source_snapshot,borrower_revision,mutation_started_at,mutation_completed_at FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &itemState, &itemError, &afterSnapshot, &afterRevision, &started, &completed); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, operationID).Scan(ctx, &claimState); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT state,stage,safe_error FROM operation WHERE id=?`, operationID).Scan(ctx, &operationState, &operationStage, &operationError); err != nil {
		t.Fatal(err)
	}
	if itemState != "recovery_required" || itemError != "source removal outcome is unknown" || claimState != "recovery_required" || operationState != "failed" || operationStage != "cleanup_recovery_required" || operationError != "source removal outcome is unknown" || started == nil || completed != nil || string(afterSnapshot) != string(beforeSnapshot) || afterRevision != beforeRevision {
		t.Fatalf("failed cleanup states item=%q/%q claim=%q op=%q/%q/%q frontier=%v/%v retained=%t/%t", itemState, itemError, claimState, operationState, operationStage, operationError, started, completed, string(afterSnapshot) == string(beforeSnapshot), afterRevision == beforeRevision)
	}
	var owners int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, operationID).Scan(ctx, &owners); err != nil || owners != 1 {
		t.Fatalf("retained owners=%d, err=%v", owners, err)
	}
}

func TestFailPublicationSourceCleanupDeliveryRejectsStaleOrInvalidDiagnosticPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		mutate    func(*PublicationSourceCleanupDeliveryGuard) string
		wantError bool
	}{
		{name: "stale guard", mutate: func(guard *PublicationSourceCleanupDeliveryGuard) string {
			guard.DeliveryToken = "stale"
			return "safe error"
		}},
		{name: "blank diagnostic", mutate: func(*PublicationSourceCleanupDeliveryGuard) string { return " \t " }, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, fixture, repository, guard, operationID, _, _ := setupStartedCleanupRemoval(t)
			safeError := tc.mutate(&guard)
			failed, err := repository.FailPublicationSourceCleanupDelivery(ctx, guard, safeError)
			if tc.wantError {
				if err == nil || failed {
					t.Fatalf("invalid diagnostic = %v, %v; want error", failed, err)
				}
			} else if err != nil || failed {
				t.Fatalf("stale guard = %v, %v; want false,nil", failed, err)
			}
			assertCleanupRemovalRetained(t, ctx, fixture, operationID)
		})
	}
}

func TestFailPublicationSourceCleanupDeliveryCompletedOnlyDoesNotFailPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, guard, operationID, itemID, _ := setupStartedCleanupRemoval(t)
	outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return nil })
	if err != nil || outcome != PublicationSourceCleanupRemovalCompleted {
		t.Fatalf("complete cleanup item = %v, %v", outcome, err)
	}
	failed, err := repository.FailPublicationSourceCleanupDelivery(ctx, guard, "late failure")
	if err != nil || failed {
		t.Fatalf("fail completed-only cleanup = %v, %v; want false,nil", failed, err)
	}
	settled, err := repository.SettlePublicationSourceCleanup(ctx, guard)
	if err != nil || !settled {
		t.Fatalf("settle completed cleanup = %v, %v", settled, err)
	}
	var state string
	if err := fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, operationID).Scan(ctx, &state); err != nil || state != "succeeded" {
		t.Fatalf("completed cleanup operation = %q, %v", state, err)
	}
}

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

func TestFailPublicationSourceCleanupDeliveryRollsBackLateOperationFailurePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, guard, operationID, itemID, _ := setupStartedCleanupRemoval(t)
	if outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) error {
		return errors.New("injected incomplete unlink")
	}); err == nil || outcome != PublicationSourceCleanupRemovalBlocked {
		t.Fatalf("create unresolved frontier = %v, %v", outcome, err)
	}
	if _, err := fixture.db.ExecContext(ctx, `CREATE FUNCTION fail_cleanup_delivery_operation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='failed' THEN RAISE EXCEPTION 'injected cleanup delivery failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_cleanup_delivery_operation BEFORE UPDATE ON operation FOR EACH ROW EXECUTE FUNCTION fail_cleanup_delivery_operation()`); err != nil {
		t.Fatal(err)
	}
	failed, err := repository.FailPublicationSourceCleanupDelivery(ctx, guard, "safe failure")
	if err == nil || failed || !strings.Contains(err.Error(), "injected cleanup delivery failure") {
		t.Fatalf("failure transition = %v, %v", failed, err)
	}
	var itemState, claimState, operationState string
	if err := fixture.db.NewRaw(`SELECT state FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &itemState); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, operationID).Scan(ctx, &claimState); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, operationID).Scan(ctx, &operationState); err != nil {
		t.Fatal(err)
	}
	if itemState != "claimed" || claimState != "executing" || operationState != "running" {
		t.Fatalf("rollback left item=%q claim=%q operation=%q", itemState, claimState, operationState)
	}
	if _, err := fixture.db.ExecContext(ctx, `DROP TRIGGER fail_cleanup_delivery_operation ON operation; DROP FUNCTION fail_cleanup_delivery_operation()`); err != nil {
		t.Fatal(err)
	}
}
