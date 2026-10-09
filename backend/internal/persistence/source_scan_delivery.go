package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// StartSourceScanDelivery marks a scan running only for the River delivery
// currently recorded on the operation. A late delivery is read-only.
func (repository *SourceInventoryRepository) StartSourceScanDelivery(ctx context.Context, operationID uuid.UUID, attempt int, jobID int64) error {
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("start source scan delivery: lock output admission gate: %w", err)
		}
		operation, err := scanDeliveryForUpdate(ctx, tx, operationID, attempt, jobID)
		if err != nil {
			return err
		}
		if operation.State != "queued" && operation.State != "running" {
			return fmt.Errorf("start source scan delivery: %w", ErrSourceAnalysisStale)
		}
		if operation.State == "queued" {
			var startedAt time.Time
			if err := tx.NewRaw("SELECT clock_timestamp()").Scan(ctx, &startedAt); err != nil {
				return fmt.Errorf("start source scan delivery: read database clock: %w", err)
			}
			operation.State = "running"
			operation.StartedAt = ptrTime(startedAt)
			operation.UpdatedAt = startedAt
			if _, err := tx.NewUpdate().Model(operation).Column("state", "started_at", "updated_at").WherePK().Exec(ctx); err != nil {
				return fmt.Errorf("start source scan delivery: %w", err)
			}
		}
		return nil
	})
}

// SetSourceScanDeliveryStage updates progress only while the delivery identity
// still owns the active attempt.
func (repository *SourceInventoryRepository) SetSourceScanDeliveryStage(ctx context.Context, operationID uuid.UUID, attempt int, jobID int64, stage string) error {
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		operation, err := scanDeliveryForUpdate(ctx, tx, operationID, attempt, jobID)
		if err != nil {
			return err
		}
		if operation.State != "running" {
			return fmt.Errorf("update source scan delivery: %w", ErrSourceAnalysisStale)
		}
		operation.Stage = stage
		operation.UpdatedAt = time.Now().UTC()
		if _, err := tx.NewUpdate().Model(operation).Column("stage", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("update source scan delivery: %w", err)
		}
		return nil
	})
}

// FinishSourceScanDelivery atomically cleans failed-attempt candidates and
// records its terminal state. The attempt/job fence is checked under the same
// operation lock, so an old delivery cannot affect a retry.
func (repository *SourceInventoryRepository) FinishSourceScanDelivery(ctx context.Context, operationID uuid.UUID, attempt int, jobID int64, state, stage, safeError string) error {
	if state != "failed" && state != "succeeded" {
		return fmt.Errorf("finish source scan delivery: invalid terminal state")
	}
	if state == "failed" && safeError == "" {
		return fmt.Errorf("finish source scan delivery: a safe error is required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		operation, err := scanDeliveryForUpdate(ctx, tx, operationID, attempt, jobID)
		if err != nil {
			return err
		}
		if operation.State != "queued" && operation.State != "running" {
			return fmt.Errorf("finish source scan delivery: %w", ErrSourceAnalysisStale)
		}
		if state == "failed" {
			if _, err := tx.NewDelete().Model((*SourceScanCandidate)(nil)).Where("operation_id = ?", operationID).Exec(ctx); err != nil {
				return fmt.Errorf("finish source scan delivery: delete candidates: %w", err)
			}
			errorText := safeError
			operation.SafeError = &errorText
		} else {
			operation.SafeError = nil
		}
		if _, err := tx.NewRaw(`DELETE FROM operation_tool_read_hold WHERE operation_id=?`, operationID).Exec(ctx); err != nil {
			return fmt.Errorf("finish source scan delivery: release tool holds: %w", err)
		}
		operation.ToolsReadRequired = false
		operation.State = state
		operation.Stage = stage
		operation.FinishedAt = ptrTime(time.Now().UTC())
		operation.UpdatedAt = time.Now().UTC()
		operation.TargetWorkID = nil
		operation.TargetStep = nil
		operation.TargetSourceRootID = nil
		operation.TargetSourceLocationID = nil
		operation.RerunTarget = false
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "safe_error", "finished_at", "updated_at", "target_work_id", "target_step", "target_source_root_id", "target_source_location_id", "tools_read_required", "rerun_target").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("finish source scan delivery: %w", err)
		}
		return nil
	})
}

