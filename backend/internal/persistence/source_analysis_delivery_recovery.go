package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// RecoverNormalizedSourceAnalysisDelivery returns only the exact orphaned
// delivery to pending work. A stale worker cannot clear fences or holds for a
// retry that has already installed a newer attempt/job identity.
func (repository *SourceInventoryRepository) RecoverNormalizedSourceAnalysisDelivery(
	ctx context.Context,
	operationID uuid.UUID,
	delivery SourceAnalysisOperationDelivery,
	safeError string,
) error {
	if operationID == uuid.Nil || delivery.Attempt < 1 || delivery.JobID < 1 || safeError == "" {
		return fmt.Errorf("recover source analysis delivery: valid operation, delivery, and safe error are required")
	}
	captured := new(Operation)
	if err := repository.db.NewRaw(`SELECT * FROM operation WHERE id=?`, operationID).Scan(ctx, captured); err != nil {
		return fmt.Errorf("recover source analysis delivery: read operation: %w", err)
	}
	if captured.Attempt != delivery.Attempt || captured.RiverJobID == nil || *captured.RiverJobID != delivery.JobID {
		return fmt.Errorf("recover source analysis delivery: %w", ErrSourceAnalysisStale)
	}
	return recoverInterruptedNormalizedSourceAnalysis(ctx, repository.db, captured, delivery, safeError, true)
}

func recoverInterruptedNormalizedSourceAnalysis(
	ctx context.Context,
	db bun.IDB,
	captured *Operation,
	delivery SourceAnalysisOperationDelivery,
	safeError string,
	strictFence bool,
) error {
	operationID := captured.ID
	type heldWork struct {
		WorkID     uuid.UUID `bun:"work_id,type:uuid"`
		LocationID uuid.UUID `bun:"location_id,type:uuid"`
		RootID     uuid.UUID `bun:"source_root_id,type:uuid"`
	}
	var held []heldWork
	if err := db.NewRaw(`SELECT h.work_id,w.location_id,w.source_root_id FROM operation_source_work_hold h
		JOIN source_analysis_work w ON w.id=h.work_id WHERE h.operation_id=? ORDER BY h.work_id`, operationID).Scan(ctx, &held); err != nil {
		return fmt.Errorf("recover source analysis: read held work: %w", err)
	}
	return db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		rootIDs := make(map[uuid.UUID]struct{}, len(held)+1)
		if captured.TargetSourceRootID != nil {
			rootIDs[*captured.TargetSourceRootID] = struct{}{}
		}
		for _, work := range held {
			rootIDs[work.RootID] = struct{}{}
		}
		orderedRoots := make([]uuid.UUID, 0, len(rootIDs))
		for id := range rootIDs {
			orderedRoots = append(orderedRoots, id)
		}
		sort.Slice(orderedRoots, func(i, j int) bool { return strings.Compare(orderedRoots[i].String(), orderedRoots[j].String()) < 0 })
		for _, id := range orderedRoots {
			root := new(SourceRoot)
			if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, id).Scan(ctx, root); err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("recover source analysis: lock source root: %w", err)
			}
		}
		type heldLocation struct {
			RootID     uuid.UUID
			LocationID uuid.UUID
		}
		locations := make([]heldLocation, 0, len(held))
		for _, work := range held {
			locations = append(locations, heldLocation{RootID: work.RootID, LocationID: work.LocationID})
		}
		sort.Slice(locations, func(i, j int) bool {
			if locations[i].RootID != locations[j].RootID {
				return strings.Compare(locations[i].RootID.String(), locations[j].RootID.String()) < 0
			}
			return strings.Compare(locations[i].LocationID.String(), locations[j].LocationID.String()) < 0
		})
		for _, held := range locations {
			location := new(SourceLocation)
			if err := tx.NewRaw(`SELECT * FROM source_location WHERE id=? AND source_root_id=? FOR UPDATE`, held.LocationID, held.RootID).Scan(ctx, location); err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("recover source analysis: lock held location: %w", err)
			}
		}
		for _, work := range held {
			row := new(SourceAnalysisWork)
			if err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE id=? FOR UPDATE`, work.WorkID).Scan(ctx, row); err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("recover source analysis: lock held work: %w", err)
			}
		}
		operation := new(Operation)
		if err := tx.NewRaw(`SELECT * FROM operation WHERE id=? FOR UPDATE`, operationID).Scan(ctx, operation); err != nil {
			return fmt.Errorf("recover source analysis: lock operation: %w", err)
		}
		if operation.Attempt != delivery.Attempt || operation.RiverJobID == nil || *operation.RiverJobID != delivery.JobID ||
			operation.Kind != captured.Kind || operation.SourceAnalysisMode != captured.SourceAnalysisMode ||
			!sameOptionalUUID(operation.TargetSourceRootID, captured.TargetSourceRootID) ||
			!sameOptionalUUID(operation.TargetSourceLocationID, captured.TargetSourceLocationID) ||
			!sameOptionalUUID(operation.TargetWorkID, captured.TargetWorkID) ||
			!sameOptionalString(operation.TargetStep, captured.TargetStep) || operation.RerunTarget != captured.RerunTarget {
			if strictFence {
				return fmt.Errorf("recover source analysis delivery: %w", ErrSourceAnalysisStale)
			}
			return nil
		}
		if operation.State == "succeeded" || operation.State == "failed" {
			return nil
		}
		if operation.SourceAnalysisMode == "" || (operation.State != "queued" && operation.State != "running") {
			return fmt.Errorf("recover source analysis: operation is not active normalized analysis")
		}
		var live bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM river_job WHERE id=? AND state IN ('available','pending','retryable','scheduled'))`, *operation.RiverJobID).Scan(ctx, &live); err != nil {
			return fmt.Errorf("recover source analysis: check River delivery: %w", err)
		}
		if live {
			return nil
		}
		if _, err := tx.NewRaw(`UPDATE source_analysis_step SET state='pending',safe_error=NULL,skip_reason=NULL,
			execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL,last_operation_id=?,updated_at=now()
			WHERE execution_operation_id=? AND execution_operation_attempt=? AND execution_job_id=? AND state IN ('queued','running')`,
			operationID, operationID, operation.Attempt, *operation.RiverJobID).Exec(ctx); err != nil {
			return fmt.Errorf("recover source analysis: reset interrupted steps: %w", err)
		}
		if _, err := tx.NewRaw(`DELETE FROM operation_source_work_hold WHERE operation_id=?`, operationID).Exec(ctx); err != nil {
			return fmt.Errorf("recover source analysis: release work holds: %w", err)
		}
		if _, err := tx.NewRaw(`DELETE FROM operation_tool_read_hold WHERE operation_id=?`, operationID).Exec(ctx); err != nil {
			return fmt.Errorf("recover source analysis: release tool holds: %w", err)
		}
		now := time.Now().UTC()
		operation.State = "failed"
		operation.Stage = "recovered"
		operation.SafeError = &safeError
		operation.TargetWorkID = nil
		operation.TargetStep = nil
		operation.TargetSourceRootID = nil
		operation.TargetSourceLocationID = nil
		operation.ToolsReadRequired = false
		operation.RerunTarget = false
		operation.FinishedAt = &now
		operation.UpdatedAt = now
		if _, err := tx.NewUpdate().Model(operation).Column(
			"state", "stage", "safe_error", "target_work_id", "target_step", "target_source_root_id", "target_source_location_id", "tools_read_required", "rerun_target", "finished_at", "updated_at",
		).WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("recover source analysis: mark operation recovered: %w", err)
		}
		return nil
	})
}
