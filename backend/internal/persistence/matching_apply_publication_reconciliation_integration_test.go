//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
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
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestAutomaticMatchingApplyRequestsPublicationReconciliationPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	evaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{
		fixture.candidate(fixture.memberA, .95), fixture.candidate(fixture.memberB, .95),
	})
	evaluation.Groups[0].Evidence = matchingApplyEvidence(t, fixture, fixture.trackID)
	if err := repository.Replace(ctx, evaluation); err != nil {
		t.Fatalf("persist unique matching evaluation: %v", err)
	}
	header, _, err := repository.Get(ctx, fixture.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read evaluation header: header=%+v err=%v", header, err)
	}
	if err := repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, header.EvaluationRevision); err != nil {
		t.Fatalf("apply unique matching evaluation: %v", err)
	}
	positions, epoch := assertAutomaticMatchingReconciliation(t, ctx, db, fixture)
	var generationTotal int64
	if err := db.NewRaw(`SELECT sum(requested_generation) FROM publication_reconciliation_request WHERE output_epoch=?`, epoch).Scan(ctx, &generationTotal); err != nil {
		t.Fatal(err)
	}
	if generationTotal != int64(len(positions)) {
		t.Fatalf("initial requested generations total=%d, want %d", generationTotal, len(positions))
	}

	// Direct apply has no receipt, so the same valid evaluation is another
	// materialization event; the per-position pending event must coalesce.
	if err := repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, header.EvaluationRevision); err != nil {
		t.Fatalf("repeat matching evaluation: %v", err)
	}
	var replayTotal int64
	if err := db.NewRaw(`SELECT sum(requested_generation) FROM publication_reconciliation_request WHERE output_epoch=?`, epoch).Scan(ctx, &replayTotal); err != nil {
		t.Fatal(err)
	}
	if replayTotal != generationTotal+int64(len(positions)) {
		t.Fatalf("repeat direct apply generations=%d, want %d", replayTotal, generationTotal+int64(len(positions)))
	}
	assertAutomaticMatchingReconciliation(t, ctx, db, fixture)
}

func TestAutomaticMatchingApplyDoesNotRequestReconciliationForReviewOrManualAuthorityPostgreSQL(t *testing.T) {
	for _, test := range []struct {
		name           string
		manual         bool
		expectConflict bool
	}{
		{name: "review decision"},
		{name: "manual authority", manual: true, expectConflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := testpostgres.OpenMigrated(t)
			ctx := context.Background()
			fixture := newIncomingMatchFixture(t, ctx, db)
			repository := persistence.NewIncomingMatchCandidateRepository(db)
			evaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{
				fixture.candidate(fixture.memberA, .95), fixture.candidate(fixture.memberB, .95),
			})
			evaluation.Groups[0].Evidence = matchingApplyEvidence(t, fixture, fixture.trackID)
			if !test.manual {
				var envelope map[string]json.RawMessage
				if err := json.Unmarshal(evaluation.Groups[0].Evidence, &envelope); err != nil {
					t.Fatal(err)
				}
				var decision map[string]json.RawMessage
				if err := json.Unmarshal(envelope["evaluation_decision"], &decision); err != nil {
					t.Fatal(err)
				}
				decision["outcome"] = json.RawMessage(`"review"`)
				decision["assignments"] = json.RawMessage(`[]`)
				envelope["evaluation_decision"], _ = json.Marshal(decision)
				evaluation.Groups[0].Evidence, _ = json.Marshal(envelope)
			}
			if err := repository.Replace(ctx, evaluation); err != nil {
				t.Fatalf("persist matching evaluation: %v", err)
			}
			header, _, err := repository.Get(ctx, fixture.group.ID)
			if err != nil || header == nil {
				t.Fatalf("read evaluation: header=%+v err=%v", header, err)
			}
			if test.manual {
				if _, err := db.NewRaw(`INSERT INTO incoming_group_matching_authority(group_id,mode,revision) VALUES (?,'manual',1)`, fixture.group.ID).Exec(ctx); err != nil {
					t.Fatalf("establish manual authority: %v", err)
				}
			}
			err = repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, header.EvaluationRevision)
			if test.expectConflict && !errors.Is(err, persistence.ErrIncomingMatchConflict) {
				t.Fatalf("apply with manual authority err=%v, want matching conflict", err)
			}
			if !test.expectConflict && err == nil {
				t.Fatal("review decision unexpectedly applied")
			}
			assertNoAutomaticMatchingReconciliation(t, ctx, db)
		})
	}
}

