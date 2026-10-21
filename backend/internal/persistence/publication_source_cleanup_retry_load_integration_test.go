//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestLoadPublicationSourceCleanupExplicitRetryDeliveryUsesGrantReferencesPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture, repository, variantID := publicationSourceCleanupReadyFixture(t)
	if _, err := fixture.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-remove-v1' WHERE media_variant_id=?`, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var work SourceAnalysisWork
	if err := fixture.db.NewSelect().Model(&work).Where("id IN (SELECT work_id FROM source_work_variant_identity WHERE media_variant_id=?)", variantID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	addCleanupRetrySourceCopies(t, ctx, fixture, work, "loader-second.flac", "loader-third.flac")
	oldGuard, originalID := admitAndClaimSourceCleanup(t, ctx, fixture, repository)
	if started, err := repository.StartPublicationSourceCleanupExecution(ctx, oldGuard); err != nil || !started {
		t.Fatalf("start original cleanup=%t,%v", started, err)
	}
	var items []SourceCleanupItem
	if err := fixture.db.NewSelect().Model(&items).Where("operation_id=?", originalID).Order("id ASC").Scan(ctx); err != nil || len(items) != 3 {
		t.Fatalf("original items=%d,%v", len(items), err)
	}
	for i, item := range items {
		if i == 1 {
			if result, err := repository.RemovePublicationSourceCleanupItem(ctx, oldGuard, item.ID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return context.Canceled }); result != PublicationSourceCleanupRemovalBlocked || err == nil {
				t.Fatalf("make original item uncertain=%s,%v", result, err)
			}
			continue
		}
		if result, err := repository.RemovePublicationSourceCleanupItem(ctx, oldGuard, item.ID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return nil }); result != PublicationSourceCleanupRemovalCompleted || err != nil {
			t.Fatalf("complete original item=%s,%v", result, err)
		}
	}
	if failed, err := repository.FailPublicationSourceCleanupDelivery(ctx, oldGuard, "callback returned uncertain"); err != nil || !failed {
		t.Fatalf("fail original cleanup=%t,%v", failed, err)
	}
	admitted, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalID, new(publicationSourceCleanupTestInserter))
	if err != nil || admitted.OperationID == uuid.Nil || len(admitted.Blockers) != 0 {
		t.Fatalf("admit retry=%+v,%v", admitted, err)
	}
	var grant SourceCleanupExplicitRetry
	if err := fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id=?", admitted.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var references []SourceCleanupExplicitRetryItem
	if err := fixture.db.NewSelect().Model(&references).Where("retry_id=?", grant.ID).Order("item_id ASC").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := repository.LoadPublicationSourceCleanupExplicitRetryDelivery(ctx, admitted.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := make([]uuid.UUID, 0, len(references))
	for _, reference := range references {
		wantIDs = append(wantIDs, reference.ItemID)
	}
	if !reflect.DeepEqual(got.ItemIDs, wantIDs) || got.OperationID != admitted.OperationID || got.ApplicationAttempt != 1 || got.Epoch != grant.Epoch || got.PositionID != grant.PositionID {
		t.Fatalf("loaded retry=%+v, want item references %v", got, wantIDs)
	}
	var originalAfter Operation
	if err := fixture.db.NewSelect().Model(&originalAfter).Where("id=?", originalID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var originalAfterSnapshot publicationSourceCleanupOperationSnapshot
	if err := json.Unmarshal([]byte(originalAfter.InputSnapshot), &originalAfterSnapshot); err != nil {
		t.Fatal(err)
	}
	if originalAfter.Kind != "cleanup_publication_sources_v1" || originalAfter.State != "failed" || originalAfterSnapshot.Version != 1 {
		t.Fatalf("loader modified original operation: %+v", originalAfter)
	}
}

func TestLoadPublicationSourceCleanupExplicitRetryDeliveryRejectsWrongKindPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, _, repository, _, originalID := failedPublicationSourceCleanupForRetry(t)
	if _, err := repository.LoadPublicationSourceCleanupExplicitRetryDelivery(ctx, originalID); err == nil {
		t.Fatal("wrong operation kind unexpectedly loaded as retry")
	}
}
