//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Keep the SQL-guard cases on one admitted retry: setting up the publication
// cleanup lifecycle is deliberately expensive, while each rejected mutation is
// isolated by its own rolled-back transaction.
func TestPublicationSourceCleanupExplicitRetrySchemaGuardsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, _, originalID := failedPublicationSourceCleanupForRetry(t)
	result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalID, new(publicationSourceCleanupTestInserter))
	if err != nil || result.OperationID == uuid.Nil || len(result.Blockers) != 0 {
		t.Fatalf("admit valid grant baseline: %+v, %v", result, err)
	}
	var grant SourceCleanupExplicitRetry
	if err := fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id = ?", result.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}

	// PostgreSQL CHECK treats NULL as success unless the expression is TRUE.
	// Prove the all-NULL baseline first, then reject all six partial-null masks.
	for mask := 1; mask < 7; mask++ {
		sets, args := make([]string, 0, 3), make([]any, 0, 4)
		fields, values := []string{"consumed_application_attempt", "consumed_river_job_id", "consumed_claim_uuid"}, []any{2, int64(987654321), uuid.New()}
		for i, field := range fields {
			if mask&(1<<i) != 0 {
				sets = append(sets, field+" = NULL")
			} else {
				sets = append(sets, field+" = ?")
				args = append(args, values[i])
			}
		}
		args = append(args, grant.ID)
		assertRetrySchemaRejects(t, ctx, fixture.db, "source_cleanup_explicit_retry_consumption_complete", func(tx bun.Tx) error {
			_, err := tx.NewRaw("UPDATE source_cleanup_explicit_retry SET "+strings.Join(sets, ",")+" WHERE id = ?", args...).Exec(ctx)
			return err
		})
	}

	var binding map[string]any
	if err := json.Unmarshal(grant.Binding, &binding); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"non-object", nil},
		{"empty-object", func(b map[string]any) { clear(b) }},
		{"missing-version", func(b map[string]any) { delete(b, "version") }},
		{"missing-original-identity", func(b map[string]any) { delete(b, "original_operation_id") }},
		{"wrong-original-identity", func(b map[string]any) { b["original_operation_id"] = uuid.NewString() }},
		{"item-object-missing-key", func(b map[string]any) { b["items"].([]any)[0].(map[string]any)["work_id"] = nil }},
		{"null-item", func(b map[string]any) { b["items"] = []any{nil} }},
		{"wrong-type", func(b map[string]any) { b["original_application_attempt"] = "1" }},
	}
	for _, tc := range cases {
		candidate := make(map[string]any, len(binding))
		encodedBinding, err := json.Marshal(binding)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encodedBinding, &candidate); err != nil {
			t.Fatal(err)
		}
		if tc.mutate != nil {
			tc.mutate(candidate)
		}
		encoded, err := json.Marshal(candidate)
		if tc.name == "non-object" {
			encoded = []byte("null")
			err = nil
		}
		if err != nil {
			t.Fatal(err)
		}
		assertRetrySchemaRejects(t, ctx, fixture.db, "source_cleanup_explicit_retry_binding_contract", func(tx bun.Tx) error {
			if _, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='schema_fixture',safe_error='schema rejection fixture',finished_at=now() WHERE id=?`, grant.RecoveryOperationID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw("DELETE FROM source_cleanup_explicit_retry_item WHERE retry_id = ?", grant.ID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw("DELETE FROM source_cleanup_explicit_retry WHERE id = ?", grant.ID).Exec(ctx); err != nil {
				return err
			}
			_, err := tx.NewRaw(`INSERT INTO source_cleanup_explicit_retry
				(id,recovery_operation_id,original_operation_id,original_application_attempt,original_river_job_id,original_claim_uuid,position_id,epoch,publication_generation,binding)
				VALUES (?,?,?,?,?,?,?,?,?,?::jsonb)`, grant.ID, grant.RecoveryOperationID, grant.OriginalOperationID,
				grant.OriginalApplicationAttempt, grant.OriginalRiverJobID, grant.OriginalClaimUUID,
				grant.PositionID, grant.Epoch, grant.PublicationGeneration, string(encoded)).Exec(ctx)
			return err
		})
	}

	var recoveryJobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, grant.RecoveryOperationID).Scan(ctx, &recoveryJobID); err != nil {
		t.Fatal(err)
	}
	var epoch uuid.UUID
	if err := fixture.db.NewRaw(`SELECT epoch FROM source_cleanup_explicit_retry WHERE id=?`, grant.ID).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	claimID := uuid.New()
	claim := PublicationExecutionClaim{OperationID: grant.RecoveryOperationID, ApplicationAttempt: 1, RiverJobID: recoveryJobID,
		RiverAttempt: 1, ClaimUUID: claimID, BootUUID: uuid.New(), DeliveryToken: "test-owned-recovery", Epoch: epoch, State: "executing"}
	if _, err := fixture.db.NewInsert().Model(&claim).Exec(ctx); err != nil {
		t.Fatalf("insert valid recovery claim: %v", err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE source_cleanup_explicit_retry SET consumed_application_attempt=1,consumed_river_job_id=?,consumed_claim_uuid=? WHERE id=?`, recoveryJobID, claimID, grant.ID).Exec(ctx); err != nil {
		t.Fatalf("consume grant with matching full claim: %v", err)
	}
	// Exact replay is idempotent; resetting or rebinding consumed authority is not.
	if _, err := fixture.db.NewRaw(`UPDATE source_cleanup_explicit_retry SET consumed_application_attempt=1,consumed_river_job_id=?,consumed_claim_uuid=? WHERE id=?`, recoveryJobID, claimID, grant.ID).Exec(ctx); err != nil {
		t.Fatalf("idempotent exact consumption update: %v", err)
	}
	for _, mutation := range []struct {
		name string
		sql  string
		args []any
	}{
		{"reset", `UPDATE source_cleanup_explicit_retry SET consumed_application_attempt=NULL,consumed_river_job_id=NULL,consumed_claim_uuid=NULL WHERE id=?`, []any{grant.ID}},
		{"rebind-claim", `UPDATE source_cleanup_explicit_retry SET consumed_application_attempt=1,consumed_river_job_id=?,consumed_claim_uuid=? WHERE id=?`, []any{recoveryJobID, uuid.New(), grant.ID}},
	} {
		assertRetrySchemaRejects(t, ctx, fixture.db, "source cleanup retry consumption is immutable", func(tx bun.Tx) error {
			_, err := tx.NewRaw(mutation.sql, mutation.args...).Exec(ctx)
			return err
		})
	}
}

func assertRetrySchemaRejects(t *testing.T, ctx context.Context, db *bun.DB, message string, mutate func(bun.Tx) error) {
	t.Helper()
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error { return mutate(tx) })
	if err == nil || !strings.Contains(err.Error(), message) {
		t.Fatalf("schema mutation error = %v, want %q", err, message)
	}
}
