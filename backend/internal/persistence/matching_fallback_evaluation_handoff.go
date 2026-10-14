package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const matchingFallbackEvaluationProposalAuthorityVersion = 3

const MatchingAcquisitionFallbackEvaluationSealed = "fallback_evaluation_sealed"

// MatchingFallbackEvaluationHandoff identifies the immutable seal and the
// durable ID-only wake registered with its atomic continuation.
type MatchingFallbackEvaluationHandoff struct {
	ProviderAuthorityVersion int       `json:"provider_authority_version"`
	RunID                    uuid.UUID `json:"run_id"`
	WaitRevision             int64     `json:"wait_revision"`
	SealDigest               string    `json:"seal_digest"`
	RiverJobID               int64     `json:"river_job_id"`
	OperationAttempt         int       `json:"operation_attempt"`
}

// ContinueMatchingFallbackSeal seals an exact parked fallback wait, advances
// its append-only phase cursor, and records one matching evaluation wake in the
// same transaction. It creates a preparation handoff only; publication and
// apply remain unavailable until their runtime is implemented.
func (repository *SetupManagerRepository) ContinueMatchingFallbackSeal(
	ctx context.Context,
	client RiverInserter,
	runID uuid.UUID,
	expectedOperationAttempt int,
	expectedWaitRevision int64,
) (MatchingFallbackEvaluationHandoff, bool, error) {
	var handoff MatchingFallbackEvaluationHandoff
	if repository == nil || client == nil || runID == uuid.Nil || expectedOperationAttempt < 1 || expectedWaitRevision < 1 {
		return handoff, false, fmt.Errorf("continue fallback seal: run, operation attempt, wait revision, and River client are required")
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		return repository.continueMatchingFallbackSealTx(ctx, tx, client, runID, expectedOperationAttempt, expectedWaitRevision, &handoff)
	})
	return handoff, err == nil, err
}

