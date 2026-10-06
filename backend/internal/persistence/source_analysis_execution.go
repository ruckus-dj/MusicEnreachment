package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// SourceAnalysisExecution is one exact work/step execution triple owned by a
// normalized operation delivery.
type SourceAnalysisExecution struct {
	Work           SourceAnalysisWork
	Step           SourceAnalysisStep
	ExistingSHA256 *[sha256.Size]byte
}

func (repository *SourceInventoryRepository) ListNormalizedSourceAnalysisExecution(ctx context.Context, operationID uuid.UUID, operationAttempt int, jobID int64) ([]SourceAnalysisExecution, error) {
	var result []SourceAnalysisExecution
	rows := make([]struct {
		WorkID uuid.UUID `bun:"work_id,type:uuid"`
		Step   string    `bun:"step"`
	}, 0)
	err := repository.db.NewSelect().TableExpr("source_analysis_step AS s").ColumnExpr("s.work_id, s.step").
		Where("s.execution_operation_id = ? AND s.execution_operation_attempt = ? AND s.execution_job_id = ? AND s.state = 'queued'", operationID, operationAttempt, jobID).
		OrderExpr("s.work_id, s.step").Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("list normalized source analysis execution: %w", err)
	}
	for _, row := range rows {
		work, _, err := repository.GetNormalizedSourceAnalysisWork(ctx, row.WorkID)
		if err != nil {
			return nil, err
		}
		step := new(SourceAnalysisStep)
		if err := repository.db.NewSelect().Model(step).Where("work_id = ? AND step = ?", row.WorkID, row.Step).Scan(ctx); err != nil {
			return nil, fmt.Errorf("read normalized source analysis step: %w", err)
		}
		execution := SourceAnalysisExecution{Work: *work, Step: *step}
		var digest []byte
		if err := repository.db.NewRaw(`SELECT v.source_sha256 FROM source_analysis_step s JOIN media_variant v ON v.id=s.success_sha_variant_id WHERE s.work_id=? AND s.step='sha256' AND s.success_sha_variant_id IS NOT NULL`, work.ID).Scan(ctx, &digest); err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("read existing source digest: %w", err)
		}
		if len(digest) == sha256.Size {
			var value [sha256.Size]byte
			copy(value[:], digest)
			execution.ExistingSHA256 = &value
		}
		result = append(result, execution)
	}
	return result, nil
}

func (repository *SourceInventoryRepository) GetNormalizedSourceAnalysisWork(ctx context.Context, workID uuid.UUID) (*SourceAnalysisWork, *SourceLocation, error) {
	work := new(SourceAnalysisWork)
	if err := repository.db.NewSelect().Model(work).Where("id = ?", workID).Scan(ctx); err != nil {
		return nil, nil, fmt.Errorf("read normalized source analysis work: %w", err)
	}
	location := new(SourceLocation)
	if err := repository.db.NewSelect().Model(location).Where("id = ? AND source_root_id = ?", work.LocationID, work.SourceRootID).Scan(ctx); err != nil {
		return nil, nil, fmt.Errorf("read normalized source analysis location: %w", err)
	}
	return work, location, nil
}

func (repository *SourceInventoryRepository) CheckNormalizedSourceAnalysisToolHold(ctx context.Context, operationID, installationID uuid.UUID, operationAttempt int, jobID int64, step string) error {
	var held bool
	err := repository.db.NewRaw(`SELECT EXISTS(
		SELECT 1 FROM operation o JOIN operation_tool_read_hold h ON h.operation_id=o.id
		JOIN source_analysis_step s ON s.execution_operation_id=o.id
		WHERE o.id=? AND o.state='running' AND o.attempt=? AND o.river_job_id=?
		AND h.installation_id=? AND s.execution_operation_attempt=? AND s.execution_job_id=? AND s.step=? AND s.state='running'
	)`, operationID, operationAttempt, jobID, installationID, operationAttempt, jobID, step).Scan(ctx, &held)
	if err != nil {
		return fmt.Errorf("check source analysis tool hold: %w", err)
	}
	if !held {
		return fmt.Errorf("source analysis tool hold is no longer current")
	}
	return nil
}
