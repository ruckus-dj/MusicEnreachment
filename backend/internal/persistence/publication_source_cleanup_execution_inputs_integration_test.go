//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func TestValidatePublicationSourceCleanupExecutionInputsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantID := publicationSourceCleanupReadyFixture(t)
	if _, err := f.db.NewRaw(`UPDATE source_root SET status=?`, SourceRootStatusAvailable).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-validation-v1' WHERE media_variant_id=?`, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	inserter := new(publicationSourceCleanupTestInserter)
	request := PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}
	admitted, err := repo.AdmitPublicationSourceCleanup(ctx, request, inserter)
	if err != nil || admitted.OperationID == uuid.Nil {
		t.Fatalf("admit cleanup = %+v, %v", admitted, err)
	}
	args, err := json.Marshal(inserter.args)
	if err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := f.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, admitted.OperationID).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='running',attempt=1,args=?::jsonb WHERE id=?`, string(args), jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	guard := PublicationSourceCleanupDeliveryGuard{OperationID: admitted.OperationID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "cleanup-validation", Epoch: f.guard.Epoch, PositionID: request.PositionID}
	if got, err := repo.ClaimPublicationSourceCleanupDelivery(ctx, guard); err != nil || got != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", got, err)
	}
	validate := func() bool {
		t.Helper()
		valid := false
		err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			var err error
			valid, err = validatePublicationSourceCleanupExecutionInputsTx(ctx, tx, guard)
			return err
		})
		if err != nil {
			t.Fatalf("validate inputs: %v", err)
		}
		return valid
	}
	if !validate() {
		t.Fatal("fresh admitted and claimed delivery did not validate")
	}
	// The validator observes retained execution inputs; it must not alter any
	// durable operation, item, owner, or delivery-claim state itself.
	var before string
	const evidence = `SELECT jsonb_build_object(
		'operation',(SELECT to_jsonb(o) FROM operation o WHERE id=?),
		'items',(SELECT COALESCE(jsonb_agg(to_jsonb(i) ORDER BY id),'[]') FROM source_cleanup_item i WHERE operation_id=?),
		'owners',(SELECT COALESCE(jsonb_agg(to_jsonb(o) ORDER BY item_id),'[]') FROM source_cleanup_owner o WHERE operation_id=?),
		'claims',(SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY application_attempt),'[]') FROM publication_execution_claim c WHERE operation_id=?))::text`
	if err := f.db.NewRaw(evidence, admitted.OperationID, admitted.OperationID, admitted.OperationID, admitted.OperationID).Scan(ctx, &before); err != nil {
		t.Fatal(err)
	}
	if !validate() {
		t.Fatal("repeated validation blocked an unchanged delivery")
	}
	var after string
	if err := f.db.NewRaw(evidence, admitted.OperationID, admitted.OperationID, admitted.OperationID, admitted.OperationID).Scan(ctx, &after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("validator changed durable execution evidence:\nbefore %s\nafter %s", before, after)
	}
	rollbackMutation := errors.New("roll back evidence mutation")
	for _, mutation := range []struct {
		name, query string
		argument    any
	}{
		{"source size", `UPDATE source_location SET size_bytes=size_bytes+1 WHERE id IN (SELECT source_location_id FROM source_cleanup_owner WHERE operation_id=?)`, admitted.OperationID},
		{"source mtime", `UPDATE source_location SET mtime=mtime+interval '1 microsecond' WHERE id IN (SELECT source_location_id FROM source_cleanup_owner WHERE operation_id=?)`, admitted.OperationID},
		{"source root disabled", `UPDATE source_root SET enabled=false WHERE id IN (SELECT source_root_id FROM source_cleanup_owner WHERE operation_id=?)`, admitted.OperationID},
		{"link selection", `UPDATE track_source SET selected=NOT selected WHERE media_variant_id=?`, variantID},
		{"link revision", `UPDATE track_source SET updated_at=updated_at+interval '1 microsecond' WHERE media_variant_id=?`, variantID},
		{"link policy value", `UPDATE track_source SET policy_version='cleanup-validation-v2' WHERE media_variant_id=?`, variantID},
		{"link removal", `DELETE FROM track_source WHERE media_variant_id=?`, variantID},
		{"borrower validation", `UPDATE track_publication SET validated_at=validated_at+interval '1 microsecond' WHERE position_id=?`, request.PositionID},
		{"foreign publication hold", `INSERT INTO publication_source_hold(operation_id,application_attempt,river_job_id,epoch,hold_uuid,source_root_id,source_location_id,work_id,media_variant_id,configured_path,inventory_path,relative_path,size_bytes,mtime)
			SELECT op.id,op.attempt,op.river_job_id,a.epoch,'22222222-2222-4222-8222-222222222222',o.source_root_id,o.source_location_id,o.work_id,l.media_variant_id,r.configured_path,r.inventory_path,l.relative_path,l.size_bytes,l.mtime
			FROM operation op JOIN track_publication_attempt a ON a.operation_id=op.id CROSS JOIN source_cleanup_owner o
			JOIN source_location l ON l.id=o.source_location_id JOIN source_root r ON r.id=o.source_root_id WHERE op.id=?`, f.guard.OperationID},
		{"owner root mismatch", `UPDATE source_cleanup_owner SET source_root_id='11111111-1111-4111-8111-111111111111' WHERE operation_id=?`, admitted.OperationID},
		{"retained operation source work hold", `INSERT INTO operation_source_work_hold(operation_id,work_id) SELECT ?,work_id FROM source_cleanup_owner WHERE operation_id=?`, []any{f.guard.OperationID, admitted.OperationID}},
		{"output epoch", `UPDATE publication_namespace_state SET output_epoch=? WHERE singleton`, uuid.New()},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				var execErr error
				if args, ok := mutation.argument.([]any); ok {
					_, execErr = tx.NewRaw(mutation.query, args...).Exec(ctx)
				} else {
					_, execErr = tx.NewRaw(mutation.query, mutation.argument).Exec(ctx)
				}
				if execErr != nil {
					return execErr
				}
				valid, err := validatePublicationSourceCleanupExecutionInputsTx(ctx, tx, guard)
				if err != nil {
					return err
				}
				if valid {
					t.Errorf("changed %s validated", mutation.name)
				}
				return rollbackMutation
			})
			if !errors.Is(err, rollbackMutation) {
				t.Fatalf("validate changed evidence: %v", err)
			}
		})
	}
	if !validate() {
		t.Fatal("rolled-back evidence mutations changed the valid baseline")
	}
	// A stale worker attempt and a stale retained claim must each block without
	// modifying the remaining execution state.
	if _, err := f.db.NewRaw(`UPDATE river_job SET attempt=2 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if validate() {
		t.Fatal("stale River attempt validated")
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE publication_execution_claim SET delivery_token='stale' WHERE operation_id=?`, admitted.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if validate() {
		t.Fatal("stale delivery token validated")
	}
	if _, err := f.db.NewRaw(`UPDATE publication_execution_claim SET delivery_token=? WHERE operation_id=?`, guard.DeliveryToken, admitted.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !validate() {
		t.Fatal("restored delivery token did not validate before permission change")
	}
	if _, err := f.db.NewRaw(`UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if validate() {
		t.Fatal("disabled permission validated")
	}
}

