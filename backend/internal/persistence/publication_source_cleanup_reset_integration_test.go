//go:build integration

package persistence

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The ready-publication fixture commits a real publication but intentionally
// leaves the publisher's operation/job alive. Settle that delivery as a worker
// would before exercising reset's independent cleanup-owner proof.
func settleReadyPublicationForReset(t *testing.T, f publicationExecutionFixture) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.db.ExecContext(ctx, `UPDATE operation SET state='succeeded',stage='complete',finished_at=now() WHERE id=?`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='completed',finalized_at=now() WHERE id=?`, f.guard.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `DELETE FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `DELETE FROM publication_execution_claim WHERE operation_id=?`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
}

func TestOutputResetCancelsUnstartedPublicationSourceCleanupPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	settleReadyPublicationForReset(t, f)
	inserter := new(publicationSourceCleanupTestInserter)
	admitted, err := repo.AdmitPublicationSourceCleanup(ctx, PublicationSourceCleanupAdmission{
		PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration,
	}, inserter)
	if err != nil || admitted.OperationID == uuid.Nil || len(admitted.Blockers) != 0 {
		t.Fatalf("cleanup admission = %+v, %v", admitted, err)
	}
	var itemID uuid.UUID
	if err := f.db.QueryRowContext(ctx, `SELECT item.id FROM source_cleanup_item item JOIN source_cleanup_owner owner ON owner.item_id=item.id WHERE item.operation_id=?`, admitted.OperationID).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	// Model two independently owned items delivered by the same real operation
	// and River job; the reset must cancel that shared job only once.
	secondItemID := uuid.New()
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_cleanup_item(id,operation_id,position_id,epoch,publication_generation,state,source_snapshot,borrower_revision)
		SELECT ?,operation_id,position_id,epoch,publication_generation,state,source_snapshot,? FROM source_cleanup_item WHERE id=?`, secondItemID, uuid.New(), itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_cleanup_owner(item_id,operation_id,application_attempt,river_job_id,owner_uuid,source_root_id,source_location_id,work_id)
		SELECT ?,operation_id,application_attempt,river_job_id,?,source_root_id,?,? FROM source_cleanup_owner WHERE item_id=?`, secondItemID, uuid.New(), uuid.New(), uuid.New(), itemID); err != nil {
		t.Fatal(err)
	}
	var ownerCount int
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, admitted.OperationID).Scan(&ownerCount); err != nil || ownerCount != 2 {
		t.Fatalf("pre-reset owners=%d, %v", ownerCount, err)
	}
	var inventoryBefore string
	if err := f.db.QueryRowContext(ctx, `SELECT inventory_path FROM source_root ORDER BY id LIMIT 1`).Scan(&inventoryBefore); err != nil {
		t.Fatal(err)
	}
	var oldRoot string
	if err := f.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(setting_value), '') FROM app_setting WHERE setting_name='output_directory'`).Scan(&oldRoot); err != nil {
		t.Fatal(err)
	}
	var toolsRoot string
	if err := f.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(setting_value), '') FROM app_setting WHERE setting_name='tools_directory'`).Scan(&toolsRoot); err != nil {
		t.Fatal(err)
	}
	newRoot := nativeTestPath(t, "/music/new")
	_, err = NewSetupManagerRepository(f.db).RunOutputResetRequest(ctx, OutputResetRequest{
		ExpectedOldRoot: oldRoot, ExpectedToolsRoot: toolsRoot, NewRoot: newRoot,
		Prepare: func(context.Context, func(OutputResetDirectory) error) error { return nil },
		Finish:  func(context.Context, OutputResetJournal) error { return nil },
	})
	if err != nil {
		t.Fatalf("reset with proven queued cleanup owner: %v", err)
	}
	var state, operationState, jobState string
	var snapshotNull, revisionNull bool
	for _, id := range []uuid.UUID{itemID, secondItemID} {
		if err := f.db.QueryRowContext(ctx, `SELECT item.state,item.source_snapshot IS NULL,item.borrower_revision IS NULL,operation.state,job.state FROM source_cleanup_item item JOIN operation operation ON operation.id=item.operation_id JOIN river_job job ON job.id=operation.river_job_id WHERE item.id=?`, id).Scan(&state, &snapshotNull, &revisionNull, &operationState, &jobState); err != nil {
			t.Fatal(err)
		}
		if state != "cancelled" || !snapshotNull || !revisionNull || operationState != "failed" || jobState != "cancelled" {
			t.Fatalf("reset cleanup outcome item=%s item-state=%s snapshot-null=%t revision-null=%t op=%s job=%s", id, state, snapshotNull, revisionNull, operationState, jobState)
		}
	}
	var owners int
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, admitted.OperationID).Scan(&owners); err != nil || owners != 0 {
		t.Fatalf("owner count=%d, %v", owners, err)
	}
	var epoch uuid.UUID
	if err := f.db.QueryRowContext(ctx, `SELECT output_epoch FROM publication_namespace_state WHERE singleton`).Scan(&epoch); err != nil || epoch == f.guard.Epoch {
		t.Fatalf("epoch=%s err=%v, want rotation", epoch, err)
	}
	var publicationCount int
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(&publicationCount); err != nil || publicationCount != 0 {
		t.Fatalf("publication count=%d, %v", publicationCount, err)
	}
	var inventoryAfter string
	if err := f.db.QueryRowContext(ctx, `SELECT inventory_path FROM source_root ORDER BY id LIMIT 1`).Scan(&inventoryAfter); err != nil || inventoryAfter != inventoryBefore {
		t.Fatalf("inventory path changed %q -> %q: %v", inventoryBefore, inventoryAfter, err)
	}
}

