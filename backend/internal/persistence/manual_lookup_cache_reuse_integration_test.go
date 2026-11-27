//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

type failMatchingWakeInserter struct {
	delegate persistence.RiverInserter
	fail     bool
}

func (inserter *failMatchingWakeInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if inserter.fail && args.Kind() == "matching_evaluation_v1" {
		return nil, errors.New("injected matching wake failure")
	}
	return inserter.delegate.InsertTx(ctx, tx, args, options)
}

func TestManualLookupCacheReuseCapturesExactResponseBeforeSettlementPostgres(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigratedWithRiver(t)
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
		GroupContext: manualGroupContext(t, group),
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

	// Dependency wake insertion and the manual lookup seal share the provider
	// completion transaction. A failed wake must roll both back so a retry
	// settles the completion once and registers exactly one wake.
	settlementClient := &failMatchingWakeInserter{delegate: client, fail: true}
	settled, err := repository.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, childID, operation.Attempt,
		*operation.RiverJobID, epoch, settlementClient, func(operation *persistence.Operation) error {
			operation.State, operation.Stage = "succeeded", "provider_cache_reused"
			finished := time.Now().UTC()
			operation.FinishedAt = &finished
			return nil
		})
	if err == nil || settled {
		t.Fatalf("settlement with a failing matching wake = settled %t, err %v; want rollback", settled, err)
	}
	var rolledBack struct {
		State        string  `bun:"state"`
		Stage        string  `bun:"stage"`
		Sealed       bool    `bun:"sealed"`
		Outcome      *string `bun:"terminal_outcome"`
		WakeDelivery int     `bun:"wake_delivery"`
	}
	if err := db.NewRaw(`SELECT operation.state,operation.stage,
			seal.sealed_at IS NOT NULL AS sealed,
			dependency.terminal_outcome,
			(SELECT count(*) FROM matching_delivery delivery WHERE delivery.run_id=? AND delivery.river_job_id<>?) AS wake_delivery
		FROM operation
		JOIN manual_matching_admission admission ON admission.lookup_operation_id=operation.id
		JOIN manual_matching_lookup_seal seal ON seal.request_id=admission.request_id
		JOIN matching_provider_dependency dependency ON dependency.request_id=operation.id
		WHERE operation.id=?`, parent.ID, *parent.RiverJobID, childID).Scan(ctx, &rolledBack); err != nil {
		t.Fatalf("read rolled-back manual completion: %v", err)
	}
	if rolledBack.State != "running" || rolledBack.Stage != "fetching_provider" || rolledBack.Sealed || rolledBack.Outcome != nil || rolledBack.WakeDelivery != 0 {
		t.Fatalf("failed wake settlement did not roll back: %+v", rolledBack)
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
	settlementClient.fail = false
	settled, err = repository.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, childID, operation.Attempt,
		*operation.RiverJobID, epoch, settlementClient, func(operation *persistence.Operation) error {
			operation.State, operation.Stage = "succeeded", "provider_cache_reused"
			finished := time.Now().UTC()
			operation.FinishedAt = &finished
			return nil
		})
	if err != nil || !settled {
		t.Fatalf("retry cached lookup settlement after replacement: settled=%t err=%v", settled, err)
	}
	var settledWake struct {
		State        string  `bun:"state"`
		Sealed       bool    `bun:"sealed"`
		Outcome      *string `bun:"terminal_outcome"`
		WakeDelivery int     `bun:"wake_delivery"`
	}
	if err := db.NewRaw(`SELECT operation.state,
			seal.sealed_at IS NOT NULL AS sealed,
			dependency.terminal_outcome,
			(SELECT count(*) FROM matching_delivery delivery WHERE delivery.run_id=? AND delivery.river_job_id<>?) AS wake_delivery
		FROM operation
		JOIN manual_matching_admission admission ON admission.lookup_operation_id=operation.id
		JOIN manual_matching_lookup_seal seal ON seal.request_id=admission.request_id
		JOIN matching_provider_dependency dependency ON dependency.request_id=operation.id
		WHERE operation.id=?`, parent.ID, *parent.RiverJobID, childID).Scan(ctx, &settledWake); err != nil {
		t.Fatalf("read retried manual completion: %v", err)
	}
	if settledWake.State != "succeeded" || !settledWake.Sealed || settledWake.Outcome == nil || *settledWake.Outcome != "succeeded" || settledWake.WakeDelivery != 1 {
		t.Fatalf("retried settlement = %+v; want one succeeded wake and sealed receipt", settledWake)
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

func manualGroupContext(t *testing.T, group persistence.IncomingGroupingGroup) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(struct {
		GroupID  uuid.UUID   `json:"group_id"`
		Revision string      `json:"revision"`
		Members  []uuid.UUID `json:"members"`
	}{group.ID, group.Revision, group.Members})
	if err != nil {
		t.Fatalf("encode manual matching group context: %v", err)
	}
	return encoded
}
