//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestMatchingLocalConfirmationRequestsPublicationReconciliationPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	intent := persistence.MatchingLocalIntent{
		ExpectedGroupRevision:     fixture.group.Revision,
		ExpectedAuthorityRevision: 0,
		ReleaseTitle:              "Operator release",
		ReleaseArtists:            []persistence.MatchingLocalArtist{{Name: "Operator artist"}},
		Tracks: []persistence.MatchingLocalTrack{
			{Medium: 1, Position: 1, DisplayedNumber: "A1", Title: "First", FileMemberIDs: []uuid.UUID{fixture.memberA}},
			{Medium: 1, Position: 2, DisplayedNumber: "A2", Title: "Second", FileMemberIDs: []uuid.UUID{fixture.memberB}},
		},
	}
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	preview, err := repository.PreviewMatchingLocal(ctx, fixture.group.ID, intent)
	if err != nil {
		t.Fatalf("preview local release: %v", err)
	}
	var requestsBefore int
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request`).Scan(ctx, &requestsBefore); err != nil {
		t.Fatal(err)
	}
	if requestsBefore != 0 {
		t.Fatalf("fixture unexpectedly contains %d publication requests", requestsBefore)
	}
	if _, err := repository.PreviewMatchingLocal(ctx, fixture.group.ID, intent); err != nil {
		t.Fatalf("repeat preview: %v", err)
	}
	var afterPreview int
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request`).Scan(ctx, &afterPreview); err != nil || afterPreview != requestsBefore {
		t.Fatalf("preview requests = %d, before=%d, err=%v", afterPreview, requestsBefore, err)
	}
	var releasesBefore int
	if err := db.NewRaw(`SELECT count(*) FROM local_release`).Scan(ctx, &releasesBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION test_matching_local_publication_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced receipt failure'; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER test_matching_local_publication_fail BEFORE INSERT ON local_matching_receipt FOR EACH ROW EXECUTE FUNCTION test_matching_local_publication_fail()`); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ConfirmMatchingLocal(ctx, fixture.group.ID, intent, preview.PreviewDigest, uuid.New()); err == nil {
		t.Fatal("injected receipt failure unexpectedly committed")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER test_matching_local_publication_fail ON local_matching_receipt`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DROP FUNCTION test_matching_local_publication_fail()`); err != nil {
		t.Fatal(err)
	}
	var afterRollback int
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request`).Scan(ctx, &afterRollback); err != nil || afterRollback != requestsBefore {
		t.Fatalf("rollback requests = %d, before=%d, err=%v", afterRollback, requestsBefore, err)
	}
	var releasesAfter int
	if err := db.NewRaw(`SELECT count(*) FROM local_release`).Scan(ctx, &releasesAfter); err != nil || releasesAfter != releasesBefore {
		t.Fatalf("receipt failure retained domain changes: releases=%d before=%d err=%v", releasesAfter, releasesBefore, err)
	}
	requestID := uuid.New()
	confirmed, err := repository.ConfirmMatchingLocal(ctx, fixture.group.ID, intent, preview.PreviewDigest, requestID)
	if err != nil {
		t.Fatalf("confirmation: %v", err)
	}
	if confirmed.LocalReleaseID == nil {
		t.Fatal("confirmation returned no local release")
	}
	releaseID := *confirmed.LocalReleaseID
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	var expected, actual []uuid.UUID
	if err := db.NewRaw(`SELECT id FROM release_track WHERE release_id=? ORDER BY id`, releaseID).Scan(ctx, &expected); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT position_id FROM publication_reconciliation_request WHERE output_epoch=? ORDER BY position_id`, epoch).Scan(ctx, &actual); err != nil {
		t.Fatal(err)
	}
	if len(expected) != 2 || len(actual) != len(expected) {
		t.Fatalf("requested publication positions = %v, release positions = %v", actual, expected)
	}
	for i := range expected {
		if expected[i] != actual[i] {
			t.Fatalf("requested publication positions = %v, want release positions %v", actual, expected)
		}
	}
	var generation int64
	if err := db.NewRaw(`SELECT sum(requested_generation) FROM publication_reconciliation_request WHERE output_epoch=?`, epoch).Scan(ctx, &generation); err != nil {
		t.Fatal(err)
	}
	if generation != int64(len(expected)) {
		t.Fatalf("requested generations total = %d, want %d", generation, len(expected))
	}
	var dirtyScopes int
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope s JOIN publication_reconciliation_request r USING(output_epoch,position_id) WHERE s.output_epoch=? AND s.scope='local_confirmation' AND s.generation=r.requested_generation`, epoch).Scan(ctx, &dirtyScopes); err != nil || dirtyScopes != len(expected) {
		t.Fatalf("local confirmation dirty scopes=%d want=%d err=%v", dirtyScopes, len(expected), err)
	}
	if _, err := repository.ConfirmMatchingLocal(ctx, fixture.group.ID, intent, preview.PreviewDigest, requestID); err != nil {
		t.Fatalf("receipt replay: %v", err)
	}
	var replayGeneration int64
	if err := db.NewRaw(`SELECT sum(requested_generation) FROM publication_reconciliation_request WHERE output_epoch=?`, epoch).Scan(ctx, &replayGeneration); err != nil {
		t.Fatal(err)
	}
	if replayGeneration != generation {
		t.Fatalf("replay changed request generations: %d -> %d", generation, replayGeneration)
	}
	var requestsAfter int
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request`).Scan(ctx, &requestsAfter); err != nil {
		t.Fatal(err)
	}
	if requestsAfter != requestsBefore+len(expected) {
		t.Fatalf("preview or replay added requests: before=%d after=%d", requestsBefore, requestsAfter)
	}
}
