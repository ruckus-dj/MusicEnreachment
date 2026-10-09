//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestToolsExecutionClaimFencesDeliveryAndProtectsHistoryPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSetupManagerRepository(database)
	jobID := int64(9001)
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "retry:switched",
		Attempt: 1, RiverJobID: &jobID, InputSnapshot: []byte(`{}`),
	}
	if err := repository.CreateOperation(ctx, operation); err != nil {
		t.Fatalf("create tools operation: %v", err)
	}

	claimed, err := repository.ClaimToolsExecutionDelivery(ctx, operation.ID, 1, jobID, operation.Kind)
	if err != nil || !claimed {
		t.Fatalf("claim current delivery = %v, %v; want true, nil", claimed, err)
	}
	current, err := repository.GetOperation(ctx, operation.ID)
	if err != nil || current.State != "running" || current.Stage != "retry:switched" {
		t.Fatalf("operation after claim = %#v, %v; want running with prior retry stage", current, err)
	}
	if _, err := repository.ClaimToolsExecutionDelivery(ctx, operation.ID, 1, jobID, operation.Kind); !errors.Is(err, persistence.ErrToolsExecutionClaimHeld) {
		t.Fatalf("duplicate active delivery error = %v; want held claim", err)
	}
	if err := repository.AbandonToolsExecutionDelivery(ctx, operation.ID, 1, jobID, operation.Kind); err != nil {
		t.Fatalf("abandon completed callback claim: %v", err)
	}
	if claimed, err := repository.ClaimToolsExecutionDelivery(ctx, operation.ID, 1, jobID, operation.Kind); err != nil || !claimed {
		t.Fatalf("reclaim abandoned delivery = %v, %v; want true, nil", claimed, err)
	}
	claimed, err = repository.ClaimToolsExecutionDelivery(ctx, operation.ID, 1, jobID+1, operation.Kind)
	if err != nil || claimed {
		t.Fatalf("claim stale delivery = %v, %v; want false, nil", claimed, err)
	}

	if err := repository.TransitionOperation(ctx, operation.ID, func(current *persistence.Operation) error {
		current.State = "failed"
		reason := "tools operation failed"
		current.SafeError = &reason
		finished := time.Now()
		current.FinishedAt = &finished
		return nil
	}); err != nil {
		t.Fatalf("make tools operation terminal: %v", err)
	}
	if err := repository.DismissOperation(ctx, operation.ID); err == nil {
		t.Fatal("dismissed operation with unresolved tools execution claim")
	}
	if err := repository.ReleaseToolsExecutionDelivery(ctx, operation.ID, 1, jobID, operation.Kind); err != nil {
		t.Fatalf("release tools execution claim: %v", err)
	}
	if err := repository.DismissOperation(ctx, operation.ID); err != nil {
		t.Fatalf("dismiss operation after claim release: %v", err)
	}
}
