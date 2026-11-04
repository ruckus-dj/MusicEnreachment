//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"

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

}
