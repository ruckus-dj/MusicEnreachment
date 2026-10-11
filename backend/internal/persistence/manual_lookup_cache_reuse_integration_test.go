//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestManualLookupCacheReuseCapturesExactResponseBeforeSettlementPostgres(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	selected := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	cacheKey := "release-lookup:" + selected.String()
	cache := persistence.NewProviderCacheRepository(db)
	payloadA := json.RawMessage(`{"release":"accepted-a"}`)
	ticket, err := cache.BeginRefresh(ctx, fixture.source, cacheKey, "incoming-match-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payloadA, Graph: fixture.graph,
	}); err != nil {
		t.Fatalf("prepopulate complete release lookup cache: %v", err)
	}

	var source persistence.ProviderSource
	if err := db.NewRaw(`SELECT id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &source); err != nil {
		t.Fatal(err)
	}
	state, err := persistence.NewIncomingGroupingRepository(db).State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var group persistence.IncomingGroupingGroup
	groups, err := persistence.NewIncomingGroupingRepository(db).List(ctx)
	if err != nil || len(groups) != 1 {
		t.Fatalf("read matching group: groups=%+v err=%v", groups, err)
	}
	group = groups[0]
	requestID, childID := uuid.New(), uuid.New()
	identity := source.ConfigurationIdentity
	child := persistence.MatchingProviderRequest{Operation: &persistence.Operation{
		ID: childID, Kind: "provider_release_lookup", State: "queued", Stage: "queued",
		InputSnapshot:    json.RawMessage(`{"schema_version":1,"mbid":"` + selected.String() + `"}`),
		ProviderSourceID: &source.ID, ProviderID: &source.ProviderID, ProviderEndpoint: source.Endpoint,
		ProviderConfigurationIdentity: &identity, ProviderCacheKey: &cacheKey,
	}, Args: service.ProviderFetchOperationArgs{OperationID: childID}}
	plan := persistence.ManualMatchingRunPlan{
		GroupingEpoch: state.Revision, PolicyVersion: service.MatchingPolicyVersion,
		Plan:         json.RawMessage(`{"schema_version":1,"mode":"manual_review"}`),
		GroupContext: json.RawMessage(`{"group_id":"` + group.ID.String() + `","revision":"` + group.Revision + `"}`),
		SourceContext: json.RawMessage(fmt.Sprintf(`{"provider_id":%q,"provider_source_id":%q,"endpoint":%q,"configuration_identity":%q}`,
			source.ProviderID, source.ID, source.Endpoint, source.ConfigurationIdentity)),
	}
	repository := persistence.NewSetupManagerRepository(db)
	client := openScanEnqueueRiver(t, db)
	parent, err := repository.AdmitManualReleaseMatchingRun(ctx, client, requestID, group.ID, group.Revision, nil, selected, child, plan)
	if err != nil {
		t.Fatalf("admit manual lookup: %v", err)
	}
	operation, err := repository.GetOperation(ctx, childID)
	if err != nil || operation.RiverJobID == nil {
		t.Fatalf("read admitted lookup operation: operation=%+v err=%v", operation, err)
	}
	epoch, claimed, err := repository.ClaimProviderOperationDelivery(ctx, childID, operation.Attempt, *operation.RiverJobID)
	if err != nil || !claimed {
		t.Fatalf("claim lookup delivery: claimed=%t err=%v", claimed, err)
	}
	started, err := repository.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, childID, operation.Attempt,
		*operation.RiverJobID, epoch, client, func(operation *persistence.Operation) error {
			operation.State, operation.Stage = "running", "fetching_provider"
			return nil
		})
	if err != nil || !started {
		t.Fatalf("start lookup delivery: started=%t err=%v", started, err)
	}
	response, err := cache.GetCurrentCachedResponse(ctx, source.ID, cacheKey, identity)
	if err != nil || response == nil {
		t.Fatalf("read prepopulated accepted response: response=%+v err=%v", response, err)
	}
	fence := persistence.ProviderDeliveryFence{OperationID: childID, Attempt: operation.Attempt, RiverJobID: *operation.RiverJobID, ExecutionEpoch: epoch}
	captured, err := cache.CaptureSuccessfulManualLookupReuse(ctx, fence, response)
	if err != nil || !captured {
		t.Fatalf("capture exact manual lookup response: captured=%t err=%v", captured, err)
	}

	// A later accepted cache replacement must not rewrite the immutable A receipt.
	payloadB := json.RawMessage(`{"release":"replacement-b"}`)
	replacement, err := cache.BeginRefresh(ctx, source.ID, cacheKey, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: replacement, FetchedAt: time.Now().UTC(), Payload: payloadB, Graph: fixture.graph,
	}); err != nil {
		t.Fatalf("publish replacement cache response: %v", err)
	}
	settled, err := repository.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, childID, operation.Attempt,
		*operation.RiverJobID, epoch, client, func(operation *persistence.Operation) error {
			operation.State, operation.Stage = "succeeded", "provider_cache_reused"
			finished := time.Now().UTC()
			operation.FinishedAt = &finished
			return nil
		})
	if err != nil || !settled {
		t.Fatalf("settle cached lookup after replacement: settled=%t err=%v", settled, err)
	}
	var receiptDigest []byte
	if err := db.NewRaw(`SELECT payload_sha256 FROM manual_matching_lookup_seal WHERE request_id=? AND sealed_at IS NOT NULL`, requestID).Scan(ctx, &receiptDigest); err != nil {
		t.Fatal(err)
	}
	digestA := sha256.Sum256(response.Payload)
	digestB := sha256.Sum256(payloadB)
	if string(receiptDigest) != string(digestA[:]) || string(receiptDigest) == string(digestB[:]) {
		t.Fatalf("sealed receipt digest=%x, want cached response A %x and not B %x", receiptDigest, digestA, digestB)
	}
	snapshot, exists, err := repository.GetManualReleaseMatchingRun(ctx, requestID)
	if err != nil || !exists || snapshot.Pending || snapshot.RunID != parent.ID || snapshot.AcceptedPayloadSHA256 == "" {
		t.Fatalf("manual lookup snapshot=%+v exists=%t err=%v", snapshot, exists, err)
	}
}
