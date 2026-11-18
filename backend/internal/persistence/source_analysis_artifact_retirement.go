package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

// RetireSourceAnalysisWorkArtifact is called by the exact borrower after all
// readers for this file have closed. A failed removal is recorded separately
// from the analysis result.
func (repository *SourceInventoryRepository) RetireSourceAnalysisWorkArtifact(
	ctx context.Context,
	workID uuid.UUID,
	delivery SourceAnalysisOperationDelivery,
	operationID uuid.UUID,
	remove func(workDirectory string, artifact SourceAnalysisArtifact) error,
) error {
	fence := SourceAnalysisArtifactFence{
		WorkID: workID, OperationID: operationID,
		OperationAttempt: delivery.Attempt, JobID: delivery.JobID,
	}
	if !validSourceAnalysisArtifactFence(fence) || remove == nil {
		return errors.New("retire staged source analysis artifact: invalid delivery or remover")
	}
	var removalErr error
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := advisoryXactLock(ctx, tx, true, toolsCoordinationNamespace, workDirectoryCoordinationLock); err != nil {
			return fmt.Errorf("lock staged work directory for retirement: %w", err)
		}
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return err
		}
		// A work can become invalidated during analysis; it still owns its
		// registered temporary input. Do not require a current source location.
		var rootID uuid.UUID
		if err := tx.NewRaw(`SELECT source_root_id FROM source_analysis_work WHERE id=? FOR UPDATE`, workID).Scan(ctx, &rootID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("lock source analysis work for retirement: %w", err)
		}
		var liveID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM operation WHERE id=? AND kind='analyze_source'
			AND state='running' AND attempt=? AND river_job_id=? FOR UPDATE`,
			operationID, delivery.Attempt, delivery.JobID).Scan(ctx, &liveID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("lock source analysis delivery for retirement: %w", err)
		}
		var artifact SourceAnalysisArtifact
		if err := tx.NewRaw(`SELECT artifact.* FROM source_analysis_work_artifact_binding binding
			JOIN source_analysis_artifact artifact ON artifact.id=binding.artifact_id AND artifact.work_id=binding.work_id
			WHERE binding.work_id=? AND binding.borrower_operation_id=? AND binding.borrower_operation_attempt=?
			AND binding.borrower_job_id=? AND artifact.state='ready' FOR UPDATE OF binding,artifact`,
			workID, operationID, delivery.Attempt, delivery.JobID).Scan(ctx, &artifact); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("lock staged artifact for retirement: %w", err)
		}
		if artifact.RelativeOutputPath != sourceAnalysisArtifactPath(rootID, workID, artifact.ID) {
			return errors.New("staged source analysis artifact has a noncanonical path")
		}
		var workDirectory string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name=?`, settings.WorkDirectoryKey).Scan(ctx, &workDirectory); err != nil {
			return fmt.Errorf("read staged work directory for retirement: %w", err)
		}
		if _, err := tx.NewRaw(`DELETE FROM source_analysis_work_artifact_binding WHERE work_id=? AND artifact_id=?`, workID, artifact.ID).Exec(ctx); err != nil {
			return fmt.Errorf("release staged artifact binding: %w", err)
		}
		removalErr = remove(workDirectory, artifact)
		if removalErr != nil {
			_, err := tx.NewRaw(`UPDATE source_analysis_artifact
				SET state='cleanup_failed', cleanup_error=?, cleanup_at=now(), updated_at=now()
				WHERE id=?`, "The staged analysis artifact could not be cleaned up.", artifact.ID).Exec(ctx)
			return err
		}
		// If a crash prevents this commit, recovery still owns the registered
		// leaf. Reconciliation treats an already absent leaf as cleaned.
		_, err := tx.NewRaw(`DELETE FROM source_analysis_artifact WHERE id=?`, artifact.ID).Exec(ctx)
		return err
	})
	return errors.Join(err, removalErr)
}

// RetireIdleSourceAnalysisArtifacts detaches temporary inputs left behind by
// settled or recovered work. The cleanup worker performs the actual unlink.
func (repository *SetupManagerRepository) RetireIdleSourceAnalysisArtifacts(ctx context.Context, limit int) error {
	if limit < 1 || limit > 500 {
		return errors.New("retire idle staged artifacts: invalid limit")
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return err
		}
		var workIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT work.id FROM source_analysis_work work
			JOIN source_analysis_work_artifact_binding binding ON binding.work_id=work.id
			WHERE binding.borrower_operation_id IS NULL
			AND NOT EXISTS (SELECT 1 FROM operation_source_work_hold hold JOIN operation owner ON owner.id=hold.operation_id
				WHERE hold.work_id=work.id AND owner.state IN ('queued','running'))
			ORDER BY work.id LIMIT ? FOR UPDATE OF work SKIP LOCKED`, limit).Scan(ctx, &workIDs); err != nil {
			return fmt.Errorf("lock idle staged analysis work: %w", err)
		}
		if len(workIDs) == 0 {
			return nil
		}
		if _, err := tx.NewRaw(`WITH retired AS (
			UPDATE source_analysis_artifact artifact SET state='cleanup_eligible', updated_at=now()
			FROM source_analysis_work_artifact_binding binding
			WHERE binding.work_id IN (?) AND binding.borrower_operation_id IS NULL
			AND artifact.id=binding.artifact_id AND artifact.state='ready'
			AND NOT EXISTS (SELECT 1 FROM operation_source_work_hold hold JOIN operation owner ON owner.id=hold.operation_id
				WHERE hold.work_id=binding.work_id AND owner.state IN ('queued','running'))
			RETURNING artifact.id
		) DELETE FROM source_analysis_work_artifact_binding binding USING retired
			WHERE binding.artifact_id=retired.id AND binding.borrower_operation_id IS NULL`, bun.List(workIDs)).Exec(ctx); err != nil {
			return fmt.Errorf("retire idle staged analysis work: %w", err)
		}
		return nil
	})
}
