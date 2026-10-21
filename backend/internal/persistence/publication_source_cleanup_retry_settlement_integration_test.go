//go:build integration

package persistence

import (
	"context"
	"errors"
	"io/fs"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func resolveAllRetryItems(t *testing.T, f retryResolutionFixture) {
	t.Helper()
	for i, itemID := range f.itemIDs {
		var remove func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error)
		switch i {
		case 1:
			remove = func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
				return PublicationSourceCleanupNativeMissingLeaf, fs.ErrNotExist
			}
		case 2:
			remove = func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
				return PublicationSourceCleanupNativeRemoved, nil
			}
		}
		got, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, itemID, remove)
		if err != nil || got != PublicationSourceCleanupRetryResolutionCompleted {
			t.Fatalf("resolve retry item %s = %s, %v", itemID, got, err)
		}
	}
}

func retrySettlementAuxiliaryEvidence(t *testing.T, f retryResolutionFixture) string {
	t.Helper()
	var evidence string
	err := f.fixture.db.NewRaw(`SELECT jsonb_build_object(
		'operations',(SELECT COALESCE(jsonb_agg(to_jsonb(o) ORDER BY id),'[]') FROM operation o WHERE id IN (?,?)),
		'grants',(SELECT COALESCE(jsonb_agg(to_jsonb(g) ORDER BY id),'[]') FROM source_cleanup_explicit_retry g WHERE original_operation_id=?),
		'references',(SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY item_id),'[]') FROM source_cleanup_explicit_retry_item r WHERE original_operation_id=?),
		'resolutions',(SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY item_id),'[]') FROM source_cleanup_explicit_retry_resolution r WHERE recovery_operation_id=?),
		'claims',(SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY operation_id,application_attempt),'[]') FROM publication_execution_claim c WHERE operation_id IN (?,?)),
		'owners',(SELECT COALESCE(jsonb_agg(to_jsonb(o) ORDER BY item_id),'[]') FROM source_cleanup_owner o WHERE operation_id=?),
		'items',(SELECT COALESCE(jsonb_agg(to_jsonb(i) ORDER BY id),'[]') FROM source_cleanup_item i WHERE operation_id=?))::text`,
		f.originalID, f.newGuard.OperationID, f.originalID, f.originalID, f.newGuard.OperationID,
		f.originalID, f.newGuard.OperationID, f.originalID, f.originalID).Scan(f.ctx, &evidence)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func retrySettlementSourceInventory(t *testing.T, f retryResolutionFixture) string {
	t.Helper()
	var evidence string
	err := f.fixture.db.NewRaw(`SELECT jsonb_build_object(
		'locations',(SELECT COALESCE(jsonb_agg(to_jsonb(l) ORDER BY id),'[]') FROM source_location l),
		'links',(SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY media_variant_id,id),'[]') FROM track_source s))::text`).Scan(f.ctx, &evidence)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func assertRetrySettlementAuxiliaryGone(t *testing.T, f retryResolutionFixture) {
	t.Helper()
	var total int
	if err := f.fixture.db.NewRaw(`SELECT
		(SELECT count(*) FROM operation WHERE id=?) +
		(SELECT count(*) FROM source_cleanup_explicit_retry WHERE original_operation_id=?) +
		(SELECT count(*) FROM source_cleanup_explicit_retry_item WHERE original_operation_id=?) +
		(SELECT count(*) FROM source_cleanup_explicit_retry_resolution WHERE recovery_operation_id=?) +
		(SELECT count(*) FROM publication_execution_claim WHERE operation_id IN (?,?)) +
		(SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?) +
		(SELECT count(*) FROM source_cleanup_item WHERE operation_id=?)`, f.newGuard.OperationID, f.originalID, f.originalID,
		f.newGuard.OperationID, f.originalID, f.newGuard.OperationID, f.originalID, f.originalID).Scan(f.ctx, &total); err != nil || total != 0 {
		t.Fatalf("retry settlement left auxiliary rows=%d err=%v", total, err)
	}
	var original Operation
	if err := f.fixture.db.NewSelect().Model(&original).Where("id=?", f.originalID).Scan(f.ctx); err != nil {
		t.Fatal(err)
	}
	if original.State != "succeeded" || original.Stage != "finished" || original.SafeError != nil || original.FinishedAt == nil || original.Attempt != 1 {
		t.Fatalf("original cleanup not settled in place: %+v", original)
	}
}

func TestSettlePublicationSourceCleanupExplicitRetryPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	resolveAllRetryItems(t, f)
	if _, err := f.fixture.db.NewRaw(`UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`).Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
	beforeInventory := retrySettlementSourceInventory(t, f)
	settled, err := f.repository.SettlePublicationSourceCleanupExplicitRetry(f.ctx, f.newGuard)
	if err != nil || !settled {
		t.Fatalf("settle resolved retry=%t,%v", settled, err)
	}
	assertRetrySettlementAuxiliaryGone(t, f)
	if after := retrySettlementSourceInventory(t, f); after != beforeInventory {
		t.Fatalf("settlement changed inventory or links\nbefore %s\nafter %s", beforeInventory, after)
	}
	settled, err = f.repository.SettlePublicationSourceCleanupExplicitRetry(f.ctx, f.newGuard)
	if err != nil || settled {
		t.Fatalf("repeat settlement=%t,%v; want false,nil", settled, err)
	}
}

func TestSettlePublicationSourceCleanupExplicitRetryRejectsIncompleteOrStaleEvidencePostgreSQL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, retryResolutionFixture)
		guard   func(retryResolutionFixture) PublicationSourceCleanupDeliveryGuard
	}{
		{name: "incomplete", prepare: func(*testing.T, retryResolutionFixture) {}, guard: func(f retryResolutionFixture) PublicationSourceCleanupDeliveryGuard { return f.newGuard }},
		{name: "missing resolution", prepare: func(t *testing.T, f retryResolutionFixture) {
			for _, itemID := range f.itemIDs[:2] {
				var remove func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error)
				if itemID == f.itemIDs[1] {
					remove = func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
						return PublicationSourceCleanupNativeMissingLeaf, fs.ErrNotExist
					}
				}
				if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, itemID, remove); err != nil || outcome != PublicationSourceCleanupRetryResolutionCompleted {
					t.Fatalf("resolve partial frontier=%s,%v", outcome, err)
				}
			}
		}, guard: func(f retryResolutionFixture) PublicationSourceCleanupDeliveryGuard { return f.newGuard }},
		{name: "started but incomplete resolution", prepare: func(t *testing.T, f retryResolutionFixture) {
			// The first two resolutions are complete; leave the third at its durable
			// started frontier by returning an ambiguous callback failure.
			for _, itemID := range f.itemIDs[:2] {
				remove := func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
					return PublicationSourceCleanupNativeRemoved, nil
				}
				if itemID == f.itemIDs[1] {
					if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
						return PublicationSourceCleanupNativeMissingLeaf, fs.ErrNotExist
					}); err != nil || outcome != PublicationSourceCleanupRetryResolutionCompleted {
						t.Fatalf("resolve second frontier=%s,%v", outcome, err)
					}
					continue
				}
				if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, itemID, remove); err != nil || outcome != PublicationSourceCleanupRetryResolutionCompleted {
					t.Fatalf("resolve first frontier=%s,%v", outcome, err)
				}
			}
			uncertain := errors.New("native callback outcome is uncertain")
			if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[2], func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
				return PublicationSourceCleanupNativeUnresolved, uncertain
			}); outcome != PublicationSourceCleanupRetryResolutionBlocked || !errors.Is(err, uncertain) {
				t.Fatalf("leave third frontier started=%s,%v", outcome, err)
			}
		}, guard: func(f retryResolutionFixture) PublicationSourceCleanupDeliveryGuard { return f.newGuard }},
		{name: "stale River attempt", prepare: func(t *testing.T, f retryResolutionFixture) {
			resolveAllRetryItems(t, f)
			if _, err := f.fixture.db.NewRaw(`UPDATE river_job SET attempt=attempt+1 WHERE id=?`, f.newGuard.RiverJobID).Exec(f.ctx); err != nil {
				t.Fatal(err)
			}
		}, guard: func(f retryResolutionFixture) PublicationSourceCleanupDeliveryGuard { return f.newGuard }},
		{name: "stale River state", prepare: func(t *testing.T, f retryResolutionFixture) {
			resolveAllRetryItems(t, f)
			if _, err := f.fixture.db.NewRaw(`UPDATE river_job SET state='available' WHERE id=?`, f.newGuard.RiverJobID).Exec(f.ctx); err != nil {
				t.Fatal(err)
			}
		}, guard: func(f retryResolutionFixture) PublicationSourceCleanupDeliveryGuard { return f.newGuard }},
		{name: "stale River arguments", prepare: func(t *testing.T, f retryResolutionFixture) {
			resolveAllRetryItems(t, f)
			if _, err := f.fixture.db.NewRaw(`UPDATE river_job SET args=jsonb_build_object('operation_id',?) WHERE id=?`, uuid.New().String(), f.newGuard.RiverJobID).Exec(f.ctx); err != nil {
				t.Fatal(err)
			}
		}, guard: func(f retryResolutionFixture) PublicationSourceCleanupDeliveryGuard { return f.newGuard }},
		{name: "stale guard", prepare: func(t *testing.T, f retryResolutionFixture) { resolveAllRetryItems(t, f) }, guard: func(f retryResolutionFixture) PublicationSourceCleanupDeliveryGuard {
			g := f.newGuard
			g.ClaimUUID = uuid.New()
			return g
		}},
		{name: "changed source facts", prepare: func(t *testing.T, f retryResolutionFixture) {
			resolveAllRetryItems(t, f)
			if _, err := f.fixture.db.NewRaw(`UPDATE source_location SET size_bytes=size_bytes+1 WHERE id=(SELECT source_location_id FROM source_cleanup_owner WHERE operation_id=? LIMIT 1)`, f.originalID).Exec(f.ctx); err != nil {
				t.Fatal(err)
			}
		}, guard: func(f retryResolutionFixture) PublicationSourceCleanupDeliveryGuard { return f.newGuard }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newRetryResolutionFixture(t)
			tc.prepare(t, f)
			before := retrySettlementAuxiliaryEvidence(t, f)
			settled, err := f.repository.SettlePublicationSourceCleanupExplicitRetry(f.ctx, tc.guard(f))
			if settled || err != nil {
				t.Fatalf("settlement=%t,%v; want false,nil", settled, err)
			}
			if after := retrySettlementAuxiliaryEvidence(t, f); after != before {
				t.Fatalf("rejection mutated evidence\nbefore %s\nafter %s", before, after)
			}
		})
	}
}

