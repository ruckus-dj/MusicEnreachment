package persistence

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// retireSourceAnalysisWork detaches one immutable work identity from live
// inventory. Work with retained artifacts remains as a permanent tombstone;
// artifact-free work is deleted after its steps and unreferenced fingerprint
// results have been cleaned up.
func retireSourceAnalysisWork(ctx context.Context, tx bun.IDB, workID uuid.UUID) error {
	if workID == uuid.Nil {
		return fmt.Errorf("retire source analysis work: work identity is required")
	}
	var work SourceAnalysisWork
	if err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE id=? FOR UPDATE`, workID).Scan(ctx, &work); err != nil {
		return fmt.Errorf("retire source analysis work: read work: %w", err)
	}
	var active bool
	if err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM operation_source_work_hold hold
		JOIN operation operation ON operation.id=hold.operation_id
		WHERE hold.work_id=? AND operation.state IN ('queued','running')
	)`, workID).Scan(ctx, &active); err != nil {
		return fmt.Errorf("retire source analysis work: check active holds: %w", err)
	}
	if active {
		return fmt.Errorf("retire source analysis work: work has an active hold")
	}
	var resultIDs []uuid.UUID
	if err := tx.NewRaw(`SELECT success_fingerprint_result_id FROM source_analysis_step
		WHERE work_id=? AND success_fingerprint_result_id IS NOT NULL`, workID).Scan(ctx, &resultIDs); err != nil {
		return fmt.Errorf("retire source analysis work: read fingerprint references: %w", err)
	}
	if _, err := tx.NewRaw(`DELETE FROM source_analysis_step WHERE work_id=?`, workID).Exec(ctx); err != nil {
		return fmt.Errorf("retire source analysis work: delete steps: %w", err)
	}
	var hasArtifacts bool
	if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM source_analysis_artifact WHERE work_id=?)`, workID).Scan(ctx, &hasArtifacts); err != nil {
		return fmt.Errorf("retire source analysis work: check retained artifacts: %w", err)
	}
	if hasArtifacts {
		if _, err := tx.NewRaw(`UPDATE source_analysis_work SET current_location_id=NULL WHERE id=?`, workID).Exec(ctx); err != nil {
			return fmt.Errorf("retire source analysis work: detach retained work: %w", err)
		}
	} else if _, err := tx.NewRaw(`DELETE FROM source_analysis_work WHERE id=?`, workID).Exec(ctx); err != nil {
		return fmt.Errorf("retire source analysis work: delete work: %w", err)
	}
	for _, resultID := range resultIDs {
		if err := deleteUnreferencedSourceFingerprintResult(ctx, tx, resultID); err != nil {
			return err
		}
	}
	return nil
}
