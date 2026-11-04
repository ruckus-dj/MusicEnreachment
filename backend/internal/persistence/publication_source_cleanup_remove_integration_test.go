//go:build integration

package persistence

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
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
