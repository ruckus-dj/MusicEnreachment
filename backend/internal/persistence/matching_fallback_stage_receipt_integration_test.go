//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

func TestAutomaticFallbackStageOneWorkerCapturesRegisteredReceiptPostgreSQL(t *testing.T) {
	testAutomaticFallbackCacheHitWorkerCapturesReceiptPostgreSQL(t, false, testFallbackStageOneReceipt)
}

type fallbackStageReceiptFixture struct {
	ctx                context.Context
	fixture            automaticTargetFenceFixture
	cache              *persistence.ProviderCacheRepository
	config             settings.MusicBrainzConfig
	source             persistence.ProviderSource
	plan               policy.TypedFallbackQueryPlan
	client             persistence.RiverInserter
	worker             *jobs.ProviderFetchWorker
	parentID           uuid.UUID
	stageZeroRequestID uuid.UUID
	stageZeroReceipt   *persistence.MatchingFallbackSearchReceipt
}

// testFallbackStageOneReceipt starts after the real stage-zero provider worker
// has captured its receipt. It emulates only the not-yet-shipped stage engine:
// the saved plan and stage-zero receipt remain production-created, while this
// test transaction appends the next-phase ledger row and registers its exact
// stage-one provider request. The receipt under test is produced by the real
// durable ProviderFetchWorker, never inserted by the fixture.
func testFallbackStageOneReceipt(t *testing.T, state fallbackStageReceiptFixture) {
	t.Helper()
	if len(state.plan.Stages) != 3 || state.plan.Stages[0].Kind != "album_track" || state.plan.Stages[1].Kind != "album" || state.plan.Stages[2].Kind != "track" {
		t.Fatalf("expected immutable album_track/album/track plan, got %+v", state.plan.Stages)
	}
	if state.stageZeroReceipt.RequestID != state.stageZeroRequestID || state.stageZeroReceipt.StageIndex != 0 {
		t.Fatalf("stage-zero receipt scope changed: %+v", state.stageZeroReceipt)
	}

	stage := state.plan.Stages[1]
	kind, prefix, ok := policy.ProviderKindForFallbackStage(stage.Kind)
	if !ok || kind != "provider_release_search" || prefix != "release-search" {
		t.Fatalf("stage one provider mapping = %q/%q, want provider_release_search/release-search", kind, prefix)
	}
	input, err := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}{1, stage.Query, stage.Offset, stage.Limit})
	if err != nil {
		t.Fatalf("encode saved stage-one descriptor: %v", err)
	}
	key := fmt.Sprintf("%s:%d:%d:%s", prefix, stage.Offset, stage.Limit, stage.Query)
	operationID := uuid.New()
	providerID, sourceID, identity := state.source.ProviderID, state.source.ID, state.config.Identity
	operation := &persistence.Operation{
		ID: operationID, Kind: kind, State: "queued", Stage: "queued", InputSnapshot: input,
		ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderConfigurationIdentity: &identity,
		ProviderEndpoint: state.source.Endpoint, ProviderCacheKey: &key, Attempt: 1,
	}
	if err := state.fixture.repository.CreateOperationAndEnqueue(state.ctx, operation, state.client,
		service.ProviderFetchOperationArgs{OperationID: operationID}, nil); err != nil {
		t.Fatalf("enqueue actual stage-one provider operation: %v", err)
	}
	registeredJob, err := state.fixture.repository.GetOperation(state.ctx, operationID)
	if err != nil || registeredJob.RiverJobID == nil {
		t.Fatalf("read registered stage-one worker delivery: operation=%+v err=%v", registeredJob, err)
	}

	// The cache row is only worker input. The durable worker claims this operation,
	// binds the accepted response to its delivery fence, and captures the receipt.
	accepted := json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":1,"offset":0,"releases":[{"id":"22222222-2222-4222-8222-222222222222","title":"Captured Album"}]}`)
	ticket, err := state.cache.BeginRefresh(state.ctx, state.source.ID, key, state.config.Identity)
	if err != nil {
		t.Fatalf("reserve stage-one cache response: %v", err)
	}
	if err := state.cache.ApplySuccessfulResponse(state.ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: accepted}); err != nil {
		t.Fatalf("seed stage-one cache response: %v", err)
	}

	canonicalInput, err := json.Marshal(map[string]any{"schema_version": 1, "query": stage.Query, "offset": stage.Offset, "limit": stage.Limit})
	if err != nil {
		t.Fatalf("canonicalize stage-one request: %v", err)
	}
	digest := sha256.Sum256(canonicalInput)
	phaseOutput, err := json.Marshal(map[string]any{"stage_index": 1, "request_id": operationID})
	if err != nil {
		t.Fatalf("encode stage-one handoff proof: %v", err)
	}
	if err := state.fixture.db.RunInTx(state.ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO matching_provider_dependency(parent_run_id,request_id,cache_key) VALUES (?,?,?)`, state.parentID, operationID, key).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_fallback_request_registration
			(run_id,stage_index,role,request_id,provider_source_id,configuration_identity,kind,cache_key,canonical_input,input_sha256)
			VALUES (?,1,'search',?,?,?,?,?,?::jsonb,?)`, state.parentID, operationID, state.source.ID, state.config.Identity, kind, key, string(canonicalInput), digest[:]).Exec(ctx); err != nil {
			return err
		}
		var revision int64
		if err := tx.NewRaw(`SELECT revision FROM matching_acquisition WHERE run_id=? FOR UPDATE`, state.parentID).Scan(ctx, &revision); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_acquisition_phase(run_id,revision,phase,input,output,response_provenance)
			VALUES (?,?,'fallback_search','{}'::jsonb,?::jsonb,'[]'::jsonb)`, state.parentID, revision+1, string(phaseOutput)).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`UPDATE matching_acquisition SET current_phase='fallback_search',revision=? WHERE run_id=?`, revision+1, state.parentID).Exec(ctx)
		return err
	}); err != nil {
		t.Fatalf("register exact stage-one test handoff: %v", err)
	}

	if err := state.worker.Work(state.ctx, &river.Job[service.ProviderFetchOperationArgs]{
		JobRow: &rivertype.JobRow{ID: *registeredJob.RiverJobID, Attempt: 1},
		Args:   service.ProviderFetchOperationArgs{OperationID: operationID},
	}); err != nil {
		t.Fatalf("run real stage-one provider fetch worker: %v", err)
	}
	stageOneReceipt, err := state.fixture.repository.GetMatchingFallbackSearchReceipt(state.ctx, state.parentID, operationID)
	if err != nil || stageOneReceipt == nil {
		t.Fatalf("read actual stage-one accepted receipt: receipt=%+v err=%v", stageOneReceipt, err)
	}
	completed, err := state.fixture.repository.GetOperation(state.ctx, operationID)
	if err != nil || completed.State != "succeeded" || completed.RiverJobID == nil ||
		stageOneReceipt.ProviderAttempt != completed.Attempt || stageOneReceipt.RiverJobID != *completed.RiverJobID ||
		stageOneReceipt.ExecutionEpoch != completed.ProviderExecutionEpoch {
		t.Fatalf("stage-one receipt is not bound to its completed worker delivery: operation=%+v receipt=%+v err=%v", completed, stageOneReceipt, err)
	}
	if stageOneReceipt.StageIndex != 1 || stageOneReceipt.RequestID != operationID || stageOneReceipt.RequestID == state.stageZeroRequestID ||
		stageOneReceipt.ProviderSourceID != state.source.ID || stageOneReceipt.ConfigurationIdentity != state.config.Identity ||
		stageOneReceipt.CacheKey != key || !jsonEqual(stageOneReceipt.RequestInput, canonicalInput) {
		t.Fatalf("stage-one receipt does not prove its registered request scope: %+v", stageOneReceipt)
	}
	acquisition, exists, err := state.fixture.repository.GetMatchingAcquisition(state.ctx, state.parentID)
	var handoff struct {
		StageIndex int       `json:"stage_index"`
		RequestID  uuid.UUID `json:"request_id"`
	}
	if err != nil || !exists || acquisition.Phase != persistence.MatchingAcquisitionFallbackSearch ||
		json.Unmarshal(acquisition.Output, &handoff) != nil || handoff.StageIndex != 1 || handoff.RequestID != operationID {
		t.Fatalf("stage-one receipt does not correspond to the simulated persisted phase handoff: acquisition=%+v exists=%v err=%v", acquisition, exists, err)
	}
	var dependencyState string
	if err := state.fixture.db.NewRaw(`SELECT terminal_outcome FROM matching_provider_dependency WHERE parent_run_id=? AND request_id=?`, state.parentID, operationID).Scan(state.ctx, &dependencyState); err != nil || dependencyState != "succeeded" {
		t.Fatalf("stage-one provider dependency was not settled by worker: state=%q err=%v", dependencyState, err)
	}
	if !jsonEqual(stageOneReceipt.Payload, accepted) {
		t.Fatalf("stage-one receipt differs from accepted worker response: payload=%s expected=%s", stageOneReceipt.Payload, accepted)
	}
	unchangedStageZero, err := state.fixture.repository.GetMatchingFallbackSearchReceipt(state.ctx, state.parentID, state.stageZeroRequestID)
	if err != nil || unchangedStageZero == nil || unchangedStageZero.StageIndex != 0 || unchangedStageZero.RequestID != state.stageZeroReceipt.RequestID ||
		!jsonEqual(unchangedStageZero.Payload, state.stageZeroReceipt.Payload) {
		t.Fatalf("stage-one receipt replaced or re-scoped initial stage-zero receipt: receipt=%+v err=%v", unchangedStageZero, err)
	}
}

func jsonEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil && reflect.DeepEqual(leftValue, rightValue)
}
