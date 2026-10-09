package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// SourceLocationDetailSnapshot contains the rows that make up one consistent
// inspector read. Optional rows are nil when the location has no result or
// active analysis.
type SourceLocationDetailSnapshot struct {
	Root                     *SourceRoot
	Location                 *SourceLocation
	Work                     *SourceAnalysisWork
	Steps                    []SourceAnalysisStep
	SHAVariant               *SourceMediaVariant
	Fingerprint              *SourceFingerprintResult
	Metadata                 *SourceMetadataResult
	MatchingEligible         bool
	Variant                  *MediaVariant
	ActiveOperationID        *uuid.UUID
	ActiveFPCalcInstallation *ToolInstallation
	StagedArtifact           *SourceLocationStagedArtifact
}

// SourceLocationStagedArtifact is a bounded projection of the single artifact
// registered for the location's current analysis work. Paths remain relative to
// the managed output root; no filesystem access is needed to read this record.
type SourceLocationStagedArtifact struct {
	ID                  uuid.UUID  `bun:"id,type:uuid"`
	State               string     `bun:"state"`
	RequestedSteps      []string   `bun:"requested_steps,array"`
	RequestedStepsKnown bool       `bun:"requested_steps_known"`
	CreatorOperationID  *uuid.UUID `bun:"creator_operation_id,type:uuid,nullzero"`
	BorrowerOperationID *uuid.UUID `bun:"borrower_operation_id,type:uuid,nullzero"`
	SafeError           *string    `bun:"safe_error,nullzero"`
	RelativeOutputPath  string     `bun:"relative_output_path"`
}

