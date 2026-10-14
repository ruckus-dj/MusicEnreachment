//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
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

func TestAutomaticFallbackMatchingAdmissionPersistsVersionedPlanAndReceiptPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAutomaticMatchingTargetFenceFixture(t)
	var target struct {
		GroupID  uuid.UUID `bun:"group_id"`
		Revision string    `bun:"captured_group_revision"`
	}
	if err := fixture.db.NewRaw(`SELECT group_id,captured_group_revision FROM matching_run_target WHERE run_id=?`, fixture.runID).Scan(fixture.ctx, &target); err != nil {
		t.Fatalf("read target: %v", err)
	}
	var event persistence.IncomingMatchingReconcileEvent
	if err := fixture.db.NewSelect().Model(&event).Where("state = ? AND delivery_claimed", "dispatched").Order("created_at DESC").Limit(1).Scan(fixture.ctx); err != nil {
		t.Fatalf("read claimed reconciliation event: %v", err)
	}
	var source struct {
		ID         uuid.UUID `bun:"provider_source_id"`
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := fixture.db.NewRaw(`SELECT id AS provider_source_id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=(SELECT provider_source_id FROM matching_run WHERE id=?)`, fixture.runID).Scan(fixture.ctx, &source); err != nil {
		t.Fatalf("read provider source: %v", err)
	}
	var epoch int64
	if err := fixture.db.NewRaw(`SELECT delivery_epoch FROM incoming_matching_reconcile_event WHERE id=?`, event.ID).Scan(fixture.ctx, &epoch); err != nil {
		t.Fatalf("read event epoch: %v", err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE media_metadata_result SET observed_tags='{"ALBUM":["Captured Album"],"TITLE":["Captured Track"]}'::jsonb`).Exec(fixture.ctx); err != nil {
		t.Fatalf("set album-and-title captured metadata: %v", err)
	}
	tags := []map[string][]string{{"ALBUM": {"Captured Album"}, "TITLE": {"Captured Track"}}, {"ALBUM": {"Captured Album"}, "TITLE": {"Captured Track"}}}
	planned, artistBearing := policy.PlanFallbackQueries(tags)
	if artistBearing {
		t.Fatal("album-only fixture unexpectedly has artist metadata")
	}
	plan := policy.TypedFallbackQueryPlan{SchemaVersion: planned.SchemaVersion, Stages: planned.Stages}
	stage := plan.Stages[0]
	providerKind, cachePrefix, ok := policy.ProviderKindForFallbackStage(stage.Kind)
	if !ok {
		t.Fatalf("fallback stage %q has no provider mapping", stage.Kind)
	}
	makeAdmission := func() persistence.AutomaticFallbackMatchingAdmission {
		parentID, providerOperationID := uuid.New(), uuid.New()
		key, identity := cachePrefix+":0:10:"+stage.Query, source.Identity
		providerID, sourceID := source.ProviderID, source.ID
		input, _ := json.Marshal(struct {
			SchemaVersion int    `json:"schema_version"`
			Query         string `json:"query"`
			Offset        int    `json:"offset"`
			Limit         int    `json:"limit"`
		}{1, stage.Query, 0, 10})
		provider := &persistence.Operation{ID: providerOperationID, Kind: providerKind, State: "queued", Stage: "queued", InputSnapshot: input, ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderConfigurationIdentity: &identity, ProviderEndpoint: source.Endpoint, ProviderCacheKey: &key, Attempt: 1}
		admissionPlan := plan
		admissionPlan.Stages = append([]policy.TypedFallbackQueryStage(nil), plan.Stages...)
		return persistence.AutomaticFallbackMatchingAdmission{Parent: &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID}, Provider: persistence.MatchingProviderRequest{Operation: provider, Args: service.ProviderFetchOperationArgs{OperationID: providerOperationID}}, Source: persistence.AutomaticFallbackProviderSource{ID: source.ID, ProviderID: source.ProviderID, Endpoint: source.Endpoint, ConfigurationIdentity: source.Identity}, GroupID: target.GroupID, ExpectedGroupRevision: target.Revision, PolicyVersion: service.MatchingPolicyVersion, Plan: admissionPlan, EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: event.HighestDeliveryAttempt, EventDeliveryEpoch: epoch}
	}
	client := openScanEnqueueRiver(t, fixture.db)
	for name, mutate := range map[string]func(*policy.TypedFallbackQueryPlan){
		"forged query": func(plan *policy.TypedFallbackQueryPlan) { plan.Stages[0].Query = `release:"forged"` },
		"wrong subset": func(plan *policy.TypedFallbackQueryPlan) { plan.Stages = plan.Stages[:2] },
	} {
		invalid := makeAdmission()
		mutate(&invalid.Plan)
		if _, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(fixture.ctx, client, invalid); err == nil {
			t.Errorf("accepted fallback plan with %s", name)
		}
	}
	falseEmpty := makeAdmission()
	falseEmpty.Plan.Stages = []policy.TypedFallbackQueryStage{}
	falseEmpty.Provider = persistence.MatchingProviderRequest{}
	falseEmpty.Parent.Stage = "waiting_policy"
	if _, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(fixture.ctx, client, falseEmpty); err == nil {
		t.Error("accepted empty fallback plan despite captured album/title values")
	}
	for _, artistTag := range []string{"ARTIST", "ALBUMARTIST"} {
		if _, err := fixture.db.NewRaw(`UPDATE media_metadata_result SET observed_tags=jsonb_build_object(?,jsonb_build_array('Captured Artist'))`, artistTag).Exec(fixture.ctx); err != nil {
			t.Fatalf("set %s-only captured metadata: %v", artistTag, err)
		}
		parentID := uuid.New()
		artistAdmission := persistence.AutomaticFallbackMatchingAdmission{
			Parent:     &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_policy", Attempt: 1},
			ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
			Source:     persistence.AutomaticFallbackProviderSource{ID: source.ID, ProviderID: source.ProviderID, Endpoint: source.Endpoint, ConfigurationIdentity: source.Identity},
			GroupID:    target.GroupID, ExpectedGroupRevision: target.Revision, PolicyVersion: service.MatchingPolicyVersion,
			Plan:    policy.TypedFallbackQueryPlan{SchemaVersion: 1, Stages: []policy.TypedFallbackQueryStage{}},
			EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: event.HighestDeliveryAttempt, EventDeliveryEpoch: epoch,
		}
		if _, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(fixture.ctx, client, artistAdmission); err == nil {
			t.Errorf("accepted artist-bearing fallback source with %s", artistTag)
		}
	}
	if _, err := fixture.db.NewRaw(`UPDATE media_metadata_result SET observed_tags='{"ALBUM":["Captured Album"],"TITLE":["Captured Track"]}'::jsonb`).Exec(fixture.ctx); err != nil {
		t.Fatalf("restore album-and-title captured metadata: %v", err)
	}
	acceptedAdmission := makeAdmission()
	first, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(fixture.ctx, client, acceptedAdmission)
	if err != nil || first.AlreadyAdmitted || first.Operation == nil {
		t.Fatalf("admit fallback plan: result=%+v err=%v", first, err)
	}
	acquisition, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, first.OriginalRunID)
	if err != nil || !exists || acquisition.Intent.SchemaVersion != 3 || acquisition.Intent.ArtistQueryPlanVersion != 0 || len(acquisition.Intent.FallbackQueryPlan) == 0 {
		t.Fatalf("read fallback acquisition intent: exists=%v intent=%+v err=%v", exists, acquisition.Intent, err)
	}
	if acquisition.Phase != "fallback_search" || string(acquisition.Output) == "" {
		t.Fatalf("fallback initial stage descriptor was not captured: phase=%q output=%s", acquisition.Phase, acquisition.Output)
	}
	var initialOutput struct {
		State      string                         `json:"state"`
		StageIndex int                            `json:"stage_index"`
		Descriptor policy.TypedFallbackQueryStage `json:"descriptor"`
	}
	if err := json.Unmarshal(acquisition.Output, &initialOutput); err != nil || initialOutput.State != "fallback_search" || initialOutput.StageIndex != 0 || initialOutput.Descriptor != plan.Stages[0] {
		t.Fatalf("fallback initial stage descriptor differs from plan: output=%+v err=%v", initialOutput, err)
	}
	if _, _, exists, err := fixture.repository.GetMatchingAcquisitionMetadataCapture(fixture.ctx, first.OriginalRunID, "fallback_search"); err != nil || !exists {
		t.Fatalf("read immutable fallback metadata capture: exists=%v err=%v", exists, err)
	}
	// Capture through the real cache-reuse acceptance hook, then prove the
	// receipt remains the accepted authority after the mutable cache is replaced.
	cache := persistence.NewProviderCacheRepository(fixture.db)
	acceptedPayload := json.RawMessage(`{"count":1,"results":[{"id":"accepted","score":0}],"unknown":{"preserved":[3,1]}}`)
	ticket, err := cache.BeginRefresh(fixture.ctx, source.ID, *acceptedAdmission.Provider.Operation.ProviderCacheKey, source.Identity)
	if err != nil {
		t.Fatalf("reserve accepted cache payload: %v", err)
	}
	if err := cache.ApplySuccessfulResponse(fixture.ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: acceptedPayload}); err != nil {
		t.Fatalf("apply accepted cache payload: %v", err)
	}
	child, err := fixture.repository.GetOperation(fixture.ctx, acceptedAdmission.Provider.Operation.ID)
	if err != nil || child.RiverJobID == nil {
		t.Fatalf("read initial provider operation: operation=%+v err=%v", child, err)
	}
	epoch, claimed, err := fixture.repository.ClaimProviderOperationDelivery(fixture.ctx, child.ID, child.Attempt, *child.RiverJobID)
	if err != nil || !claimed {
		t.Fatalf("claim initial provider delivery: claimed=%v err=%v", claimed, err)
	}
	operations := service.NewOperationsWithRiver(fixture.repository, client)
	if err := operations.RunningForProviderDelivery(fixture.ctx, child, epoch, "searching"); err != nil {
		t.Fatalf("mark initial provider delivery running: %v", err)
	}
	deliveryFence := persistence.ProviderDeliveryFence{OperationID: child.ID, Attempt: child.Attempt, RiverJobID: *child.RiverJobID, ExecutionEpoch: epoch}
	cached, err := cache.GetCachedResponse(fixture.ctx, source.ID, *acceptedAdmission.Provider.Operation.ProviderCacheKey)
	if err != nil || cached == nil {
		t.Fatalf("read accepted response for cache reuse: response=%+v err=%v", cached, err)
	}
	if err := cache.CaptureManualPickerSearchReuse(fixture.ctx, deliveryFence, cached); err != nil {
		t.Fatalf("capture fallback cache reuse: %v", err)
	}
	if err := cache.CaptureManualPickerSearchReuse(fixture.ctx, deliveryFence, cached); err != nil {
		t.Fatalf("idempotently recapture fallback cache reuse: %v", err)
	}
	if err := operations.SucceedForProviderDelivery(fixture.ctx, child, epoch, "succeeded"); err != nil {
		t.Fatalf("settle provider delivery after receipt capture: %v", err)
	}
	searchReceipt, err := fixture.repository.GetMatchingFallbackSearchReceipt(fixture.ctx, first.OriginalRunID, child.ID)
	if err != nil || searchReceipt == nil || string(searchReceipt.Payload) != string(acceptedPayload) {
		t.Fatalf("read immutable accepted fallback receipt: receipt=%+v err=%v", searchReceipt, err)
	}
	acceptedDigest := sha256.Sum256(searchReceipt.Payload)
	if searchReceipt.PayloadSHA256 != fmt.Sprintf("%x", acceptedDigest[:]) || searchReceipt.StageIndex != 0 || searchReceipt.CacheGeneration != ticket.Generation {
		t.Fatalf("fallback receipt digest or provenance is invalid: %+v", searchReceipt)
	}
	replacement, err := cache.BeginRefresh(fixture.ctx, source.ID, *acceptedAdmission.Provider.Operation.ProviderCacheKey, source.Identity)
	if err != nil {
		t.Fatalf("reserve replacement cache payload: %v", err)
	}
	if err := cache.ApplySuccessfulResponse(fixture.ctx, persistence.ProviderSuccessfulResponse{Ticket: replacement, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"count":0,"results":[],"different":true}`)}); err != nil {
		t.Fatalf("replace mutable provider cache: %v", err)
	}
	searchReceipt, err = fixture.repository.GetMatchingFallbackSearchReceipt(fixture.ctx, first.OriginalRunID, child.ID)
	if err != nil || searchReceipt == nil || string(searchReceipt.Payload) != string(acceptedPayload) {
		t.Fatalf("cache replacement changed accepted fallback authority: receipt=%+v err=%v", searchReceipt, err)
	}
	fence, exists, err := fixture.repository.GetMatchingRunFence(fixture.ctx, first.OriginalRunID)
	if err != nil || !exists || fence.Version != 2 || fence.GroupID != target.GroupID {
		t.Fatalf("read fallback target fence: exists=%v fence=%+v err=%v", exists, fence, err)
	}
	var before struct {
		HighWater int64 `bun:"high_water"`
		Jobs      int   `bun:"jobs"`
	}
	if err := fixture.db.NewRaw(`SELECT generation.high_water,(SELECT count(*) FROM operation WHERE kind='matching_evaluation_v1') AS jobs FROM incoming_matching_generation generation WHERE group_id=?`, target.GroupID).Scan(fixture.ctx, &before); err != nil {
		t.Fatalf("read before duplicate: %v", err)
	}
	duplicate, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(fixture.ctx, client, makeAdmission())
	if err != nil || !duplicate.AlreadyAdmitted || duplicate.OriginalRunID != first.OriginalRunID {
		t.Fatalf("duplicate fallback receipt: result=%+v err=%v", duplicate, err)
	}
	var after struct {
		HighWater int64 `bun:"high_water"`
		Jobs      int   `bun:"jobs"`
	}
	if err := fixture.db.NewRaw(`SELECT generation.high_water,(SELECT count(*) FROM operation WHERE kind='matching_evaluation_v1') AS jobs FROM incoming_matching_generation generation WHERE group_id=?`, target.GroupID).Scan(fixture.ctx, &after); err != nil {
		t.Fatalf("read after duplicate: %v", err)
	}
	if before.HighWater != after.HighWater || before.Jobs != after.Jobs {
		t.Fatalf("duplicate fallback admission created durable work: before=%+v after=%+v", before, after)
	}
	if _, err := fixture.db.NewRaw(`UPDATE media_metadata_result SET observed_tags='{}'::jsonb`).Exec(fixture.ctx); err != nil {
		t.Fatalf("set truly empty captured metadata: %v", err)
	}
	wrongSourceID := uuid.New()
	wrongSource := persistence.AutomaticFallbackMatchingAdmission{
		Parent:     &persistence.Operation{ID: wrongSourceID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_policy", Attempt: 1},
		ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: wrongSourceID},
		Source:     persistence.AutomaticFallbackProviderSource{ID: source.ID, ProviderID: uuid.New(), Endpoint: source.Endpoint, ConfigurationIdentity: source.Identity},
		GroupID:    target.GroupID, ExpectedGroupRevision: target.Revision, PolicyVersion: service.MatchingPolicyVersion,
		Plan:    policy.TypedFallbackQueryPlan{SchemaVersion: 1, Stages: []policy.TypedFallbackQueryStage{}},
		EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: event.HighestDeliveryAttempt, EventDeliveryEpoch: epoch,
	}
	if _, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(fixture.ctx, client, wrongSource); err == nil {
		t.Error("accepted an empty-plan source descriptor with a mismatched provider ID")
	}
	manualID := uuid.New()
	manual, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(fixture.ctx, client, persistence.AutomaticFallbackMatchingAdmission{
		Parent:     &persistence.Operation{ID: manualID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_policy", InputSnapshot: json.RawMessage(`{}`), Attempt: 1},
		ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: manualID}, GroupID: target.GroupID,
		ExpectedGroupRevision: target.Revision, PolicyVersion: service.MatchingPolicyVersion,
		Source:  persistence.AutomaticFallbackProviderSource{ID: source.ID, ProviderID: source.ProviderID, Endpoint: source.Endpoint, ConfigurationIdentity: source.Identity},
		Plan:    policy.TypedFallbackQueryPlan{SchemaVersion: 1, Stages: []policy.TypedFallbackQueryStage{}},
		EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: event.HighestDeliveryAttempt, EventDeliveryEpoch: epoch,
	})
	if err != nil || manual.Operation == nil || manual.OriginalRunID != manualID {
		t.Fatalf("admit empty manual-wait plan: result=%+v err=%v", manual, err)
	}
	var emptyRunSource struct {
		SourceID      uuid.UUID `bun:"provider_source_id"`
		Configuration string    `bun:"provider_configuration_identity"`
	}
	if err := fixture.db.NewRaw(`SELECT provider_source_id,provider_configuration_identity FROM matching_run WHERE id=?`, manualID).Scan(fixture.ctx, &emptyRunSource); err != nil || emptyRunSource.SourceID != source.ID || emptyRunSource.Configuration != source.Identity {
		t.Fatalf("empty-plan run lost its saved provider source: source=%+v err=%v", emptyRunSource, err)
	}
	manualAcquisition, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, manualID)
	var manualWaitOutput struct {
		State      string          `json:"state"`
		StageIndex *int            `json:"stage_index"`
		Descriptor json.RawMessage `json:"descriptor"`
		Reason     string          `json:"reason"`
	}
	if err != nil || !exists {
		t.Fatalf("read empty-plan acquisition: exists=%v err=%v", exists, err)
	}
	if err := json.Unmarshal(manualAcquisition.Output, &manualWaitOutput); err != nil {
		t.Fatalf("decode empty-plan manual wait: %v", err)
	}
	if manualAcquisition.Phase != "waiting_policy" || len(manualAcquisition.Intent.InitialProviderInputs) != 0 || len(manualAcquisition.Intent.FallbackQueryPlan) == 0 || manualWaitOutput.State != "waiting_policy" || manualWaitOutput.StageIndex != nil || string(manualWaitOutput.Descriptor) != "null" || manualWaitOutput.Reason != "fallback_plan_empty" {
		t.Fatalf("read empty-plan manual wait: exists=%v acquisition=%+v err=%v", exists, manualAcquisition, err)
	}
	var childCount int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, manualID).Scan(fixture.ctx, &childCount); err != nil || childCount != 0 {
		t.Fatalf("empty plan registered provider child: count=%d err=%v", childCount, err)
	}
	var registrationCount int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?`, manualID).Scan(fixture.ctx, &registrationCount); err != nil || registrationCount != 0 {
		t.Fatalf("empty plan registered fallback request: count=%d err=%v", registrationCount, err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE matching_acquisition SET intent=jsonb_set(intent,'{fallback_query_plan,stages,0,query}',to_jsonb('forged'::text)) WHERE run_id=?`, first.OriginalRunID).Exec(fixture.ctx); err != nil {
		t.Fatalf("forge saved fallback plan for reader test: %v", err)
	}
	if _, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, first.OriginalRunID); err == nil || exists {
		t.Fatalf("forged fallback plan was accepted: exists=%v err=%v", exists, err)
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
	return newAutomaticMatchingTargetFenceFixtureWithDatabase(t, nil)
}

func newAutomaticMatchingTargetFenceFixtureWithDatabase(t *testing.T, database *bun.DB) automaticTargetFenceFixture {
	t.Helper()
	ctx := context.Background()
	db := database
	if db == nil {
		db = testpostgres.OpenMigrated(t)
	}
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
