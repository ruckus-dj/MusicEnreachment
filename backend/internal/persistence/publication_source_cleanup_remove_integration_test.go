//go:build integration

package persistence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/uptrace/bun"
)

func setupStartedCleanupRemoval(t *testing.T) (context.Context, publicationExecutionFixture, *SetupManagerRepository, PublicationSourceCleanupDeliveryGuard, uuid.UUID, uuid.UUID, PublicationSourceCleanupRemovalRequest) {
	t.Helper()
	ctx := context.Background()
	f, repo, variantID := publicationSourceCleanupReadyFixture(t)
	if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-remove-v1' WHERE media_variant_id=?`, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	guard, operationID := admitAndClaimSourceCleanup(t, ctx, f, repo)
	started, err := repo.StartPublicationSourceCleanupExecution(ctx, guard)
	if err != nil || !started {
		t.Fatalf("start cleanup = %v, %v", started, err)
	}
	var itemID uuid.UUID
	if err := f.db.NewRaw(`SELECT id FROM source_cleanup_item WHERE operation_id=?`, operationID).Scan(ctx, &itemID); err != nil {
		t.Fatal(err)
	}
	var snapshot []byte
	if err := f.db.NewRaw(`SELECT source_snapshot FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &snapshot); err != nil {
		t.Fatal(err)
	}
	var frozen publicationSourceCleanupSnapshot
	if err := decodeStrictJSON(snapshot, &frozen); err != nil {
		t.Fatal(err)
	}
	mtime, err := time.Parse(time.RFC3339Nano, frozen.Mtime)
	if err != nil {
		t.Fatal(err)
	}
	request := PublicationSourceCleanupRemovalRequest{ConfiguredRootPath: frozen.ConfiguredPath, RelativePath: frozen.RelativePath, SizeBytes: frozen.SizeBytes, MTime: mtime}
	return ctx, f, repo, guard, operationID, itemID, request
}

func TestRemovePublicationSourceCleanupItemCommitsFrontierBeforeMutationAndReplaysSafelyPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, f, repo, guard, operationID, itemID, want := setupStartedCleanupRemoval(t)
	called := false
	remove := func(callbackCtx context.Context, got PublicationSourceCleanupRemovalRequest) error {
		called = true
		if got.ConfiguredRootPath != want.ConfiguredRootPath || got.RelativePath != want.RelativePath || got.SizeBytes != want.SizeBytes || !got.MTime.Equal(want.MTime) {
			t.Errorf("callback request = %+v, want frozen %+v", got, want)
		}
		// Separate connection observes only committed state; seeing the frontier
		// here proves the filesystem callback runs after its transaction commits.
		var startedAt any
		if err := f.db.NewRaw(`SELECT mutation_started_at FROM source_cleanup_item WHERE id=?`, itemID).Scan(callbackCtx, &startedAt); err != nil {
			return err
		}
		if startedAt == nil {
			t.Error("mutation frontier was not committed before callback")
		}
		return nil
	}
	outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, itemID, remove)
	if err != nil || outcome != PublicationSourceCleanupRemovalCompleted || !called {
		t.Fatalf("remove = %v, %v, called=%v", outcome, err, called)
	}
	var completed any
	var itemState string
	if err := f.db.NewRaw(`SELECT state,mutation_completed_at FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &itemState, &completed); err != nil {
		t.Fatal(err)
	}
	if itemState != "claimed" || completed == nil {
		t.Fatalf("completion state=%q completed=%v", itemState, completed)
	}
	var markers int
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_completion WHERE work_id=(SELECT work_id FROM source_cleanup_owner WHERE item_id=?)`, itemID).Scan(ctx, &markers); err != nil || markers != 1 {
		t.Fatalf("native completion markers=%d, %v", markers, err)
	}
	// The public assessment remains advisory and unchanged; only the internal
	// automatic-cleanup projection suppresses an exact handled identity.
	publicAssessment, err := repo.ReadPublicationSourceCleanupAssessment(ctx, guard.PositionID)
	if err != nil || len(publicAssessment.Candidates) != 1 {
		t.Fatalf("public cleanup assessment candidates=%d, %v", len(publicAssessment.Candidates), err)
	}
	var filtered int
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		assessment := PublicationSourceCleanupAssessment{PositionID: guard.PositionID}
		if err := readPublicationSourceCleanupAssessmentTx(ctx, tx, guard.PositionID, &assessment); err != nil {
			return err
		}
		if err := filterHandledSourceCleanupAssessment(ctx, tx, &assessment); err != nil {
			return err
		}
		filtered = len(assessment.Candidates)
		return nil
	}); err != nil || filtered != 0 {
		t.Fatalf("internal cleanup projection candidates=%d, %v", filtered, err)
	}
	called = false
	outcome, err = repo.RemovePublicationSourceCleanupItem(ctx, guard, itemID, remove)
	if err != nil || outcome != PublicationSourceCleanupRemovalAlreadyCompleted || called {
		t.Fatalf("replay = %v,%v called=%v", outcome, err, called)
	}
	assertCleanupRemovalRetained(t, ctx, f, operationID)
	if settled, err := repo.SettlePublicationSourceCleanup(ctx, guard); err != nil || !settled {
		t.Fatalf("settle completed cleanup = %t, %v", settled, err)
	}
	var settledOwners, settledItems, settledMarkers int
	for query, target := range map[string]*int{
		`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`: &settledOwners,
		`SELECT count(*) FROM source_cleanup_item WHERE operation_id=?`:  &settledItems,
		`SELECT count(*) FROM source_cleanup_completion`:                 &settledMarkers,
	} {
		if err := f.db.NewRaw(query, operationID).Scan(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	if settledOwners != 0 || settledItems != 0 || settledMarkers != 1 {
		t.Fatalf("settled owners=%d items=%d durable markers=%d", settledOwners, settledItems, settledMarkers)
	}
}

func TestRemovePublicationSourceCleanupItemFailureIsBlockedAndNotRetriedPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{errors.New("unlink rejected"), os.ErrNotExist} {
		t.Run(cause.Error(), func(t *testing.T) {
			ctx, f, repo, guard, operationID, itemID, _ := setupStartedCleanupRemoval(t)
			calls := 0
			remove := func(context.Context, PublicationSourceCleanupRemovalRequest) error { calls++; return cause }
			outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, itemID, remove)
			if outcome != PublicationSourceCleanupRemovalBlocked || !errors.Is(err, cause) || calls != 1 {
				t.Fatalf("failure %v => %v,%v calls=%d", cause, outcome, err, calls)
			}
			var started, completed any
			if err := f.db.NewRaw(`SELECT mutation_started_at,mutation_completed_at FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &started, &completed); err != nil {
				t.Fatal(err)
			}
			if started == nil || completed != nil {
				t.Fatalf("failure frontier started=%v completed=%v", started, completed)
			}
			var markers int
			if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_completion`).Scan(ctx, &markers); err != nil || markers != 0 {
				t.Fatalf("failed unlink completion markers=%d, %v", markers, err)
			}
			outcome, err = repo.RemovePublicationSourceCleanupItem(ctx, guard, itemID, remove)
			if outcome != PublicationSourceCleanupRemovalBlocked || err != nil || calls != 1 {
				t.Fatalf("unsafe retry = %v,%v calls=%d", outcome, err, calls)
			}
			assertCleanupRemovalRetained(t, ctx, f, operationID)
		})
	}
}

func TestRemovePublicationSourceCleanupItemConcurrentSingleCallbackPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, _, repo, guard, _, itemID, _ := setupStartedCleanupRemoval(t)
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	remove := func(context.Context, PublicationSourceCleanupRemovalRequest) error {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return nil
	}
	results := make(chan PublicationSourceCleanupRemovalOutcome, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, itemID, remove)
			results <- result
			errs <- err
		}()
	}
	<-entered
	close(release)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	completed, blocked := 0, 0
	for result := range results {
		switch result {
		case PublicationSourceCleanupRemovalCompleted:
			completed++
		case PublicationSourceCleanupRemovalBlocked, PublicationSourceCleanupRemovalAlreadyCompleted:
			blocked++
		default:
			t.Fatalf("unexpected outcome %v", result)
		}
	}
	if calls.Load() != 1 || completed != 1 || blocked != 1 {
		t.Fatalf("calls=%d completed=%d blocked=%d", calls.Load(), completed, blocked)
	}
}

func TestRemovePublicationSourceCleanupItemDeferredCommitFailureDoesNotRetryPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, f, repo, guard, _, itemID, _ := setupStartedCleanupRemoval(t)
	if _, err := f.db.ExecContext(ctx, `CREATE FUNCTION fail_cleanup_completion_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.mutation_completed_at IS NOT NULL THEN RAISE EXCEPTION 'injected completion commit failure'; END IF; RETURN NEW; END $$; CREATE CONSTRAINT TRIGGER fail_cleanup_completion_commit AFTER UPDATE ON source_cleanup_item DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_cleanup_completion_commit()`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	remove := func(context.Context, PublicationSourceCleanupRemovalRequest) error { calls++; return nil }
	outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, itemID, remove)
	if outcome != PublicationSourceCleanupRemovalBlocked || err == nil || !strings.Contains(err.Error(), "injected completion commit failure") || calls != 1 {
		t.Fatalf("commit failure=%v,%v calls=%d", outcome, err, calls)
	}
	var started, completed any
	if err := f.db.NewRaw(`SELECT mutation_started_at,mutation_completed_at FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &started, &completed); err != nil {
		t.Fatal(err)
	}
	if started == nil || completed != nil {
		t.Fatalf("commit rollback frontier started=%v completed=%v", started, completed)
	}
	var markers int
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_completion`).Scan(ctx, &markers); err != nil || markers != 0 {
		t.Fatalf("rolled-back completion markers=%d, %v", markers, err)
	}
	if _, err := f.db.ExecContext(ctx, `DROP TRIGGER fail_cleanup_completion_commit ON source_cleanup_item; DROP FUNCTION fail_cleanup_completion_commit()`); err != nil {
		t.Fatal(err)
	}
	outcome, err = repo.RemovePublicationSourceCleanupItem(ctx, guard, itemID, remove)
	if outcome != PublicationSourceCleanupRemovalBlocked || err != nil || calls != 1 {
		t.Fatalf("retry after uncertain mutation=%v,%v calls=%d", outcome, err, calls)
	}
}

func TestRemovePublicationSourceCleanupItemRechecksPermissionAfterFrontierPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, f, repo, guard, _, itemID, _ := setupStartedCleanupRemoval(t)
	if _, err := f.db.ExecContext(ctx, `CREATE FUNCTION disable_cleanup_after_frontier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.mutation_started_at IS NULL AND NEW.mutation_started_at IS NOT NULL AND NEW.mutation_completed_at IS NULL THEN UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'; END IF; RETURN NEW; END $$; CREATE CONSTRAINT TRIGGER disable_cleanup_after_frontier AFTER UPDATE ON source_cleanup_item DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION disable_cleanup_after_frontier()`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { calls++; return nil })
	if err != nil || outcome != PublicationSourceCleanupRemovalBlocked || calls != 0 {
		t.Fatalf("permission recheck=%v,%v calls=%d", outcome, err, calls)
	}
	var started, completed any
	if err := f.db.NewRaw(`SELECT mutation_started_at,mutation_completed_at FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &started, &completed); err != nil {
		t.Fatal(err)
	}
	if started == nil || completed != nil {
		t.Fatalf("blocked frontier started=%v completed=%v", started, completed)
	}
}

func TestRemovePublicationSourceCleanupItemRejectsNilCallbackPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, f, repo, guard, _, itemID, _ := setupStartedCleanupRemoval(t)
	called := false
	if outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, uuid.Nil, func(context.Context, PublicationSourceCleanupRemovalRequest) error { called = true; return nil }); err == nil || outcome != PublicationSourceCleanupRemovalBlocked || called {
		t.Fatalf("nil item = %v, %v, called=%t", outcome, err, called)
	}
	outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, itemID, nil)
	if err == nil || outcome != PublicationSourceCleanupRemovalBlocked {
		t.Fatalf("nil callback = %v, %v; want blocked error", outcome, err)
	}
	var started, completed any
	if err := f.db.NewRaw(`SELECT mutation_started_at,mutation_completed_at FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &started, &completed); err != nil {
		t.Fatal(err)
	}
	if started != nil || completed != nil {
		t.Fatalf("nil callback advanced frontier: started=%v completed=%v", started, completed)
	}
}

