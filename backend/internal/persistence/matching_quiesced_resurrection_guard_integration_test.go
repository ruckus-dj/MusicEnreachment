//go:build integration

package persistence_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/uptrace/bun"
)

func TestQuiescedMatchingRunCannotResurrectThroughFallbackOrRetryPostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowWithRecoveryCallback(t, false, false, false, false, false, nil,
		func(t *testing.T, ctx context.Context, database *bun.DB, repository *persistence.SetupManagerRepository, inserter *fallbackHandoffRiverInserter, runID uuid.UUID, waitRevision int64, _ *jobs.MatchingEvaluationWorker) {
			t.Helper()
			if _, err := database.NewRaw(`UPDATE matching_run SET quiesced_at=now() WHERE id=?`, runID).Exec(ctx); err != nil {
				t.Fatalf("mark sealed matching run quiesced: %v", err)
			}
			beforeRecovery := readQuiescedMatchingGraph(t, ctx, database, runID)
			if err := repository.RecoverMatchingRuntime(ctx, inserter); err != nil {
				t.Fatalf("recover matching runtime with quiesced sealed run: %v", err)
			}
			if after := readQuiescedMatchingGraph(t, ctx, database, runID); !reflect.DeepEqual(after, beforeRecovery) {
				t.Fatalf("recovery mutated quiesced matching graph: before=%+v after=%+v", beforeRecovery, after)
			}

			operation, err := repository.GetOperation(ctx, runID)
			if err != nil {
				t.Fatalf("read sealed matching operation: %v", err)
			}
			if _, _, err := repository.ContinueMatchingFallbackSeal(ctx, inserter, runID, operation.Attempt, waitRevision); err == nil {
				t.Fatal("fallback continuation accepted a quiesced matching run")
			}
			if after := readQuiescedMatchingGraph(t, ctx, database, runID); !reflect.DeepEqual(after, beforeRecovery) {
				t.Fatalf("fallback continuation mutated quiesced matching graph: before=%+v after=%+v", beforeRecovery, after)
			}

			// A failed operation is the required retry precondition. The marker must
			// still reject retry before it inserts a replacement job or delivery.
			if _, err := database.NewRaw(`UPDATE operation SET state='failed',safe_error='fixture failure',finished_at=now() WHERE id=?`, runID).Exec(ctx); err != nil {
				t.Fatalf("prepare failed operation retry case: %v", err)
			}
			if _, err := database.NewRaw(`UPDATE matching_run SET state='failed' WHERE id=?`, runID).Exec(ctx); err != nil {
				t.Fatalf("prepare failed run retry case: %v", err)
			}
			beforeRetry := readQuiescedMatchingGraph(t, ctx, database, runID)
			_, err = repository.RetryMatchingEvaluationOperationAndEnqueue(ctx, runID, inserter,
				service.MatchingEvaluationOperationArgs{OperationID: runID}, (*river.InsertOpts)(nil))
			if err == nil {
				t.Fatal("retry accepted a quiesced matching run")
			}
			if after := readQuiescedMatchingGraph(t, ctx, database, runID); !reflect.DeepEqual(after, beforeRetry) {
				t.Fatalf("retry mutated quiesced matching graph: before=%+v after=%+v", beforeRetry, after)
			}
		}, true, false, false)
}

