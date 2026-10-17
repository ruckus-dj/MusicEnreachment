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
	"github.com/uptrace/bun"
)

func TestManualMatchingTrackOverridesAreLocalAndFencedPostgreSQL(t *testing.T) {
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
	confirm := func(assignments []persistence.MatchingManualAssignment, requestID uuid.UUID) persistence.MatchingManualPreview {
		t.Helper()
		preview, err := repository.PreviewMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignments)
		if err != nil {
			t.Fatalf("preview: %v", err)
		}
		result, err := repository.ConfirmMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignments, preview.PreviewDigest, requestID)
		if err != nil {
			t.Fatalf("confirm: %v", err)
		}
		return result
	}
	initial := confirm(assignments, uuid.New())
	var localTrackID, localRecordingID uuid.UUID
	var sourceLinks int
	if err := db.NewRaw(`SELECT m.local_track_id,t.recording_id FROM provider_local_track m JOIN release_track t ON t.track_id=m.local_track_id WHERE m.provider_id=? AND m.provider_track_id=?`, fixture.provider, fixture.trackID).Scan(ctx, &localTrackID, &localRecordingID); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM track_source WHERE track_id=?`, localTrackID).Scan(ctx, &sourceLinks); err != nil {
		t.Fatal(err)
	}
	var recordingTitle string
	if err := db.NewRaw(`SELECT title FROM local_recording WHERE id=?`, localRecordingID).Scan(ctx, &recordingTitle); err != nil {
		t.Fatal(err)
	}
	var providerRecordingTitle string
	if err := db.NewRaw(`SELECT COALESCE(fields->>'title','') FROM provider_source_entity WHERE provider_id=? AND entity_kind='recording' AND provider_key=? LIMIT 1`, fixture.provider, recordingKey).Scan(ctx, &providerRecordingTitle); err != nil {
		t.Fatal(err)
	}
	var providerDefaultTrackTitle string
	if err := db.NewRaw(`SELECT title FROM local_track WHERE id=?`, localTrackID).Scan(ctx, &providerDefaultTrackTitle); err != nil {
		t.Fatal(err)
	}
	if providerDefaultTrackTitle != "Fixture Track" {
		t.Fatalf("initial manual confirmation did not use provider title: %q", providerDefaultTrackTitle)
	}
	emptyTitle := ""
	artistOverrides := []string{"", "Name"}
	explicit := []persistence.MatchingManualAssignment{{FileMemberID: fixture.memberA, ProviderTrackID: fixture.trackID, Medium: 1, Position: 1, TitleOverride: &emptyTitle, ArtistOverrides: &artistOverrides}}
	explicitPreview, err := repository.PreviewMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, explicit)
	if err != nil {
		t.Fatalf("preview explicit overrides: %v", err)
	}
	explicitRequest := uuid.New()
	confirmed, err := repository.ConfirmMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, explicit, explicitPreview.PreviewDigest, explicitRequest)
	if err != nil {
		t.Fatalf("confirm explicit overrides: %v", err)
	}
	confirmedAssignment := manualReceiptAssignment(confirmed, fixture.memberA)
	if confirmedAssignment == nil || confirmedAssignment.TitleOverride == nil || *confirmedAssignment.TitleOverride != "" || confirmedAssignment.ArtistOverrides == nil || len(*confirmedAssignment.ArtistOverrides) != 2 {
		t.Fatalf("receipt did not retain explicit override values: %+v", confirmedAssignment)
	}
	assertManualOverrideValues(t, ctx, db, localTrackID, "", []string{"", "Name"})
	var overrideRevision int64
	if err := db.NewRaw(`SELECT metadata_revision FROM local_track WHERE id=?`, localTrackID).Scan(ctx, &overrideRevision); err != nil {
		t.Fatal(err)
	}
	if overrideRevision != 2 {
		t.Fatalf("manual title/credit override metadata revision = %d, want 2", overrideRevision)
	}
	var afterRecordingTitle string
	if err := db.NewRaw(`SELECT title FROM local_recording WHERE id=?`, localRecordingID).Scan(ctx, &afterRecordingTitle); err != nil {
		t.Fatal(err)
	}
	if afterRecordingTitle != recordingTitle {
		t.Fatalf("track override changed shared local recording title: %q -> %q", recordingTitle, afterRecordingTitle)
	}
	if err := db.NewRaw(`SELECT COALESCE(fields->>'title','') FROM provider_source_entity WHERE provider_id=? AND entity_kind='recording' AND provider_key=? LIMIT 1`, fixture.provider, recordingKey).Scan(ctx, &afterRecordingTitle); err != nil {
		t.Fatal(err)
	}
	if afterRecordingTitle != providerRecordingTitle {
		t.Fatal("track override changed provider recording metadata")
	}
	var replay persistence.MatchingManualPreview
	replay, err = repository.ConfirmMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, explicit, explicitPreview.PreviewDigest, explicitRequest)
	replayedAssignment := manualReceiptAssignment(replay, fixture.memberA)
	if err != nil || replayedAssignment == nil || replayedAssignment.ArtistOverrides == nil || len(*replayedAssignment.ArtistOverrides) != 2 {
		t.Fatalf("receipt replay lost overrides: %+v, %v", replay, err)
	}
	if _, err := repository.ConfirmMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, []persistence.MatchingManualAssignment{{FileMemberID: fixture.memberA, ProviderTrackID: fixture.trackID, Medium: 1, Position: 1, TitleOverride: ptrString("different")}}, explicitPreview.PreviewDigest, uuid.New()); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("changed override with stale digest error = %v, want conflict", err)
	}
	noOverride := assignments
	noOverridePreview, err := repository.PreviewMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, noOverride)
	if err != nil {
		t.Fatalf("preview without new override: %v", err)
	}
	if _, err := repository.ConfirmMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, noOverride, noOverridePreview.PreviewDigest, uuid.New()); err != nil {
		t.Fatalf("confirm without new override: %v", err)
	}
	assertManualOverrideValues(t, ctx, db, localTrackID, "", []string{"", "Name"})
	if err := db.NewRaw(`SELECT metadata_revision FROM local_track WHERE id=?`, localTrackID).Scan(ctx, &overrideRevision); err != nil {
		t.Fatal(err)
	}
	if overrideRevision != 2 {
		t.Fatalf("confirmation without overrides changed metadata revision: %d", overrideRevision)
	}
	if _, err := db.NewRaw(`UPDATE local_track SET title='concurrent edit' WHERE id=?`, localTrackID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ConfirmMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, noOverride, noOverridePreview.PreviewDigest, uuid.New()); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("changed local metadata with stale digest error = %v, want conflict", err)
	}
	var currentLinks, mappings int
	if err := db.NewRaw(`SELECT count(*) FROM track_source WHERE track_id=?`, localTrackID).Scan(ctx, &currentLinks); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM provider_local_track WHERE provider_id=? AND provider_track_id=?`, fixture.provider, fixture.trackID).Scan(ctx, &mappings); err != nil {
		t.Fatal(err)
	}
	if currentLinks != sourceLinks || mappings != 1 || initial.LocalReleaseID == uuid.Nil {
		t.Fatalf("repeat confirmation changed links/mappings: links %d -> %d, mappings=%d", sourceLinks, currentLinks, mappings)
	}
}

