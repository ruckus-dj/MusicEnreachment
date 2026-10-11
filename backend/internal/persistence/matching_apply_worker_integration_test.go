//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"strings"
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

// This covers the production worker boundary: a durable apply intent is claimed,
// materialized and receipted by Work, rather than applying through the candidate
// repository's direct convenience method.
func TestMatchingApplyWorkerMaterializesPersistedIntentPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	candidates := persistence.NewIncomingMatchCandidateRepository(db)
	evaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{
		fixture.candidate(fixture.memberA, .95), fixture.candidate(fixture.memberB, .95),
	})
	evaluation.Groups[0].Evidence = matchingApplyEvidence(t, fixture, fixture.trackID)
	if err := candidates.Replace(ctx, evaluation); err != nil {
		t.Fatalf("persist unique matching evaluation: %v", err)
	}
	header, _, err := candidates.Get(ctx, fixture.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read persisted evaluation: header=%+v err=%v", header, err)
	}

	setup := persistence.NewSetupManagerRepository(db)
	parentID := uuid.New()
	finishedAt := time.Now().UTC()
	parent := &persistence.Operation{
		ID: parentID, Kind: "matching_evaluation_v1", State: "succeeded", Stage: "complete",
		InputSnapshot: json.RawMessage(`{}`), FinishedAt: &finishedAt,
	}
	if err := setup.CreateOperation(ctx, parent); err != nil {
		t.Fatalf("create original matching operation: %v", err)
	}
	const executionEpoch = int64(1)
	if _, err := db.NewRaw(`INSERT INTO matching_run(
		id, operation_id, plan_admission_key, plan_digest, immutable_plan, grouping_revision,
		grouping_epoch, provider_source_id, provider_configuration_identity, policy_version,
		state, execution_epoch
	) SELECT ?, ?, ?, ?, '{}'::jsonb, revision, ?, ?, 'incoming-match-integration',
		'matching-rapidfuzz-v1', 'succeeded', ? FROM incoming_grouping_state WHERE singleton=true`,
		parentID, parentID, "worker-test-"+parentID.String(), make([]byte, 32), fixture.groupingEpoch,
		fixture.source, executionEpoch).Exec(ctx); err != nil {
		t.Fatalf("persist original matching run: %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO matching_run_target(run_id,group_id,captured_group_revision,target_generation) VALUES (?,?,?,1)`,
		parentID, fixture.group.ID, fixture.group.Revision).Exec(ctx); err != nil {
		t.Fatalf("persist matching run target: %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO incoming_matching_generation(group_id,high_water,current_run_id) VALUES (?,1,?)
		ON CONFLICT (group_id) DO UPDATE SET high_water=1,current_run_id=EXCLUDED.current_run_id`, fixture.group.ID, parentID).Exec(ctx); err != nil {
		t.Fatalf("persist current matching generation: %v", err)
	}

	operationID := uuid.New()
	intent, err := json.Marshal(struct {
		SchemaVersion      int       `json:"schema_version"`
		ParentRunID        uuid.UUID `json:"parent_run_id"`
		GroupID            uuid.UUID `json:"group_id"`
		EvaluationRevision string    `json:"evaluation_revision"`
		ExecutionEpoch     int64     `json:"execution_epoch"`
	}{1, parentID, fixture.group.ID, header.EvaluationRevision, executionEpoch})
	if err != nil {
		t.Fatal(err)
	}
	riverClient := openScanEnqueueRiver(t, db)
	operation := &persistence.Operation{
		ID: operationID, Kind: persistence.MatchingApplyOperationKind, State: "queued", Stage: "queued",
		InputSnapshot: intent,
	}
	if err := setup.CreateOperationAndEnqueue(ctx, operation, riverClient, persistence.MatchingApplyOperationArgs{OperationID: operationID}, nil); err != nil {
		t.Fatalf("create durable apply operation and River delivery: %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO matching_apply_request(group_id,parent_run_id,evaluation_revision,apply_operation_id) VALUES (?,?,?,?)`,
		fixture.group.ID, parentID, header.EvaluationRevision, operationID).Exec(ctx); err != nil {
		t.Fatalf("persist apply request: %v", err)
	}
	jobID := *operation.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running', attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatalf("mark apply delivery running: %v", err)
	}

	worker := jobs.NewMatchingApplyWorker(setup, service.NewOperations(setup))
	job := &river.Job[persistence.MatchingApplyOperationArgs]{
		JobRow: &rivertype.JobRow{ID: jobID, Attempt: 1},
		Args:   persistence.MatchingApplyOperationArgs{OperationID: operationID},
	}
	beforeCounts := matchingApplyPersistedCounts(t, ctx, db, fixture.provider)
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_matching_apply_materialization() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected matching apply database failure'; END $$`); err != nil {
		t.Fatalf("install materialization failure trigger: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_matching_apply_materialization BEFORE INSERT ON local_release
		FOR EACH ROW EXECUTE FUNCTION fail_matching_apply_materialization()`); err != nil {
		t.Fatalf("install materialization failure trigger: %v", err)
	}
	if workErr := worker.Work(ctx, job); workErr == nil || !strings.Contains(workErr.Error(), "injected matching apply database failure") {
		t.Fatalf("first matching apply delivery error=%v, want injected database failure", workErr)
	}
	var claimReleased bool
	var epochAfterFailure int64
	if err := db.NewRaw(`SELECT provider_delivery_claimed_at IS NULL, provider_execution_epoch FROM operation WHERE id=?`, operationID).Scan(ctx, &claimReleased, &epochAfterFailure); err != nil || !claimReleased || epochAfterFailure != 1 {
		t.Fatalf("failed apply delivery released=%v epoch=%d err=%v, want released epoch 1", claimReleased, epochAfterFailure, err)
	}
	var receiptCount int
	if err := db.NewRaw(`SELECT count(*) FROM matching_apply_receipt WHERE apply_operation_id=?`, operationID).Scan(ctx, &receiptCount); err != nil || receiptCount != 0 {
		t.Fatalf("failed apply receipt count=%d err=%v, want no receipt", receiptCount, err)
	}
	if afterFailure := matchingApplyPersistedCounts(t, ctx, db, fixture.provider); afterFailure != beforeCounts {
		t.Fatalf("failed apply changed persisted counts: before=%+v after=%+v", beforeCounts, afterFailure)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_matching_apply_materialization ON local_release`); err != nil {
		t.Fatalf("remove materialization failure trigger: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP FUNCTION fail_matching_apply_materialization()`); err != nil {
		t.Fatalf("remove materialization failure trigger function: %v", err)
	}
	job.Attempt = 2 // Same registered River job; durable operation attempt remains 1.
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("retry actual matching apply worker after database recovery: %v", err)
	}

	completed, err := setup.GetOperation(ctx, operationID)
	if err != nil || completed.State != "succeeded" || completed.Stage != "applied" {
		t.Fatalf("apply operation = %+v err=%v, want succeeded/applied", completed, err)
	}
	var receiptOperationID, receiptGroupID uuid.UUID
	var receiptOutcome string
	if err := db.NewRaw(`SELECT apply_operation_id, group_id, outcome FROM matching_apply_receipt WHERE apply_operation_id=?`, operationID).Scan(ctx, &receiptOperationID, &receiptGroupID, &receiptOutcome); err != nil || receiptOperationID != operationID || receiptGroupID != fixture.group.ID || receiptOutcome != "applied" {
		t.Fatalf("apply receipt op=%s group=%s outcome=%q err=%v, want %s/%s/applied", receiptOperationID, receiptGroupID, receiptOutcome, err, operationID, fixture.group.ID)
	}
	var requestParent, requestGroup uuid.UUID
	var requestRevision string
	if err := db.NewRaw(`SELECT parent_run_id, group_id, evaluation_revision FROM matching_apply_request WHERE apply_operation_id=?`, operationID).Scan(ctx, &requestParent, &requestGroup, &requestRevision); err != nil || requestParent != parentID || requestGroup != fixture.group.ID || requestRevision != header.EvaluationRevision {
		t.Fatalf("apply request parent=%s group=%s revision=%q err=%v, want %s/%s/%q", requestParent, requestGroup, requestRevision, err, parentID, fixture.group.ID, header.EvaluationRevision)
	}
	// The persisted decision assigns both members to the one evaluated provider
	// track, so each work's actual source variant must map to that local track
	// with an unselected automatic decision rather than merely any nonzero row.
	for _, workID := range []uuid.UUID{fixture.workA, fixture.workB} {
		var variants []uuid.UUID
		if err := db.NewRaw(`SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=? ORDER BY media_variant_id`, workID).Scan(ctx, &variants); err != nil || len(variants) == 0 {
			t.Fatalf("source work %s variants=%v err=%v, want at least one", workID, variants, err)
		}
		for _, variantID := range variants {
			var mapped struct {
				ProviderTrackID uuid.UUID `bun:"provider_track_id"`
				Selected        bool      `bun:"selected"`
				Method          string    `bun:"decision_method"`
				PolicyVersion   string    `bun:"policy_version"`
			}
			if err := db.NewRaw(`SELECT plt.provider_track_id, ts.selected, ts.decision_method, ts.policy_version
				FROM track_source ts JOIN provider_local_track plt ON plt.local_track_id=ts.track_id
				WHERE ts.media_variant_id=? AND plt.provider_id=?`, variantID, fixture.provider).Scan(ctx, &mapped); err != nil {
				t.Fatalf("read materialized variant %s source: %v", variantID, err)
			}
			if mapped.ProviderTrackID != fixture.trackID || mapped.Selected || mapped.Method != "automatic" || mapped.PolicyVersion != "matching-rapidfuzz-v1" {
				t.Fatalf("variant %s materialized as %+v, want provider track %s unselected automatic matching-rapidfuzz-v1", variantID, mapped, fixture.trackID)
			}
		}
	}
	appliedCounts := matchingApplyPersistedCounts(t, ctx, db, fixture.provider)
	if _, err := db.NewRaw(`UPDATE incoming_match_evaluation SET evaluation_revision='newer-evaluation-revision' WHERE group_id=?`, fixture.group.ID).Exec(ctx); err != nil {
		t.Fatalf("publish a later evaluation revision for replay fence: %v", err)
	}
	job.Attempt = 3
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("redeliver completed apply after a newer evaluation: %v", err)
	}
	if replayedCounts := matchingApplyPersistedCounts(t, ctx, db, fixture.provider); replayedCounts != appliedCounts {
		t.Fatalf("redelivered apply changed persisted counts: before=%+v after=%+v", appliedCounts, replayedCounts)
	}
	var replayedOperationID, replayedGroupID uuid.UUID
	var replayedOutcome string
	if err := db.NewRaw(`SELECT apply_operation_id, group_id, outcome FROM matching_apply_receipt WHERE apply_operation_id=?`, operationID).Scan(ctx, &replayedOperationID, &replayedGroupID, &replayedOutcome); err != nil || replayedOperationID != receiptOperationID || replayedGroupID != receiptGroupID || replayedOutcome != receiptOutcome {
		t.Fatalf("redelivered receipt op=%s group=%s outcome=%q err=%v, want stable %s/%s/%q", replayedOperationID, replayedGroupID, replayedOutcome, err, receiptOperationID, receiptGroupID, receiptOutcome)
	}
}
