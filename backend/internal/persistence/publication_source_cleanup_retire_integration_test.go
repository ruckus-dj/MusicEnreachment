//go:build integration

package persistence

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFailUnstartedPublicationSourceCleanupDeliveryRequiresExactZeroFrontierPostgreSQL(t *testing.T) {
	t.Parallel()
	t.Run("running claimed exact claim with permission enabled and changed source facts", func(t *testing.T) {
		ctx, fixture, repository, guard, operationID, itemID, _ := setupStartedCleanupRemoval(t)
		if _, err := fixture.db.ExecContext(ctx, `UPDATE source_location SET size_bytes=size_bytes+1 WHERE id=(SELECT (source_snapshot->>'source_location_id')::uuid FROM source_cleanup_item WHERE id=?)`, itemID); err != nil {
			t.Fatal(err)
		}
		retireAndAssert(t, ctx, fixture, repository, guard, operationID, itemID, true, false)
	})

	t.Run("started mutation frontier retained", func(t *testing.T) {
		ctx, fixture, repository, guard, operationID, itemID, _ := setupStartedCleanupRemoval(t)
		if outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return context.Canceled }); err == nil || outcome != PublicationSourceCleanupRemovalBlocked {
			t.Fatalf("start removal = %v, %v", outcome, err)
		}
		retireAndAssert(t, ctx, fixture, repository, guard, operationID, itemID, false, false)
	})
}

func TestFailUnstartedPublicationSourceCleanupDeliveryRetiresQueuedClaimAndBlocksRemovalPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture, repository, _ := publicationSourceCleanupReadyFixture(t)
	guard, operationID := admitAndClaimSourceCleanup(t, ctx, fixture, repository)
	var itemID uuid.UUID
	if err := fixture.db.NewRaw(`SELECT id FROM source_cleanup_item WHERE operation_id=?`, operationID).Scan(ctx, &itemID); err != nil {
		t.Fatal(err)
	}
	retired, err := repository.FailUnstartedPublicationSourceCleanupDelivery(ctx, guard, "safe retirement")
	if err != nil || !retired {
		t.Fatalf("retire queued claimed delivery = %v, %v", retired, err)
	}
	var items, owners, claims int
	for _, check := range []struct {
		query string
		count *int
	}{
		{`SELECT count(*) FROM source_cleanup_item WHERE operation_id=?`, &items},
		{`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, &owners},
		{`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, &claims},
	} {
		if err := fixture.db.NewRaw(check.query, operationID).Scan(ctx, check.count); err != nil {
			t.Fatal(err)
		}
	}
	if items != 0 || owners != 0 || claims != 0 {
		t.Fatalf("retirement rows items/owners/claims=%d/%d/%d", items, owners, claims)
	}
	calls := 0
	outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { calls++; return nil })
	if err != nil || outcome != PublicationSourceCleanupRemovalBlocked || calls != 0 {
		t.Fatalf("late removal = %q, %v, callback calls=%d", outcome, err, calls)
	}
}