func TestManualMatchingSequentialOverridesPreserveNestedKeysPostgreSQL(t *testing.T) {
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
	var localTrackID uuid.UUID
	confirm := func(title *string, artists *[]string) {
		t.Helper()
		assignments := []persistence.MatchingManualAssignment{{FileMemberID: fixture.memberA, ProviderTrackID: fixture.trackID, Medium: 1, Position: 1, TitleOverride: title, ArtistOverrides: artists}}
		preview, err := repository.PreviewMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignments)
		if err != nil {
			t.Fatalf("preview overrides: %v", err)
		}
		if _, err := repository.ConfirmMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignments, preview.PreviewDigest, uuid.New()); err != nil {
			t.Fatalf("confirm overrides: %v", err)
		}
		if localTrackID == uuid.Nil {
			if err := db.NewRaw(`SELECT m.local_track_id FROM provider_local_track m WHERE m.provider_id=? AND m.provider_track_id=?`, fixture.provider, fixture.trackID).Scan(ctx, &localTrackID); err != nil {
				t.Fatal(err)
			}
		}
	}
	assertOverride := func(wantTitle any, wantArtists any) {
		t.Helper()
		var stored string
		if err := db.NewRaw(`SELECT (metadata->'manual_matching_override')::text FROM local_track WHERE id=?`, localTrackID).Scan(ctx, &stored); err != nil {
			t.Fatalf("read nested manual override: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(stored), &got); err != nil {
			t.Fatalf("decode nested manual override %s: %v", stored, err)
		}
		if got["title_override"] != wantTitle || !equalJSONForTest(t, got["artist_overrides"], wantArtists) {
			t.Fatalf("nested manual override = %s, want title=%#v artists=%#v", stored, wantTitle, wantArtists)
		}
	}
	firstArtists := []string{"Artist one", "Artist two"}
	confirm(ptrString("First title"), nil)
	confirm(nil, &firstArtists)
	confirm(ptrString("Changed title"), nil)
	assertOverride("Changed title", []any{"Artist one", "Artist two"})
	// An explicitly supplied empty title is a value, not an omission, and must
	// update just that key without discarding the previously supplied artists.
	confirm(ptrString(""), nil)
	assertOverride("", []any{"Artist one", "Artist two"})
}

func assertManualOverrideValues(t *testing.T, ctx context.Context, db *bun.DB, trackID uuid.UUID, expectedTitle string, expectedArtists []string) {
	t.Helper()
	var title string
	if err := db.NewRaw(`SELECT title FROM local_track WHERE id=?`, trackID).Scan(ctx, &title); err != nil {
		t.Fatal(err)
	}
	if title != expectedTitle {
		t.Fatalf("local title = %q, want %q", title, expectedTitle)
	}
	var artists []string
	if err := db.NewRaw(`SELECT name FROM track_artist WHERE track_id=? ORDER BY position`, trackID).Scan(ctx, &artists); err != nil {
		t.Fatal(err)
	}
	if len(artists) != len(expectedArtists) {
		t.Fatalf("local artists = %#v, want %#v", artists, expectedArtists)
	}
	for i := range artists {
		if artists[i] != expectedArtists[i] {
			t.Fatalf("local artists = %#v, want %#v", artists, expectedArtists)
		}
	}
}

func ptrString(value string) *string { return &value }

func manualReceiptAssignment(preview persistence.MatchingManualPreview, memberID uuid.UUID) *persistence.MatchingManualAssignment {
	for _, member := range preview.Members {
		if member.FileMemberID == memberID {
			return member.Assignment
		}
	}
	return nil
}