// ReadSourceLocationDetail reads the root, owned location, saved result and
// active analysis from one read-only repeatable-read snapshot. It takes no row or
// table locks, so unlinking or deleting an orphan variant cannot block the read.
func (repository *SourceInventoryRepository) ReadSourceLocationDetail(ctx context.Context, rootID, locationID uuid.UUID) (*SourceLocationDetailSnapshot, error) {
	var snapshot *SourceLocationDetailSnapshot
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		root := new(SourceRoot)
		if err := tx.NewSelect().Model(root).Where("id = ?", rootID).Scan(ctx); err != nil {
			return fmt.Errorf("read source location detail root: %w", err)
		}
		location := new(SourceLocation)
		if err := tx.NewSelect().Model(location).Where("id = ?", locationID).
			Where("source_root_id = ?", rootID).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("read source location detail location: %w", ErrSourceLocationNotFound)
			}
			return fmt.Errorf("read source location detail location: %w", err)
		}
		var selectedVariantID *uuid.UUID
		var work *SourceAnalysisWork
		var steps []SourceAnalysisStep
		var shaVariant *SourceMediaVariant
		var fingerprint *SourceFingerprintResult
		var metadataResult *SourceMetadataResult
		matchingEligible := false
		candidate := new(SourceAnalysisWork)
		if err := tx.NewSelect().Model(candidate).
			Where("source_root_id = ?", rootID).Where("location_id = ?", locationID).
			Where("configured_path = ?", root.ConfiguredPath).
			Where("inventory_path = ?", root.InventoryPath).
			Where("relative_path = ?", location.RelativePath).Where("size_bytes = ?", location.SizeBytes).
			Where("mtime = ?", location.Mtime).Order("created_at DESC").Limit(1).Scan(ctx); err == nil {
			work = candidate
			steps = make([]SourceAnalysisStep, 0, 4)
			if err := tx.NewSelect().Model(&steps).Where("work_id = ?", work.ID).
				OrderExpr("CASE step WHEN 'sha256' THEN 1 WHEN 'probe' THEN 2 WHEN 'fingerprint' THEN 3 WHEN 'metadata' THEN 4 ELSE 5 END ASC").Scan(ctx); err != nil {
				return fmt.Errorf("read source location detail steps: %w", err)
			}
			stepByName := make(map[string]SourceAnalysisStep, len(steps))
			for _, step := range steps {
				stepByName[step.Step] = step
			}
			if step, ok := stepByName[string(SourceStepProbe)]; ok && step.SuccessProbeVariantID != nil {
				selectedVariantID = step.SuccessProbeVariantID
			}
			if step, ok := stepByName[string(SourceStepSHA256)]; ok && step.SuccessSHAVariantID != nil {
				shaVariant = new(SourceMediaVariant)
				if err := tx.NewSelect().Model(shaVariant).Where("id = ?", *step.SuccessSHAVariantID).Scan(ctx); err != nil {
					return fmt.Errorf("read source location detail selected SHA variant: %w", err)
				}
			}
			if step, ok := stepByName[string(SourceStepFingerprint)]; ok && step.SuccessFingerprintResultID != nil {
				fingerprint = new(SourceFingerprintResult)
				if err := tx.NewSelect().Model(fingerprint).Where("id = ?", *step.SuccessFingerprintResultID).Scan(ctx); err != nil {
					return fmt.Errorf("read source location detail selected fingerprint: %w", err)
				}
			}
			if step, ok := stepByName[string(SourceStepMetadata)]; ok && step.SuccessMetadataResultID != nil {
				metadataResult = new(SourceMetadataResult)
				if err := tx.NewSelect().Model(metadataResult).Where("id = ?", *step.SuccessMetadataResultID).Scan(ctx); err != nil {
					return fmt.Errorf("read source location detail selected metadata result: %w", err)
				}
			}
			if selectedVariantID != nil && fingerprint != nil {
				probeVariant := new(SourceMediaVariant)
				if err := tx.NewSelect().Model(probeVariant).Column("audio_stream_count").Where("id = ?", *selectedVariantID).Scan(ctx); err != nil {
					return fmt.Errorf("read source location detail probe stream count: %w", err)
				}
				matchingEligible = probeVariant.AudioStreamCount != nil && *probeVariant.AudioStreamCount == 1
			}
		} else if err != sql.ErrNoRows {
			return fmt.Errorf("read source location detail current work: %w", err)
		}
		// The location link is the current result only when there is no matching
		// analysis work. An existing work item without a successful probe must
		// not inherit a canonical result from another selection.
		if selectedVariantID == nil && work == nil {
			selectedVariantID = location.MediaVariantID
		}
		var variant *MediaVariant
		if selectedVariantID != nil {
			variant = new(MediaVariant)
			if err := tx.NewRaw(`SELECT id, size_bytes, analysis_policy_version, ffprobe_version, ffprobe_json,
					observed_tags, inspected_at, applied_operation_id, created_at
					FROM media_variant WHERE id = ? AND ffprobe_version IS NOT NULL /* source_location_detail_variant */`, *selectedVariantID).Scan(ctx, variant); err != nil {
				if err == sql.ErrNoRows {
					variant = nil
				} else {
					return fmt.Errorf("read source location detail variant: %w", err)
				}
			}
		}
		var activeOperation struct {
			ID            uuid.UUID       `bun:"id,type:uuid"`
			InputSnapshot json.RawMessage `bun:"input_snapshot"`
		}
		err := tx.NewRaw(
			`SELECT o.id, o.input_snapshot FROM operation o /* source_location_detail_active */
				 WHERE kind = ? AND state IN ('queued', 'running')
				   AND target_source_root_id = ? AND (
				     target_source_location_id = ? OR
				     (target_source_location_id IS NULL AND EXISTS (
				       SELECT 1 FROM operation_source_work_hold h WHERE h.operation_id=o.id AND h.work_id=?
				     ))
				   )
				 ORDER BY o.created_at DESC
				 LIMIT 1`,
			analysisSourceOperationKind, rootID, locationID, workID(work),
		).Scan(ctx, &activeOperation)
		var active *uuid.UUID
		if err == nil {
			active = &activeOperation.ID
		} else if err != sql.ErrNoRows {
			return fmt.Errorf("read source location detail active analysis: %w", err)
		}
		var activeFPCalcInstallation *ToolInstallation
		activeFPCalc := new(ToolInstallation)
		if err := tx.NewRaw(`SELECT installation.*
				FROM tool_installation AS installation
				JOIN app_setting AS setting
				  ON setting.setting_name = 'active_fpcalc_installation_id'
				 AND setting.setting_value = installation.id::text
				WHERE installation.package_kind = 'fpcalc'
				  AND installation.state = 'ready'
				LIMIT 1`).Scan(ctx, activeFPCalc); err == nil {
			activeFPCalcInstallation = activeFPCalc
		} else if err != sql.ErrNoRows {
			return fmt.Errorf("read source location detail active fpcalc installation: %w", err)
		}
		stagedArtifact, err := readSourceLocationStagedArtifact(ctx, tx, work, active, activeOperation.InputSnapshot)
		if err != nil {
			return err
		}
		snapshot = &SourceLocationDetailSnapshot{
			Root: root, Location: location, Work: work, Steps: steps, SHAVariant: shaVariant,
			Fingerprint: fingerprint, Metadata: metadataResult, MatchingEligible: matchingEligible,
			Variant: variant, ActiveOperationID: active, ActiveFPCalcInstallation: activeFPCalcInstallation,
			StagedArtifact: stagedArtifact,
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read source location detail: %w", err)
	}
	return snapshot, nil
}