// TestQuiescedMatchingRunRejectsPreviouslyCapturedSealedRecoveryCandidatePostgreSQL
// exercises the independent marker guard: it captures a candidate while the run is
// eligible, then sets the persisted quiescence marker directly before replaying the
// candidate through the production transaction. This isolates the marker check from
// recovery classification, which is covered by the natural obsolete-run fixtures.
func TestQuiescedMatchingRunRejectsPreviouslyCapturedSealedRecoveryCandidatePostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowWithRecoveryCallback(t, false, false, false, false, false, nil,
		func(t *testing.T, ctx context.Context, database *bun.DB, repository *persistence.SetupManagerRepository, inserter *fallbackHandoffRiverInserter, runID uuid.UUID, _ int64, _ *jobs.MatchingEvaluationWorker) {
			t.Helper()
			if stopper, ok := inserter.client.(interface{ Stop(context.Context) error }); ok {
				if err := stopper.Stop(ctx); err != nil {
					t.Fatalf("stop automatic River polling: %v", err)
				}
			}
			var candidate persistence.MatchingRuntimeRecoveryCandidateForTest
			if err := database.NewRaw(`SELECT run.id,operation.attempt,acquisition.revision,acquisition.current_phase,
				run.current_river_job_id
				FROM matching_run run JOIN operation ON operation.id=run.operation_id
				JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
				WHERE run.id=? AND operation.kind='matching_evaluation_v1'
				AND acquisition.current_phase='fallback_evaluation_sealed'`, runID).
				Scan(ctx, &candidate.RunID, &candidate.Attempt, &candidate.Revision, &candidate.Phase, &candidate.CurrentJob); err != nil {
				t.Fatalf("capture eligible sealed recovery candidate before quiescing: %v", err)
			}
			if candidate.RunID != runID || candidate.Attempt < 1 || candidate.Revision < 2 ||
				candidate.Phase != persistence.MatchingAcquisitionFallbackEvaluationSealed || candidate.CurrentJob == nil {
				t.Fatalf("captured candidate is not the real sealed-flow candidate: %+v", candidate)
			}
			if _, err := database.NewRaw(`UPDATE river_job SET state='discarded',finalized_at=now() WHERE id=?`, *candidate.CurrentJob).Exec(ctx); err != nil {
				t.Fatalf("retire original wake before recovery: %v", err)
			}
			beforeMarker := readQuiescedMatchingGraph(t, ctx, database, runID)
			if beforeMarker.Quiesced || beforeMarker.ActiveJob != nil || beforeMarker.ActiveEpoch != nil {
				t.Fatalf("sealed recovery fixture still has an active owner: %+v", beforeMarker)
			}
			if _, err := database.NewRaw(`UPDATE matching_run SET quiesced_at=transaction_timestamp() WHERE id=?`, runID).Exec(ctx); err != nil {
				t.Fatalf("mark sealed matching run quiesced: %v", err)
			}
			beforeRecovery := readQuiescedMatchingGraph(t, ctx, database, runID)
			if beforeRecovery.OperationState != beforeMarker.OperationState || beforeRecovery.OperationStage != beforeMarker.OperationStage ||
				beforeRecovery.RunState != beforeMarker.RunState || beforeRecovery.Phase != beforeMarker.Phase ||
				beforeRecovery.Revision != beforeMarker.Revision || beforeRecovery.ExecutionEpoch != beforeMarker.ExecutionEpoch ||
				beforeRecovery.CurrentJob == nil || *beforeRecovery.CurrentJob != *candidate.CurrentJob ||
				beforeRecovery.ActiveJob != nil || beforeRecovery.ActiveEpoch != nil || !beforeRecovery.Quiesced {
				t.Fatalf("obsolete marker changed sealed execution state or retained an owner: before=%+v after=%+v", beforeMarker, beforeRecovery)
			}
			if after := readQuiescedMatchingGraph(t, ctx, database, runID); !reflect.DeepEqual(after, beforeRecovery) {
				t.Fatalf("outer recovery changed quiesced graph: before=%+v after=%+v", beforeRecovery, after)
			}
			recovered, err := repository.RecoverSealedMatchingWakeForTest(ctx, inserter, candidate)
			if err != nil || recovered {
				t.Fatalf("stale sealed candidate bypassed quiescence guard: recovered=%v err=%v", recovered, err)
			}
			if after := readQuiescedMatchingGraph(t, ctx, database, runID); !reflect.DeepEqual(after, beforeRecovery) {
				t.Fatalf("transactional recovery changed quiesced graph from stale candidate: before=%+v after=%+v", beforeRecovery, after)
			}
		}, true, false, false)
}

// quiescedMatchingGraphSnapshot captures the quiesced matching graph for
// before/after comparison. Its *int64 job/epoch fields are scanned into fresh
// pointers on every read, so snapshots must be compared with reflect.DeepEqual;
// a plain == or != would compare pointer identity and always report a mutation.
type quiescedMatchingGraphSnapshot struct {
	OperationState  string `bun:"operation_state"`
	OperationStage  string `bun:"operation_stage"`
	RunState        string `bun:"run_state"`
	Phase           string `bun:"current_phase"`
	Revision        int64  `bun:"revision"`
	ExecutionEpoch  int64  `bun:"execution_epoch"`
	CurrentJob      *int64 `bun:"current_river_job_id"`
	CurrentJobState string `bun:"current_job_state"`
	ActiveJob       *int64 `bun:"active_river_job_id"`
	ActiveEpoch     *int64 `bun:"active_execution_epoch"`
	Quiesced        bool   `bun:"is_quiesced"`
	Jobs            int    `bun:"jobs"`
	Deliveries      int    `bun:"deliveries"`
	Handoffs        int    `bun:"handoffs"`
	Registrations   int    `bun:"registrations"`
	Dependencies    int    `bun:"dependencies"`
	Cursor          int64  `bun:"cursor"`
}

func readQuiescedMatchingGraph(t *testing.T, ctx context.Context, database *bun.DB, runID uuid.UUID) quiescedMatchingGraphSnapshot {
	t.Helper()
	var state quiescedMatchingGraphSnapshot
	if err := database.NewRaw(`SELECT
		operation.state AS operation_state,operation.stage AS operation_stage,run.state AS run_state,
		acquisition.current_phase,acquisition.revision,run.execution_epoch,run.current_river_job_id,
		COALESCE((SELECT state::text FROM river_job WHERE id=run.current_river_job_id),'missing') AS current_job_state,
		run.active_river_job_id,run.active_execution_epoch,(run.quiesced_at IS NOT NULL) AS is_quiesced,
		(SELECT count(*) FROM river_job WHERE args->>'operation_id'=?::text OR args->>'operation_id' IN (
			SELECT request_id::text FROM matching_fallback_request_registration WHERE run_id=?)) AS jobs,
		(SELECT count(*) FROM matching_delivery WHERE run_id=?) AS deliveries,
		(SELECT count(*) FROM matching_fallback_evaluation_handoff WHERE run_id=?) AS handoffs,
		(SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?) AS registrations,
		(SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?) AS dependencies,
		acquisition.revision AS cursor
		FROM matching_run run JOIN operation ON operation.id=run.operation_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id WHERE run.id=?`,
		runID, runID, runID, runID, runID, runID, runID).Scan(ctx, &state); err != nil {
		t.Fatalf("read quiesced matching graph: %v", err)
	}
	return state
}