func TestFailUnstartedPublicationSourceCleanupDeliveryRejectsCompletedFrontierPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, guard, operationID, itemID, _ := setupStartedCleanupRemoval(t)
	outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return nil })
	if err != nil || outcome != PublicationSourceCleanupRemovalCompleted {
		t.Fatalf("complete removal = %q, %v", outcome, err)
	}
	retired, err := repository.FailUnstartedPublicationSourceCleanupDelivery(ctx, guard, "late retirement")
	if err != nil || retired {
		t.Fatalf("retire completed frontier = %v, %v; want false,nil", retired, err)
	}
	assertCleanupRemovalRetained(t, ctx, fixture, operationID)
	var completed any
	if err := fixture.db.NewRaw(`SELECT mutation_completed_at FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &completed); err != nil || completed == nil {
		t.Fatalf("completed frontier timestamp=%v, err=%v", completed, err)
	}
}

func TestFailUnstartedPublicationSourceCleanupDeliveryRollsBackLateOperationFailurePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, guard, operationID, itemID, _ := setupStartedCleanupRemoval(t)
	var snapshot []byte
	var revision uuid.UUID
	if err := fixture.db.NewRaw(`SELECT source_snapshot,borrower_revision FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &snapshot, &revision); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(ctx, `CREATE FUNCTION fail_cleanup_retirement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='failed' THEN RAISE EXCEPTION 'injected retirement commit failure'; END IF; RETURN NEW; END $$;
		CREATE CONSTRAINT TRIGGER fail_cleanup_retirement AFTER UPDATE ON operation DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_cleanup_retirement()`); err != nil {
		t.Fatal(err)
	}
	retired, retirementErr := repository.FailUnstartedPublicationSourceCleanupDelivery(ctx, guard, "safe retirement")
	if retirementErr == nil || retired || !strings.Contains(retirementErr.Error(), "injected retirement commit failure") {
		t.Fatalf("retirement = %v, %v; want injected deferred failure", retired, retirementErr)
	}
	var itemCount, ownerCount, claimCount int
	var state string
	if err := fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_item WHERE operation_id=?`, operationID).Scan(ctx, &itemCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, operationID).Scan(ctx, &ownerCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, operationID).Scan(ctx, &claimCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, operationID).Scan(ctx, &state); err != nil {
		t.Fatal(err)
	}
	var after []byte
	var afterRevision uuid.UUID
	if err := fixture.db.NewRaw(`SELECT source_snapshot,borrower_revision FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &after, &afterRevision); err != nil {
		t.Fatal(err)
	}
	if itemCount != 1 || ownerCount != 1 || claimCount != 1 || state != "running" || string(snapshot) != string(after) || revision != afterRevision {
		t.Fatalf("rollback rows=%d/%d/%d operation=%q snapshot=%t revision=%t", itemCount, ownerCount, claimCount, state, string(snapshot) == string(after), revision == afterRevision)
	}
	if _, err := fixture.db.ExecContext(ctx, `DROP TRIGGER fail_cleanup_retirement ON operation; DROP FUNCTION fail_cleanup_retirement()`); err != nil {
		t.Fatal(err)
	}
}

func retireAndAssert(t *testing.T, ctx context.Context, fixture publicationExecutionFixture, repository *SetupManagerRepository, guard PublicationSourceCleanupDeliveryGuard, operationID, itemID uuid.UUID, wantRetired, wantCompleted bool) {
	t.Helper()
	var snapshot []byte
	var revision uuid.UUID
	if err := fixture.db.NewRaw(`SELECT source_snapshot,borrower_revision FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &snapshot, &revision); err != nil {
		t.Fatal(err)
	}
	retired, err := repository.FailUnstartedPublicationSourceCleanupDelivery(ctx, guard, "changed source identity")
	if err != nil || retired != wantRetired {
		t.Fatalf("retire = %v, %v; want %v", retired, err, wantRetired)
	}
	var items, owners, claims int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_item WHERE operation_id=?`, operationID).Scan(ctx, &items); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, operationID).Scan(ctx, &owners); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, operationID).Scan(ctx, &claims); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, operationID).Scan(ctx, &state); err != nil {
		t.Fatal(err)
	}
	if wantRetired {
		if items != 0 || owners != 0 || claims != 0 || state != "failed" {
			t.Fatalf("retirement artifacts items/owners/claims=%d/%d/%d operation=%q", items, owners, claims, state)
		}
		return
	}
	if items != 1 || owners != 1 || claims != 1 || state != "running" {
		t.Fatalf("retained artifacts items/owners/claims=%d/%d/%d operation=%q", items, owners, claims, state)
	}
	var after []byte
	var afterRevision uuid.UUID
	var completed any
	if err := fixture.db.NewRaw(`SELECT source_snapshot,borrower_revision,mutation_completed_at FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &after, &afterRevision, &completed); err != nil {
		t.Fatal(err)
	}
	if string(snapshot) != string(after) || revision != afterRevision || (completed != nil) != wantCompleted {
		t.Fatalf("frontier changed snapshot=%t revision=%t completion=%v", string(snapshot) == string(after), revision == afterRevision, completed)
	}
	if strings.TrimSpace(state) == "" {
		t.Fatal("operation state is blank")
	}
}