func (repository *SourceInventoryRepository) DeleteSourceScanCandidatesForDelivery(ctx context.Context, operationID uuid.UUID, attempt int, jobID int64) error {
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		operation, err := scanDeliveryForUpdate(ctx, tx, operationID, attempt, jobID)
		if err != nil {
			return err
		}
		if operation.State != "running" {
			return fmt.Errorf("delete fenced source scan candidates: %w", ErrSourceAnalysisStale)
		}
		if _, err := tx.NewDelete().Model((*SourceScanCandidate)(nil)).Where("operation_id = ?", operationID).Exec(ctx); err != nil {
			return fmt.Errorf("delete fenced source scan candidates: %w", err)
		}
		return nil
	})
}

// DeleteSourceScanCandidatesForRootDelivery validates the root captured by a
// traversal before clearing its previous candidates. This is deliberately
// separate from the legacy delivery fence used by the existing scan pipeline.
func (repository *SourceInventoryRepository) DeleteSourceScanCandidatesForRootDelivery(ctx context.Context, operationID, rootID uuid.UUID, configuredPath string, attempt int, jobID int64) error {
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		root := new(SourceRoot)
		if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, rootID).Scan(ctx, root); err != nil {
			return fmt.Errorf("delete root-fenced source scan candidates: lock root: %w", err)
		}
		operation, err := scanDeliveryForUpdate(ctx, tx, operationID, attempt, jobID)
		if err != nil {
			return err
		}
		if operation.State != "running" || operation.TargetSourceRootID == nil || *operation.TargetSourceRootID != rootID || root.ConfiguredPath != configuredPath {
			return fmt.Errorf("delete root-fenced source scan candidates: delivery target changed: %w", ErrSourceAnalysisStale)
		}
		if _, err := tx.NewDelete().Model((*SourceScanCandidate)(nil)).Where("operation_id = ?", operationID).Exec(ctx); err != nil {
			return fmt.Errorf("delete root-fenced source scan candidates: %w", err)
		}
		return nil
	})
}

func (repository *SourceInventoryRepository) AppendSourceScanCandidatesForDelivery(ctx context.Context, operationID uuid.UUID, attempt int, jobID int64, batch []SourceScanCandidateInput) error {
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		operation, err := scanDeliveryForUpdate(ctx, tx, operationID, attempt, jobID)
		if err != nil {
			return err
		}
		if operation.State != "running" {
			return fmt.Errorf("append source scan candidates: %w", ErrSourceAnalysisStale)
		}
		if err := storeSourceScanCandidates(ctx, tx, operationID, batch); err != nil {
			return fmt.Errorf("append source scan candidates: %w", err)
		}
		return nil
	})
}

func scanDeliveryForUpdate(ctx context.Context, tx bun.Tx, operationID uuid.UUID, attempt int, jobID int64) (*Operation, error) {
	operation := new(Operation)
	if err := tx.NewRaw(`SELECT * FROM operation WHERE id=? FOR UPDATE`, operationID).Scan(ctx, operation); err != nil {
		return nil, fmt.Errorf("lock source scan delivery: %w", err)
	}
	if operation.Kind != sourceScanOperationKind || operation.Attempt != attempt || operation.RiverJobID == nil || *operation.RiverJobID != jobID {
		return nil, fmt.Errorf("source scan delivery identity changed: %w", ErrSourceAnalysisStale)
	}
	return operation, nil
}

func ptrTime(value time.Time) *time.Time { return &value }
