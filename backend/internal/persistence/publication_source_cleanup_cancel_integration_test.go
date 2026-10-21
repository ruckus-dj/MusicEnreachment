//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func disablePublicationSourceRemoval(t *testing.T, ctx context.Context, f publicationExecutionFixture) {
	t.Helper()
	if _, err := f.db.ExecContext(ctx, `UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`); err != nil {
		t.Fatal(err)
	}
}

func TestCancelUnstartedPublicationSourceCleanupDeliveryPostgreSQL(t *testing.T) {
	t.Parallel()
	t.Run("queued without claim", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, repo, variantID := publicationSourceCleanupReadyFixture(t)
		if _, err := f.db.NewRaw(`UPDATE source_root SET status=?`, SourceRootStatusAvailable).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-cancel-v1' WHERE media_variant_id=?`, variantID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		request := PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}
		inserter := new(publicationSourceCleanupTestInserter)
		admitted, err := repo.AdmitPublicationSourceCleanup(ctx, request, inserter)
		if err != nil || admitted.OperationID == uuid.Nil {
			t.Fatalf("admit cleanup=%+v, %v", admitted, err)
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
		guard := PublicationSourceCleanupDeliveryGuard{OperationID: admitted.OperationID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "cancel-unclaimed", Epoch: f.guard.Epoch, PositionID: request.PositionID}
		var before string
		if err := f.db.NewRaw(`SELECT jsonb_build_object('locations',(SELECT jsonb_agg(to_jsonb(x)) FROM source_location x),'links',(SELECT jsonb_agg(to_jsonb(x)) FROM track_source x),'publications',(SELECT jsonb_agg(to_jsonb(x)) FROM track_publication x),'river',(SELECT to_jsonb(x) FROM river_job x WHERE id=?))::text`, jobID).Scan(ctx, &before); err != nil {
			t.Fatal(err)
		}
		disablePublicationSourceRemoval(t, ctx, f)
		cancelled, err := repo.CancelUnstartedPublicationSourceCleanupDelivery(ctx, guard, "Source removal is disabled")
		if err != nil || !cancelled {
			t.Fatalf("cancel unclaimed cleanup=%v, %v", cancelled, err)
		}
		var state, stage, safeError string
		if err := f.db.NewRaw(`SELECT state,stage,safe_error FROM operation WHERE id=?`, guard.OperationID).Scan(ctx, &state, &stage, &safeError); err != nil || state != "failed" || stage != "finished" || safeError != "Source removal is disabled" {
			t.Fatalf("operation terminal state=(%q,%q,%q), %v", state, stage, safeError, err)
		}
		for _, table := range []string{"source_cleanup_item", "source_cleanup_owner", "publication_execution_claim"} {
			var count int
			if err := f.db.NewRaw(`SELECT count(*) FROM `+table+` WHERE operation_id=?`, guard.OperationID).Scan(ctx, &count); err != nil || count != 0 {
				t.Fatalf("%s count=%d, %v", table, count, err)
			}
		}
		var after string
		if err := f.db.NewRaw(`SELECT jsonb_build_object('locations',(SELECT jsonb_agg(to_jsonb(x)) FROM source_location x),'links',(SELECT jsonb_agg(to_jsonb(x)) FROM track_source x),'publications',(SELECT jsonb_agg(to_jsonb(x)) FROM track_publication x),'river',(SELECT to_jsonb(x) FROM river_job x WHERE id=?))::text`, jobID).Scan(ctx, &after); err != nil || after != before {
			t.Fatalf("inventory/publication/River changed: err=%v", err)
		}
		cancelled, err = repo.CancelUnstartedPublicationSourceCleanupDelivery(ctx, guard, "Source removal is disabled")
		if err != nil || cancelled {
			t.Fatalf("repeat cancel=%v, %v; want false,nil", cancelled, err)
		}
	})

	t.Run("claimed queued and running unstarted", func(t *testing.T) {
		for _, start := range []bool{false, true} {
			name := "queued"
			if start {
				name = "running"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				f, repo, _ := publicationSourceCleanupReadyFixture(t)
				guard, operationID := admitAndClaimSourceCleanup(t, ctx, f, repo)
				if start {
					started, err := repo.StartPublicationSourceCleanupExecution(ctx, guard)
					if err != nil || !started {
						t.Fatalf("start cleanup=%v, %v", started, err)
					}
				}
				disablePublicationSourceRemoval(t, ctx, f)
				cancelled, err := repo.CancelUnstartedPublicationSourceCleanupDelivery(ctx, guard, "disabled")
				if err != nil || !cancelled {
					t.Fatalf("cancel cleanup=%v, %v", cancelled, err)
				}
				for _, table := range []string{"source_cleanup_item", "source_cleanup_owner", "publication_execution_claim"} {
					var count int
					if err := f.db.NewRaw(`SELECT count(*) FROM `+table+` WHERE operation_id=?`, operationID).Scan(ctx, &count); err != nil || count != 0 {
						t.Fatalf("%s count=%d, %v", table, count, err)
					}
				}
			})
		}
	})

	t.Run("permission remains enabled", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, repo, _ := publicationSourceCleanupReadyFixture(t)
		guard, _ := admitAndClaimSourceCleanup(t, ctx, f, repo)
		cancelled, err := repo.CancelUnstartedPublicationSourceCleanupDelivery(ctx, guard, "disabled")
		if err != nil || cancelled {
			t.Fatalf("cancel with permission enabled=%v, %v; want false,nil", cancelled, err)
		}
	})

	t.Run("mutation frontier blocks", func(t *testing.T) {
		t.Parallel()
		ctx, f, repo, guard, operationID, itemID, request := setupStartedCleanupRemoval(t)
		if _, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return context.Canceled }); err == nil {
			t.Fatal("expected removal callback failure")
		}
		disablePublicationSourceRemoval(t, ctx, f)
		cancelled, err := repo.CancelUnstartedPublicationSourceCleanupDelivery(ctx, guard, "disabled")
		if err != nil || cancelled {
			t.Fatalf("cancel started mutation=%v, %v; want false,nil", cancelled, err)
		}
		var count int
		if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, operationID).Scan(ctx, &count); err != nil || count != 1 {
			t.Fatalf("retained owners=%d, %v", count, err)
		}
		_ = request
	})
}

