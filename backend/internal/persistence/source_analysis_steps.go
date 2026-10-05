package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// SourceStepName identifies one independently applicable analysis result.
type SourceStepName string

const (
	SourceStepSHA256      SourceStepName = "sha256"
	SourceStepProbe       SourceStepName = "probe"
	SourceStepFingerprint SourceStepName = "fingerprint"
)

// SourceAnalysisStepInput is one initial item in a work record.
type SourceAnalysisStepInput struct {
	Step       SourceStepName
	State      string
	SafeError  *string
	SkipReason *string
}

// SourceStepClaim is the immutable delivery fence captured by a worker before
// executing a step. Claim increments and persists the step attempt.
type SourceStepClaim struct {
	WorkID           uuid.UUID
	OperationID      uuid.UUID
	OperationAttempt int
	JobID            int64
	Step             SourceStepName
	AllowSuccessful  bool
}

// SourceSHA256Apply is the successful hash output and its captured fence.
type SourceSHA256Apply struct {
	WorkID           uuid.UUID
	OperationID      uuid.UUID
	OperationAttempt int
	JobID            int64
	StepAttempt      int
	SHA256           []byte
	CalculatedAt     time.Time
	Algorithm        string
}

// SourceProbeApply is one successful full technical probe.
type SourceProbeApply struct {
	WorkID           uuid.UUID
	OperationID      uuid.UUID
	OperationAttempt int
	JobID            int64
	StepAttempt      int
	SizeBytes        int64
	AnalysisPolicy   int
	FFProbeVersion   string
	FFProbeJSON      json.RawMessage
	ObservedTags     json.RawMessage
	InspectedAt      time.Time
	AudioStreamCount int
}

// SourceFingerprintApply carries actual fpcalc/parser provenance.
type SourceFingerprintApply struct {
	WorkID           uuid.UUID
	OperationID      uuid.UUID
	OperationAttempt int
	JobID            int64
	StepAttempt      int
	Result           SourceFingerprintResult
}

// SourceStepFailure settles only the claimed step and leaves its previous
// successful selection, and every sibling step, untouched.
type SourceStepFailure struct {
	WorkID           uuid.UUID
	OperationID      uuid.UUID
	OperationAttempt int
	JobID            int64
	StepAttempt      int
	Step             SourceStepName
	SafeError        string
}

type lockedSourceStep struct {
	Root      SourceRoot
	Location  SourceLocation
	Work      SourceAnalysisWork
	Operation Operation
	Step      SourceAnalysisStep
}

func validSourceStep(step SourceStepName) bool {
	return step == SourceStepSHA256 || step == SourceStepProbe || step == SourceStepFingerprint
}

// StoreSourceAnalysisWork records one current immutable work identity and its
// steps atomically. A caller must use a new UUID after the file stat changes.
func (repository *SourceInventoryRepository) StoreSourceAnalysisWork(ctx context.Context, work *SourceAnalysisWork, steps []SourceAnalysisStepInput) error {
	if work == nil || work.ID == uuid.Nil || work.LocationID == uuid.Nil || work.SourceRootID == uuid.Nil || work.SizeBytes < 0 || work.Mtime.IsZero() || work.ConfiguredPath == "" || work.InventoryPath == "" || work.RelativePath == "" || work.OriginScanOperationID == uuid.Nil {
		return fmt.Errorf("store source analysis work: valid immutable identity is required")
	}
	seen := make(map[SourceStepName]struct{}, len(steps))
	for _, input := range steps {
		if !validSourceStep(input.Step) {
			return fmt.Errorf("store source analysis work: unsupported step %q", input.Step)
		}
		if _, ok := seen[input.Step]; ok {
			return fmt.Errorf("store source analysis work: duplicate step %q", input.Step)
		}
		seen[input.Step] = struct{}{}
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		root := new(SourceRoot)
		if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, work.SourceRootID).Scan(ctx, root); err != nil {
			return fmt.Errorf("store source analysis work: lock root: %w", err)
		}
		location := new(SourceLocation)
		if err := tx.NewRaw(`SELECT * FROM source_location WHERE id=? AND source_root_id=? FOR UPDATE`, work.LocationID, work.SourceRootID).Scan(ctx, location); err != nil {
			return fmt.Errorf("store source analysis work: lock location: %w", err)
		}
		if root.Stale() || !root.Enabled || root.ConfiguredPath != work.ConfiguredPath || root.InventoryPath == nil || *root.InventoryPath != work.InventoryPath || location.RelativePath != work.RelativePath || location.SizeBytes != work.SizeBytes || !sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(work.Mtime)) {
			return fmt.Errorf("store source analysis work: %w", ErrSourceAnalysisStale)
		}
		if _, err := tx.NewInsert().Model(work).Exec(ctx); err != nil {
			return fmt.Errorf("store source analysis work: insert work: %w", err)
		}
		for _, input := range steps {
			step := &SourceAnalysisStep{WorkID: work.ID, Step: string(input.Step), State: input.State, SafeError: input.SafeError, SkipReason: input.SkipReason}
			if _, err := tx.NewInsert().Model(step).Exec(ctx); err != nil {
				return fmt.Errorf("store source analysis work: insert %s step: %w", input.Step, err)
			}
		}
		return nil
	})
}

