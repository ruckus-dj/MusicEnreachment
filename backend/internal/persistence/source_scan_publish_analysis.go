package persistence

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// publishPreparedSourceScanAnalysis publishes scan-prepared immutable results
// in the caller's inventory transaction. The SHA policy is the value sampled
// into the scan snapshot; it is deliberately not re-read from runtime settings.
func publishPreparedSourceScanAnalysis(ctx context.Context, tx bun.Tx, root SourceRoot, scanOperation Operation, sha256Enabled bool, candidates []SourceScanCandidateInput) error {
	if scanOperation.ID == uuid.Nil || scanOperation.Kind != "scan_source" || scanOperation.TargetSourceRootID == nil || *scanOperation.TargetSourceRootID != root.ID {
		return fmt.Errorf("publish prepared source scan analysis: scan operation does not target the locked root")
	}
	var persisted Operation
	if err := tx.NewRaw(`SELECT * FROM operation WHERE id=? AND kind='scan_source' AND target_source_root_id=?`, scanOperation.ID, root.ID).Scan(ctx, &persisted); err != nil {
		return fmt.Errorf("publish prepared source scan analysis: verify scan operation: %w", err)
	}
	if persisted.State != "running" || scanOperation.State != "running" || persisted.Attempt != scanOperation.Attempt || persisted.RiverJobID == nil || scanOperation.RiverJobID == nil || *persisted.RiverJobID != *scanOperation.RiverJobID || persisted.Attempt < 1 {
		return fmt.Errorf("publish prepared source scan analysis: scan operation identity changed")
	}
	canonicalSHAs, err := lockPreparedScanSHAs(ctx, tx, root, scanOperation.ID, candidates)
	if err != nil {
		return fmt.Errorf("publish prepared source scan analysis: lock canonical digests: %w", err)
	}
	for _, candidate := range candidates {
		prepared := candidate.PreparedAnalysis
		if prepared == nil {
			continue
		}
		if err := prepared.Validate(); err != nil {
			return fmt.Errorf("publish prepared analysis for %q: %w", candidate.RelativePath, err)
		}
		location := new(SourceLocation)
		if err := tx.NewRaw(`SELECT * FROM source_location WHERE source_root_id=? AND relative_path=? FOR UPDATE`, root.ID, candidate.RelativePath).Scan(ctx, location); err != nil {
			return fmt.Errorf("publish prepared analysis for %q: read applied location: %w", candidate.RelativePath, err)
		}
		if location.SizeBytes != candidate.SizeBytes || !sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(candidate.Mtime)) {
			return fmt.Errorf("publish prepared analysis for %q: %w", candidate.RelativePath, ErrSourceAnalysisStale)
		}
		if prepared.OriginalLocationID != nil && *prepared.OriginalLocationID != location.ID {
			return fmt.Errorf("publish prepared analysis for %q: prepared location identity changed", candidate.RelativePath)
		}
		if prepared.SuccessorLocationID != nil && *prepared.SuccessorLocationID != location.ID {
			return fmt.Errorf("publish prepared analysis for %q: prepared successor location changed", candidate.RelativePath)
		}
		if prepared.OriginalMediaVariantID != nil {
			var exists bool
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_variant WHERE id=?)`, *prepared.OriginalMediaVariantID).Scan(ctx, &exists); err != nil || !exists {
				return fmt.Errorf("publish prepared analysis for %q: original media variant identity is unavailable", candidate.RelativePath)
			}
		}

		var old SourceAnalysisWork
		err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE current_location_id=? AND current_location_id=location_id FOR UPDATE`, location.ID).Scan(ctx, &old)
		hasOld := err == nil
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("publish prepared analysis for %q: read current work: %w", candidate.RelativePath, err)
		}
		unchanged := hasOld && old.SourceRootID == root.ID && old.ConfiguredPath == root.ConfiguredPath && old.InventoryPath == root.ConfiguredPath && old.RelativePath == candidate.RelativePath && old.SizeBytes == candidate.SizeBytes && sourceAnalysisMtime(old.Mtime).Equal(sourceAnalysisMtime(candidate.Mtime))
		if prepared.RetainedSHA256Variant != nil {
			if prepared.OriginalMediaVariantID == nil || *prepared.OriginalMediaVariantID != prepared.RetainedSHA256Variant.ID || !unchanged {
				return fmt.Errorf("publish prepared analysis for %q: retained SHA-256 is not attached to the unchanged work identity", candidate.RelativePath)
			}
		}
		if unchanged {
			if err := publishPreparedResults(ctx, tx, &old, location, scanOperation.ID, prepared, true, sha256Enabled, nil); err != nil {
				return fmt.Errorf("publish prepared analysis for %q: %w", candidate.RelativePath, err)
			}
			continue
		}
		if hasOld {
			var active int
			if err := tx.NewRaw(`SELECT count(*) FROM operation_source_work_hold h JOIN operation o ON o.id=h.operation_id WHERE h.work_id=? AND o.state IN ('queued','running')`, old.ID).Scan(ctx, &active); err != nil {
				return fmt.Errorf("publish prepared analysis for %q: check old work holds: %w", candidate.RelativePath, err)
			}
			if active != 0 {
				return fmt.Errorf("publish prepared analysis for %q: old analysis work has an active hold", candidate.RelativePath)
			}
			if err := removeScanAnalysisWork(ctx, tx, old.ID); err != nil {
				return fmt.Errorf("publish prepared analysis for %q: replace old work: %w", candidate.RelativePath, err)
			}
		}

		work := SourceAnalysisWork{
			ID: uuid.New(), LocationID: location.ID, SourceRootID: root.ID,
			ConfiguredPath: root.ConfiguredPath, InventoryPath: root.ConfiguredPath,
			RelativePath: candidate.RelativePath, SizeBytes: candidate.SizeBytes,
			Mtime: sourceAnalysisMtime(candidate.Mtime), SHA256Enabled: sha256Enabled,
			OriginScanOperationID: scanOperation.ID,
		}
		var canonicalSHA *SourceMediaVariant
		if prepared.SHA256State == SourcePreparedSucceeded {
			canonicalSHA = canonicalSHAs[hex.EncodeToString(prepared.SHA256Variant.SourceSHA256)]
		}
		if err := publishPreparedResults(ctx, tx, &work, location, scanOperation.ID, prepared, false, sha256Enabled, canonicalSHA); err != nil {
			return fmt.Errorf("publish prepared analysis for %q: %w", candidate.RelativePath, err)
		}
	}
	return nil
}

