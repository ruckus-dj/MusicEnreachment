//go:build integration

package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestFailPublicationSourceCleanupExplicitRetryDeliveryFencesReturnedCallbackPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	before := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID)
	callbackErr := errors.New("native callback failed")
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[2], func(_ context.Context, _ PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		return PublicationSourceCleanupNativeUnresolved, callbackErr
	}); outcome != PublicationSourceCleanupRetryResolutionBlocked || !errors.Is(err, callbackErr) {
		t.Fatalf("callback failure=%s,%v", outcome, err)
	}
	if _, err := f.fixture.db.NewRaw(`UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`).Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.fixture.db.NewRaw(`UPDATE source_root SET enabled=false WHERE id IN (SELECT source_root_id FROM source_cleanup_owner WHERE operation_id=?)`, f.originalID).Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
	if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, f.newGuard, "  retry callback returned uncertain  "); err != nil || !failed {
		t.Fatalf("fence retry delivery=%t,%v", failed, err)
	}
	var state, stage, safeError, claimState string
	var finished bool
	if err := f.fixture.db.NewRaw(`SELECT state,stage,safe_error,finished_at IS NOT NULL FROM operation WHERE id=?`, f.newGuard.OperationID).Scan(f.ctx, &state, &stage, &safeError, &finished); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || stage != "cleanup_retry_recovery_required" || safeError != "retry callback returned uncertain" || !finished {
		t.Fatalf("retry operation=%q %q %q finished=%t", state, stage, safeError, finished)
	}
	if err := f.fixture.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.newGuard.OperationID).Scan(f.ctx, &claimState); err != nil {
		t.Fatal(err)
	}
	if claimState != "recovery_required" {
		t.Fatalf("retry claim state=%q", claimState)
	}
	var consumed bool
	if err := f.fixture.db.NewRaw(`SELECT consumed_claim_uuid=? AND consumed_application_attempt=? AND consumed_river_job_id=? FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?`, f.newGuard.ClaimUUID, f.newGuard.ApplicationAttempt, f.newGuard.RiverJobID, f.newGuard.OperationID).Scan(f.ctx, &consumed); err != nil || !consumed {
		t.Fatalf("grant consumption retained=%t err=%v", consumed, err)
	}
	var started, completed int
	if err := f.fixture.db.NewRaw(`SELECT count(*),count(*) FILTER (WHERE completed_at IS NOT NULL) FROM source_cleanup_explicit_retry_resolution WHERE recovery_operation_id=?`, f.newGuard.OperationID).Scan(f.ctx, &started, &completed); err != nil || started != 1 || completed != 0 {
		t.Fatalf("resolution frontier started=%d completed=%d err=%v", started, completed, err)
	}
	if got := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID); got != before {
		t.Fatalf("original cleanup evidence changed:\nbefore %s\nafter  %s", before, got)
	}
	if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, f.newGuard, "different replay"); err != nil || failed {
		t.Fatalf("duplicate terminal failure=%t,%v", failed, err)
	}
	if err := f.fixture.db.NewRaw(`SELECT state,stage,safe_error,finished_at IS NOT NULL FROM operation WHERE id=?`, f.newGuard.OperationID).Scan(f.ctx, &state, &stage, &safeError, &finished); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || stage != "cleanup_retry_recovery_required" || safeError != "retry callback returned uncertain" || !finished {
		t.Fatalf("duplicate failure changed terminal operation=%q %q %q finished=%t", state, stage, safeError, finished)
	}
}