// ClaimSourceAnalysisStep persists a new delivery attempt before execution.
// Existing successes can be claimed only for an explicit rerun.
func (repository *SourceInventoryRepository) ClaimSourceAnalysisStep(ctx context.Context, claim SourceStepClaim) (int, error) {
	if !validSourceStep(claim.Step) || claim.WorkID == uuid.Nil || claim.OperationID == uuid.Nil || claim.OperationAttempt < 1 || claim.JobID < 1 {
		return 0, fmt.Errorf("claim source analysis step: valid delivery identity is required")
	}
	var attempt int
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		locked, err := lockSourceAnalysisStep(ctx, tx, claim.WorkID, claim.OperationID, claim.OperationAttempt, claim.JobID, claim.Step)
		if err != nil {
			return fmt.Errorf("claim source analysis step: %w", err)
		}
		if locked.Operation.State != "running" {
			return fmt.Errorf("claim source analysis step: operation must be running")
		}
		if locked.Step.State == "queued" || locked.Step.State == "running" || locked.Step.State == "skipped" || (locked.Step.State == "succeeded" && !claim.AllowSuccessful) {
			return fmt.Errorf("claim source analysis step: step is not claimable")
		}
		var held bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation_source_work_hold WHERE operation_id=? AND work_id=?)`, claim.OperationID, claim.WorkID).Scan(ctx, &held); err != nil {
			return fmt.Errorf("claim source analysis step: check work hold: %w", err)
		}
		if !held {
			return fmt.Errorf("claim source analysis step: operation does not hold the work item")
		}
		attempt = locked.Step.StepAttempt + 1
		if _, err := tx.NewRaw(`UPDATE source_analysis_step SET state='running', step_attempt=?, safe_error=NULL, skip_reason=NULL,
			execution_operation_id=?, execution_operation_attempt=?, execution_job_id=?, updated_at=now()
			WHERE work_id=? AND step=?`, attempt, claim.OperationID, claim.OperationAttempt, claim.JobID, claim.WorkID, claim.Step).Exec(ctx); err != nil {
			return fmt.Errorf("claim source analysis step: persist claim: %w", err)
		}
		return nil
	})
	return attempt, err
}

// ApplySourceSHA256 commits the digest independently and promotes selected
// probe/fingerprint successes into canonical caches only when those caches are
// empty. INSERT and SELECT are separate statements for READ COMMITTED races.
func (repository *SourceInventoryRepository) ApplySourceSHA256(ctx context.Context, apply SourceSHA256Apply) (*SourceMediaVariant, error) {
	if len(apply.SHA256) != 32 || apply.OperationID == uuid.Nil || apply.WorkID == uuid.Nil || apply.OperationAttempt < 1 || apply.StepAttempt < 1 || apply.JobID < 1 || apply.Algorithm == "" || apply.CalculatedAt.IsZero() {
		return nil, fmt.Errorf("apply source sha256: valid digest and delivery fence are required")
	}
	var canonical *SourceMediaVariant
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		locked, err := lockSourceAnalysisStep(ctx, tx, apply.WorkID, apply.OperationID, apply.OperationAttempt, apply.JobID, SourceStepSHA256)
		if err != nil {
			return fmt.Errorf("apply source sha256: %w", err)
		}
		if err := checkSourceStepApplyFence(locked, apply.StepAttempt); err != nil {
			if locked.Step.State == "succeeded" && locked.Step.StepAttempt == apply.StepAttempt && sameUUID(locked.Step.LastOperationID, apply.OperationID) && locked.Step.SuccessSHAVariantID != nil {
				canonical = new(SourceMediaVariant)
				if scanErr := tx.NewRaw(`SELECT * FROM media_variant WHERE id=?`, *locked.Step.SuccessSHAVariantID).Scan(ctx, canonical); scanErr == nil && string(canonical.SourceSHA256) == string(apply.SHA256) {
					return nil
				}
			}
			return fmt.Errorf("apply source sha256: %w", ErrSourceAnalysisStale)
		}
		if locked.Operation.State != "running" {
			return fmt.Errorf("apply source sha256: %w", ErrSourceAnalysisStale)
		}
		if _, err := tx.NewRaw(`INSERT INTO media_variant (id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (source_sha256) DO NOTHING`, uuid.New(), locked.Work.SizeBytes, apply.SHA256, apply.CalculatedAt, apply.Algorithm, apply.OperationID).Exec(ctx); err != nil {
			return fmt.Errorf("apply source sha256: insert canonical digest: %w", err)
		}
		canonical = new(SourceMediaVariant)
		if err := tx.NewRaw(`SELECT * FROM media_variant WHERE source_sha256=?`, apply.SHA256).Scan(ctx, canonical); err != nil {
			return fmt.Errorf("apply source sha256: select canonical digest: %w", err)
		}
		if canonical.SizeBytes != locked.Work.SizeBytes {
			return fmt.Errorf("apply source sha256: canonical digest has conflicting size")
		}
		if err := promoteSourceResults(ctx, tx, locked, canonical); err != nil {
			return fmt.Errorf("apply source sha256: promote selected results: %w", err)
		}
		if err := tx.NewRaw(`SELECT * FROM media_variant WHERE id=?`, canonical.ID).Scan(ctx, canonical); err != nil {
			return fmt.Errorf("apply source sha256: read promoted canonical digest: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE source_location SET media_variant_id=?, updated_at=now() WHERE id=?`, canonical.ID, locked.Location.ID).Exec(ctx); err != nil {
			return fmt.Errorf("apply source sha256: link canonical identity: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE source_analysis_step SET state='succeeded', success_sha_variant_id=?, success_reuse_origin='executed', safe_error=NULL,
			execution_operation_id=NULL, execution_operation_attempt=NULL, execution_job_id=NULL, last_operation_id=?, updated_at=now()
			WHERE work_id=? AND step='sha256' AND step_attempt=?`, canonical.ID, apply.OperationID, locked.Work.ID, apply.StepAttempt).Exec(ctx); err != nil {
			return fmt.Errorf("apply source sha256: persist step: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

// ApplySourceProbe commits a successful technical probe while preserving SHA
// identity and prior selected consumers. Canonical probe provenance is filled
// only if absent; otherwise this work retains its own probe-bearing row.
func (repository *SourceInventoryRepository) ApplySourceProbe(ctx context.Context, apply SourceProbeApply) (*SourceMediaVariant, error) {
	if apply.SizeBytes < 0 || apply.AnalysisPolicy < 1 || apply.FFProbeVersion == "" || len(apply.FFProbeJSON) == 0 || apply.ObservedTags == nil || apply.InspectedAt.IsZero() || apply.AudioStreamCount < 0 {
		return nil, fmt.Errorf("apply source probe: complete probe result is required")
	}
	var result *SourceMediaVariant
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		locked, err := lockSourceAnalysisStep(ctx, tx, apply.WorkID, apply.OperationID, apply.OperationAttempt, apply.JobID, SourceStepProbe)
		if err != nil {
			return fmt.Errorf("apply source probe: %w", err)
		}
		if err := checkSourceStepApplyFence(locked, apply.StepAttempt); err != nil {
			if locked.Step.State == "succeeded" && locked.Step.StepAttempt == apply.StepAttempt && sameUUID(locked.Step.LastOperationID, apply.OperationID) && locked.Step.SuccessProbeVariantID != nil {
				id := *locked.Step.SuccessProbeVariantID
				var matches bool
				if scanErr := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_variant WHERE id=? AND size_bytes=? AND ffprobe_version=? AND ffprobe_json=?::jsonb AND observed_tags=?::jsonb AND analysis_policy_version=? AND inspected_at=? AND applied_operation_id=? AND audio_stream_count=?)`, id, apply.SizeBytes, apply.FFProbeVersion, apply.FFProbeJSON, apply.ObservedTags, apply.AnalysisPolicy, apply.InspectedAt, apply.OperationID, apply.AudioStreamCount).Scan(ctx, &matches); scanErr == nil && matches {
					result = new(SourceMediaVariant)
					if err := tx.NewRaw(`SELECT * FROM media_variant WHERE id=?`, id).Scan(ctx, result); err == nil {
						return nil
					}
				}
			}
			return fmt.Errorf("apply source probe: %w", ErrSourceAnalysisStale)
		}
		if locked.Operation.State != "running" || apply.SizeBytes != locked.Work.SizeBytes {
			return fmt.Errorf("apply source probe: %w", ErrSourceAnalysisStale)
		}
		var canonicalID *uuid.UUID
		var shaStep SourceAnalysisStep
		if err := tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step='sha256'`, locked.Work.ID).Scan(ctx, &shaStep); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("apply source probe: read hash step: %w", err)
		}
		if shaStep.SuccessSHAVariantID != nil {
			canonicalID = shaStep.SuccessSHAVariantID
		}
		result = new(SourceMediaVariant)
		if canonicalID != nil {
			canonical := new(SourceMediaVariant)
			if err := tx.NewRaw(`SELECT * FROM media_variant WHERE id=? FOR UPDATE`, *canonicalID).Scan(ctx, canonical); err != nil {
				return fmt.Errorf("apply source probe: lock canonical identity: %w", err)
			}
			if canonical.FFProbeVersion == nil {
				if _, err := tx.NewRaw(`UPDATE media_variant SET ffprobe_version=?, ffprobe_json=?::jsonb, analysis_policy_version=?, observed_tags=?::jsonb,
					inspected_at=?, applied_operation_id=?, audio_stream_count=? WHERE id=? AND ffprobe_version IS NULL`, apply.FFProbeVersion, apply.FFProbeJSON, apply.AnalysisPolicy, apply.ObservedTags, apply.InspectedAt, apply.OperationID, apply.AudioStreamCount, canonical.ID).Exec(ctx); err != nil {
					return fmt.Errorf("apply source probe: fill canonical probe: %w", err)
				}
				if err := tx.NewRaw(`SELECT * FROM media_variant WHERE id=?`, canonical.ID).Scan(ctx, result); err != nil {
					return fmt.Errorf("apply source probe: read canonical result: %w", err)
				}
			} else {
				result, err = insertSourceProbeVariant(ctx, tx, locked.Work.SizeBytes, apply)
				if err != nil {
					return err
				}
			}
		} else {
			result, err = insertSourceProbeVariant(ctx, tx, locked.Work.SizeBytes, apply)
			if err != nil {
				return err
			}
		}
		if canonicalID == nil {
			if _, err := tx.NewRaw(`UPDATE source_location SET media_variant_id=?, updated_at=now() WHERE id=?`, result.ID, locked.Location.ID).Exec(ctx); err != nil {
				return fmt.Errorf("apply source probe: link probe result: %w", err)
			}
		}
		if _, err := tx.NewRaw(`UPDATE source_analysis_step SET state='succeeded', success_probe_variant_id=?, success_reuse_origin='executed', safe_error=NULL,
			execution_operation_id=NULL, execution_operation_attempt=NULL, execution_job_id=NULL, last_operation_id=?, updated_at=now()
			WHERE work_id=? AND step='probe' AND step_attempt=?`, result.ID, apply.OperationID, locked.Work.ID, apply.StepAttempt).Exec(ctx); err != nil {
			return fmt.Errorf("apply source probe: persist step: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ApplySourceFingerprint writes an immutable fingerprint result and records the
// selected winner. The SHA/version cache uses first-committed-wins semantics.
func (repository *SourceInventoryRepository) ApplySourceFingerprint(ctx context.Context, apply SourceFingerprintApply) (*SourceFingerprintResult, error) {
	if apply.Result.ID == uuid.Nil || apply.Result.FPCalcVersion == "" || apply.Result.VersionBanner == "" || apply.Result.AlgorithmNamespace == "" || apply.Result.AlgorithmID < 0 || apply.Result.AlgorithmID > 255 || apply.Result.Fingerprint == "" || apply.Result.ReportedDuration < 0 || apply.Result.CalculatedAt.IsZero() || apply.Result.ParserContractVersion < 1 {
		return nil, fmt.Errorf("apply source fingerprint: complete fingerprint provenance is required")
	}
	apply.Result.AppliedOperationID = apply.OperationID
	var selected *SourceFingerprintResult
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		locked, err := lockSourceAnalysisStep(ctx, tx, apply.WorkID, apply.OperationID, apply.OperationAttempt, apply.JobID, SourceStepFingerprint)
		if err != nil {
			return fmt.Errorf("apply source fingerprint: %w", err)
		}
		if err := checkSourceStepApplyFence(locked, apply.StepAttempt); err != nil {
			if locked.Step.State == "succeeded" && locked.Step.StepAttempt == apply.StepAttempt && sameUUID(locked.Step.LastOperationID, apply.OperationID) && locked.Step.SuccessFingerprintResultID != nil {
				applied := new(SourceFingerprintResult)
				if scanErr := tx.NewRaw(`SELECT * FROM media_fingerprint_result WHERE id=?`, apply.Result.ID).Scan(ctx, applied); scanErr != nil || !sameFingerprintResult(applied, apply.Result) {
					return fmt.Errorf("apply source fingerprint: %w", ErrSourceAnalysisStale)
				}
				selected = new(SourceFingerprintResult)
				if scanErr := tx.NewRaw(`SELECT * FROM media_fingerprint_result WHERE id=?`, *locked.Step.SuccessFingerprintResultID).Scan(ctx, selected); scanErr == nil && selected.ID == apply.Result.ID {
					return nil
				}
			}
			return fmt.Errorf("apply source fingerprint: %w", ErrSourceAnalysisStale)
		}
		if locked.Operation.State != "running" {
			return fmt.Errorf("apply source fingerprint: %w", ErrSourceAnalysisStale)
		}
		previousResultID := locked.Step.SuccessFingerprintResultID
		var sha []byte
		var shaStep SourceAnalysisStep
		if err := tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step='sha256'`, locked.Work.ID).Scan(ctx, &shaStep); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("apply source fingerprint: read hash step: %w", err)
		}
		if shaStep.SuccessSHAVariantID != nil {
			variant := new(SourceMediaVariant)
			if err := tx.NewRaw(`SELECT * FROM media_variant WHERE id=?`, *shaStep.SuccessSHAVariantID).Scan(ctx, variant); err != nil {
				return fmt.Errorf("apply source fingerprint: read digest: %w", err)
			}
			sha = variant.SourceSHA256
		}
		if _, err := tx.NewInsert().Model(&apply.Result).Exec(ctx); err != nil {
			return fmt.Errorf("apply source fingerprint: insert immutable result: %w", err)
		}
		selected = &apply.Result
		origin := "executed"
		if len(sha) == 32 {
			if _, err := tx.NewRaw(`INSERT INTO media_fingerprint_cache(source_sha256,fpcalc_version,result_id) VALUES(?,?,?) ON CONFLICT(source_sha256,fpcalc_version) DO NOTHING`, sha, apply.Result.FPCalcVersion, apply.Result.ID).Exec(ctx); err != nil {
				return fmt.Errorf("apply source fingerprint: cache result: %w", err)
			}
		}
		if _, err := tx.NewRaw(`UPDATE source_analysis_step SET state='succeeded', success_fingerprint_result_id=?, success_reuse_origin=?, safe_error=NULL,
			execution_operation_id=NULL, execution_operation_attempt=NULL, execution_job_id=NULL, last_operation_id=?, updated_at=now()
			WHERE work_id=? AND step='fingerprint' AND step_attempt=?`, selected.ID, origin, apply.OperationID, locked.Work.ID, apply.StepAttempt).Exec(ctx); err != nil {
			return fmt.Errorf("apply source fingerprint: persist step: %w", err)
		}
		if previousResultID != nil && *previousResultID != selected.ID {
			if err := deleteUnreferencedSourceFingerprintResult(ctx, tx, *previousResultID); err != nil {
				return fmt.Errorf("apply source fingerprint: cleanup previous result: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return selected, nil
}

// ReuseSourceFingerprint selects an immutable cached result for the current
// digest/version pair without claiming that fpcalc ran for this operation.
func (repository *SourceInventoryRepository) ReuseSourceFingerprint(ctx context.Context, claim SourceStepClaim, capturedStepAttempt int, fpcalcVersion string) (*SourceFingerprintResult, error) {
	if claim.Step != SourceStepFingerprint || claim.WorkID == uuid.Nil || claim.OperationID == uuid.Nil || claim.OperationAttempt < 1 || claim.JobID < 1 || capturedStepAttempt < 1 || fpcalcVersion == "" {
		return nil, fmt.Errorf("reuse source fingerprint: valid fingerprint fence and version are required")
	}
	var selected *SourceFingerprintResult
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		locked, err := lockSourceAnalysisStep(ctx, tx, claim.WorkID, claim.OperationID, claim.OperationAttempt, claim.JobID, SourceStepFingerprint)
		if err != nil {
			return fmt.Errorf("reuse source fingerprint: %w", err)
		}
		var shaStep SourceAnalysisStep
		if err := tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step='sha256'`, claim.WorkID).Scan(ctx, &shaStep); err != nil || shaStep.SuccessSHAVariantID == nil {
			return fmt.Errorf("reuse source fingerprint: no successful SHA identity is selected")
		}
		variant := new(SourceMediaVariant)
		if err := tx.NewRaw(`SELECT * FROM media_variant WHERE id=?`, *shaStep.SuccessSHAVariantID).Scan(ctx, variant); err != nil {
			return fmt.Errorf("reuse source fingerprint: read current digest: %w", err)
		}
		selected = new(SourceFingerprintResult)
		if err := tx.NewRaw(`SELECT r.* FROM media_fingerprint_cache c JOIN media_fingerprint_result r ON r.id=c.result_id AND r.fpcalc_version=c.fpcalc_version WHERE c.source_sha256=? AND c.fpcalc_version=?`, variant.SourceSHA256, fpcalcVersion).Scan(ctx, selected); err != nil {
			return fmt.Errorf("reuse source fingerprint: cached result does not exist: %w", err)
		}
		if err := checkSourceStepApplyFence(locked, capturedStepAttempt); err != nil {
			if locked.Step.State == "succeeded" && locked.Step.StepAttempt == capturedStepAttempt && sameUUID(locked.Step.LastOperationID, claim.OperationID) && sameUUID(locked.Step.SuccessFingerprintResultID, selected.ID) {
				return nil
			}
			return fmt.Errorf("reuse source fingerprint: %w", ErrSourceAnalysisStale)
		}
		if locked.Operation.State != "running" {
			return fmt.Errorf("reuse source fingerprint: %w", ErrSourceAnalysisStale)
		}
		previousResultID := locked.Step.SuccessFingerprintResultID
		if _, err := tx.NewRaw(`UPDATE source_analysis_step SET state='succeeded', success_fingerprint_result_id=?, success_reuse_origin='sha256', safe_error=NULL,
				execution_operation_id=NULL, execution_operation_attempt=NULL, execution_job_id=NULL, last_operation_id=?, updated_at=now()
				WHERE work_id=? AND step='fingerprint' AND step_attempt=?`, selected.ID, claim.OperationID, claim.WorkID, capturedStepAttempt).Exec(ctx); err != nil {
			return fmt.Errorf("reuse source fingerprint: select cached result: %w", err)
		}
		if previousResultID != nil && *previousResultID != selected.ID {
			if err := deleteUnreferencedSourceFingerprintResult(ctx, tx, *previousResultID); err != nil {
				return fmt.Errorf("reuse source fingerprint: cleanup previous result: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return selected, nil
}

func deleteUnreferencedSourceFingerprintResult(ctx context.Context, tx bun.IDB, resultID uuid.UUID) error {
	if _, err := tx.NewRaw(`DELETE FROM media_fingerprint_result AS result WHERE result.id=?
		AND NOT EXISTS (SELECT 1 FROM source_analysis_step AS step WHERE step.success_fingerprint_result_id=result.id)
		AND NOT EXISTS (SELECT 1 FROM media_fingerprint_cache AS cache WHERE cache.result_id=result.id)
		AND NOT EXISTS (SELECT 1 FROM operation_source_work_hold AS hold
			JOIN source_analysis_step AS step ON step.work_id=hold.work_id
			WHERE hold.operation_id IN (SELECT id FROM operation WHERE state IN ('queued','running'))
			AND step.success_fingerprint_result_id=result.id)`, resultID).Exec(ctx); err != nil {
		return fmt.Errorf("delete unreferenced source fingerprint result: %w", err)
	}
	return nil
}

// FailSourceAnalysisStep records one safe error while retaining any successful
// selection from an earlier attempt of this same step.
func (repository *SourceInventoryRepository) FailSourceAnalysisStep(ctx context.Context, failure SourceStepFailure) error {
	if !validSourceStep(failure.Step) || failure.SafeError == "" || failure.OperationID == uuid.Nil || failure.WorkID == uuid.Nil || failure.OperationAttempt < 1 || failure.JobID < 1 || failure.StepAttempt < 1 {
		return fmt.Errorf("fail source analysis step: valid failure fence and safe error are required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		locked, err := lockSourceAnalysisStep(ctx, tx, failure.WorkID, failure.OperationID, failure.OperationAttempt, failure.JobID, failure.Step)
		if err != nil {
			return fmt.Errorf("fail source analysis step: %w", err)
		}
		if locked.Step.State == "failed" && locked.Step.StepAttempt == failure.StepAttempt && sameUUID(locked.Step.LastOperationID, failure.OperationID) && locked.Step.SafeError != nil && *locked.Step.SafeError == failure.SafeError {
			return nil
		}
		if err := checkSourceStepApplyFence(locked, failure.StepAttempt); err != nil || locked.Operation.State != "running" {
			return fmt.Errorf("fail source analysis step: %w", ErrSourceAnalysisStale)
		}
		if _, err := tx.NewRaw(`UPDATE source_analysis_step SET state='failed', safe_error=?, skip_reason=NULL,
			execution_operation_id=NULL, execution_operation_attempt=NULL, execution_job_id=NULL,
			last_operation_id=?, updated_at=now() WHERE work_id=? AND step=?`, failure.SafeError, failure.OperationID, failure.WorkID, failure.Step).Exec(ctx); err != nil {
			return fmt.Errorf("fail source analysis step: persist failure: %w", err)
		}
		return nil
	})
}

// CleanupSourceMediaVariants removes only requested digest-less variants that
// have no location, selected-step consumer, operation result reference, or active work hold.
// SHA rows are intentionally durable orphan caches.
func (repository *SourceInventoryRepository) CleanupSourceMediaVariants(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := repository.db.NewRaw(`DELETE FROM media_variant v WHERE v.id IN (?) AND v.source_sha256 IS NULL
		AND NOT EXISTS (SELECT 1 FROM source_location l WHERE l.media_variant_id=v.id)
		AND NOT EXISTS (SELECT 1 FROM operation o WHERE o.analysis_media_variant_id=v.id)
		AND NOT EXISTS (SELECT 1 FROM source_analysis_step s WHERE s.success_sha_variant_id=v.id OR s.success_probe_variant_id=v.id)
		AND NOT EXISTS (SELECT 1 FROM operation_source_work_hold h JOIN source_analysis_step s ON s.work_id=h.work_id
			WHERE h.operation_id IN (SELECT id FROM operation WHERE state IN ('queued','running'))
			AND (s.success_sha_variant_id=v.id OR s.success_probe_variant_id=v.id))`, bun.List(ids)).Exec(ctx); err != nil {
		return fmt.Errorf("cleanup source media variants: %w", err)
	}
	return nil
}

func lockSourceAnalysisStep(ctx context.Context, tx bun.Tx, workID, operationID uuid.UUID, operationAttempt int, jobID int64, stepName SourceStepName) (*lockedSourceStep, error) {
	var rootID, locationID uuid.UUID
	if err := tx.NewRaw(`SELECT source_root_id,location_id FROM source_analysis_work WHERE id=?`, workID).Scan(ctx, &rootID, &locationID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrSourceAnalysisStale
		}
		return nil, err
	}
	locked := new(lockedSourceStep)
	if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, rootID).Scan(ctx, &locked.Root); err != nil {
		return nil, ErrSourceAnalysisStale
	}
	if err := tx.NewRaw(`SELECT * FROM source_location WHERE id=? AND source_root_id=? FOR UPDATE`, locationID, rootID).Scan(ctx, &locked.Location); err != nil {
		return nil, ErrSourceAnalysisStale
	}
	if err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE id=? FOR UPDATE`, workID).Scan(ctx, &locked.Work); err != nil {
		return nil, ErrSourceAnalysisStale
	}
	if !locked.Root.Enabled || locked.Root.Stale() || locked.Root.ConfiguredPath != locked.Work.ConfiguredPath || locked.Root.InventoryPath == nil || *locked.Root.InventoryPath != locked.Work.InventoryPath || locked.Location.RelativePath != locked.Work.RelativePath || locked.Location.SizeBytes != locked.Work.SizeBytes || !sourceAnalysisMtime(locked.Location.Mtime).Equal(sourceAnalysisMtime(locked.Work.Mtime)) {
		return nil, ErrSourceAnalysisStale
	}
	if err := tx.NewRaw(`SELECT * FROM operation WHERE id=? FOR UPDATE`, operationID).Scan(ctx, &locked.Operation); err != nil {
		return nil, ErrSourceAnalysisStale
	}
	if locked.Operation.Kind != analysisSourceOperationKind || locked.Operation.TargetSourceRootID == nil || *locked.Operation.TargetSourceRootID != rootID || (locked.Operation.TargetSourceLocationID != nil && *locked.Operation.TargetSourceLocationID != locationID) || locked.Operation.Attempt != operationAttempt || locked.Operation.RiverJobID == nil || *locked.Operation.RiverJobID != jobID {
		return nil, ErrSourceAnalysisStale
	}
	if err := tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step=? FOR UPDATE`, workID, stepName).Scan(ctx, &locked.Step); err != nil {
		return nil, ErrSourceAnalysisStale
	}
	return locked, nil
}

func checkSourceStepApplyFence(locked *lockedSourceStep, attempt int) error {
	if locked.Step.State != "running" || locked.Step.StepAttempt != attempt || locked.Step.ExecutionOperationID == nil || *locked.Step.ExecutionOperationID != locked.Operation.ID || locked.Step.ExecutionOperationAttempt == nil || *locked.Step.ExecutionOperationAttempt != locked.Operation.Attempt || locked.Step.ExecutionJobID == nil || locked.Operation.RiverJobID == nil || *locked.Step.ExecutionJobID != *locked.Operation.RiverJobID {
		return ErrSourceAnalysisStale
	}
	return nil
}

func insertSourceProbeVariant(ctx context.Context, tx bun.Tx, size int64, apply SourceProbeApply) (*SourceMediaVariant, error) {
	id := uuid.New()
	if _, err := tx.NewRaw(`INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count)
		VALUES(?,?,?, ?,?::jsonb,?::jsonb,?,?,?)`, id, size, apply.AnalysisPolicy, apply.FFProbeVersion, apply.FFProbeJSON, apply.ObservedTags, apply.InspectedAt, apply.OperationID, apply.AudioStreamCount).Exec(ctx); err != nil {
		return nil, fmt.Errorf("insert probe-bearing result: %w", err)
	}
	result := new(SourceMediaVariant)
	if err := tx.NewRaw(`SELECT * FROM media_variant WHERE id=?`, id).Scan(ctx, result); err != nil {
		return nil, fmt.Errorf("read probe-bearing result: %w", err)
	}
	return result, nil
}

func promoteSourceResults(ctx context.Context, tx bun.Tx, locked *lockedSourceStep, canonical *SourceMediaVariant) error {
	var probeStep SourceAnalysisStep
	if err := tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step='probe'`, locked.Work.ID).Scan(ctx, &probeStep); err != nil && err != sql.ErrNoRows {
		return err
	}
	if probeStep.SuccessProbeVariantID != nil && canonical.FFProbeVersion == nil {
		if _, err := tx.NewRaw(`UPDATE media_variant dst SET ffprobe_version=src.ffprobe_version, ffprobe_json=src.ffprobe_json,
			analysis_policy_version=src.analysis_policy_version, observed_tags=src.observed_tags, inspected_at=src.inspected_at,
			applied_operation_id=src.applied_operation_id, audio_stream_count=src.audio_stream_count
			FROM media_variant src WHERE dst.id=? AND dst.ffprobe_version IS NULL AND src.id=?`, canonical.ID, *probeStep.SuccessProbeVariantID).Exec(ctx); err != nil {
			return err
		}
	}
	var fingerprintStep SourceAnalysisStep
	if err := tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step='fingerprint'`, locked.Work.ID).Scan(ctx, &fingerprintStep); err != nil && err != sql.ErrNoRows {
		return err
	}
	if fingerprintStep.SuccessFingerprintResultID != nil {
		result := new(SourceFingerprintResult)
		if err := tx.NewRaw(`SELECT * FROM media_fingerprint_result WHERE id=?`, *fingerprintStep.SuccessFingerprintResultID).Scan(ctx, result); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO media_fingerprint_cache(source_sha256,fpcalc_version,result_id) VALUES(?,?,?) ON CONFLICT(source_sha256,fpcalc_version) DO NOTHING`, canonical.SourceSHA256, result.FPCalcVersion, result.ID).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func sameUUID(left *uuid.UUID, right uuid.UUID) bool { return left != nil && *left == right }

func sameFingerprintResult(left *SourceFingerprintResult, right SourceFingerprintResult) bool {
	return left.ID == right.ID && left.FPCalcVersion == right.FPCalcVersion && left.VersionBanner == right.VersionBanner && left.AlgorithmNamespace == right.AlgorithmNamespace && left.AlgorithmID == right.AlgorithmID && left.Fingerprint == right.Fingerprint && left.ReportedDuration == right.ReportedDuration && sourceAnalysisMtime(left.CalculatedAt).Equal(sourceAnalysisMtime(right.CalculatedAt)) && left.ParserContractVersion == right.ParserContractVersion && left.AppliedOperationID == right.AppliedOperationID
}
