//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func TestPublicationSourceCleanupExplicitRetryConsumptionRequiresCompleteTuplePostgreSQL(t *testing.T) {
	t.Parallel()
	for mask := 1; mask < 7; mask++ {
		t.Run(fmt.Sprintf("partial-null-mask-%03b", mask), func(t *testing.T) {
			ctx, fixture, repository, _, originalID := failedPublicationSourceCleanupForRetry(t)
			result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalID, new(publicationSourceCleanupTestInserter))
			if err != nil || result.OperationID == uuid.Nil {
				t.Fatalf("admit valid grant: %+v, %v", result, err)
			}
			var grant SourceCleanupExplicitRetry
			if err := fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id = ?", result.OperationID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			fields := []string{"consumed_application_attempt", "consumed_river_job_id", "consumed_claim_uuid"}
			values := []any{2, int64(987654321), uuid.New()}
			sets, args := make([]string, 0, 3), make([]any, 0, 4)
			for i, field := range fields {
				if mask&(1<<i) != 0 {
					sets = append(sets, field+" = NULL")
				} else {
					sets = append(sets, field+" = ?")
					args = append(args, values[i])
				}
			}
			args = append(args, grant.ID)
			_, err = fixture.db.ExecContext(ctx, "UPDATE source_cleanup_explicit_retry SET "+strings.Join(sets, ",")+" WHERE id = ?", args...)
			if err == nil || !strings.Contains(err.Error(), "source_cleanup_explicit_retry_consumption_complete") {
				t.Fatalf("partial consumption mask %03b error = %v", mask, err)
			}
			assertRetryGrantUnconsumed(t, ctx, fixture, grant.ID, grant.Binding)
		})
	}
}