func TestFailPublicationSourceCleanupExplicitRetryDeliveryRejectsMalformedGuardPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, alter := range []struct {
		name   string
		change func(*PublicationSourceCleanupDeliveryGuard)
	}{
		{"zero operation", func(g *PublicationSourceCleanupDeliveryGuard) { g.OperationID = uuid.Nil }},
		{"zero claim", func(g *PublicationSourceCleanupDeliveryGuard) { g.ClaimUUID = uuid.Nil }},
		{"empty token", func(g *PublicationSourceCleanupDeliveryGuard) { g.DeliveryToken = "" }},
		{"zero application attempt", func(g *PublicationSourceCleanupDeliveryGuard) { g.ApplicationAttempt = 0 }},
		{"zero river job", func(g *PublicationSourceCleanupDeliveryGuard) { g.RiverJobID = 0 }},
		{"zero river attempt", func(g *PublicationSourceCleanupDeliveryGuard) { g.RiverAttempt = 0 }},
	} {
		t.Run(alter.name, func(t *testing.T) {
			f := newRetryResolutionFixture(t)
			before := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID)
			retryID := f.newGuard.OperationID
			var consumptionBefore string
			if err := f.fixture.db.NewRaw(`SELECT jsonb_build_array(consumed_application_attempt,consumed_river_job_id,consumed_claim_uuid)::text FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?`, retryID).Scan(f.ctx, &consumptionBefore); err != nil {
				t.Fatal(err)
			}
			alter.change(&f.newGuard)
			if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, f.newGuard, "safe failure"); err == nil || failed {
				t.Fatalf("malformed guard=%t,%v", failed, err)
			}
			var state, claimState string
			if err := f.fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, retryID).Scan(f.ctx, &state); err != nil {
				t.Fatal(err)
			}
			if err := f.fixture.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, retryID).Scan(f.ctx, &claimState); err != nil {
				t.Fatal(err)
			}
			var consumptionAfter string
			if err := f.fixture.db.NewRaw(`SELECT jsonb_build_array(consumed_application_attempt,consumed_river_job_id,consumed_claim_uuid)::text FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?`, retryID).Scan(f.ctx, &consumptionAfter); err != nil {
				t.Fatal(err)
			}
			var resolutions int
			if err := f.fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_explicit_retry_resolution WHERE recovery_operation_id=?`, retryID).Scan(f.ctx, &resolutions); err != nil {
				t.Fatal(err)
			}
			if state != "running" || claimState != "executing" || consumptionAfter != consumptionBefore || resolutions != 0 {
				t.Fatalf("malformed guard mutated retry state op=%q claim=%q consumption=%s/%s resolutions=%d", state, claimState, consumptionAfter, consumptionBefore, resolutions)
			}
			if got := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID); got != before {
				t.Fatalf("malformed guard changed original evidence:\nbefore %s\nafter  %s", before, got)
			}
		})
	}
}

func TestFailPublicationSourceCleanupExplicitRetryDeliveryAllowsAllResolvedItemsPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	for _, id := range f.itemIDs {
		outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, id, func(_ context.Context, _ PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
			return PublicationSourceCleanupNativeRemoved, nil
		})
		if err != nil || outcome != PublicationSourceCleanupRetryResolutionCompleted {
			t.Fatalf("resolve %s=%s,%v", id, outcome, err)
		}
	}
	if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, f.newGuard, "settlement uncertain"); err != nil || !failed {
		t.Fatalf("fence completed resolutions=%t,%v", failed, err)
	}
}

func TestFailPublicationSourceCleanupExplicitRetryDeliveryFencesAfterRiverChangePostgreSQL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(context.Context, *retryResolutionFixture) error
	}{
		{"deleted River row", func(ctx context.Context, f *retryResolutionFixture) error {
			_, err := f.fixture.db.NewRaw(`DELETE FROM river_job WHERE id=?`, f.newGuard.RiverJobID).Exec(ctx)
			return err
		}},
		{"rescued River row", func(ctx context.Context, f *retryResolutionFixture) error {
			_, err := f.fixture.db.NewRaw(`UPDATE river_job SET state='available',attempt=attempt+1,finalized_at=NULL WHERE id=?`, f.newGuard.RiverJobID).Exec(ctx)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRetryResolutionFixture(t)
			before := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID)
			if err := tc.mutate(f.ctx, &f); err != nil {
				t.Fatalf("mutate actual River row: %v", err)
			}
			if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, f.newGuard, "uncertain delivery"); err != nil || !failed {
				t.Fatalf("fence delivery after River change=%t,%v", failed, err)
			}
			var opState, claimState string
			if err := f.fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, f.newGuard.OperationID).Scan(f.ctx, &opState); err != nil {
				t.Fatal(err)
			}
			if err := f.fixture.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.newGuard.OperationID).Scan(f.ctx, &claimState); err != nil {
				t.Fatal(err)
			}
			if opState != "failed" || claimState != "recovery_required" {
				t.Fatalf("post-River-change operation=%q claim=%q", opState, claimState)
			}
			if got := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID); got != before {
				t.Fatalf("failure fence changed original evidence:\nbefore %s\nafter  %s", before, got)
			}
		})
	}
}

func TestFailPublicationSourceCleanupExplicitRetryDeliveryRejectsFrozenItemMismatchPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	// The source snapshot is intentionally drifted through SQL; this tests the API's
	// frozen-identity proof, not the database's immutability enforcement.
	if _, err := f.fixture.db.NewRaw(`UPDATE source_cleanup_item SET source_snapshot=jsonb_set(source_snapshot,'{version}','999'::jsonb) WHERE id=?`, f.itemIDs[0]).Exec(f.ctx); err != nil {
		t.Fatalf("prepare frozen snapshot drift: %v", err)
	}
	var frozenBaseline string
	if err := f.fixture.db.NewRaw(`SELECT source_snapshot::text FROM source_cleanup_item WHERE id=?`, f.itemIDs[0]).Scan(f.ctx, &frozenBaseline); err != nil {
		t.Fatal(err)
	}
	before := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID)
	if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, f.newGuard, "uncertain delivery"); err != nil || failed {
		t.Fatalf("frozen item mismatch failure=%t,%v", failed, err)
	}
	var afterSnapshot string
	if err := f.fixture.db.NewRaw(`SELECT source_snapshot::text FROM source_cleanup_item WHERE id=?`, f.itemIDs[0]).Scan(f.ctx, &afterSnapshot); err != nil {
		t.Fatal(err)
	}
	if afterSnapshot != frozenBaseline {
		t.Fatalf("failure call changed drifted frozen snapshot: before=%s after=%s", frozenBaseline, afterSnapshot)
	}
	if got := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID); got != before {
		t.Fatalf("frozen mismatch changed old evidence:\nbefore %s\nafter  %s", before, got)
	}
	var opState, claimState string
	if err := f.fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, f.newGuard.OperationID).Scan(f.ctx, &opState); err != nil {
		t.Fatal(err)
	}
	if err := f.fixture.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.newGuard.OperationID).Scan(f.ctx, &claimState); err != nil {
		t.Fatal(err)
	}
	var consumed bool
	if err := f.fixture.db.NewRaw(`SELECT consumed_claim_uuid IS NOT NULL FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?`, f.newGuard.OperationID).Scan(f.ctx, &consumed); err != nil {
		t.Fatal(err)
	}
	var resolutions int
	if err := f.fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_explicit_retry_resolution WHERE recovery_operation_id=?`, f.newGuard.OperationID).Scan(f.ctx, &resolutions); err != nil {
		t.Fatal(err)
	}
	if opState != "running" || claimState != "executing" || !consumed || resolutions != 0 {
		t.Fatalf("frozen mismatch mutated state op=%q claim=%q consumed=%t resolutions=%d", opState, claimState, consumed, resolutions)
	}
}

