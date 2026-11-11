package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// lockAndValidateLibraryDeletionRiver must run after the output gate and while
// the operation row is locked. It fences settlement against the actual live
// River delivery, not merely the job ID copied into the application operation.
func lockAndValidateLibraryDeletionRiver(ctx context.Context, tx bun.Tx, guard LibraryDeletionDeliveryGuard) error {
	var kind, state string
	var applicationAttempt int
	var riverJobID sql.NullInt64
	if err := tx.NewRaw(`SELECT kind,state,attempt,river_job_id FROM operation WHERE id=? FOR UPDATE`, guard.OperationID).
		Scan(ctx, &kind, &state, &applicationAttempt, &riverJobID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return fmt.Errorf("lock library deletion operation delivery: %w", err)
	}
	if kind != LibraryDeletionOperationKind || state != "running" || applicationAttempt != guard.ApplicationAttempt ||
		!riverJobID.Valid || riverJobID.Int64 != guard.RiverJobID {
		return sql.ErrNoRows
	}

	var riverKind, riverState string
	var riverAttempt int
	var rawArgs []byte
	if err := tx.NewRaw(`SELECT kind,state,attempt,args FROM river_job WHERE id=? FOR UPDATE`, guard.RiverJobID).
		Scan(ctx, &riverKind, &riverState, &riverAttempt, &rawArgs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return fmt.Errorf("lock library deletion River delivery: %w", err)
	}
	var args libraryDeletionRiverArgs
	if err := decodeStrictJSON(rawArgs, &args); err != nil {
		return fmt.Errorf("decode library deletion River args: %w", err)
	}
	if riverKind != LibraryDeletionOperationKind || riverState != "running" || riverAttempt != guard.RiverAttempt || args.OperationID != guard.OperationID {
		return sql.ErrNoRows
	}
	return nil
}
