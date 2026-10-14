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

type MatchingDeliveryClaim struct {
	ExecutionEpoch int64
	RiverAttempt   int
}

type MatchingDeliveryClaimOutcome int

const (
	MatchingDeliveryNotClaimed MatchingDeliveryClaimOutcome = iota
	MatchingDeliveryClaimed
	MatchingDeliveryBusy
)

// ClaimMatchingDelivery claims a registered current delivery only while the
// corresponding River job is running or retryable. Active work is fenced by
// the durable run epoch, never by elapsed wall-clock time.
func (repository *SetupManagerRepository) ClaimMatchingDelivery(ctx context.Context, operationID uuid.UUID, attempt int, riverJobID int64, riverAttempt int) (MatchingDeliveryClaim, MatchingDeliveryClaimOutcome, error) {
	var claim MatchingDeliveryClaim
	outcome := MatchingDeliveryNotClaimed
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var runPlan struct {
			SourceID     uuid.UUID  `bun:"provider_source_id"`
			Identity     string     `bun:"provider_configuration_identity"`
			FenceVersion int        `bun:"fence_version"`
			QuiescedAt   *time.Time `bun:"quiesced_at"`
		}
		if err := tx.NewRaw(`SELECT provider_source_id, provider_configuration_identity, fence_version, quiesced_at FROM matching_run WHERE id=?`, operationID).Scan(ctx, &runPlan); err != nil {
			return fmt.Errorf("claim matching delivery: read run: %w", err)
		}
		if runPlan.QuiescedAt != nil {
			return nil
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("claim matching delivery: lock source mutation gate: %w", err)
		}
		// Provider configuration follows the source mutation gate everywhere
		// that a matching delivery captures provider-source identity.
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("claim matching delivery: lock provider configuration: %w", err)
		}
		var sourceIdentity string
		if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=? FOR SHARE`, runPlan.SourceID).Scan(ctx, &sourceIdentity); err != nil {
			return fmt.Errorf("claim matching delivery: lock provider source: %w", err)
		}
		if sourceIdentity != runPlan.Identity {
			return nil
		}
		var lockedGroups []uuid.UUID
		if err := tx.NewRaw(`SELECT generation.group_id FROM incoming_matching_generation generation
			JOIN matching_run_target target ON target.group_id=generation.group_id AND target.run_id=?
			JOIN incoming_group current_group ON current_group.id=target.group_id
			ORDER BY generation.group_id FOR UPDATE OF generation, current_group`, operationID).Scan(ctx, &lockedGroups); err != nil {
			return fmt.Errorf("claim matching delivery: lock target groups and generations: %w", err)
		}
		var identity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &identity); err != nil {
			return fmt.Errorf("claim matching delivery: read provider configuration: %w", err)
		}
		if identity != runPlan.Identity {
			return nil
		}
		var stale bool
		switch runPlan.FenceVersion {
		case 2:
			if err := validateMatchingRunFence(ctx, tx, operationID, runPlan.FenceVersion); err != nil {
				return err
			}
			if err := tx.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM matching_run_target target
				LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
				LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
				WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
					generation.high_water IS DISTINCT FROM target.target_generation OR
					current_group.revision IS DISTINCT FROM target.captured_group_revision)
			)`, operationID).Scan(ctx, &stale); err != nil {
				return fmt.Errorf("claim matching delivery: validate target generations: %w", err)
			}
		case 1:
			if err := tx.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM matching_run_target target
				LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
				LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
				WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
					generation.high_water IS DISTINCT FROM target.target_generation OR
					current_group.revision IS DISTINCT FROM target.captured_group_revision)
			) OR EXISTS (SELECT 1 FROM incoming_grouping_state WHERE singleton=true AND needs_refresh=true)`, operationID).Scan(ctx, &stale); err != nil {
				return fmt.Errorf("claim matching delivery: validate target generations: %w", err)
			}
		default:
			return fmt.Errorf("claim matching delivery: unsupported fence version %d", runPlan.FenceVersion)
		}
		if stale {
			return nil
		}
		var river struct {
			State   string `bun:"state"`
			Attempt int    `bun:"attempt"`
		}
		if err := tx.NewRaw(`SELECT state, attempt FROM river_job WHERE id=? FOR UPDATE`, riverJobID).Scan(ctx, &river); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim matching delivery: lock River job: %w", err)
		}
		if river.State != "running" || riverAttempt < 1 || river.Attempt != riverAttempt {
			return nil
		}
		var registered bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_delivery WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL)`, operationID, riverJobID, attempt).Scan(ctx, &registered); err != nil {
			return fmt.Errorf("claim matching delivery: validate registration: %w", err)
		}
		if !registered {
			return nil
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if operation.Kind != "matching_evaluation_v1" || operation.Attempt != attempt || (operation.State != "queued" && operation.State != "running") {
			return nil
		}
		var run matchingClaimRun
		if err := tx.NewRaw(`SELECT state, execution_epoch, active_river_job_id, active_river_attempt,current_river_job_id,quiesced_at
					FROM matching_run WHERE id=? FOR UPDATE`, operationID).Scan(ctx, &run); err != nil {
			return fmt.Errorf("claim matching delivery: lock run: %w", err)
		}
		if run.QuiescedAt != nil {
			return nil
		}
		if run.State != "queued" && run.State != "running" && run.State != "waiting_provider" {
			return nil
		}
		var acquisitionPhase string
		if err := tx.NewRaw(`SELECT current_phase FROM matching_acquisition WHERE run_id=?`, operationID).Scan(ctx, &acquisitionPhase); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("claim matching delivery: read acquisition phase: %w", err)
		}
		if acquisitionPhase == MatchingAcquisitionFallbackEvaluationSealed &&
			(run.CurrentRiverJobID == nil || *run.CurrentRiverJobID != riverJobID) {
			return nil
		}
		if run.ActiveRiverJobID != nil && run.ActiveRiverAttempt != nil {
			var activeState string
			var activeAttempt int
			if err := tx.NewRaw(`SELECT state, attempt FROM river_job WHERE id=?`, *run.ActiveRiverJobID).Scan(ctx, &activeState, &activeAttempt); err == nil && activeState == "running" && activeAttempt == *run.ActiveRiverAttempt {
				outcome = MatchingDeliveryBusy
				return nil
			}
		}
		now := time.Now().UTC()
		claim.ExecutionEpoch = run.ExecutionEpoch + 1
		claim.RiverAttempt = riverAttempt
		if _, err := tx.NewRaw(`UPDATE matching_run SET state='running', execution_epoch=?, current_river_job_id=?,
			active_river_job_id=?, active_river_attempt=?, active_execution_epoch=?, updated_at=? WHERE id=?`, claim.ExecutionEpoch, riverJobID, riverJobID, river.Attempt, claim.ExecutionEpoch, now, operationID).Exec(ctx); err != nil {
			return fmt.Errorf("claim matching delivery: update run claim: %w", err)
		}
		operation.State, operation.RiverJobID, operation.UpdatedAt = "running", &riverJobID, now
		if operation.StartedAt == nil {
			operation.StartedAt = &now
		}
		if _, err := tx.NewUpdate().Model(operation).Column("state", "river_job_id", "started_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("claim matching delivery: update operation: %w", err)
		}
		outcome = MatchingDeliveryClaimed
		return nil
	})
	return claim, outcome, err
}