// lockPreparedScanSHAs acquires all scan-created canonical digest rows in a
// stable digest order. Candidate and current-work locks are taken first, so two
// roots publishing overlapping content cannot deadlock by visiting candidates
// in opposite traversal order.
func lockPreparedScanSHAs(ctx context.Context, tx bun.Tx, root SourceRoot, operationID uuid.UUID, candidates []SourceScanCandidateInput) (map[string]*SourceMediaVariant, error) {
	type digestInput struct {
		variant  *SourceMediaVariant
		existing *uuid.UUID
		digest   []byte
		size     int64
		set      bool
	}
	inputs := make(map[string]digestInput)
	addExisting := func(variant *SourceMediaVariant, size int64) error {
		if variant == nil || len(variant.SourceSHA256) != 32 || variant.SizeBytes != size {
			return fmt.Errorf("existing selected SHA-256 identity is incomplete or stale")
		}
		key := hex.EncodeToString(variant.SourceSHA256)
		id := variant.ID
		input := inputs[key]
		if input.existing != nil && *input.existing != id {
			return fmt.Errorf("multiple immutable rows claim one canonical SHA-256 digest")
		}
		if input.set && input.size != size {
			return fmt.Errorf("one SHA-256 digest has conflicting file sizes")
		}
		input.variant, input.existing, input.digest, input.size, input.set = variant, &id, variant.SourceSHA256, size, true
		inputs[key] = input
		return nil
	}
	seenPaths := make(map[string]struct{})
	for _, candidate := range candidates {
		prepared := candidate.PreparedAnalysis
		if prepared == nil {
			continue
		}
		if err := prepared.Validate(); err != nil {
			return nil, fmt.Errorf("validate %q: %w", candidate.RelativePath, err)
		}
		if _, duplicate := seenPaths[candidate.RelativePath]; duplicate {
			return nil, fmt.Errorf("duplicate prepared source path %q", candidate.RelativePath)
		}
		seenPaths[candidate.RelativePath] = struct{}{}
		location := new(SourceLocation)
		if err := tx.NewRaw(`SELECT * FROM source_location WHERE source_root_id=? AND relative_path=? FOR UPDATE`, root.ID, candidate.RelativePath).Scan(ctx, location); err != nil {
			return nil, fmt.Errorf("lock applied location %q: %w", candidate.RelativePath, err)
		}
		if location.SizeBytes != candidate.SizeBytes || !sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(candidate.Mtime)) || prepared.OriginalLocationID != nil && *prepared.OriginalLocationID != location.ID || prepared.SuccessorLocationID != nil && *prepared.SuccessorLocationID != location.ID {
			return nil, fmt.Errorf("prepared identity for %q no longer matches the applied location", candidate.RelativePath)
		}
		var old SourceAnalysisWork
		err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE current_location_id=? AND current_location_id=location_id FOR UPDATE`, location.ID).Scan(ctx, &old)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("lock current work for %q: %w", candidate.RelativePath, err)
		}
		unchanged := err == nil && old.SourceRootID == root.ID && old.ConfiguredPath == root.ConfiguredPath && old.InventoryPath == root.ConfiguredPath && old.RelativePath == candidate.RelativePath && old.SizeBytes == candidate.SizeBytes && sourceAnalysisMtime(old.Mtime).Equal(sourceAnalysisMtime(candidate.Mtime))
		if prepared.RetainedSHA256Variant != nil && (prepared.OriginalMediaVariantID == nil || *prepared.OriginalMediaVariantID != prepared.RetainedSHA256Variant.ID || !unchanged) {
			return nil, fmt.Errorf("retained SHA-256 for %q is not attached to unchanged work", candidate.RelativePath)
		}
		if unchanged {
			var step SourceAnalysisStep
			err := tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step='sha256'`, old.ID).Scan(ctx, &step)
			if err != nil && err != sql.ErrNoRows {
				return nil, fmt.Errorf("read selected SHA-256 for unchanged %q: %w", candidate.RelativePath, err)
			}
			if step.SuccessSHAVariantID != nil {
				variant := new(SourceMediaVariant)
				if err := tx.NewRaw(`SELECT * FROM media_variant WHERE id=?`, *step.SuccessSHAVariantID).Scan(ctx, variant); err != nil {
					return nil, fmt.Errorf("read selected SHA-256 for unchanged %q: %w", candidate.RelativePath, err)
				}
				if err := addExisting(variant, candidate.SizeBytes); err != nil {
					return nil, fmt.Errorf("selected SHA-256 for unchanged %q: %w", candidate.RelativePath, err)
				}
			}
			if prepared.RetainedSHA256Variant != nil {
				if err := addExisting(prepared.RetainedSHA256Variant, candidate.SizeBytes); err != nil {
					return nil, fmt.Errorf("retained SHA-256 for %q: %w", candidate.RelativePath, err)
				}
			}
		}
		if !unchanged && prepared.SHA256State == SourcePreparedSucceeded {
			if prepared.SHA256Variant.SHA256AppliedOperationID == nil || *prepared.SHA256Variant.SHA256AppliedOperationID != operationID {
				return nil, fmt.Errorf("new SHA-256 provenance for %q does not name the scan operation", candidate.RelativePath)
			}
			key := hex.EncodeToString(prepared.SHA256Variant.SourceSHA256)
			input := inputs[key]
			if input.set && input.size != candidate.SizeBytes {
				return nil, fmt.Errorf("one SHA-256 digest has conflicting file sizes")
			}
			if input.variant == nil {
				input.variant = prepared.SHA256Variant
			}
			input.digest, input.size, input.set = prepared.SHA256Variant.SourceSHA256, candidate.SizeBytes, true
			inputs[key] = input
		}
	}
	keys := make([]string, 0, len(inputs))
	for key := range inputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	selected := make(map[string]*SourceMediaVariant, len(keys))
	for _, key := range keys {
		input := inputs[key]
		var canonical *SourceMediaVariant
		var err error
		if input.existing != nil {
			canonical = new(SourceMediaVariant)
			err = tx.NewRaw(`SELECT * FROM media_variant WHERE source_sha256=? FOR UPDATE`, input.digest).Scan(ctx, canonical)
			if err == nil && (canonical.ID != *input.existing || canonical.SizeBytes != input.size) {
				err = fmt.Errorf("locked canonical SHA-256 identity differs from current work")
			}
		} else {
			canonical, err = insertOrSelectScanSHA(ctx, tx, input.variant, input.size, true)
		}
		if err != nil {
			return nil, fmt.Errorf("lock digest %s: %w", key, err)
		}
		selected[key] = canonical
	}
	return selected, nil
}

