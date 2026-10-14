//go:build integration

package persistence_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/uptrace/bun"
)

func TestAutomaticFallbackProofRetirementPostgreSQL(t *testing.T) {
	callbackExecuted := false
	testAutomaticFallbackFiniteFlowWithPublicationCallback(t, false, false, false, false, false, func(f uniquePublicationApplyFixture) {
		callbackExecuted = true
		ctx, db, repository := f.Context, f.Database, f.Repository
		parentID, childID := f.ParentOperationID, f.ChildOperation.ID
		groupID := f.InputSnapshot.Authority.GroupID
		if f.InputSnapshot.SchemaVersion != 2 || f.InputSnapshot.Authority.ParentRunID != parentID {
			t.Fatalf("callback did not expose original schema-2 child snapshot: %+v", f.InputSnapshot)
		}

		completeFallbackParentDelivery(t, ctx, db, parentID)
		jobID := *f.ChildOperation.RiverJobID
		parentPast := time.Now().UTC().Add(-96 * time.Hour)
		childPast := time.Now().UTC().Add(-48 * time.Hour)
		if _, err := db.NewRaw(`UPDATE operation SET finished_at=?,updated_at=? WHERE id=?`, parentPast, parentPast, parentID).Exec(ctx); err != nil {
			t.Fatalf("age completed parent: %v", err)
		}
		// An old parent remains held while its successful evaluation is current.
		// Derive the lifecycle baseline once, before any cleanup, then require it
		// to stay unchanged for as long as the run is held. The apply receipt is
		// absent until the schema-2 child actually executes.
		retained := readFallbackRetirementCounts(t, ctx, db, parentID, childID)
		assertFallbackRetirementBaseline(t, retained, false)
		if err := repository.DeleteSucceededBefore(ctx, time.Now().UTC().Add(-24*time.Hour)); err != nil {
			t.Fatalf("cleanup while current header holds the graph: %v", err)
		}
		assertFallbackRetirementHeld(t, ctx, db, parentID, childID, retained)

		// Both authorization and immutable receipt guards are live database guards.
		if _, err := db.NewRaw(`DELETE FROM matching_fallback_authority_seal WHERE run_id=?`, parentID).Exec(ctx); err == nil || !strings.Contains(err.Error(), "write-once") {
			t.Fatalf("unauthorized proof delete error=%v, want write-once trigger rejection", err)
		}
		if _, err := db.NewRaw(`UPDATE matching_fallback_authority_seal SET authority=authority WHERE run_id=?`, parentID).Exec(ctx); err == nil || !strings.Contains(err.Error(), "write-once") {
			t.Fatalf("proof update error=%v, want immutable trigger rejection", err)
		}
		if _, err := db.NewRaw(`INSERT INTO matching_run_retirement_authorization(run_id) VALUES (?)`, parentID).Exec(ctx); err == nil || !strings.Contains(err.Error(), "matching run is not eligible for proof retirement") {
			t.Fatalf("current-header authorization error=%v, want database rejection", err)
		}

		var highWaterBefore, manualRevisionBefore int64
		if err := db.NewRaw(`SELECT high_water FROM incoming_matching_generation WHERE group_id=?`, groupID).Scan(ctx, &highWaterBefore); err != nil {
			t.Fatalf("read generation high-water: %v", err)
		}
		if err := db.NewRaw(`SELECT COALESCE((SELECT revision FROM incoming_group_matching_authority WHERE group_id=?),0)`, groupID).Scan(ctx, &manualRevisionBefore); err != nil {
			t.Fatalf("read manual matching authority revision: %v", err)
		}
		if _, err := db.NewRaw(`DELETE FROM incoming_match_evaluation WHERE group_id=?`, groupID).Exec(ctx); err != nil {
			t.Fatalf("remove current evaluation header: %v", err)
		}
		// With no current header, the current generation owner and queued child are
		// independent holds; neither can be treated as history.
		if err := repository.DeleteSucceededBefore(ctx, time.Now().UTC().Add(-24*time.Hour)); err != nil {
			t.Fatalf("cleanup with current generation owner: %v", err)
		}
		assertFallbackRetirementHeld(t, ctx, db, parentID, childID, retained)
		if _, err := db.NewRaw(`UPDATE incoming_matching_generation SET current_run_id=NULL WHERE group_id=? AND current_run_id=?`, groupID, parentID).Exec(ctx); err != nil {
			t.Fatalf("release current generation ownership: %v", err)
		}
		if err := repository.DeleteSucceededBefore(ctx, time.Now().UTC().Add(-24*time.Hour)); err != nil {
			t.Fatalf("cleanup with queued schema-2 child: %v", err)
		}
		assertFallbackRetirementHeld(t, ctx, db, parentID, childID, retained)
		if _, err := db.NewRaw(`INSERT INTO matching_run_retirement_authorization(run_id) VALUES (?)`, parentID).Exec(ctx); err == nil || !strings.Contains(err.Error(), "matching run is not eligible for proof retirement") {
			t.Fatalf("queued-child authorization error=%v, want database rejection", err)
		}

		// Complete the actual schema-2 child through its production worker boundary,
		// using its persisted River job identity rather than forging a terminal row.
		if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
			t.Fatalf("claim schema-2 apply job: %v", err)
		}
		worker := jobs.NewMatchingApplyWorker(repository, service.NewOperations(repository))
		job := &river.Job[persistence.MatchingApplyOperationArgs]{JobRow: &rivertype.JobRow{ID: jobID, Attempt: 1}, Args: persistence.MatchingApplyOperationArgs{OperationID: childID}}
		if err := worker.Work(ctx, job); err != nil {
			t.Fatalf("run schema-2 apply worker: %v", err)
		}
		var childState string
		var childSnapshot []byte
		var applyReceipts int
		if err := db.NewRaw(`SELECT state,input_snapshot FROM operation WHERE id=?`, childID).Scan(ctx, &childState, &childSnapshot); err != nil || childState != "succeeded" {
			t.Fatalf("schema-2 child state=%q err=%v, want succeeded", childState, err)
		}
		if !bytes.Equal(childSnapshot, f.ChildOperation.InputSnapshot) {
			t.Fatal("completed child no longer retains its original immutable snapshot")
		}
		if err := db.NewRaw(`SELECT count(*) FROM matching_apply_receipt WHERE apply_operation_id=?`, childID).Scan(ctx, &applyReceipts); err != nil || applyReceipts != 1 {
			t.Fatalf("schema-2 apply receipt count=%d err=%v, want one persisted receipt", applyReceipts, err)
		}
		retained = readFallbackRetirementCounts(t, ctx, db, parentID, childID)
		assertFallbackRetirementBaseline(t, retained, true)
		if _, err := db.NewRaw(`UPDATE operation SET finished_at=?,updated_at=? WHERE id=?`, childPast, childPast, childID).Exec(ctx); err != nil {
			t.Fatalf("age completed child: %v", err)
		}
		if _, err := db.NewRaw(`UPDATE river_job SET state='completed',finalized_at=? WHERE id=?`, childPast, jobID).Exec(ctx); err != nil {
			t.Fatalf("age completed child job: %v", err)
		}
		ageFallbackProviderDependencies(t, ctx, db, parentID, childPast)

		// A still-recent parent is never eligible, even without a current header.
		recent := time.Now().UTC()
		if _, err := db.NewRaw(`UPDATE operation SET finished_at=?,updated_at=? WHERE id=?`, recent, recent, parentID).Exec(ctx); err != nil {
			t.Fatalf("make parent recently finished: %v", err)
		}
		if _, err := db.NewRaw(`INSERT INTO matching_run_retirement_authorization(run_id,authorized_at) VALUES (?,?)`, parentID, time.Now().UTC().Add(time.Hour)).Exec(ctx); err == nil || !strings.Contains(err.Error(), "timestamp must equal transaction_timestamp()") {
			t.Fatalf("future authorization error=%v, want transaction-time guard", err)
		}
		if _, err := db.NewRaw(`INSERT INTO matching_run_retirement_authorization(run_id) VALUES (?)`, parentID).Exec(ctx); err == nil || !strings.Contains(err.Error(), "matching run is not eligible for proof retirement") {
			t.Fatalf("recent-parent authorization error=%v, want eligibility rejection", err)
		}
		if _, err := db.NewRaw(`UPDATE operation SET finished_at=?,updated_at=? WHERE id=?`, parentPast, parentPast, parentID).Exec(ctx); err != nil {
			t.Fatalf("restore expired parent finish time: %v", err)
		}
		if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,created_at,updated_at,finished_at)
			VALUES (?, 'move_tools_root', 'succeeded', 'complete', '{}'::jsonb, 1, ?, ?, ?)`, uuid.New(), parentPast, parentPast, parentPast).Exec(ctx); err != nil {
			t.Fatalf("create unrelated expired successful operation: %v", err)
		}
		// The caller's stricter 72-hour cutoff applies to every operation cleanup.
		// A 48-hour-old child therefore holds this proof, while the unrelated
		// 96-hour-old success is still cleaned in its own eligible path.
		if err := repository.DeleteSucceededBefore(ctx, time.Now().UTC().Add(-72*time.Hour)); err != nil {
			t.Fatalf("cleanup with caller cutoff stricter than child age: %v", err)
		}
		assertFallbackRetirementHeld(t, ctx, db, parentID, childID, retained)
		assertFallbackRetirementGraph(t, ctx, db, parentID, childID, true)
		var unrelated int
		if err := db.NewRaw(`SELECT count(*) FROM operation WHERE kind='move_tools_root' AND finished_at=?`, parentPast).Scan(ctx, &unrelated); err != nil || unrelated != 0 {
			t.Fatalf("unrelated expired operation count=%d err=%v, want cleanup despite held graph", unrelated, err)
		}
		if err := repository.DeleteSucceededBefore(ctx, time.Now().UTC().Add(-24*time.Hour)); err != nil {
			t.Fatalf("retire expired proof graph after existing cutoff: %v", err)
		}
		assertFallbackRetirementGraph(t, ctx, db, parentID, childID, false)
		var highWaterAfter, manualRevisionAfter int64
		if err := db.NewRaw(`SELECT high_water FROM incoming_matching_generation WHERE group_id=?`, groupID).Scan(ctx, &highWaterAfter); err != nil || highWaterAfter != highWaterBefore {
			t.Fatalf("retirement changed generation high-water: before=%d after=%d err=%v", highWaterBefore, highWaterAfter, err)
		}
		if err := db.NewRaw(`SELECT COALESCE((SELECT revision FROM incoming_group_matching_authority WHERE group_id=?),0)`, groupID).Scan(ctx, &manualRevisionAfter); err != nil || manualRevisionAfter != manualRevisionBefore {
			t.Fatalf("retirement changed manual matching authority revision: before=%d after=%d err=%v", manualRevisionBefore, manualRevisionAfter, err)
		}
		var replayLinks, replayJobs int
		if err := db.NewRaw(`SELECT count(*) FROM matching_apply_request WHERE parent_run_id=?`, parentID).Scan(ctx, &replayLinks); err != nil {
			t.Fatal(err)
		}
		if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='matching_apply_v1' AND args->>'operation_id'=?`, childID.String()).Scan(ctx, &replayJobs); err != nil {
			t.Fatal(err)
		}
		if replayLinks != 0 || replayJobs != 1 {
			t.Fatalf("pruning/replay altered durable child job authority: apply links=%d historical jobs=%d", replayLinks, replayJobs)
		}
		assertRetiredOperationsMissing(t, ctx, repository, parentID, childID)
	}, true)
	if !callbackExecuted {
		t.Fatal("automatic unique-publication callback did not execute")
	}
}

