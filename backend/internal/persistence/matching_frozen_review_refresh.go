package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// FrozenMatchingReviewRefreshCandidate identifies one current automatic frozen
// artistless review whose sealed provider history or frozen threshold can be
// rechecked without scoring or provider requests.
type FrozenMatchingReviewRefreshCandidate struct {
	GroupID            uuid.UUID
	RunID              uuid.UUID
	EvaluationRevision string
	WaitRevision       int64
}

// ListFrozenMatchingReviewRefreshCandidates returns one page of published
// frozen artistless automatic reviews in ascending group order. The keyset
// cursor is the last group ID from the previous page.
func (repository *SetupManagerRepository) ListFrozenMatchingReviewRefreshCandidates(ctx context.Context, after uuid.UUID, limit int) ([]FrozenMatchingReviewRefreshCandidate, error) {
	if limit < 1 || limit > 256 {
		return nil, fmt.Errorf("list frozen matching review refresh candidates: limit must be between 1 and 256")
	}
	var rows []struct {
		GroupID            uuid.UUID `bun:"group_id"`
		RunID              uuid.UUID `bun:"run_id"`
		EvaluationRevision string    `bun:"evaluation_revision"`
		WaitRevision       int64     `bun:"frozen_wait_revision"`
	}
	if err := repository.db.NewRaw(`SELECT evaluation.group_id, evaluation.target_run_id AS run_id,
			evaluation.evaluation_revision, evaluation.frozen_wait_revision
		FROM incoming_match_evaluation evaluation
		JOIN matching_run run ON run.id=evaluation.target_run_id
		JOIN matching_run_target target ON target.run_id=run.id AND target.group_id=evaluation.group_id
		JOIN incoming_matching_generation generation ON generation.group_id=evaluation.group_id
		JOIN incoming_group current_group ON current_group.id=evaluation.group_id
		JOIN provider_source source ON source.id=run.provider_source_id
		JOIN app_setting config ON config.setting_name='musicbrainz_config_identity'
			AND config.setting_value=run.provider_configuration_identity
		LEFT JOIN incoming_group_matching_authority authority ON authority.group_id=evaluation.group_id
		WHERE evaluation.group_id > ?
			AND evaluation.evaluation_revision IS NOT NULL
			AND evaluation.frozen_authority_version=3
			AND evaluation.frozen_run_id=evaluation.target_run_id
			AND evaluation.frozen_wait_revision > 0
			AND evaluation.frozen_seal_digest IS NOT NULL
			AND evaluation.fence_version=2
			AND evaluation.target_run_id IS NOT NULL
			AND evaluation.matching_authority_mode='automatic'
			AND evaluation.matching_authority_revision=target.authority_revision
			AND evaluation.group_revision=target.captured_group_revision
			AND evaluation.provider_source_id=run.provider_source_id
			AND evaluation.provider_configuration_identity=run.provider_configuration_identity
			AND evaluation.evaluation_evidence->'frozen_publication'->'authority'->>'seal_disposition'
				IN ('review_exhausted','review_indeterminate')
			AND run.fence_version=2
			AND run.state='succeeded'
			AND run.quiesced_at IS NULL
			AND run.active_river_job_id IS NULL
			AND run.immutable_plan->>'schema_version'='5'
			AND run.immutable_plan->>'intent'='automatic_fallback_acquisition'
			AND run.immutable_plan->>'mode'='automatic'
			AND target.target_capture IS NOT NULL
			AND generation.current_run_id=run.id
			AND generation.high_water=target.target_generation
			AND generation.requested_generation=generation.reconciled_generation
			AND (target.reconciliation_generation IS NULL OR generation.requested_generation=target.reconciliation_generation)
			AND current_group.revision=target.captured_group_revision
			AND COALESCE(authority.mode,'automatic')='automatic'
			AND COALESCE(authority.revision,0)=target.authority_revision
			AND NOT EXISTS (SELECT 1 FROM incoming_grouping_state state WHERE state.singleton=true AND state.needs_refresh=true)
			AND NOT EXISTS (
				SELECT 1 FROM matching_delivery delivery JOIN river_job job ON job.id=delivery.river_job_id
				WHERE delivery.run_id=run.id AND delivery.consumed_at IS NULL AND job.finalized_at IS NULL
			)
		ORDER BY evaluation.group_id
		LIMIT ?`, after, limit).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list frozen matching review refresh candidates: %w", err)
	}
	candidates := make([]FrozenMatchingReviewRefreshCandidate, 0, len(rows))
	for _, row := range rows {
		candidates = append(candidates, FrozenMatchingReviewRefreshCandidate{
			GroupID: row.GroupID, RunID: row.RunID,
			EvaluationRevision: row.EvaluationRevision, WaitRevision: row.WaitRevision,
		})
	}
	return candidates, nil
}

