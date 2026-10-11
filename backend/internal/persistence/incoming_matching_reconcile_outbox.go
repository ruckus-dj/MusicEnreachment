package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

const IncomingMatchingReconcileJobKind = "incoming_matching_reconcile_v1"

// IncomingMatchingReconcileJobArgs carries only the durable event identity.
type IncomingMatchingReconcileJobArgs struct {
	EventID uuid.UUID `json:"event_id"`
}

func (IncomingMatchingReconcileJobArgs) Kind() string { return IncomingMatchingReconcileJobKind }

type IncomingMatchingReconcileEvent struct {
	bun.BaseModel          `bun:"table:incoming_matching_reconcile_event"`
	ID                     uuid.UUID       `bun:"id,pk,type:uuid"`
	SourceOperationID      uuid.UUID       `bun:"source_operation_id,type:uuid"`
	OriginalAttempt        int             `bun:"source_attempt"`
	Reason                 string          `bun:"reason"`
	SourceInputSnapshot    json.RawMessage `bun:"source_input_snapshot,type:jsonb"`
	State                  string          `bun:"state"`
	RegisteredJobID        *int64          `bun:"registered_job_id,nullzero"`
	DeliveryEpoch          int64           `bun:"delivery_epoch"`
	HighestDeliveryAttempt int             `bun:"highest_delivery_attempt"`
	DeliveryClaimed        bool            `bun:"delivery_claimed"`
	CreatedAt              time.Time       `bun:"created_at"`
	DispatchedAt           *time.Time      `bun:"dispatched_at,nullzero"`
	CompletedAt            *time.Time      `bun:"completed_at,nullzero"`
}

// GetIncomingMatchingReconciliationEvent returns the immutable event payload.
func (repository *SourceInventoryRepository) GetIncomingMatchingReconciliationEvent(ctx context.Context, eventID uuid.UUID) (*IncomingMatchingReconcileEvent, error) {
	if eventID == uuid.Nil {
		return nil, fmt.Errorf("get incoming matching reconciliation event: event identity is required")
	}
	event := new(IncomingMatchingReconcileEvent)
	if err := repository.db.NewSelect().Model(event).Where("id = ?", eventID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("get incoming matching reconciliation event: %w", err)
	}
	return event, nil
}

// DispatchPendingMatchingReconciliations registers each event and records its
// exact River job identity in the same transaction. River owns retry/recovery
// after dispatch; this outbox intentionally has no lease expiry or re-enqueue.
func (repository *SourceInventoryRepository) DispatchPendingMatchingReconciliations(ctx context.Context, client RiverInserter) error {
	if client == nil {
		return fmt.Errorf("dispatch matching reconciliations: River client is required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		rows := make([]IncomingMatchingReconcileEvent, 0)
		if err := tx.NewRaw(`SELECT id FROM incoming_matching_reconcile_event WHERE state='pending' ORDER BY created_at,id FOR UPDATE SKIP LOCKED`).Scan(ctx, &rows); err != nil {
			return fmt.Errorf("dispatch matching reconciliations: lock pending events: %w", err)
		}
		for _, event := range rows {
			result, err := client.InsertTx(ctx, tx.Tx, IncomingMatchingReconcileJobArgs{EventID: event.ID}, &river.InsertOpts{Queue: "matching", MaxAttempts: 20})
			if err != nil {
				return fmt.Errorf("dispatch matching reconciliation event %s: %w", event.ID, err)
			}
			if result == nil || result.Job == nil || result.Job.ID < 1 {
				return fmt.Errorf("dispatch matching reconciliation event %s: River returned no registered job identity", event.ID)
			}
			if _, err := tx.NewRaw(`UPDATE incoming_matching_reconcile_event SET state='dispatched',registered_job_id=?,dispatched_at=now() WHERE id=? AND state='pending'`, result.Job.ID, event.ID).Exec(ctx); err != nil {
				return fmt.Errorf("dispatch matching reconciliation event %s: record job: %w", event.ID, err)
			}
		}
		return nil
	})
}