func (repository *SetupManagerRepository) continueMatchingFallbackSealTx(
	ctx context.Context, tx bun.Tx, client RiverInserter, runID uuid.UUID, expectedOperationAttempt int, expectedWaitRevision int64, handoff *MatchingFallbackEvaluationHandoff,
) error {
	if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
		return fmt.Errorf("continue fallback seal: lock output gate: %w", err)
	}
	var sourceFence struct {
		SourceID uuid.UUID `bun:"provider_source_id"`
		Identity string    `bun:"provider_configuration_identity"`
	}
	if err := tx.NewRaw(`SELECT provider_source_id,provider_configuration_identity FROM matching_run WHERE id=?`, runID).Scan(ctx, &sourceFence); err != nil {
		return fmt.Errorf("continue fallback seal: read provider source fence: %w", err)
	}
	if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
		return fmt.Errorf("continue fallback seal: lock source mutation gate: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
		return fmt.Errorf("continue fallback seal: lock provider configuration: %w", err)
	}
	operation, err := repository.GetOperationForUpdate(ctx, tx, runID)
	if err != nil {
		return err
	}
	var run struct {
		State         string     `bun:"state"`
		SourceID      uuid.UUID  `bun:"provider_source_id"`
		Identity      string     `bun:"provider_configuration_identity"`
		Fence         int        `bun:"fence_version"`
		Active        *int64     `bun:"active_river_job_id"`
		CurrentJob    *int64     `bun:"current_river_job_id"`
		Execution     int64      `bun:"execution_epoch"`
		ActiveEpoch   *int64     `bun:"active_execution_epoch"`
		ActiveAttempt *int       `bun:"active_river_attempt"`
		QuiescedAt    *time.Time `bun:"quiesced_at"`
	}
	if err := tx.NewRaw(`SELECT state,provider_source_id,provider_configuration_identity,fence_version,active_river_job_id,
			current_river_job_id,execution_epoch,active_execution_epoch,active_river_attempt,quiesced_at
			FROM matching_run WHERE id=? FOR UPDATE`, runID).Scan(ctx, &run); err != nil {
		return fmt.Errorf("continue fallback seal: read run: %w", err)
	}
	if run.QuiescedAt != nil {
		return fmt.Errorf("continue fallback seal: matching run is quiesced")
	}
	if operation.Kind != "matching_evaluation_v1" || operation.Attempt != expectedOperationAttempt {
		return fmt.Errorf("continue fallback seal: operation attempt changed")
	}
	if err := validateCurrentFallbackHandoffAuthority(ctx, tx, runID, run.SourceID, run.Identity, run.Fence); err != nil {
		return err
	}

	// An exact replay returns the original durable wake; it never creates a
	// replacement River delivery or rewrites the handoff tuple.
	var existing struct {
		Version  int    `bun:"provider_authority_version"`
		Digest   []byte `bun:"seal_digest"`
		JobID    int64  `bun:"river_job_id"`
		Attempt  int    `bun:"operation_attempt"`
		Revision int64  `bun:"wait_revision"`
	}
	err = tx.NewRaw(`SELECT provider_authority_version,seal_digest,river_job_id,operation_attempt,wait_revision
			FROM matching_fallback_evaluation_handoff WHERE run_id=? AND wait_revision=?`, runID, expectedWaitRevision).Scan(ctx, &existing)
	if err == nil {
		if existing.Version != matchingFallbackEvaluationProposalAuthorityVersion || existing.Attempt != expectedOperationAttempt ||
			existing.Revision != expectedWaitRevision || operation.RiverJobID == nil || *operation.RiverJobID != existing.JobID || existing.JobID < 1 ||
			len(existing.Digest) != sha256.Size {
			return fmt.Errorf("continue fallback seal: existing handoff conflicts with current operation")
		}
		var currentRevision int64
		var phase string
		if err := tx.NewRaw(`SELECT revision,current_phase FROM matching_acquisition WHERE run_id=?`, runID).Scan(ctx, &currentRevision, &phase); err != nil ||
			currentRevision != expectedWaitRevision+1 || phase != MatchingAcquisitionFallbackEvaluationSealed {
			return fmt.Errorf("continue fallback seal: existing handoff phase changed")
		}
		var sealDigest []byte
		if err := tx.NewRaw(`SELECT authority_digest FROM matching_fallback_authority_seal WHERE run_id=? AND wait_revision=?`, runID, expectedWaitRevision).Scan(ctx, &sealDigest); err != nil || !equalDigest(sealDigest, existing.Digest) {
			return fmt.Errorf("continue fallback seal: existing handoff seal identity changed")
		}
		var registered bool
		if err := tx.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM matching_delivery delivery
				WHERE delivery.run_id=? AND delivery.river_job_id=? AND delivery.operation_attempt=?
			)`, runID, existing.JobID, expectedOperationAttempt).Scan(ctx, &registered); err != nil || !registered {
			return fmt.Errorf("continue fallback seal: existing handoff wake is not registered")
		}
		var riverState string
		var riverAttempt int
		if err := tx.NewRaw(`SELECT state,attempt FROM river_job WHERE id=?`, existing.JobID).Scan(ctx, &riverState, &riverAttempt); err != nil {
			return fmt.Errorf("continue fallback seal: read existing handoff River job: %w", err)
		}
		queued := run.State == "queued" && run.Active == nil && run.ActiveEpoch == nil && run.ActiveAttempt == nil &&
			run.CurrentJob != nil && *run.CurrentJob == existing.JobID &&
			operation.State == "queued" && operation.Stage == "queued" && riverState == "available"
		claimed := run.State == "running" && run.Active != nil && *run.Active == existing.JobID && run.CurrentJob != nil &&
			*run.CurrentJob == existing.JobID && run.ActiveEpoch != nil && *run.ActiveEpoch == run.Execution &&
			run.ActiveAttempt != nil && *run.ActiveAttempt == riverAttempt && riverAttempt > 0 &&
			operation.State == "running" && riverState == "running"
		if !queued && !claimed {
			return fmt.Errorf("continue fallback seal: existing handoff conflicts with current operation")
		}
		*handoff = MatchingFallbackEvaluationHandoff{ProviderAuthorityVersion: existing.Version, RunID: runID,
			WaitRevision: existing.Revision, SealDigest: hex.EncodeToString(existing.Digest), RiverJobID: existing.JobID, OperationAttempt: existing.Attempt}
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("continue fallback seal: read existing handoff: %w", err)
	}
	if run.State != "waiting_policy" || run.Active != nil || run.ActiveEpoch != nil || run.ActiveAttempt != nil ||
		operation.State != "running" {
		return fmt.Errorf("continue fallback seal: matching run is not in its parked policy wait")
	}
	var currentRevision int64
	var currentPhase string
	if err := tx.NewRaw(`SELECT revision,current_phase FROM matching_acquisition WHERE run_id=? FOR UPDATE`, runID).Scan(ctx, &currentRevision, &currentPhase); err != nil || currentRevision != expectedWaitRevision {
		return fmt.Errorf("continue fallback seal: durable wait revision changed")
	}
	var pendingDependencies int
	if err := tx.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=? AND terminal_outcome IS NULL`, runID).Scan(ctx, &pendingDependencies); err != nil {
		return fmt.Errorf("continue fallback seal: read pending dependencies: %w", err)
	}
	var terminalDeliveryConsumed bool
	if run.CurrentJob == nil || operation.RiverJobID == nil || *operation.RiverJobID != *run.CurrentJob {
		return fmt.Errorf("continue fallback seal: terminal delivery identity changed")
	}
	if err := tx.NewRaw(`SELECT COALESCE((SELECT consumed_at IS NOT NULL FROM matching_delivery
			WHERE run_id=? AND river_job_id=? AND operation_attempt=?),false)`, runID, *run.CurrentJob, expectedOperationAttempt).
		Scan(ctx, &terminalDeliveryConsumed); err != nil {
		return fmt.Errorf("continue fallback seal: read terminal delivery: %w", err)
	}
	if pendingDependencies != 0 || !terminalDeliveryConsumed {
		return fmt.Errorf("continue fallback seal: durable wait still owns provider work")
	}
	authority, found, err := buildMatchingFallbackSealAuthority(ctx, tx, runID, expectedWaitRevision, false)
	if err != nil {
		return err
	}
	if !found || (authority.Disposition != "no_query" && authority.Disposition != "qualified" && authority.Disposition != "review_exhausted") {
		return fmt.Errorf("continue fallback seal: durable wait is not an eligible fallback result")
	}
	wantPhase, wantStage := "fallback_review_wait", "review_pending"
	if authority.Disposition == "qualified" {
		wantPhase, wantStage = "pending_seal", "pending_seal"
	}
	if currentPhase != wantPhase || operation.Stage != wantStage {
		return fmt.Errorf("continue fallback seal: parked phase does not match fallback disposition")
	}
	if err := persistMatchingFallbackAuthoritySeal(ctx, tx, authority); err != nil {
		return err
	}
	digest, err := hex.DecodeString(authority.Digest)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("continue fallback seal: derived seal digest is invalid")
	}
	phaseInput, _ := json.Marshal(struct {
		WaitRevision int64  `json:"wait_revision"`
		SealDigest   string `json:"seal_digest"`
	}{expectedWaitRevision, authority.Digest})
	phaseOutput, _ := json.Marshal(struct {
		State                    string `json:"state"`
		ProviderAuthorityVersion int    `json:"provider_authority_version"`
	}{MatchingAcquisitionFallbackEvaluationSealed, matchingFallbackEvaluationProposalAuthorityVersion})
	if _, err := tx.NewRaw(`INSERT INTO matching_acquisition_phase(run_id,revision,phase,input,output,response_provenance)
			VALUES (?,?,?,?::jsonb,?::jsonb,'[]'::jsonb)`, runID, expectedWaitRevision+1,
		MatchingAcquisitionFallbackEvaluationSealed, string(phaseInput), string(phaseOutput)).Exec(ctx); err != nil {
		return fmt.Errorf("continue fallback seal: append evaluation handoff phase: %w", err)
	}
	result, err := tx.NewRaw(`UPDATE matching_acquisition SET current_phase=?,revision=?
			WHERE run_id=? AND revision=?`, MatchingAcquisitionFallbackEvaluationSealed, expectedWaitRevision+1, runID, expectedWaitRevision).Exec(ctx)
	if err != nil {
		return fmt.Errorf("continue fallback seal: advance acquisition cursor: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return fmt.Errorf("continue fallback seal: acquisition cursor changed before advancement")
	}
	inserted, err := client.InsertTx(ctx, tx.Tx, MatchingEvaluationWakeArgs{OperationID: runID}, nil)
	if err != nil || inserted == nil || inserted.Job == nil || inserted.Job.ID < 1 {
		if err == nil {
			err = fmt.Errorf("river returned no registered job identity")
		}
		return fmt.Errorf("continue fallback seal: enqueue matching evaluation wake: %w", err)
	}
	jobID := inserted.Job.ID
	if _, err := tx.NewRaw(`INSERT INTO matching_delivery(run_id,river_job_id,operation_attempt) VALUES (?,?,?)`, runID, jobID, expectedOperationAttempt).Exec(ctx); err != nil {
		return fmt.Errorf("continue fallback seal: register matching delivery: %w", err)
	}
	if _, err := tx.NewRaw(`INSERT INTO matching_fallback_evaluation_handoff
			(run_id,wait_revision,provider_authority_version,seal_digest,river_job_id,operation_attempt)
			VALUES (?,?,3,?,?,?)`, runID, expectedWaitRevision, digest, jobID, expectedOperationAttempt).Exec(ctx); err != nil {
		return fmt.Errorf("continue fallback seal: persist evaluation handoff: %w", err)
	}
	now := time.Now().UTC()
	if _, err := tx.NewRaw(`UPDATE matching_run SET state='queued',current_river_job_id=?,active_river_job_id=NULL,active_river_attempt=NULL,
			active_execution_epoch=NULL,updated_at=? WHERE id=?`, jobID, now, runID).Exec(ctx); err != nil {
		return fmt.Errorf("continue fallback seal: queue matching run: %w", err)
	}
	operation.State, operation.Stage, operation.RiverJobID, operation.UpdatedAt = "queued", "queued", &jobID, now
	if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "river_job_id", "updated_at").WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("continue fallback seal: queue matching operation: %w", err)
	}
	*handoff = MatchingFallbackEvaluationHandoff{ProviderAuthorityVersion: matchingFallbackEvaluationProposalAuthorityVersion,
		RunID: runID, WaitRevision: expectedWaitRevision, SealDigest: authority.Digest, RiverJobID: jobID, OperationAttempt: expectedOperationAttempt}
	return nil
}

