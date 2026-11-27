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
	matchingFallbackCalculationSchemaVersion = 1
	// matchingFallbackCalculationDigestVersion tags the canonical input-digest
	// domain. It is not a persisted receipt field; the domain separation label
	// and SHA-256 algorithm are unchanged.
	matchingFallbackCalculationDigestVersion = 1
)

// MatchingFallbackCalculationReceipt is the immutable calculation handoff. The
// context is the full canonical loader snapshot; LayoutDecision is the complete
// compact whole-layout result that was validated against it. Both digests are
// stored so a reader can reject a changed context or decision without trusting
// either JSON payload's self-description.
type MatchingFallbackCalculationReceipt struct {
	RunID               uuid.UUID              `json:"run_id"`
	AcquisitionRevision int64                  `json:"acquisition_revision"`
	StageIndex          int                    `json:"stage_index"`
	SchemaVersion       int                    `json:"schema_version"`
	InputDigest         string                 `json:"input_digest"`
	Context             json.RawMessage        `json:"context"`
	LayoutDecision      MatchingLayoutDecision `json:"layout_decision"`
	DecisionDigest      string                 `json:"decision_digest"`
}

func supportedMatchingFallbackCalculationVersion(schemaVersion int) bool {
	return schemaVersion == matchingFallbackCalculationSchemaVersion
}

// GetMatchingFallbackCalculationReceipt returns immutable saved evidence.
func (repository *SetupManagerRepository) GetMatchingFallbackCalculationReceipt(
	ctx context.Context, runID uuid.UUID, acquisitionRevision int64,
) (*MatchingFallbackCalculationReceipt, error) {
	return getMatchingFallbackCalculationReceipt(ctx, repository.db, runID, acquisitionRevision)
}

func getMatchingFallbackCalculationReceipt(
	ctx context.Context, database bun.IDB, runID uuid.UUID, acquisitionRevision int64,
) (*MatchingFallbackCalculationReceipt, error) {
	if runID == uuid.Nil || acquisitionRevision < 1 {
		return nil, fmt.Errorf("read fallback calculation receipt: identity is required")
	}
	var receipt MatchingFallbackCalculationReceipt
	var contextJSON, decisionJSON string
	var schemaVersion int
	err := database.NewRaw(`SELECT receipt.run_id,receipt.acquisition_revision,receipt.stage_index,
		encode(receipt.input_digest,'hex'),receipt.context::text,receipt.layout_decision::text,
		encode(receipt.decision_digest,'hex'),
		receipt.schema_version
		FROM matching_fallback_calculation_receipt receipt
		WHERE receipt.run_id=? AND receipt.acquisition_revision=?`, runID, acquisitionRevision).
		Scan(ctx, &receipt.RunID, &receipt.AcquisitionRevision, &receipt.StageIndex, &receipt.InputDigest,
			&contextJSON, &decisionJSON, &receipt.DecisionDigest, &schemaVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read fallback calculation receipt: %w", err)
	}
	receipt.Context = json.RawMessage(contextJSON)
	receipt.SchemaVersion = schemaVersion
	if receipt.RunID != runID || receipt.AcquisitionRevision != acquisitionRevision || receipt.StageIndex < 0 ||
		!json.Valid(receipt.Context) || !json.Valid([]byte(decisionJSON)) ||
		json.Unmarshal([]byte(decisionJSON), &receipt.LayoutDecision) != nil {
		return nil, fmt.Errorf("read fallback calculation receipt: saved identity or evidence is invalid")
	}
	if !supportedMatchingFallbackCalculationVersion(schemaVersion) {
		return nil, fmt.Errorf("read fallback calculation receipt: stored schema version is unsupported")
	}
	decodedDigest, decodeErr := hex.DecodeString(receipt.InputDigest)
	if decodeErr != nil || len(decodedDigest) != sha256.Size || hex.EncodeToString(decodedDigest) != receipt.InputDigest {
		return nil, fmt.Errorf("read fallback calculation receipt: saved digest is invalid")
	}
	decodedDecisionDigest, decodeErr := hex.DecodeString(receipt.DecisionDigest)
	if decodeErr != nil || len(decodedDecisionDigest) != sha256.Size || hex.EncodeToString(decodedDecisionDigest) != receipt.DecisionDigest {
		return nil, fmt.Errorf("read fallback calculation receipt: saved decision digest is invalid")
	}
	calculatedDecisionDigest, err := receipt.LayoutDecision.Digest()
	if err != nil || calculatedDecisionDigest != receipt.DecisionDigest {
		return nil, fmt.Errorf("read fallback calculation receipt: saved layout decision digest is invalid")
	}
	var snapshot MatchingFallbackCalculationContext
	if err := json.Unmarshal(receipt.Context, &snapshot); err != nil || snapshot.RunID != runID ||
		snapshot.AcquisitionRevision != acquisitionRevision || snapshot.StageIndex != receipt.StageIndex {
		return nil, fmt.Errorf("read fallback calculation receipt: saved context identity is invalid")
	}
	calculatedDigest, err := MatchingFallbackCalculationInputDigest(snapshot)
	if err != nil || calculatedDigest != receipt.InputDigest {
		return nil, fmt.Errorf("read fallback calculation receipt: saved context digest is invalid")
	}
	evidence, err := DecodeFallbackLayoutStageEvidence(snapshot, snapshot.AcceptedShortlist)
	if err != nil {
		return nil, fmt.Errorf("read fallback calculation receipt: %w", err)
	}
	if err := evidence.ValidateDecision(receipt.LayoutDecision); err != nil {
		return nil, fmt.Errorf("read fallback calculation receipt: %w", err)
	}
	return &receipt, nil
}

