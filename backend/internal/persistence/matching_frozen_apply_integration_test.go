//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

func TestFrozenMatchingApplyUsesPublishedSnapshotAfterProviderRefreshPostgreSQL(t *testing.T) {
	t.Run("fresh apply baseline and idempotent redelivery", func(t *testing.T) {
		callbackExecuted := false
		testAutomaticFallbackFiniteFlowWithPublicationCallback(t, false, false, false, false, func(fixture uniquePublicationApplyFixture) {
			callbackExecuted = true
			assertFrozenApplyFreshBaseline(t, fixture)
		}, true)
		if !callbackExecuted {
			t.Fatal("fresh frozen apply publication callback was not executed")
		}
	})
	t.Run("refreshed provider projection rejects stale decision", func(t *testing.T) {
		callbackExecuted := false
		testAutomaticFallbackFiniteFlowWithPublicationCallback(t, false, false, false, false, func(fixture uniquePublicationApplyFixture) {
			callbackExecuted = true
			assertFrozenApplyRejectsRefreshedProjection(t, fixture)
		}, true)
		if !callbackExecuted {
			t.Fatal("stale frozen apply publication callback was not executed")
		}
	})
}

// assertFrozenApplyFreshBaseline proves the published child applies from its
// immutable snapshot and that replaying the same delivery adds no work.
func assertFrozenApplyFreshBaseline(t *testing.T, fixture uniquePublicationApplyFixture) {
	t.Helper()
	ctx := context.Background()
	database := fixture.Database
	cache := persistence.NewProviderCacheRepository(database)
	frozen := fixture.InputSnapshot.Selected.Projection
	if len(frozen.Tracks) != 2 {
		t.Fatalf("published snapshot tracks=%d, want 2", len(frozen.Tracks))
	}
	assertSealedDecisionMatchesSnapshot(t, ctx, fixture)

	current, err := cache.GetMusicBrainzMatchingSnapshot(ctx, fixture.Source.Endpoint, fixture.Source.ConfigurationIdentity,
		[]string{fixture.InputSnapshot.Selected.ProviderKey}, []string{fixture.InputSnapshot.Selected.Lookup.CacheKey})
	if err != nil || len(current.Releases) != 1 || current.Releases[0].Name != frozen.Release.Title {
		t.Fatalf("fresh baseline cache=%+v err=%v; want sealed projection %q", current.Releases, err, frozen.Release.Title)
	}

	operations := service.NewOperations(fixture.Repository)
	runFrozenApplyWorker(t, fixture, operations, true)
	applied, err := fixture.Repository.GetOperation(ctx, fixture.ChildOperation.ID)
	if err != nil || applied.State != "succeeded" || applied.Stage != "applied" {
		t.Fatalf("frozen apply child=%+v err=%v; want succeeded/applied", applied, err)
	}
	assertFrozenApplyMaterialization(t, fixture, frozen)
	assertFrozenApplyReceipt(t, fixture, "applied")
	assertFrozenApplyReconciliation(t, fixture)

	var receiptCount, childCount int
	var generation int64
	readFrozenApplyCounts(t, fixture, &receiptCount, &childCount, &generation)

	runFrozenApplyWorker(t, fixture, operations, false)
	var replayReceiptCount, replayChildCount int
	var replayGeneration int64
	readFrozenApplyCounts(t, fixture, &replayReceiptCount, &replayChildCount, &replayGeneration)
	if replayReceiptCount != receiptCount || replayChildCount != childCount || replayGeneration != generation {
		t.Fatalf("worker replay added materialization work: receipts=%d/%d children=%d/%d generation=%d/%d",
			replayReceiptCount, receiptCount, replayChildCount, childCount, replayGeneration, generation)
	}
	currentAfter, err := cache.GetMusicBrainzMatchingSnapshot(ctx, fixture.Source.Endpoint, fixture.Source.ConfigurationIdentity,
		[]string{fixture.InputSnapshot.Selected.ProviderKey}, []string{fixture.InputSnapshot.Selected.Lookup.CacheKey})
	if err != nil || len(currentAfter.Releases) != 1 || currentAfter.Releases[0].Name != frozen.Release.Title {
		t.Fatalf("fresh apply replaced its sealed projection: releases=%+v err=%v", currentAfter.Releases, err)
	}
}

