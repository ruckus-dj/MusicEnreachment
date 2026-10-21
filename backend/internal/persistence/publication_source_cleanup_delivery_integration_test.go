//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

func TestClaimPublicationSourceCleanupDeliveryPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	inserter := new(publicationSourceCleanupTestInserter)
	admitted, err := repo.AdmitPublicationSourceCleanup(ctx, PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}, inserter)
	if err != nil || admitted.OperationID == uuid.Nil {
		t.Fatalf("admit cleanup = %+v, %v", admitted, err)
	}
	jobArgs, ok := inserter.args.(publicationSourceCleanupJobArgs)
	if !ok {
		t.Fatalf("job args = %#v", inserter.args)
	}
	args, err := json.Marshal(jobArgs)
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
	guard := PublicationSourceCleanupDeliveryGuard{OperationID: admitted.OperationID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "cleanup-delivery", Epoch: f.guard.Epoch, PositionID: f.guard.ExpectedPositionID}
	var ownersBefore string
	if err := f.db.NewRaw(`SELECT COALESCE(jsonb_agg(jsonb_build_array(item_id,operation_id,application_attempt,river_job_id,owner_uuid,source_root_id,source_location_id,work_id) ORDER BY item_id)::text,'[]') FROM source_cleanup_owner WHERE operation_id=?`, admitted.OperationID).Scan(ctx, &ownersBefore); err != nil {
		t.Fatal(err)
	}
	assertOutcome := func(want PublicationDeliveryClaimOutcome, g PublicationSourceCleanupDeliveryGuard) {
		t.Helper()
		got, err := repo.ClaimPublicationSourceCleanupDelivery(ctx, g)
		if err != nil || got != want {
			t.Fatalf("claim = %q, %v; want %q", got, err, want)
		}
	}
	assertOutcome(PublicationDeliveryAcquired, guard)
	assertOutcome(PublicationDeliveryAlreadyOwned, guard)
	otherToken := guard
	otherToken.DeliveryToken = "different-delivery"
	assertOutcome(PublicationDeliveryBlocked, otherToken)
	var claims, items, owners int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, admitted.OperationID).Scan(ctx, &claims); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_item WHERE operation_id=? AND state='queued'`, admitted.OperationID).Scan(ctx, &items); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=? AND application_attempt=1 AND river_job_id=?`, admitted.OperationID, jobID).Scan(ctx, &owners); err != nil {
		t.Fatal(err)
	}
	if claims != 1 || items == 0 || owners != items {
		t.Fatalf("claim/item/owner counts = %d/%d/%d", claims, items, owners)
	}
	var ownersAfter string
	if err := f.db.NewRaw(`SELECT COALESCE(jsonb_agg(jsonb_build_array(item_id,operation_id,application_attempt,river_job_id,owner_uuid,source_root_id,source_location_id,work_id) ORDER BY item_id)::text,'[]') FROM source_cleanup_owner WHERE operation_id=?`, admitted.OperationID).Scan(ctx, &ownersAfter); err != nil {
		t.Fatal(err)
	}
	if ownersAfter != ownersBefore {
		t.Fatalf("cleanup owners changed: before %s, after %s", ownersBefore, ownersAfter)
	}
	if _, err := repo.ClaimPublicationSourceCleanupDelivery(ctx, PublicationSourceCleanupDeliveryGuard{}); err == nil {
		t.Fatal("zero guard accepted")
	}
	if _, err := f.db.NewRaw(`UPDATE operation SET input_snapshot=(input_snapshot::jsonb || '{"extra":true}'::jsonb)::text::jsonb WHERE id=?`, admitted.OperationID).Exec(ctx); err == nil {
		t.Fatal("database accepted unknown operation snapshot field")
	}
	// The schema rejects malformed snapshots before the decoder can encounter
	// one, so assert the normal row remains claimable rather than bypassing it.
	assertOutcome(PublicationDeliveryAlreadyOwned, guard)
}