// RefreshFrozenMatchingReview requests one current-state re-evaluation when a
// published frozen review's accepted provider evidence changed semantically or
// its frozen threshold differs from the effective one. It performs no scoring
// and no provider request, never edits the published review, and uses the same
// gate order as the frozen publisher.
func (repository *SetupManagerRepository) RefreshFrozenMatchingReview(ctx context.Context, candidate FrozenMatchingReviewRefreshCandidate) (bool, error) {
	if candidate.GroupID == uuid.Nil || candidate.RunID == uuid.Nil || candidate.EvaluationRevision == "" || candidate.WaitRevision < 1 {
		return false, fmt.Errorf("refresh frozen matching review: candidate identity is incomplete")
	}
	changed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("refresh frozen matching review: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("refresh frozen matching review: lock source mutations: %w", err)
		}
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
			return fmt.Errorf("refresh frozen matching review: lock MusicBrainz configuration: %w", err)
		}
		var currentIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentIdentity); err != nil {
			return fmt.Errorf("refresh frozen matching review: read current MusicBrainz configuration: %w", err)
		}
		seal, found, err := readMatchingFallbackAuthoritySeal(ctx, tx, candidate.RunID, candidate.WaitRevision)
		if err != nil {
			return fmt.Errorf("refresh frozen matching review: read authority seal: %w", err)
		}
		if !found || seal.RunID != candidate.RunID || seal.WaitRevision != candidate.WaitRevision || seal.Target.GroupID != candidate.GroupID {
			return nil
		}
		// Qualified decisions are already applied and no-query reviews have no
		// provider history or threshold-derived layout to refresh.
		if seal.Disposition != "review_exhausted" && seal.Disposition != "review_indeterminate" {
			return nil
		}
		drifted := currentIdentity != seal.SourceConfiguration
		if !drifted {
			if err := recheckFrozenFallbackProviderHistory(ctx, tx, seal); err != nil {
				if !errors.Is(err, ErrIncomingMatchConflict) {
					return fmt.Errorf("refresh frozen matching review: recheck provider history: %w", err)
				}
				drifted = true
			}
		}
		if !drifted {
			currentThreshold, err := matchingLayoutTargetPairThreshold(ctx, tx)
			if err != nil {
				return fmt.Errorf("refresh frozen matching review: read current threshold: %w", err)
			}
			_, frozenThreshold, err := DecodeMatchingLayoutTarget(seal.Intent.LayoutTarget, seal.Target.GroupID, MatchingLayoutPolicyVersion)
			if err != nil {
				return fmt.Errorf("refresh frozen matching review: decode frozen threshold: %w", err)
			}
			canonicalFrozen, err := CanonicalMatchingLayoutThreshold(frozenThreshold)
			if err != nil {
				return fmt.Errorf("refresh frozen matching review: canonicalize frozen threshold: %w", err)
			}
			drifted = canonicalFrozen != currentThreshold
		}
		if !drifted {
			return nil
		}
		current, err := validateFrozenMatchingReviewRefreshCurrent(ctx, tx, candidate, seal)
		if err != nil {
			return err
		}
		if !current {
			return nil
		}
		if err := RequestIncomingMatchingReconciliationWith(ctx, tx, []uuid.UUID{candidate.GroupID}); err != nil {
			return fmt.Errorf("refresh frozen matching review: request reconciliation: %w", err)
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

func validateFrozenMatchingReviewRefreshCurrent(ctx context.Context, tx bun.Tx, candidate FrozenMatchingReviewRefreshCandidate, seal MatchingFallbackSealAuthority) (bool, error) {
	if err := validateMatchingRunFenceCurrentTarget(ctx, tx, candidate.RunID); err != nil {
		if errors.Is(err, ErrIncomingMatchConflict) || errors.Is(err, ErrMatchingTargetNeedsRefresh) {
			return false, nil
		}
		return false, fmt.Errorf("refresh frozen matching review: validate current target: %w", err)
	}
	var target struct {
		TargetGeneration         int64  `bun:"target_generation"`
		ReconciliationGeneration *int64 `bun:"reconciliation_generation"`
	}
	if err := tx.NewRaw(`SELECT target_generation,reconciliation_generation FROM matching_run_target WHERE run_id=? AND group_id=?`,
		candidate.RunID, candidate.GroupID).Scan(ctx, &target); err != nil {
		return false, fmt.Errorf("refresh frozen matching review: read target generation: %w", err)
	}
	var generation struct {
		CurrentRunID *uuid.UUID `bun:"current_run_id"`
		HighWater    int64      `bun:"high_water"`
		Requested    int64      `bun:"requested_generation"`
		Reconciled   int64      `bun:"reconciled_generation"`
	}
	if err := tx.NewRaw(`SELECT current_run_id,high_water,requested_generation,reconciled_generation
		FROM incoming_matching_generation WHERE group_id=? FOR UPDATE`, candidate.GroupID).Scan(ctx, &generation); err != nil {
		return false, fmt.Errorf("refresh frozen matching review: lock current generation: %w", err)
	}
	if generation.CurrentRunID == nil || *generation.CurrentRunID != candidate.RunID ||
		generation.HighWater != target.TargetGeneration ||
		generation.Requested != generation.Reconciled ||
		(target.ReconciliationGeneration != nil && generation.Requested != *target.ReconciliationGeneration) {
		return false, nil
	}
	var run struct {
		State         string     `bun:"state"`
		QuiescedAt    *time.Time `bun:"quiesced_at"`
		ActiveJobID   *int64     `bun:"active_river_job_id"`
		SourceID      uuid.UUID  `bun:"provider_source_id"`
		Configuration string     `bun:"provider_configuration_identity"`
		FenceVersion  int        `bun:"fence_version"`
	}
	if err := tx.NewRaw(`SELECT state,quiesced_at,active_river_job_id,provider_source_id,provider_configuration_identity,fence_version
		FROM matching_run WHERE id=? FOR SHARE`, candidate.RunID).Scan(ctx, &run); err != nil {
		return false, fmt.Errorf("refresh frozen matching review: read run state: %w", err)
	}
	if run.State != "succeeded" || run.QuiescedAt != nil || run.ActiveJobID != nil || run.FenceVersion != 2 ||
		run.SourceID != seal.SourceID || run.Configuration != seal.SourceConfiguration {
		return false, nil
	}
	var header struct {
		Revision              string     `bun:"evaluation_revision"`
		FrozenAuthority       *int       `bun:"frozen_authority_version"`
		FrozenRunID           *uuid.UUID `bun:"frozen_run_id"`
		FrozenWaitRevision    *int64     `bun:"frozen_wait_revision"`
		FrozenSealDigest      []byte     `bun:"frozen_seal_digest"`
		TargetRunID           *uuid.UUID `bun:"target_run_id"`
		FenceVersion          int        `bun:"fence_version"`
		AuthorityMode         string     `bun:"matching_authority_mode"`
		AuthorityRevision     int64      `bun:"matching_authority_revision"`
		GroupRevision         string     `bun:"group_revision"`
		ProviderSourceID      uuid.UUID  `bun:"provider_source_id"`
		ProviderConfiguration string     `bun:"provider_configuration_identity"`
	}
	if err := tx.NewRaw(`SELECT evaluation_revision,frozen_authority_version,frozen_run_id,frozen_wait_revision,frozen_seal_digest,
			target_run_id,fence_version,matching_authority_mode,matching_authority_revision,group_revision,
			provider_source_id,provider_configuration_identity
		FROM incoming_match_evaluation WHERE group_id=? FOR UPDATE`, candidate.GroupID).Scan(ctx, &header); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("refresh frozen matching review: read current evaluation: %w", err)
	}
	if header.Revision != candidate.EvaluationRevision || header.FrozenAuthority == nil || *header.FrozenAuthority != 3 ||
		header.FrozenRunID == nil || *header.FrozenRunID != candidate.RunID ||
		header.FrozenWaitRevision == nil || *header.FrozenWaitRevision != candidate.WaitRevision ||
		!equalDigest(header.FrozenSealDigest, mustDecodeSHA256(seal.Digest)) ||
		header.TargetRunID == nil || *header.TargetRunID != candidate.RunID || header.FenceVersion != 2 ||
		header.AuthorityMode != "automatic" || header.AuthorityRevision != seal.GroupAuthority.Revision ||
		header.GroupRevision != seal.Target.GroupRevision || header.ProviderSourceID != seal.SourceID ||
		header.ProviderConfiguration != seal.SourceConfiguration {
		return false, nil
	}
	return true, nil
}
