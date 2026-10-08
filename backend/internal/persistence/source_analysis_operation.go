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

// SourceAnalysisOperationDelivery is the River attempt fence required when a
// worker settles an operation. It prevents a stale delivery from failing a
// newer retry of the same durable operation.
type SourceAnalysisOperationDelivery struct {
	Attempt int
	JobID   int64
}

// SettleNormalizedSourceAnalysisOperation moves a normalized technical
// operation to a terminal state and releases all work/tool holds and delivery
// triples in the same transaction. Use SettleNormalizedSourceAnalysisDelivery
// for worker deliveries; this method is reserved for trusted internal lifecycle
// transitions that do not carry a River delivery identity.
func (repository *SourceInventoryRepository) SettleNormalizedSourceAnalysisOperation(
	ctx context.Context,
	operationID uuid.UUID,
	state, stage, safeError string,
) error {
	return repository.settleNormalizedSourceAnalysisOperation(ctx, operationID, nil, state, stage, safeError)
}

// SettleNormalizedSourceAnalysisDelivery settles only the exact persisted River
// attempt/job. A stale job cannot release holds belonging to a newer retry.
func (repository *SourceInventoryRepository) SettleNormalizedSourceAnalysisDelivery(
	ctx context.Context,
	operationID uuid.UUID,
	delivery SourceAnalysisOperationDelivery,
	state, stage, safeError string,
) error {
	if delivery.Attempt < 1 || delivery.JobID < 1 {
		return fmt.Errorf("settle source analysis delivery: valid attempt and job identity are required")
	}
	return repository.settleNormalizedSourceAnalysisOperation(ctx, operationID, &delivery, state, stage, safeError)
}

