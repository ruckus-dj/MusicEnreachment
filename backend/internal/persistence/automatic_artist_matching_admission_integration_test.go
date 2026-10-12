//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestAutomaticArtistMatchingAdmissionIsIdempotentAndFencesStaleEventPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	fixture := newIncomingMatchFixture(t, ctx, db)
	setup := persistence.NewSetupManagerRepository(db)
	inventory := persistence.NewSourceInventoryRepository(db)
	client := openScanEnqueueRiver(t, db)

	var event persistence.IncomingMatchingReconcileEvent
	if err := db.NewSelect().Model(&event).Where("reason = ?", "analysis_success").Order("created_at ASC").Order("id ASC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read successful source-analysis outbox event: %v", err)
	}
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch real source-analysis outbox events: %v", err)
	}
	dispatchedEvent, err := inventory.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("read dispatched source-analysis event: %v", err)
	}
	event = *dispatchedEvent
	if event.State != "dispatched" || event.RegisteredJobID == nil {
		t.Fatalf("source-analysis event was not dispatched: %+v", event)
	}
	var providerFence struct {
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := db.NewRaw(`SELECT provider_id, endpoint, configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &providerFence); err != nil {
		t.Fatalf("read fixture provider source fence: %v", err)
	}
	epoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *event.RegisteredJobID, 1)
	if err != nil || !claimed || epoch < 1 {
		t.Fatalf("claim registered reconciliation job attempt 1: epoch=%d claimed=%v err=%v", epoch, claimed, err)
	}

	query := json.RawMessage(`{"schema_version":1,"query":"Fixture Artist","offset":0,"limit":1}`)
	admission := func() persistence.AutomaticArtistMatchingAdmission {
		providerSourceID, providerID := fixture.source, providerFence.ProviderID
		identity, endpoint := providerFence.Identity, providerFence.Endpoint
		cacheKey := "artist-search:0:1:Fixture Artist"
		providerOperationID := uuid.New()
		parent := &persistence.Operation{
			ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider",
			InputSnapshot: json.RawMessage(`{}`), Attempt: 1,
		}
		providerOperation := &persistence.Operation{
			ID: providerOperationID, Kind: "provider_artist_search", State: "queued", Stage: "queued",
			InputSnapshot: append(json.RawMessage(nil), query...), ProviderSourceID: &providerSourceID,
			ProviderID: &providerID, ProviderConfigurationIdentity: &identity, ProviderEndpoint: endpoint,
			ProviderCacheKey: &cacheKey, Attempt: 1,
		}
		return persistence.AutomaticArtistMatchingAdmission{
			Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parent.ID},
			Provider: persistence.MatchingProviderRequest{
				Operation: providerOperation,
				Args:      service.ProviderFetchOperationArgs{OperationID: providerOperation.ID},
			},
			GroupID: fixture.group.ID, ExpectedGroupRevision: fixture.group.Revision,
			GroupingEpoch: fixture.groupingEpoch, PolicyVersion: "matching-rapidfuzz-v1",
			ArtistQueryPlanVersion: 1, QueryDescriptor: append(json.RawMessage(nil), query...),
			EventID: event.ID, EventRiverJobID: *event.RegisteredJobID,
			EventRiverAttempt: 1, EventDeliveryEpoch: epoch,
		}
	}

	firstRequest := admission()
	first, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, firstRequest)
	if err != nil {
		t.Fatalf("admit automatic artist matching run: %v", err)
	}
	if first.AlreadyAdmitted || first.OriginalRunID != firstRequest.Parent.ID || first.OriginalOperationID != firstRequest.Parent.ID {
		t.Fatalf("first admission result = %+v, want a new receipt for %s", first, firstRequest.Parent.ID)
	}
	baseline := automaticArtistAdmissionCounts(t, ctx, db, fixture.group.ID)

	secondRequest := admission()
	second, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, secondRequest)
	if err != nil {
		t.Fatalf("repeat equivalent automatic artist admission: %v", err)
	}
	if !second.AlreadyAdmitted || second.OriginalRunID != first.OriginalRunID || second.OriginalOperationID != first.OriginalOperationID {
		t.Fatalf("repeat admission result = %+v, want original run and operation %s", second, first.OriginalRunID)
	}
	assertAutomaticArtistAdmissionCounts(t, ctx, db, fixture.group.ID, baseline)
	if second.Operation == nil || second.Operation.ID != first.OriginalOperationID {
		t.Fatalf("repeat admission did not return the original operation: %+v", second.Operation)
	}

	newEpoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *event.RegisteredJobID, 2)
	if err != nil || !claimed || newEpoch <= epoch {
		t.Fatalf("claim reconciliation redelivery attempt 2: epoch=%d claimed=%v err=%v", newEpoch, claimed, err)
	}
	oldJobID := *event.RegisteredJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='cancelled',finalized_at=now() WHERE id=?`, oldJobID).Exec(ctx); err != nil {
		t.Fatalf("cancel old River delivery: %v", err)
	}
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("replace cancelled reconciliation delivery: %v", err)
	}
	replaced, err := inventory.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil || replaced.RegisteredJobID == nil || *replaced.RegisteredJobID == oldJobID || replaced.DeliveryEpoch <= newEpoch || replaced.HighestDeliveryAttempt != 0 {
		t.Fatalf("event did not retain identity and fence replacement: event=%+v err=%v", replaced, err)
	}
	staleRequest := admission()
	staleRequest.Parent.ID = uuid.New()
	staleRequest.Provider.Operation.ID = uuid.New()
	if _, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, staleRequest); err == nil {
		t.Fatal("replaced attempt-1 event fence admitted work before checking the existing receipt")
	}
	assertAutomaticArtistAdmissionCounts(t, ctx, db, fixture.group.ID, baseline)
}