// The fixture drives the production worker directly; finish its successfully
// processed River delivery before exercising retirement eligibility.
func completeFallbackParentDelivery(t *testing.T, ctx context.Context, db *bun.DB, runID uuid.UUID) {
	t.Helper()
	result, err := db.NewRaw(`UPDATE river_job SET state='completed',finalized_at=transaction_timestamp()
		WHERE id=(SELECT river_job_id FROM operation WHERE id=(SELECT operation_id FROM matching_run WHERE id=?))`, runID).Exec(ctx)
	if err != nil {
		t.Fatalf("complete successfully processed fallback parent River job: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("complete fallback parent River job rows=%d err=%v, want one", affected, err)
	}
	result, err = db.NewRaw(`UPDATE matching_delivery SET consumed_at=transaction_timestamp() WHERE run_id=? AND consumed_at IS NULL`, runID).Exec(ctx)
	if err != nil {
		t.Fatalf("consume successfully processed fallback parent deliveries: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		t.Fatalf("consume fallback parent deliveries rows=%d err=%v, want at least one", affected, err)
	}
}

func fallbackRetirementEligibilityFacts(t *testing.T, ctx context.Context, db *bun.DB, runID uuid.UUID, cutoff time.Time) []byte {
	t.Helper()
	var facts []byte
	query := `SELECT jsonb_build_object(
		'parent_ready', EXISTS (SELECT 1 FROM matching_run run JOIN operation parent ON parent.id=run.operation_id WHERE run.id=? AND parent.kind='matching_evaluation_v1' AND parent.state='succeeded' AND parent.finished_at < ?),
		'acquisition_ready', EXISTS (SELECT 1 FROM matching_acquisition WHERE run_id=? AND intent_version=3),
		'run_succeeded', EXISTS (SELECT 1 FROM matching_run WHERE id=? AND state='succeeded'),
		'current_generation_owner', EXISTS (SELECT 1 FROM incoming_matching_generation WHERE current_run_id=?),
		'live_evaluation', EXISTS (SELECT 1 FROM incoming_match_evaluation WHERE target_run_id=? OR frozen_run_id=?),
		'live_apply', EXISTS (SELECT 1 FROM matching_apply_request request JOIN operation child ON child.id=request.apply_operation_id LEFT JOIN river_job job ON job.id=child.river_job_id WHERE request.parent_run_id=? AND (child.state <> 'succeeded' OR child.finished_at IS NULL OR child.finished_at >= ? OR job.state IN ('available','pending','running','retryable','scheduled'))),
		'live_dependency', EXISTS (SELECT 1 FROM matching_provider_dependency dependency JOIN operation child ON child.id=dependency.request_id LEFT JOIN river_job job ON job.id=child.river_job_id WHERE dependency.parent_run_id=? AND (dependency.terminal_outcome IS NULL OR child.state NOT IN ('succeeded','failed') OR child.finished_at IS NULL OR child.finished_at >= ? OR child.state IN ('queued','running') OR job.state IN ('available','pending','running','retryable','scheduled'))),
		'live_parent_job', EXISTS (SELECT 1 FROM matching_run run JOIN operation parent ON parent.id=run.operation_id JOIN river_job job ON job.id=parent.river_job_id WHERE run.id=? AND job.state IN ('available','pending','running','retryable','scheduled')),
		'unconsumed_delivery', EXISTS (SELECT 1 FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL))`
	if err := db.NewRaw(query, runID, cutoff, runID, runID, runID, runID, runID, runID, cutoff, runID, cutoff, runID, runID).Scan(ctx, &facts); err != nil {
		t.Fatalf("read fallback retirement eligibility facts: %v", err)
	}
	return facts
}

func ageFallbackProviderDependencies(t *testing.T, ctx context.Context, db *bun.DB, parentID uuid.UUID, finishedAt time.Time) {
	t.Helper()
	if _, err := db.NewRaw(`UPDATE operation SET finished_at=?,updated_at=? WHERE id IN
		(SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=?)`, finishedAt, finishedAt, parentID).Exec(ctx); err != nil {
		t.Fatalf("age terminal provider dependency operations: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE river_job job SET state='completed',finalized_at=? FROM operation child
		JOIN matching_provider_dependency dependency ON dependency.request_id=child.id
		WHERE dependency.parent_run_id=? AND job.id=child.river_job_id`, finishedAt, parentID).Exec(ctx); err != nil {
		t.Fatalf("age terminal provider dependency jobs: %v", err)
	}
}

func assertFallbackRetirementGraph(t *testing.T, ctx context.Context, db interface {
	NewRaw(string, ...any) *bun.RawQuery
}, parentID, childID uuid.UUID, retained bool) {
	t.Helper()
	counts := readFallbackRetirementCounts(t, ctx, db, parentID, childID)
	for name, count := range counts {
		if !retained && count != 0 {
			t.Fatalf("retirement closure %s count=%d retained=%v, want zero", name, count, retained)
		}
	}
	if retained {
		for _, name := range []string{"parent_operation", "child_operation", "run", "acquisition", "acquisition_phase", "calculation", "search_receipt", "lookup_receipt", "handoff", "fallback_seal", "request_registration", "apply_request", "apply_receipt", "provider_dependency", "target", "delivery"} {
			if counts[name] <= 0 {
				t.Fatalf("retained retirement closure %s count=%d, want positive", name, counts[name])
			}
		}
	}
}

func readFallbackRetirementCounts(t *testing.T, ctx context.Context, db interface {
	NewRaw(string, ...any) *bun.RawQuery
}, parentID, childID uuid.UUID) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	queries := []struct {
		name  string
		query string
		id    uuid.UUID
	}{
		{"parent_operation", `SELECT count(*) FROM operation WHERE id=?`, parentID},
		{"child_operation", `SELECT count(*) FROM operation WHERE id=?`, childID},
		{"run", `SELECT count(*) FROM matching_run WHERE id=?`, parentID},
		{"acquisition", `SELECT count(*) FROM matching_acquisition WHERE run_id=?`, parentID},
		{"acquisition_phase", `SELECT count(*) FROM matching_acquisition_phase WHERE run_id=?`, parentID},
		{"acquisition_seal_legacy", `SELECT count(*) FROM matching_acquisition_seal WHERE run_id=?`, parentID},
		{"calculation", `SELECT count(*) FROM matching_fallback_calculation_receipt WHERE run_id=?`, parentID},
		{"search_receipt", `SELECT count(*) FROM matching_fallback_search_receipt WHERE run_id=?`, parentID},
		{"lookup_receipt", `SELECT count(*) FROM matching_fallback_lookup_receipt WHERE run_id=?`, parentID},
		{"handoff", `SELECT count(*) FROM matching_fallback_evaluation_handoff WHERE run_id=?`, parentID},
		{"fallback_seal", `SELECT count(*) FROM matching_fallback_authority_seal WHERE run_id=?`, parentID},
		{"request_registration", `SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?`, parentID},
		{"apply_request", `SELECT count(*) FROM matching_apply_request WHERE parent_run_id=?`, parentID},
		{"apply_receipt", `SELECT count(*) FROM matching_apply_receipt WHERE apply_operation_id=?`, childID},
		{"provider_dependency", `SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, parentID},
		{"target", `SELECT count(*) FROM matching_run_target WHERE run_id=?`, parentID},
		{"retirement_authorization", `SELECT count(*) FROM matching_run_retirement_authorization WHERE run_id=?`, parentID},
		{"delivery", `SELECT count(*) FROM matching_delivery WHERE run_id=?`, parentID},
	}
	for _, item := range queries {
		var count int
		if err := db.NewRaw(item.query, item.id).Scan(ctx, &count); err != nil {
			t.Fatalf("read retirement count %s: %v", item.name, err)
		}
		counts[item.name] = count
	}
	return counts
}

func assertFallbackRetirementBaseline(t *testing.T, counts map[string]int, workerCompleted bool) {
	t.Helper()
	for _, name := range []string{
		"acquisition", "acquisition_phase", "calculation", "search_receipt", "lookup_receipt", "handoff",
		"fallback_seal", "request_registration", "apply_request", "provider_dependency", "target", "delivery",
	} {
		if counts[name] <= 0 {
			t.Fatalf("retirement baseline %s count=%d, want a positive persisted proof", name, counts[name])
		}
	}
	if counts["retirement_authorization"] != 0 {
		t.Fatalf("retirement baseline authorization count=%d, want zero before authorized cleanup", counts["retirement_authorization"])
	}
	wantReceipt := 0
	if workerCompleted {
		wantReceipt = 1
	}
	if counts["apply_receipt"] != wantReceipt {
		t.Fatalf("retirement baseline apply receipt count=%d workerCompleted=%v, want %d", counts["apply_receipt"], workerCompleted, wantReceipt)
	}
}

func assertFallbackRetirementHeld(t *testing.T, ctx context.Context, db interface {
	NewRaw(string, ...any) *bun.RawQuery
}, parentID, childID uuid.UUID, baseline map[string]int) {
	t.Helper()
	current := readFallbackRetirementCounts(t, ctx, db, parentID, childID)
	for name, count := range baseline {
		if current[name] != count {
			t.Fatalf("held retirement closure %s count=%d, baseline=%d", name, current[name], count)
		}
	}
}

func assertRetiredOperationsMissing(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, parentID, childID uuid.UUID) {
	t.Helper()
	for _, id := range []uuid.UUID{parentID, childID} {
		if _, err := repository.GetOperation(ctx, id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("retired operation %s lookup error=%v, want sql.ErrNoRows", id, err)
		}
	}
}
