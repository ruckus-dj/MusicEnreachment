//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/uptrace/bun"
)

type publicationSourceCleanupFailFirstInserter struct {
	base      publicationSourceCleanupTestInserter
	calls     int
	failures  int
	successes int
	err       error
}

func (inserter *publicationSourceCleanupFailFirstInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := inserter.base.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return nil, err
	}
	inserter.calls++
	if inserter.calls == 1 {
		inserter.failures++
		return nil, inserter.err
	}
	inserter.successes++
	return result, nil
}

func insertCurrentCleanupRequest(t *testing.T, f publicationExecutionFixture) {
	t.Helper()
	if _, err := f.db.ExecContext(context.Background(), `INSERT INTO publication_source_cleanup_request(originating_operation_id,position_id,output_epoch,publication_generation) VALUES (?,?,?,?)`, f.guard.OperationID, f.guard.ExpectedPositionID, f.guard.Epoch, f.guard.CandidateGeneration); err != nil {
		t.Fatal(err)
	}
}

func TestConsumeCurrentPublicationSourceCleanupRequestEnqueuesAtomicallyPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	insertCurrentCleanupRequest(t, f)
	inserter := new(publicationSourceCleanupTestInserter)
	result, err := repo.ConsumePublicationSourceCleanupRequest(ctx, f.guard.OperationID, inserter)
	if err != nil || result.OperationID == uuid.Nil || len(result.Blockers) != 0 {
		t.Fatalf("consume current cleanup request = %+v, %v", result, err)
	}
	var requests, jobs, cleanupOps int
	for _, check := range []struct {
		query  string
		target *int
	}{{`SELECT count(*) FROM publication_source_cleanup_request`, &requests}, {`SELECT count(*) FROM river_job WHERE kind='cleanup_publication_sources_v1'`, &jobs}, {`SELECT count(*) FROM operation WHERE kind='cleanup_publication_sources_v1'`, &cleanupOps}} {
		if err := f.db.QueryRowContext(ctx, check.query).Scan(check.target); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 0 || jobs != 1 || cleanupOps != 1 {
		t.Fatalf("consumed request artifacts requests=%d jobs=%d cleanup operations=%d", requests, jobs, cleanupOps)
	}
}

func TestPublicationSourceCleanupRequestUpsertResetsAttemptTimePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	insertCurrentCleanupRequest(t, f)
	if _, err := f.db.ExecContext(ctx, `UPDATE publication_source_cleanup_request SET last_attempted_at=clock_timestamp() WHERE originating_operation_id=?`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repo.requestSourceCleanupTx(ctx, tx, f.guard.OperationID, f.guard.ExpectedPositionID, f.guard.Epoch, f.guard.CandidateGeneration)
	}); err != nil {
		t.Fatal(err)
	}
	var attemptedAt sql.NullTime
	if err := f.db.NewRaw(`SELECT last_attempted_at FROM publication_source_cleanup_request WHERE originating_operation_id=?`, f.guard.OperationID).Scan(ctx, &attemptedAt); err != nil {
		t.Fatal(err)
	}
	if attemptedAt.Valid {
		t.Fatalf("re-request retained last attempt timestamp %s", attemptedAt.Time)
	}
}

