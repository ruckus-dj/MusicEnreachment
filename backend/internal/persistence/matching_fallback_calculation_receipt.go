package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const (
	matchingFallbackCalculationSchemaVersion           = 3
	matchingFallbackCalculationCalculatorVersion       = 3
	matchingFallbackCalculationDigestVersion           = 3
	matchingFallbackCalculationLegacySchemaVersion     = 2
	matchingFallbackCalculationLegacyCalculatorVersion = 1
	matchingFallbackCalculationLegacyDigestVersion     = 2
)

// MatchingFallbackCalculationReceipt is the immutable calculation handoff. The
// context is the full canonical loader snapshot; edges contains the complete
// compact calculator result, not only qualifying assignments.
type MatchingFallbackCalculationReceipt struct {
	RunID               uuid.UUID       `json:"run_id"`
	AcquisitionRevision int64           `json:"acquisition_revision"`
	StageIndex          int             `json:"stage_index"`
	InputDigest         string          `json:"input_digest"`
	Context             json.RawMessage `json:"context"`
	Edges               json.RawMessage `json:"edges"`
}

// GetMatchingFallbackCalculationReceipt returns immutable saved evidence. The
// stored version tuple selects the canonical digest format; a historical receipt
// is validated using its original format and is never recalculated here.
func (repository *SetupManagerRepository) GetMatchingFallbackCalculationReceipt(
	ctx context.Context, runID uuid.UUID, acquisitionRevision int64,
) (*MatchingFallbackCalculationReceipt, error) {
	if runID == uuid.Nil || acquisitionRevision < 1 {
		return nil, fmt.Errorf("read fallback calculation receipt: identity is required")
	}
	var receipt MatchingFallbackCalculationReceipt
	var contextJSON, edgesJSON string
	var schemaVersion, calculatorVersion, digestVersion int
	err := repository.db.NewRaw(`SELECT receipt.run_id,receipt.acquisition_revision,receipt.stage_index,
		encode(receipt.input_digest,'hex'),receipt.context::text,receipt.edge_calculations::text,
		receipt.schema_version,receipt.calculator_version,receipt.digest_version
		FROM matching_fallback_calculation_receipt receipt
		WHERE receipt.run_id=? AND receipt.acquisition_revision=?`, runID, acquisitionRevision).
		Scan(ctx, &receipt.RunID, &receipt.AcquisitionRevision, &receipt.StageIndex, &receipt.InputDigest,
			&contextJSON, &edgesJSON, &schemaVersion, &calculatorVersion, &digestVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read fallback calculation receipt: %w", err)
	}
	receipt.Context = json.RawMessage(contextJSON)
	receipt.Edges = json.RawMessage(edgesJSON)
	if receipt.RunID != runID || receipt.AcquisitionRevision != acquisitionRevision || receipt.StageIndex < 0 ||
		!json.Valid(receipt.Context) || !json.Valid(receipt.Edges) {
		return nil, fmt.Errorf("read fallback calculation receipt: saved identity or evidence is invalid")
	}
	decodedDigest, decodeErr := hex.DecodeString(receipt.InputDigest)
	if decodeErr != nil || len(decodedDigest) != sha256.Size || hex.EncodeToString(decodedDigest) != receipt.InputDigest {
		return nil, fmt.Errorf("read fallback calculation receipt: saved digest is invalid")
	}
	var snapshot MatchingFallbackCalculationContext
	if err := json.Unmarshal(receipt.Context, &snapshot); err != nil || snapshot.RunID != runID ||
		snapshot.AcquisitionRevision != acquisitionRevision || snapshot.StageIndex != receipt.StageIndex {
		return nil, fmt.Errorf("read fallback calculation receipt: saved context identity is invalid")
	}
	var calculatedDigest string
	switch {
	case schemaVersion == matchingFallbackCalculationSchemaVersion && calculatorVersion == matchingFallbackCalculationCalculatorVersion && digestVersion == matchingFallbackCalculationDigestVersion:
		calculatedDigest, err = MatchingFallbackCalculationInputDigest(snapshot)
	case schemaVersion == matchingFallbackCalculationLegacySchemaVersion && calculatorVersion == matchingFallbackCalculationLegacyCalculatorVersion && digestVersion == matchingFallbackCalculationLegacyDigestVersion && receipt.StageIndex == 0:
		calculatedDigest, err = MatchingFallbackCalculationInputDigestV2(snapshot)
	default:
		return nil, fmt.Errorf("read fallback calculation receipt: stored version tuple is unsupported")
	}
	if err != nil || calculatedDigest != receipt.InputDigest {
		return nil, fmt.Errorf("read fallback calculation receipt: saved context digest is invalid")
	}
	if err := ValidateMatchingFallbackCalculationEdges(snapshot, receipt.Edges); err != nil {
		return nil, fmt.Errorf("read fallback calculation receipt: %w", err)
	}
	return &receipt, nil
}

