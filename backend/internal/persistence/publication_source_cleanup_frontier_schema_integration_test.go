//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPublicationSourceCleanupMutationFrontierSchemaPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	inserter := new(publicationSourceCleanupTestInserter)
	admitted, err := repo.AdmitPublicationSourceCleanup(ctx, PublicationSourceCleanupAdmission{
		PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration,
	}, inserter)
	if err != nil || admitted.OperationID == uuid.Nil {
		t.Fatalf("admit cleanup = %+v, %v", admitted, err)
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
	claimUUID := uuid.New()
	guard := PublicationSourceCleanupDeliveryGuard{
		OperationID: admitted.OperationID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1,
		ClaimUUID: claimUUID, BootUUID: uuid.New(), DeliveryToken: "frontier-schema", Epoch: f.guard.Epoch,
		PositionID: f.guard.ExpectedPositionID,
	}
	if outcome, err := repo.ClaimPublicationSourceCleanupDelivery(ctx, guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim cleanup delivery = %q, %v", outcome, err)
	}
	var itemID uuid.UUID
	if err := f.db.NewRaw(`SELECT id FROM source_cleanup_item WHERE operation_id=? LIMIT 1`, admitted.OperationID).Scan(ctx, &itemID); err != nil {
		t.Fatal(err)
	}
	var nullFrontiers int
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_item WHERE mutation_claim_uuid IS NULL AND mutation_started_at IS NULL AND mutation_completed_at IS NULL`).Scan(ctx, &nullFrontiers); err != nil || nullFrontiers == 0 {
		t.Fatalf("new cleanup items without frontier = %d, %v", nullFrontiers, err)
	}
	started := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	completed := started.Add(time.Minute)
	if _, err := f.db.NewRaw(`UPDATE source_cleanup_item SET mutation_claim_uuid=?,mutation_started_at=?,mutation_completed_at=? WHERE id=?`, claimUUID, started, completed, itemID).Exec(ctx); err != nil {
		t.Fatalf("store complete mutation frontier: %v", err)
	}
	var storedClaim uuid.UUID
	var storedStarted, storedCompleted time.Time
	if err := f.db.NewRaw(`SELECT mutation_claim_uuid,mutation_started_at,mutation_completed_at FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &storedClaim, &storedStarted, &storedCompleted); err != nil {
		t.Fatal(err)
	}
	if storedClaim != claimUUID || !storedStarted.Equal(started) || !storedCompleted.Equal(completed) {
		t.Fatalf("frontier roundtrip = %s/%s/%s", storedClaim, storedStarted, storedCompleted)
	}
	if _, err := f.db.NewRaw(`DELETE FROM publication_execution_claim WHERE operation_id=? AND claim_uuid=?`, admitted.OperationID, claimUUID).Exec(ctx); err == nil {
		t.Fatal("deleting a claim pinned by a cleanup frontier succeeded")
	}

	tests := []struct {
		name  string
		query string
		args  []any
	}{
		{"claim without start", `UPDATE source_cleanup_item SET mutation_claim_uuid=?,mutation_started_at=NULL,mutation_completed_at=NULL WHERE id=?`, []any{claimUUID, itemID}},
		{"start without claim", `UPDATE source_cleanup_item SET mutation_claim_uuid=NULL,mutation_started_at=? WHERE id=?`, []any{started, itemID}},
		{"completion without start", `UPDATE source_cleanup_item SET mutation_claim_uuid=NULL,mutation_completed_at=?,mutation_started_at=NULL WHERE id=?`, []any{completed, itemID}},
		{"completion before start", `UPDATE source_cleanup_item SET mutation_completed_at=?,mutation_started_at=? WHERE id=?`, []any{started.Add(-time.Second), started, itemID}},
		{"unknown claim", `UPDATE source_cleanup_item SET mutation_claim_uuid=?,mutation_started_at=? WHERE id=?`, []any{uuid.New(), started, itemID}},
		{"claim from another operation", `UPDATE source_cleanup_item SET mutation_claim_uuid=?,mutation_started_at=? WHERE id=?`, []any{f.guard.ClaimUUID, started, itemID}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.db.NewRaw(tc.query, tc.args...).Exec(ctx); err == nil {
				t.Fatal("invalid mutation frontier was accepted")
			}
		})
	}
}
