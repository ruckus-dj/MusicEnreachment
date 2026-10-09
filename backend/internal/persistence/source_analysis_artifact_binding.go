package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

type sourceAnalysisArtifactBinding struct {
	WorkID                   uuid.UUID  `bun:"work_id,type:uuid"`
	ArtifactID               uuid.UUID  `bun:"artifact_id,type:uuid"`
	BorrowerOperationID      *uuid.UUID `bun:"borrower_operation_id,type:uuid,nullzero"`
	BorrowerOperationAttempt *int       `bun:"borrower_operation_attempt,nullzero"`
	BorrowerJobID            *int64     `bun:"borrower_job_id,nullzero"`
	RequestedSteps           []string   `bun:"requested_steps,array"`
	UpdatedAt                time.Time  `bun:"updated_at"`
}

// GetBinding returns the registered artifact for a work item, without making
// any filesystem or freshness claim about that artifact.
func (repository *SourceAnalysisArtifactRepository) GetBinding(ctx context.Context, workID uuid.UUID) (*SourceAnalysisArtifact, error) {
	if workID == uuid.Nil {
		return nil, fmt.Errorf("get source analysis artifact binding: work is required")
	}
	artifact := new(SourceAnalysisArtifact)
	err := repository.db.NewSelect().Model(artifact).
		ModelTableExpr("source_analysis_artifact AS artifact").
		ColumnExpr("artifact.*").
		Join("JOIN source_analysis_work_artifact_binding AS binding ON binding.artifact_id = artifact.id").
		Where("binding.work_id = ?", workID).Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get source analysis artifact binding: %w", err)
	}
	return artifact, nil
}

// ListRetained returns ready artifacts with canonical identity against the
// current inventory source. The caller must still verify the file itself.
func (repository *SourceAnalysisArtifactRepository) ListRetained(ctx context.Context, workID uuid.UUID) ([]*SourceAnalysisArtifact, error) {
	if workID == uuid.Nil {
		return nil, fmt.Errorf("list retained source analysis artifacts: work is required")
	}
	var artifacts []*SourceAnalysisArtifact
	err := repository.db.NewRaw(`SELECT artifact.*
		FROM source_analysis_work_artifact_binding AS binding
		JOIN source_analysis_artifact AS artifact ON artifact.id=binding.artifact_id AND artifact.work_id=binding.work_id
		JOIN source_analysis_work AS work ON work.id=binding.work_id
		JOIN source_root AS root ON root.id=work.source_root_id
		JOIN source_location AS location ON location.id=work.current_location_id AND location.source_root_id=work.source_root_id
		WHERE work.id=? AND work.current_location_id=work.location_id
		  AND root.enabled AND root.inventory_path IS NOT NULL AND root.inventory_path=root.configured_path
		  AND work.configured_path=root.configured_path AND work.inventory_path=root.inventory_path
		  AND location.relative_path=work.relative_path AND location.size_bytes=work.size_bytes AND location.mtime=work.mtime
		  AND artifact.state='ready'
		  AND artifact.relative_output_path='analysis/staging/' || lower(root.id::text) || '/' || lower(work.id::text) || '/' || lower(artifact.id::text)
		  AND artifact.source_size_bytes=work.size_bytes AND artifact.source_mtime=work.mtime
		ORDER BY artifact.created_at, artifact.id`, workID).Scan(ctx, &artifacts)
	if err != nil {
		return nil, fmt.Errorf("list retained source analysis artifacts: %w", err)
	}
	return artifacts, nil
}