// assertFrozenApplyRejectsRefreshedProjection accepts a newer provider
// projection for the same release and proves the sealed decision can no longer
// apply. The replay must not add a second receipt.
func assertFrozenApplyRejectsRefreshedProjection(t *testing.T, fixture uniquePublicationApplyFixture) {
	t.Helper()
	ctx := context.Background()
	database := fixture.Database
	cache := persistence.NewProviderCacheRepository(database)
	registry := settings.New(persistence.NewSettingsRepository(database), nil)
	configuration, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatalf("read provider configuration: %v", err)
	}
	providerFetch := service.NewProviderFetchService(registry, nil, cache)
	request, err := providerFetch.PrepareOperation(ctx, "release_lookup", fixture.InputSnapshot.Selected.ProviderKey, 0, 0, true)
	if err != nil {
		t.Fatalf("prepare admitted refresh lookup: %v", err)
	}
	if request.ProviderSourceID == nil || *request.ProviderSourceID != fixture.Source.ID || request.ProviderConfigurationIdentity == nil || *request.ProviderConfigurationIdentity != fixture.Source.ConfigurationIdentity {
		t.Fatalf("refresh lookup changed source/configuration: operation=%+v source=%+v", request, fixture.Source)
	}
	client := openScanEnqueueRiver(t, database)
	if err := fixture.Repository.CreateOperationAndEnqueue(ctx, request, client,
		service.ProviderFetchOperationArgs{OperationID: request.ID}, nil); err != nil {
		t.Fatalf("admit refresh lookup: %v", err)
	}
	if request.RiverJobID == nil {
		t.Fatal("admitted refresh lookup has no River job")
	}
	if _, err := database.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *request.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("mark refresh lookup running: %v", err)
	}
	epoch, claimed, err := fixture.Repository.ClaimProviderOperationDelivery(ctx, request.ID, request.Attempt, *request.RiverJobID)
	if err != nil || !claimed {
		t.Fatalf("claim refresh lookup: claimed=%v err=%v", claimed, err)
	}
	operations := service.NewOperations(fixture.Repository)
	operation, err := fixture.Repository.GetOperation(ctx, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := operations.RunningForProviderDelivery(ctx, operation, epoch, "provider_fetching"); err != nil {
		t.Fatalf("start refresh lookup: %v", err)
	}

	seal, found, err := fixture.Repository.GetMatchingFallbackAuthoritySeal(ctx, fixture.ParentOperationID, fixture.InputSnapshot.Authority.WaitRevision)
	if err != nil || !found || len(seal.ProofHistory) != 2 || seal.SelectedCalculation == nil {
		t.Fatalf("read frozen provider history: seal=%+v found=%v err=%v", seal, found, err)
	}
	history := make([]persistence.MatchingFallbackCalculationContext, len(seal.ProofHistory))
	for i, proof := range seal.ProofHistory {
		receipt, err := fixture.Repository.GetMatchingFallbackCalculationReceipt(ctx, fixture.ParentOperationID, proof.AcquisitionRevision)
		if err != nil || receipt == nil || receipt.InputDigest != proof.InputDigest ||
			receipt.DecisionDigest != proof.DecisionDigest {
			t.Fatalf("read stage %d calculation receipt: %v", i, err)
		}
		if err := json.Unmarshal(receipt.Context, &history[i]); err != nil {
			t.Fatalf("decode stage %d proof: %v", i, err)
		}
	}
	if len(history[0].LookupReceipts) != 0 || len(history[1].LookupReceipts) != 1 {
		t.Fatal("expected an accepted empty search followed by one accepted lookup")
	}
	validateProviderEvidence := func(want error) {
		t.Helper()
		validateErr := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return persistence.ValidateMatchingFallbackProviderLayoutHistoryTx(ctx, tx, fixture.ParentOperationID, history)
		})
		if want == nil {
			if validateErr != nil {
				t.Fatalf("validate accepted provider layout evidence: %v", validateErr)
			}
			return
		}
		if !errors.Is(validateErr, want) {
			t.Fatalf("validate changed provider layout evidence = %v, want %v", validateErr, want)
		}
	}
	validateProviderEvidence(nil)

	frozen := fixture.InputSnapshot.Selected.Projection
	if len(frozen.Tracks) != 2 {
		t.Fatalf("published snapshot tracks=%d, want 2", len(frozen.Tracks))
	}
	artistID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	payload := json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Provider Album B","artist-credit":[{"name":"Artist B","artist":{"id":%q,"name":"Artist B"}}],"media":[{"position":1,"tracks":[{"id":%q,"number":"1","length":180000,"title":"Track Override B","recording":{"id":%q,"title":"Recording Title B","artist-credit":[{"name":"Artist B","artist":{"id":%q,"name":"Artist B"}}]}},{"id":%q,"number":"2","length":180000,"title":"Second Track B","recording":{"id":%q,"title":"Second Recording B","artist-credit":[{"name":"Artist B","artist":{"id":%q,"name":"Artist B"}}]}}]}]}`,
		fixture.InputSnapshot.Selected.ProviderKey, artistID,
		frozen.Tracks[0].ProviderTrackID, frozen.Tracks[0].RecordingProviderKey, artistID,
		frozen.Tracks[1].ProviderTrackID, frozen.Tracks[1].RecordingProviderKey, artistID))
	graph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: payload}})
	if err != nil {
		t.Fatalf("normalize B provider response: %v", err)
	}
	ticket, err := cache.BeginRefresh(ctx, fixture.Source.ID, fixture.InputSnapshot.Selected.Lookup.CacheKey, configuration.Identity)
	if err != nil {
		t.Fatalf("reserve B lookup response: %v", err)
	}
	// A new generation is a reservation only; it must not invalidate the
	// previously accepted projection revision before the response is accepted.
	validateProviderEvidence(nil)
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payload, Graph: graph,
		Delivery: &persistence.ProviderDeliveryFence{OperationID: request.ID, Attempt: request.Attempt, RiverJobID: *request.RiverJobID, ExecutionEpoch: epoch},
	}); err != nil {
		t.Fatalf("accept refreshed B lookup response: %v", err)
	}
	validateProviderEvidence(persistence.ErrIncomingMatchConflict)
	current, err := cache.GetMusicBrainzMatchingSnapshot(ctx, fixture.Source.Endpoint, fixture.Source.ConfigurationIdentity,
		[]string{fixture.InputSnapshot.Selected.ProviderKey}, []string{fixture.InputSnapshot.Selected.Lookup.CacheKey})
	if err != nil || len(current.Releases) != 1 || current.Releases[0].Name != "Provider Album B" {
		t.Fatalf("current provider projection before apply=%+v err=%v; want B", current.Releases, err)
	}
	if len(current.Releases[0].Tracks) != 2 || current.Releases[0].Tracks[0].RecordingTitle != "Recording Title B" || !strings.Contains(string(current.Releases[0].Fields), "Track Override B") {
		t.Fatalf("current provider track projection=%+v; want B override and recording title", current.Releases[0].Tracks)
	}

	runFrozenApplyWorker(t, fixture, operations, true)
	rejected, err := fixture.Repository.GetOperation(ctx, fixture.ChildOperation.ID)
	if err != nil || rejected.State != "succeeded" || rejected.Stage != "review_required" || rejected.SafeError == nil ||
		!strings.Contains(*rejected.SafeError, "stale") {
		t.Fatalf("refreshed provider projection did not reject the sealed decision: operation=%+v err=%v", rejected, err)
	}
	assertFrozenApplyReceipt(t, fixture, "review_required")
	assertNoFrozenApplyMaterialization(t, fixture, frozen)

	var receiptCount, childCount int
	var generation int64
	readFrozenApplyCounts(t, fixture, &receiptCount, &childCount, &generation)
	runFrozenApplyWorker(t, fixture, operations, false)
	var replayReceiptCount, replayChildCount int
	var replayGeneration int64
	readFrozenApplyCounts(t, fixture, &replayReceiptCount, &replayChildCount, &replayGeneration)
	if replayReceiptCount != receiptCount || replayChildCount != childCount || replayGeneration != generation {
		t.Fatalf("stale replay added work: receipts=%d/%d children=%d/%d generation=%d/%d",
			replayReceiptCount, receiptCount, replayChildCount, childCount, replayGeneration, generation)
	}
}

