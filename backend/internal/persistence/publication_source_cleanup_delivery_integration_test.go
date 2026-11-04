//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
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
	// Exercise representative unqualified execution tuples against this same
	// admitted operation, restoring the row between probes rather than paying
	// for a new migrated database per guard permutation.
	if _, err := f.db.NewRaw(`UPDATE river_job SET attempt=2 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assertOutcome(PublicationDeliveryBlocked, guard)
	if _, err := f.db.NewRaw(`UPDATE river_job SET attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET args=jsonb_build_object('operation_id',?::text,'extra',true) WHERE id=?`, admitted.OperationID, jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClaimPublicationSourceCleanupDelivery(ctx, guard); err == nil {
		t.Fatal("claim accepted unknown River argument")
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET args=?::jsonb WHERE id=?`, string(args), jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO publication_execution_claim(operation_id,application_attempt,river_job_id,river_attempt,claim_uuid,boot_uuid,delivery_token,epoch,state) VALUES (?,1,?,1,?,?,? ,?,'recovery_required')`, admitted.OperationID, jobID, uuid.New(), uuid.New(), "recovery", f.guard.Epoch).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assertOutcome(PublicationDeliveryBlocked, guard)
	if _, err := f.db.NewRaw(`DELETE FROM publication_execution_claim WHERE operation_id=?`, admitted.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
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
	assertOutcome(PublicationDeliveryAlreadyOwned, guard)
}
