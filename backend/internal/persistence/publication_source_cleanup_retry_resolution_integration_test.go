//go:build integration

package persistence

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type retryResolutionFixture struct {
	ctx        context.Context
	fixture    publicationExecutionFixture
	repository *SetupManagerRepository
	originalID uuid.UUID
	newGuard   PublicationSourceCleanupDeliveryGuard
	itemIDs    []uuid.UUID
}

func newRetryResolutionFixture(t *testing.T) retryResolutionFixture {
	t.Helper()
	ctx := context.Background()
	fixture, repository, variantID := publicationSourceCleanupReadyFixture(t)
	if _, err := fixture.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-remove-v1' WHERE media_variant_id=?`, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var work SourceAnalysisWork
	if err := fixture.db.NewSelect().Model(&work).Where("id IN (SELECT work_id FROM source_work_variant_identity WHERE media_variant_id=?)", variantID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	// Freeze a native absolute root, including a volume on Windows. The shared
	// read-only protection fixture's "/source" is not an executable Windows root.
	root := t.TempDir()
	if !filepath.IsAbs(root) {
		t.Fatalf("retry source root is not native absolute: %q", root)
	}
	if _, err := fixture.db.NewRaw(`UPDATE source_root SET configured_path=?,inventory_path=? WHERE id=?`, root, root, work.SourceRootID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE source_analysis_work SET configured_path=?,inventory_path=? WHERE source_root_id=?`, root, root, work.SourceRootID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	work.ConfiguredPath, work.InventoryPath = root, root
	addCleanupRetrySourceCopies(t, ctx, fixture, work, "retry-second.flac", "retry-third.flac")
	oldGuard, originalID := admitAndClaimSourceCleanup(t, ctx, fixture, repository)
	if started, err := repository.StartPublicationSourceCleanupExecution(ctx, oldGuard); err != nil || !started {
		t.Fatalf("start original cleanup=%t,%v", started, err)
	}
	var items []SourceCleanupItem
	if err := fixture.db.NewSelect().Model(&items).Where("operation_id=?", originalID).Order("id ASC").Scan(ctx); err != nil || len(items) != 3 {
		t.Fatalf("original cleanup items=%d,%v; want three", len(items), err)
	}
	if outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, oldGuard, items[0].ID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return nil }); err != nil || outcome != PublicationSourceCleanupRemovalCompleted {
		t.Fatalf("complete original first item=%s,%v", outcome, err)
	}
	uncertain := errors.New("callback result is uncertain")
	if outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, oldGuard, items[1].ID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return uncertain }); outcome != PublicationSourceCleanupRemovalBlocked || !errors.Is(err, uncertain) {
		t.Fatalf("leave original second item uncertain=%s,%v", outcome, err)
	}
	if failed, err := repository.FailPublicationSourceCleanupDelivery(ctx, oldGuard, "source removal result is unknown"); err != nil || !failed {
		t.Fatalf("return original delivery=%t,%v", failed, err)
	}
	admitted, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalID, new(publicationSourceCleanupTestInserter))
	if err != nil || admitted.OperationID == uuid.Nil || len(admitted.Blockers) != 0 {
		t.Fatalf("admit explicit retry=%+v,%v", admitted, err)
	}
	var jobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, admitted.OperationID).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var grant SourceCleanupExplicitRetry
	if err := fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id=?", admitted.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	newGuard := PublicationSourceCleanupDeliveryGuard{OperationID: admitted.OperationID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1,
		ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "retry-resolution", Epoch: grant.Epoch, PositionID: grant.PositionID}
	if outcome, err := repository.ClaimPublicationSourceCleanupExplicitRetryDelivery(ctx, newGuard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim explicit retry=%s,%v", outcome, err)
	}
	return retryResolutionFixture{ctx: ctx, fixture: fixture, repository: repository, originalID: originalID, newGuard: newGuard,
		itemIDs: []uuid.UUID{items[0].ID, items[1].ID, items[2].ID}}
}

func TestResolvePublicationSourceCleanupRetryItemPersistsThreeResolutionKindsPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	before := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID)
	for _, tc := range []struct {
		itemID  uuid.UUID
		outcome PublicationSourceCleanupNativeOutcome
		err     error
		want    string
	}{
		{itemID: f.itemIDs[0], want: "original_native_completed"},
		{itemID: f.itemIDs[1], outcome: PublicationSourceCleanupNativeMissingLeaf, err: fs.ErrNotExist, want: "absent_after_explicit_retry"},
		{itemID: f.itemIDs[2], outcome: PublicationSourceCleanupNativeRemoved, want: "native_removed"},
	} {
		calls := 0
		var remove func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error)
		if tc.itemID != f.itemIDs[0] {
			remove = func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
				calls++
				return tc.outcome, tc.err
			}
		}
		outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, tc.itemID, remove)
		if err != nil || outcome != PublicationSourceCleanupRetryResolutionCompleted {
			t.Fatalf("resolve %s=%s,%v", tc.want, outcome, err)
		}
		if (tc.itemID == f.itemIDs[0] && calls != 0) || (tc.itemID != f.itemIDs[0] && calls != 1) {
			t.Fatalf("callback count for %s=%d", tc.want, calls)
		}
		var got string
		if err := f.fixture.db.NewRaw(`SELECT resolution_kind FROM source_cleanup_explicit_retry_resolution WHERE item_id=?`, tc.itemID).Scan(f.ctx, &got); err != nil || got != tc.want {
			t.Fatalf("resolution kind=%q err=%v; want %q", got, err, tc.want)
		}
		if tc.want == "absent_after_explicit_retry" {
			var snapshotRaw []byte
			if err := f.fixture.db.NewRaw(`SELECT source_snapshot FROM source_cleanup_item WHERE id=?`, tc.itemID).Scan(f.ctx, &snapshotRaw); err != nil {
				t.Fatal(err)
			}
			var frozen publicationSourceCleanupSnapshot
			if err := decodeStrictJSON(snapshotRaw, &frozen); err != nil {
				t.Fatalf("decode frozen cleanup item: %v", err)
			}
			var markerKind string
			var markerWorkID, markerLocationID uuid.UUID
			if err := f.fixture.db.NewRaw(`SELECT kind,work_id,source_location_id FROM source_cleanup_completion WHERE work_id=?`, frozen.WorkID).Scan(f.ctx, &markerKind, &markerWorkID, &markerLocationID); err != nil || markerKind != tc.want || markerWorkID != frozen.WorkID || markerLocationID != frozen.SourceLocationID {
				t.Fatalf("completion marker=(%q,%s,%s), err=%v; want absence for frozen work/location (%s,%s)", markerKind, markerWorkID, markerLocationID, err, frozen.WorkID, frozen.SourceLocationID)
			}
			var originalCompleted any
			if err := f.fixture.db.NewRaw(`SELECT mutation_completed_at FROM source_cleanup_item WHERE id=?`, tc.itemID).Scan(f.ctx, &originalCompleted); err != nil || originalCompleted != nil {
				t.Fatalf("original mutation completion=%v err=%v; explicit-retry absence must not complete the original mutation", originalCompleted, err)
			}
		}
		if replay, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, tc.itemID, nil); err != nil || replay != PublicationSourceCleanupRetryResolutionAlreadyCompleted {
			t.Fatalf("ack resolution replay=%s,%v", replay, err)
		}
	}
	if got := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID); got != before {
		t.Fatalf("resolution mutated original cleanup evidence:\nbefore %s\nafter  %s", before, got)
	}
}

func TestResolvePublicationSourceCleanupRetryAbsenceCompletionInsertFailureRollsBackMarkerPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	if _, err := f.fixture.db.ExecContext(f.ctx, `CREATE FUNCTION reject_absence_completion_marker() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.kind='absent_after_explicit_retry' THEN RAISE EXCEPTION 'injected absence marker failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	defer f.fixture.db.ExecContext(f.ctx, `DROP FUNCTION reject_absence_completion_marker() CASCADE`)
	if _, err := f.fixture.db.ExecContext(f.ctx, `CREATE TRIGGER reject_absence_completion_marker BEFORE INSERT ON source_cleanup_completion FOR EACH ROW EXECUTE FUNCTION reject_absence_completion_marker()`); err != nil {
		t.Fatal(err)
	}
	missing := func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		return PublicationSourceCleanupNativeMissingLeaf, fs.ErrNotExist
	}
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[1], missing); outcome != PublicationSourceCleanupRetryResolutionBlocked || err == nil {
		t.Fatalf("resolution with rejected completion marker=%s,%v; want blocked error", outcome, err)
	}
	var markers, completed int
	if err := f.fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_completion WHERE work_id=(SELECT work_id FROM source_cleanup_owner WHERE item_id=?) AND kind='absent_after_explicit_retry'`, f.itemIDs[1]).Scan(f.ctx, &markers); err != nil || markers != 0 {
		t.Fatalf("absence completion markers after rollback=%d err=%v; want none", markers, err)
	}
	if err := f.fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_item WHERE id=? AND mutation_completed_at IS NOT NULL`, f.itemIDs[1]).Scan(f.ctx, &completed); err != nil || completed != 0 {
		t.Fatalf("original item completed after failed marker insert=%d err=%v; want zero", completed, err)
	}
	var startedAt any
	var resolutionCompleted any
	var resolutionKind any
	if err := f.fixture.db.NewRaw(`SELECT started_at,completed_at,resolution_kind FROM source_cleanup_explicit_retry_resolution WHERE item_id=?`, f.itemIDs[1]).Scan(f.ctx, &startedAt, &resolutionCompleted, &resolutionKind); err != nil {
		t.Fatal(err)
	}
	if startedAt == nil || resolutionCompleted != nil || resolutionKind != nil {
		t.Fatalf("retry resolution frontier started=%v completed=%v kind=%v; want retained incomplete start after marker transaction rollback", startedAt, resolutionCompleted, resolutionKind)
	}
}