// BindRetained assigns a ready retained artifact to the exact current staged
// delivery. A stale borrower can be replaced; a live owner is never stolen.
func (repository *SourceAnalysisArtifactRepository) BindRetained(ctx context.Context, artifactID uuid.UUID, fence SourceAnalysisArtifactFence) (*SourceAnalysisArtifact, error) {
	if artifactID == uuid.Nil || !validSourceAnalysisArtifactFence(fence) {
		return nil, fmt.Errorf("bind retained source analysis artifact: valid artifact and delivery fence are required")
	}
	var artifact *SourceAnalysisArtifact
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		work, root, location, operation, err := lockSourceAnalysisArtifactOwner(ctx, tx, fence)
		if err != nil {
			return fmt.Errorf("bind retained source analysis artifact: %w", err)
		}
		if !sourceAnalysisArtifactOwnerCurrent(work, root, location) || operation.Kind != analysisSourceOperationKind || operation.State != "running" || operation.Attempt != fence.OperationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != fence.JobID {
			return fmt.Errorf("bind retained source analysis artifact: delivery or source is stale")
		}
		if err := verifySourceAnalysisArtifactExecution(ctx, tx, fence); err != nil {
			return err
		}
		execution, err := getSourceAnalysisWorkExecution(ctx, tx, fence)
		if err != nil || execution.ProcessingMode != "staged" {
			return fmt.Errorf("bind retained source analysis artifact: current execution is not staged")
		}
		artifact = new(SourceAnalysisArtifact)
		if err := tx.NewRaw(`SELECT * FROM source_analysis_artifact WHERE id=? FOR UPDATE`, artifactID).Scan(ctx, artifact); err != nil {
			return fmt.Errorf("bind retained source analysis artifact: read artifact: %w", err)
		}
		if artifact.WorkID != work.ID || artifact.State != SourceAnalysisArtifactReady || artifact.RelativeOutputPath != sourceAnalysisArtifactPath(root.ID, work.ID, artifactID) ||
			artifact.SourceSizeBytes != work.SizeBytes || !sourceAnalysisMtime(artifact.SourceMtime).Equal(sourceAnalysisMtime(work.Mtime)) {
			return fmt.Errorf("bind retained source analysis artifact: artifact is not a ready canonical match")
		}
		binding := new(sourceAnalysisArtifactBinding)
		if err := tx.NewRaw(`SELECT * FROM source_analysis_work_artifact_binding WHERE work_id=? FOR UPDATE`, work.ID).Scan(ctx, binding); err != nil {
			return fmt.Errorf("bind retained source analysis artifact: read binding: %w", err)
		}
		if binding.ArtifactID != artifactID {
			return fmt.Errorf("bind retained source analysis artifact: artifact is not the current work binding")
		}
		sameBorrower := binding.BorrowerOperationID != nil && *binding.BorrowerOperationID == fence.OperationID && binding.BorrowerOperationAttempt != nil && *binding.BorrowerOperationAttempt == fence.OperationAttempt && binding.BorrowerJobID != nil && *binding.BorrowerJobID == fence.JobID
		if binding.BorrowerOperationID != nil && !sameBorrower {
			live, err := liveArtifactDelivery(ctx, tx, *binding.BorrowerOperationID, *binding.BorrowerOperationAttempt, *binding.BorrowerJobID)
			if err != nil {
				return err
			}
			if live {
				return fmt.Errorf("bind retained source analysis artifact: another live delivery borrows the artifact")
			}
		}
		liveCreator, err := liveArtifactDelivery(ctx, tx, artifact.OwnerOperationID, artifact.OwnerOperationAttempt, artifact.OwnerJobID)
		if err != nil {
			return err
		}
		if !sameBorrower && (liveCreator || artifact.State == SourceAnalysisArtifactAcquiring) {
			return fmt.Errorf("bind retained source analysis artifact: live creator owns the artifact")
		}
		steps, err := requestedStepsForOperation(ctx, tx, operation, work.ID)
		if err != nil {
			return err
		}
		binding.RequestedSteps = unionRequestedSteps(binding.RequestedSteps, steps)
		if _, err := tx.NewRaw(`UPDATE source_analysis_artifact SET requested_steps=?, requested_steps_known=true WHERE id=?`, pgdialect.Array(binding.RequestedSteps), artifactID).Exec(ctx); err != nil {
			return fmt.Errorf("bind retained source analysis artifact: update accumulated steps: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE source_analysis_work_artifact_binding SET borrower_operation_id=?, borrower_operation_attempt=?, borrower_job_id=?, requested_steps=?, updated_at=now() WHERE work_id=? AND artifact_id=?`,
			fence.OperationID, fence.OperationAttempt, fence.JobID, pgdialect.Array(binding.RequestedSteps), work.ID, artifactID).Exec(ctx); err != nil {
			return fmt.Errorf("bind retained source analysis artifact: update binding: %w", err)
		}
		artifact.RequestedSteps = binding.RequestedSteps
		artifact.RequestedStepsKnown = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return artifact, nil
}

// InvalidateBinding removes only the exact delivery's borrower reference. It
// intentionally leaves the artifact registry and file untouched.
func (repository *SourceAnalysisArtifactRepository) InvalidateBinding(ctx context.Context, artifactID uuid.UUID, fence SourceAnalysisArtifactFence) error {
	if artifactID == uuid.Nil || !validSourceAnalysisArtifactFence(fence) {
		return fmt.Errorf("invalidate source analysis artifact binding: valid artifact and delivery fence are required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		_, _, _, operation, err := lockSourceAnalysisArtifactOwner(ctx, tx, fence)
		if err != nil {
			return fmt.Errorf("invalidate source analysis artifact binding: %w", err)
		}
		if operation.Kind != analysisSourceOperationKind || operation.State != "running" || operation.Attempt != fence.OperationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != fence.JobID {
			return fmt.Errorf("invalidate source analysis artifact binding: delivery is not current and running")
		}
		var artifactIDInRow uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM source_analysis_artifact WHERE id=? AND work_id=? FOR UPDATE`, artifactID, fence.WorkID).Scan(ctx, &artifactIDInRow); err != nil {
			return fmt.Errorf("invalidate source analysis artifact binding: lock artifact: %w", err)
		}
		binding := new(sourceAnalysisArtifactBinding)
		if err := tx.NewRaw(`SELECT * FROM source_analysis_work_artifact_binding WHERE work_id=? FOR UPDATE`, fence.WorkID).Scan(ctx, binding); err != nil {
			return fmt.Errorf("invalidate source analysis artifact binding: read binding: %w", err)
		}
		if binding.ArtifactID != artifactID || binding.BorrowerOperationID == nil || *binding.BorrowerOperationID != fence.OperationID || binding.BorrowerOperationAttempt == nil || *binding.BorrowerOperationAttempt != fence.OperationAttempt || binding.BorrowerJobID == nil || *binding.BorrowerJobID != fence.JobID {
			return fmt.Errorf("invalidate source analysis artifact binding: borrower fence does not match")
		}
		if _, err := tx.NewRaw(`DELETE FROM source_analysis_work_artifact_binding WHERE work_id=? AND artifact_id=? AND borrower_operation_id=? AND borrower_operation_attempt=? AND borrower_job_id=?`,
			fence.WorkID, artifactID, fence.OperationID, fence.OperationAttempt, fence.JobID).Exec(ctx); err != nil {
			return fmt.Errorf("invalidate source analysis artifact binding: delete binding: %w", err)
		}
		return nil
	})
}

func bindAcquiredArtifact(ctx context.Context, tx bun.Tx, artifact *SourceAnalysisArtifact, operation *Operation, fence SourceAnalysisArtifactFence) error {
	steps, err := requestedStepsForOperation(ctx, tx, operation, fence.WorkID)
	if err != nil {
		return err
	}
	var previousArtifactID uuid.UUID
	previousErr := tx.NewRaw(`SELECT artifact_id FROM source_analysis_work_artifact_binding WHERE work_id=?`, fence.WorkID).Scan(ctx, &previousArtifactID)
	if previousErr != nil && previousErr != sql.ErrNoRows {
		return fmt.Errorf("read prior acquired artifact binding: %w", previousErr)
	}
	if previousErr == nil && previousArtifactID != artifact.ID {
		previous := new(SourceAnalysisArtifact)
		if err := tx.NewRaw(`SELECT * FROM source_analysis_artifact WHERE id=? AND work_id=? FOR UPDATE`, previousArtifactID, fence.WorkID).Scan(ctx, previous); err != nil {
			return fmt.Errorf("lock prior acquired artifact: %w", err)
		}
		if previous.State != SourceAnalysisArtifactAcquiring {
			return fmt.Errorf("acquire source analysis artifact: another artifact is bound to work")
		}
		liveCreator, err := liveArtifactDelivery(ctx, tx, previous.OwnerOperationID, previous.OwnerOperationAttempt, previous.OwnerJobID)
		if err != nil {
			return err
		}
		binding := new(sourceAnalysisArtifactBinding)
		if err := tx.NewRaw(`SELECT * FROM source_analysis_work_artifact_binding WHERE work_id=? FOR UPDATE`, fence.WorkID).Scan(ctx, binding); err != nil {
			return fmt.Errorf("lock prior acquired binding: %w", err)
		}
		if binding.ArtifactID != previousArtifactID || liveCreator {
			return fmt.Errorf("acquire source analysis artifact: prior creator is still live or binding changed")
		}
		if binding.BorrowerOperationID != nil {
			liveBorrower, err := liveArtifactDelivery(ctx, tx, *binding.BorrowerOperationID, *binding.BorrowerOperationAttempt, *binding.BorrowerJobID)
			if err != nil {
				return err
			}
			if liveBorrower {
				return fmt.Errorf("acquire source analysis artifact: prior borrower is still live")
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM source_analysis_work_artifact_binding WHERE work_id=? AND artifact_id=?`, fence.WorkID, previousArtifactID).Exec(ctx); err != nil {
			return fmt.Errorf("delete stale acquired artifact binding: %w", err)
		}
	}
	_, err = tx.NewRaw(`INSERT INTO source_analysis_work_artifact_binding
		(work_id, artifact_id, borrower_operation_id, borrower_operation_attempt, borrower_job_id, requested_steps)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (work_id) DO NOTHING`,
		fence.WorkID, artifact.ID, fence.OperationID, fence.OperationAttempt, fence.JobID, pgdialect.Array(steps)).Exec(ctx)
	if err != nil {
		return fmt.Errorf("bind acquired source analysis artifact: %w", err)
	}
	binding := new(sourceAnalysisArtifactBinding)
	if err := tx.NewRaw(`SELECT * FROM source_analysis_work_artifact_binding WHERE work_id=? FOR UPDATE`, fence.WorkID).Scan(ctx, binding); err != nil {
		return fmt.Errorf("read acquired artifact binding: %w", err)
	}
	if binding.ArtifactID != artifact.ID || binding.BorrowerOperationID == nil || *binding.BorrowerOperationID != fence.OperationID || binding.BorrowerOperationAttempt == nil || *binding.BorrowerOperationAttempt != fence.OperationAttempt || binding.BorrowerJobID == nil || *binding.BorrowerJobID != fence.JobID {
		return fmt.Errorf("acquire source analysis artifact: another artifact or delivery is bound to work")
	}
	binding.RequestedSteps = unionRequestedSteps(binding.RequestedSteps, steps)
	if _, err := tx.NewRaw(`UPDATE source_analysis_artifact SET requested_steps=?, requested_steps_known=true WHERE id=?`, pgdialect.Array(binding.RequestedSteps), artifact.ID).Exec(ctx); err != nil {
		return fmt.Errorf("update acquired artifact accumulated steps: %w", err)
	}
	if _, err := tx.NewRaw(`UPDATE source_analysis_work_artifact_binding SET requested_steps=?, updated_at=now() WHERE work_id=? AND artifact_id=?`, pgdialect.Array(binding.RequestedSteps), fence.WorkID, artifact.ID).Exec(ctx); err != nil {
		return fmt.Errorf("update acquired artifact obligations: %w", err)
	}
	artifact.RequestedSteps = binding.RequestedSteps
	artifact.RequestedStepsKnown = true
	return nil
}

func requestedStepsForOperation(ctx context.Context, tx bun.Tx, operation *Operation, workID uuid.UUID) ([]string, error) {
	var intent struct {
		TargetStep    *string `json:"target_step"`
		SelectedSteps []struct {
			WorkID uuid.UUID `json:"work_id"`
			Step   string    `json:"step"`
		} `json:"selected_steps"`
	}
	if err := json.Unmarshal(operation.InputSnapshot, &intent); err != nil {
		return nil, fmt.Errorf("decode requested source analysis steps: %w", err)
	}
	steps := make([]string, 0, 3)
	if intent.TargetStep != nil {
		steps = append(steps, *intent.TargetStep)
	}
	for _, selected := range intent.SelectedSteps {
		if selected.WorkID == workID {
			steps = append(steps, selected.Step)
		}
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("source analysis operation has no explicit requested steps for work")
	}
	return unionRequestedSteps(nil, steps), nil
}

func unionRequestedSteps(left, right []string) []string {
	seen := make(map[string]bool, len(left)+len(right))
	for _, step := range left {
		seen[step] = true
	}
	for _, step := range right {
		seen[step] = true
	}
	result := make([]string, 0, len(seen))
	for _, step := range []string{"sha256", "probe", "fingerprint", "metadata"} {
		if seen[step] {
			result = append(result, step)
		}
	}
	return result
}

func requireCurrentArtifactBinding(ctx context.Context, tx bun.Tx, artifactID uuid.UUID, fence SourceAnalysisArtifactFence) error {
	binding := new(sourceAnalysisArtifactBinding)
	if err := tx.NewRaw(`SELECT * FROM source_analysis_work_artifact_binding WHERE work_id=? FOR UPDATE`, fence.WorkID).Scan(ctx, binding); err != nil {
		return fmt.Errorf("read current artifact binding: %w", err)
	}
	if binding.ArtifactID != artifactID || binding.BorrowerOperationID == nil || *binding.BorrowerOperationID != fence.OperationID || binding.BorrowerOperationAttempt == nil || *binding.BorrowerOperationAttempt != fence.OperationAttempt || binding.BorrowerJobID == nil || *binding.BorrowerJobID != fence.JobID {
		return fmt.Errorf("artifact is not bound to this delivery")
	}
	return nil
}

func liveArtifactDelivery(ctx context.Context, tx bun.Tx, operationID uuid.UUID, attempt int, jobID int64) (bool, error) {
	var live bool
	if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM operation WHERE id=? AND attempt=? AND river_job_id=? AND state IN ('queued','running'))`, operationID, attempt, jobID).Scan(ctx, &live); err != nil {
		return false, fmt.Errorf("check source analysis delivery liveness: %w", err)
	}
	return live, nil
}

// releaseSourceAnalysisArtifactBindings releases only bindings borrowed by the
// exact settled delivery, retaining both the artifact and accumulated intent.
func releaseSourceAnalysisArtifactBindings(ctx context.Context, tx bun.Tx, operationID uuid.UUID, delivery SourceAnalysisOperationDelivery) error {
	if operationID == uuid.Nil || delivery.Attempt < 1 || delivery.JobID < 1 {
		return fmt.Errorf("release source analysis artifact bindings: valid operation delivery is required")
	}
	if _, err := tx.NewRaw(`UPDATE source_analysis_work_artifact_binding SET borrower_operation_id=NULL, borrower_operation_attempt=NULL, borrower_job_id=NULL, updated_at=now()
		WHERE borrower_operation_id=? AND borrower_operation_attempt=? AND borrower_job_id=?`, operationID, delivery.Attempt, delivery.JobID).Exec(ctx); err != nil {
		return fmt.Errorf("release source analysis artifact bindings: %w", err)
	}
	return nil
}

// makeCompletedSourceAnalysisArtifactsCleanupEligible marks ready staged copies
// for cleanup only after every accumulated requested step has a current result.
// The binding must already have been released by its exact borrower.
func makeCompletedSourceAnalysisArtifactsCleanupEligible(ctx context.Context, tx bun.Tx, workIDs []uuid.UUID) error {
	if len(workIDs) == 0 {
		return nil
	}
	if _, err := tx.NewRaw(`WITH eligible_bindings AS MATERIALIZED (
		SELECT binding.work_id, binding.artifact_id
		FROM source_analysis_work_artifact_binding binding
		JOIN source_analysis_artifact artifact ON artifact.id=binding.artifact_id AND artifact.state='ready'
		WHERE binding.work_id IN (?) AND binding.borrower_operation_id IS NULL
		  AND cardinality(binding.requested_steps) > 0
		  AND NOT EXISTS (
			SELECT 1 FROM unnest(binding.requested_steps) requested(step)
			LEFT JOIN source_analysis_step analysis_step ON analysis_step.work_id=binding.work_id AND analysis_step.step=requested.step
			WHERE analysis_step.work_id IS NULL OR analysis_step.state <> 'succeeded'
			   OR (requested.step='sha256' AND NOT EXISTS (
				SELECT 1 FROM media_variant result WHERE result.id=analysis_step.success_sha_variant_id
				  AND octet_length(result.source_sha256)=32 AND result.sha256_calculated_at IS NOT NULL
			   ))
			   OR (requested.step='probe' AND NOT EXISTS (
				SELECT 1 FROM media_variant result WHERE result.id=analysis_step.success_probe_variant_id
				  AND result.ffprobe_json IS NOT NULL AND result.audio_stream_count IS NOT NULL
			   ))
			   OR (requested.step='fingerprint' AND NOT EXISTS (
				SELECT 1 FROM media_fingerprint_result result WHERE result.id=analysis_step.success_fingerprint_result_id
				  AND result.fingerprint <> '' AND result.fpcalc_version <> ''
			   ))
			   OR (requested.step='metadata' AND NOT EXISTS (
				SELECT 1 FROM media_metadata_result result WHERE result.id=analysis_step.success_metadata_result_id
				  AND jsonb_typeof(result.observed_tags)='object' AND jsonb_typeof(result.provenance)='object'
			   ))
		  )
	), marked_artifacts AS (
		UPDATE source_analysis_artifact artifact SET state='cleanup_eligible', updated_at=now()
		FROM eligible_bindings eligible
		WHERE artifact.id=eligible.artifact_id AND artifact.state='ready'
		RETURNING artifact.id
	)
	DELETE FROM source_analysis_work_artifact_binding binding
	USING eligible_bindings eligible, marked_artifacts marked
	WHERE binding.work_id=eligible.work_id AND binding.artifact_id=eligible.artifact_id AND marked.id=eligible.artifact_id
	  AND binding.borrower_operation_id IS NULL`, bun.List(workIDs)).Exec(ctx); err != nil {
		return fmt.Errorf("make completed source analysis artifacts cleanup eligible: %w", err)
	}
	return nil
}
