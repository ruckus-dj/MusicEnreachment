package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ReusePendingSourceAnalysisCache applies an existing digest-keyed result to one
// still-pending analysis step. The stored result and its provenance are immutable;
// this only changes the step's selected result pointer.
func (repository *SourceInventoryRepository) ReusePendingSourceAnalysisCache(
	ctx context.Context,
	rootID uuid.UUID,
	workID uuid.UUID,
	step SourceStepName,
	variantOrFingerprintID uuid.UUID,
	expectedVersion string,
) (bool, error) {
	if rootID == uuid.Nil || workID == uuid.Nil || variantOrFingerprintID == uuid.Nil || expectedVersion == "" ||
		(step != SourceStepProbe && step != SourceStepFingerprint) {
		return false, fmt.Errorf("reuse pending source analysis cache: valid root, work, cache result, step and version are required")
	}
	reused := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		// Resolve the location before taking locks, then acquire locks in the same
		// root -> location -> work order used by source-analysis admission.
		var locationID uuid.UUID
		if err := tx.NewRaw(`SELECT location_id FROM source_analysis_work WHERE id=? AND source_root_id=?`, workID, rootID).Scan(ctx, &locationID); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("reuse pending source analysis cache: read work location: %w", err)
		}
		root := new(SourceRoot)
		if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, rootID).Scan(ctx, root); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("reuse pending source analysis cache: lock root: %w", err)
		}
		location := new(SourceLocation)
		if err := tx.NewRaw(`SELECT * FROM source_location WHERE id=? AND source_root_id=? FOR UPDATE`, locationID, rootID).Scan(ctx, location); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("reuse pending source analysis cache: lock location: %w", err)
		}
		work := new(SourceAnalysisWork)
		if err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE id=? AND source_root_id=? AND location_id=? FOR UPDATE`, workID, rootID, locationID).Scan(ctx, work); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("reuse pending source analysis cache: lock work: %w", err)
		}
		if !root.Enabled || root.Stale() || root.InventoryPath == nil || root.ConfiguredPath != work.ConfiguredPath ||
			*root.InventoryPath != work.InventoryPath || location.RelativePath != work.RelativePath ||
			location.SizeBytes != work.SizeBytes || !sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(work.Mtime)) {
			return nil
		}
		var active bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation WHERE target_source_root_id=? AND state IN ('queued','running') AND kind IN ('scan_source','analyze_source'))`, rootID).Scan(ctx, &active); err != nil {
			return fmt.Errorf("reuse pending source analysis cache: check active root operation: %w", err)
		}
		if active {
			return nil
		}
		stepRow := new(SourceAnalysisStep)
		if err := tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step=? FOR UPDATE`, workID, step).Scan(ctx, stepRow); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("reuse pending source analysis cache: lock step: %w", err)
		}
		if stepRow.State != "pending" {
			return nil
		}
		if len(stepRow.InputSnapshot) > 0 && string(stepRow.InputSnapshot) != "null" {
			input, err := DecodeRetainedSourceAnalysisStepInput(*stepRow, *work)
			if err != nil {
				return fmt.Errorf("reuse pending source analysis cache: validate retained input: %w", err)
			}
			if *input.RerunTarget {
				return nil
			}
			version := input.CacheOnlyFPCalcVersion
			if step == SourceStepProbe {
				version = input.CacheOnlyFFProbeVersion
			}
			for _, tool := range input.Tools {
				if step == SourceStepProbe && tool.Executable == "ffprobe" {
					version = tool.VersionBanner
				}
				if step == SourceStepFingerprint && tool.Executable == "fpcalc" {
					version = tool.Version
				}
			}
			if version != expectedVersion {
				return nil
			}
		}

		// The SHA step selection is the actual digest for this immutable work. Do
		// not infer or backfill one from a cache hit.
		var shaVariantID uuid.UUID
		if err := tx.NewRaw(`SELECT success_sha_variant_id FROM source_analysis_step WHERE work_id=? AND step='sha256' AND success_sha_variant_id IS NOT NULL`, workID).Scan(ctx, &shaVariantID); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("reuse pending source analysis cache: read successful SHA selection: %w", err)
		}
		var digest []byte
		if err := tx.NewRaw(`SELECT source_sha256 FROM media_variant WHERE id=?`, shaVariantID).Scan(ctx, &digest); err != nil {
			return fmt.Errorf("reuse pending source analysis cache: read successful SHA digest: %w", err)
		}
		if len(digest) != 32 {
			return nil
		}

		var update *bun.UpdateQuery
		switch step {
		case SourceStepProbe:
			cached := new(SourceMediaVariant)
			if err := tx.NewSelect().Model(cached).Where("id=?", variantOrFingerprintID).Scan(ctx); err != nil {
				if err == sql.ErrNoRows {
					return nil
				}
				return fmt.Errorf("reuse pending source analysis cache: read probe cache result: %w", err)
			}
			if cached.FFProbeVersion == nil || *cached.FFProbeVersion != expectedVersion || cached.AnalysisPolicyVersion == nil ||
				*cached.AnalysisPolicyVersion != SourceAnalysisPolicyVersion || cached.AppliedOperationID == nil ||
				len(cached.FFProbeJSON) == 0 || cached.InspectedAt == nil || cached.ObservedTags == nil || cached.AudioStreamCount == nil {
				return nil
			}
			var associated bool
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_probe_cache WHERE source_sha256=? AND ffprobe_version_sha256=sha256(convert_to(?, 'UTF8')) AND ffprobe_version=? AND analysis_policy_version=? AND result_id=?)`, digest, expectedVersion, expectedVersion, SourceAnalysisPolicyVersion, variantOrFingerprintID).Scan(ctx, &associated); err != nil {
				return fmt.Errorf("reuse pending source analysis cache: verify probe cache association: %w", err)
			}
			if !associated || cached.SizeBytes != work.SizeBytes {
				return nil
			}
			if cached.AudioStreamCount == nil {
				return nil
			}
			update = tx.NewUpdate().Table("source_analysis_step").Set("state='succeeded'").
				Set("success_probe_variant_id=?", variantOrFingerprintID).Set("success_reuse_origin='sha256'")
		case SourceStepFingerprint:
			cached := new(SourceFingerprintResult)
			if err := tx.NewSelect().Model(cached).Where("id=?", variantOrFingerprintID).Scan(ctx); err != nil {
				if err == sql.ErrNoRows {
					return nil
				}
				return fmt.Errorf("reuse pending source analysis cache: read fingerprint cache result: %w", err)
			}
			if cached.FPCalcVersion != expectedVersion || cached.VersionBanner == "" || cached.AlgorithmNamespace == "" ||
				cached.Fingerprint == "" || cached.AppliedOperationID == uuid.Nil {
				return nil
			}
			var associated bool
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_fingerprint_cache WHERE source_sha256=? AND fpcalc_version=? AND result_id=?)`, digest, expectedVersion, variantOrFingerprintID).Scan(ctx, &associated); err != nil {
				return fmt.Errorf("reuse pending source analysis cache: verify fingerprint cache association: %w", err)
			}
			if !associated {
				return nil
			}
			update = tx.NewUpdate().Table("source_analysis_step").Set("state='succeeded'").
				Set("success_fingerprint_result_id=?", variantOrFingerprintID).Set("success_reuse_origin='sha256'")
		}
		result, err := update.Set("safe_error=NULL").Set("skip_reason=NULL").Set("execution_operation_id=NULL").
			Set("execution_operation_attempt=NULL").Set("execution_job_id=NULL").Set("updated_at=now()").
			Where("work_id=? AND step=? AND state='pending'", workID, step).Exec(ctx)
		if err != nil {
			return fmt.Errorf("reuse pending source analysis cache: select cached result: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("reuse pending source analysis cache: count selected result: %w", err)
		}
		reused = count == 1
		if reused && step == SourceStepProbe {
			var audioStreamCount int
			if err := tx.NewRaw(`SELECT audio_stream_count FROM media_variant WHERE id=?`, variantOrFingerprintID).Scan(ctx, &audioStreamCount); err != nil {
				return fmt.Errorf("reuse pending source analysis cache: read cached probe status: %w", err)
			}
			if err := updateSourceLocationProbeStatus(ctx, tx, location.ID, audioStreamCount); err != nil {
				return fmt.Errorf("reuse pending source analysis cache: persist location probe status: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return reused, nil
}