func TestRemovePublicationSourceCleanupItemsRemovesTwoRealFilesPostgreSQL(t *testing.T) {
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
	guard, operationID := admitAndClaimSourceCleanup(t, ctx, f, repo)
	if started, err := repo.StartPublicationSourceCleanupExecution(ctx, guard); err != nil || !started {
		t.Fatalf("start two-source cleanup=%t,%v", started, err)
	}
	var items []SourceCleanupItem
	if err := f.db.NewSelect().Model(&items).Where("operation_id=?", operationID).Order("id ASC").Scan(ctx); err != nil || len(items) != 2 {
		t.Fatalf("cleanup items=%d,%v", len(items), err)
	}
	for _, item := range items {
		outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, item.ID, func(ctx context.Context, request PublicationSourceCleanupRemovalRequest) error {
			return (sourcefs.SourceRemover{}).Remove(ctx, request.ConfiguredRootPath, request.RelativePath, request.SizeBytes, request.MTime)
		})
		if err != nil || outcome != PublicationSourceCleanupRemovalCompleted {
			t.Fatalf("remove item %s=%s,%v", item.ID, outcome, err)
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
}

func assertCleanupRemovalRetained(t *testing.T, ctx context.Context, f publicationExecutionFixture, operationID uuid.UUID) {
	t.Helper()
	var owners, claims int
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, operationID).Scan(ctx, &owners); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, operationID).Scan(ctx, &claims); err != nil {
		t.Fatal(err)
	}
	if owners == 0 || claims == 0 {
		t.Fatalf("retained cleanup owners=%d claims=%d", owners, claims)
	}
}
