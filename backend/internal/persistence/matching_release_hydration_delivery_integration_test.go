//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type failingSecondHydrationInsert struct {
	persistence.RiverInserter
	inserts int
}

func (inserter *failingSecondHydrationInsert) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	inserter.inserts++
	if inserter.inserts == 2 {
		return nil, errors.New("injected second hydration enqueue failure")
	}
	return inserter.RiverInserter.InsertTx(ctx, tx, args, options)
}

func TestMatchingReleaseHydrationConsumesClaimedRankingDeliveryAtomicallyPostgreSQL(t *testing.T) {
	f := newMatchingAcquisitionGuardFixture(t)
	f.matchingAcquisitionCatalogReady()

	var wakeID int64
	if err := f.db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL ORDER BY id DESC LIMIT 1`, f.parentID).Scan(f.ctx, &wakeID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, wakeID).Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
	claim, outcome, err := f.repository.ClaimMatchingDelivery(f.ctx, f.parentID, f.parent.Attempt, wakeID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim catalog-ranking wake: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}
	ids := []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}
	phase, children := matchingHydrationRequest(f, 3, ids)
	before := matchingAcquisitionGuardState(t, f)
	inserter := &failingSecondHydrationInsert{RiverInserter: f.river}
	changed, err := f.repository.StartMatchingReleaseHydrationForDelivery(f.ctx, inserter, f.parentID, f.parent.Attempt, wakeID, claim, 3, phase, children)
	if err == nil || changed || inserter.inserts != 2 {
		t.Fatalf("partial hydration enqueue changed=%v inserts=%d err=%v", changed, inserter.inserts, err)
	}
	if after := matchingAcquisitionGuardState(t, f); before != after {
		t.Fatalf("failed hydration changed durable state: before=%+v after=%+v", before, after)
	}
	var consumed bool
	if err := f.db.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=? AND operation_attempt=?`, f.parentID, wakeID, f.parent.Attempt).Scan(f.ctx, &consumed); err != nil || consumed {
		t.Fatalf("failed enqueue consumed ranking delivery=%t err=%v", consumed, err)
	}
	changed, err = f.repository.StartMatchingReleaseHydrationForDelivery(f.ctx, f.river, f.parentID, f.parent.Attempt, wakeID, claim, 3, phase, children)
	if err != nil || !changed {
		t.Fatalf("retry hydration admission: changed=%v err=%v", changed, err)
	}
	var pending, deps int
	if err := f.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL`, f.parentID).Scan(f.ctx, &pending); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=? AND request_id IN (?,?)`, f.parentID, children[0].Operation.ID, children[1].Operation.ID).Scan(f.ctx, &deps); err != nil {
		t.Fatal(err)
	}
	if pending != 0 || deps != len(children) {
		t.Fatalf("hydration durable state: pending deliveries=%d dependencies=%d", pending, deps)
	}
	var state string
	if err := f.db.NewRaw(`SELECT state FROM matching_run WHERE id=?`, f.parentID).Scan(f.ctx, &state); err != nil || state != "waiting_provider" {
		t.Fatalf("run state=%q err=%v; want waiting_provider", state, err)
	}
	if changed, err := f.repository.StartMatchingReleaseHydrationForDelivery(f.ctx, f.river, f.parentID, f.parent.Attempt, wakeID, claim, 3, phase, children); err != nil || changed {
		t.Fatalf("replay consumed ranking delivery changed=%v err=%v", changed, err)
	}
}