func assertSealedDecisionMatchesSnapshot(t *testing.T, ctx context.Context, fixture uniquePublicationApplyFixture) {
	t.Helper()
	seal, found, err := fixture.Repository.GetMatchingFallbackAuthoritySeal(ctx, fixture.ParentOperationID, fixture.InputSnapshot.Authority.WaitRevision)
	if err != nil || !found || seal.SelectedCalculation == nil ||
		seal.LayoutDecision.Status != persistence.MatchingLayoutDecisionSelected ||
		seal.LayoutDecision.ProviderReleaseID == nil ||
		*seal.LayoutDecision.ProviderReleaseID != fixture.InputSnapshot.Selected.ProviderReleaseID {
		t.Fatalf("read sealed layout decision: seal=%+v found=%v err=%v", seal, found, err)
	}
	digest, err := seal.LayoutDecision.Digest()
	if err != nil || digest != seal.SelectedCalculation.DecisionDigest {
		t.Fatalf("sealed decision digest=%q calculated=%q err=%v", seal.SelectedCalculation.DecisionDigest, digest, err)
	}
	if len(seal.LayoutDecision.Assignments) != len(fixture.InputSnapshot.Assignments) {
		t.Fatalf("sealed decision assignments=%d snapshot=%d", len(seal.LayoutDecision.Assignments), len(fixture.InputSnapshot.Assignments))
	}
	wanted := make(map[uuid.UUID]uuid.UUID, len(fixture.InputSnapshot.Assignments))
	for _, assignment := range fixture.InputSnapshot.Assignments {
		wanted[assignment.FileMemberID] = assignment.ProviderTrackID
	}
	for _, assignment := range seal.LayoutDecision.Assignments {
		if wanted[assignment.FileMemberID] != assignment.ProviderTrackID {
			t.Fatalf("sealed decision assigned wrong track for member %s: %s", assignment.FileMemberID, assignment.ProviderTrackID)
		}
	}
}

