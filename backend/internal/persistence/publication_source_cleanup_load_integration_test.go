//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type cleanupLoadState struct {
	OperationState string
	Attempt        int
	Snapshot       string
	ItemStates     []string
	OwnerCount     int
	ClaimCount     int
}

func readCleanupLoadState(t *testing.T, ctx context.Context, f publicationExecutionFixture, operationID uuid.UUID) cleanupLoadState {
	t.Helper()
	var state cleanupLoadState
	if err := f.db.NewRaw(`SELECT state,attempt,input_snapshot FROM operation WHERE id=?`, operationID).Scan(ctx, &state.OperationState, &state.Attempt, &state.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT state FROM source_cleanup_item WHERE operation_id=? ORDER BY id`, operationID).Scan(ctx, &state.ItemStates); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, operationID).Scan(ctx, &state.OwnerCount); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, operationID).Scan(ctx, &state.ClaimCount); err != nil {
		t.Fatal(err)
	}
	return state
}
