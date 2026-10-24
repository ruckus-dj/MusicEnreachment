//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
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
)

func TestFrozenMatchingApplyUsesPublishedSnapshotAfterProviderRefreshPostgreSQL(t *testing.T) {
	callbackExecuted := false
	testAutomaticFallbackFiniteFlowWithPublicationCallback(t, false, false, false, true, false, func(fixture uniquePublicationApplyFixture) {
		callbackExecuted = true
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
		if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
			Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payload, Graph: graph,
			Delivery: &persistence.ProviderDeliveryFence{OperationID: request.ID, Attempt: request.Attempt, RiverJobID: *request.RiverJobID, ExecutionEpoch: epoch},
		}); err != nil {
			t.Fatalf("accept refreshed B lookup response: %v", err)
		}
		current, err := cache.GetMusicBrainzMatchingSnapshot(ctx, fixture.Source.Endpoint, fixture.Source.ConfigurationIdentity,
			[]string{fixture.InputSnapshot.Selected.ProviderKey}, []string{fixture.InputSnapshot.Selected.Lookup.CacheKey})
		if err != nil || len(current.Releases) != 1 || current.Releases[0].Name != "Provider Album B" {
			t.Fatalf("current provider projection before apply=%+v err=%v; want B", current.Releases, err)
		}
		if len(current.Releases[0].Tracks) != 2 || current.Releases[0].Tracks[0].RecordingTitle != "Recording Title B" || !strings.Contains(string(current.Releases[0].Fields), "Track Override B") {
			t.Fatalf("current provider track projection=%+v; want B override and recording title", current.Releases[0].Tracks)
		}

		applyWorker := jobs.NewMatchingApplyWorker(fixture.Repository, operations)
		if fixture.ChildOperation.RiverJobID == nil {
			t.Fatal("frozen apply child has no River job")
		}
		jobID := *fixture.ChildOperation.RiverJobID
		if _, err := database.NewRaw(`UPDATE river_job SET state='running',attempt=? WHERE id=?`, fixture.ChildOperation.Attempt, jobID).Exec(ctx); err != nil {
			t.Fatalf("mark frozen apply job running: %v", err)
		}
		job := &river.Job[persistence.MatchingApplyOperationArgs]{
			JobRow: &rivertype.JobRow{ID: jobID, Attempt: fixture.ChildOperation.Attempt},
			Args:   persistence.MatchingApplyOperationArgs{OperationID: fixture.ChildOperation.ID},
		}
		if err := applyWorker.Work(ctx, job); err != nil {
			t.Fatalf("run real frozen matching apply worker: %v", err)
		}
		applied, err := fixture.Repository.GetOperation(ctx, fixture.ChildOperation.ID)
		if err != nil || applied.State != "succeeded" || applied.Stage != "applied" {
			t.Fatalf("frozen apply child=%+v err=%v; want succeeded/applied", applied, err)
		}
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
		var initialGeneration int64
		if err := database.NewRaw(`SELECT sum(requested_generation) FROM publication_reconciliation_request WHERE output_epoch=?`, reconciliationEpoch).Scan(ctx, &initialGeneration); err != nil {
			t.Fatal(err)
		}
		var reconciliationScopes int
		if err := database.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND scope='automatic_matching'`, reconciliationEpoch).Scan(ctx, &reconciliationScopes); err != nil || reconciliationScopes != len(reconciliationPositions) {
			t.Fatalf("frozen apply automatic_matching scopes=%d want=%d err=%v", reconciliationScopes, len(reconciliationPositions), err)
		}

		// The apply materializes A from its immutable child snapshot, without
		// replacing the accepted B cache projection.
		var releaseTitle string
		if err := database.NewRaw(`SELECT local_release.title FROM local_release JOIN provider_local_release mapping ON mapping.local_release_id=local_release.id WHERE mapping.provider_id=? AND mapping.provider_release_id=?`, fixture.Source.ProviderID, frozen.Release.ID).Scan(ctx, &releaseTitle); err != nil || releaseTitle != frozen.Release.Title {
			t.Fatalf("materialized release title=%q err=%v; want frozen A title %q", releaseTitle, err, frozen.Release.Title)
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
		var receiptCount, childCount int
		if err := database.NewRaw(`SELECT count(*) FROM matching_apply_receipt WHERE apply_operation_id=?`, fixture.ChildOperation.ID).Scan(ctx, &receiptCount); err != nil {
			t.Fatal(err)
		}
		if err := database.NewRaw(`SELECT count(*) FROM operation WHERE kind=? AND id IN (SELECT apply_operation_id FROM matching_apply_request WHERE parent_run_id=?)`, persistence.MatchingApplyOperationKind, fixture.ParentOperationID).Scan(ctx, &childCount); err != nil {
			t.Fatal(err)
		}
		if receiptCount != 1 || childCount != 1 {
			t.Fatalf("apply receipts/children=%d/%d; want one each", receiptCount, childCount)
		}
		if err := applyWorker.Work(ctx, job); err != nil {
			t.Fatalf("replay completed frozen apply worker: %v", err)
		}
		var replayReceiptCount, replayChildCount int
		if err := database.NewRaw(`SELECT count(*) FROM matching_apply_receipt WHERE apply_operation_id=?`, fixture.ChildOperation.ID).Scan(ctx, &replayReceiptCount); err != nil {
			t.Fatal(err)
		}
		if err := database.NewRaw(`SELECT count(*) FROM operation WHERE kind=? AND id IN (SELECT apply_operation_id FROM matching_apply_request WHERE parent_run_id=?)`, persistence.MatchingApplyOperationKind, fixture.ParentOperationID).Scan(ctx, &replayChildCount); err != nil {
			t.Fatal(err)
		}
		if replayReceiptCount != receiptCount || replayChildCount != childCount {
			t.Fatalf("worker replay added materialization work: receipts=%d children=%d", replayReceiptCount, replayChildCount)
		}
		var replayGeneration int64
		if err := database.NewRaw(`SELECT sum(requested_generation) FROM publication_reconciliation_request WHERE output_epoch=?`, reconciliationEpoch).Scan(ctx, &replayGeneration); err != nil || replayGeneration != initialGeneration {
			t.Fatalf("frozen apply replay changed reconciliation generation: %d -> %d err=%v", initialGeneration, replayGeneration, err)
		}
		currentAfter, err := cache.GetMusicBrainzMatchingSnapshot(ctx, fixture.Source.Endpoint, fixture.Source.ConfigurationIdentity,
			[]string{fixture.InputSnapshot.Selected.ProviderKey}, []string{fixture.InputSnapshot.Selected.Lookup.CacheKey})
		if err != nil || len(currentAfter.Releases) != 1 || currentAfter.Releases[0].Name != "Provider Album B" || currentAfter.Releases[0].Tracks[0].RecordingTitle != "Recording Title B" || !strings.Contains(string(currentAfter.Releases[0].Fields), "Track Override B") {
			t.Fatalf("apply replaced current B projection: releases=%+v err=%v", currentAfter.Releases, err)
		}
	}, true)
	if !callbackExecuted {
		t.Fatal("frozen apply publication callback was not executed")
	}
}