func TestOutputResetRejectsFailedPublicationSourceCleanupOwnerPostgreSQL(t *testing.T) {
	t.Parallel()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	settleReadyPublicationForReset(t, f)
	admitted, err := repo.AdmitPublicationSourceCleanup(context.Background(), PublicationSourceCleanupAdmission{
		PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration,
	}, new(publicationSourceCleanupTestInserter))
	if err != nil || admitted.OperationID == uuid.Nil {
		t.Fatalf("cleanup admission = %+v, %v", admitted, err)
	}
	var jobID int64
	if err := f.db.QueryRowContext(context.Background(), `SELECT river_job_id FROM operation WHERE id=?`, admitted.OperationID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(context.Background(), `UPDATE operation SET state='failed',stage='failed',safe_error='failed probe',finished_at=now() WHERE id=?`, admitted.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(context.Background(), `UPDATE river_job SET state='discarded',attempt=1,attempted_at=now(),finalized_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	before := readCleanupResetState(t, f, admitted.OperationID)
	prepared := false
	_, err = NewSetupManagerRepository(f.db).RunOutputResetRequest(context.Background(), cleanupResetRequest(t, f, func(context.Context, func(OutputResetDirectory) error) error { prepared = true; return nil }))
	if err == nil || prepared {
		t.Fatalf("reset err=%v, prepare called=%t", err, prepared)
	}
	assertCleanupResetState(t, f, admitted.OperationID, before)
}

func TestOutputResetRechecksPublicationSourceCleanupAfterPreparePostgreSQL(t *testing.T) {
	t.Parallel()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	settleReadyPublicationForReset(t, f)
	admitted, err := repo.AdmitPublicationSourceCleanup(context.Background(), PublicationSourceCleanupAdmission{
		PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration,
	}, new(publicationSourceCleanupTestInserter))
	if err != nil || admitted.OperationID == uuid.Nil {
		t.Fatalf("cleanup admission = %+v, %v", admitted, err)
	}
	var jobID int64
	if err := f.db.QueryRowContext(context.Background(), `SELECT river_job_id FROM operation WHERE id=?`, admitted.OperationID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	before := readCleanupResetState(t, f, admitted.OperationID)
	request := cleanupResetRequest(t, f, func(context.Context, func(OutputResetDirectory) error) error {
		_, err := f.db.ExecContext(context.Background(), `UPDATE river_job SET state='available',attempt=1,attempted_at=now(),finalized_at=NULL WHERE id=?`, jobID)
		return err
	})
	_, err = NewSetupManagerRepository(f.db).RunOutputResetRequest(context.Background(), request)
	if err == nil {
		t.Fatal("reset accepted cleanup delivery claimed during preparation")
	}
	assertCleanupResetState(t, f, admitted.OperationID, before)
	journal, err := NewSetupManagerRepository(f.db).ReadUnresolvedOutputReset(context.Background())
	if err != nil || journal == nil || journal.State != "preparing" {
		t.Fatalf("unresolved journal = %+v, %v; want preparing", journal, err)
	}
}

func TestOutputResetRollsBackPublicationSourceCleanupOnLateFailurePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	settleReadyPublicationForReset(t, f)
	admitted, err := repo.AdmitPublicationSourceCleanup(ctx, PublicationSourceCleanupAdmission{
		PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration,
	}, new(publicationSourceCleanupTestInserter))
	if err != nil || admitted.OperationID == uuid.Nil {
		t.Fatalf("cleanup admission = %+v, %v", admitted, err)
	}
	before := readCleanupResetState(t, f, admitted.OperationID)
	for _, statement := range []string{
		`CREATE FUNCTION reject_cleanup_reset_epoch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected late cleanup reset failure'; END $$`,
		`CREATE TRIGGER reject_cleanup_reset_epoch BEFORE UPDATE ON publication_namespace_state FOR EACH ROW EXECUTE FUNCTION reject_cleanup_reset_epoch()`,
	} {
		if _, err := f.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	_, err = repo.RunOutputResetRequest(ctx, cleanupResetRequest(t, f, func(context.Context, func(OutputResetDirectory) error) error { return nil }))
	if err == nil || !strings.Contains(err.Error(), "injected late cleanup reset failure") {
		t.Fatalf("late reset failure = %v", err)
	}
	assertCleanupResetState(t, f, admitted.OperationID, before)
}

type cleanupResetState struct {
	owners, items, publications int
	epoch                       uuid.UUID
	operation, job              string
	itemEvidence                string
}

func readCleanupResetState(t *testing.T, f publicationExecutionFixture, operationID uuid.UUID) cleanupResetState {
	t.Helper()
	ctx := context.Background()
	s := cleanupResetState{}
	for query, dest := range map[string]any{`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`: &s.owners, `SELECT count(*) FROM source_cleanup_item WHERE operation_id=?`: &s.items, `SELECT state FROM operation WHERE id=?`: &s.operation, `SELECT state FROM river_job WHERE id=(SELECT river_job_id FROM operation WHERE id=?)`: &s.job} {
		if err := f.db.QueryRowContext(ctx, query, operationID).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.QueryRowContext(ctx, `SELECT output_epoch FROM publication_namespace_state WHERE singleton`).Scan(&s.epoch); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(&s.publications); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id,'state',state,'snapshot',source_snapshot,'revision',borrower_revision) ORDER BY id)::text,'[]') FROM source_cleanup_item WHERE operation_id=?`, operationID).Scan(&s.itemEvidence); err != nil {
		t.Fatal(err)
	}
	return s
}

func assertCleanupResetState(t *testing.T, f publicationExecutionFixture, operationID uuid.UUID, want cleanupResetState) {
	t.Helper()
	if got := readCleanupResetState(t, f, operationID); got != want {
		t.Fatalf("cleanup/reset state changed: got %+v want %+v", got, want)
	}
}

func cleanupResetRequest(t *testing.T, f publicationExecutionFixture, prepare func(context.Context, func(OutputResetDirectory) error) error) OutputResetRequest {
	t.Helper()
	ctx := context.Background()
	oldRoot, toolsRoot := nativeTestPath(t, "/music/cleanup-reset-old"), nativeTestPath(t, "/tools/cleanup-reset")
	for name, value := range map[string]string{"output_directory": oldRoot, "tools_directory": toolsRoot} {
		if _, err := f.db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value,updated_at) VALUES (?, ?, now()) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value,updated_at=now()`, name, value); err != nil {
			t.Fatal(err)
		}
	}
	return OutputResetRequest{ExpectedOldRoot: oldRoot, ExpectedToolsRoot: toolsRoot, NewRoot: nativeTestPath(t, "/music/cleanup-reset-new"), Prepare: prepare, Finish: func(context.Context, OutputResetJournal) error { return nil }}
}