type automaticArtistAdmissionSnapshot struct {
	receipts, runs, targets, dependencies, parentOperations, providerOperations, parentJobs, providerJobs int
	generation                                                                                            int64
}

func automaticArtistAdmissionCounts(t *testing.T, ctx context.Context, db *bun.DB, groupID uuid.UUID) automaticArtistAdmissionSnapshot {
	t.Helper()
	var counts automaticArtistAdmissionSnapshot
	queries := []struct {
		query string
		dest  any
	}{
		{`SELECT count(*) FROM incoming_matching_automatic_admission`, &counts.receipts},
		{`SELECT count(*) FROM matching_run_target WHERE group_id=?`, &counts.targets},
		{`SELECT count(*) FROM matching_run mr JOIN matching_run_target target ON target.run_id=mr.id WHERE target.group_id=?`, &counts.runs},
		{`SELECT count(*) FROM matching_provider_dependency dependency JOIN matching_run_target target ON target.run_id=dependency.parent_run_id WHERE target.group_id=?`, &counts.dependencies},
		{`SELECT count(*) FROM operation WHERE kind='matching_evaluation_v1'`, &counts.parentOperations},
		{`SELECT count(*) FROM operation WHERE kind='provider_artist_search'`, &counts.providerOperations},
		{`SELECT count(*) FROM river_job WHERE kind='matching_evaluation_v1'`, &counts.parentJobs},
		{`SELECT count(*) FROM river_job WHERE kind='provider_fetch_v1'`, &counts.providerJobs},
	}
	for _, item := range queries {
		var err error
		if item.query != `SELECT count(*) FROM incoming_matching_automatic_admission` {
			err = db.NewRaw(item.query, groupID).Scan(ctx, item.dest)
		} else {
			err = db.NewRaw(item.query).Scan(ctx, item.dest)
		}
		if err != nil {
			t.Fatalf("read automatic admission state: %v", err)
		}
	}
	if err := db.NewRaw(`SELECT COALESCE(high_water,0) FROM incoming_matching_generation WHERE group_id=?`, groupID).Scan(ctx, &counts.generation); err != nil {
		t.Fatalf("read automatic matching target generation: %v", err)
	}
	return counts
}

func assertAutomaticArtistAdmissionCounts(t *testing.T, ctx context.Context, db *bun.DB, groupID uuid.UUID, expected automaticArtistAdmissionSnapshot) {
	t.Helper()
	actual := automaticArtistAdmissionCounts(t, ctx, db, groupID)
	if actual != expected {
		t.Fatalf("automatic admission state changed: got %+v, want %+v", actual, expected)
	}
}
