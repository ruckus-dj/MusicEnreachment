//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestMatchingTargetFenceReadersAndDeliveryIgnoreUnrelatedGroupingEpochPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAutomaticMatchingTargetFenceFixture(t)
	before, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, fixture.runID)
	if err != nil || !exists || before.Intent.FenceVersion != 2 {
		t.Fatalf("read version-2 acquisition: exists=%v err=%v", exists, err)
	}
	beforeCapture, beforeDigest, exists, err := fixture.repository.GetMatchingAcquisitionMetadataCapture(fixture.ctx, fixture.runID, "admitted")
	if err != nil || !exists || len(beforeCapture) == 0 || beforeDigest == "" {
		t.Fatalf("read original metadata capture: members=%d digest=%q exists=%v err=%v", len(beforeCapture), beforeDigest, exists, err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE incoming_grouping_state SET revision=revision+1,needs_refresh=false WHERE singleton=true`).Exec(fixture.ctx); err != nil {
		t.Fatalf("advance unrelated grouping epoch: %v", err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, fixture.jobID).Exec(fixture.ctx); err != nil {
		t.Fatalf("start matching delivery job: %v", err)
	}
	_, outcome, err := fixture.repository.ClaimMatchingDelivery(fixture.ctx, fixture.runID, 1, fixture.jobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("unrelated grouping epoch fenced version-2 claim: outcome=%v err=%v", outcome, err)
	}
	// A reader reports the immutable admission snapshot, never current source data.
	after, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, fixture.runID)
	if err != nil || !exists || after.Intent.CaptureDigest != before.Intent.CaptureDigest || after.Intent.TargetCaptureDigest != before.Intent.TargetCaptureDigest {
		t.Fatalf("read acquisition after unrelated epoch: exists=%v err=%v", exists, err)
	}
	afterCapture, afterDigest, exists, err := fixture.repository.GetMatchingAcquisitionMetadataCapture(fixture.ctx, fixture.runID, "admitted")
	if err != nil || !exists || afterDigest != beforeDigest || !equalJSONForTest(t, afterCapture, beforeCapture) {
		t.Fatalf("metadata reader changed original capture: exists=%v digest=%q err=%v", exists, afterDigest, err)
	}
}

func TestMatchingTargetFenceDeliveryRejectsChangedCapturedSourcePostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAutomaticMatchingTargetFenceFixture(t)
	original, originalDigest, exists, err := fixture.repository.GetMatchingAcquisitionMetadataCapture(fixture.ctx, fixture.runID, "admitted")
	if err != nil || !exists || len(original) == 0 {
		t.Fatalf("read original metadata capture: exists=%v err=%v", exists, err)
	}
	var groupID uuid.UUID
	if err := fixture.db.NewRaw(`SELECT group_id FROM matching_run_target WHERE run_id=?`, fixture.runID).Scan(fixture.ctx, &groupID); err != nil {
		t.Fatalf("read target group: %v", err)
	}
	result, err := fixture.db.NewRaw(`UPDATE incoming_group_member SET ready=NOT ready
		WHERE group_id=? AND variant_id=(SELECT variant_id FROM incoming_group_member WHERE group_id=? ORDER BY variant_id LIMIT 1)`, groupID, groupID).Exec(fixture.ctx)
	if err != nil {
		t.Fatalf("change a captured target member: %v", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		t.Fatalf("change captured target member: rows=%d err=%v", changed, err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, fixture.jobID).Exec(fixture.ctx); err != nil {
		t.Fatalf("start stale matching delivery job: %v", err)
	}
	_, outcome, err := fixture.repository.ClaimMatchingDelivery(fixture.ctx, fixture.runID, 1, fixture.jobID, 1)
	if !errors.Is(err, persistence.ErrIncomingMatchConflict) || outcome != persistence.MatchingDeliveryNotClaimed {
		t.Fatalf("claim accepted changed captured target: outcome=%v err=%v", outcome, err)
	}
	// Reads still return the saved values so callers can diagnose the stale run.
	readBack, digest, exists, err := fixture.repository.GetMatchingAcquisitionMetadataCapture(fixture.ctx, fixture.runID, "admitted")
	if err != nil || !exists || digest != originalDigest || !equalJSONForTest(t, readBack, original) {
		t.Fatalf("changed source replaced immutable capture: exists=%v digest=%q err=%v", exists, digest, err)
	}
}

type automaticTargetFenceFixture struct {
	ctx        context.Context
	db         *bun.DB
	repository *persistence.SetupManagerRepository
	runID      uuid.UUID
	jobID      int64
}

func newAutomaticMatchingTargetFenceFixture(t *testing.T) automaticTargetFenceFixture {
	t.Helper()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	incoming := newIncomingMatchFixture(t, ctx, db)
	setup := persistence.NewSetupManagerRepository(db)
	inventory := persistence.NewSourceInventoryRepository(db)
	client := openScanEnqueueRiver(t, db)
	var event persistence.IncomingMatchingReconcileEvent
	if err := db.NewSelect().Model(&event).Where("reason = ?", "analysis_success").Order("created_at ASC").Order("id ASC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read source-analysis reconciliation event: %v", err)
	}
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch real reconciliation event: %v", err)
	}
	eventPtr, err := inventory.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil || eventPtr == nil || eventPtr.RegisteredJobID == nil {
		t.Fatalf("read dispatched reconciliation event: event=%+v err=%v", eventPtr, err)
	}
	event = *eventPtr
	var source struct {
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := db.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, incoming.source).Scan(ctx, &source); err != nil {
		t.Fatalf("read provider source fence: %v", err)
	}
	epoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *event.RegisteredJobID, 1)
	if err != nil || !claimed {
		t.Fatalf("claim source-analysis reconciliation event: claimed=%v err=%v", claimed, err)
	}
	query := json.RawMessage(`{"schema_version":1,"query":"Fixture Artist","offset":0,"limit":1}`)
	providerID, sourceID := source.ProviderID, incoming.source
	identity, endpoint, key := source.Identity, source.Endpoint, "artist-search:0:1:Fixture Artist"
	parentID, providerOperationID := uuid.New(), uuid.New()
	parent := &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	provider := &persistence.Operation{
		ID: providerOperationID, Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
		ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderConfigurationIdentity: matchingCoordinatorStringPtr(identity),
		ProviderEndpoint: endpoint, ProviderCacheKey: &key, Attempt: 1,
	}
	admitted, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Provider: persistence.MatchingProviderRequest{Operation: provider, Args: service.ProviderFetchOperationArgs{OperationID: providerOperationID}},
		GroupID:  incoming.group.ID, ExpectedGroupRevision: incoming.group.Revision, GroupingEpoch: incoming.groupingEpoch,
		PolicyVersion: service.MatchingPolicyVersion, ArtistQueryPlanVersion: 1, QueryDescriptor: query,
		EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: epoch,
	})
	if err != nil || admitted.AlreadyAdmitted || admitted.Operation == nil {
		t.Fatalf("admit version-2 automatic run: result=%+v err=%v", admitted, err)
	}
	var jobID int64
	if err := db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, parentID).Scan(ctx, &jobID); err != nil {
		t.Fatalf("read registered matching delivery job: %v", err)
	}
	return automaticTargetFenceFixture{ctx: ctx, db: db, repository: setup, runID: parentID, jobID: jobID}
}

func equalJSONForTest(t *testing.T, left, right any) bool {
	t.Helper()
	leftBytes, err := json.Marshal(left)
	if err != nil {
		t.Fatalf("marshal left JSON value: %v", err)
	}
	rightBytes, err := json.Marshal(right)
	if err != nil {
		t.Fatalf("marshal right JSON value: %v", err)
	}
	return string(leftBytes) == string(rightBytes)
}