func (repository *SourceInventoryRepository) settleNormalizedSourceAnalysisOperation(
	ctx context.Context,
	operationID uuid.UUID,
	delivery *SourceAnalysisOperationDelivery,
	state, stage, safeError string,
) error {
	if operationID == uuid.Nil || (state != "succeeded" && state != "failed") || stage == "" || (state == "failed" && safeError == "") {
		return fmt.Errorf("settle source analysis: valid terminal state, stage, and safe error are required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("settle source analysis: lock output admission gate: %w", err)
		}
		var rootID *uuid.UUID
		if err := tx.NewRaw(`SELECT target_source_root_id FROM operation WHERE id=?`, operationID).Scan(ctx, &rootID); err != nil {
			return fmt.Errorf("settle source analysis: read root target: %w", err)
		}
		if rootID != nil {
			root := new(SourceRoot)
			if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, *rootID).Scan(ctx, root); err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("settle source analysis: lock source root: %w", err)
			}
			type workLocation struct {
				WorkID     uuid.UUID `bun:"work_id,type:uuid"`
				LocationID uuid.UUID `bun:"location_id,type:uuid"`
			}
			works := make([]workLocation, 0)
			if err := tx.NewRaw(`SELECT h.work_id,w.location_id FROM operation_source_work_hold h JOIN source_analysis_work w ON w.id=h.work_id WHERE h.operation_id=? ORDER BY h.work_id`, operationID).Scan(ctx, &works); err != nil {
				return fmt.Errorf("settle source analysis: list held work: %w", err)
			}
			locations := make([]uuid.UUID, 0, len(works))
			for _, work := range works {
				locations = append(locations, work.LocationID)
			}
			sort.Slice(locations, func(i, j int) bool { return strings.Compare(locations[i].String(), locations[j].String()) < 0 })
			for _, locationID := range locations {
				location := new(SourceLocation)
				if err := tx.NewRaw(`SELECT * FROM source_location WHERE id=? AND source_root_id=? FOR UPDATE`, locationID, *rootID).Scan(ctx, location); err != nil && err != sql.ErrNoRows {
					return fmt.Errorf("settle source analysis: lock held location: %w", err)
				}
			}
			for _, work := range works {
				row := new(SourceAnalysisWork)
				if err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE id=? FOR UPDATE`, work.WorkID).Scan(ctx, row); err != nil && err != sql.ErrNoRows {
					return fmt.Errorf("settle source analysis: lock held work: %w", err)
				}
			}
		}
		operation := new(Operation)
		if err := tx.NewRaw(`SELECT * FROM operation WHERE id=? FOR UPDATE`, operationID).Scan(ctx, operation); err != nil {
			return fmt.Errorf("settle source analysis: lock operation: %w", err)
		}
		if delivery != nil && (operation.Attempt != delivery.Attempt || operation.RiverJobID == nil || *operation.RiverJobID != delivery.JobID) {
			return fmt.Errorf("settle source analysis: %w", ErrSourceAnalysisStale)
		}
		if operation.SourceAnalysisMode == "" {
			return fmt.Errorf("settle source analysis: operation is not normalized source analysis")
		}
		if operation.State == "succeeded" || operation.State == "failed" {
			return nil
		}
		if operation.State != "queued" && operation.State != "running" {
			return fmt.Errorf("settle source analysis: operation is not active")
		}
		if state == "failed" {
			if _, err := tx.NewRaw(`UPDATE source_analysis_step SET state='failed',safe_error=?,skip_reason=NULL,
				execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL,
				last_operation_id=?,updated_at=now()
				WHERE execution_operation_id=? AND state IN ('queued','running')`, safeError, operationID, operationID).Exec(ctx); err != nil {
				return fmt.Errorf("settle source analysis: fail unfinished steps: %w", err)
			}
		} else {
			snapshot, err := DecodeSourceAnalysisOperationSnapshot(operation.InputSnapshot)
			if err != nil {
				return fmt.Errorf("settle source analysis: decode selected steps: %w", err)
			}
			selected := snapshot.SelectedSteps
			if snapshot.TargetStep != nil && snapshot.TargetWorkID != nil {
				selected = []SourceAnalysisStepSelection{{WorkID: *snapshot.TargetWorkID, Step: SourceStepName(*snapshot.TargetStep)}}
			}
			selectedIncomplete := false
			for _, selection := range selected {
				var complete bool
				if err := tx.NewRaw(`SELECT EXISTS (
					SELECT 1 FROM source_analysis_step
					WHERE work_id=? AND step=? AND state='succeeded'
				)`, selection.WorkID, selection.Step).Scan(ctx, &complete); err != nil {
					return fmt.Errorf("settle source analysis: check selected step: %w", err)
				}
				if !complete {
					selectedIncomplete = true
					break
				}
			}
			if selectedIncomplete {
				state = "failed"
				stage = "incomplete"
				safeError = "One or more requested analysis steps remain incomplete. Retry the failed steps."
				if _, err := tx.NewRaw(`UPDATE source_analysis_step SET state='failed', safe_error=COALESCE(safe_error, ?), skip_reason=NULL,
					execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL,
					last_operation_id=?,updated_at=now()
					WHERE execution_operation_id=? AND state IN ('queued','running')`, safeError, operationID, operationID).Exec(ctx); err != nil {
					return fmt.Errorf("settle source analysis: fail incomplete selected steps: %w", err)
				}
			}
		}
		deliveryToRelease := SourceAnalysisOperationDelivery{Attempt: operation.Attempt}
		if delivery != nil {
			deliveryToRelease = *delivery
		} else if operation.RiverJobID != nil {
			deliveryToRelease.JobID = *operation.RiverJobID
		}
		var releasedArtifactWorkIDs []uuid.UUID
		if state == "succeeded" {
			if err := tx.NewRaw(`SELECT work_id FROM source_analysis_work_artifact_binding
				WHERE borrower_operation_id=? AND borrower_operation_attempt=? AND borrower_job_id=?`,
				operationID, deliveryToRelease.Attempt, deliveryToRelease.JobID).Scan(ctx, &releasedArtifactWorkIDs); err != nil {
				return fmt.Errorf("settle source analysis: list delivery artifact bindings: %w", err)
			}
		}
		if err := releaseSourceAnalysisArtifactBindings(ctx, tx, operationID, deliveryToRelease); err != nil {
			return fmt.Errorf("settle source analysis: release artifact bindings: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE source_analysis_step SET execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL,updated_at=now()
			WHERE execution_operation_id=?`, operationID).Exec(ctx); err != nil {
			return fmt.Errorf("settle source analysis: clear step execution triples: %w", err)
		}
		if _, err := tx.NewRaw(`DELETE FROM operation_source_work_hold WHERE operation_id=?`, operationID).Exec(ctx); err != nil {
			return fmt.Errorf("settle source analysis: release work holds: %w", err)
		}
		if _, err := tx.NewRaw(`DELETE FROM operation_tool_read_hold WHERE operation_id=?`, operationID).Exec(ctx); err != nil {
			return fmt.Errorf("settle source analysis: release tool holds: %w", err)
		}
		if state == "succeeded" && len(releasedArtifactWorkIDs) > 0 {
			if err := makeCompletedSourceAnalysisArtifactsCleanupEligible(ctx, tx, releasedArtifactWorkIDs); err != nil {
				return fmt.Errorf("settle source analysis: %w", err)
			}
		}
		now := time.Now().UTC()
		operation.State = state
		operation.Stage = stage
		operation.TargetWorkID = nil
		operation.TargetStep = nil
		operation.TargetSourceRootID = nil
		operation.TargetSourceLocationID = nil
		operation.ToolsReadRequired = false
		operation.RerunTarget = false
		operation.FinishedAt = &now
		operation.UpdatedAt = now
		if state == "failed" {
			operation.SafeError = &safeError
		} else {
			operation.SafeError = nil
		}
		if _, err := tx.NewUpdate().Model(operation).Column(
			"state", "stage", "safe_error", "target_work_id", "target_step", "target_source_root_id", "target_source_location_id", "tools_read_required", "rerun_target", "finished_at", "updated_at",
		).WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("settle source analysis: update terminal operation: %w", err)
		}
		return nil
	})
}