func TestResolvePublicationSourceCleanupRetryItemBlocksUnstartedAbsenceAndIncompleteReplayPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	var calls int
	missing := func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		calls++
		return PublicationSourceCleanupNativeMissingLeaf, fs.ErrNotExist
	}
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[2], missing); outcome != PublicationSourceCleanupRetryResolutionBlocked || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unstarted missing leaf=%s,%v", outcome, err)
	}
	if calls != 1 {
		t.Fatalf("first callback count=%d", calls)
	}
	removed := func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		calls++
		return PublicationSourceCleanupNativeRemoved, nil
	}
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[2], removed); outcome != PublicationSourceCleanupRetryResolutionBlocked || err != nil {
		t.Fatalf("incomplete start replay=%s,%v", outcome, err)
	}
	if calls != 1 {
		t.Fatalf("incomplete start replay invoked callback again: %d", calls)
	}
	var starts, completed int
	if err := f.fixture.db.NewRaw(`SELECT count(*),count(*) FILTER (WHERE completed_at IS NOT NULL) FROM source_cleanup_explicit_retry_resolution WHERE item_id=?`, f.itemIDs[2]).Scan(f.ctx, &starts, &completed); err != nil || starts != 1 || completed != 0 {
		t.Fatalf("unstarted missing leaf frontier starts=%d completed=%d err=%v", starts, completed, err)
	}
}