func runFrozenApplyWorker(t *testing.T, fixture uniquePublicationApplyFixture, operations *service.Operations, markRunning bool) {
	t.Helper()
	if fixture.ChildOperation.RiverJobID == nil {
		t.Fatal("frozen apply child has no River job")
	}
	jobID := *fixture.ChildOperation.RiverJobID
	if markRunning {
		if _, err := fixture.Database.NewRaw(`UPDATE river_job SET state='running',attempt=? WHERE id=?`, fixture.ChildOperation.Attempt, jobID).Exec(fixture.Context); err != nil {
			t.Fatalf("mark frozen apply job running: %v", err)
		}
	}
	worker := jobs.NewMatchingApplyWorker(fixture.Repository, operations)
	job := &river.Job[persistence.MatchingApplyOperationArgs]{
		JobRow: &rivertype.JobRow{ID: jobID, Attempt: fixture.ChildOperation.Attempt},
		Args:   persistence.MatchingApplyOperationArgs{OperationID: fixture.ChildOperation.ID},
	}
	if err := worker.Work(fixture.Context, job); err != nil {
		t.Fatalf("run real frozen matching apply worker: %v", err)
	}
}

func assertFrozenApplyMaterialization(t *testing.T, fixture uniquePublicationApplyFixture, frozen persistence.FrozenMaterialization) {
	t.Helper()
	ctx, database := fixture.Context, fixture.Database
	for _, assignment := range fixture.InputSnapshot.Assignments {
		var expected *uuid.UUID
		for _, captured := range fixture.InputSnapshot.Captured {
			if captured.MemberID == assignment.FileMemberID && captured.WorkID == assignment.WorkID {
				expected = captured.CaptureAnalysisID
			}
		}
		if expected == nil {
			t.Fatal("frozen matching fixture must include each member's analysed metadata")
		}
		var actual *uuid.UUID
		if err := database.NewRaw(`SELECT association.source_metadata_result_id FROM track_source association
			JOIN provider_local_track mapping ON mapping.local_track_id=association.track_id
			JOIN source_work_variant_identity identity ON identity.media_variant_id=association.media_variant_id
			WHERE mapping.provider_id=? AND mapping.provider_track_id=? AND identity.work_id=?`, fixture.Source.ProviderID, assignment.ProviderTrackID, assignment.WorkID).Scan(ctx, &actual); err != nil {
			t.Fatal(err)
		}
		if actual == nil || *actual != *expected {
			t.Fatalf("frozen matching attached another member's metadata: member=%s got=%v want=%s", assignment.FileMemberID, actual, *expected)
		}
	}
	var releaseTitle string
	if err := database.NewRaw(`SELECT local_release.title FROM local_release JOIN provider_local_release mapping ON mapping.local_release_id=local_release.id WHERE mapping.provider_id=? AND mapping.provider_release_id=?`, fixture.Source.ProviderID, frozen.Release.ID).Scan(ctx, &releaseTitle); err != nil || releaseTitle != frozen.Release.Title {
		t.Fatalf("materialized release title=%q err=%v; want frozen title %q", releaseTitle, err, frozen.Release.Title)
	}
	for _, track := range frozen.Tracks {
		if len(track.RecordingCredits) == 0 || len(track.Credits) == 0 || track.RecordingCredits[0].Name == track.Credits[0].Name {
			t.Fatalf("fixture must distinguish recording credits from normalized track override: recording=%+v track=%+v", track.RecordingCredits, track.Credits)
		}
		var recordingTitle, trackTitle string
		if err := database.NewRaw(`SELECT recording.title,local_track.title FROM provider_local_track mapping JOIN local_track ON local_track.id=mapping.local_track_id JOIN release_track ON release_track.track_id=local_track.id JOIN provider_local_release release_mapping ON release_mapping.local_release_id=release_track.release_id AND release_mapping.provider_id=mapping.provider_id JOIN local_recording recording ON recording.id=release_track.recording_id WHERE release_mapping.provider_release_id=? AND mapping.provider_id=? AND mapping.provider_track_id=?`, frozen.Release.ID, fixture.Source.ProviderID, track.ProviderTrackID).Scan(ctx, &recordingTitle, &trackTitle); err != nil || recordingTitle != track.RecordingTitle || trackTitle != track.Title {
			t.Fatalf("materialized frozen recording/track title=%q/%q err=%v; want snapshot values %q/%q", recordingTitle, trackTitle, err, track.RecordingTitle, track.Title)
		}
		var recordingCredits, trackCredits []struct {
			Position   int    `bun:"position"`
			Name       string `bun:"name"`
			JoinPhrase string `bun:"name_join_phrase"`
		}
		if err := database.NewRaw(`SELECT credit.position,credit.name,credit.name_join_phrase FROM provider_local_recording mapping
			JOIN provider_recording provider ON provider.id=mapping.provider_recording_id AND provider.provider_id=mapping.provider_id
			JOIN recording_artist credit ON credit.recording_id=mapping.local_recording_id
			WHERE mapping.provider_id=? AND provider.provider_key=? ORDER BY credit.position`, fixture.Source.ProviderID, track.RecordingProviderKey).Scan(ctx, &recordingCredits); err != nil {
			t.Fatalf("read materialized frozen recording credits: %v", err)
		}
		if err := database.NewRaw(`SELECT credit.position,credit.name,credit.name_join_phrase FROM provider_local_track mapping
			JOIN track_artist credit ON credit.track_id=mapping.local_track_id
			WHERE mapping.provider_id=? AND mapping.provider_track_id=? ORDER BY credit.position`, fixture.Source.ProviderID, track.ProviderTrackID).Scan(ctx, &trackCredits); err != nil {
			t.Fatalf("read materialized frozen track credits: %v", err)
		}
		if len(recordingCredits) != len(track.RecordingCredits) || len(trackCredits) != len(track.Credits) {
			t.Fatalf("materialized recording/track credit counts=%d/%d, want frozen %d/%d", len(recordingCredits), len(trackCredits), len(track.RecordingCredits), len(track.Credits))
		}
		for index, credit := range track.RecordingCredits {
			if recordingCredits[index].Position != credit.Position || recordingCredits[index].Name != credit.Name || recordingCredits[index].JoinPhrase != credit.JoinPhrase {
				t.Fatalf("materialized recording credit[%d]=%+v, want frozen %+v", index, recordingCredits[index], credit)
			}
		}
		for index, credit := range track.Credits {
			if trackCredits[index].Position != credit.Position || trackCredits[index].Name != credit.Name || trackCredits[index].JoinPhrase != credit.JoinPhrase {
				t.Fatalf("materialized track credit[%d]=%+v, want frozen %+v", index, trackCredits[index], credit)
			}
		}
	}
}

