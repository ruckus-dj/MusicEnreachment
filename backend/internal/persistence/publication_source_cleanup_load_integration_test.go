//go:build integration

package persistence

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
)

type cleanupLoadState struct {
	OperationState string
	Attempt        int
	Snapshot       string
	ItemStates     []string
	OwnerCount     int
	ClaimCount     int
}

func readCleanupLoadState(t *testing.T, ctx context.Context, f publicationExecutionFixture, operationID uuid.UUID) cleanupLoadState {
	t.Helper()
	var state cleanupLoadState
	if err := f.db.NewRaw(`SELECT state,attempt,input_snapshot FROM operation WHERE id=?`, operationID).Scan(ctx, &state.OperationState, &state.Attempt, &state.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT state FROM source_cleanup_item WHERE operation_id=? ORDER BY id`, operationID).Scan(ctx, &state.ItemStates); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, operationID).Scan(ctx, &state.OwnerCount); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, operationID).Scan(ctx, &state.ClaimCount); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestLoadPublicationSourceCleanupDeliveryReturnsCompleteSortedSnapshotPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	guard, operationID := admitAndClaimSourceCleanup(t, ctx, f, repo)
	before := readCleanupLoadState(t, ctx, f, operationID)
	got, err := repo.LoadPublicationSourceCleanupDelivery(ctx, operationID)
	if err != nil {
		t.Fatal(err)
	}
	var itemIDs []uuid.UUID
	if err := f.db.NewRaw(`SELECT id FROM source_cleanup_item WHERE operation_id=?`, operationID).Scan(ctx, &itemIDs); err != nil {
		t.Fatal(err)
	}
	sort.Slice(itemIDs, func(i, j int) bool { return itemIDs[i].String() < itemIDs[j].String() })
	if got.OperationID != operationID || got.ApplicationAttempt != guard.ApplicationAttempt || got.Epoch != f.guard.Epoch || got.PositionID != f.guard.ExpectedPositionID || !reflect.DeepEqual(got.ItemIDs, itemIDs) || len(got.ItemIDs) == 0 || !got.RemovalEnabled {
		t.Fatalf("loaded input = %+v, want operation=%s attempt=%d epoch=%s position=%s items=%v enabled=true", got, operationID, guard.ApplicationAttempt, f.guard.Epoch, f.guard.ExpectedPositionID, itemIDs)
	}
	if after := readCleanupLoadState(t, ctx, f, operationID); !reflect.DeepEqual(after, before) {
		t.Fatalf("load mutated admitted delivery state: before=%+v after=%+v", before, after)
	}
	if before.OperationState != "queued" || before.OwnerCount != len(itemIDs) || before.ClaimCount != 1 {
		t.Fatalf("fixture was not admitted with retained ownership and claim: %+v", before)
	}
}

func TestLoadPublicationSourceCleanupDeliverySettingOffIsAdvisoryAndReadOnlyPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	_, operationID := admitAndClaimSourceCleanup(t, ctx, f, repo)
	if err := NewSettingsRepository(f.db).Set(ctx, "publication_remove_sources", "false"); err != nil {
		t.Fatal(err)
	}
	before := readCleanupLoadState(t, ctx, f, operationID)
	got, err := repo.LoadPublicationSourceCleanupDelivery(ctx, operationID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OperationID != operationID || len(got.ItemIDs) == 0 || got.RemovalEnabled {
		t.Fatalf("loaded disabled delivery = %+v, want valid nonempty delivery with RemovalEnabled=false", got)
	}
	if after := readCleanupLoadState(t, ctx, f, operationID); !reflect.DeepEqual(after, before) {
		t.Fatalf("disabled load mutated operation/item/ownership/claim state: before=%+v after=%+v", before, after)
	}
}

func TestLoadPublicationSourceCleanupDeliveryRejectsMissingAndWrongKindPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	if got, err := repo.LoadPublicationSourceCleanupDelivery(ctx, uuid.Nil); err == nil || got.OperationID != uuid.Nil {
		t.Fatalf("nil operation load = %+v, %v; want error", got, err)
	}
	if _, err := repo.LoadPublicationSourceCleanupDelivery(ctx, uuid.New()); err == nil {
		t.Fatal("missing operation load succeeded")
	}
	if _, err := repo.LoadPublicationSourceCleanupDelivery(ctx, f.guard.OperationID); err == nil {
		t.Fatal("publication operation was accepted as a cleanup delivery")
	}
}