func TestSettlePublicationSourceCleanupExplicitRetryConcurrentPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	resolveAllRetryItems(t, f)
	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			settled, err := f.repository.SettlePublicationSourceCleanupExplicitRetry(f.ctx, f.newGuard)
			results <- settled
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errs)
	trueCount, falseCount := 0, 0
	for settled := range results {
		if settled {
			trueCount++
		} else {
			falseCount++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if trueCount != 1 || falseCount != 1 {
		t.Fatalf("concurrent settlements success=%d blocked=%d", trueCount, falseCount)
	}
	assertRetrySettlementAuxiliaryGone(t, f)
}

func TestSettlePublicationSourceCleanupExplicitRetryCommitFailureRollsBackPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	resolveAllRetryItems(t, f)
	before := retrySettlementAuxiliaryEvidence(t, f)
	if _, err := f.fixture.db.ExecContext(f.ctx, `CREATE FUNCTION reject_original_cleanup_settlement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='succeeded' AND OLD.state='failed' THEN RAISE EXCEPTION 'injected deferred settlement failure'; END IF; RETURN NEW; END $$; CREATE CONSTRAINT TRIGGER reject_original_cleanup_settlement AFTER UPDATE ON operation DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_original_cleanup_settlement()`); err != nil {
		t.Fatal(err)
	}
	settled, err := f.repository.SettlePublicationSourceCleanupExplicitRetry(f.ctx, f.newGuard)
	if settled || err == nil {
		t.Fatalf("commit failure settlement=%t,%v", settled, err)
	}
	if after := retrySettlementAuxiliaryEvidence(t, f); after != before {
		t.Fatalf("commit failure did not roll back complete state\nbefore %s\nafter %s", before, after)
	}
}
