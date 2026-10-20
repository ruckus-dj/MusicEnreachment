package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const PublicationReconciliationOperationKind = "publication_reconcile_v1"

// PublicationReconciliationSnapshot is source-free intent; settings are read by
// the eventual operation consumer when it starts, not captured by producers.
type PublicationReconciliationSnapshot struct {
	Version     int       `json:"version"`
	OutputEpoch uuid.UUID `json:"output_epoch"`
	PositionID  uuid.UUID `json:"position_id"`
}

// RequestPublicationReconciliationWith records explicit event-driven
// obligations. Producers acquire the output-admission gate before domain locks
// and mutations; replayed receipts must not call this helper.
func (repository *SetupManagerRepository) RequestPublicationReconciliationWith(ctx context.Context, tx bun.Tx, epoch uuid.UUID, positions []uuid.UUID, scopes []string) error {
	if tx.Tx == nil || epoch == uuid.Nil || len(positions) == 0 || len(scopes) == 0 {
		return fmt.Errorf("request publication reconciliation: epoch, positions, and scopes are required")
	}
	positionSet := make(map[uuid.UUID]struct{}, len(positions))
	for _, position := range positions {
		if position == uuid.Nil {
			return fmt.Errorf("request publication reconciliation: position is required")
		}
		positionSet[position] = struct{}{}
	}
	scopeSet := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if scope == "" || strings.TrimSpace(scope) != scope || strings.ContainsRune(scope, '\x00') {
			return fmt.Errorf("request publication reconciliation: scope is required")
		}
		scopeSet[scope] = struct{}{}
	}
	positions = make([]uuid.UUID, 0, len(positionSet))
	for position := range positionSet {
		positions = append(positions, position)
	}
	sort.Slice(positions, func(i, j int) bool { return positions[i].String() < positions[j].String() })
	scopes = make([]string, 0, len(scopeSet))
	for scope := range scopeSet {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
		return fmt.Errorf("request publication reconciliation: acquire output admission gate: %w", err)
	}
	var currentEpoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &currentEpoch); err != nil {
		return fmt.Errorf("request publication reconciliation: read current output epoch: %w", err)
	}
	if currentEpoch != epoch {
		return fmt.Errorf("request publication reconciliation: supplied output epoch is stale")
	}
	for _, position := range positions {
		var generation int64
		if err := tx.NewRaw(`INSERT INTO publication_reconciliation_request(output_epoch,position_id,requested_generation)
			VALUES (?, ?, 1) ON CONFLICT (output_epoch,position_id) DO UPDATE
			SET requested_generation=publication_reconciliation_request.requested_generation+1
			RETURNING requested_generation`, epoch, position).Scan(ctx, &generation); err != nil {
			return fmt.Errorf("request publication reconciliation generation: %w", err)
		}
		for _, scope := range scopes {
			if _, err := tx.NewRaw(`INSERT INTO publication_reconciliation_dirty_scope(output_epoch,position_id,scope,generation)
				VALUES (?,?,?,?) ON CONFLICT (output_epoch,position_id,scope) DO UPDATE SET generation=EXCLUDED.generation`, epoch, position, scope, generation).Exec(ctx); err != nil {
				return fmt.Errorf("mark publication reconciliation scope dirty: %w", err)
			}
		}
		snapshot, err := json.Marshal(PublicationReconciliationSnapshot{Version: 1, OutputEpoch: epoch, PositionID: position})
		if err != nil {
			return err
		}
		var pendingOperation uuid.UUID
		lookupErr := tx.NewRaw(`SELECT operation_id FROM publication_reconciliation_event WHERE output_epoch=? AND position_id=? AND state='pending' FOR UPDATE`, epoch, position).Scan(ctx, &pendingOperation)
		if lookupErr == nil {
			if _, err := tx.NewRaw(`UPDATE publication_reconciliation_event SET observed_generation=? WHERE operation_id=? AND state='pending'`, generation, pendingOperation).Exec(ctx); err != nil {
				return fmt.Errorf("coalesce publication reconciliation event: %w", err)
			}
			continue
		}
		if lookupErr != sql.ErrNoRows {
			return fmt.Errorf("find pending publication reconciliation event: %w", lookupErr)
		}
		operationID, eventID := uuid.New(), uuid.New()
		if _, err := tx.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot) VALUES (?,?,'queued','queued',?::jsonb)`, operationID, PublicationReconciliationOperationKind, string(snapshot)).Exec(ctx); err != nil {
			return fmt.Errorf("create publication reconciliation operation: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO publication_reconciliation_event(id,output_epoch,position_id,operation_id,observed_generation) VALUES (?,?,?,?,?)`, eventID, epoch, position, operationID, generation).Exec(ctx); err != nil {
			return fmt.Errorf("record publication reconciliation event: %w", err)
		}
	}
	return nil
}