func publishPreparedResults(ctx context.Context, tx bun.Tx, work *SourceAnalysisWork, location *SourceLocation, scanOperationID uuid.UUID, prepared *SourceScanPreparedAnalysis, existingWork, sha256Enabled bool, canonicalSHA *SourceMediaVariant) error {
	var selectedSHA, selectedProbe *SourceMediaVariant
	var err error
	if existingWork {
		var current SourceAnalysisStep
		if err := tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step='sha256'`, work.ID).Scan(ctx, &current); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read existing SHA-256 step: %w", err)
		}
		if current.SuccessSHAVariantID != nil {
			selectedSHA = new(SourceMediaVariant)
			if err := tx.NewRaw(`SELECT * FROM media_variant WHERE id=?`, *current.SuccessSHAVariantID).Scan(ctx, selectedSHA); err != nil {
				return fmt.Errorf("read existing SHA-256 result: %w", err)
			}
		}
		if selectedSHA == nil && prepared.RetainedSHA256Variant != nil {
			selectedSHA = new(SourceMediaVariant)
			if err := tx.NewRaw(`SELECT * FROM media_variant WHERE id=?`, prepared.RetainedSHA256Variant.ID).Scan(ctx, selectedSHA); err != nil || selectedSHA.SizeBytes != work.SizeBytes || !samePreparedSHA(selectedSHA, prepared.RetainedSHA256Variant) {
				return fmt.Errorf("retained SHA-256 identity does not match the current immutable result")
			}
		}
		if prepared.RetainedSHA256Variant != nil && (selectedSHA == nil || selectedSHA.SizeBytes != work.SizeBytes || !samePreparedSHA(selectedSHA, prepared.RetainedSHA256Variant)) {
			return fmt.Errorf("retained SHA-256 identity is not selected by the unchanged work")
		}
	} else if prepared.SHA256State == SourcePreparedSucceeded {
		if prepared.SHA256Variant.SHA256AppliedOperationID == nil || *prepared.SHA256Variant.SHA256AppliedOperationID != scanOperationID {
			return fmt.Errorf("new SHA-256 provenance does not name the scan operation")
		}
		selectedSHA = canonicalSHA
		if selectedSHA == nil || selectedSHA.SizeBytes != work.SizeBytes || !bytesEqual(selectedSHA.SourceSHA256, prepared.SHA256Variant.SourceSHA256) {
			return fmt.Errorf("canonical SHA-256 identity was not locked for this work")
		}
	} else if prepared.RetainedSHA256Variant != nil {
		selectedSHA = new(SourceMediaVariant)
		if err := tx.NewRaw(`SELECT * FROM media_variant WHERE id=?`, prepared.RetainedSHA256Variant.ID).Scan(ctx, selectedSHA); err != nil || selectedSHA.SizeBytes != work.SizeBytes || !samePreparedSHA(selectedSHA, prepared.RetainedSHA256Variant) {
			return fmt.Errorf("retained SHA-256 identity does not match the stored immutable result")
		}
	}

	if prepared.ProbeState == SourcePreparedSucceeded {
		if containsPreparedID(prepared.ReusedImmutableIDs, prepared.ProbeVariant.ID) {
			if selectedSHA == nil {
				return fmt.Errorf("reused probe result has no selected SHA-256 identity")
			}
			selectedProbe, err = selectPreparedProbeCache(ctx, tx, selectedSHA.SourceSHA256, work.SizeBytes, prepared.ProbeVariant)
		} else {
			if prepared.ProbeVariant.AppliedOperationID == nil || *prepared.ProbeVariant.AppliedOperationID != scanOperationID {
				return fmt.Errorf("new probe provenance does not name the scan operation")
			}
			if selectedSHA != nil {
				selectedProbe, err = promoteOrInsertScanProbe(ctx, tx, selectedSHA, prepared.ProbeVariant, work.SizeBytes)
			} else {
				selectedProbe, err = insertScanProbe(ctx, tx, prepared.ProbeVariant, work.SizeBytes)
			}
		}
		if err != nil {
			return err
		}
		if selectedSHA != nil {
			if err := registerSourceProbeCache(ctx, tx, selectedSHA.SourceSHA256, selectedProbe.ID); err != nil {
				return fmt.Errorf("register prepared probe cache: %w", err)
			}
		}
		linkID := selectedProbe.ID
		if selectedSHA != nil {
			linkID = selectedSHA.ID
		}
		if location.MediaVariantID == nil || *location.MediaVariantID != linkID {
			if _, err := tx.NewRaw(`UPDATE source_location SET media_variant_id=?, updated_at=now() WHERE id=?`, linkID, location.ID).Exec(ctx); err != nil {
				return fmt.Errorf("link prepared probe: %w", err)
			}
		}
	} else if selectedSHA != nil && (location.MediaVariantID == nil || *location.MediaVariantID != selectedSHA.ID) {
		if _, err := tx.NewRaw(`UPDATE source_location SET media_variant_id=?, updated_at=now() WHERE id=?`, selectedSHA.ID, location.ID).Exec(ctx); err != nil {
			return fmt.Errorf("link prepared SHA-256: %w", err)
		}
	}

	var selectedFingerprint *SourceFingerprintResult
	fingerprintOrigin := "executed"
	retainedFingerprint := false
	if prepared.FingerprintState == SourcePreparedSucceeded {
		if prepared.FingerprintReused {
			if selectedSHA == nil || prepared.FingerprintCacheSHA256 != hex.EncodeToString(selectedSHA.SourceSHA256) {
				return fmt.Errorf("cached fingerprint does not match the selected SHA-256")
			}
			selectedFingerprint, err = selectPreparedFingerprintCache(ctx, tx, selectedSHA.SourceSHA256, prepared.FingerprintCacheVersion, prepared.FingerprintResult)
			fingerprintOrigin = "sha256"
		} else if containsPreparedID(prepared.ReusedImmutableIDs, prepared.FingerprintResult.ID) {
			// A successful fingerprint may be retained from this exact unchanged
			// work even when this scan's probe failed. The prepared immutable ID is
			// only a claim; prove it against the previous successful step and its
			// stored result before carrying it forward.
			if !existingWork {
				return fmt.Errorf("retained fingerprint is not attached to unchanged work")
			}
			var previous SourceAnalysisStep
			if err := tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step='fingerprint'`, work.ID).Scan(ctx, &previous); err != nil || (previous.State != "succeeded" && previous.State != "failed") || previous.SuccessFingerprintResultID == nil || *previous.SuccessFingerprintResultID != prepared.FingerprintResult.ID {
				return fmt.Errorf("retained fingerprint is not the selected prior success of unchanged work")
			}
			selectedFingerprint, err = selectScanFingerprint(ctx, tx, prepared.FingerprintResult.ID)
			if err == nil && !sameFingerprintResult(selectedFingerprint, *prepared.FingerprintResult) {
				err = fmt.Errorf("retained fingerprint differs from its immutable result")
			}
			if previous.SuccessReuseOrigin != nil {
				fingerprintOrigin = *previous.SuccessReuseOrigin
			}
			retainedFingerprint = true
		} else {
			if prepared.FingerprintResult.AppliedOperationID != scanOperationID {
				return fmt.Errorf("new fingerprint provenance does not name the scan operation")
			}
			selectedFingerprint, err = insertOrPromoteScanFingerprint(ctx, tx, selectedSHA, prepared.FingerprintResult)
		}
		if err != nil {
			return err
		}
	}

	states := []struct {
		name        SourceStepName
		state       SourcePreparedStepState
		error, skip string
		sha, probe  *SourceMediaVariant
		fp          *SourceFingerprintResult
		origin      string
	}{
		{SourceStepSHA256, prepared.SHA256State, prepared.SHA256SafeError, prepared.SHA256SkipReason, selectedSHA, nil, nil, "executed"},
		{SourceStepProbe, prepared.ProbeState, prepared.ProbeSafeError, prepared.ProbeSkipReason, nil, selectedProbe, nil, "executed"},
		{SourceStepFingerprint, prepared.FingerprintState, prepared.FingerprintSafeError, prepared.FingerprintSkipReason, nil, nil, selectedFingerprint, fingerprintOrigin},
	}
	if !existingWork {
		if _, err := tx.NewInsert().Model(work).Exec(ctx); err != nil {
			return fmt.Errorf("insert work: %w", err)
		}
	}
	for i := range states {
		state := &states[i]
		if existingWork && state.name == SourceStepSHA256 {
			continue
		}
		if state.name == SourceStepSHA256 && !sha256Enabled {
			state.state, state.error, state.skip = SourcePreparedSkipped, "", "disabled"
		}
		if state.state == SourcePreparedDeferred {
			state.state = SourcePreparedStepState("pending")
		}
		if state.state == SourcePreparedNotRequested {
			if existingWork {
				continue
			}
			state.state, state.skip = SourcePreparedSkipped, "not_requested"
		}
		oldStep := new(SourceAnalysisStep)
		err := tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step=?`, work.ID, state.name).Scan(ctx, oldStep)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read previous %s step: %w", state.name, err)
		}
		if existingWork && err == nil && oldStep.State == "succeeded" && state.state != SourcePreparedSucceeded {
			continue
		}
		if existingWork && state.name == SourceStepFingerprint && retainedFingerprint && oldStep.SuccessFingerprintResultID != nil && selectedFingerprint != nil && *oldStep.SuccessFingerprintResultID == selectedFingerprint.ID {
			// This immutable result is transport for the retained selection, not
			// a new fingerprint-step outcome. Keep the prior state, safe error,
			// and admitted intent (if any) exactly as they were.
			continue
		}
		step := preparedStep(work.ID, state.name, state.state, state.error, state.skip, state.sha, state.probe, state.fp, state.origin)
		if existingWork {
			if err == sql.ErrNoRows {
				if _, err := tx.NewInsert().Model(&step).Exec(ctx); err != nil {
					return fmt.Errorf("insert %s step: %w", step.Step, err)
				}
			} else if err := replaceScanAnalysisStep(ctx, tx, step); err != nil {
				return err
			}
		} else {
			if _, err := tx.NewInsert().Model(&step).Exec(ctx); err != nil {
				return fmt.Errorf("insert %s step: %w", step.Step, err)
			}
		}
	}
	return nil
}

func preparedStep(workID uuid.UUID, name SourceStepName, state SourcePreparedStepState, safeError, skipReason string, sha, probe *SourceMediaVariant, fingerprint *SourceFingerprintResult, origin string) SourceAnalysisStep {
	step := SourceAnalysisStep{WorkID: workID, Step: string(name), State: string(state), UpdatedAt: time.Now().UTC()}
	if state == SourcePreparedFailed {
		step.SafeError = &safeError
	}
	if state == SourcePreparedSkipped {
		step.SkipReason = &skipReason
	}
	if state == SourcePreparedSucceeded {
		step.SuccessReuseOrigin = &origin
		switch name {
		case SourceStepSHA256:
			step.SuccessSHAVariantID = &sha.ID
		case SourceStepProbe:
			step.SuccessProbeVariantID = &probe.ID
		case SourceStepFingerprint:
			step.SuccessFingerprintResultID = &fingerprint.ID
		}
	}
	return step
}

func insertOrSelectScanSHA(ctx context.Context, tx bun.Tx, input *SourceMediaVariant, size int64, lock bool) (*SourceMediaVariant, error) {
	_, err := tx.NewRaw(`INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id)
		VALUES(?,?,?,?,?,?) ON CONFLICT(source_sha256) DO NOTHING`, input.ID, size, input.SourceSHA256, input.SHA256CalculatedAt, input.SHA256Algorithm, input.SHA256AppliedOperationID).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("insert SHA-256 identity: %w", err)
	}
	selected := new(SourceMediaVariant)
	query := `SELECT * FROM media_variant WHERE source_sha256=?`
	if lock {
		query += ` FOR UPDATE`
	}
	if err := tx.NewRaw(query, input.SourceSHA256).Scan(ctx, selected); err != nil {
		return nil, fmt.Errorf("select canonical SHA-256 identity: %w", err)
	}
	if selected.SizeBytes != size {
		return nil, fmt.Errorf("canonical SHA-256 identity has conflicting file size")
	}
	return selected, nil
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func promoteOrInsertScanProbe(ctx context.Context, tx bun.Tx, sha, probe *SourceMediaVariant, size int64) (*SourceMediaVariant, error) {
	var selected SourceMediaVariant
	err := tx.NewRaw(`SELECT * FROM media_variant WHERE id=? FOR UPDATE`, sha.ID).Scan(ctx, &selected)
	if err != nil {
		return nil, fmt.Errorf("lock canonical SHA-256 identity: %w", err)
	}
	if selected.FFProbeVersion == nil {
		if _, err := tx.NewRaw(`UPDATE media_variant SET ffprobe_version=?,ffprobe_json=?::jsonb,analysis_policy_version=?,observed_tags=?::jsonb,inspected_at=?,applied_operation_id=?,audio_stream_count=? WHERE id=? AND ffprobe_version IS NULL`, probe.FFProbeVersion, probe.FFProbeJSON, probe.AnalysisPolicyVersion, probe.ObservedTags, probe.InspectedAt, probe.AppliedOperationID, probe.AudioStreamCount, sha.ID).Exec(ctx); err != nil {
			return nil, fmt.Errorf("promote canonical SHA-256 identity: %w", err)
		}
		return selectScanVariant(ctx, tx, sha.ID)
	}
	return insertScanProbe(ctx, tx, probe, size)
}

func insertScanProbe(ctx context.Context, tx bun.Tx, probe *SourceMediaVariant, size int64) (*SourceMediaVariant, error) {
	_, err := tx.NewRaw(`INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES(?,?,?,?,?::jsonb,?::jsonb,?,?,?)`, probe.ID, size, probe.AnalysisPolicyVersion, probe.FFProbeVersion, probe.FFProbeJSON, probe.ObservedTags, probe.InspectedAt, probe.AppliedOperationID, probe.AudioStreamCount).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("insert probe result: %w", err)
	}
	return selectScanVariant(ctx, tx, probe.ID)
}

func selectScanVariant(ctx context.Context, tx bun.IDB, id uuid.UUID) (*SourceMediaVariant, error) {
	result := new(SourceMediaVariant)
	if err := tx.NewRaw(`SELECT * FROM media_variant WHERE id=?`, id).Scan(ctx, result); err != nil {
		return nil, fmt.Errorf("read selected media variant: %w", err)
	}
	return result, nil
}

func samePreparedSHA(stored, prepared *SourceMediaVariant) bool {
	return stored != nil && prepared != nil && stored.ID == prepared.ID && string(stored.SourceSHA256) == string(prepared.SourceSHA256) && stored.SHA256Algorithm != nil && prepared.SHA256Algorithm != nil && *stored.SHA256Algorithm == *prepared.SHA256Algorithm && stored.SHA256AppliedOperationID != nil && prepared.SHA256AppliedOperationID != nil && *stored.SHA256AppliedOperationID == *prepared.SHA256AppliedOperationID && stored.SHA256CalculatedAt != nil && prepared.SHA256CalculatedAt != nil && stored.SHA256CalculatedAt.Equal(*prepared.SHA256CalculatedAt)
}

func containsPreparedID(ids []uuid.UUID, expected uuid.UUID) bool {
	for _, id := range ids {
		if id == expected {
			return true
		}
	}
	return false
}

func selectPreparedProbeCache(ctx context.Context, tx bun.IDB, digest []byte, size int64, input *SourceMediaVariant) (*SourceMediaVariant, error) {
	result := new(SourceMediaVariant)
	if err := tx.NewRaw(`SELECT result.* FROM media_probe_cache cache
		JOIN media_variant result ON result.id=cache.result_id
		WHERE cache.source_sha256=? AND cache.ffprobe_version_sha256=sha256(convert_to(?, 'UTF8')) AND cache.ffprobe_version=? AND cache.analysis_policy_version=? AND result.id=?`, digest, input.FFProbeVersion, input.FFProbeVersion, input.AnalysisPolicyVersion, input.ID).Scan(ctx, result); err != nil {
		return nil, fmt.Errorf("select prepared probe cache entry: %w", err)
	}
	if result.SizeBytes != size || input.SizeBytes != size || result.FFProbeVersion == nil || input.FFProbeVersion == nil || *result.FFProbeVersion != *input.FFProbeVersion || !sameJSON(result.FFProbeJSON, input.FFProbeJSON) || !sameJSON(result.ObservedTags, input.ObservedTags) || result.AnalysisPolicyVersion == nil || input.AnalysisPolicyVersion == nil || *result.AnalysisPolicyVersion != *input.AnalysisPolicyVersion || result.InspectedAt == nil || input.InspectedAt == nil || !result.InspectedAt.Equal(*input.InspectedAt) || result.AppliedOperationID == nil || input.AppliedOperationID == nil || *result.AppliedOperationID != *input.AppliedOperationID || result.AudioStreamCount == nil || input.AudioStreamCount == nil || *result.AudioStreamCount != *input.AudioStreamCount {
		return nil, fmt.Errorf("prepared probe cache result differs from its immutable database identity")
	}
	return result, nil
}

func replaceScanAnalysisStep(ctx context.Context, tx bun.IDB, step SourceAnalysisStep) error {
	var previousFingerprint *uuid.UUID
	if step.Step == string(SourceStepFingerprint) {
		var previous SourceAnalysisStep
		if err := tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step=? FOR UPDATE`, step.WorkID, step.Step).Scan(ctx, &previous); err != nil {
			return fmt.Errorf("lock previous fingerprint step: %w", err)
		}
		previousFingerprint = previous.SuccessFingerprintResultID
	}
	if _, err := tx.NewRaw(`UPDATE source_analysis_step SET state=?,step_attempt=0,safe_error=?,skip_reason=?,updated_at=now(),input_snapshot=NULL,execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL,last_operation_id=NULL,success_sha_variant_id=?,success_probe_variant_id=?,success_fingerprint_result_id=?,success_reuse_origin=? WHERE work_id=? AND step=?`, step.State, step.SafeError, step.SkipReason, step.SuccessSHAVariantID, step.SuccessProbeVariantID, step.SuccessFingerprintResultID, step.SuccessReuseOrigin, step.WorkID, step.Step).Exec(ctx); err != nil {
		return fmt.Errorf("replace prepared %s step: %w", step.Step, err)
	}
	if previousFingerprint != nil && (step.SuccessFingerprintResultID == nil || *previousFingerprint != *step.SuccessFingerprintResultID) {
		if err := deleteUnreferencedSourceFingerprintResult(ctx, tx, *previousFingerprint); err != nil {
			return fmt.Errorf("cleanup replaced fingerprint result: %w", err)
		}
	}
	return nil
}