func assertNoFrozenApplyMaterialization(t *testing.T, fixture uniquePublicationApplyFixture, frozen persistence.FrozenMaterialization) {
	t.Helper()
	ctx, database := fixture.Context, fixture.Database
	var releases, associations, reconciliation int
	if err := database.NewRaw(`SELECT count(*) FROM provider_local_release WHERE provider_id=? AND provider_release_id=?`,
		fixture.Source.ProviderID, frozen.Release.ID).Scan(ctx, &releases); err != nil {
		t.Fatal(err)
	}
	for _, track := range frozen.Tracks {
		var count int
		if err := database.NewRaw(`SELECT count(*) FROM track_source association
			JOIN provider_local_track mapping ON mapping.local_track_id=association.track_id
			WHERE mapping.provider_id=? AND mapping.provider_track_id=?`, fixture.Source.ProviderID, track.ProviderTrackID).Scan(ctx, &count); err != nil {
			t.Fatal(err)
		}
		associations += count
	}
	if err := database.NewRaw(`SELECT count(*) FROM publication_reconciliation_request`).Scan(ctx, &reconciliation); err != nil {
		t.Fatal(err)
	}
	if releases != 0 || associations != 0 || reconciliation != 0 {
		t.Fatalf("stale apply materialized work: releases=%d associations=%d reconciliation=%d", releases, associations, reconciliation)
	}
}

