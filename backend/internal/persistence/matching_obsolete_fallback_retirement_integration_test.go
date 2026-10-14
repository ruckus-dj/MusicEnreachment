//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestObsoleteAutomaticFallbackRunRetirementPreservesCurrentGenerationPostgreSQL(t *testing.T) {
	ctx := context.Background()
	db := testpostgres.Open(t)
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	applyMatchingMigrationsThrough(t, ctx, db, collection, "20261211000000")

	callbackExecuted := false
	testAutomaticFallbackFiniteFlowWithDatabase(t, true, false, true, true, false, nil, nil, db,
		func(t *testing.T, ctx context.Context, db *bun.DB, oldRunID uuid.UUID, waitRevision, oldWakeID int64) bool {
			callbackExecuted = true
			setup := persistence.NewSetupManagerRepository(db)
			oldAcquisition, exists, err := setup.GetMatchingAcquisition(ctx, oldRunID)
			if err != nil || !exists || oldAcquisition.Intent.SchemaVersion != 3 || len(oldAcquisition.Intent.FallbackQueryPlan) == 0 || len(oldAcquisition.Intent.CapturedMembers) == 0 {
				t.Fatalf("read original automatic fallback intent: exists=%v intent=%+v err=%v", exists, oldAcquisition.Intent, err)
			}
			var oldGroupID uuid.UUID
			if err := db.NewRaw(`SELECT group_id FROM matching_run_target WHERE run_id=?`, oldRunID).Scan(ctx, &oldGroupID); err != nil {
				t.Fatalf("read original fallback group: %v", err)
			}
			var oldChildID uuid.UUID
			if err := db.NewRaw(`SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=? ORDER BY request_id LIMIT 1`, oldRunID).Scan(ctx, &oldChildID); err != nil {
				t.Fatalf("read original fallback provider child: %v", err)
			}
			var oldTargetGeneration int64
			if err := db.NewRaw(`SELECT target_generation FROM matching_run_target WHERE run_id=? AND group_id=?`, oldRunID, oldGroupID).Scan(ctx, &oldTargetGeneration); err != nil {
				t.Fatalf("read old fallback target generation: %v", err)
			}
			if oldAcquisition.Intent.FallbackProviderSource == nil {
				t.Fatal("original fallback intent omitted its provider source")
			}
			source := persistence.AutomaticFallbackProviderSource{
				ID: oldAcquisition.Intent.FallbackProviderSource.ID, ProviderID: oldAcquisition.Intent.FallbackProviderSource.ProviderID,
				Endpoint:              oldAcquisition.Intent.FallbackProviderSource.Endpoint,
				ConfigurationIdentity: oldAcquisition.Intent.FallbackProviderSource.ConfigurationIdentity,
			}
			if source.ID == uuid.Nil || source.ConfigurationIdentity == "" {
				t.Fatalf("original provider source identity is incomplete: %+v", source)
			}
			var plan policy.TypedFallbackQueryPlan
			if err := json.Unmarshal(oldAcquisition.Intent.FallbackQueryPlan, &plan); err != nil || len(plan.Stages) != 3 {
				t.Fatalf("decode saved finite fallback plan: plan=%+v err=%v", plan, err)
			}

			// Request a genuinely newer reconciliation generation. Its dispatched,
			// claimed event is the authority for the new automatic admission.
			inventory := persistence.NewSourceInventoryRepository(db)
			if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				return persistence.RequestIncomingMatchingReconciliationWith(ctx, tx, []uuid.UUID{oldGroupID})
			}); err != nil {
				t.Fatalf("request newer fallback generation: %v", err)
			}
			client := openScanEnqueueRiver(t, db)
			if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
				t.Fatalf("dispatch newer fallback reconciliation: %v", err)
			}
			var event persistence.IncomingMatchingReconcileEvent
			if err := db.NewSelect().Model(&event).Where("reason = ?", "current").Where("state = ?", "dispatched").Order("created_at DESC").Order("id DESC").Limit(1).Scan(ctx); err != nil {
				t.Fatalf("read newer dispatched reconciliation: %v", err)
			}
			if event.RegisteredJobID == nil {
				t.Fatal("new reconciliation event has no registered River delivery")
			}
			epoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *event.RegisteredJobID, 1)
			if err != nil || !claimed {
				t.Fatalf("claim newer reconciliation event: epoch=%d claimed=%v err=%v", epoch, claimed, err)
			}
			var generation int64
			if err := db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, oldGroupID).Scan(ctx, &generation); err != nil {
				t.Fatalf("read requested generation: %v", err)
			}
			grouping := persistence.NewIncomingGroupingRepository(db)
			if err := service.NewIncomingGroups(grouping).Refresh(ctx); err != nil {
				t.Fatalf("refresh current fallback group: %v", err)
			}
			groups, err := grouping.List(ctx)
			if err != nil || len(groups) != 1 || groups[0].ID != oldGroupID {
				t.Fatalf("read current fallback group: groups=%+v err=%v", groups, err)
			}
			stage := plan.Stages[0]
			kind, prefix, ok := policy.ProviderKindForFallbackStage(stage.Kind)
			if !ok {
				t.Fatalf("fallback stage has no provider mapping: %q", stage.Kind)
			}
			cacheKey := fmt.Sprintf("%s:%d:%d:%s", prefix, stage.Offset, stage.Limit, stage.Query)
			input, err := json.Marshal(struct {
				SchemaVersion int    `json:"schema_version"`
				Query         string `json:"query"`
				Offset        int    `json:"offset"`
				Limit         int    `json:"limit"`
			}{1, stage.Query, stage.Offset, stage.Limit})
			if err != nil {
				t.Fatalf("encode new first-stage provider input: %v", err)
			}
			providerID, sourceID, identity := source.ProviderID, source.ID, source.ConfigurationIdentity
			childID := uuid.New()
			child := &persistence.Operation{ID: childID, Kind: kind, State: "queued", Stage: "queued", InputSnapshot: input,
				ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderConfigurationIdentity: &identity,
				ProviderEndpoint: source.Endpoint, ProviderCacheKey: &cacheKey, Attempt: 1}
			parentID := uuid.New()
			parent := &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
			newAdmission, err := setup.AdmitAutomaticFallbackMatchingRun(ctx, client, persistence.AutomaticFallbackMatchingAdmission{
				Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
				Provider: persistence.MatchingProviderRequest{Operation: child, Args: service.ProviderFetchOperationArgs{OperationID: childID}},
				Source:   source, GroupID: oldGroupID, ExpectedGroupRevision: groups[0].Revision,
				PolicyVersion: oldAcquisition.Intent.PolicyVersion, Plan: plan,
				EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: epoch,
				RequestGeneration: generation,
			})
			if err != nil || newAdmission.AlreadyAdmitted || newAdmission.OriginalRunID == oldRunID {
				t.Fatalf("admit real newer fallback run: admission=%+v err=%v", newAdmission, err)
			}
			newRunID := newAdmission.OriginalRunID
			var newIntentBytes []byte
			if err := db.NewRaw(`SELECT intent FROM matching_acquisition WHERE run_id=?`, newRunID).Scan(ctx, &newIntentBytes); err != nil {
				t.Fatalf("read new admission capture: %v", err)
			}
			var newIntent persistence.MatchingAcquisitionIntent
			if err := json.Unmarshal(newIntentBytes, &newIntent); err != nil {
				t.Fatalf("decode new admission capture: %v", err)
			}
			if !equalJSONForTest(t, newIntent.CapturedMembers, oldAcquisition.Intent.CapturedMembers) || newIntent.FallbackProviderSource == nil ||
				newIntent.FallbackProviderSource.ConfigurationIdentity != source.ConfigurationIdentity {
				t.Fatalf("new run did not capture current source metadata with original provider identity: new=%+v old=%+v", newIntent, oldAcquisition.Intent)
			}

			// The finite-flow helper has actually run every provider worker. Retire
			// its registered deliveries administratively as fixture cleanup, then
			// quiesce with no active execution owner before recording legacy age.
			if _, err := db.NewRaw(`UPDATE river_job SET state='cancelled',finalized_at=transaction_timestamp()
				WHERE id IN (SELECT river_job_id FROM matching_delivery WHERE run_id=?)
				AND state IN ('available','pending','running','retryable','scheduled')`, oldRunID).Exec(ctx); err != nil {
				t.Fatalf("settle old matching wake jobs: %v", err)
			}
			if _, err := db.NewRaw(`UPDATE river_job SET state='completed',finalized_at=transaction_timestamp()
				WHERE args->>'operation_id' IN (SELECT request_id::text FROM matching_provider_dependency WHERE parent_run_id=?)
				AND state IN ('available','pending','running','retryable','scheduled')`, oldRunID).Exec(ctx); err != nil {
				t.Fatalf("settle real provider jobs: %v", err)
			}
			var unsettledChildren int
			if err := db.NewRaw(`SELECT count(*) FROM matching_provider_dependency dependency
				JOIN operation child ON child.id=dependency.request_id
				LEFT JOIN river_job job ON job.id=child.river_job_id
				WHERE dependency.parent_run_id=? AND (dependency.terminal_outcome IS NULL OR child.state NOT IN ('succeeded','failed') OR
				child.finished_at IS NULL OR job.state IN ('available','pending','running','retryable','scheduled'))`, oldRunID).Scan(ctx, &unsettledChildren); err != nil || unsettledChildren != 0 {
				t.Fatalf("old provider outcomes are not genuinely terminal: unsettled=%d err=%v", unsettledChildren, err)
			}
			if _, err := db.NewRaw(`UPDATE matching_delivery SET consumed_at=transaction_timestamp() WHERE run_id=? AND consumed_at IS NULL`, oldRunID).Exec(ctx); err != nil {
				t.Fatalf("acknowledge settled old deliveries: %v", err)
			}
			past := time.Now().UTC().Add(-96 * time.Hour)
			if _, err := db.NewRaw(`UPDATE operation SET finished_at=?,updated_at=? WHERE id IN
				(SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=?)`, past, past, oldRunID).Exec(ctx); err != nil {
				t.Fatalf("age actual terminal provider outcomes: %v", err)
			}
			if err := setup.QuiesceObsoleteAutomaticMatchingRuns(ctx); err != nil {
				t.Fatalf("quiesce obsolete fallback run: %v", err)
			}
			var owners int
			if err := db.NewRaw(`SELECT count(*) FROM matching_run WHERE id=? AND
				(active_river_job_id IS NOT NULL OR active_river_attempt IS NOT NULL OR active_execution_epoch IS NOT NULL)`, oldRunID).Scan(ctx, &owners); err != nil || owners != 0 {
				t.Fatalf("old fallback execution owners=%d err=%v", owners, err)
			}
			oldMarker := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)
			if _, err := db.NewRaw(`UPDATE matching_run SET quiesced_at=? WHERE id=?`, oldMarker, oldRunID).Exec(ctx); err != nil {
				t.Fatalf("seed pre-schema-12 historical quiescence marker: %v", err)
			}

			counts := readFallbackRetirementCounts(t, ctx, db, oldRunID, oldChildID)
			for _, name := range []string{"acquisition", "acquisition_phase", "calculation", "search_receipt", "lookup_receipt", "handoff", "fallback_seal", "request_registration", "provider_dependency", "target", "delivery"} {
				if counts[name] == 0 {
					t.Fatalf("old fallback retirement baseline %s is empty: %+v", name, counts)
				}
			}
			if counts["retirement_authorization"] != 0 {
				t.Fatalf("pre-cleanup retirement authorization count=%d", counts["retirement_authorization"])
			}
			var highWaterBefore int64
			if err := db.NewRaw(`SELECT high_water FROM incoming_matching_generation WHERE group_id=?`, oldGroupID).Scan(ctx, &highWaterBefore); err != nil {
				t.Fatalf("read generation high-water before retirement: %v", err)
			}
			if highWaterBefore <= oldTargetGeneration {
				t.Fatalf("new automatic admission did not advance high-water past old target: high-water=%d old-target=%d", highWaterBefore, oldTargetGeneration)
			}
			var currentRun uuid.UUID
			if err := db.NewRaw(`SELECT current_run_id FROM incoming_matching_generation WHERE group_id=?`, oldGroupID).Scan(ctx, &currentRun); err != nil || currentRun != newRunID {
				t.Fatalf("current run=%s, want newly admitted fallback run=%s err=%v", currentRun, newRunID, err)
			}
			var cacheCount int
			if err := db.NewRaw(`SELECT count(*) FROM provider_response_cache WHERE provider_source_id=?`, source.ID).Scan(ctx, &cacheCount); err != nil || cacheCount == 0 {
				t.Fatalf("accepted provider cache baseline=%d err=%v", cacheCount, err)
			}
			newGraphCounts := make(map[string]int)
			for name, query := range map[string]string{
				"acquisition": `SELECT count(*) FROM matching_acquisition WHERE run_id=?`,
				"target":      `SELECT count(*) FROM matching_run_target WHERE run_id=?`,
				"delivery":    `SELECT count(*) FROM matching_delivery WHERE run_id=?`,
			} {
				var count int
				if err := db.NewRaw(query, newRunID).Scan(ctx, &count); err != nil || count == 0 {
					t.Fatalf("new matching graph baseline %s=%d err=%v", name, count, err)
				}
				newGraphCounts[name] = count
			}
			// Capture every old provider request and its terminal status before the
			// direct obsolete-run deletion. That deletion removes the run-owned proof
			// graph and the parent operation but deliberately leaves ordinary
			// successful provider operations for the generic succeeded-operation
			// cleanup to retire once they are aged and unreferenced.
			var oldProviderIDs []uuid.UUID
			if err := db.NewRaw(`SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=? ORDER BY request_id`, oldRunID).Scan(ctx, &oldProviderIDs); err != nil || len(oldProviderIDs) == 0 {
				t.Fatalf("capture old fallback provider requests: count=%d err=%v", len(oldProviderIDs), err)
			}
			for _, id := range oldProviderIDs {
				var state string
				if err := db.NewRaw(`SELECT state FROM operation WHERE id=?`, id).Scan(ctx, &state); err != nil || state != "succeeded" {
					t.Fatalf("old fallback provider request %s state=%q err=%v, want succeeded", id, state, err)
				}
			}
			var newRootState, newRootStage string
			if err := db.NewRaw(`SELECT state,stage FROM operation WHERE id=(SELECT operation_id FROM matching_run WHERE id=?)`, newRunID).Scan(ctx, &newRootState, &newRootStage); err != nil {
				t.Fatalf("read new fallback root status: %v", err)
			}
			var newReceipt []byte
			if err := db.NewRaw(`SELECT fingerprint FROM incoming_matching_automatic_admission WHERE original_run_id=?`, newRunID).Scan(ctx, &newReceipt); err != nil {
				t.Fatalf("capture new fallback admission receipt: %v", err)
			}
			var cachePayloadBefore []byte
			var cacheRevisionBefore string
			if err := db.NewRaw(`SELECT payload,revision FROM provider_response_cache WHERE provider_source_id=? AND cache_key=?`, source.ID, cacheKey).Scan(ctx, &cachePayloadBefore, &cacheRevisionBefore); err != nil {
				t.Fatalf("capture accepted provider cache before cleanup: %v", err)
			}

			applyMatchingMigration(t, ctx, db, collection, "20261212000000")
			if err := setup.DeleteObsoleteQuiescedMatchingRuns(ctx, time.Now().UTC()); err != nil {
				t.Fatalf("retire obsolete quiesced fallback run: %v", err)
			}
			assertObsoleteDirectRetirementClosure(t, ctx, db, oldRunID, oldChildID)
			for _, id := range oldProviderIDs {
				var retained int
				if err := db.NewRaw(`SELECT count(*) FROM operation WHERE id=? AND state='succeeded'`, id).Scan(ctx, &retained); err != nil || retained != 1 {
					t.Fatalf("direct retirement changed old successful provider request %s count=%d err=%v, want retained", id, retained, err)
				}
			}
			// The second phase retires the aged, now-unreferenced old provider
			// operations. The generic cleanup's provider-dependency guard preserves
			// the fresh new provider operation and any child a surviving dependency
			// still references.
			if err := setup.DeleteSucceededBefore(ctx, time.Now().UTC().Add(-96*time.Hour)); err != nil {
				t.Fatalf("cleanup aged obsolete provider operations: %v", err)
			}
			assertFallbackRetirementGraph(t, ctx, db, oldRunID, oldChildID, false)
			for _, id := range oldProviderIDs {
				var retained int
				if err := db.NewRaw(`SELECT count(*) FROM operation WHERE id=?`, id).Scan(ctx, &retained); err != nil || retained != 0 {
					t.Fatalf("aged obsolete provider request %s count=%d err=%v, want cleanup", id, retained, err)
				}
			}
			var retainedRootState, retainedRootStage string
			if err := db.NewRaw(`SELECT state,stage FROM operation WHERE id=(SELECT operation_id FROM matching_run WHERE id=?)`, newRunID).Scan(ctx, &retainedRootState, &retainedRootStage); err != nil || retainedRootState != newRootState || retainedRootStage != newRootStage {
				t.Fatalf("retirement changed new fallback root: state=%q stage=%q, want state=%q stage=%q err=%v", retainedRootState, retainedRootStage, newRootState, newRootStage, err)
			}
			var retainedReceipt []byte
			if err := db.NewRaw(`SELECT fingerprint FROM incoming_matching_automatic_admission WHERE original_run_id=?`, newRunID).Scan(ctx, &retainedReceipt); err != nil || string(retainedReceipt) != string(newReceipt) {
				t.Fatalf("retirement changed new fallback admission fingerprint: before=%s after=%s err=%v", newReceipt, retainedReceipt, err)
			}
			var retainedIntent []byte
			if err := db.NewRaw(`SELECT intent FROM matching_acquisition WHERE run_id=?`, newRunID).Scan(ctx, &retainedIntent); err != nil || !sameJSONValueForTest(t, newIntentBytes, retainedIntent) {
				t.Fatalf("retirement changed new fallback source intent: before=%s after=%s err=%v", newIntentBytes, retainedIntent, err)
			}
			var retainedCachePayload []byte
			var retainedCacheRevision string
			if err := db.NewRaw(`SELECT payload,revision FROM provider_response_cache WHERE provider_source_id=? AND cache_key=?`, source.ID, cacheKey).Scan(ctx, &retainedCachePayload, &retainedCacheRevision); err != nil ||
				!sameJSONValueForTest(t, cachePayloadBefore, retainedCachePayload) || retainedCacheRevision != cacheRevisionBefore {
				t.Fatalf("retirement changed accepted provider cache: revision=%q want=%q payload=%s want=%s err=%v", retainedCacheRevision, cacheRevisionBefore, retainedCachePayload, cachePayloadBefore, err)
			}
			var retainedHighWater int64
			var retainedRun uuid.UUID
			if err := db.NewRaw(`SELECT high_water,current_run_id FROM incoming_matching_generation WHERE group_id=?`, oldGroupID).Scan(ctx, &retainedHighWater, &retainedRun); err != nil || retainedHighWater != highWaterBefore || retainedRun != newRunID {
				t.Fatalf("retirement changed current generation: high-water=%d want=%d run=%s want=%s err=%v", retainedHighWater, highWaterBefore, retainedRun, newRunID, err)
			}
			for name, query := range map[string]string{
				"acquisition": `SELECT count(*) FROM matching_acquisition WHERE run_id=?`,
				"target":      `SELECT count(*) FROM matching_run_target WHERE run_id=?`,
				"delivery":    `SELECT count(*) FROM matching_delivery WHERE run_id=?`,
			} {
				var count int
				if err := db.NewRaw(query, newRunID).Scan(ctx, &count); err != nil || count != newGraphCounts[name] {
					t.Fatalf("retirement changed new fallback %s graph: count=%d before=%d err=%v", name, count, newGraphCounts[name], err)
				}
			}
			var retainedCache int
			if err := db.NewRaw(`SELECT count(*) FROM provider_response_cache WHERE provider_source_id=?`, source.ID).Scan(ctx, &retainedCache); err != nil || retainedCache != cacheCount {
				t.Fatalf("retirement changed accepted provider cache: before=%d after=%d err=%v", cacheCount, retainedCache, err)
			}
			return false
		}, false, false, true)
	if !callbackExecuted {
		t.Fatal("obsolete fallback retirement callback did not execute")
	}
}

func assertObsoleteDirectRetirementClosure(t *testing.T, ctx context.Context, db interface {
	NewRaw(string, ...any) *bun.RawQuery
}, oldRunID, oldChildID uuid.UUID) {
	t.Helper()
	counts := readFallbackRetirementCounts(t, ctx, db, oldRunID, oldChildID)
	for name, count := range counts {
		if name == "child_operation" {
			if count != 1 {
				t.Fatalf("direct retirement child operation count=%d, want one surviving ordinary provider operation", count)
			}
			continue
		}
		if count != 0 {
			t.Fatalf("direct retirement run-owned closure %s count=%d, want zero", name, count)
		}
	}
}

func sameJSONValueForTest(t *testing.T, left, right []byte) bool {
	t.Helper()
	var leftValue, rightValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		t.Fatalf("decode left provider cache payload: %v", err)
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		t.Fatalf("decode right provider cache payload: %v", err)
	}
	return equalJSONForTest(t, leftValue, rightValue)
}
