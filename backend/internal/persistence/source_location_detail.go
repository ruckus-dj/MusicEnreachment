package persistence

import (
	"context"
	"database/sql"
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
	MatchingEligible         bool
	Variant                  *MediaVariant
	ActiveOperationID        *uuid.UUID
	ActiveFPCalcInstallation *ToolInstallation
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
		matchingEligible := false
		candidate := new(SourceAnalysisWork)
		if err := tx.NewSelect().Model(candidate).
			Where("source_root_id = ?", rootID).Where("location_id = ?", locationID).
			Where("configured_path = ?", root.ConfiguredPath).
			Where("inventory_path = ?", root.InventoryPath).
			Where("relative_path = ?", location.RelativePath).Where("size_bytes = ?", location.SizeBytes).
			Where("mtime = ?", location.Mtime).Order("created_at DESC").Limit(1).Scan(ctx); err == nil {
			work = candidate
			steps = make([]SourceAnalysisStep, 0, 3)
			if err := tx.NewSelect().Model(&steps).Where("work_id = ?", work.ID).
				OrderExpr("CASE step WHEN 'sha256' THEN 1 WHEN 'probe' THEN 2 WHEN 'fingerprint' THEN 3 ELSE 4 END ASC").Scan(ctx); err != nil {
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
		var activeID uuid.UUID
		err := tx.NewRaw(
			`SELECT o.id FROM operation o /* source_location_detail_active */
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
		).Scan(ctx, &activeID)
		var active *uuid.UUID
		if err == nil {
			active = &activeID
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
		snapshot = &SourceLocationDetailSnapshot{
			Root: root, Location: location, Work: work, Steps: steps, SHAVariant: shaVariant,
			Fingerprint: fingerprint, MatchingEligible: matchingEligible,
			Variant: variant, ActiveOperationID: active, ActiveFPCalcInstallation: activeFPCalcInstallation,
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read source location detail: %w", err)
	}
	return snapshot, nil
}

func workID(work *SourceAnalysisWork) uuid.UUID {
	if work == nil {
		return uuid.Nil
	}
	return work.ID
}