type matchingClaimRun struct {
	State              string     `bun:"state"`
	ExecutionEpoch     int64      `bun:"execution_epoch"`
	ActiveRiverJobID   *int64     `bun:"active_river_job_id"`
	ActiveRiverAttempt *int       `bun:"active_river_attempt"`
	CurrentRiverJobID  *int64     `bun:"current_river_job_id"`
	QuiescedAt         *time.Time `bun:"quiesced_at"`
}

// IsMatchingDeliveryCurrent checks whether an acquisition call still belongs to
// the active, unconsumed River delivery. It is intentionally read-only; the
// eventual write remains protected by its transactional delivery fence.
func (repository *SetupManagerRepository) IsMatchingDeliveryCurrent(ctx context.Context, operationID uuid.UUID, attempt int, riverJobID int64, claim MatchingDeliveryClaim) (bool, error) {
	var current bool
	err := repository.db.NewRaw(`SELECT EXISTS (
		SELECT 1
		FROM matching_run run
		JOIN operation ON operation.id=run.id
		JOIN matching_delivery delivery ON delivery.run_id=run.id
		JOIN river_job ON river_job.id=delivery.river_job_id
		WHERE run.id=? AND run.state='running'
			AND run.execution_epoch=? AND run.active_execution_epoch=?
			AND run.current_river_job_id=? AND run.active_river_job_id=? AND run.active_river_attempt=?
			AND operation.kind='matching_evaluation_v1' AND operation.state='running'
			AND operation.attempt=? AND operation.river_job_id=?
			AND delivery.operation_attempt=? AND delivery.river_job_id=? AND delivery.consumed_at IS NULL
			AND river_job.state='running' AND river_job.attempt=?
	)`, operationID, claim.ExecutionEpoch, claim.ExecutionEpoch, riverJobID, riverJobID, claim.RiverAttempt,
		attempt, riverJobID, attempt, riverJobID, claim.RiverAttempt).Scan(ctx, &current)
	if err != nil {
		return false, fmt.Errorf("check matching delivery: %w", err)
	}
	return current, nil
}

