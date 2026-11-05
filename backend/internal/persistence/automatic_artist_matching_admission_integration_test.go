//go:build integration

package persistence_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"testing"
)

type automaticArtistAdmissionSnapshot struct {
	receipts, runs, targets, dependencies, parentOperations, providerOperations, parentJobs, providerJobs int
	generation                                                                                            int64
}

func automaticArtistAdmissionCounts(t *testing.T, ctx context.Context, db *bun.DB, groupID uuid.UUID) automaticArtistAdmissionSnapshot {
	t.Helper()
	var counts automaticArtistAdmissionSnapshot
	queries := []struct {
		query string
		dest  any
	}{
		{`SELECT count(*) FROM incoming_matching_automatic_admission`, &counts.receipts},
		{`SELECT count(*) FROM matching_run_target WHERE group_id=?`, &counts.targets},
		{`SELECT count(*) FROM matching_run mr JOIN matching_run_target target ON target.run_id=mr.id WHERE target.group_id=?`, &counts.runs},
		{`SELECT count(*) FROM matching_provider_dependency dependency JOIN matching_run_target target ON target.run_id=dependency.parent_run_id WHERE target.group_id=?`, &counts.dependencies},
		{`SELECT count(*) FROM operation WHERE kind='matching_evaluation_v1'`, &counts.parentOperations},
		{`SELECT count(*) FROM operation WHERE kind='provider_artist_search'`, &counts.providerOperations},
		{`SELECT count(*) FROM river_job WHERE kind='matching_evaluation_v1'`, &counts.parentJobs},
		{`SELECT count(*) FROM river_job WHERE kind='provider_fetch_v1'`, &counts.providerJobs},
	}
	for _, item := range queries {
		var err error
		if item.query != `SELECT count(*) FROM incoming_matching_automatic_admission` {
			err = db.NewRaw(item.query, groupID).Scan(ctx, item.dest)
		} else {
			err = db.NewRaw(item.query).Scan(ctx, item.dest)
		}
		if err != nil {
			t.Fatalf("read automatic admission state: %v", err)
		}
	}
	if err := db.NewRaw(`SELECT COALESCE(high_water,0) FROM incoming_matching_generation WHERE group_id=?`, groupID).Scan(ctx, &counts.generation); err != nil {
		t.Fatalf("read automatic matching target generation: %v", err)
	}
	return counts
}

func assertAutomaticArtistAdmissionCounts(t *testing.T, ctx context.Context, db *bun.DB, groupID uuid.UUID, expected automaticArtistAdmissionSnapshot) {
	t.Helper()
	actual := automaticArtistAdmissionCounts(t, ctx, db, groupID)
	if actual != expected {
		t.Fatalf("automatic admission state changed: got %+v, want %+v", actual, expected)
	}
}