func TestDispatchPublicationSourceCleanupRotatesPastBlockedBatchPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantS := publicationSourceCleanupReadyFixture(t)
	// Establish a real live cleanup owner for this source. Every cloned release
	// position deliberately retains the same track_id, so its existing
	// track_source association makes it a borrower of this owned variant too.
	owner, err := repo.AdmitPublicationSourceCleanup(ctx, PublicationSourceCleanupAdmission{
		PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration,
	}, new(publicationSourceCleanupTestInserter))
	if err != nil || owner.OperationID == uuid.Nil {
		t.Fatalf("admit cleanup owner for blocked source: %+v, %v", owner, err)
	}
	ids := make([]uuid.UUID, publicationSourceCleanupRequestBatch)
	for i := range ids {
		ids[i] = uuid.New()
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	for i, id := range ids {
		// Clone the fixture's valid publication operation snapshot rather than
		// manufacturing an invalid empty operation snapshot.
		if _, err := f.db.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,finished_at)
			SELECT ?,kind,'succeeded','succeeded',input_snapshot,attempt,clock_timestamp() FROM operation WHERE id=?`, id, f.guard.OperationID); err != nil {
			t.Fatal(err)
		}
		positionID := uuid.New()
		if _, err := f.db.ExecContext(ctx, `INSERT INTO release_track(release_id,position,track_id,recording_id,medium,displayed_number,id)
			SELECT release_id,?,track_id,recording_id,medium,displayed_number,? FROM release_track WHERE id=?`, 100+i, positionID, f.guard.ExpectedPositionID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at)
			SELECT ?,epoch,generation,relative_path || ?,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at FROM track_publication WHERE position_id=?`, positionID, "/"+positionID.String(), f.guard.ExpectedPositionID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(ctx, `INSERT INTO publication_source_cleanup_request(originating_operation_id,position_id,output_epoch,publication_generation) VALUES (?,?,?,?)`, id, positionID, f.guard.Epoch, f.guard.CandidateGeneration); err != nil {
			t.Fatal(err)
		}
	}
	blocked, err := repo.ConsumePublicationSourceCleanupRequest(ctx, ids[0], new(publicationSourceCleanupTestInserter))
	if err != nil || blocked.OperationID != uuid.Nil || len(blocked.Blockers) == 0 {
		t.Fatalf("request sharing cleanup-owned source = %+v, %v; want actual blocker", blocked, err)
	}
	if consumed, err := repo.DispatchPendingPublicationSourceCleanupRequests(ctx, new(publicationSourceCleanupTestInserter)); err != nil || consumed != 0 {
		t.Fatalf("first blocked batch dispatch = %d, %v", consumed, err)
	}
	var workS SourceAnalysisWork
	if err := f.db.NewSelect().Model(&workS).Where("id IN (SELECT work_id FROM source_work_variant_identity WHERE media_variant_id=?)", variantS).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	addCleanupRetrySourceCopies(t, ctx, f, workS, "dispatch-eligible-T.flac")
	var variantT uuid.UUID
	if err := f.db.NewRaw(`SELECT media_variant_id FROM source_location WHERE relative_path='dispatch-eligible-T.flac'`).Scan(ctx, &variantT); err != nil {
		t.Fatal(err)
	}
	trackT := uuid.New()
	if _, err := f.db.NewRaw(`INSERT INTO local_track(id,title) SELECT ?,title || ' T' FROM local_track WHERE id=(SELECT track_id FROM release_track WHERE id=?)`, trackT, f.guard.ExpectedPositionID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_source SET track_id=? WHERE media_variant_id=?`, trackT, variantT).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	eligiblePosition := uuid.New()
	if _, err := f.db.NewRaw(`INSERT INTO release_track(release_id,position,track_id,recording_id,medium,displayed_number,id)
		SELECT release_id,999,?,recording_id,medium,displayed_number,? FROM release_track WHERE id=?`, trackT, eligiblePosition, f.guard.ExpectedPositionID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at)
		SELECT ?,epoch,generation,relative_path || '/eligible-' || ?,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at FROM track_publication WHERE position_id=?`, eligiblePosition, eligiblePosition.String(), f.guard.ExpectedPositionID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	eligibleOrigin := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	if _, err := f.db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,finished_at)
		SELECT ?,kind,'succeeded','succeeded',input_snapshot,attempt,clock_timestamp() FROM operation WHERE id=?`, eligibleOrigin, f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO publication_source_cleanup_request(originating_operation_id,position_id,output_epoch,publication_generation) VALUES (?,?,?,?)`, eligibleOrigin, eligiblePosition, f.guard.Epoch, f.guard.CandidateGeneration).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var attempted sql.NullTime
	if err := f.db.NewRaw(`SELECT last_attempted_at FROM publication_source_cleanup_request WHERE originating_operation_id=?`, eligibleOrigin).Scan(ctx, &attempted); err != nil {
		t.Fatal(err)
	}
	if attempted.Valid {
		t.Fatal("eligible request outside the first batch was marked attempted")
	}
	if consumed, err := repo.DispatchPendingPublicationSourceCleanupRequests(ctx, new(publicationSourceCleanupTestInserter)); err != nil || consumed != 1 {
		t.Fatalf("second dispatch did not admit eligible request: consumed=%d, err=%v", consumed, err)
	}
	var cleanupJobs int
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='cleanup_publication_sources_v1'`).Scan(ctx, &cleanupJobs); err != nil {
		t.Fatal(err)
	}
	if cleanupJobs != 2 { // Existing S owner plus the eligible T request.
		t.Fatalf("cleanup jobs=%d, want 2 (owner and eligible request)", cleanupJobs)
	}
}

func TestConsumePublicationSourceCleanupRequestEnqueueFailureRetainsRequestPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	insertCurrentCleanupRequest(t, f)
	var before int
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM operation WHERE kind='cleanup_publication_sources_v1'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("insert failure")
	_, err := repo.ConsumePublicationSourceCleanupRequest(ctx, f.guard.OperationID, &publicationSourceCleanupTestInserter{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("consume with failed enqueue = %v, want sentinel", err)
	}
	var requests, after int
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM publication_source_cleanup_request WHERE originating_operation_id=?`, f.guard.OperationID).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM operation WHERE kind='cleanup_publication_sources_v1'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || after != before {
		t.Fatalf("failed enqueue left requests=%d cleanup ops=%d (before %d)", requests, after, before)
	}
}