// ConsumePendingMatchingDelivery turns a claimed delivery into a durable wait
// when dependencies remain. Provider settlement always inserts its wake in the
// same transaction, so a wake racing this check remains independently claimable.
func (repository *SetupManagerRepository) ConsumePendingMatchingDelivery(ctx context.Context, operationID uuid.UUID, operationAttempt int, riverJobID int64, claim MatchingDeliveryClaim) (bool, error) {
	consumed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if operation.Kind != "matching_evaluation_v1" || operation.Attempt != operationAttempt || operation.State != "running" || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID {
			return nil
		}
		var run matchingClaimRun
		if err := tx.NewRaw(`SELECT state, execution_epoch, active_river_job_id, active_river_attempt FROM matching_run WHERE id=? FOR NO KEY UPDATE`, operationID).Scan(ctx, &run); err != nil {
			return fmt.Errorf("consume pending matching delivery: lock run: %w", err)
		}
		if run.State != "running" || run.ExecutionEpoch != claim.ExecutionEpoch || run.ActiveRiverJobID == nil || *run.ActiveRiverJobID != riverJobID || run.ActiveRiverAttempt == nil || *run.ActiveRiverAttempt != claim.RiverAttempt {
			return nil
		}
		var pending int
		if err := tx.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=? AND terminal_outcome IS NULL`, operationID).Scan(ctx, &pending); err != nil {
			return fmt.Errorf("consume pending matching delivery: check dependencies: %w", err)
		}
		if pending == 0 {
			return nil
		}
		now := time.Now().UTC()
		result, err := tx.NewRaw(`UPDATE matching_delivery SET consumed_at=? WHERE run_id=? AND river_job_id=? AND consumed_at IS NULL`, now, operationID, riverJobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("consume pending matching delivery: consume registration: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("consume pending matching delivery: delivery is no longer registered")
		}
		if _, err := tx.NewRaw(`UPDATE matching_run SET state='waiting_provider', active_river_job_id=NULL, active_river_attempt=NULL, active_execution_epoch=NULL, updated_at=? WHERE id=? AND execution_epoch=?`, now, operationID, claim.ExecutionEpoch).Exec(ctx); err != nil {
			return fmt.Errorf("consume pending matching delivery: park run: %w", err)
		}
		operation.Stage, operation.UpdatedAt = "waiting_provider", now
		if _, err := tx.NewUpdate().Model(operation).Column("stage", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("consume pending matching delivery: park operation: %w", err)
		}
		consumed = true
		return nil
	})
	return consumed, err
}

// FailMatchingDelivery marks an invalid admitted plan failed only while this
// River attempt still owns the matching execution fence.
func (repository *SetupManagerRepository) FailMatchingDelivery(ctx context.Context, operationID uuid.UUID, operationAttempt int, riverJobID int64, claim MatchingDeliveryClaim, safeMessage string) (bool, error) {
	if safeMessage == "" {
		return false, fmt.Errorf("fail matching delivery: safe message is required")
	}
	changed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var runPlan struct {
			SourceID     uuid.UUID `bun:"provider_source_id"`
			Identity     string    `bun:"provider_configuration_identity"`
			FenceVersion int       `bun:"fence_version"`
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("fail matching delivery: lock provider configuration: %w", err)
		}
		if err := tx.NewRaw(`SELECT provider_source_id, provider_configuration_identity, fence_version FROM matching_run WHERE id=?`, operationID).Scan(ctx, &runPlan); err != nil {
			return fmt.Errorf("fail matching delivery: read run identity: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("fail matching delivery: lock source mutation gate: %w", err)
		}
		var sourceIdentity string
		if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=? FOR SHARE`, runPlan.SourceID).Scan(ctx, &sourceIdentity); err != nil {
			return fmt.Errorf("fail matching delivery: lock provider source: %w", err)
		}
		if sourceIdentity != runPlan.Identity {
			return nil
		}
		var lockedGroups []uuid.UUID
		if err := tx.NewRaw(`SELECT generation.group_id FROM incoming_matching_generation generation
			JOIN matching_run_target target ON target.group_id=generation.group_id AND target.run_id=?
			JOIN incoming_group current_group ON current_group.id=target.group_id
			ORDER BY generation.group_id FOR UPDATE OF generation, current_group`, operationID).Scan(ctx, &lockedGroups); err != nil {
			return fmt.Errorf("fail matching delivery: lock target groups and generations: %w", err)
		}
		var configurationIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &configurationIdentity); err != nil {
			return fmt.Errorf("fail matching delivery: read provider configuration: %w", err)
		}
		if configurationIdentity != runPlan.Identity {
			return nil
		}
		var river struct {
			State   string `bun:"state"`
			Attempt int    `bun:"attempt"`
		}
		if err := tx.NewRaw(`SELECT state, attempt FROM river_job WHERE id=? FOR UPDATE`, riverJobID).Scan(ctx, &river); err != nil {
			return fmt.Errorf("fail matching delivery: lock River job: %w", err)
		}
		if river.State != "running" || river.Attempt != claim.RiverAttempt {
			return nil
		}
		var targets []struct {
			GroupID          uuid.UUID  `bun:"group_id"`
			HighWater        int64      `bun:"high_water"`
			CurrentRunID     *uuid.UUID `bun:"current_run_id"`
			TargetGeneration int64      `bun:"target_generation"`
			CurrentRevision  *string    `bun:"current_revision"`
			CapturedRevision string     `bun:"captured_group_revision"`
		}
		if err := tx.NewRaw(`SELECT generation.group_id, generation.high_water, generation.current_run_id,
			target.target_generation, current_group.revision AS current_revision, target.captured_group_revision
			FROM matching_run_target target
			JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
			JOIN incoming_group current_group ON current_group.id=target.group_id
			WHERE target.run_id=? ORDER BY generation.group_id`, operationID).Scan(ctx, &targets); err != nil {
			return fmt.Errorf("fail matching delivery: lock target generations: %w", err)
		}
		staleTargets := len(targets) == 0
		for _, target := range targets {
			if target.CurrentRunID == nil || *target.CurrentRunID != operationID || target.HighWater != target.TargetGeneration || target.CurrentRevision == nil || *target.CurrentRevision != target.CapturedRevision {
				staleTargets = true
				break
			}
		}
		switch runPlan.FenceVersion {
		case 1:
			var groupingNeedsRefresh bool
			if err := tx.NewRaw(`SELECT needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &groupingNeedsRefresh); err != nil {
				return fmt.Errorf("fail matching delivery: check grouping freshness: %w", err)
			}
			if groupingNeedsRefresh {
				staleTargets = true
			}
		case 2:
			if err := validateMatchingRunFence(ctx, tx, operationID, runPlan.FenceVersion); err != nil {
				return err
			}
		default:
			return fmt.Errorf("fail matching delivery: unsupported fence version %d", runPlan.FenceVersion)
		}
		if staleTargets {
			return nil
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if operation.Kind != "matching_evaluation_v1" || operation.Attempt != operationAttempt || operation.State != "running" || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID {
			return nil
		}
		var run matchingClaimRun
		if err := tx.NewRaw(`SELECT state, execution_epoch, active_river_job_id, active_river_attempt FROM matching_run WHERE id=? FOR UPDATE`, operationID).Scan(ctx, &run); err != nil {
			return fmt.Errorf("fail matching delivery: lock run: %w", err)
		}
		if run.State != "running" || run.ExecutionEpoch != claim.ExecutionEpoch || run.ActiveRiverJobID == nil || *run.ActiveRiverJobID != riverJobID || run.ActiveRiverAttempt == nil || *run.ActiveRiverAttempt != claim.RiverAttempt {
			return nil
		}
		var registered bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_delivery WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL)`, operationID, riverJobID, operationAttempt).Scan(ctx, &registered); err != nil {
			return fmt.Errorf("fail matching delivery: validate registration: %w", err)
		}
		if !registered {
			return nil
		}
		now := time.Now().UTC()
		operation.State, operation.Stage, operation.SafeError = "failed", "failed", &safeMessage
		operation.FinishedAt, operation.UpdatedAt = &now, now
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "safe_error", "finished_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("fail matching delivery: update operation: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE matching_run SET state='failed', active_river_job_id=NULL, active_river_attempt=NULL, active_execution_epoch=NULL, updated_at=? WHERE id=? AND execution_epoch=?`, now, operationID, claim.ExecutionEpoch).Exec(ctx); err != nil {
			return fmt.Errorf("fail matching delivery: update run: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE matching_delivery SET consumed_at=? WHERE run_id=? AND river_job_id=? AND consumed_at IS NULL`, now, operationID, riverJobID).Exec(ctx); err != nil {
			return fmt.Errorf("fail matching delivery: consume delivery: %w", err)
		}
		changed = true
		return nil
	})
	return changed, err
}