func TestStartPublicationSourceCleanupExecutionTransitionsAndRetainsEvidencePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantID := publicationSourceCleanupReadyFixture(t)
	if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-validation-v1' WHERE media_variant_id=?`, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	guard, operationID := admitAndClaimSourceCleanup(t, ctx, f, repo)
	var before string
	const evidence = `SELECT jsonb_build_object('operation',(SELECT to_jsonb(o)-'state'-'stage'-'started_at'-'updated_at' FROM operation o WHERE id=?),'items',(SELECT jsonb_agg(to_jsonb(i)-'state' ORDER BY id) FROM source_cleanup_item i WHERE operation_id=?),'owners',(SELECT jsonb_agg(to_jsonb(o) ORDER BY item_id) FROM source_cleanup_owner o WHERE operation_id=?),'claims',(SELECT jsonb_agg(to_jsonb(c) ORDER BY application_attempt) FROM publication_execution_claim c WHERE operation_id=?))::text`
	read := func() string {
		t.Helper()
		var v string
		if err := f.db.NewRaw(evidence, operationID, operationID, operationID, operationID).Scan(ctx, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before = read()
	started, err := repo.StartPublicationSourceCleanupExecution(ctx, guard)
	if err != nil || !started {
		t.Fatalf("start = %v, %v", started, err)
	}
	started, err = repo.StartPublicationSourceCleanupExecution(ctx, guard)
	if err != nil || started {
		t.Fatalf("repeat start = %v, %v; want false,nil", started, err)
	}
	var opState, stage, itemState string
	var startedAt any
	var items, owners, claims int
	if err := f.db.NewRaw(`SELECT state,stage,started_at FROM operation WHERE id=?`, operationID).Scan(ctx, &opState, &stage, &startedAt); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT min(state),count(*) FROM source_cleanup_item WHERE operation_id=?`, operationID).Scan(ctx, &itemState, &items); err != nil {
		t.Fatal(err)
	}
	for query, target := range map[string]*int{`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`: &owners, `SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`: &claims} {
		if err := f.db.NewRaw(query, operationID).Scan(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	if opState != "running" || stage != "running" || startedAt == nil || items != 1 || itemState != "claimed" || owners != 1 || claims != 1 {
		t.Fatalf("transition op=%s/%s started=%v items=%d/%s owners=%d claims=%d", opState, stage, startedAt, items, itemState, owners, claims)
	}
	if after := read(); after != before {
		t.Fatalf("start changed retained evidence:\nbefore %s\nafter %s", before, after)
	}
}

func TestStartPublicationSourceCleanupExecutionRejectsStaleAndDisabledPermissionPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantID := publicationSourceCleanupReadyFixture(t)
	if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-validation-v1' WHERE media_variant_id=?`, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	guard, operationID := admitAndClaimSourceCleanup(t, ctx, f, repo)
	if _, err := f.db.NewRaw(`UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	started, err := repo.StartPublicationSourceCleanupExecution(ctx, guard)
	if err != nil || started {
		t.Fatalf("disabled start = %v,%v", started, err)
	}
	if err := assertCleanupQueued(ctx, f, operationID); err != nil {
		t.Fatal(err)
	}
	if err := NewSettingsRepository(f.db).Set(ctx, "publication_remove_sources", "true"); err != nil {
		t.Fatal(err)
	}
	stale := guard
	stale.DeliveryToken = "stale"
	started, err = repo.StartPublicationSourceCleanupExecution(ctx, stale)
	if err != nil || started {
		t.Fatalf("stale guard start = %v,%v", started, err)
	}
	if err := assertCleanupQueued(ctx, f, operationID); err != nil {
		t.Fatal(err)
	}
	started, err = repo.StartPublicationSourceCleanupExecution(ctx, guard)
	if err != nil || !started {
		t.Fatalf("restored valid start = %v,%v", started, err)
	}
}

func TestStartPublicationSourceCleanupExecutionRollsBackPartialItemClaimPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantID := publicationSourceCleanupReadyFixture(t)
	if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-validation-v1' WHERE media_variant_id=?`, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	guard, operationID := admitAndClaimSourceCleanup(t, ctx, f, repo)
	if _, err := f.db.ExecContext(ctx, `CREATE FUNCTION fail_cleanup_claim() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected cleanup claim failure'; END $$; CREATE TRIGGER fail_cleanup_claim BEFORE UPDATE ON source_cleanup_item FOR EACH ROW WHEN (NEW.state='claimed') EXECUTE FUNCTION fail_cleanup_claim()`); err != nil {
		t.Fatal(err)
	}
	started, err := repo.StartPublicationSourceCleanupExecution(ctx, guard)
	if err == nil || started || !strings.Contains(err.Error(), "injected cleanup claim failure") {
		t.Fatalf("failed start = %v,%v", started, err)
	}
	if err := assertCleanupQueued(ctx, f, operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `DROP TRIGGER fail_cleanup_claim ON source_cleanup_item; DROP FUNCTION fail_cleanup_claim()`); err != nil {
		t.Fatal(err)
	}
	started, err = repo.StartPublicationSourceCleanupExecution(ctx, guard)
	if err != nil || !started {
		t.Fatalf("start after removing trigger = %v,%v", started, err)
	}
}

func TestStartPublicationSourceCleanupExecutionConcurrentSingleWinnerPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantID := publicationSourceCleanupReadyFixture(t)
	if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-validation-v1' WHERE media_variant_id=?`, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	guard, _ := admitAndClaimSourceCleanup(t, ctx, f, repo)
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := repo.StartPublicationSourceCleanupExecution(ctx, guard)
			results <- v
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	count := 0
	for v := range results {
		if v {
			count++
		}
	}
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if count != 1 {
		t.Fatalf("concurrent successful starts=%d, want 1", count)
	}
}

func admitAndClaimSourceCleanup(t *testing.T, ctx context.Context, f publicationExecutionFixture, repo *SetupManagerRepository) (PublicationSourceCleanupDeliveryGuard, uuid.UUID) {
	t.Helper()
	if _, err := f.db.NewRaw(`UPDATE source_root SET status=?`, SourceRootStatusAvailable).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	request := PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}
	inserter := new(publicationSourceCleanupTestInserter)
	result, err := repo.AdmitPublicationSourceCleanup(ctx, request, inserter)
	if err != nil || result.OperationID == uuid.Nil {
		t.Fatalf("admit cleanup=%+v,%v", result, err)
	}
	args, err := json.Marshal(inserter.args)
	if err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := f.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, result.OperationID).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='running',attempt=1,args=?::jsonb WHERE id=?`, string(args), jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	guard := PublicationSourceCleanupDeliveryGuard{OperationID: result.OperationID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "cleanup-start", Epoch: f.guard.Epoch, PositionID: request.PositionID}
	if got, err := repo.ClaimPublicationSourceCleanupDelivery(ctx, guard); err != nil || got != PublicationDeliveryAcquired {
		t.Fatalf("claim=%q,%v", got, err)
	}
	return guard, result.OperationID
}

func assertCleanupQueued(ctx context.Context, f publicationExecutionFixture, id uuid.UUID) error {
	var state, stage, item string
	var startedAt any
	if err := f.db.NewRaw(`SELECT state,stage,started_at FROM operation WHERE id=?`, id).Scan(ctx, &state, &stage, &startedAt); err != nil {
		return err
	}
	if err := f.db.NewRaw(`SELECT min(state) FROM source_cleanup_item WHERE operation_id=?`, id).Scan(ctx, &item); err != nil {
		return err
	}
	if state != "queued" || stage != "queued" || startedAt != nil || item != "queued" {
		return errors.New("failed start did not roll back operation/item state")
	}
	return nil
}
