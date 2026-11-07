package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const sourceAnalysisChangedSafeError = "The source file changed. Start a new analysis."

// InvalidateSourceAnalysisWork revokes one exact running step delivery after its
// captured source-file check detects changed content.
func (repository *SourceInventoryRepository) InvalidateSourceAnalysisWork(ctx context.Context, claim SourceStepClaim, capturedStepAttempt int) error {
	if !validSourceStep(claim.Step) || claim.WorkID == uuid.Nil || claim.OperationID == uuid.Nil || claim.OperationAttempt < 1 || claim.JobID < 1 || capturedStepAttempt < 1 {
		return fmt.Errorf("invalidate source analysis work: valid step delivery fence is required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("invalidate source analysis work: source mutation gate: %w", err)
		}
		var alreadyInvalidated bool
		if err := tx.NewRaw(`SELECT invalidated_at IS NOT NULL FROM source_analysis_work WHERE id=?`, claim.WorkID).Scan(ctx, &alreadyInvalidated); err != nil {
			return fmt.Errorf("invalidate source analysis work: read immutable work: %w", err)
		}
		if alreadyInvalidated {
			return nil
		}
		locked, err := lockSourceAnalysisStep(ctx, tx, claim.WorkID, claim.OperationID, claim.OperationAttempt, claim.JobID, claim.Step)
		if err != nil {
			return fmt.Errorf("invalidate source analysis work: %w", err)
		}
		if locked.Step.State != "running" || locked.Step.StepAttempt != capturedStepAttempt ||
			locked.Step.ExecutionOperationID == nil || *locked.Step.ExecutionOperationID != claim.OperationID ||
			locked.Step.ExecutionOperationAttempt == nil || *locked.Step.ExecutionOperationAttempt != claim.OperationAttempt ||
			locked.Step.ExecutionJobID == nil || *locked.Step.ExecutionJobID != claim.JobID ||
			locked.Operation.State != "running" || locked.Work.InvalidatedAt != nil {
			return ErrSourceAnalysisStale
		}
		var held, executionRecorded bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM operation_source_work_hold WHERE operation_id=? AND work_id=?)`, claim.OperationID, claim.WorkID).Scan(ctx, &held); err != nil {
			return fmt.Errorf("invalidate source analysis work: verify operation hold: %w", err)
		}
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM source_analysis_work_execution
			WHERE work_id=? AND operation_id=? AND operation_attempt=? AND job_id=?
		)`, claim.WorkID, claim.OperationID, claim.OperationAttempt, claim.JobID).Scan(ctx, &executionRecorded); err != nil {
			return fmt.Errorf("invalidate source analysis work: verify execution history: %w", err)
		}
		if !held || !executionRecorded {
			return ErrSourceAnalysisStale
		}
		return invalidateCurrentSourceAnalysisWork(ctx, tx, locked.Work.ID, locked.Location.ID)
	})
}

// invalidateCurrentSourceAnalysisWork is shared by execution-time revocation
// and scan reconciliation. The caller owns root, location, and work row locks.
func invalidateCurrentSourceAnalysisWork(ctx context.Context, tx bun.IDB, workID, locationID uuid.UUID) error {
	result, err := tx.NewRaw(`UPDATE source_analysis_work
		SET invalidated_at=COALESCE(invalidated_at,now()),current_location_id=NULL
		WHERE id=? AND location_id=? AND current_location_id=? AND invalidated_at IS NULL`, workID, locationID, locationID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("detach invalidated source work: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read invalidated source work result: %w", err)
	}
	if changed == 0 {
		var invalidated bool
		if err := tx.NewRaw(`SELECT invalidated_at IS NOT NULL FROM source_analysis_work WHERE id=? AND location_id=?`, workID, locationID).Scan(ctx, &invalidated); err != nil {
			return fmt.Errorf("verify invalidated source work: %w", err)
		}
		if !invalidated {
			return ErrSourceAnalysisStale
		}
		return nil
	}
	return resetChangedSourceLocation(ctx, tx, locationID)
}

func resetChangedSourceLocation(ctx context.Context, tx bun.IDB, locationID uuid.UUID) error {
	if _, err := tx.NewRaw(`UPDATE source_location SET media_variant_id=NULL,probe_status='not_analyzed',safe_error=NULL,updated_at=now() WHERE id=?`, locationID).Exec(ctx); err != nil {
		return fmt.Errorf("reset changed source location identity: %w", err)
	}
	if err := InvalidateIncomingGrouping(ctx, tx); err != nil {
		return fmt.Errorf("invalidate incoming grouping for changed source: %w", err)
	}
	return nil
}

// SourceScanIdentityFence identifies the active scan permitted to observe a root.
type SourceScanIdentityFence struct {
	OperationID            uuid.UUID
	RootID                 uuid.UUID
	ExpectedAttempt        int
	ExpectedJobID          int64
	ExpectedConfiguredPath string
}

// SourceScanIdentityTarget binds one current source work identity to the scan
// observation that may invalidate it.
type SourceScanIdentityTarget struct {
	LocationID   uuid.UUID
	WorkID       *uuid.UUID
	RelativePath string
	SizeBytes    int64
	Mtime        time.Time
}

type SourceChangedFileObservation struct {
	Fence     SourceScanIdentityFence
	Target    SourceScanIdentityTarget
	SizeBytes int64
	Mtime     time.Time
}

