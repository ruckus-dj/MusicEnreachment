//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestManualMatchingAuthorityContinuationPostgreSQL(t *testing.T) {
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
		t.Fatalf("read evaluation header: %+v, %v", header, err)
	}
	assignments := []persistence.MatchingManualAssignment{{FileMemberID: fixture.memberA, ProviderTrackID: fixture.trackID, Medium: 1, Position: 1}}
	preview, err := repository.PreviewMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignments)
	if err != nil {
		t.Fatalf("initial preview: %v", err)
	}
	if _, err := repository.ConfirmMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignments, preview.PreviewDigest, uuid.New()); err != nil {
		t.Fatalf("initial confirm: %v", err)
	}
	if _, err := repository.ConfirmMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignments, preview.PreviewDigest, uuid.New()); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("confirmation with pre-transition digest error = %v, want conflict", err)
	}
	// Confirmation advances authority automatic/0 -> manual/1. This exact
	// one-step continuation may keep using the evaluation without re-evaluating.
	if _, err := repository.PreviewMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignments); err != nil {
		t.Fatalf("preview after manual continuation: %v", err)
	}
	conflict := func(label string) {
		t.Helper()
		if _, err := repository.PreviewMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignments); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
			t.Fatalf("%s preview error = %v, want conflict", label, err)
		}
	}
	if _, err := db.NewRaw(`UPDATE incoming_group_matching_authority SET mode='manual',revision=3 WHERE group_id=?`, fixture.group.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	conflict("manual authority revision 3 (returned automatic cycle)")
	if _, err := db.NewRaw(`UPDATE incoming_group_matching_authority SET mode='automatic',revision=2 WHERE group_id=?`, fixture.group.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	conflict("automatic authority revision 2")
}
