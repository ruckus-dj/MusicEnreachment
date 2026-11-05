//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"errors"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"testing"
)

type failingMatchingAcquisitionInserter struct{ persistence.RiverInserter }

func (failingMatchingAcquisitionInserter) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("injected catalog wake insert failure")
}

func assertCatalogWakeRollback(t *testing.T, fixture *matchingAcquisitionGuardFixture) {
	t.Helper()
	snapshot, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, fixture.parentID)
	if err != nil || !exists || snapshot.Revision != 1 || snapshot.Phase != persistence.MatchingAcquisitionArtistCatalogPage {
		t.Fatalf("failed enqueue changed acquisition snapshot=%+v exists=%t err=%v", snapshot, exists, err)
	}
	var consumed bool
	if err := fixture.db.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, fixture.parentID, fixture.parentJobID).Scan(fixture.ctx, &consumed); err != nil || consumed {
		t.Fatalf("failed enqueue consumed current delivery=%t err=%v", consumed, err)
	}
	var pending int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL`, fixture.parentID).Scan(fixture.ctx, &pending); err != nil || pending != 1 {
		t.Fatalf("failed enqueue pending registrations=%d err=%v; want only current delivery", pending, err)
	}
	var stage string
	if err := fixture.db.NewRaw(`SELECT stage FROM operation WHERE id=?`, fixture.parentID).Scan(fixture.ctx, &stage); err != nil || stage == "ranking_pending" {
		t.Fatalf("failed enqueue changed parent stage to %q err=%v", stage, err)
	}
}