func TestFailPublicationSourceCleanupExplicitRetryDeliveryRejectsStaleIdentityPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, alter := range []struct {
		name   string
		change func(*PublicationSourceCleanupDeliveryGuard)
	}{
		{"claim", func(g *PublicationSourceCleanupDeliveryGuard) { g.ClaimUUID = uuid.New() }},
		{"boot", func(g *PublicationSourceCleanupDeliveryGuard) { g.BootUUID = uuid.New() }},
		{"token", func(g *PublicationSourceCleanupDeliveryGuard) { g.DeliveryToken += "-stale" }},
		{"epoch", func(g *PublicationSourceCleanupDeliveryGuard) { g.Epoch = uuid.New() }},
		{"river attempt", func(g *PublicationSourceCleanupDeliveryGuard) { g.RiverAttempt++ }},
	} {
		t.Run(alter.name, func(t *testing.T) {
			f := newRetryResolutionFixture(t)
			guard := f.newGuard
			alter.change(&guard)
			if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, guard, "safe failure"); err != nil || failed {
				t.Fatalf("stale guard=%t,%v", failed, err)
			}
			var state string
			if err := f.fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, f.newGuard.OperationID).Scan(f.ctx, &state); err != nil || state != "running" {
				t.Fatalf("operation state=%q err=%v", state, err)
			}
		})
	}
}

func TestFailPublicationSourceCleanupExplicitRetryDeliveryRollsBackDeferredCommitFailurePostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	if _, err := f.fixture.db.ExecContext(f.ctx, `CREATE FUNCTION reject_retry_failure_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='failed' THEN RAISE EXCEPTION 'injected retry failure commit'; END IF; RETURN NEW; END $$; CREATE CONSTRAINT TRIGGER reject_retry_failure_commit AFTER UPDATE ON operation DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_retry_failure_commit()`); err != nil {
		t.Fatal(err)
	}
	if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, f.newGuard, "safe failure"); err == nil || failed {
		t.Fatalf("deferred commit failure=%t,%v", failed, err)
	}
	var state, claimState string
	if err := f.fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, f.newGuard.OperationID).Scan(f.ctx, &state); err != nil {
		t.Fatal(err)
	}
	if err := f.fixture.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.newGuard.OperationID).Scan(f.ctx, &claimState); err != nil {
		t.Fatal(err)
	}
	if state != "running" || claimState != "executing" {
		t.Fatalf("rollback operation=%q claim=%q", state, claimState)
	}
}