func sameJSON(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func selectPreparedFingerprintCache(ctx context.Context, tx bun.IDB, digest []byte, version string, input *SourceFingerprintResult) (*SourceFingerprintResult, error) {
	result := new(SourceFingerprintResult)
	if err := tx.NewRaw(`SELECT * FROM media_fingerprint_result WHERE source_sha256=? AND fpcalc_version=?`, digest, version).Scan(ctx, result); err != nil {
		return nil, fmt.Errorf("select prepared fingerprint cache entry: %w", err)
	}
	if !sameFingerprintResult(result, *input) {
		return nil, fmt.Errorf("prepared fingerprint cache result differs from its immutable database identity")
	}
	return result, nil
}

func insertOrPromoteScanFingerprint(ctx context.Context, tx bun.IDB, sha *SourceMediaVariant, input *SourceFingerprintResult) (*SourceFingerprintResult, error) {
	if sha == nil || len(sha.SourceSHA256) != 32 {
		input.WinningResultID = input.ID
		if _, err := tx.NewInsert().Model(input).Exec(ctx); err != nil {
			return nil, fmt.Errorf("insert fingerprint result without digest: %w", err)
		}
		return selectScanFingerprint(ctx, tx, input.ID)
	}
	input.SourceSHA256 = append([]byte(nil), sha.SourceSHA256...)
	return upsertCanonicalFingerprintResult(ctx, tx, sha.SourceSHA256, *input)
}

func selectScanFingerprint(ctx context.Context, tx bun.IDB, id uuid.UUID) (*SourceFingerprintResult, error) {
	result := new(SourceFingerprintResult)
	if err := tx.NewRaw(`SELECT * FROM media_fingerprint_result WHERE id=?`, id).Scan(ctx, result); err != nil {
		return nil, fmt.Errorf("read selected fingerprint result: %w", err)
	}
	return result, nil
}

func removeScanAnalysisWork(ctx context.Context, tx bun.IDB, workID uuid.UUID) error {
	return retireSourceAnalysisWork(ctx, tx, workID)
}
