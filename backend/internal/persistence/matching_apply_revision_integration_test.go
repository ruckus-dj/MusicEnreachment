//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestMatchingApplyRequestsAreScopedToEvaluationRevisionPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	candidates := persistence.NewIncomingMatchCandidateRepository(db)
	setup := persistence.NewSetupManagerRepository(db)

	firstEvaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{
		fixture.candidate(fixture.memberA, .95), fixture.candidate(fixture.memberB, .95),
	})
	firstEvaluation.Groups[0].Evidence = matchingApplyEvidence(t, fixture, fixture.trackID)
	if err := candidates.Replace(ctx, firstEvaluation); err != nil {
		t.Fatalf("persist first unique evaluation: %v", err)
	}
	firstHeader, _, err := candidates.Get(ctx, fixture.group.ID)
	if err != nil || firstHeader == nil {
		t.Fatalf("read first evaluation: header=%+v err=%v", firstHeader, err)
	}

	parentID := uuid.New()
	finishedAt := time.Now().UTC()
	if err := setup.CreateOperation(ctx, &persistence.Operation{
		ID: parentID, Kind: "matching_evaluation_v1", State: "succeeded", Stage: "complete",
		InputSnapshot: json.RawMessage(`{}`), FinishedAt: &finishedAt,
	}); err != nil {
		t.Fatalf("create parent operation: %v", err)
	}
	const executionEpoch = int64(1)
	if _, err := db.NewRaw(`INSERT INTO matching_run(
		id, operation_id, plan_admission_key, plan_digest, immutable_plan, grouping_revision,
		grouping_epoch, provider_source_id, provider_configuration_identity, policy_version,
		state, execution_epoch
	) SELECT ?, ?, ?, ?, '{}'::jsonb, revision, ?, ?, 'incoming-match-integration',
		'matching-rapidfuzz-v1', 'succeeded', ? FROM incoming_grouping_state WHERE singleton=true`,
		parentID, parentID, "revision-test-"+parentID.String(), make([]byte, 32), fixture.groupingEpoch,
		fixture.source, executionEpoch).Exec(ctx); err != nil {
		t.Fatalf("persist parent matching run: %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO matching_run_target(run_id,group_id,captured_group_revision,target_generation) VALUES (?,?,?,1)`,
		parentID, fixture.group.ID, fixture.group.Revision).Exec(ctx); err != nil {
		t.Fatalf("persist matching target: %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO incoming_matching_generation(group_id,high_water,current_run_id) VALUES (?,1,?)
		ON CONFLICT (group_id) DO UPDATE SET high_water=1,current_run_id=EXCLUDED.current_run_id`, fixture.group.ID, parentID).Exec(ctx); err != nil {
		t.Fatalf("persist current matching generation: %v", err)
	}
	riverClient := openScanEnqueueRiver(t, db)
	createApplyIntent := func(revision string) (uuid.UUID, int64) {
		t.Helper()
		operationID := uuid.New()
		intent, err := json.Marshal(struct {
			SchemaVersion      int       `json:"schema_version"`
			ParentRunID        uuid.UUID `json:"parent_run_id"`
			GroupID            uuid.UUID `json:"group_id"`
			EvaluationRevision string    `json:"evaluation_revision"`
			ExecutionEpoch     int64     `json:"execution_epoch"`
		}{1, parentID, fixture.group.ID, revision, executionEpoch})
		if err != nil {
			t.Fatal(err)
		}
		operation := &persistence.Operation{
			ID: operationID, Kind: persistence.MatchingApplyOperationKind, State: "queued", Stage: "queued",
			InputSnapshot: intent,
		}
		if err := setup.CreateOperationAndEnqueue(ctx, operation, riverClient, persistence.MatchingApplyOperationArgs{OperationID: operationID}, nil); err != nil {
			t.Fatalf("create apply operation %s: %v", operationID, err)
		}
		if _, err := db.NewRaw(`INSERT INTO matching_apply_request(group_id,parent_run_id,evaluation_revision,apply_operation_id) VALUES (?,?,?,?)`,
			fixture.group.ID, parentID, revision, operationID).Exec(ctx); err != nil {
			t.Fatalf("persist request for revision %s: %v", revision, err)
		}
		jobID := *operation.RiverJobID
		if _, err := db.NewRaw(`UPDATE river_job SET state='running', attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
			t.Fatalf("mark apply delivery running: %v", err)
		}
		return operationID, jobID
	}

	firstOperationID, firstJobID := createApplyIntent(firstHeader.EvaluationRevision)

	secondEvaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{
		fixture.candidate(fixture.memberA, .95), fixture.candidate(fixture.memberB, .95),
	})
	secondEvaluation.Groups[0].Evidence = matchingApplyEvidence(t, fixture, fixture.trackID)
	if err := candidates.Replace(ctx, secondEvaluation); err != nil {
		t.Fatalf("persist later unique evaluation: %v", err)
	}
	secondHeader, _, err := candidates.Get(ctx, fixture.group.ID)
	if err != nil || secondHeader == nil || secondHeader.EvaluationRevision == firstHeader.EvaluationRevision {
		t.Fatalf("second evaluation revision=%+v err=%v, want distinct revision", secondHeader, err)
	}
	secondOperationID, secondJobID := createApplyIntent(secondHeader.EvaluationRevision)

	worker := jobs.NewMatchingApplyWorker(setup, service.NewOperations(setup))
	work := func(operationID uuid.UUID, jobID int64) {
		t.Helper()
		if err := worker.Work(ctx, &river.Job[persistence.MatchingApplyOperationArgs]{
			JobRow: &rivertype.JobRow{ID: jobID, Attempt: 1},
			Args:   persistence.MatchingApplyOperationArgs{OperationID: operationID},
		}); err != nil {
			t.Fatalf("run apply worker for %s: %v", operationID, err)
		}
	}
	work(firstOperationID, firstJobID)
	assertApplyOutcome := func(operationID uuid.UUID, want string) {
		t.Helper()
		var outcome string
		if err := db.NewRaw(`SELECT outcome FROM matching_apply_receipt WHERE apply_operation_id=? AND group_id=?`, operationID, fixture.group.ID).Scan(ctx, &outcome); err != nil || outcome != want {
			t.Fatalf("receipt for operation %s = %q err=%v, want %q", operationID, outcome, err, want)
		}
	}
	assertApplyOutcome(firstOperationID, "review_required")

	// Recover the still-unfinished second request through the repository's real
	// retry path after the first operation has written its group receipt.
	// Its immutable intent/request must still validate independently.
	retried, err := setup.RequeueMatchingApplyOperation(ctx, secondOperationID, riverClient,
		persistence.MatchingApplyOperationArgs{OperationID: secondOperationID}, nil, true)
	if err != nil || retried == nil || retried.RiverJobID == nil || retried.Attempt != 2 {
		t.Fatalf("recover second apply request: operation=%+v err=%v", retried, err)
	}
	secondJobID = *retried.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running', attempt=1 WHERE id=?`, secondJobID).Exec(ctx); err != nil {
		t.Fatalf("mark recovered apply delivery running: %v", err)
	}
	work(secondOperationID, secondJobID)
	assertApplyOutcome(secondOperationID, "applied")

	// Replay the first operation after the second has an applied receipt. The
	// receipt lookup must remain keyed by operation ID, not just group ID.
	if _, err := db.NewRaw(`UPDATE operation SET state='queued', stage='queued', finished_at=NULL, provider_delivery_claimed_at=NULL WHERE id=?`, firstOperationID).Exec(ctx); err != nil {
		t.Fatalf("requeue first operation for receipt replay: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='running' WHERE id=?`, firstJobID).Exec(ctx); err != nil {
		t.Fatalf("restart first delivery for replay: %v", err)
	}
	work(firstOperationID, firstJobID)
	assertApplyOutcome(firstOperationID, "review_required")
	assertApplyOutcome(secondOperationID, "applied")
}