func TestResolvePublicationSourceCleanupRetryItemRejectsNilCallbackBeforeStartPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[2], nil); outcome != PublicationSourceCleanupRetryResolutionBlocked || err != nil {
		t.Fatalf("nil callback for unstarted item=%s,%v", outcome, err)
	}
	var starts int
	if err := f.fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_explicit_retry_resolution WHERE item_id=?`, f.itemIDs[2]).Scan(f.ctx, &starts); err != nil || starts != 0 {
		t.Fatalf("nil callback created a resolution start: count=%d err=%v", starts, err)
	}
}

func TestResolvePublicationSourceCleanupRetryItemRequiresConsistentNativeOutcomePostgreSQL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		outcome PublicationSourceCleanupNativeOutcome
		err     error
	}{
		{name: "removed with error", outcome: PublicationSourceCleanupNativeRemoved, err: fs.ErrNotExist},
		{name: "missing without error", outcome: PublicationSourceCleanupNativeMissingLeaf},
		{name: "unresolved without error", outcome: PublicationSourceCleanupNativeUnresolved},
		{name: "unknown outcome", outcome: "unexpected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRetryResolutionFixture(t)
			calls := 0
			callback := func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
				calls++
				return tc.outcome, tc.err
			}
			if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[2], callback); outcome != PublicationSourceCleanupRetryResolutionBlocked {
				t.Fatalf("inconsistent outcome=%s,%v", outcome, err)
			}
			if calls != 1 {
				t.Fatalf("callback count=%d", calls)
			}
			var starts, completed int
			if err := f.fixture.db.NewRaw(`SELECT count(*),count(*) FILTER (WHERE completed_at IS NOT NULL) FROM source_cleanup_explicit_retry_resolution WHERE item_id=?`, f.itemIDs[2]).Scan(f.ctx, &starts, &completed); err != nil || starts != 1 || completed != 0 {
				t.Fatalf("inconsistent outcome frontier starts=%d completed=%d err=%v", starts, completed, err)
			}
		})
	}
}

func TestResolvePublicationSourceCleanupRetryItemPermissionOffAllowsOnlyAcknowledgementsPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	if _, err := f.fixture.db.NewRaw(`UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`).Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
	calls := 0
	callback := func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		calls++
		return PublicationSourceCleanupNativeRemoved, nil
	}
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[1], callback); outcome != PublicationSourceCleanupRetryResolutionBlocked || err != nil {
		t.Fatalf("permission-off filesystem resolution=%s,%v", outcome, err)
	}
	if calls != 0 {
		t.Fatalf("permission-off callback count=%d", calls)
	}
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[0], nil); outcome != PublicationSourceCleanupRetryResolutionCompleted || err != nil {
		t.Fatalf("original native completion acknowledgement=%s,%v", outcome, err)
	}
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[0], callback); outcome != PublicationSourceCleanupRetryResolutionAlreadyCompleted || err != nil {
		t.Fatalf("permission-off completed resolution acknowledgement=%s,%v", outcome, err)
	}
	if calls != 0 {
		t.Fatalf("acknowledgement invoked callback: %d", calls)
	}
}

func TestResolvePublicationSourceCleanupRetryItemCommitFailureKeepsOnlyStartPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	if _, err := f.fixture.db.ExecContext(f.ctx, `
		CREATE FUNCTION reject_retry_resolution_completion() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.completed_at IS NOT NULL THEN RAISE EXCEPTION 'injected resolution commit failure'; END IF;
			RETURN NEW;
		END $$;
		CREATE CONSTRAINT TRIGGER reject_retry_resolution_completion AFTER UPDATE ON source_cleanup_explicit_retry_resolution
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_retry_resolution_completion()`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	callback := func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		calls++
		return PublicationSourceCleanupNativeRemoved, nil
	}
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[2], callback); outcome != PublicationSourceCleanupRetryResolutionBlocked || err == nil {
		t.Fatalf("completion commit failure=%s,%v", outcome, err)
	}
	if calls != 1 {
		t.Fatalf("callback count=%d", calls)
	}
	var completed int
	if err := f.fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_explicit_retry_resolution WHERE item_id=? AND completed_at IS NOT NULL`, f.itemIDs[2]).Scan(f.ctx, &completed); err != nil || completed != 0 {
		t.Fatalf("commit failure persisted completion count=%d err=%v", completed, err)
	}
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[2], callback); outcome != PublicationSourceCleanupRetryResolutionBlocked || err != nil {
		t.Fatalf("post-commit-failure replay=%s,%v", outcome, err)
	}
	if calls != 1 {
		t.Fatalf("incomplete start was replayed; callback count=%d", calls)
	}
}

