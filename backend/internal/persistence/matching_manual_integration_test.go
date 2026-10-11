//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestManualMatchingPartialAssignmentReceiptPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	if err := repository.Replace(ctx, fixture.evaluation(nil)); err != nil {
		t.Fatalf("publish evaluation: %v", err)
	}
	var releaseKey, recordingKey string
	if err := db.NewRaw(`SELECT r.provider_key,rec.provider_key FROM provider_release r JOIN provider_release_track t ON t.provider_id=r.provider_id AND t.release_id=r.id JOIN provider_recording rec ON rec.provider_id=t.provider_id AND rec.id=t.recording_id WHERE r.id=? AND t.id=?`, fixture.releaseID, fixture.trackID).Scan(ctx, &releaseKey, &recordingKey); err != nil {
		t.Fatal(err)
	}
	proposal, err := json.Marshal(map[string]any{
		"version": 1, "group_id": fixture.group.ID,
		"selected_release": map[string]any{"provider_release_id": fixture.releaseID, "release_id": releaseKey, "title": "Fixture Album", "tracklist_complete": true,
			"positions": []any{map[string]any{"track_id": fixture.trackID, "medium": 0, "position": 0, "recording_id": recordingKey, "displayed_number": "1", "title": "Fixture Track"}}},
		"releases": []any{map[string]any{
			"provider_release_id": fixture.releaseID, "release_id": releaseKey, "title": "Fixture Album", "tracklist_complete": true,
			"positions": []any{map[string]any{"track_id": fixture.trackID, "medium": 0, "position": 0, "recording_id": recordingKey, "displayed_number": "1", "title": "Fixture Track"}},
		}},
		"files": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := json.Marshal(map[string]any{"review_proposal": json.RawMessage(proposal)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE incoming_match_evaluation SET evaluation_evidence=?::jsonb WHERE group_id=?`, string(evidence), fixture.group.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assignment := []persistence.MatchingManualAssignment{{FileMemberID: fixture.memberA, ProviderTrackID: fixture.trackID, Medium: 1, Position: 1}}
	header, _, err := repository.Get(ctx, fixture.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read evaluation header: %+v, %v", header, err)
	}
	// Invalid requests must fail before publication/materialization and leave the
	// valid snapshot usable for the subsequent preview and confirmation.
	for name, invalid := range map[string][]persistence.MatchingManualAssignment{
		"nil member":       {{ProviderTrackID: fixture.trackID, Medium: 1, Position: 1}},
		"zero position":    {{FileMemberID: fixture.memberA, ProviderTrackID: fixture.trackID, Medium: 1}},
		"duplicate member": {assignment[0], assignment[0]},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := repository.PreviewMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, invalid); err == nil {
				t.Fatal("invalid assignment unexpectedly previewed")
			}
		})
	}
	preview, err := repository.PreviewMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignment)
	if err != nil {
		t.Fatalf("preview partial manual assignment: %v", err)
	}
	if len(preview.Members) != 2 || len(preview.OmittedMemberIDs) != 1 || preview.OmittedMemberIDs[0] != fixture.memberB {
		t.Fatalf("preview scope = %+v", preview)
	}
	if preview.SelectedRelease.ReleaseID != releaseKey || preview.SelectedRelease.Title != "Fixture Album" || len(preview.SelectedRelease.Positions) != 1 ||
		preview.SelectedRelease.Positions[0].RecordingID != recordingKey || preview.SelectedRelease.Positions[0].DisplayedNumber != "1" {
		t.Fatalf("preview omitted saved selected-release layout: %+v", preview.SelectedRelease)
	}
	requestID := uuid.New()
	type confirmationResult struct {
		response persistence.MatchingManualPreview
		err      error
	}
	start := make(chan struct{})
	results := make(chan confirmationResult, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			response, confirmErr := repository.ConfirmMatchingManual(ctx, fixture.group.ID, preview.EvaluationRevision, fixture.releaseID, assignment, preview.PreviewDigest, requestID)
			results <- confirmationResult{response: response, err: confirmErr}
		}()
	}
	ready.Wait()
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent identical confirmation errors: %v, %v", first.err, second.err)
	}
	if !reflect.DeepEqual(first.response, second.response) {
		t.Fatalf("concurrent confirmation responses differ: %+v / %+v", first.response, second.response)
	}
	confirmed := first.response
	if confirmed.LocalReleaseID == uuid.Nil {
		t.Fatal("confirmation omitted local release identity")
	}
	var manualTracks, manualLinks, receipts int
	if err := db.NewRaw(`SELECT count(*) FROM provider_local_track WHERE provider_id=? AND provider_track_id=? AND method='manual' AND policy_version IS NULL`, fixture.provider, fixture.trackID).Scan(ctx, &manualTracks); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM track_source WHERE media_variant_id IN (SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=?) AND decision_method='manual' AND policy_version IS NULL`, fixture.workA).Scan(ctx, &manualLinks); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM manual_matching_receipt WHERE request_id=?`, requestID).Scan(ctx, &receipts); err != nil {
		t.Fatal(err)
	}
	if manualTracks != 1 || manualLinks < 1 || receipts != 1 {
		t.Fatalf("manual track mappings=%d source links=%d receipts=%d", manualTracks, manualLinks, receipts)
	}
	if _, err := db.NewRaw(`UPDATE incoming_match_evaluation SET evaluation_revision='changed-after-confirmation' WHERE group_id=?`, fixture.group.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	replay, err := service.NewMatchingManualService(repository).Confirm(ctx, fixture.group.ID, preview.EvaluationRevision, fixture.releaseID,
		[]service.MatchingManualAssignment{{FileMemberID: fixture.memberA, ProviderTrackID: fixture.trackID, Medium: 1, Position: 1}}, preview.PreviewDigest, requestID)
	if err != nil || replay.PreviewDigest != confirmed.PreviewDigest {
		t.Fatalf("receipt replay after evaluation change = %+v, %v", replay, err)
	}
	if replay.EvaluationRevision != preview.EvaluationRevision || !reflect.DeepEqual(replay.SelectedRelease, service.MatchingReviewRelease{
		ProviderReleaseID: fixture.releaseID, ReleaseID: releaseKey, Title: "Fixture Album", TracklistComplete: true,
		Positions: []service.MatchingReviewLayoutPosition{{TrackID: fixture.trackID, Medium: 0, Position: 0, DisplayedNumber: "1", RecordingID: recordingKey, Title: "Fixture Track"}},
	}) {
		t.Fatalf("service replay did not preserve immutable selected release: %+v", replay)
	}
	_, err = repository.ConfirmMatchingManual(ctx, fixture.group.ID, preview.EvaluationRevision, fixture.releaseID, nil, preview.PreviewDigest, requestID)
	if !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("changed receipt intent error = %v", err)
	}
}
