//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
)

func TestPublicationSourceCleanupTwoItemLifecyclePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantID := publicationSourceCleanupReadyFixture(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var work SourceAnalysisWork
	if err := f.db.NewSelect().Model(&work).Where("id IN (SELECT work_id FROM source_work_variant_identity WHERE media_variant_id=?)", variantID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE source_root SET configured_path=?,inventory_path=?`,
		`UPDATE source_analysis_work SET configured_path=?,inventory_path=?`,
	} {
		if _, err := f.db.NewRaw(query, root, root).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	secondVariant, secondLocation, secondWork := uuid.New(), uuid.New(), uuid.New()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO media_variant(id,size_bytes) VALUES (?,0)`, []any{secondVariant}},
		{`INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id)
			SELECT ?,source_root_id,'second.flac',0,mtime,last_seen_scan_generation,'audio',? FROM source_location WHERE id=?`, []any{secondLocation, secondVariant, work.LocationID}},
		{`INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id)
			SELECT ?,?,source_root_id,configured_path,inventory_path,'second.flac',size_bytes,mtime,false,origin_scan_operation_id,? FROM source_analysis_work WHERE id=?`, []any{secondWork, secondLocation, secondLocation, work.ID}},
		{`INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, []any{secondWork, secondVariant}},
		{`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method)
			SELECT ?,track_id,?,false,true,'manual' FROM release_track WHERE id=?`, []any{uuid.New(), secondVariant, f.guard.ExpectedPositionID}},
	} {
		if _, err := f.db.NewRaw(statement.query, statement.args...).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, relative := range []string{work.RelativePath, "second.flac"} {
		path := filepath.Join(root, relative)
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, work.Mtime, work.Mtime); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.NewRaw(`UPDATE source_root SET status=?`, SourceRootStatusAvailable).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	request := PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}
	inserter := new(publicationSourceCleanupTestInserter)
	if _, err := f.db.ExecContext(ctx, `UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`); err != nil {
		t.Fatal(err)
	}
	blocked, err := repo.AdmitPublicationSourceCleanup(ctx, request, inserter)
	if err != nil || blocked.OperationID != uuid.Nil || len(blocked.Blockers) == 0 {
		t.Fatalf("disabled cleanup admission=%+v,%v", blocked, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE app_setting SET setting_value='malformed' WHERE setting_name='publication_remove_sources'`); err != nil {
		t.Fatal(err)
	}
	if malformed, err := repo.AdmitPublicationSourceCleanup(ctx, request, inserter); err == nil || malformed.OperationID != uuid.Nil {
		t.Fatalf("malformed permission admission=%+v,%v; want error and no operation", malformed, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE app_setting SET setting_value='true' WHERE setting_name='publication_remove_sources'`); err != nil {
		t.Fatal(err)
	}
	var baselineOperations, baselineJobs int
	if err := f.db.NewRaw(`SELECT count(*) FROM operation`).Scan(ctx, &baselineOperations); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &baselineJobs); err != nil {
		t.Fatal(err)
	}
	enqueueFailure := errors.New("injected River insert failure")
	failedInserter := &publicationSourceCleanupTestInserter{err: enqueueFailure}
	if failed, err := repo.AdmitPublicationSourceCleanup(ctx, request, failedInserter); !errors.Is(err, enqueueFailure) || failed.OperationID != uuid.Nil {
		t.Fatalf("failed River insert admission=%+v,%v", failed, err)
	}
	var operations, stagedItems, owners, jobs int
	for query, target := range map[string]*int{
		`SELECT count(*) FROM operation`:            &operations,
		`SELECT count(*) FROM source_cleanup_item`:  &stagedItems,
		`SELECT count(*) FROM source_cleanup_owner`: &owners,
		`SELECT count(*) FROM river_job`:            &jobs,
	} {
		if err := f.db.NewRaw(query).Scan(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	if operations != baselineOperations || stagedItems != 0 || owners != 0 || jobs != baselineJobs {
		t.Fatalf("failed enqueue leaked operations/items/owners/jobs=%d/%d/%d/%d (baseline operations/jobs=%d/%d)", operations, stagedItems, owners, jobs, baselineOperations, baselineJobs)
	}
	admitted, err := repo.AdmitPublicationSourceCleanup(ctx, request, inserter)
	if err != nil || admitted.OperationID == uuid.Nil || len(admitted.Blockers) != 0 {
		t.Fatalf("admit two-source cleanup=%+v,%v", admitted, err)
	}
	var itemCount, ownerCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_item WHERE operation_id=?`, admitted.OperationID).Scan(ctx, &itemCount); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, admitted.OperationID).Scan(ctx, &ownerCount); err != nil {
		t.Fatal(err)
	}
	if itemCount != 2 || ownerCount != 2 {
		t.Fatalf("admitted source items/owners=%d/%d; want two each", itemCount, ownerCount)
	}
	var jobID int64
	if err := f.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, admitted.OperationID).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(inserter.args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='running',attempt=1,args=?::jsonb WHERE id=?`, string(args), jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	guard := PublicationSourceCleanupDeliveryGuard{OperationID: admitted.OperationID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "cleanup-lifecycle", Epoch: f.guard.Epoch, PositionID: request.PositionID}
	if claimed, err := repo.ClaimPublicationSourceCleanupDelivery(ctx, guard); err != nil || claimed != PublicationDeliveryAcquired {
		t.Fatalf("claim lifecycle delivery=%q,%v", claimed, err)
	}
	if again, err := repo.AdmitPublicationSourceCleanup(ctx, request, inserter); err != nil || again.OperationID != uuid.Nil || len(again.Blockers) == 0 {
		t.Fatalf("duplicate lifecycle admission=%+v,%v; want blocker", again, err)
	}
	operationID := admitted.OperationID
	if _, err := f.db.ExecContext(ctx, `CREATE FUNCTION fail_cleanup_claim() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected cleanup claim failure'; END $$; CREATE TRIGGER fail_cleanup_claim BEFORE UPDATE ON source_cleanup_item FOR EACH ROW WHEN (NEW.state='claimed') EXECUTE FUNCTION fail_cleanup_claim()`); err != nil {
		t.Fatal(err)
	}
	if started, err := repo.StartPublicationSourceCleanupExecution(ctx, guard); err == nil || started || !strings.Contains(err.Error(), "injected cleanup claim failure") {
		t.Fatalf("injected start failure=%t,%v", started, err)
	}
	var queuedOperation, queuedItems int
	if err := f.db.NewRaw(`SELECT count(*) FROM operation WHERE id=? AND state='queued' AND started_at IS NULL`, operationID).Scan(ctx, &queuedOperation); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_item WHERE operation_id=? AND state='queued' AND mutation_started_at IS NULL`, operationID).Scan(ctx, &queuedItems); err != nil {
		t.Fatal(err)
	}
	if queuedOperation != 1 || queuedItems != 2 {
		t.Fatalf("failed start retained queued operation/items=%d/%d; want 1/2", queuedOperation, queuedItems)
	}
	if _, err := f.db.ExecContext(ctx, `DROP TRIGGER fail_cleanup_claim ON source_cleanup_item; DROP FUNCTION fail_cleanup_claim()`); err != nil {
		t.Fatal(err)
	}
	if started, err := repo.StartPublicationSourceCleanupExecution(ctx, guard); err != nil || !started {
		t.Fatalf("start two-source cleanup=%t,%v", started, err)
	}
	var items []SourceCleanupItem
	if err := f.db.NewSelect().Model(&items).Where("operation_id=?", operationID).Order("id ASC").Scan(ctx); err != nil || len(items) != 2 {
		t.Fatalf("cleanup items=%d,%v", len(items), err)
	}
	for _, item := range items {
		calls := 0
		outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, item.ID, func(ctx context.Context, request PublicationSourceCleanupRemovalRequest) error {
			calls++
			var frontier any
			if err := f.db.NewRaw(`SELECT mutation_started_at FROM source_cleanup_item WHERE id=?`, item.ID).Scan(ctx, &frontier); err != nil {
				return err
			}
			if frontier == nil {
				return errors.New("mutation frontier was not committed before filesystem callback")
			}
			return (sourcefs.SourceRemover{}).Remove(ctx, request.ConfiguredRootPath, request.RelativePath, request.SizeBytes, request.MTime)
		})
		if err != nil || outcome != PublicationSourceCleanupRemovalCompleted {
			t.Fatalf("remove item %s=%s,%v", item.ID, outcome, err)
		}
		outcome, err = repo.RemovePublicationSourceCleanupItem(ctx, guard, item.ID, func(context.Context, PublicationSourceCleanupRemovalRequest) error {
			calls++
			return nil
		})
		if err != nil || outcome != PublicationSourceCleanupRemovalAlreadyCompleted || calls != 1 {
			t.Fatalf("completed item replay=%s,%v callback calls=%d", outcome, err, calls)
		}
	}
	for _, relative := range []string{work.RelativePath, "second.flac"} {
		if _, err := os.Lstat(filepath.Join(root, relative)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("removed source %s still exists: %v", relative, err)
		}
	}
	for _, query := range []string{
		`SELECT count(*) FROM source_location`,
		`SELECT count(*) FROM source_analysis_work`,
		`SELECT count(*) FROM track_source`,
		`SELECT count(*) FROM source_cleanup_owner`,
		`SELECT count(*) FROM source_cleanup_item WHERE mutation_completed_at IS NOT NULL`,
	} {
		var count int
		if err := f.db.NewRaw(query).Scan(ctx, &count); err != nil || count != 2 {
			t.Fatalf("retained source facts/completions count=%d,%v for %s", count, err, query)
		}
	}
	assertCleanupRemovalRetained(t, ctx, f, operationID)
	if _, err := f.db.ExecContext(ctx, `CREATE FUNCTION fail_cleanup_lifecycle_settlement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='succeeded' THEN RAISE EXCEPTION 'injected lifecycle settlement failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_cleanup_lifecycle_settlement BEFORE UPDATE ON operation FOR EACH ROW EXECUTE FUNCTION fail_cleanup_lifecycle_settlement()`); err != nil {
		t.Fatal(err)
	}
	if settled, err := repo.SettlePublicationSourceCleanup(ctx, guard); err == nil || settled || !strings.Contains(err.Error(), "injected lifecycle settlement failure") {
		t.Fatalf("injected settlement failure=%t,%v", settled, err)
	}
	assertCleanupRemovalRetained(t, ctx, f, operationID)
	if _, err := f.db.ExecContext(ctx, `DROP TRIGGER fail_cleanup_lifecycle_settlement ON operation; DROP FUNCTION fail_cleanup_lifecycle_settlement()`); err != nil {
		t.Fatal(err)
	}
	if settled, err := repo.SettlePublicationSourceCleanup(ctx, guard); err != nil || !settled {
		t.Fatalf("settle two-item cleanup=%t,%v", settled, err)
	}
	var state string
	if err := f.db.NewRaw(`SELECT state FROM operation WHERE id=?`, operationID).Scan(ctx, &state); err != nil || state != "succeeded" {
		t.Fatalf("settled operation state=%q,%v", state, err)
	}
	for _, table := range []string{"source_location", "source_analysis_work", "track_source"} {
		var count int
		if err := f.db.NewRaw("SELECT count(*) FROM "+table).Scan(ctx, &count); err != nil || count != 2 {
			t.Fatalf("settlement removed retained %s facts: count=%d err=%v", table, count, err)
		}
	}
}