func TestCancelStartedCleanupFrontiersAndStaleDeliveryPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "started", true: "completed"}[completed], func(t *testing.T) {
			t.Parallel()
			ctx, f, repo, guard, operationID, itemID, _ := setupStartedCleanupRemoval(t)
			startedCalls := 0
			if completed {
				var frozen publicationSourceCleanupSnapshot
				var snapshot []byte
				if err := f.db.NewRaw(`SELECT source_snapshot FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &snapshot); err != nil {
					t.Fatal(err)
				}
				if err := decodeStrictJSON(snapshot, &frozen); err != nil {
					t.Fatal(err)
				}
				mtime, err := time.Parse(time.RFC3339Nano, frozen.Mtime)
				if err != nil {
					t.Fatal(err)
				}
				want := PublicationSourceCleanupRemovalRequest{ConfiguredRootPath: frozen.ConfiguredPath, RelativePath: frozen.RelativePath, SizeBytes: frozen.SizeBytes, MTime: mtime}
				outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(_ context.Context, got PublicationSourceCleanupRemovalRequest) error {
					startedCalls++
					if got.ConfiguredRootPath != want.ConfiguredRootPath || got.RelativePath != want.RelativePath || got.SizeBytes != want.SizeBytes || !got.MTime.Equal(want.MTime) {
						t.Errorf("callback request=%+v want=%+v", got, want)
					}
					return nil
				})
				if err != nil || outcome != PublicationSourceCleanupRemovalCompleted || startedCalls != 1 {
					t.Fatalf("complete=%v,%v calls=%d", outcome, err, startedCalls)
				}
			} else {
				outcome, err := repo.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) error {
					startedCalls++
					return context.Canceled
				})
				if err == nil || outcome != PublicationSourceCleanupRemovalBlocked || startedCalls != 1 {
					t.Fatalf("start frontier=%v,%v calls=%d", outcome, err, startedCalls)
				}
			}
			var mutationStarted, mutationCompleted any
			if err := f.db.NewRaw(`SELECT mutation_started_at,mutation_completed_at FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &mutationStarted, &mutationCompleted); err != nil {
				t.Fatal(err)
			}
			if mutationStarted == nil || (completed != (mutationCompleted != nil)) {
				t.Fatalf("frontier started=%v completed=%v", mutationStarted, mutationCompleted)
			}
			disablePublicationSourceRemoval(t, ctx, f)
			cancelled, err := repo.CancelUnstartedPublicationSourceCleanupDelivery(ctx, guard, "disabled")
			if err != nil || cancelled {
				t.Fatalf("cancel frontier=%v,%v; want false,nil", cancelled, err)
			}
			var count int
			for _, table := range []string{"source_cleanup_owner", "source_cleanup_item", "publication_execution_claim"} {
				if err := f.db.NewRaw(`SELECT count(*) FROM `+table+` WHERE operation_id=?`, operationID).Scan(ctx, &count); err != nil || count == 0 {
					t.Fatalf("%s retained count=%d err=%v", table, count, err)
				}
			}
		})
	}
}

