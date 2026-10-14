package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// IncomingMatchingCurrentRequest is a coalesced obligation to reconcile the
// current projection for one group. Generations are independent of run
// high-water marks and immutable source-event history.
type IncomingMatchingCurrentRequest struct {
	GroupID              uuid.UUID `bun:"group_id"`
	RequestedGeneration  int64     `bun:"requested_generation"`
	ReconciledGeneration int64     `bun:"reconciled_generation"`
}

// RequestIncomingMatchingReconciliationWith records current-state obligations
// and a durable aggregate wake in the caller's transaction.
func RequestIncomingMatchingReconciliationWith(ctx context.Context, tx bun.IDB, groupIDs []uuid.UUID) error {
	if tx == nil {
		return fmt.Errorf("request incoming matching reconciliation: transaction is required")
	}
	unique := make(map[uuid.UUID]struct{}, len(groupIDs))
	for _, id := range groupIDs {
		if id == uuid.Nil {
			return fmt.Errorf("request incoming matching reconciliation: group identity is required")
		}
		unique[id] = struct{}{}
	}
	ids := make([]uuid.UUID, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	if len(ids) == 0 {
		return nil
	}
	if _, err := tx.NewRaw(`INSERT INTO incoming_matching_generation (group_id, requested_generation)
		SELECT id, 1 FROM unnest(?::uuid[]) AS groups(id) ORDER BY id
		ON CONFLICT (group_id) DO UPDATE
		SET requested_generation=incoming_matching_generation.requested_generation+1, updated_at=now()`, uuidArray(ids)).Exec(ctx); err != nil {
		return fmt.Errorf("request incoming matching reconciliation: %w", err)
	}
	return wakeCurrentIncomingMatchingReconciliationWith(ctx, tx)
}

func requestAllAutomaticIncomingMatchingReconciliationWith(ctx context.Context, tx bun.IDB) error {
	var currentRequestsAvailable bool
	if err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='incoming_matching_generation' AND column_name='requested_generation'
	)`).Scan(ctx, &currentRequestsAvailable); err != nil {
		return fmt.Errorf("check current incoming matching request schema: %w", err)
	}
	// Migration rollback/incremental migration tests exercise older schemas.
	// Runtime startup applies all migrations before these writes can occur.
	if !currentRequestsAvailable {
		return nil
	}
	result, err := tx.NewRaw(`INSERT INTO incoming_matching_generation (group_id, requested_generation)
		SELECT groups.id, 1 FROM incoming_group groups
		LEFT JOIN incoming_group_matching_authority authority ON authority.group_id=groups.id
		WHERE groups.manual=false AND COALESCE(authority.mode, 'automatic')='automatic'
		ORDER BY groups.id
		ON CONFLICT (group_id) DO UPDATE
		SET requested_generation=incoming_matching_generation.requested_generation+1, updated_at=now()`).Exec(ctx)
	if err != nil {
		return fmt.Errorf("request automatic incoming matching reconciliation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count automatic incoming matching requests: %w", err)
	}
	if count == 0 {
		return nil
	}
	return wakeCurrentIncomingMatchingReconciliationWith(ctx, tx)
}

func wakeCurrentIncomingMatchingReconciliationWith(ctx context.Context, tx bun.IDB) error {
	if _, err := tx.NewRaw(`INSERT INTO incoming_matching_reconcile_event
		(id, reason, source_input_snapshot) VALUES (?, 'current', '{}'::jsonb)
		ON CONFLICT (reason) WHERE reason='current' AND state='pending' DO NOTHING`, uuid.New()).Exec(ctx); err != nil {
		return fmt.Errorf("wake current incoming matching reconciliation: %w", err)
	}
	return nil
}

// ReadPendingCurrentRequests returns the current obligations, never a frozen
// source-event snapshot.
func (repository *SourceInventoryRepository) ReadPendingCurrentRequests(ctx context.Context) ([]IncomingMatchingCurrentRequest, error) {
	requests := make([]IncomingMatchingCurrentRequest, 0)
	if err := repository.db.NewRaw(`SELECT group_id, requested_generation, reconciled_generation
		FROM incoming_matching_generation WHERE requested_generation > reconciled_generation
		ORDER BY group_id`).Scan(ctx, &requests); err != nil {
		return nil, fmt.Errorf("read pending current incoming matching requests: %w", err)
	}
	return requests, nil
}

// AcknowledgeCurrentRequestWith acknowledges no more than the generation the
// consumer observed. A concurrent later request remains pending.
func (repository *SourceInventoryRepository) AcknowledgeCurrentRequestWith(ctx context.Context, tx bun.IDB, groupID uuid.UUID, observedGeneration int64) error {
	if tx == nil || groupID == uuid.Nil || observedGeneration < 1 {
		return fmt.Errorf("acknowledge current incoming matching request: valid transaction, group, and generation are required")
	}
	if _, err := tx.NewRaw(`UPDATE incoming_matching_generation
		SET reconciled_generation=GREATEST(reconciled_generation, ?), updated_at=now()
		WHERE group_id=? AND requested_generation >= ? AND reconciled_generation < ?`,
		observedGeneration, groupID, observedGeneration, observedGeneration).Exec(ctx); err != nil {
		return fmt.Errorf("acknowledge current incoming matching request for group %s: %w", groupID, err)
	}
	return nil
}

// AcknowledgeCurrentRequest commits only the observed obligation generation.
func (repository *SourceInventoryRepository) AcknowledgeCurrentRequest(ctx context.Context, groupID uuid.UUID, observedGeneration int64) error {
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repository.AcknowledgeCurrentRequestWith(ctx, tx, groupID, observedGeneration)
	})
}

// AcknowledgeRemovedCurrentGroup acknowledges an observed request only when a
// fresh, locked grouping projection proves that its group was removed. The
// source mutation gate prevents a concurrent grouping replacement or recreation
// until this transaction commits.
func (repository *SourceInventoryRepository) AcknowledgeRemovedCurrentGroup(ctx context.Context, groupID uuid.UUID, observedGeneration int64) (bool, error) {
	if repository == nil || groupID == uuid.Nil || observedGeneration < 1 {
		return false, fmt.Errorf("acknowledge removed current incoming group: valid group and generation are required")
	}
	var acknowledged bool
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("acknowledge removed current incoming group: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("acknowledge removed current incoming group: lock source mutation gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
			return fmt.Errorf("acknowledge removed current incoming group: lock provider configuration: %w", err)
		}
		var needsRefresh bool
		if err := tx.NewRaw(`SELECT needs_refresh FROM incoming_grouping_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &needsRefresh); err != nil {
			return fmt.Errorf("acknowledge removed current incoming group: lock grouping state: %w", err)
		}
		if needsRefresh {
			return nil
		}
		var exists bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM incoming_group WHERE id=?)`, groupID).Scan(ctx, &exists); err != nil {
			return fmt.Errorf("acknowledge removed current incoming group: check current grouping: %w", err)
		}
		if exists {
			return nil
		}
		result, err := tx.NewRaw(`UPDATE incoming_matching_generation generation
				SET reconciled_generation=GREATEST(generation.reconciled_generation, ?),
					current_run_id=CASE WHEN EXISTS (
						SELECT 1 FROM matching_run run
						JOIN operation ON operation.id=run.operation_id
						LEFT JOIN matching_run_target target ON target.run_id=run.id AND target.group_id=generation.group_id
						WHERE run.id=generation.current_run_id
							AND run.state IN ('succeeded','failed')
							AND operation.state IN ('succeeded','failed')
							AND (target.run_id IS NULL OR target.target_generation <= ?)
					) THEN NULL ELSE generation.current_run_id END,
					updated_at=now()
				WHERE generation.group_id=? AND generation.requested_generation >= ? AND generation.reconciled_generation < ?`,
			observedGeneration, observedGeneration, groupID, observedGeneration, observedGeneration).Exec(ctx)
		if err != nil {
			return fmt.Errorf("acknowledge removed current incoming group: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("acknowledge removed current incoming group: count acknowledged requests: %w", err)
		}
		acknowledged = count == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	return acknowledged, nil
}

// IsCurrentMatchingReconciliationEvent distinguishes current-state wakeups from
// immutable source-event reconciliations without putting payload in River args.
func (repository *SourceInventoryRepository) IsCurrentMatchingReconciliationEvent(ctx context.Context, eventID uuid.UUID) (bool, error) {
	var current bool
	if err := repository.db.NewRaw(`SELECT reason='current' FROM incoming_matching_reconcile_event WHERE id=?`, eventID).Scan(ctx, &current); err != nil {
		return false, fmt.Errorf("read matching reconciliation event reason: %w", err)
	}
	return current, nil
}
