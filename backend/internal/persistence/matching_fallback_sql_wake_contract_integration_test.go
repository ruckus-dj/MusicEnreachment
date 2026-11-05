//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

func TestFallbackEmptyPlanWakeRollbackAndReplaySQLPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := seedFallbackSQLRun(t, false)
	client := openScanEnqueueRiver(t, fixture.db)
	inserter := &fallbackHandoffRiverInserter{client: client, failWake: true}

	var parentJobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, fixture.runID).Scan(fixture.ctx, &parentJobID); err != nil {
		t.Fatalf("read admitted matching River job: %v", err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=? AND state='available'`, parentJobID).Exec(fixture.ctx); err != nil {
		t.Fatalf("claim admitted matching River job for repository contract: %v", err)
	}
	claim, outcome, err := fixture.repository.ClaimMatchingDelivery(fixture.ctx, fixture.runID, 1, parentJobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim admitted matching delivery: outcome=%v err=%v", outcome, err)
	}

	before := fallbackWakeContractState(t, fixture.runID, fixture.ctx, fixture.db)
	changed, err := fixture.repository.CompleteEmptyFallbackPlanAndContinueForDelivery(
		fixture.ctx, inserter, fixture.runID, 1, parentJobID, claim, 0,
	)
	if err == nil || changed {
		t.Fatalf("expected empty-plan continuation to roll back on injected wake failure: changed=%v err=%v", changed, err)
	}
	if inserter.wakeCalls != 1 {
		t.Fatalf("failed continuation attempted %d wakes, want 1", inserter.wakeCalls)
	}
	afterFailure := fallbackWakeContractState(t, fixture.runID, fixture.ctx, fixture.db)
	if before != afterFailure {
		t.Fatalf("failed continuation did not roll back durable state: before=%+v after=%+v", before, afterFailure)
	}

	inserter.failWake = false
	changed, err = fixture.repository.CompleteEmptyFallbackPlanAndContinueForDelivery(
		fixture.ctx, inserter, fixture.runID, 1, parentJobID, claim, 0,
	)
	if err != nil || !changed {
		t.Fatalf("retry empty-plan continuation: changed=%v err=%v", changed, err)
	}
	if inserter.wakeCalls != 2 {
		t.Fatalf("retry attempted %d total wakes, want 2", inserter.wakeCalls)
	}
	var original persistence.MatchingFallbackEvaluationHandoff
	if err := fixture.db.NewRaw(`SELECT provider_authority_version,run_id,wait_revision,encode(seal_digest,'hex') AS seal_digest,river_job_id,operation_attempt
		FROM matching_fallback_evaluation_handoff WHERE run_id=?`, fixture.runID).Scan(fixture.ctx, &original); err != nil {
		t.Fatalf("read committed fallback handoff: %v", err)
	}
	if original.RiverJobID < 1 || original.WaitRevision < 1 || original.OperationAttempt != 1 {
		t.Fatalf("committed handoff is incomplete: %+v", original)
	}

	replayed, found, err := fixture.repository.ContinueMatchingFallbackSeal(
		fixture.ctx, inserter, fixture.runID, 1, original.WaitRevision,
	)
	if err != nil || !found {
		t.Fatalf("replay committed continuation: found=%v err=%v", found, err)
	}
	if replayed != original {
		t.Fatalf("replay changed handoff identity: original=%+v replayed=%+v", original, replayed)
	}
	if inserter.wakeCalls != 2 {
		t.Fatalf("exact replay inserted another matching wake: calls=%d", inserter.wakeCalls)
	}
	afterReplay := fallbackWakeContractState(t, fixture.runID, fixture.ctx, fixture.db)
	if afterReplay.HandoffCount != 1 || afterReplay.SealCount != 1 || afterReplay.WakeCount != before.WakeCount+1 {
		t.Fatalf("replay did not retain exactly one continuation wake: before=%+v after=%+v", before, afterReplay)
	}
}

type fallbackWakeContractSnapshot struct {
	Phase        string
	Revision     int64
	Operation    string
	Stage        string
	SealCount    int
	HandoffCount int
	WakeCount    int
}

func fallbackWakeContractState(t *testing.T, runID uuid.UUID, ctx context.Context, db *bun.DB) fallbackWakeContractSnapshot {
	t.Helper()
	var state fallbackWakeContractSnapshot
	if err := db.NewRaw(`SELECT current_phase,revision FROM matching_acquisition WHERE run_id=?`, runID).
		Scan(ctx, &state.Phase, &state.Revision); err != nil {
		t.Fatalf("read fallback acquisition cursor: %v", err)
	}
	if err := db.NewRaw(`SELECT state,stage FROM operation WHERE id=?`, runID).Scan(ctx, &state.Operation, &state.Stage); err != nil {
		t.Fatalf("read matching operation state: %v", err)
	}
	queries := []struct {
		query string
		count *int
	}{
		{`SELECT count(*) FROM matching_fallback_authority_seal WHERE run_id=?`, &state.SealCount},
		{`SELECT count(*) FROM matching_fallback_evaluation_handoff WHERE run_id=?`, &state.HandoffCount},
		{`SELECT count(*) FROM river_job WHERE args->>'operation_id'=?`, &state.WakeCount},
	}
	for _, item := range queries {
		if err := db.NewRaw(item.query, runID.String()).Scan(ctx, item.count); err != nil {
			t.Fatalf("read fallback continuation state: %v", err)
		}
	}
	return state
}