func TestClaimPublicationSourceCleanupDeliveryRejectsUnqualifiedRowsPostgreSQL(t *testing.T) {
	cases := []struct {
		name      string
		wantError bool
		setup     func(context.Context, *publicationExecutionFixture, *SetupManagerRepository, uuid.UUID, int64, river.JobArgs) error
	}{
		{name: "wrong River attempt", setup: func(ctx context.Context, f *publicationExecutionFixture, _ *SetupManagerRepository, _ uuid.UUID, id int64, _ river.JobArgs) error {
			_, err := f.db.NewRaw(`UPDATE river_job SET attempt=2 WHERE id=?`, id).Exec(ctx)
			return err
		}},
		{name: "wrong River kind", setup: func(ctx context.Context, f *publicationExecutionFixture, _ *SetupManagerRepository, _ uuid.UUID, id int64, _ river.JobArgs) error {
			_, err := f.db.NewRaw(`UPDATE river_job SET kind='wrong' WHERE id=?`, id).Exec(ctx)
			return err
		}},
		{name: "unknown River args", wantError: true, setup: func(ctx context.Context, f *publicationExecutionFixture, _ *SetupManagerRepository, op uuid.UUID, id int64, _ river.JobArgs) error {
			_, err := f.db.NewRaw(`UPDATE river_job SET args=jsonb_build_object('operation_id',?::text,'extra',true) WHERE id=?`, op, id).Exec(ctx)
			return err
		}},
		{name: "malformed operation summary rejected by database", setup: func(ctx context.Context, f *publicationExecutionFixture, _ *SetupManagerRepository, op uuid.UUID, _ int64, _ river.JobArgs) error {
			_, err := f.db.NewRaw(`UPDATE operation SET input_snapshot=(input_snapshot::jsonb || '{"extra":true}'::jsonb)::text::jsonb WHERE id=?`, op).Exec(ctx)
			if err == nil {
				return fmt.Errorf("database accepted unknown operation snapshot field")
			}
			return nil
		}},
		{name: "non-running River job", setup: func(ctx context.Context, f *publicationExecutionFixture, _ *SetupManagerRepository, _ uuid.UUID, id int64, _ river.JobArgs) error {
			_, err := f.db.NewRaw(`UPDATE river_job SET state='available' WHERE id=?`, id).Exec(ctx)
			return err
		}},
		{name: "disabled permission", setup: func(ctx context.Context, f *publicationExecutionFixture, _ *SetupManagerRepository, _ uuid.UUID, _ int64, _ river.JobArgs) error {
			_, err := f.db.NewRaw(`UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`).Exec(ctx)
			return err
		}},
		{name: "terminal operation", setup: func(ctx context.Context, f *publicationExecutionFixture, _ *SetupManagerRepository, op uuid.UUID, _ int64, _ river.JobArgs) error {
			_, err := f.db.NewRaw(`UPDATE operation SET state='succeeded', finished_at=now() WHERE id=?`, op).Exec(ctx)
			return err
		}},
		{name: "existing recovery claim", setup: func(ctx context.Context, f *publicationExecutionFixture, _ *SetupManagerRepository, op uuid.UUID, id int64, _ river.JobArgs) error {
			_, err := f.db.NewRaw(`INSERT INTO publication_execution_claim(operation_id,application_attempt,river_job_id,river_attempt,claim_uuid,boot_uuid,delivery_token,epoch,state) VALUES (?,1,?,1,?,?,? ,?,'recovery_required')`, op, id, uuid.New(), uuid.New(), "recovery", f.guard.Epoch).Exec(ctx)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, repo, _ := publicationSourceCleanupReadyFixture(t)
			inserter := new(publicationSourceCleanupTestInserter)
			admitted, err := repo.AdmitPublicationSourceCleanup(ctx, PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}, inserter)
			if err != nil || admitted.OperationID == uuid.Nil {
				t.Fatalf("admit = %+v, %v", admitted, err)
			}
			args, err := json.Marshal(inserter.args)
			if err != nil {
				t.Fatal(err)
			}
			var id int64
			if err := f.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, admitted.OperationID).Scan(ctx, &id); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.NewRaw(`UPDATE river_job SET state='running',attempt=1,args=?::jsonb WHERE id=?`, string(args), id).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if err := tc.setup(ctx, &f, repo, admitted.OperationID, id, inserter.args); err != nil {
				t.Fatal(err)
			}
			guard := PublicationSourceCleanupDeliveryGuard{OperationID: admitted.OperationID, ApplicationAttempt: 1, RiverJobID: id, RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "blocked-test", Epoch: f.guard.Epoch, PositionID: f.guard.ExpectedPositionID}
			got, err := repo.ClaimPublicationSourceCleanupDelivery(ctx, guard)
			if tc.wantError {
				if err == nil {
					t.Fatalf("claim = %q, nil; want decode error", got)
				}
			} else if tc.name == "malformed operation summary rejected by database" {
				if err != nil || got != PublicationDeliveryAcquired {
					t.Fatalf("claim after database rejected malformed summary = %q, %v; want acquired", got, err)
				}
			} else if err != nil || got != PublicationDeliveryBlocked {
				t.Fatalf("claim = %q, %v; want blocked", got, err)
			}
			var claims int
			wantClaims := 0
			if tc.name == "existing recovery claim" || tc.name == "malformed operation summary rejected by database" {
				wantClaims = 1
			}
			if err := f.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, admitted.OperationID).Scan(ctx, &claims); err != nil || claims != wantClaims {
				t.Fatalf("claims = %d, %v", claims, err)
			}
		})
	}
}