// RecordMatchingFallbackCalculationForDelivery persists a calculation only
// while the exact running River delivery owns the current applicability phase.
// It deliberately does not consume or otherwise advance that delivery.
func (repository *SetupManagerRepository) RecordMatchingFallbackCalculationForDelivery(
	ctx context.Context, runID uuid.UUID, attempt int, riverJobID int64, claim MatchingDeliveryClaim,
	expectedRevision int64, receipt MatchingFallbackCalculationReceipt,
) (bool, error) {
	if runID == uuid.Nil || attempt < 1 || riverJobID < 1 || claim.ExecutionEpoch < 1 || claim.RiverAttempt < 1 ||
		expectedRevision < 1 || receipt.RunID != runID || receipt.AcquisitionRevision != expectedRevision {
		return false, fmt.Errorf("record fallback calculation receipt: invalid receipt or delivery identity")
	}
	digest, err := hex.DecodeString(receipt.InputDigest)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != receipt.InputDigest ||
		!json.Valid(receipt.Context) || !json.Valid(receipt.Edges) {
		return false, fmt.Errorf("record fallback calculation receipt: invalid digest or serialized evidence")
	}
	var contextObject map[string]json.RawMessage
	var edgesArray []json.RawMessage
	if json.Unmarshal(receipt.Context, &contextObject) != nil || contextObject == nil ||
		json.Unmarshal(receipt.Edges, &edgesArray) != nil || edgesArray == nil {
		return false, fmt.Errorf("record fallback calculation receipt: context must be an object and edges an array")
	}
	var snapshot MatchingFallbackCalculationContext
	if json.Unmarshal(receipt.Context, &snapshot) != nil || snapshot.RunID != runID || snapshot.AcquisitionRevision != expectedRevision ||
		snapshot.StageIndex != receipt.StageIndex || receipt.StageIndex < 0 {
		return false, fmt.Errorf("record fallback calculation receipt: context identity is invalid")
	}
	contextDigest, err := MatchingFallbackCalculationInputDigest(snapshot)
	if err != nil || contextDigest != receipt.InputDigest {
		return false, fmt.Errorf("record fallback calculation receipt: context digest does not match")
	}
	if err := ValidateMatchingFallbackCalculationEdges(snapshot, receipt.Edges); err != nil {
		return false, fmt.Errorf("record fallback calculation receipt: %w", err)
	}

	recorded := false
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("record fallback calculation receipt: lock provider configuration: %w", err)
		}
		var runAuthority struct {
			SourceID uuid.UUID `bun:"provider_source_id"`
			Identity string    `bun:"provider_configuration_identity"`
			Fence    int       `bun:"fence_version"`
		}
		if err := tx.NewRaw(`SELECT provider_source_id,provider_configuration_identity,fence_version FROM matching_run WHERE id=?`, runID).Scan(ctx, &runAuthority); err != nil {
			return fmt.Errorf("record fallback calculation receipt: read run authority: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("record fallback calculation receipt: lock source mutations: %w", err)
		}
		var sourceIdentity, configuredIdentity string
		if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=? FOR SHARE`, runAuthority.SourceID).Scan(ctx, &sourceIdentity); err != nil {
			return fmt.Errorf("record fallback calculation receipt: lock source authority: %w", err)
		}
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &configuredIdentity); err != nil {
			return fmt.Errorf("record fallback calculation receipt: read provider configuration: %w", err)
		}
		if sourceIdentity != runAuthority.Identity || configuredIdentity != runAuthority.Identity {
			return nil
		}
		if err := validateMatchingRunFence(ctx, tx, runID, runAuthority.Fence); err != nil {
			return err
		}
		var lockedGroups []uuid.UUID
		if err := tx.NewRaw(`SELECT generation.group_id FROM incoming_matching_generation generation
			JOIN matching_run_target target ON target.group_id=generation.group_id AND target.run_id=?
			JOIN incoming_group current_group ON current_group.id=target.group_id
			ORDER BY generation.group_id FOR UPDATE OF generation,current_group`, runID).Scan(ctx, &lockedGroups); err != nil {
			return fmt.Errorf("record fallback calculation receipt: lock target groups: %w", err)
		}
		var staleTarget bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM matching_run_target target
			LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
			LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
			WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
				generation.high_water IS DISTINCT FROM target.target_generation OR
				current_group.revision IS DISTINCT FROM target.captured_group_revision)
		)`, runID).Scan(ctx, &staleTarget); err != nil {
			return fmt.Errorf("record fallback calculation receipt: validate target generations: %w", err)
		}
		if staleTarget {
			return nil
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, runID)
		if err != nil {
			return err
		}
		if operation.Kind != "matching_evaluation_v1" || operation.Attempt != attempt || operation.State != "running" ||
			operation.RiverJobID == nil || *operation.RiverJobID != riverJobID {
			return nil
		}
		var current struct {
			AcquisitionRevision int64  `bun:"revision"`
			Phase               string `bun:"current_phase"`
			RunState            string `bun:"state"`
			Epoch               int64  `bun:"execution_epoch"`
			ActiveEpoch         *int64 `bun:"active_execution_epoch"`
			ActiveJob           *int64 `bun:"active_river_job_id"`
			ActiveAttempt       *int   `bun:"active_river_attempt"`
			CurrentJob          *int64 `bun:"current_river_job_id"`
		}
		if err := tx.NewRaw(`SELECT acquisition.revision,acquisition.current_phase,run.state,run.execution_epoch,
			run.active_execution_epoch,run.active_river_job_id,run.active_river_attempt,run.current_river_job_id
			FROM matching_run run JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
			WHERE run.id=? FOR UPDATE OF run`, runID).Scan(ctx, &current.AcquisitionRevision, &current.Phase, &current.RunState,
			&current.Epoch, &current.ActiveEpoch, &current.ActiveJob, &current.ActiveAttempt, &current.CurrentJob); err != nil {
			return fmt.Errorf("record fallback calculation receipt: lock run: %w", err)
		}
		if current.AcquisitionRevision != expectedRevision || current.Phase != MatchingAcquisitionFallbackApplicability ||
			current.RunState != "running" || current.Epoch != claim.ExecutionEpoch || current.ActiveEpoch == nil ||
			*current.ActiveEpoch != claim.ExecutionEpoch || current.ActiveJob == nil ||
			*current.ActiveJob != riverJobID || current.ActiveAttempt == nil || *current.ActiveAttempt != claim.RiverAttempt ||
			current.CurrentJob == nil || *current.CurrentJob != riverJobID {
			return nil
		}
		var registered bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM matching_delivery delivery JOIN river_job ON river_job.id=delivery.river_job_id
			WHERE delivery.run_id=? AND delivery.operation_attempt=? AND delivery.river_job_id=? AND delivery.consumed_at IS NULL
			AND river_job.state='running' AND river_job.attempt=?)`, runID, attempt, riverJobID, claim.RiverAttempt).Scan(ctx, &registered); err != nil {
			return fmt.Errorf("record fallback calculation receipt: verify delivery: %w", err)
		}
		if !registered {
			return nil
		}
		// The receipt's self-reported digest is not authority. Reconstruct the
		// frozen context through this transaction after all writer fences are held.
		persisted, exists, err := loadMatchingFallbackCalculationContext(ctx, tx, runID, expectedRevision)
		if err != nil {
			return fmt.Errorf("record fallback calculation receipt: reconstruct persisted context: %w", err)
		}
		if !exists {
			return nil
		}
		persistedDigest, err := MatchingFallbackCalculationInputDigest(persisted)
		if err != nil {
			return fmt.Errorf("record fallback calculation receipt: digest persisted context: %w", err)
		}
		persistedCanonical, err := canonicalMatchingFallbackCalculationContextBytes(persisted)
		if err != nil {
			return fmt.Errorf("record fallback calculation receipt: canonicalize persisted context: %w", err)
		}
		receiptCanonical, err := canonicalMatchingFallbackCalculationContextBytes(snapshot)
		if err != nil {
			return fmt.Errorf("record fallback calculation receipt: canonicalize supplied context: %w", err)
		}
		if persistedDigest != receipt.InputDigest || !equalDigest(persistedCanonical, receiptCanonical) {
			return errors.New("record fallback calculation receipt: supplied context differs from persisted authority")
		}

		var priorReceipt bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM matching_fallback_calculation_receipt WHERE run_id=? AND acquisition_revision=?)`, runID, expectedRevision).Scan(ctx, &priorReceipt); err != nil {
			return fmt.Errorf("record fallback calculation receipt: check prior receipt: %w", err)
		}
		if priorReceipt {
			var oldDigest, oldContext, oldEdges string
			var oldStage, oldSchema, oldCalculator, oldDigestVersion int
			if err := tx.NewRaw(`SELECT encode(input_digest,'hex'),context::text,edge_calculations::text,stage_index,schema_version,calculator_version,digest_version
				FROM matching_fallback_calculation_receipt WHERE run_id=? AND acquisition_revision=? FOR UPDATE`, runID, expectedRevision).
				Scan(ctx, &oldDigest, &oldContext, &oldEdges, &oldStage, &oldSchema, &oldCalculator, &oldDigestVersion); err != nil {
				return fmt.Errorf("record fallback calculation receipt: read prior receipt: %w", err)
			}
			var oldSnapshot MatchingFallbackCalculationContext
			var oldContextCanonical []byte
			var contextErr error
			if json.Unmarshal([]byte(oldContext), &oldSnapshot) == nil {
				switch {
				case oldSchema == matchingFallbackCalculationLegacySchemaVersion && oldCalculator == matchingFallbackCalculationLegacyCalculatorVersion && oldDigestVersion == matchingFallbackCalculationLegacyDigestVersion:
					oldContextCanonical, contextErr = canonicalMatchingFallbackCalculationContextBytesV2(oldSnapshot)
				case oldSchema == matchingFallbackCalculationSchemaVersion && oldCalculator == matchingFallbackCalculationCalculatorVersion && oldDigestVersion == matchingFallbackCalculationDigestVersion:
					oldContextCanonical, contextErr = canonicalMatchingFallbackCalculationContextBytes(oldSnapshot)
				default:
					contextErr = errors.New("stored calculation version pair is unsupported")
				}
			} else {
				contextErr = errors.New("stored context is invalid")
			}
			receiptContextCanonical := receiptCanonical
			if oldSchema == matchingFallbackCalculationLegacySchemaVersion && oldCalculator == matchingFallbackCalculationLegacyCalculatorVersion && oldDigestVersion == matchingFallbackCalculationLegacyDigestVersion {
				receiptContextCanonical, contextErr = canonicalMatchingFallbackCalculationContextBytesV2(snapshot)
			}
			digestMatches := oldDigest == receipt.InputDigest
			if oldSchema == matchingFallbackCalculationLegacySchemaVersion && oldCalculator == matchingFallbackCalculationLegacyCalculatorVersion && oldDigestVersion == matchingFallbackCalculationLegacyDigestVersion {
				digestMatches = false
				if legacyDigest, digestErr := MatchingFallbackCalculationInputDigestV2(snapshot); digestErr == nil {
					digestMatches = oldDigest == legacyDigest && receipt.StageIndex == 0
				}
			}
			if oldStage != receipt.StageIndex || !digestMatches || contextErr != nil ||
				!equalDigest(oldContextCanonical, receiptContextCanonical) || !matchingJSONEqual([]byte(oldEdges), receipt.Edges) {
				return errors.New("conflicting fallback calculation receipt replay")
			}
			recorded = true
			return nil
		}
		_, err = tx.NewRaw(`INSERT INTO matching_fallback_calculation_receipt
			(run_id,acquisition_revision,stage_index,schema_version,calculator_version,digest_version,input_digest,context,edge_calculations)
			VALUES (?,?, ?,?,?,?, ?,?::jsonb,?::jsonb)`, runID, expectedRevision, snapshot.StageIndex,
			matchingFallbackCalculationSchemaVersion, matchingFallbackCalculationCalculatorVersion,
			matchingFallbackCalculationDigestVersion, digest, string(receipt.Context), string(receipt.Edges)).Exec(ctx)
		if err != nil {
			return fmt.Errorf("record fallback calculation receipt: insert: %w", err)
		}
		recorded = true
		return nil
	})
	return recorded, err
}
