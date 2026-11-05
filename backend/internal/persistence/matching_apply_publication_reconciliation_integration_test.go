//go:build integration

package persistence_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
	"testing"
)

func assertNoAutomaticMatchingReconciliation(t *testing.T, ctx context.Context, db *bun.DB) {
	t.Helper()
	var requests, dirty, events, operations int
	for _, item := range []struct {
		query  string
		target *int
	}{
		{`SELECT count(*) FROM publication_reconciliation_request`, &requests},
		{`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE scope='automatic_matching'`, &dirty},
		{`SELECT count(*) FROM publication_reconciliation_event`, &events},
		{`SELECT count(*) FROM operation WHERE kind=?`, &operations},
	} {
		var err error
		if item.target == &operations {
			err = db.NewRaw(item.query, persistence.PublicationReconciliationOperationKind).Scan(ctx, item.target)
		} else {
			err = db.NewRaw(item.query).Scan(ctx, item.target)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if requests != 0 || dirty != 0 || events != 0 || operations != 0 {
		t.Fatalf("unexpected reconciliation rows requests=%d dirty=%d events=%d operations=%d", requests, dirty, events, operations)
	}
}

func assertAutomaticMatchingReconciliation(t *testing.T, ctx context.Context, db *bun.DB, fixture *incomingMatchFixture) ([]uuid.UUID, uuid.UUID) {
	t.Helper()
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	var expected, actual []uuid.UUID
	if err := db.NewRaw(`SELECT position.id FROM release_track position JOIN provider_local_release mapping ON mapping.local_release_id=position.release_id WHERE mapping.provider_id=? AND mapping.provider_release_id=? ORDER BY position.id`, fixture.provider, fixture.releaseID).Scan(ctx, &expected); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT position_id FROM publication_reconciliation_request WHERE output_epoch=? ORDER BY position_id`, epoch).Scan(ctx, &actual); err != nil {
		t.Fatal(err)
	}
	if len(expected) == 0 || len(actual) != len(expected) {
		t.Fatalf("reconciliation positions=%v, materialized positions=%v", actual, expected)
	}
	for i := range expected {
		if expected[i] != actual[i] {
			t.Fatalf("reconciliation positions=%v, want %v", actual, expected)
		}
	}
	var badScopes, pending, operationCount int
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND scope='automatic_matching'`, epoch).Scan(ctx, &badScopes); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE output_epoch=? AND state='pending'`, epoch).Scan(ctx, &pending); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE kind=?`, persistence.PublicationReconciliationOperationKind).Scan(ctx, &operationCount); err != nil {
		t.Fatal(err)
	}
	if badScopes != len(expected) || pending != len(expected) || operationCount != len(expected) {
		t.Fatalf("scope/pending/event operations=%d/%d/%d, want %d each", badScopes, pending, operationCount, len(expected))
	}
	var mismatched int
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request request LEFT JOIN publication_reconciliation_dirty_scope scope ON scope.output_epoch=request.output_epoch AND scope.position_id=request.position_id AND scope.scope='automatic_matching' AND scope.generation=request.requested_generation WHERE request.output_epoch=? AND scope.position_id IS NULL`, epoch).Scan(ctx, &mismatched); err != nil {
		t.Fatal(err)
	}
	if mismatched != 0 {
		t.Fatalf("%d requests lack current automatic_matching dirty scope", mismatched)
	}
	return expected, epoch
}