func TestAutomaticMatchingApplyRollbackRemovesReconciliationPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	evaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{
		fixture.candidate(fixture.memberA, .95), fixture.candidate(fixture.memberB, .95),
	})
	evaluation.Groups[0].Evidence = matchingApplyEvidence(t, fixture, fixture.trackID)
	if err := repository.Replace(ctx, evaluation); err != nil {
		t.Fatalf("persist matching evaluation: %v", err)
	}
	header, _, err := repository.Get(ctx, fixture.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read evaluation: header=%+v err=%v", header, err)
	}
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_automatic_apply_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.outcome='applied' THEN RAISE EXCEPTION 'injected receipt failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_automatic_apply_receipt BEFORE INSERT ON matching_apply_receipt FOR EACH ROW EXECUTE FUNCTION fail_automatic_apply_receipt()`); err != nil {
		t.Fatal(err)
	}
	setup := persistence.NewSetupManagerRepository(db)
	parentID, operationID := uuid.New(), uuid.New()
	finished := time.Now().UTC()
	if err := setup.CreateOperation(ctx, &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "succeeded", Stage: "complete", InputSnapshot: json.RawMessage(`{}`), FinishedAt: &finished}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO matching_run(id,operation_id,plan_admission_key,plan_digest,immutable_plan,grouping_revision,grouping_epoch,provider_source_id,provider_configuration_identity,policy_version,state,execution_epoch)
		SELECT ?,?,?,?, '{}'::jsonb,revision,?,?,'incoming-match-integration','matching-rapidfuzz-v1','succeeded',1 FROM incoming_grouping_state WHERE singleton=true`,
		parentID, parentID, "rollback-"+parentID.String(), make([]byte, 32), fixture.groupingEpoch, fixture.source).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO matching_run_target(run_id,group_id,captured_group_revision,target_generation) VALUES (?,?,?,1)`, parentID, fixture.group.ID, fixture.group.Revision).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO incoming_matching_generation(group_id,high_water,current_run_id) VALUES (?,1,?) ON CONFLICT (group_id) DO UPDATE SET high_water=1,current_run_id=EXCLUDED.current_run_id`, fixture.group.ID, parentID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	intent, err := json.Marshal(map[string]any{"schema_version": 1, "parent_run_id": parentID, "group_id": fixture.group.ID, "evaluation_revision": header.EvaluationRevision, "execution_epoch": 1})
	if err != nil {
		t.Fatal(err)
	}
	client := openScanEnqueueRiver(t, db)
	operation := &persistence.Operation{ID: operationID, Kind: persistence.MatchingApplyOperationKind, State: "queued", Stage: "queued", InputSnapshot: intent}
	if err := setup.CreateOperationAndEnqueue(ctx, operation, client, persistence.MatchingApplyOperationArgs{OperationID: operationID}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO matching_apply_request(group_id,parent_run_id,evaluation_revision,apply_operation_id) VALUES (?,?,?,?)`, fixture.group.ID, parentID, header.EvaluationRevision, operationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *operation.RiverJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	worker := jobs.NewMatchingApplyWorker(setup, service.NewOperations(setup))
	job := &river.Job[persistence.MatchingApplyOperationArgs]{JobRow: &rivertype.JobRow{ID: *operation.RiverJobID, Attempt: 1}, Args: persistence.MatchingApplyOperationArgs{OperationID: operationID}}
	if err := worker.Work(ctx, job); err == nil || !strings.Contains(err.Error(), "injected receipt failure") {
		t.Fatalf("apply error=%v, want injected receipt failure after materialization", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_automatic_apply_receipt ON matching_apply_receipt`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DROP FUNCTION fail_automatic_apply_receipt()`); err != nil {
		t.Fatal(err)
	}
	assertNoAutomaticMatchingReconciliation(t, ctx, db)
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("apply valid baseline after removing receipt failure: %v", err)
	}
	current, err := setup.GetOperation(ctx, operationID)
	if err != nil || current == nil || current.State != "succeeded" {
		t.Fatalf("apply baseline operation=%+v err=%v, want succeeded", current, err)
	}
	var outcome string
	if err := db.NewRaw(`SELECT outcome FROM matching_apply_receipt WHERE apply_operation_id=?`, operationID).Scan(ctx, &outcome); err != nil || outcome != "applied" {
		t.Fatalf("apply baseline outcome=%q err=%v, want applied", outcome, err)
	}
	assertAutomaticMatchingReconciliation(t, ctx, db, fixture)
}

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
