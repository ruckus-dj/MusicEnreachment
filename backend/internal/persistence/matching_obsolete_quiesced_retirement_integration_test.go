//go:build integration

package persistence_test

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"testing"
)

// ageQuiescedMatchingRunForRetirement simulates elapsed retention time without
// restoring historical migration staging. The current quiescence writer first
// creates a valid marker; only this transaction's focused test fixture disables
// that one timestamp trigger while aging the marker, and re-enables it before
// commit. Production timestamp/write-once guard tests remain enabled and separate.
func ageQuiescedMatchingRunForRetirement(t *testing.T, ctx context.Context, db *bun.DB, runID uuid.UUID) {
	t.Helper()
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var valid bool
		if err := tx.NewRaw(`SELECT quiesced_at IS NOT NULL AND active_river_job_id IS NULL
			AND active_river_attempt IS NULL AND active_execution_epoch IS NULL
			FROM matching_run WHERE id=? FOR UPDATE`, runID).Scan(ctx, &valid); err != nil {
			return err
		}
		if !valid {
			return fmt.Errorf("matching run is not in a current quiesced state")
		}
		if _, err := tx.ExecContext(ctx, `ALTER TABLE matching_run DISABLE TRIGGER matching_run_quiescence_timestamp_immutable`); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE matching_run SET quiesced_at=transaction_timestamp()-interval '25 hours' WHERE id=?`, runID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `ALTER TABLE matching_run ENABLE TRIGGER matching_run_quiescence_timestamp_immutable`)
		return err
	}); err != nil {
		t.Fatalf("age valid quiescence marker in a focused test transaction: %v", err)
	}
}
