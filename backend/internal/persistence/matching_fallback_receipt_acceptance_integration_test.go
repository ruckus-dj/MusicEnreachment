//go:build integration

package persistence_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func TestAutomaticFallbackRecordingReceiptSurvivesCacheRefreshPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAutomaticMatchingTargetFenceFixture(t)
	ctx := fixture.ctx
	var target struct {
		GroupID  uuid.UUID `bun:"group_id"`
		Revision string    `bun:"captured_group_revision"`
	}
	if err := fixture.db.NewRaw(`SELECT group_id,captured_group_revision FROM matching_run_target WHERE run_id=?`, fixture.runID).Scan(ctx, &target); err != nil {
		t.Fatalf("read target: %v", err)
	}
	var event persistence.IncomingMatchingReconcileEvent
	if err := fixture.db.NewSelect().Model(&event).Where("state = ? AND delivery_claimed", "dispatched").Order("created_at DESC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read claimed reconciliation event: %v", err)
	}
	var source struct {
		ID         uuid.UUID `bun:"provider_source_id"`
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := fixture.db.NewRaw(`SELECT id AS provider_source_id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=(SELECT provider_source_id FROM matching_run WHERE id=?)`, fixture.runID).Scan(ctx, &source); err != nil {
		t.Fatalf("read provider source: %v", err)
	}
	var epoch int64
	if err := fixture.db.NewRaw(`SELECT delivery_epoch FROM incoming_matching_reconcile_event WHERE id=?`, event.ID).Scan(ctx, &epoch); err != nil {
		t.Fatalf("read reconciliation epoch: %v", err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE media_metadata_result SET observed_tags='{"TITLE":["Receipt Track"]}'::jsonb`).Exec(ctx); err != nil {
		t.Fatalf("set recording-only metadata: %v", err)
	}
	planned, artistBearing := policy.PlanFallbackQueries([]map[string][]string{{"TITLE": {"Receipt Track"}}})
	if artistBearing || len(planned.Stages) == 0 || planned.Stages[0].Kind != "track" {
		t.Fatalf("unexpected recording fallback plan: %+v artistBearing=%v", planned, artistBearing)
	}
	stage := planned.Stages[0]
	parentID, providerOperationID := uuid.New(), uuid.New()
	providerKind, cachePrefix, ok := policy.ProviderKindForFallbackStage(stage.Kind)
	if !ok {
		t.Fatalf("fallback stage %q has no provider mapping", stage.Kind)
	}
	key, identity := cachePrefix+":0:10:"+stage.Query, source.Identity
	providerID, sourceID := source.ProviderID, source.ID
	input, err := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}{1, stage.Query, 0, 10})
	if err != nil {
		t.Fatalf("encode provider request: %v", err)
	}
	provider := &persistence.Operation{ID: providerOperationID, Kind: providerKind, State: "queued", Stage: "queued", InputSnapshot: input,
		ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderConfigurationIdentity: &identity,
		ProviderEndpoint: source.Endpoint, ProviderCacheKey: &key, Attempt: 1}
	plan := policy.TypedFallbackQueryPlan{SchemaVersion: planned.SchemaVersion, Stages: planned.Stages}
	client := openScanEnqueueRiver(t, fixture.db)
	admitted, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(ctx, client, persistence.AutomaticFallbackMatchingAdmission{
		Parent:     &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1},
		ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Provider:   persistence.MatchingProviderRequest{Operation: provider, Args: service.ProviderFetchOperationArgs{OperationID: providerOperationID}},
		Source:     persistence.AutomaticFallbackProviderSource{ID: source.ID, ProviderID: source.ProviderID, Endpoint: source.Endpoint, ConfigurationIdentity: source.Identity},
		GroupID:    target.GroupID, ExpectedGroupRevision: target.Revision, PolicyVersion: service.MatchingPolicyVersion,
		Plan: plan, EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: event.HighestDeliveryAttempt, EventDeliveryEpoch: epoch,
	})
	if err != nil || admitted.AlreadyAdmitted || admitted.Operation == nil {
		t.Fatalf("admit recording fallback operation: result=%+v err=%v", admitted, err)
	}
	op, err := fixture.repository.GetOperation(ctx, providerOperationID)
	if err != nil {
		t.Fatalf("read registered recording operation: %v", err)
	}
	if op.RiverJobID == nil {
		t.Fatal("fallback operation did not receive its registered River job")
	}
	deliveryEpoch, claimed, err := fixture.repository.ClaimProviderOperationDelivery(ctx, op.ID, op.Attempt, *op.RiverJobID)
	if err != nil || !claimed {
		t.Fatalf("claim provider delivery: claimed=%v err=%v", claimed, err)
	}
	operations := service.NewOperations(fixture.repository)
	if err := operations.RunningForProviderDelivery(ctx, op, deliveryEpoch, "fetching_provider"); err != nil {
		t.Fatalf("start provider delivery: %v", err)
	}
	cache := persistence.NewProviderCacheRepository(fixture.db)
	ticket, err := cache.BeginRefresh(ctx, source.ID, key, source.Identity)
	if err != nil {
		t.Fatalf("reserve initial recording search: %v", err)
	}
	acceptedA := json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":2,"offset":0,"recordings":[{"id":"recording-a","title":"First","releases":[{"id":"release-a","title":"One"},{"id":"release-b","title":"Two"}],"unknown":{"z":1,"a":2}},{"id":"recording-b","title":"Second","score":0,"releases":[{"id":"release-c","title":"Three"}]}]}`)
	fence := persistence.ProviderDeliveryFence{OperationID: op.ID, Attempt: op.Attempt, RiverJobID: *op.RiverJobID, ExecutionEpoch: deliveryEpoch}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), Payload: acceptedA,
		Graph: persistence.ProviderGraph{}, Delivery: &fence,
	}); err != nil {
		t.Fatalf("accept initial provider response: %v", err)
	}
	before, err := fixture.repository.GetMatchingFallbackSearchReceipt(ctx, parentID, op.ID)
	if err != nil || before == nil {
		t.Fatalf("read accepted fallback receipt: receipt=%+v err=%v", before, err)
	}
	digestA := sha256.Sum256(before.Payload)
	if before.PayloadSHA256 != hex.EncodeToString(digestA[:]) || before.CacheGeneration != ticket.Generation || before.ExecutionEpoch != deliveryEpoch {
		t.Fatalf("receipt does not represent accepted delivery: %+v", before)
	}
	var receiptPayload map[string]json.RawMessage
	if err := json.Unmarshal(before.Payload, &receiptPayload); err != nil {
		t.Fatalf("decode receipt payload: %v", err)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(receiptPayload["recordings"], &rows); err != nil || len(rows) != 2 {
		t.Fatalf("accepted recording order was not retained: rows=%d err=%v", len(rows), err)
	}
	if string(rows[0]["id"]) != `"recording-a"` || string(rows[1]["id"]) != `"recording-b"` {
		t.Fatalf("recording order changed in receipt payload: %s", before.Payload)
	}
	var first map[string]json.RawMessage
	if err := json.Unmarshal(rows[0]["unknown"], &first); err != nil || string(first["z"]) != "1" || string(first["a"]) != "2" {
		t.Fatalf("unknown provider fields were not preserved: unknown=%s err=%v", rows[0]["unknown"], err)
	}
	if _, exists := rows[0]["score"]; exists {
		t.Fatalf("missing score became present in receipt: %s", rows[0]["score"])
	}
	if string(rows[1]["score"]) != "0" {
		t.Fatalf("explicit zero score was not preserved: %s", rows[1]["score"])
	}

	// A cache refresh by an unrelated operation replaces only the mutable cache.
	ticketB, err := cache.BeginRefresh(ctx, source.ID, key, source.Identity)
	if err != nil {
		t.Fatalf("reserve unrelated cache refresh: %v", err)
	}
	payloadB := json.RawMessage(`{"created":"2026-10-12T00:01:00Z","count":0,"offset":0,"recordings":[]}`)
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticketB, FetchedAt: time.Date(2026, 10, 12, 0, 1, 0, 0, time.UTC), Payload: payloadB,
	}); err != nil {
		t.Fatalf("apply unrelated cache refresh: %v", err)
	}
	after, err := fixture.repository.GetMatchingFallbackSearchReceipt(ctx, parentID, op.ID)
	if err != nil || after == nil || string(after.Payload) != string(before.Payload) || after.PayloadSHA256 != before.PayloadSHA256 || after.CacheGeneration != before.CacheGeneration {
		t.Fatalf("cache refresh changed immutable receipt: before=%+v after=%+v err=%v", before, after, err)
	}
	current, err := cache.GetCachedResponse(ctx, source.ID, key)
	if err != nil || current == nil || string(current.Payload) == string(before.Payload) {
		t.Fatalf("cache did not advance independently of receipt: response=%+v err=%v", current, err)
	}
}
