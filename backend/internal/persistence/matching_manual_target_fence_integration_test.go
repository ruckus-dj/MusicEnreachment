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

// This fixture tests the manual reader continuation boundary, not automatic
// evaluation publication. It uses a real automatic v2 admission/capture and
// seeds the existing legacy evaluation row with that capture's exact authority.
func TestManualMatchingPreviewContinuesAgainstVersionTwoTargetCapturePostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	incoming := newIncomingMatchFixture(t, ctx, db)
	candidates := persistence.NewIncomingMatchCandidateRepository(db)
	if err := candidates.Replace(ctx, incoming.evaluation(nil)); err != nil {
		t.Fatalf("seed existing evaluation row: %v", err)
	}

	setup := persistence.NewSetupManagerRepository(db)
	client := openScanEnqueueRiver(t, db)
	var event persistence.IncomingMatchingReconcileEvent
	if err := db.NewSelect().Model(&event).Where("reason = ?", "analysis_success").Order("created_at ASC").Order("id ASC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read real reconciliation event: %v", err)
	}
	inventory := persistence.NewSourceInventoryRepository(db)
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch real reconciliation event: %v", err)
	}
	delivered, err := inventory.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil || delivered == nil || delivered.RegisteredJobID == nil {
		t.Fatalf("read registered reconciliation delivery: event=%+v err=%v", delivered, err)
	}
	event = *delivered
	epoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *event.RegisteredJobID, 1)
	if err != nil || !claimed {
		t.Fatalf("claim real reconciliation delivery: claimed=%v err=%v", claimed, err)
	}
	var source struct {
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := db.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, incoming.source).Scan(ctx, &source); err != nil {
		t.Fatalf("read provider source: %v", err)
	}
	query := json.RawMessage(`{"schema_version":1,"query":"Fixture Artist","offset":0,"limit":1}`)
	parentID, providerOperationID := uuid.New(), uuid.New()
	providerID, sourceID := source.ProviderID, incoming.source
	identity, key := source.Identity, "artist-search:0:1:Fixture Artist"
	parent := &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	providerOperation := &persistence.Operation{
		ID: providerOperationID, Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
		ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderConfigurationIdentity: matchingCoordinatorStringPtr(identity),
		ProviderEndpoint: source.Endpoint, ProviderCacheKey: &key, Attempt: 1,
	}
	admitted, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Provider: persistence.MatchingProviderRequest{Operation: providerOperation, Args: service.ProviderFetchOperationArgs{OperationID: providerOperationID}},
		GroupID:  incoming.group.ID, ExpectedGroupRevision: incoming.group.Revision, GroupingEpoch: incoming.groupingEpoch,
		PolicyVersion: service.MatchingPolicyVersion, ArtistQueryPlanVersion: 1, QueryDescriptor: query,
		EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: epoch,
	})
	if err != nil || admitted.AlreadyAdmitted || admitted.Operation == nil {
		t.Fatalf("admit real automatic v2 run for evaluation group: result=%+v err=%v", admitted, err)
	}
	fence, exists, err := setup.GetMatchingRunFence(ctx, parentID)
	if err != nil || !exists || fence.Version != 2 || fence.GroupID != incoming.group.ID || len(fence.Capture) == 0 || fence.CaptureDigest == "" {
		t.Fatalf("read actual v2 target capture: fence=%+v exists=%v err=%v", fence, exists, err)
	}
	targetOnlyRevision, err := service.IncomingGroupsRevision([]service.IncomingGroup{{
		ID: incoming.group.ID, Revision: fence.GroupRevision, Members: []uuid.UUID{incoming.memberA, incoming.memberB},
	}})
	if err != nil {
		t.Fatalf("compute target-only group revision: %v", err)
	}
	if targetOnlyRevision == fence.GroupRevision {
		t.Fatal("target-only groups revision unexpectedly equals the captured group revision")
	}
	if _, err := db.NewRaw(`UPDATE incoming_match_evaluation SET
		group_revision=?,groups_revision=?,fence_version=2,target_run_id=?,target_capture=?::jsonb,target_capture_digest=decode(?,'hex')
		WHERE group_id=?`, fence.GroupRevision, targetOnlyRevision, parentID, string(fence.Capture), fence.CaptureDigest, incoming.group.ID).Exec(ctx); err != nil {
		t.Fatalf("seed manual-reader evaluation authority from actual v2 capture: %v", err)
	}
	var persistedFence int
	var persistedGroupsRevision string
	if err := db.NewRaw(`SELECT fence_version,groups_revision FROM incoming_match_evaluation WHERE group_id=?`, incoming.group.ID).Scan(ctx, &persistedFence, &persistedGroupsRevision); err != nil {
		t.Fatal(err)
	}
	if persistedFence != 2 || persistedGroupsRevision != targetOnlyRevision || persistedGroupsRevision == fence.GroupRevision {
		t.Fatalf("evaluation fixture fence=%d groups revision=%q, target-only=%q target group=%q", persistedFence, persistedGroupsRevision, targetOnlyRevision, fence.GroupRevision)
	}

	var releaseKey, recordingKey string
	if err := db.NewRaw(`SELECT r.provider_key,rec.provider_key FROM provider_release r JOIN provider_release_track track ON track.provider_id=r.provider_id AND track.release_id=r.id JOIN provider_recording rec ON rec.provider_id=track.provider_id AND rec.id=track.recording_id WHERE r.id=? AND track.id=?`, incoming.releaseID, incoming.trackID).Scan(ctx, &releaseKey, &recordingKey); err != nil {
		t.Fatal(err)
	}
	proposal, err := json.Marshal(map[string]any{
		"version": 1, "group_id": incoming.group.ID,
		"selected_release": map[string]any{"provider_release_id": incoming.releaseID, "release_id": releaseKey, "title": "Fixture Album", "tracklist_complete": true,
			"positions": []any{map[string]any{"track_id": incoming.trackID, "medium": 0, "position": 0, "recording_id": recordingKey, "displayed_number": "1", "title": "Fixture Track"}}},
		"releases": []any{map[string]any{"provider_release_id": incoming.releaseID, "release_id": releaseKey, "title": "Fixture Album", "tracklist_complete": true,
			"positions": []any{map[string]any{"track_id": incoming.trackID, "medium": 0, "position": 0, "recording_id": recordingKey, "displayed_number": "1", "title": "Fixture Track"}}}},
		"files": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := json.Marshal(map[string]any{"review_proposal": json.RawMessage(proposal)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE incoming_match_evaluation SET evaluation_evidence=?::jsonb WHERE group_id=?`, string(evidence), incoming.group.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	header, _, err := candidates.Get(ctx, incoming.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read seeded evaluation: header=%+v err=%v", header, err)
	}
	override := "Manual title"
	assignments := []persistence.MatchingManualAssignment{{FileMemberID: incoming.memberA, ProviderTrackID: incoming.trackID, Medium: 1, Position: 1, TitleOverride: &override}}
	preview, err := candidates.PreviewMatchingManual(ctx, incoming.group.ID, header.EvaluationRevision, incoming.releaseID, assignments)
	if err != nil {
		t.Fatalf("positive v2 manual preview: %v", err)
	}
	if _, err := candidates.ConfirmMatchingManual(ctx, incoming.group.ID, header.EvaluationRevision, incoming.releaseID, assignments, preview.PreviewDigest, uuid.New()); err != nil {
		t.Fatalf("confirm v2 manual title override: %v", err)
	}
	if _, err := candidates.PreviewMatchingManual(ctx, incoming.group.ID, header.EvaluationRevision, incoming.releaseID, assignments); err != nil {
		t.Fatalf("manual preview after authority promotion: %v", err)
	}

	result, err := db.NewRaw(`UPDATE incoming_group_member SET ready=NOT ready WHERE group_id=? AND variant_id=(SELECT variant_id FROM incoming_group_member WHERE group_id=? ORDER BY variant_id LIMIT 1)`, incoming.group.ID, incoming.group.ID).Exec(ctx)
	if err != nil {
		t.Fatalf("mutate captured target source fact: %v", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		t.Fatalf("mutate captured target source fact: rows=%d err=%v", changed, err)
	}
	if _, err := candidates.PreviewMatchingManual(ctx, incoming.group.ID, header.EvaluationRevision, incoming.releaseID, assignments); err == nil {
		t.Fatal("manual preview accepted a changed captured target source fact")
	}
}