func TestCancelCleanupRejectsStaleDeliveryBeforeMutationFrontierPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	guard, operationID := admitAndClaimSourceCleanup(t, ctx, f, repo)
	disablePublicationSourceRemoval(t, ctx, f)
	var frontier any
	if err := f.db.NewRaw(`SELECT mutation_started_at FROM source_cleanup_item WHERE operation_id=?`, operationID).Scan(ctx, &frontier); err != nil || frontier != nil {
		t.Fatalf("unexpected frontier=%v err=%v", frontier, err)
	}
	stale := guard
	stale.DeliveryToken += "-stale"
	if cancelled, err := repo.CancelUnstartedPublicationSourceCleanupDelivery(ctx, stale, "disabled"); err != nil || cancelled {
		t.Fatalf("stale token=%v,%v", cancelled, err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET attempt=attempt+1 WHERE id=?`, guard.RiverJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if cancelled, err := repo.CancelUnstartedPublicationSourceCleanupDelivery(ctx, guard, "disabled"); err != nil || cancelled {
		t.Fatalf("stale River attempt=%v,%v", cancelled, err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET attempt=? WHERE id=?`, guard.RiverAttempt, guard.RiverJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if cancelled, err := repo.CancelUnstartedPublicationSourceCleanupDelivery(ctx, guard, "disabled"); err != nil || !cancelled {
		t.Fatalf("valid restored delivery=%v,%v; want true,nil", cancelled, err)
	}
}

func TestCancelCleanupDeferredFailureRollsBackOwnershipDeletionPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, f, repo, guard, operationID, _, _ := setupStartedCleanupRemoval(t)
	if _, err := f.db.ExecContext(ctx, `CREATE FUNCTION fail_cleanup_cancel_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='failed' THEN RAISE EXCEPTION 'injected cleanup cancel failure'; END IF; RETURN NEW; END $$; CREATE CONSTRAINT TRIGGER fail_cleanup_cancel_commit AFTER UPDATE ON operation DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_cleanup_cancel_commit()`); err != nil {
		t.Fatal(err)
	}
	disablePublicationSourceRemoval(t, ctx, f)
	const evidenceQuery = `SELECT jsonb_build_object(
		'operation',(SELECT to_jsonb(x) FROM operation x WHERE id=?),
		'items',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM source_cleanup_item x WHERE operation_id=?),
		'owners',(SELECT jsonb_agg(to_jsonb(x) ORDER BY item_id) FROM source_cleanup_owner x WHERE operation_id=?),
		'claims',(SELECT jsonb_agg(to_jsonb(x) ORDER BY application_attempt) FROM publication_execution_claim x WHERE operation_id=?),
		'locations',(SELECT jsonb_agg(to_jsonb(x)) FROM source_location x),
	'links',(SELECT jsonb_agg(to_jsonb(x)) FROM track_source x),
	'publications',(SELECT jsonb_agg(to_jsonb(x) ORDER BY position_id) FROM track_publication x),
	'river',(SELECT to_jsonb(x) FROM river_job x WHERE id=?))::text`
	capture := func() string {
		var snapshot string
		if err := f.db.NewRaw(evidenceQuery, operationID, operationID, operationID, operationID, guard.RiverJobID).Scan(ctx, &snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	before := capture()
	if cancelled, err := repo.CancelUnstartedPublicationSourceCleanupDelivery(ctx, guard, "disabled"); err == nil || cancelled || !strings.Contains(err.Error(), "injected cleanup cancel failure") {
		t.Fatalf("cancel=%v,%v; want injected deferred rollback error", cancelled, err)
	}
	after := capture()
	if after != before {
		t.Fatalf("failed cancellation changed operation/owners/items/claims or source evidence\nbefore: %s\nafter: %s", before, after)
	}
}