// ClaimIncomingMatchingReconciliationDelivery fences any future consumer's
// work against stale River leases while leaving the original event identity intact.
func (repository *SourceInventoryRepository) ClaimIncomingMatchingReconciliationDelivery(ctx context.Context, eventID uuid.UUID, registeredJobID int64, riverAttempt int) (int64, bool, error) {
	if eventID == uuid.Nil || registeredJobID < 1 || riverAttempt < 1 {
		return 0, false, fmt.Errorf("claim matching reconciliation delivery: valid event, job, and River attempt identities are required")
	}
	var epoch int64
	var claimed bool
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`UPDATE incoming_matching_reconcile_event
			SET delivery_epoch=delivery_epoch+1, highest_delivery_attempt=?, delivery_claimed=true
			WHERE id=? AND state='dispatched' AND registered_job_id=?
			AND highest_delivery_attempt<=? AND (highest_delivery_attempt<? OR NOT delivery_claimed)
			RETURNING delivery_epoch`, riverAttempt, eventID, registeredJobID, riverAttempt, riverAttempt).Exec(ctx)
		if err != nil {
			return fmt.Errorf("claim matching reconciliation delivery: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		claimed = rows == 1
		if claimed {
			if err := tx.NewRaw(`SELECT delivery_epoch FROM incoming_matching_reconcile_event WHERE id=?`, eventID).Scan(ctx, &epoch); err != nil {
				return err
			}
		}
		return nil
	})
	return epoch, claimed, err
}

// MarkIncomingMatchingReconciliationComplete is a fenced completion hook for
// the eventual consumer; only successful consumer work should call it.
func (repository *SourceInventoryRepository) MarkIncomingMatchingReconciliationComplete(ctx context.Context, eventID uuid.UUID, registeredJobID int64, riverAttempt int, epoch int64) (bool, error) {
	if eventID == uuid.Nil || registeredJobID < 1 || riverAttempt < 1 || epoch < 1 {
		return false, fmt.Errorf("complete matching reconciliation: valid delivery fence is required")
	}
	result, err := repository.db.NewRaw(`UPDATE incoming_matching_reconcile_event SET state='done',completed_at=now(),delivery_claimed=false WHERE id=? AND state='dispatched' AND registered_job_id=? AND highest_delivery_attempt=? AND delivery_claimed AND delivery_epoch=?`, eventID, registeredJobID, riverAttempt, epoch).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("complete matching reconciliation: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// ReleaseIncomingMatchingReconciliationClaim makes a failed River delivery
// claimable again without changing the registered job or highest attempt fence.
func (repository *SourceInventoryRepository) ReleaseIncomingMatchingReconciliationClaim(ctx context.Context, eventID uuid.UUID, registeredJobID int64, riverAttempt int, epoch int64) (bool, error) {
	if eventID == uuid.Nil || registeredJobID < 1 || riverAttempt < 1 || epoch < 1 {
		return false, fmt.Errorf("release matching reconciliation claim: valid delivery fence is required")
	}
	result, err := repository.db.NewRaw(`UPDATE incoming_matching_reconcile_event SET delivery_claimed=false
		WHERE id=? AND state='dispatched' AND registered_job_id=? AND highest_delivery_attempt=? AND delivery_claimed AND delivery_epoch=?`, eventID, registeredJobID, riverAttempt, epoch).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("release matching reconciliation claim: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func insertIncomingMatchingReconcileEvent(ctx context.Context, tx bun.IDB, operation *Operation, reason string) error {
	if operation == nil || operation.ID == uuid.Nil || operation.Attempt < 1 || (reason != "analysis_success" && reason != "scan_success") {
		return fmt.Errorf("insert matching reconciliation event: valid source identity and reason are required")
	}
	snapshot := operation.InputSnapshot
	if len(snapshot) == 0 || !json.Valid(snapshot) {
		snapshot = json.RawMessage(`{}`)
	}
	if _, err := tx.NewRaw(`INSERT INTO incoming_matching_reconcile_event
		(id,source_operation_id,source_attempt,reason,source_input_snapshot)
		VALUES (?,?,?,?,?) ON CONFLICT (source_operation_id,source_attempt,reason) DO NOTHING`,
		uuid.New(), operation.ID, operation.Attempt, reason, snapshot).Exec(ctx); err != nil {
		return fmt.Errorf("insert matching reconciliation event: %w", err)
	}
	return nil
}
