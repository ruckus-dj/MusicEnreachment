//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestCompletedSourceCleanupFiltersReAdmissionButPreservesPublicAssessmentPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantS := publicationSourceCleanupReadyFixture(t)
	if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-remove-v1' WHERE media_variant_id=?`, variantS).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var workS SourceAnalysisWork
	if err := f.db.NewSelect().Model(&workS).Where("id IN (SELECT work_id FROM source_work_variant_identity WHERE media_variant_id=?)", variantS).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	guardS, operationS := admitAndClaimSourceCleanup(t, ctx, f, repo)
	if started, err := repo.StartPublicationSourceCleanupExecution(ctx, guardS); err != nil || !started {
		t.Fatalf("start S cleanup=%t,%v", started, err)
	}
	var itemS SourceCleanupItem
	if err := f.db.NewSelect().Model(&itemS).Where("operation_id=?", operationS).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	callbacksS := 0
	if outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guardS, itemS.ID, func(context.Context, PublicationSourceCleanupRemovalRequest) error {
		callbacksS++
		return nil
	}); err != nil || outcome != PublicationSourceCleanupRemovalCompleted || callbacksS != 1 {
		t.Fatalf("remove S=%s,%v callbacks=%d", outcome, err, callbacksS)
	}
	if settled, err := repo.SettlePublicationSourceCleanup(ctx, guardS); err != nil || !settled {
		t.Fatalf("settle S=%t,%v", settled, err)
	}
	var markersS, ownersS int
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_completion WHERE work_id=?`, workS.ID).Scan(ctx, &markersS); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, operationS).Scan(ctx, &ownersS); err != nil {
		t.Fatal(err)
	}
	if markersS != 1 || ownersS != 0 {
		t.Fatalf("settled S markers=%d old owners=%d", markersS, ownersS)
	}

	addCleanupRetrySourceCopies(t, ctx, f, workS, "completion-admission-T.flac")
	var variantT uuid.UUID
	var workT SourceAnalysisWork
	if err := f.db.NewRaw(`SELECT v.id,w.id FROM source_work_variant_identity i JOIN media_variant v ON v.id=i.media_variant_id JOIN source_analysis_work w ON w.id=i.work_id WHERE w.relative_path='completion-admission-T.flac'`).Scan(ctx, &variantT, &workT.ID); err != nil {
		t.Fatal(err)
	}
	if workT.ID == workS.ID {
		t.Fatalf("T reused S work %s", workS.ID)
	}
	if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-remove-v1' WHERE media_variant_id=?`, variantT).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assessmentBefore, err := repo.ReadPublicationSourceCleanupAssessment(ctx, f.guard.ExpectedPositionID)
	if err != nil || len(assessmentBefore.Candidates) != 2 {
		t.Fatalf("public assessment before admission candidates=%d,%v; want S and T", len(assessmentBefore.Candidates), err)
	}
	guardT, operationT := admitAndClaimSourceCleanup(t, ctx, f, repo)
	assessmentAfter, err := repo.ReadPublicationSourceCleanupAssessment(ctx, f.guard.ExpectedPositionID)
	if err != nil || len(assessmentAfter.Candidates) != len(assessmentBefore.Candidates) {
		t.Fatalf("public assessment after admission candidates=%d,%v; before=%d", len(assessmentAfter.Candidates), err, len(assessmentBefore.Candidates))
	}
	if started, err := repo.StartPublicationSourceCleanupExecution(ctx, guardT); err != nil || !started {
		t.Fatalf("start T cleanup=%t,%v", started, err)
	}
	var itemT SourceCleanupItem
	if err := f.db.NewSelect().Model(&itemT).Where("operation_id=?", operationT).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var snapshot publicationSourceCleanupSnapshot
	if err := decodeStrictJSON(itemT.SourceSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.WorkID != workT.ID || snapshot.SourceLocationID == workS.LocationID {
		t.Fatalf("T snapshot work=%s location=%s; S work=%s location=%s", snapshot.WorkID, snapshot.SourceLocationID, workS.ID, workS.LocationID)
	}
	callbacksT := 0
	if outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guardT, itemT.ID, func(context.Context, PublicationSourceCleanupRemovalRequest) error {
		callbacksT++
		return nil
	}); err != nil || outcome != PublicationSourceCleanupRemovalCompleted || callbacksT != 1 {
		t.Fatalf("remove T=%s,%v callbacks=%d", outcome, err, callbacksT)
	}
	if settled, err := repo.SettlePublicationSourceCleanup(ctx, guardT); err != nil || !settled {
		t.Fatalf("settle T=%t,%v", settled, err)
	}
	var markersSAfter, markersT, oldOwners, newOwners int
	for _, count := range []struct {
		query string
		id    uuid.UUID
		out   *int
	}{
		{`SELECT count(*) FROM source_cleanup_completion WHERE work_id=?`, workS.ID, &markersSAfter},
		{`SELECT count(*) FROM source_cleanup_completion WHERE work_id=?`, workT.ID, &markersT},
		{`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, operationS, &oldOwners},
		{`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, operationT, &newOwners},
	} {
		if err := f.db.NewRaw(count.query, count.id).Scan(ctx, count.out); err != nil {
			t.Fatal(err)
		}
	}
	if markersSAfter != 1 || markersT != 1 || oldOwners != 0 || newOwners != 0 {
		t.Fatalf("final markers S=%d T=%d owners old=%d new=%d", markersSAfter, markersT, oldOwners, newOwners)
	}
}

