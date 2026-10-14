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
)

func TestStartupMatchingRunQuiescenceRecognizesAutomaticV2OwnerWithPostgreSQL(t *testing.T) {
	for _, state := range []string{"queued", "running"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := testpostgres.OpenMigrated(t)
			fixture := newIncomingMatchFixture(t, ctx, db)
			setup := persistence.NewSetupManagerRepository(db)
			inventory := persistence.NewSourceInventoryRepository(db)
			client := openScanEnqueueRiver(t, db)

			var event persistence.IncomingMatchingReconcileEvent
			if err := db.NewSelect().Model(&event).Where("reason = ?", "analysis_success").Order("created_at ASC").Order("id ASC").Limit(1).Scan(ctx); err != nil {
				t.Fatalf("read source-analysis reconciliation event: %v", err)
			}
			if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
				t.Fatalf("dispatch reconciliation events: %v", err)
			}
			dispatched, err := inventory.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
			if err != nil || dispatched.RegisteredJobID == nil {
				t.Fatalf("read dispatched reconciliation event: event=%+v err=%v", dispatched, err)
			}
			event = *dispatched
			epoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *event.RegisteredJobID, 1)
			if err != nil || !claimed {
				t.Fatalf("claim reconciliation delivery: epoch=%d claimed=%v err=%v", epoch, claimed, err)
			}

			var provider struct {
				ID       uuid.UUID `bun:"provider_id"`
				Endpoint string    `bun:"endpoint"`
				Identity string    `bun:"configuration_identity"`
			}
			if err := db.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &provider); err != nil {
				t.Fatalf("read fixture provider source: %v", err)
			}
			query := json.RawMessage(`{"schema_version":1,"query":"Fixture Artist","offset":0,"limit":1}`)
			parent := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
			providerSourceID, providerID := fixture.source, provider.ID
			identity, endpoint := provider.Identity, provider.Endpoint
			cacheKey := "artist-search:0:1:Fixture Artist"
			providerOperation := &persistence.Operation{
				ID: uuid.New(), Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
				ProviderSourceID: &providerSourceID, ProviderID: &providerID, ProviderConfigurationIdentity: &identity,
				ProviderEndpoint: endpoint, ProviderCacheKey: &cacheKey, Attempt: 1,
			}
			admission := persistence.AutomaticArtistMatchingAdmission{
				Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parent.ID},
				Provider: persistence.MatchingProviderRequest{Operation: providerOperation, Args: service.ProviderFetchOperationArgs{OperationID: providerOperation.ID}},
				GroupID:  fixture.group.ID, ExpectedGroupRevision: fixture.group.Revision,
				PolicyVersion: "matching-rapidfuzz-v1", ArtistQueryPlanVersion: 1, QueryDescriptor: query,
				EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: epoch,
			}
			admitted, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, admission)
			if err != nil || admitted.AlreadyAdmitted || admitted.OriginalRunID != parent.ID {
				t.Fatalf("admit real automatic version-2 matching run: result=%+v err=%v", admitted, err)
			}
			if state == "running" {
				if _, err := db.NewRaw(`UPDATE operation SET state='running',stage='waiting_provider',updated_at=now() WHERE id=?`, parent.ID).Exec(ctx); err != nil {
					t.Fatalf("set admitted matching operation running: %v", err)
				}
			}

			owned, err := setup.IsMatchingRuntimeScannerOwnedOperation(ctx, parent.ID)
			if err != nil || owned {
				t.Fatalf("unmarked version-2 run ownership = %v, err=%v; want false", owned, err)
			}
			if _, err := db.NewRaw(`UPDATE matching_run SET quiesced_at=now() WHERE id=?`, parent.ID).Exec(ctx); err != nil {
				t.Fatalf("mark admitted matching run quiesced: %v", err)
			}
			owned, err = setup.IsMatchingRuntimeScannerOwnedOperation(ctx, parent.ID)
			if err != nil || !owned {
				t.Fatalf("quiesced version-2 run ownership = %v, err=%v; want true", owned, err)
			}
			var operation struct {
				State string `bun:"state"`
				Stage string `bun:"stage"`
			}
			if err := db.NewRaw(`SELECT state,stage FROM operation WHERE id=?`, parent.ID).Scan(ctx, &operation); err != nil {
				t.Fatalf("read operation after quiescence marker: %v", err)
			}
			if operation.State != state || operation.Stage != "waiting_provider" {
				t.Fatalf("quiescence changed operation status: state=%q stage=%q, want state=%q stage=waiting_provider", operation.State, operation.Stage, state)
			}
		})
	}
}