func TestResolvePublicationSourceCleanupRetryItemConcurrentCallbackRunsOncePostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	start := make(chan struct{})
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseCallback := func() { releaseOnce.Do(func() { close(release) }) }
	// Cleanup always unblocks the callback and cancels both resolutions, so a
	// timeout below cannot leave a goroutine waiting on the callback.
	defer func() {
		releaseCallback()
		cancel()
	}()
	var calls int
	var callMu sync.Mutex
	callback := func(callbackCtx context.Context, _ PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		callMu.Lock()
		calls++
		callMu.Unlock()
		// Capacity one keeps the first entry observable even when this
		// goroutine reaches the send before the test starts receiving.
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-callbackCtx.Done():
		}
		return PublicationSourceCleanupNativeRemoved, nil
	}
	type result struct {
		outcome PublicationSourceCleanupRetryResolutionOutcome
		err     error
	}
	results := make(chan result, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(ctx, f.newGuard, f.itemIDs[2], callback)
			results <- result{outcome: outcome, err: err}
		}()
	}
	close(start)
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatalf("concurrent callback was not entered: %v", ctx.Err())
	}
	releaseCallback()
	reported := make([]result, 0, 2)
	for len(reported) < 2 {
		select {
		case got := <-results:
			reported = append(reported, got)
		case <-ctx.Done():
			t.Fatalf("concurrent resolutions did not report outcomes: %v", ctx.Err())
		}
	}
	finished := make(chan struct{})
	go func() {
		wait.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatalf("concurrent resolutions did not finish: %v", ctx.Err())
	}
	if calls != 1 {
		t.Fatalf("concurrent filesystem callback count=%d", calls)
	}
	completed, acknowledged, blocked := 0, 0, 0
	for _, result := range reported {
		if result.err != nil {
			t.Fatal(result.err)
		}
		switch result.outcome {
		case PublicationSourceCleanupRetryResolutionCompleted:
			completed++
		case PublicationSourceCleanupRetryResolutionAlreadyCompleted:
			acknowledged++
		case PublicationSourceCleanupRetryResolutionBlocked:
			blocked++
		default:
			t.Fatalf("concurrent outcome=%s", result.outcome)
		}
	}
	if completed != 1 || acknowledged+blocked != 1 {
		t.Fatalf("concurrent outcomes completed=%d acknowledged=%d blocked=%d", completed, acknowledged, blocked)
	}
}