// validateCurrentFallbackHandoffAuthority is deliberately run before either a
// new continuation or an idempotent replay. A recorded wake cannot authorize a
// handoff after its provider configuration or captured target has gone stale.
func validateCurrentFallbackHandoffAuthority(ctx context.Context, tx bun.Tx, runID, sourceID uuid.UUID, identity string, fence int) error {
	var configIdentity, sourceIdentity string
	if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &configIdentity); err != nil || configIdentity != identity {
		return fmt.Errorf("continue fallback seal: provider configuration changed")
	}
	if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=? FOR SHARE`, sourceID).Scan(ctx, &sourceIdentity); err != nil || sourceIdentity != identity {
		return fmt.Errorf("continue fallback seal: provider source changed")
	}
	if fence != 2 {
		return fmt.Errorf("continue fallback seal: unsupported target fence version %d", fence)
	}
	var groups []uuid.UUID
	if err := tx.NewRaw(`SELECT generation.group_id FROM incoming_matching_generation generation
		JOIN matching_run_target target ON target.group_id=generation.group_id AND target.run_id=?
		JOIN incoming_group current_group ON current_group.id=target.group_id
		ORDER BY generation.group_id FOR UPDATE OF generation,current_group`, runID).Scan(ctx, &groups); err != nil {
		return fmt.Errorf("continue fallback seal: lock target generations: %w", err)
	}
	for _, groupID := range groups {
		authority, err := lockIncomingGroupMatchingAuthority(ctx, tx, groupID)
		if err != nil {
			return err
		}
		var expectedAuthorityRevision int64
		if err := tx.NewRaw(`SELECT authority_revision FROM matching_run_target WHERE run_id=? AND group_id=?`, runID, groupID).Scan(ctx, &expectedAuthorityRevision); err != nil ||
			authority.Mode != "automatic" || authority.Revision != expectedAuthorityRevision {
			return fmt.Errorf("continue fallback seal: current group authority changed")
		}
	}
	if err := validateMatchingRunFence(ctx, tx, runID, fence); err != nil {
		return err
	}
	var stale bool
	if err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM matching_run_target target
		LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
		LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
		LEFT JOIN incoming_group_matching_authority authority ON authority.group_id=target.group_id
		WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
			generation.high_water IS DISTINCT FROM target.target_generation OR
			current_group.revision IS DISTINCT FROM target.captured_group_revision OR
			COALESCE(authority.mode,'automatic') <> 'automatic' OR
			COALESCE(authority.revision,0) <> target.authority_revision))`, runID).Scan(ctx, &stale); err != nil {
		return fmt.Errorf("continue fallback seal: validate current target: %w", err)
	}
	if stale {
		return fmt.Errorf("continue fallback seal: current target authority changed")
	}
	return nil
}