func TestCompletedItemRetainedInOwnLiveDeliveryDoesNotBlockNextItemPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantS := publicationSourceCleanupReadyFixture(t)
	if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-remove-v1' WHERE media_variant_id=?`, variantS).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var workS SourceAnalysisWork
	if err := f.db.NewSelect().Model(&workS).Where("id IN (SELECT work_id FROM source_work_variant_identity WHERE media_variant_id=?)", variantS).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	addCleanupRetrySourceCopies(t, ctx, f, workS, "live-delivery-T.flac")
	var workTID uuid.UUID
	if err := f.db.NewRaw(`SELECT id FROM source_analysis_work WHERE relative_path='live-delivery-T.flac'`).Scan(ctx, &workTID); err != nil {
		t.Fatal(err)
	}
	if workTID == workS.ID {
		t.Fatalf("T reused S work %s", workS.ID)
	}
	if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-remove-v1' WHERE media_variant_id IN (SELECT media_variant_id FROM source_analysis_work w JOIN source_work_variant_identity i ON i.work_id=w.id WHERE w.relative_path='live-delivery-T.flac')`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	guard, operationID := admitAndClaimSourceCleanup(t, ctx, f, repo)
	if started, err := repo.StartPublicationSourceCleanupExecution(ctx, guard); err != nil || !started {
		t.Fatalf("start two-item cleanup=%t,%v", started, err)
	}
	var items []SourceCleanupItem
	if err := f.db.NewSelect().Model(&items).Where("operation_id=?", operationID).Scan(ctx); err != nil || len(items) != 2 {
		t.Fatalf("two-item cleanup items=%d,%v", len(items), err)
	}
	itemS, itemT := SourceCleanupItem{}, SourceCleanupItem{}
	for _, item := range items {
		var snapshot publicationSourceCleanupSnapshot
		if err := decodeStrictJSON(item.SourceSnapshot, &snapshot); err != nil {
			t.Fatal(err)
		}
		switch snapshot.WorkID {
		case workS.ID:
			itemS = item
		default:
			itemT = item
		}
	}
	if itemS.ID == uuid.Nil || itemT.ID == uuid.Nil {
		t.Fatalf("cleanup items did not contain distinct S and T: S=%s T=%s", itemS.ID, itemT.ID)
	}
	callsS, callsT := 0, 0
	if outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, itemS.ID, func(context.Context, PublicationSourceCleanupRemovalRequest) error {
		callsS++
		return nil
	}); err != nil || outcome != PublicationSourceCleanupRemovalCompleted || callsS != 1 {
		t.Fatalf("remove S in live operation=%s,%v callbacks=%d", outcome, err, callsS)
	}
	var completedS, retainedS, pendingT int
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_completion WHERE work_id=?`, workS.ID).Scan(ctx, &completedS); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=? AND item_id=?`, operationID, itemS.ID).Scan(ctx, &retainedS); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_item WHERE operation_id=? AND id=? AND mutation_started_at IS NULL AND mutation_completed_at IS NULL`, operationID, itemT.ID).Scan(ctx, &pendingT); err != nil {
		t.Fatal(err)
	}
	if completedS != 1 || retainedS != 1 || pendingT != 1 {
		t.Fatalf("before T: completed S markers=%d retained S owners=%d pending T=%d", completedS, retainedS, pendingT)
	}
	if outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, itemT.ID, func(context.Context, PublicationSourceCleanupRemovalRequest) error {
		callsT++
		return nil
	}); err != nil || outcome != PublicationSourceCleanupRemovalCompleted || callsT != 1 {
		t.Fatalf("remove T after completed S in same operation=%s,%v callbacks=%d", outcome, err, callsT)
	}
	if settled, err := repo.SettlePublicationSourceCleanup(ctx, guard); err != nil || !settled {
		t.Fatalf("settle two-item cleanup=%t,%v", settled, err)
	}
	var markerS, markerT, owners int
	for _, count := range []struct {
		query string
		id    uuid.UUID
		out   *int
	}{
		{`SELECT count(*) FROM source_cleanup_completion WHERE work_id=?`, workS.ID, &markerS},
		{`SELECT count(*) FROM source_cleanup_completion WHERE work_id=?`, workTID, &markerT},
		{`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, operationID, &owners},
	} {
		if err := f.db.NewRaw(count.query, count.id).Scan(ctx, count.out); err != nil {
			t.Fatal(err)
		}
	}
	if markerS != 1 || markerT != 1 || owners != 0 {
		t.Fatalf("settled two-item cleanup markers S=%d T=%d owners=%d", markerS, markerT, owners)
	}
	if callsS != 1 || callsT != 1 {
		t.Fatalf("settled callback counts S=%d T=%d", callsS, callsT)
	}
}