// RecordMatchingFallbackCalculationForDelivery persists a calculation only
// while the exact running River delivery owns the current applicability phase.
// The context is reconstructed from the database under the writer's locks; the
// caller supplies only the input identity it read and the resulting decision.
// It deliberately does not consume or otherwise advance that delivery.
func (repository *SetupManagerRepository) RecordMatchingFallbackCalculationForDelivery(
	ctx context.Context, runID uuid.UUID, attempt int, riverJobID int64, claim MatchingDeliveryClaim,
	expectedRevision int64, expectedInputDigest string, decision MatchingLayoutDecision,
) (bool, error) {
	if runID == uuid.Nil || attempt < 1 || riverJobID < 1 || claim.ExecutionEpoch < 1 || claim.RiverAttempt < 1 ||
		expectedRevision < 1 {
		return false, fmt.Errorf("record fallback calculation receipt: invalid delivery identity")
	}
	digest, err := hex.DecodeString(expectedInputDigest)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != expectedInputDigest {
		return false, fmt.Errorf("record fallback calculation receipt: invalid input digest")
	}
	decisionJSON, err := decision.CanonicalJSON()
	if err != nil {
		return false, fmt.Errorf("record fallback calculation receipt: invalid layout decision: %w", err)
	}
	decisionDigest, err := decision.Digest()
	if err != nil {
		return false, fmt.Errorf("record fallback calculation receipt: invalid layout decision: %w", err)
	}
	decisionDigestBytes, err := hex.DecodeString(decisionDigest)
	if err != nil {
		return false, fmt.Errorf("record fallback calculation receipt: invalid decision digest")
	}

	recorded := false
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		// Keep the outer gate order aligned with other matching-delivery writers:
		// output admission, source mutations, tools readers, then configuration.
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("record fallback calculation receipt: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("record fallback calculation receipt: lock source mutations: %w", err)
		}
		if err := lockToolsMoveReaders(ctx, tx); err != nil {
			return fmt.Errorf("record fallback calculation receipt: lock tools-root readers: %w", err)
		}
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
		var configuredIdentity, sourceIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &configuredIdentity); err != nil {
			return fmt.Errorf("record fallback calculation receipt: read provider configuration: %w", err)
		}
		if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=? FOR SHARE`, runAuthority.SourceID).Scan(ctx, &sourceIdentity); err != nil {
			return fmt.Errorf("record fallback calculation receipt: lock source authority: %w", err)
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
		// Reconstruct the frozen context through this transaction after all writer
		// fences are held. The caller's digest is only a consistency check, and the
		// decision is validated against evidence this transaction read itself.
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
		if persistedDigest != expectedInputDigest {
			return errors.New("record fallback calculation receipt: supplied input digest differs from persisted authority")
		}
		evidence, err := DecodeFallbackLayoutStageEvidence(persisted, persisted.AcceptedShortlist)
		if err != nil {
			return fmt.Errorf("record fallback calculation receipt: %w", err)
		}
		if err := evidence.ValidateDecision(decision); err != nil {
			return fmt.Errorf("record fallback calculation receipt: %w", err)
		}
		persistedCanonical, err := canonicalMatchingFallbackCalculationContextBytes(persisted)
		if err != nil {
			return fmt.Errorf("record fallback calculation receipt: canonicalize persisted context: %w", err)
		}

		var priorReceipt bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM matching_fallback_calculation_receipt WHERE run_id=? AND acquisition_revision=?)`, runID, expectedRevision).Scan(ctx, &priorReceipt); err != nil {
			return fmt.Errorf("record fallback calculation receipt: check prior receipt: %w", err)
		}
		if priorReceipt {
			var oldDigest, oldContext, oldDecision, oldDecisionDigest string
			var oldStage, oldSchema int
			if err := tx.NewRaw(`SELECT encode(input_digest,'hex'),context::text,layout_decision::text,encode(decision_digest,'hex'),
				stage_index,schema_version
				FROM matching_fallback_calculation_receipt WHERE run_id=? AND acquisition_revision=? FOR UPDATE`, runID, expectedRevision).
				Scan(ctx, &oldDigest, &oldContext, &oldDecision, &oldDecisionDigest, &oldStage, &oldSchema); err != nil {
				return fmt.Errorf("record fallback calculation receipt: read prior receipt: %w", err)
			}
			if !supportedMatchingFallbackCalculationVersion(oldSchema) {
				return errors.New("record fallback calculation receipt: stored schema version is unsupported")
			}
			var oldSnapshot MatchingFallbackCalculationContext
			var oldContextCanonical []byte
			var contextErr error
			if json.Unmarshal([]byte(oldContext), &oldSnapshot) != nil {
				contextErr = errors.New("stored context is invalid")
			} else {
				oldContextCanonical, contextErr = canonicalMatchingFallbackCalculationContextBytes(oldSnapshot)
			}
			var oldLayoutDecision MatchingLayoutDecision
			var oldDecisionCanonical []byte
			var decisionErr error
			if json.Unmarshal([]byte(oldDecision), &oldLayoutDecision) != nil {
				decisionErr = errors.New("stored layout decision is invalid")
			} else {
				oldDecisionCanonical, decisionErr = oldLayoutDecision.CanonicalJSON()
			}
			if oldStage != persisted.StageIndex || oldDigest != expectedInputDigest || contextErr != nil ||
				!equalDigest(oldContextCanonical, persistedCanonical) || oldDecisionDigest != decisionDigest ||
				decisionErr != nil || !equalDigest(oldDecisionCanonical, decisionJSON) {
				return errors.New("conflicting fallback calculation receipt replay")
			}
			recorded = true
			return nil
		}
		contextJSON, err := json.Marshal(persisted)
		if err != nil {
			return fmt.Errorf("record fallback calculation receipt: serialize persisted context: %w", err)
		}
		_, err = tx.NewRaw(`INSERT INTO matching_fallback_calculation_receipt
			(run_id,acquisition_revision,stage_index,schema_version,input_digest,context,layout_decision,decision_digest)
			VALUES (?,?, ?,?, ?,?::jsonb,?::jsonb,?)`, runID, expectedRevision, persisted.StageIndex,
			matchingFallbackCalculationSchemaVersion, digest, string(contextJSON), string(decisionJSON), decisionDigestBytes).Exec(ctx)
		if err != nil {
			return fmt.Errorf("record fallback calculation receipt: insert: %w", err)
		}
		recorded = true
		return nil
	})
	return recorded, err
}
