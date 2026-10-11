//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestIncomingMatchingReconcileOutboxFencesRiverAttemptsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	eventID := uuid.New()
	operationID := uuid.New()
	if _, err := database.NewRaw(`INSERT INTO incoming_matching_reconcile_event
		(id,source_operation_id,source_attempt,reason,source_input_snapshot,state,registered_job_id,dispatched_at)
		VALUES (?,?,1,'analysis_success',?::jsonb,'dispatched',?,now())`, eventID, operationID, `{"schema_version":1}`, 17).Exec(ctx); err != nil {
		t.Fatalf("insert dispatched event: %v", err)
	}

	event, err := repository.GetIncomingMatchingReconciliationEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("load event through Bun model table: %v", err)
	}
	if event.ID != eventID || event.HighestDeliveryAttempt != 0 {
		t.Fatalf("loaded event = %#v", event)
	}

	epoch2, claimed, err := repository.ClaimIncomingMatchingReconciliationDelivery(ctx, eventID, 17, 2)
	if err != nil || !claimed || epoch2 != 1 {
		t.Fatalf("claim River attempt 2 = (%d, %t, %v), want (1, true, nil)", epoch2, claimed, err)
	}
	if epoch, claimed, err := repository.ClaimIncomingMatchingReconciliationDelivery(ctx, eventID, 17, 2); err != nil || claimed || epoch != 0 {
		t.Fatalf("duplicate claim of active attempt = (%d, %t, %v), want (0, false, nil)", epoch, claimed, err)
	}
	if epoch, claimed, err := repository.ClaimIncomingMatchingReconciliationDelivery(ctx, eventID, 17, 1); err != nil || claimed || epoch != 0 {
		t.Fatalf("old River attempt stole claim = (%d, %t, %v), want (0, false, nil)", epoch, claimed, err)
	}

	epoch3, claimed, err := repository.ClaimIncomingMatchingReconciliationDelivery(ctx, eventID, 17, 3)
	if err != nil || !claimed || epoch3 != 2 {
		t.Fatalf("new River attempt takeover = (%d, %t, %v), want (2, true, nil)", epoch3, claimed, err)
	}
	if completed, err := repository.MarkIncomingMatchingReconciliationComplete(ctx, eventID, 17, 2, epoch2); err != nil || completed {
		t.Fatalf("stale completion = (%t, %v), want (false, nil)", completed, err)
	}
	if released, err := repository.ReleaseIncomingMatchingReconciliationClaim(ctx, eventID, 17, 2, epoch2); err != nil || released {
		t.Fatalf("stale release = (%t, %v), want (false, nil)", released, err)
	}
	if completed, err := repository.MarkIncomingMatchingReconciliationComplete(ctx, eventID, 17, 3, epoch3); err != nil || !completed {
		t.Fatalf("current completion = (%t, %v), want (true, nil)", completed, err)
	}
	if completed, err := repository.MarkIncomingMatchingReconciliationComplete(ctx, eventID, 17, 3, epoch3); err != nil || completed {
		t.Fatalf("terminal replay completion = (%t, %v), want (false, nil)", completed, err)
	}
	finished, err := repository.GetIncomingMatchingReconciliationEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("reload completed event: %v", err)
	}
	if finished.State != "done" || finished.HighestDeliveryAttempt != 3 || finished.DeliveryClaimed {
		t.Fatalf("completed event = %#v", finished)
	}
}

func TestIncomingMatchingReconcileClaimCanBeReleasedAndRetriedWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	eventID := uuid.New()
	if _, err := database.NewRaw(`INSERT INTO incoming_matching_reconcile_event
		(id,source_operation_id,source_attempt,reason,source_input_snapshot,state,registered_job_id,dispatched_at)
		VALUES (?,?,1,'analysis_success','{}'::jsonb,'dispatched',?,now())`, eventID, uuid.New(), 23).Exec(ctx); err != nil {
		t.Fatalf("insert dispatched event: %v", err)
	}
	epoch, claimed, err := repository.ClaimIncomingMatchingReconciliationDelivery(ctx, eventID, 23, 1)
	if err != nil || !claimed {
		t.Fatalf("claim first delivery = (%d, %t, %v)", epoch, claimed, err)
	}
	if released, err := repository.ReleaseIncomingMatchingReconciliationClaim(ctx, eventID, 23, 1, epoch); err != nil || !released {
		t.Fatalf("release failed delivery = (%t, %v), want true", released, err)
	}
	retryEpoch, claimed, err := repository.ClaimIncomingMatchingReconciliationDelivery(ctx, eventID, 23, 1)
	if err != nil || !claimed || retryEpoch <= epoch {
		t.Fatalf("retry claim = (%d, %t, %v), want a newer fenced claim", retryEpoch, claimed, err)
	}
}