func assertFrozenApplyReceipt(t *testing.T, fixture uniquePublicationApplyFixture, outcome string) {
	t.Helper()
	var receiptCount int
	var actual string
	if err := fixture.Database.NewRaw(`SELECT count(*),COALESCE(min(outcome),'') FROM matching_apply_receipt WHERE apply_operation_id=?`,
		fixture.ChildOperation.ID).Scan(fixture.Context, &receiptCount, &actual); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 1 || actual != outcome {
		t.Fatalf("apply receipt count/outcome=%d/%q, want 1/%q", receiptCount, actual, outcome)
	}
}

func assertFrozenApplyReconciliation(t *testing.T, fixture uniquePublicationApplyFixture) {
	t.Helper()
	ctx, database := fixture.Context, fixture.Database
	var reconciliationEpoch uuid.UUID
	if err := database.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &reconciliationEpoch); err != nil {
		t.Fatal(err)
	}
	var reconciliationPositions []uuid.UUID
	if err := database.NewRaw(`SELECT position.id FROM release_track position
		JOIN provider_local_release mapping ON mapping.local_release_id=position.release_id
		WHERE mapping.provider_id=? AND mapping.provider_release_id=? ORDER BY position.id`,
		fixture.Source.ProviderID, fixture.InputSnapshot.Selected.ProviderReleaseID).Scan(ctx, &reconciliationPositions); err != nil {
		t.Fatal(err)
	}
	var requestedPositions []uuid.UUID
	if err := database.NewRaw(`SELECT position_id FROM publication_reconciliation_request WHERE output_epoch=? ORDER BY position_id`, reconciliationEpoch).Scan(ctx, &requestedPositions); err != nil {
		t.Fatal(err)
	}
	if len(reconciliationPositions) == 0 || len(requestedPositions) != len(reconciliationPositions) {
		t.Fatalf("frozen apply reconciliation positions=%v materialized positions=%v", requestedPositions, reconciliationPositions)
	}
	for index := range reconciliationPositions {
		if reconciliationPositions[index] != requestedPositions[index] {
			t.Fatalf("frozen apply reconciliation positions=%v, want %v", requestedPositions, reconciliationPositions)
		}
	}
	var reconciliationScopes int
	if err := database.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND scope='automatic_matching'`, reconciliationEpoch).Scan(ctx, &reconciliationScopes); err != nil || reconciliationScopes != len(reconciliationPositions) {
		t.Fatalf("frozen apply automatic_matching scopes=%d want=%d err=%v", reconciliationScopes, len(reconciliationPositions), err)
	}
}

func readFrozenApplyCounts(t *testing.T, fixture uniquePublicationApplyFixture, receipts, children *int, generation *int64) {
	t.Helper()
	ctx := fixture.Context
	if err := fixture.Database.NewRaw(`SELECT count(*) FROM matching_apply_receipt WHERE apply_operation_id=?`, fixture.ChildOperation.ID).Scan(ctx, receipts); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Database.NewRaw(`SELECT count(*) FROM operation WHERE kind=? AND id IN (SELECT apply_operation_id FROM matching_apply_request WHERE parent_run_id=?)`,
		persistence.MatchingApplyOperationKind, fixture.ParentOperationID).Scan(ctx, children); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Database.NewRaw(`SELECT COALESCE(sum(requested_generation),0) FROM publication_reconciliation_request`).Scan(ctx, generation); err != nil {
		t.Fatal(err)
	}
}
