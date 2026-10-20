//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestManualMatchingRequestsPublicationReconciliationPostgreSQL(t *testing.T) {
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
		"releases": []any{map[string]any{"provider_release_id": fixture.releaseID, "release_id": releaseKey, "title": "Fixture Album", "tracklist_complete": true,
			"positions": []any{map[string]any{"track_id": fixture.trackID, "medium": 0, "position": 0, "recording_id": recordingKey, "displayed_number": "1", "title": "Fixture Track"}}}},
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
	header, _, err := repository.Get(ctx, fixture.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read evaluation: %+v, %v", header, err)
	}
	assignments := []persistence.MatchingManualAssignment{{FileMemberID: fixture.memberA, ProviderTrackID: fixture.trackID, Medium: 1, Position: 1}}
	var before int
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request`).Scan(ctx, &before); err != nil {
		t.Fatal(err)
	}
	preview, err := repository.PreviewMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignments)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	var afterPreview int
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request`).Scan(ctx, &afterPreview); err != nil {
		t.Fatal(err)
	}
	if afterPreview != before {
		t.Fatalf("preview created %d publication reconciliation requests", afterPreview-before)
	}
	requestID := uuid.New()
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_manual_receipt_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_manual_receipt_insert BEFORE INSERT ON manual_matching_receipt FOR EACH ROW EXECUTE FUNCTION fail_manual_receipt_insert()`); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ConfirmMatchingManual(ctx, fixture.group.ID, preview.EvaluationRevision, fixture.releaseID, assignments, preview.PreviewDigest, requestID); err == nil {
		t.Fatal("confirmation succeeded despite injected receipt failure")
	}
	var afterFailure, materializedTracks, receipts int
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request`).Scan(ctx, &afterFailure); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM provider_local_track WHERE provider_id=? AND provider_track_id=?`, fixture.provider, fixture.trackID).Scan(ctx, &materializedTracks); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM manual_matching_receipt WHERE request_id=?`, requestID).Scan(ctx, &receipts); err != nil {
		t.Fatal(err)
	}
	if afterFailure != before || materializedTracks != 0 || receipts != 0 {
		t.Fatalf("receipt failure did not roll back: requests=%d/%d tracks=%d receipts=%d", before, afterFailure, materializedTracks, receipts)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_manual_receipt_insert ON manual_matching_receipt`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DROP FUNCTION fail_manual_receipt_insert()`); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ConfirmMatchingManual(ctx, fixture.group.ID, preview.EvaluationRevision, fixture.releaseID, assignments, preview.PreviewDigest, requestID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	var epoch, localTrackID uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT local_track_id FROM provider_local_track WHERE provider_id=? AND provider_track_id=?`, fixture.provider, fixture.trackID).Scan(ctx, &localTrackID); err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := db.NewRaw(`SELECT request.requested_generation FROM publication_reconciliation_request request JOIN release_track position ON position.id=request.position_id WHERE request.output_epoch=? AND position.track_id=?`, epoch, localTrackID).Scan(ctx, &generation); err != nil || generation != 1 {
		t.Fatalf("manual confirmation reconciliation generation=%d err=%v", generation, err)
	}
	var scope string
	if err := db.NewRaw(`SELECT dirty.scope FROM publication_reconciliation_dirty_scope dirty JOIN publication_reconciliation_request request USING(output_epoch,position_id) JOIN release_track position ON position.id=request.position_id WHERE request.output_epoch=? AND position.track_id=?`, epoch, localTrackID).Scan(ctx, &scope); err != nil || scope != "manual_confirmation" {
		t.Fatalf("manual confirmation dirty scope=%q err=%v", scope, err)
	}
	if _, err := repository.ConfirmMatchingManual(ctx, fixture.group.ID, preview.EvaluationRevision, fixture.releaseID, assignments, preview.PreviewDigest, requestID); err != nil {
		t.Fatalf("receipt replay: %v", err)
	}
	var replayGeneration int64
	if err := db.NewRaw(`SELECT request.requested_generation FROM publication_reconciliation_request request JOIN release_track position ON position.id=request.position_id WHERE request.output_epoch=? AND position.track_id=?`, epoch, localTrackID).Scan(ctx, &replayGeneration); err != nil || replayGeneration != generation {
		t.Fatalf("receipt replay generation=%d, initial=%d err=%v", replayGeneration, generation, err)
	}
}
