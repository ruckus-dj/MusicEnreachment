//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type sourceAnalysisCleanupOwnerInserter struct {
	args river.JobArgs
}

type sourceCleanupAnalysisJobArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (sourceCleanupAnalysisJobArgs) Kind() string { return "analyze_source_v1" }

func (inserter *sourceAnalysisCleanupOwnerInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	inserter.args = args
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var jobID int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args) VALUES ($1,$2::jsonb) RETURNING id`, args.Kind(), string(encoded)).Scan(&jobID); err != nil {
		return nil, err
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: jobID}}, nil
}

func sourceCleanupTestAnalysisOperation(t *testing.T, workID, rootID uuid.UUID) *Operation {
	t.Helper()
	rerun := false
	snapshot, err := json.Marshal(SourceAnalysisOperationSnapshot{
		SchemaVersion: SourceAnalysisOperationSnapshotVersion,
		Mode:          SourceAnalysisModeBatch,
		WorkIDs:       []uuid.UUID{workID},
		RerunTarget:   &rerun,
		SelectedSteps: []SourceAnalysisStepSelection{{WorkID: workID, Step: SourceStepSHA256}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &Operation{
		ID: uuid.New(), Kind: analysisSourceOperationKind, State: "queued", Stage: "queued",
		InputSnapshot: snapshot, Attempt: 1, SourceAnalysisMode: SourceAnalysisModeBatch,
		TargetSourceRootID: &rootID,
	}
}

func TestSourceCleanupOwnerBlocksAnalysisBeforeWritesAndReleaseAllowsAdmissionPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, cleanupRepo, variantID := publicationSourceCleanupReadyFixture(t)
	var workID, rootID, locationID uuid.UUID
	if err := f.db.QueryRowContext(ctx, `SELECT w.id,w.source_root_id,w.location_id FROM source_work_variant_identity i JOIN source_analysis_work w ON w.id=i.work_id WHERE i.media_variant_id=?`, variantID).Scan(&workID, &rootID, &locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE source_analysis_work SET sha256_enabled=true WHERE id=?`, workID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,step_attempt) VALUES (?,'sha256','pending',0)`, workID); err != nil {
		t.Fatal(err)
	}
	analysisRepo := NewSourceInventoryRepository(f.db)
	operation := sourceCleanupTestAnalysisOperation(t, workID, rootID)
	analysisInserter := new(sourceAnalysisCleanupOwnerInserter)
	if err := analysisRepo.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, analysisInserter, sourceCleanupAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("valid SHA-256-only analysis baseline admission: %v", err)
	}
	// Remove only that successful baseline's hold/job/operation and restore the pending step.
	if err := analysisRepo.SettleNormalizedSourceAnalysisOperation(ctx, operation.ID, "failed", "failed", "retire baseline fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='cancelled',finalized_at=now() WHERE id=?`, *operation.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE source_analysis_step SET state='pending',safe_error=NULL,last_operation_id=NULL,input_snapshot=NULL WHERE work_id=? AND step='sha256'`, workID); err != nil {
		t.Fatal(err)
	}
	cleanupRequest := PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}
	cleanupInsert := new(publicationSourceCleanupTestInserter)
	cleanup, err := cleanupRepo.AdmitPublicationSourceCleanup(ctx, cleanupRequest, cleanupInsert)
	if err != nil || cleanup.OperationID == uuid.Nil {
		t.Fatalf("admit cleanup owner: %+v, %v", cleanup, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE operation SET state='failed',stage='failed',safe_error='cleanup failed',finished_at=now() WHERE id=?`, cleanup.OperationID); err != nil {
		t.Fatal(err)
	}
	var beforeOps, beforeJobs, beforeHolds, beforeStepOps, beforeOwner int
	for _, item := range []struct {
		query  string
		target *int
	}{
		{`SELECT count(*) FROM operation`, &beforeOps}, {`SELECT count(*) FROM river_job`, &beforeJobs},
		{`SELECT count(*) FROM operation_source_work_hold`, &beforeHolds},
		{`SELECT count(*) FROM source_analysis_step WHERE work_id=? AND step='sha256' AND state='pending'`, &beforeStepOps},
		{`SELECT count(*) FROM source_cleanup_owner WHERE work_id=? OR source_location_id=?`, &beforeOwner},
	} {
		var err error
		if item.query == `SELECT count(*) FROM source_analysis_step WHERE work_id=? AND step='sha256' AND state='pending'` {
			err = f.db.QueryRowContext(ctx, item.query, workID).Scan(item.target)
		} else if item.query == `SELECT count(*) FROM source_cleanup_owner WHERE work_id=? OR source_location_id=?` {
			err = f.db.QueryRowContext(ctx, item.query, workID, locationID).Scan(item.target)
		} else {
			err = f.db.QueryRowContext(ctx, item.query).Scan(item.target)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	blocked := sourceCleanupTestAnalysisOperation(t, workID, rootID)
	blockedInserter := new(sourceAnalysisCleanupOwnerInserter)
	err = analysisRepo.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, blocked, blockedInserter, sourceCleanupAnalysisJobArgs{OperationID: blocked.ID}, nil)
	if !errors.Is(err, ErrSourceCleanupOwned) {
		t.Fatalf("analysis with failed cleanup owner error = %v, want ErrSourceCleanupOwned", err)
	}
	if blockedInserter.args != nil {
		t.Fatalf("blocked analysis enqueued job: %#v", blockedInserter.args)
	}
	var afterOps, afterJobs, afterHolds, afterPending, afterOwner int
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM operation`).Scan(&afterOps); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM river_job`).Scan(&afterJobs); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM operation_source_work_hold`).Scan(&afterHolds); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM source_analysis_step WHERE work_id=? AND step='sha256' AND state='pending'`, workID).Scan(&afterPending); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM source_cleanup_owner WHERE work_id=? OR source_location_id=?`, workID, locationID).Scan(&afterOwner); err != nil {
		t.Fatal(err)
	}
	if afterOps != beforeOps || afterJobs != beforeJobs || afterHolds != beforeHolds || afterPending != beforeStepOps || afterOwner != beforeOwner {
		t.Fatalf("blocked analysis mutated DB: operations %d/%d jobs %d/%d holds %d/%d pending %d/%d owners %d/%d", afterOps, beforeOps, afterJobs, beforeJobs, afterHolds, beforeHolds, afterPending, beforeStepOps, afterOwner, beforeOwner)
	}
	if _, err := f.db.ExecContext(ctx, `DELETE FROM source_cleanup_owner WHERE operation_id=?`, cleanup.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := analysisRepo.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, blocked, new(sourceAnalysisCleanupOwnerInserter), sourceCleanupAnalysisJobArgs{OperationID: blocked.ID}, nil); err != nil {
		t.Fatalf("analysis admission after releasing cleanup owner: %v", err)
	}
}

func TestPublicationCleanupAdmissionBlocksRetainedOperationSourceWorkHoldPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	var workID uuid.UUID
	if err := f.db.QueryRowContext(ctx, `SELECT work_id FROM source_work_variant_identity LIMIT 1`).Scan(&workID); err != nil {
		t.Fatal(err)
	}
	var sourceRootID, locationID uuid.UUID
	if err := f.db.QueryRowContext(ctx, `SELECT source_root_id,location_id FROM source_analysis_work WHERE id=?`, workID).Scan(&sourceRootID, &locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE source_analysis_work SET sha256_enabled=true WHERE id=?`, workID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,step_attempt) VALUES (?,'sha256','pending',0)`, workID); err != nil {
		t.Fatal(err)
	}
	analysisRepo := NewSourceInventoryRepository(f.db)
	owner := sourceCleanupTestAnalysisOperation(t, workID, sourceRootID)
	if err := analysisRepo.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, owner, new(sourceAnalysisCleanupOwnerInserter), sourceCleanupAnalysisJobArgs{OperationID: owner.ID}, nil); err != nil {
		t.Fatal(err)
	}
	request := PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}
	var operations, jobs, items, owners int
	for _, x := range []struct {
		q string
		p *int
	}{{`SELECT count(*) FROM operation WHERE kind='cleanup_publication_sources_v1'`, &operations}, {`SELECT count(*) FROM river_job`, &jobs}, {`SELECT count(*) FROM source_cleanup_item`, &items}, {`SELECT count(*) FROM source_cleanup_owner`, &owners}} {
		if err := f.db.QueryRowContext(ctx, x.q).Scan(x.p); err != nil {
			t.Fatal(err)
		}
	}
	result, err := repo.AdmitPublicationSourceCleanup(ctx, request, new(publicationSourceCleanupTestInserter))
	if err != nil || result.OperationID != uuid.Nil || len(result.Blockers) == 0 {
		t.Fatalf("cleanup with retained consumer hold = %+v, %v; want blocker", result, err)
	}
	var gotOps, gotJobs, gotItems, gotOwners int
	for _, x := range []struct {
		q string
		p *int
	}{{`SELECT count(*) FROM operation WHERE kind='cleanup_publication_sources_v1'`, &gotOps}, {`SELECT count(*) FROM river_job`, &gotJobs}, {`SELECT count(*) FROM source_cleanup_item`, &gotItems}, {`SELECT count(*) FROM source_cleanup_owner`, &gotOwners}} {
		if err := f.db.QueryRowContext(ctx, x.q).Scan(x.p); err != nil {
			t.Fatal(err)
		}
	}
	if gotOps != operations || gotJobs != jobs || gotItems != items || gotOwners != owners {
		t.Fatalf("blocked cleanup wrote artifacts: ops %d/%d jobs %d/%d items %d/%d owners %d/%d", gotOps, operations, gotJobs, jobs, gotItems, items, gotOwners, owners)
	}
	if err := analysisRepo.SettleNormalizedSourceAnalysisOperation(ctx, owner.ID, "failed", "failed", "retire held analysis fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='cancelled',finalized_at=now() WHERE id=?`, *owner.RiverJobID); err != nil {
		t.Fatal(err)
	}
	result, err = repo.AdmitPublicationSourceCleanup(ctx, request, new(publicationSourceCleanupTestInserter))
	if err != nil || result.OperationID == uuid.Nil || len(result.Blockers) != 0 {
		t.Fatalf("cleanup after releasing retained hold = %+v, %v", result, err)
	}
	_ = sourceRootID
	_ = locationID
}