// ListSourceScanIdentityTargets returns current locations and any attached work
// for the exact running scan delivery.
func (repository *SourceInventoryRepository) ListSourceScanIdentityTargets(ctx context.Context, fence SourceScanIdentityFence) ([]SourceScanIdentityTarget, error) {
	if !validSourceScanIdentityFence(fence) {
		return nil, fmt.Errorf("list source scan identity targets: valid scan delivery fence is required")
	}
	rows := make([]SourceScanIdentityTarget, 0)
	err := repository.db.NewRaw(`SELECT location.relative_path,location.id AS location_id,work.id AS work_id,location.size_bytes,location.mtime
		FROM operation scan JOIN source_root root ON root.id=scan.target_source_root_id
		JOIN source_location location ON location.source_root_id=root.id
		LEFT JOIN source_analysis_work work ON work.current_location_id=location.id AND work.location_id=location.id AND work.invalidated_at IS NULL
		WHERE scan.id=? AND scan.target_source_root_id=? AND scan.kind='scan_source' AND scan.state='running' AND scan.attempt=? AND scan.river_job_id=?
		AND root.configured_path=? AND root.inventory_path=root.configured_path
		ORDER BY location.relative_path,location.id`, fence.OperationID, fence.RootID, fence.ExpectedAttempt, fence.ExpectedJobID, fence.ExpectedConfiguredPath).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("list source scan identity targets: %w", err)
	}
	return rows, nil
}

// ObserveChangedSourceFile revokes prior work when the exact scan delivery
// confirms that a target now has changed size or mtime.
func (repository *SourceInventoryRepository) ObserveChangedSourceFile(ctx context.Context, observation SourceChangedFileObservation) error {
	fence, target := observation.Fence, observation.Target
	if !validSourceScanIdentityFence(fence) || target.LocationID == uuid.Nil || target.RelativePath == "" || observation.SizeBytes < 0 || observation.Mtime.IsZero() {
		return fmt.Errorf("observe changed source file: valid scan observation is required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("observe changed source file: source mutation gate: %w", err)
		}
		root := new(SourceRoot)
		if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, fence.RootID).Scan(ctx, root); err != nil {
			return fmt.Errorf("observe changed source file: lock root: %w", err)
		}
		if root.ConfiguredPath != fence.ExpectedConfiguredPath || root.InventoryPath == nil || *root.InventoryPath != root.ConfiguredPath {
			return ErrSourceAnalysisStale
		}
		location := new(SourceLocation)
		if err := tx.NewRaw(`SELECT * FROM source_location WHERE id=? AND source_root_id=? FOR UPDATE`, target.LocationID, fence.RootID).Scan(ctx, location); err != nil {
			return fmt.Errorf("observe changed source file: lock location: %w", err)
		}
		if location.RelativePath != target.RelativePath || location.SizeBytes != target.SizeBytes || !sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(target.Mtime)) {
			return ErrSourceAnalysisStale
		}
		work := new(SourceAnalysisWork)
		if target.WorkID == nil {
			var successorExists bool
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM source_analysis_work WHERE current_location_id=? AND invalidated_at IS NULL)`, target.LocationID).Scan(ctx, &successorExists); err != nil {
				return fmt.Errorf("observe changed source file: check current work: %w", err)
			}
			if successorExists {
				return ErrSourceAnalysisStale
			}
			operation := new(Operation)
			if err := tx.NewRaw(`SELECT * FROM operation WHERE id=? FOR UPDATE`, fence.OperationID).Scan(ctx, operation); err != nil || !validSourceScanOperation(operation, fence) {
				return ErrSourceAnalysisStale
			}
			if location.SizeBytes != observation.SizeBytes || !sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(observation.Mtime)) {
				return resetChangedSourceLocation(ctx, tx, target.LocationID)
			}
			return nil
		}
		if err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE id=? AND location_id=? FOR UPDATE`, *target.WorkID, target.LocationID).Scan(ctx, work); err != nil {
			if err == sql.ErrNoRows {
				return ErrSourceAnalysisStale
			}
			return fmt.Errorf("observe changed source file: lock current work: %w", err)
		}
		operation := new(Operation)
		if err := tx.NewRaw(`SELECT * FROM operation WHERE id=? FOR UPDATE`, fence.OperationID).Scan(ctx, operation); err != nil || !validSourceScanOperation(operation, fence) {
			return ErrSourceAnalysisStale
		}
		if work.InvalidatedAt != nil {
			return nil
		}
		if work.CurrentLocationID == nil || *work.CurrentLocationID != target.LocationID {
			return ErrSourceAnalysisStale
		}
		if work.ConfiguredPath != root.ConfiguredPath || work.InventoryPath != *root.InventoryPath {
			return ErrSourceAnalysisStale
		}
		if location.SizeBytes == observation.SizeBytes && sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(observation.Mtime)) &&
			work.SizeBytes == observation.SizeBytes && sourceAnalysisMtime(work.Mtime).Equal(sourceAnalysisMtime(observation.Mtime)) {
			return nil
		}
		return invalidateCurrentSourceAnalysisWork(ctx, tx, work.ID, target.LocationID)
	})
}

func validSourceScanIdentityFence(fence SourceScanIdentityFence) bool {
	return fence.OperationID != uuid.Nil && fence.RootID != uuid.Nil && fence.ExpectedAttempt > 0 && fence.ExpectedJobID > 0 && fence.ExpectedConfiguredPath != ""
}

func validSourceScanOperation(operation *Operation, fence SourceScanIdentityFence) bool {
	return operation.Kind == sourceScanOperationKind && operation.State == "running" && operation.TargetSourceRootID != nil &&
		*operation.TargetSourceRootID == fence.RootID && operation.Attempt == fence.ExpectedAttempt && operation.RiverJobID != nil &&
		*operation.RiverJobID == fence.ExpectedJobID
}
