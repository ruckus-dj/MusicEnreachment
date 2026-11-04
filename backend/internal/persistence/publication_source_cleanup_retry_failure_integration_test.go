//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestPublicationSourceCleanupExplicitRetryFailureFencesPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigratedWithRiver(t)

	scenarios := []struct {
		name string
		run  func(*testing.T, *retryResolutionFixture)
	}{
		{
			name: "returned callback failure",
			run: func(t *testing.T, f *retryResolutionFixture) {
				before := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID)
				callbackErr := errors.New("native callback failed")
				if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[2], func(_ context.Context, _ PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
					return PublicationSourceCleanupNativeUnresolved, callbackErr
				}); outcome != PublicationSourceCleanupRetryResolutionBlocked || !errors.Is(err, callbackErr) {
					t.Fatalf("callback failure=%s,%v", outcome, err)
				}

				var priorSetting string
				settingErr := f.fixture.db.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_remove_sources'`).Scan(f.ctx, &priorSetting)
				if settingErr != nil && !errors.Is(settingErr, sql.ErrNoRows) {
					t.Fatal(settingErr)
				}
				hadSetting := settingErr == nil
				t.Cleanup(func() {
					if hadSetting {
						if _, err := f.fixture.db.NewRaw(`UPDATE app_setting SET setting_value=? WHERE setting_name='publication_remove_sources'`, priorSetting).Exec(f.ctx); err != nil {
							t.Errorf("restore publication cleanup setting: %v", err)
						}
					} else if _, err := f.fixture.db.NewRaw(`DELETE FROM app_setting WHERE setting_name='publication_remove_sources'`).Exec(f.ctx); err != nil {
						t.Errorf("restore absent publication cleanup setting: %v", err)
					}
				})
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
			},
		},
		{
			name: "all resolved items",
			run: func(t *testing.T, f *retryResolutionFixture) {
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
			},
		},
		{
			name: "deleted River row",
			run: func(t *testing.T, f *retryResolutionFixture) {
				fenceAfterRiverChange(t, f, func() error {
					_, err := f.fixture.db.NewRaw(`DELETE FROM river_job WHERE id=?`, f.newGuard.RiverJobID).Exec(f.ctx)
					return err
				})
			},
		},
		{
			name: "rescued River row",
			run: func(t *testing.T, f *retryResolutionFixture) {
				fenceAfterRiverChange(t, f, func() error {
					_, err := f.fixture.db.NewRaw(`UPDATE river_job SET state='available',attempt=attempt+1,finalized_at=NULL WHERE id=?`, f.newGuard.RiverJobID).Exec(f.ctx)
					return err
				})
			},
		},
		{
			name: "frozen item mismatch",
			run: func(t *testing.T, f *retryResolutionFixture) {
				// Drift through SQL to test the API's frozen-identity proof, not the
				// database's immutability enforcement.
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
			},
		},
		{
			name: "stale identities",
			run: func(t *testing.T, f *retryResolutionFixture) {
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
					guard := f.newGuard
					alter.change(&guard)
					if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, guard, "safe failure"); err != nil || failed {
						t.Fatalf("%s stale guard=%t,%v", alter.name, failed, err)
					}
					var state, claimState string
					if err := f.fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, f.newGuard.OperationID).Scan(f.ctx, &state); err != nil || state != "running" {
						t.Fatalf("%s operation state=%q err=%v", alter.name, state, err)
					}
					if err := f.fixture.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.newGuard.OperationID).Scan(f.ctx, &claimState); err != nil || claimState != "executing" {
						t.Fatalf("%s claim state=%q err=%v", alter.name, claimState, err)
					}
				}
			},
		},
		{
			name: "deferred commit rollback",
			run: func(t *testing.T, f *retryResolutionFixture) {
				if _, err := f.fixture.db.ExecContext(f.ctx, `CREATE FUNCTION reject_retry_failure_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='failed' THEN RAISE EXCEPTION 'injected retry failure commit'; END IF; RETURN NEW; END $$`); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if _, err := f.fixture.db.ExecContext(f.ctx, `DROP TRIGGER IF EXISTS reject_retry_failure_commit ON operation`); err != nil {
						t.Errorf("drop deferred retry failure trigger: %v", err)
					}
					if _, err := f.fixture.db.ExecContext(f.ctx, `DROP FUNCTION IF EXISTS reject_retry_failure_commit()`); err != nil {
						t.Errorf("drop deferred retry failure function: %v", err)
					}
				})
				if _, err := f.fixture.db.ExecContext(f.ctx, `CREATE CONSTRAINT TRIGGER reject_retry_failure_commit AFTER UPDATE ON operation DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_retry_failure_commit()`); err != nil {
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
			},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			f := newRetryResolutionFixtureWithDatabase(t, db, uuid.NewString())
			scenario.run(t, &f)
		})
	}
}

func fenceAfterRiverChange(t *testing.T, f *retryResolutionFixture, mutate func() error) {
	t.Helper()
	before := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID)
	if err := mutate(); err != nil {
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
}