func readSourceLocationStagedArtifact(ctx context.Context, tx bun.Tx, work *SourceAnalysisWork, activeOperationID *uuid.UUID, activeInputSnapshot json.RawMessage) (*SourceLocationStagedArtifact, error) {
	if work == nil {
		return nil, nil
	}
	artifact := new(SourceLocationStagedArtifact)
	err := tx.NewRaw(`SELECT artifact.id, artifact.state, artifact.requested_steps, artifact.requested_steps_known,
		artifact.owner_operation_id AS creator_operation_id,
		binding.borrower_operation_id, artifact.cleanup_error AS safe_error,
		artifact.relative_output_path
		FROM source_analysis_artifact artifact
		LEFT JOIN source_analysis_work_artifact_binding binding
		  ON binding.work_id=artifact.work_id AND binding.artifact_id=artifact.id
		WHERE artifact.work_id=?
		ORDER BY (binding.artifact_id IS NOT NULL) DESC, artifact.created_at DESC, artifact.id DESC LIMIT 1`, work.ID).Scan(ctx, artifact)
	if err == nil {
		if artifact.State == SourceAnalysisArtifactReady && artifact.BorrowerOperationID == nil {
			artifact.State = "retained"
		}
		return artifact, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("read source location detail staged artifact: %w", err)
	}
	if activeOperationID == nil {
		return nil, nil
	}
	var staged bool
	if err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM source_analysis_work_execution execution
		JOIN operation operation ON operation.id=execution.operation_id
		WHERE execution.work_id=? AND execution.operation_id=?
		  AND execution.operation_attempt=operation.attempt
		  AND execution.job_id=operation.river_job_id
		  AND execution.processing_mode='staged'
		  AND operation.state IN ('queued','running')
	)`, work.ID, *activeOperationID).Scan(ctx, &staged); err != nil {
		return nil, fmt.Errorf("read source location detail staged preparation: %w", err)
	}
	if !staged {
		return nil, nil
	}
	requestedSteps, requestedStepsKnown := stagedRequestedSteps(activeInputSnapshot, work.ID)
	return &SourceLocationStagedArtifact{
		State: "preparation", RequestedSteps: requestedSteps, RequestedStepsKnown: requestedStepsKnown,
	}, nil
}

func stagedRequestedSteps(inputSnapshot json.RawMessage, workID uuid.UUID) ([]string, bool) {
	var intent struct {
		TargetWorkID  *uuid.UUID `json:"target_work_id"`
		TargetStep    *string    `json:"target_step"`
		SelectedSteps []struct {
			WorkID uuid.UUID `json:"work_id"`
			Step   string    `json:"step"`
		} `json:"selected_steps"`
	}
	if len(inputSnapshot) == 0 || json.Unmarshal(inputSnapshot, &intent) != nil {
		return []string{}, false
	}
	steps := make([]string, 0, len(intent.SelectedSteps)+1)
	if intent.TargetWorkID != nil && *intent.TargetWorkID == workID && intent.TargetStep != nil {
		steps = append(steps, *intent.TargetStep)
	}
	for _, selected := range intent.SelectedSteps {
		if selected.WorkID == workID {
			steps = append(steps, selected.Step)
		}
	}
	if len(steps) == 0 {
		return []string{}, false
	}
	for _, step := range steps {
		if !validSourceStep(SourceStepName(step)) {
			return []string{}, false
		}
	}
	return unionRequestedSteps(nil, steps), true
}

func workID(work *SourceAnalysisWork) uuid.UUID {
	if work == nil {
		return uuid.Nil
	}
	return work.ID
}