func TestDispatchPublicationSourceCleanupContinuesAfterConsumerFailurePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantS := publicationSourceCleanupReadyFixture(t)
	insertCurrentCleanupRequest(t, f)
	var workS SourceAnalysisWork
	if err := f.db.NewSelect().Model(&workS).Where("id IN (SELECT work_id FROM source_work_variant_identity WHERE media_variant_id=?)", variantS).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	addCleanupRetrySourceCopies(t, ctx, f, workS, "dispatch-failure-T.flac")
	var variantT uuid.UUID
	if err := f.db.NewRaw(`SELECT media_variant_id FROM source_location WHERE relative_path='dispatch-failure-T.flac'`).Scan(ctx, &variantT); err != nil {
		t.Fatal(err)
	}
	trackT := uuid.New()
	if _, err := f.db.NewRaw(`INSERT INTO local_track(id,title) SELECT ?,title || ' T' FROM local_track WHERE id=(SELECT track_id FROM release_track WHERE id=?)`, trackT, f.guard.ExpectedPositionID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_source SET track_id=? WHERE media_variant_id=?`, trackT, variantT).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	goodPosition := uuid.New()
	if _, err := f.db.NewRaw(`INSERT INTO release_track(release_id,position,track_id,recording_id,medium,displayed_number,id)
		SELECT release_id,999,?,recording_id,medium,displayed_number,? FROM release_track WHERE id=?`, trackT, goodPosition, f.guard.ExpectedPositionID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at)
		SELECT ?,epoch,generation,relative_path || '/good-' || ?,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at FROM track_publication WHERE position_id=?`, goodPosition, goodPosition.String(), f.guard.ExpectedPositionID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	goodOrigin := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	if _, err := f.db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,finished_at)
		SELECT ?,kind,'succeeded','succeeded',input_snapshot,attempt,clock_timestamp() FROM operation WHERE id=?`, goodOrigin, f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO publication_source_cleanup_request(originating_operation_id,position_id,output_epoch,publication_generation) VALUES (?,?,?,?)`, goodOrigin, goodPosition, f.guard.Epoch, f.guard.CandidateGeneration).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("injected first eligible consumer failure")
	// Fail the first actual insertion, without assuming which request RETURNING
	// supplies first. Both requests are eligible and their sources are disjoint.
	inserter := &publicationSourceCleanupFailFirstInserter{err: sentinel}
	consumed, err := repo.DispatchPendingPublicationSourceCleanupRequests(ctx, inserter)
	if !errors.Is(err, sentinel) || consumed != 1 || inserter.failures != 1 || inserter.successes != 1 {
		t.Fatalf("dispatch after eligible consumer failure = consumed %d, error %v, failures %d, successes %d; want 1/1/1", consumed, err, inserter.failures, inserter.successes)
	}
	var failedRequests, goodRequests, jobs int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_cleanup_request WHERE originating_operation_id=?`, f.guard.OperationID).Scan(ctx, &failedRequests); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_cleanup_request WHERE originating_operation_id=?`, goodOrigin).Scan(ctx, &goodRequests); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='cleanup_publication_sources_v1'`).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if failedRequests+goodRequests != 1 || jobs != 1 || inserter.calls != 2 {
		t.Fatalf("after mixed dispatch: S requests=%d T requests=%d cleanup jobs=%d calls=%d; want one retained request, one job and two calls", failedRequests, goodRequests, jobs, inserter.calls)
	}
	var retainedOrigin uuid.UUID
	if err := f.db.NewRaw(`SELECT originating_operation_id FROM publication_source_cleanup_request`).Scan(ctx, &retainedOrigin); err != nil {
		t.Fatal(err)
	}
	result, err := repo.ConsumePublicationSourceCleanupRequest(ctx, retainedOrigin, new(publicationSourceCleanupTestInserter))
	if err != nil || result.OperationID == uuid.Nil {
		t.Fatalf("retry failed eligible request = %+v, %v", result, err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='cleanup_publication_sources_v1'`).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 2 {
		t.Fatalf("cleanup jobs after retry=%d, want successful T dispatch plus S retry", jobs)
	}
}

func TestConsumeStalePublicationSourceCleanupRequestRetiresItPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	if _, err := f.db.ExecContext(ctx, `INSERT INTO publication_source_cleanup_request(originating_operation_id,position_id,output_epoch,publication_generation) VALUES (?,?,?,?)`, f.guard.OperationID, f.guard.ExpectedPositionID, f.guard.Epoch, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ConsumePublicationSourceCleanupRequest(ctx, f.guard.OperationID, new(publicationSourceCleanupTestInserter)); err != nil {
		t.Fatal(err)
	}
	var requests int
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM publication_source_cleanup_request`).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("stale generation retained %d cleanup requests", requests)
	}
}

func TestUpdateRuntimeDisablingSourceRemovalDeletesCurrentRequestsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, _ := publicationSourceCleanupReadyFixture(t)
	insertCurrentCleanupRequest(t, f)
	if err := NewSettingsRepository(f.db).UpdateRuntime(ctx, "", "", map[string]string{"publication_remove_sources": "false"}); err != nil {
		t.Fatal(err)
	}
	var requests int
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM publication_source_cleanup_request`).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("disabling source removal retained %d requests", requests)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value) VALUES ('publication_remove_sources','true') ON CONFLICT(setting_name) DO UPDATE SET setting_value='true'`); err != nil {
		t.Fatal(err)
	}
	var revived int
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM publication_source_cleanup_request`).Scan(&revived); err != nil {
		t.Fatal(err)
	}
	if revived != 0 {
		t.Fatalf("enabling source removal revived %d old requests", revived)
	}
}