func TestPublicationSourceCleanupExplicitRetryBindingContractPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"non-object", func(b map[string]any) {}},
		{"empty-object", func(b map[string]any) { clear(b) }},
		{"missing-version", func(b map[string]any) { delete(b, "version") }},
		{"missing-original-identity", func(b map[string]any) { delete(b, "original_operation_id") }},
		{"wrong-original-identity", func(b map[string]any) { b["original_operation_id"] = uuid.NewString() }},
		{"item-object-missing-key", func(b map[string]any) { b["items"].([]any)[0].(map[string]any)["work_id"] = nil }},
		{"null-item", func(b map[string]any) { b["items"] = []any{nil} }},
		{"wrong-type", func(b map[string]any) { b["original_application_attempt"] = "1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, fixture, repository, _, originalID := failedPublicationSourceCleanupForRetry(t)
			result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalID, new(publicationSourceCleanupTestInserter))
			if err != nil || result.OperationID == uuid.Nil {
				t.Fatalf("admit valid grant: %+v, %v", result, err)
			}
			var grant SourceCleanupExplicitRetry
			if err := fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id = ?", result.OperationID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			var binding map[string]any
			if err := json.Unmarshal(grant.Binding, &binding); err != nil {
				t.Fatal(err)
			}
			if tc.name == "non-object" {
				binding = nil
			} else {
				tc.mutate(binding)
			}
			encoded, err := json.Marshal(binding)
			if err != nil {
				t.Fatal(err)
			}
			err = fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				// Retire only this test's new recovery operation inside the rejected
				// transaction; active grant-item deletion is independently fenced.
				if _, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='schema_fixture',safe_error='schema rejection fixture',finished_at=now() WHERE id=?`, result.OperationID).Exec(ctx); err != nil {
					return err
				}
				if _, err := tx.NewRaw("DELETE FROM source_cleanup_explicit_retry_item WHERE retry_id = ?", grant.ID).Exec(ctx); err != nil {
					return err
				}
				if _, err := tx.NewRaw("DELETE FROM source_cleanup_explicit_retry WHERE id = ?", grant.ID).Exec(ctx); err != nil {
					return err
				}
				_, err := tx.NewRaw(`INSERT INTO source_cleanup_explicit_retry (id,recovery_operation_id,original_operation_id,original_application_attempt,original_river_job_id,original_claim_uuid,position_id,epoch,publication_generation,binding) VALUES (?,?,?,?,?,?,?,?,?,?::jsonb)`, grant.ID, grant.RecoveryOperationID, grant.OriginalOperationID, grant.OriginalApplicationAttempt, grant.OriginalRiverJobID, grant.OriginalClaimUUID, grant.PositionID, grant.Epoch, grant.PublicationGeneration, string(encoded)).Exec(ctx)
				return err
			})
			if err == nil || !strings.Contains(err.Error(), "source_cleanup_explicit_retry_binding_contract") {
				t.Fatalf("malformed binding error = %v", err)
			}
			assertRetryGrantUnconsumed(t, ctx, fixture, grant.ID, grant.Binding)
		})
	}
}

func TestPublicationSourceCleanupExplicitRetryConsumptionIsImmutablePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, _, originalID := failedPublicationSourceCleanupForRetry(t)
	result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalID, new(publicationSourceCleanupTestInserter))
	if err != nil || result.OperationID == uuid.Nil || len(result.Blockers) != 0 {
		t.Fatalf("admit valid grant: %+v, %v", result, err)
	}
	recoveryID := result.OperationID
	var jobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, recoveryID).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	claimID := uuid.New()
	var epoch uuid.UUID
	if err := fixture.db.NewRaw(`SELECT epoch FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?`, recoveryID).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	claim := PublicationExecutionClaim{OperationID: recoveryID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1, ClaimUUID: claimID, BootUUID: uuid.New(), DeliveryToken: "test-owned-recovery", Epoch: epoch, State: "executing"}
	if _, err := fixture.db.NewInsert().Model(&claim).Exec(ctx); err != nil {
		t.Fatalf("insert valid recovery claim: %v", err)
	}
	var grant SourceCleanupExplicitRetry
	if err := fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id=?", recoveryID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.NewUpdate().Model(&grant).Set("consumed_application_attempt = ?", 1).Set("consumed_river_job_id = ?", jobID).Set("consumed_claim_uuid = ?", claimID).WherePK().Exec(ctx); err != nil {
		t.Fatalf("consume grant with matching full claim: %v", err)
	}
	update := func(attempt int, job int64, claim uuid.UUID) error {
		_, err := fixture.db.NewRaw(`UPDATE source_cleanup_explicit_retry SET consumed_application_attempt=?,consumed_river_job_id=?,consumed_claim_uuid=? WHERE id=?`, attempt, job, claim, grant.ID).Exec(ctx)
		return err
	}
	if err := update(1, jobID, claimID); err != nil {
		t.Fatalf("idempotent exact consumption update: %v", err)
	}
	for _, tc := range []struct {
		name   string
		update func() error
	}{
		{"reset", func() error {
			_, err := fixture.db.NewRaw(`UPDATE source_cleanup_explicit_retry SET consumed_application_attempt=NULL,consumed_river_job_id=NULL,consumed_claim_uuid=NULL WHERE id=?`, grant.ID).Exec(ctx)
			return err
		}},
		{"replace-claim", func() error { return update(1, jobID, uuid.New()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.update()
			if err == nil || !strings.Contains(err.Error(), "source cleanup retry consumption is immutable") {
				t.Fatalf("mutation error = %v", err)
			}
			var got SourceCleanupExplicitRetry
			if err := fixture.db.NewSelect().Model(&got).Where("id=?", grant.ID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if got.ConsumedApplicationAttempt == nil || *got.ConsumedApplicationAttempt != 1 || got.ConsumedRiverJobID == nil || *got.ConsumedRiverJobID != jobID || got.ConsumedClaimUUID == nil || *got.ConsumedClaimUUID != claimID {
				t.Fatalf("rejected mutation changed consumed authority: %+v (original operation %s)", got, originalID)
			}
		})
	}
}

func assertRetryGrantUnconsumed(t *testing.T, ctx context.Context, fixture publicationExecutionFixture, id uuid.UUID, binding json.RawMessage) {
	t.Helper()
	var grant SourceCleanupExplicitRetry
	if err := fixture.db.NewSelect().Model(&grant).Where("id = ?", id).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if grant.ConsumedApplicationAttempt != nil || grant.ConsumedRiverJobID != nil || grant.ConsumedClaimUUID != nil {
		t.Fatalf("grant consumed after rejected mutation: %+v", grant)
	}
	var before, after map[string]any
	if err := json.Unmarshal(binding, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(grant.Binding, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("binding changed: before=%v after=%v", before, after)
	}
}
