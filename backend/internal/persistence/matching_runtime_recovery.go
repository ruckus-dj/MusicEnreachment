package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type matchingRuntimeRecoveryCandidate struct {
	RunID      uuid.UUID `bun:"run_id"`
	Attempt    int       `bun:"operation_attempt"`
	Revision   int64     `bun:"revision"`
	Phase      string    `bun:"current_phase"`
	CurrentJob *int64    `bun:"current_river_job_id"`
}

// RecoverMatchingRuntime repairs only eligible automatic fallback waits and
// sealed evaluation wakes. It deliberately does not archive or fail stale work.
func (repository *SetupManagerRepository) RecoverMatchingRuntime(ctx context.Context, client RiverInserter) error {
	if client == nil {
		client = repository.riverInserter
	}
	if client == nil {
		return fmt.Errorf("recover matching runtime: River client is required")
	}
	if err := repository.QuiesceObsoleteAutomaticMatchingRuns(ctx); err != nil {
		return fmt.Errorf("recover matching runtime: quiesce obsolete automatic runs: %w", err)
	}
	var candidates []matchingRuntimeRecoveryCandidate
	err := repository.db.NewRaw(`
		SELECT run.id AS run_id, operation.attempt AS operation_attempt, acquisition.revision,
			acquisition.current_phase, run.current_river_job_id
		FROM matching_run run
		JOIN operation ON operation.id=run.operation_id
			JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
			LEFT JOIN river_job job ON job.id=run.current_river_job_id
			WHERE operation.kind='matching_evaluation_v1' AND run.quiesced_at IS NULL
			AND (
				(run.state='waiting_policy' AND operation.state='running'
				 AND acquisition.current_phase IN ('pending_seal','fallback_review_wait')
				 AND NOT EXISTS (SELECT 1 FROM matching_provider_dependency dependency
					WHERE dependency.parent_run_id=run.id AND dependency.terminal_outcome IS NULL)
				 AND EXISTS (SELECT 1 FROM matching_delivery delivery
					WHERE delivery.run_id=run.id AND delivery.river_job_id=run.current_river_job_id
					AND delivery.operation_attempt=operation.attempt AND delivery.consumed_at IS NOT NULL))
				OR (acquisition.current_phase='fallback_evaluation_sealed'
				 AND operation.state IN ('queued','running')
				 AND (job.id IS NULL OR job.state IN ('completed','cancelled','discarded')))
			)
		ORDER BY run.id`).Scan(ctx, &candidates)
	if err != nil {
		return fmt.Errorf("recover matching runtime: list candidates: %w", err)
	}
	for _, candidate := range candidates {
		if candidate.Phase == MatchingAcquisitionFallbackEvaluationSealed {
			if _, err := repository.recoverSealedMatchingWake(ctx, client, candidate); err != nil {
				return fmt.Errorf("recover matching runtime: recover sealed fallback wake: %w", err)
			}
			continue
		}
		eligible, err := repository.matchingFallbackWaitStillEligible(ctx, candidate)
		if err != nil {
			return fmt.Errorf("recover matching runtime: check fallback wait freshness: %w", err)
		}
		if !eligible {
			continue
		}
		// Freshness is explicitly checked above. Any continuation failure after
		// that check is an unexpected persistence/integrity failure and must not be
		// hidden as an ineligible candidate.
		if _, _, err := repository.ContinueMatchingFallbackSeal(ctx, client, candidate.RunID, candidate.Attempt, candidate.Revision); err != nil {
			return fmt.Errorf("recover matching runtime: continue fallback seal: %w", err)
		}
	}
	return nil
}

// QuiesceObsoleteAutomaticMatchingRuns installs an internal execution fence
// without changing operation/run status or completion timestamps. The proof is
// deliberately limited to current-state automatic intent generations 2 and 3;
// malformed, dirty, manually-owned, or merely old runs are not classified.
func (repository *SetupManagerRepository) QuiesceObsoleteAutomaticMatchingRuns(ctx context.Context) error {
	var candidates []uuid.UUID
	if err := repository.db.NewRaw(`SELECT run.id
		FROM matching_run run
		JOIN operation parent ON parent.id=run.operation_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		JOIN matching_run_target target ON target.run_id=run.id
		JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
		WHERE acquisition.intent_version IN (2,3)
		  AND parent.kind='matching_evaluation_v1' AND parent.state IN ('queued','running','failed')
		  AND run.state IN ('queued','running','waiting_provider') AND run.quiesced_at IS NULL
		  AND target.reconciliation_generation IS NOT NULL
		  AND generation.requested_generation > target.reconciliation_generation
		  AND generation.high_water > target.target_generation
		  AND generation.current_run_id IS DISTINCT FROM run.id
		ORDER BY run.id`).Scan(ctx, &candidates); err != nil {
		return fmt.Errorf("list potentially obsolete automatic matching runs: %w", err)
	}
	for _, runID := range candidates {
		if err := repository.quiesceObsoleteAutomaticMatchingRun(ctx, runID); err != nil {
			return err
		}
	}
	return nil
}

func (repository *SetupManagerRepository) quiesceObsoleteAutomaticMatchingRun(ctx context.Context, runID uuid.UUID) error {
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("lock source mutation gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
			return fmt.Errorf("lock provider configuration: %w", err)
		}
		var target struct {
			GroupID uuid.UUID `bun:"group_id"`
		}
		if err := tx.NewRaw(`SELECT target.group_id FROM matching_run_target target WHERE target.run_id=?`, runID).Scan(ctx, &target); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("read obsolete matching target: %w", err)
		}
		var locked uuid.UUID
		if err := tx.NewRaw(`SELECT group_id FROM incoming_matching_generation WHERE group_id=? FOR UPDATE`, target.GroupID).Scan(ctx, &locked); err != nil {
			return fmt.Errorf("lock obsolete matching request generation: %w", err)
		}
		// Delivery consumption locks the parent operation before the matching run.
		// Keep the same order explicitly; a joined FOR UPDATE leaves row-lock order
		// to the planner and can deadlock against that consumer.
		var lockedParent uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM operation WHERE id=(SELECT operation_id FROM matching_run WHERE id=?) FOR UPDATE`, runID).Scan(ctx, &lockedParent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("lock obsolete matching parent operation: %w", err)
		}
		var lockedRun uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM matching_run WHERE id=? FOR UPDATE`, runID).Scan(ctx, &lockedRun); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("lock obsolete matching run: %w", err)
		}
		var proof struct {
			IntentVersion           int        `bun:"intent_version"`
			Mode                    string     `bun:"mode"`
			Request                 int64      `bun:"reconciliation_generation"`
			TargetHighWater         int64      `bun:"target_generation"`
			Requested               int64      `bun:"requested_generation"`
			HighWater               int64      `bun:"high_water"`
			CurrentRun              *uuid.UUID `bun:"current_run_id"`
			State                   string     `bun:"state"`
			OperationState          string     `bun:"operation_state"`
			ActiveJob               *int64     `bun:"active_river_job_id"`
			ActiveAttempt           *int       `bun:"active_river_attempt"`
			ActiveEpoch             *int64     `bun:"active_execution_epoch"`
			QuiescedAt              *time.Time `bun:"quiesced_at"`
			ProviderSourceID        uuid.UUID  `bun:"provider_source_id"`
			ProviderIdentity        string     `bun:"provider_configuration_identity"`
			CurrentProviderIdentity string     `bun:"current_provider_identity"`
			SourceIdentity          string     `bun:"source_identity"`
		}
		if err := tx.NewRaw(`SELECT acquisition.intent_version,
			run.immutable_plan->>'mode' AS mode,
			target.reconciliation_generation, target.target_generation, generation.requested_generation, generation.high_water,
			generation.current_run_id, run.state, parent.state AS operation_state, run.quiesced_at,
			run.active_river_job_id,run.active_river_attempt,run.active_execution_epoch,
			run.provider_source_id, run.provider_configuration_identity,
				(SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity') AS current_provider_identity,
				(SELECT configuration_identity FROM provider_source WHERE id=run.provider_source_id) AS source_identity
				FROM matching_run run JOIN operation parent ON parent.id=run.operation_id
				JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
				JOIN matching_run_target target ON target.run_id=run.id AND target.group_id=?
				JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
				WHERE run.id=?`, target.GroupID, runID).Scan(ctx, &proof); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("read obsolete matching proof: %w", err)
		}
		if proof.QuiescedAt != nil || (proof.IntentVersion != 2 && proof.IntentVersion != 3) || proof.Mode != "automatic" ||
			proof.Request <= 0 || proof.Requested <= proof.Request || proof.HighWater <= proof.TargetHighWater || proof.CurrentRun != nil && *proof.CurrentRun == runID ||
			proof.ProviderIdentity == "" || proof.ProviderIdentity != proof.CurrentProviderIdentity || proof.ProviderIdentity != proof.SourceIdentity ||
			(proof.State != "queued" && proof.State != "running" && proof.State != "waiting_provider") ||
			(proof.OperationState != "queued" && proof.OperationState != "running" && proof.OperationState != "failed") {
			return nil
		}
		if proof.ActiveJob != nil || proof.ActiveAttempt != nil || proof.ActiveEpoch != nil {
			return nil
		}
		var dirty bool
		if err := tx.NewRaw(`SELECT needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &dirty); err != nil {
			return fmt.Errorf("read grouping freshness for obsolete matching proof: %w", err)
		}
		if dirty {
			return nil
		}
		if err := validateMatchingRunFenceForObsolescence(ctx, tx, runID); err != nil {
			// A stale request is tolerated by the proof validator; any other failure
			// is ambiguity or infrastructure failure and must never classify obsolete.
			return nil
		}
		deliveriesValid, err := cancelObsoleteMatchingDeliveries(ctx, tx, runID)
		if err != nil {
			return err
		}
		if !deliveriesValid {
			return nil
		}
		var liveJobs bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM matching_delivery delivery JOIN river_job job ON job.id=delivery.river_job_id
			WHERE delivery.run_id=? AND job.state IN ('available','pending','running','retryable','scheduled'))`, runID).Scan(ctx, &liveJobs); err != nil {
			return fmt.Errorf("check obsolete matching deliveries: %w", err)
		}
		var liveOwner bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_run WHERE id=? AND active_river_job_id IS NOT NULL)`, runID).Scan(ctx, &liveOwner); err != nil {
			return fmt.Errorf("check obsolete matching execution owner: %w", err)
		}
		if liveJobs || liveOwner {
			return nil
		}
		if _, err := tx.NewRaw(`UPDATE matching_run SET quiesced_at=transaction_timestamp()
			WHERE id=? AND quiesced_at IS NULL AND active_river_job_id IS NULL`, runID).Exec(ctx); err != nil {
			return fmt.Errorf("mark obsolete matching run quiescent: %w", err)
		}
		return nil
	})
}

func cancelObsoleteMatchingDeliveries(ctx context.Context, tx bun.Tx, runID uuid.UUID) (bool, error) {
	var jobs []struct {
		ID            int64           `bun:"id"`
		Kind          string          `bun:"kind"`
		Args          json.RawMessage `bun:"args"`
		Attempt       int             `bun:"attempt"`
		State         string          `bun:"state"`
		Delivery      int             `bun:"operation_attempt"`
		ParentAttempt int             `bun:"parent_attempt"`
	}
	if err := tx.NewRaw(`SELECT job.id,job.kind,job.args,job.attempt,job.state,delivery.operation_attempt,parent.attempt AS parent_attempt
		FROM matching_delivery delivery JOIN river_job job ON job.id=delivery.river_job_id
		JOIN matching_run run ON run.id=delivery.run_id JOIN operation parent ON parent.id=run.operation_id
		WHERE delivery.run_id=? ORDER BY job.id FOR UPDATE OF job`, runID).Scan(ctx, &jobs); err != nil {
		return false, fmt.Errorf("lock obsolete matching deliveries: %w", err)
	}
	var registrationCount int
	if err := tx.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, runID).Scan(ctx, &registrationCount); err != nil {
		return false, fmt.Errorf("count obsolete matching delivery registrations: %w", err)
	}
	if registrationCount != len(jobs) {
		return false, nil
	}
	for _, job := range jobs {
		var args struct {
			OperationID uuid.UUID `json:"operation_id"`
		}
		var encodedArgs map[string]json.RawMessage
		if json.Unmarshal(job.Args, &encodedArgs) != nil || len(encodedArgs) != 1 || encodedArgs["operation_id"] == nil ||
			json.Unmarshal(encodedArgs["operation_id"], &args.OperationID) != nil ||
			job.Kind != (MatchingEvaluationWakeArgs{}).Kind() || args.OperationID != runID ||
			job.Delivery < 1 || job.Delivery > job.ParentAttempt || job.Attempt < 0 {
			return false, nil
		}
	}
	for _, job := range jobs {
		if job.State == "running" {
			return false, nil
		}
	}
	var liveOwner bool
	if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_run WHERE id=? AND active_river_job_id IS NOT NULL)`, runID).Scan(ctx, &liveOwner); err != nil {
		return false, fmt.Errorf("check obsolete matching execution owner: %w", err)
	}
	if liveOwner {
		return false, nil
	}
	for _, job := range jobs {
		if job.State != "available" && job.State != "pending" && job.State != "retryable" && job.State != "scheduled" {
			continue
		}
		if _, err := tx.NewRaw(`UPDATE river_job SET state='cancelled',finalized_at=transaction_timestamp(),
			metadata=jsonb_set(COALESCE(metadata,'{}'::jsonb),'{cancel_attempted_at}',to_jsonb(transaction_timestamp()),true)
			WHERE id=? AND state IN ('available','pending','retryable','scheduled')`, job.ID).Exec(ctx); err != nil {
			return false, fmt.Errorf("cancel obsolete matching delivery %d: %w", job.ID, err)
		}
	}
	if _, err := tx.NewRaw(`UPDATE matching_delivery delivery SET consumed_at=transaction_timestamp()
		FROM river_job job WHERE delivery.run_id=? AND delivery.river_job_id=job.id
		AND delivery.consumed_at IS NULL AND job.state IN ('completed','cancelled','discarded')`, runID).Exec(ctx); err != nil {
		return false, fmt.Errorf("administratively dispose terminal obsolete matching registrations: %w", err)
	}
	return true, nil
}

func (repository *SetupManagerRepository) matchingFallbackWaitStillEligible(ctx context.Context, candidate matchingRuntimeRecoveryCandidate) (bool, error) {
	var eligible bool
	err := repository.db.NewRaw(`SELECT EXISTS (
		SELECT 1
		FROM matching_run run
		JOIN operation ON operation.id=run.operation_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		WHERE run.id=? AND operation.kind='matching_evaluation_v1'
			AND operation.state='running' AND operation.attempt=?
			AND run.state='waiting_policy'
			AND run.current_river_job_id=operation.river_job_id
			AND acquisition.revision=?
			AND acquisition.current_phase=?
			AND ((acquisition.current_phase='pending_seal' AND operation.stage='pending_seal')
				OR (acquisition.current_phase='fallback_review_wait' AND operation.stage='review_pending'))
			AND NOT EXISTS (SELECT 1 FROM matching_provider_dependency dependency
				WHERE dependency.parent_run_id=run.id AND dependency.terminal_outcome IS NULL)
			AND EXISTS (SELECT 1 FROM matching_delivery delivery
				WHERE delivery.run_id=run.id AND delivery.river_job_id=run.current_river_job_id
				AND delivery.operation_attempt=operation.attempt AND delivery.consumed_at IS NOT NULL)
	)`, candidate.RunID, candidate.Attempt, candidate.Revision, candidate.Phase).Scan(ctx, &eligible)
	if err != nil {
		return false, fmt.Errorf("query current fallback wait: %w", err)
	}
	return eligible, nil
}

// IsMatchingRuntimeScannerOwnedOperation protects quiesced runs from generic
// interruption recovery and retains the narrow automatic fallback exemption.
func (repository *SetupManagerRepository) IsMatchingRuntimeScannerOwnedOperation(ctx context.Context, operationID uuid.UUID) (bool, error) {
	var owned bool
	err := repository.db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM matching_run run
		LEFT JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		WHERE run.operation_id=? AND (run.quiesced_at IS NOT NULL OR
			(acquisition.intent_version=3 AND acquisition.current_phase IN ('pending_seal','fallback_review_wait','fallback_evaluation_sealed')))
	)`, operationID).Scan(ctx, &owned)
	if err != nil {
		return false, fmt.Errorf("check scanner-owned matching operation: %w", err)
	}
	if owned {
		return true, nil
	}
	owned, err = repository.automaticArtistReviewWaitStillCurrent(ctx, operationID)
	if err != nil {
		return false, fmt.Errorf("check automatic artist review ownership: %w", err)
	}
	return owned, nil
}

// automaticArtistReviewWaitStillCurrent recognizes only the durable v2
// automatic artist-search review publication. Unlike a live scanner wait, this
// does not protect queued or running work: its accepted empty-search receipt
// must have been consumed and published against the still-current target.
func (repository *SetupManagerRepository) automaticArtistReviewWaitStillCurrent(ctx context.Context, operationID uuid.UUID) (bool, error) {
	var runID uuid.UUID
	err := repository.db.NewRaw(`SELECT run.id FROM matching_run run
		JOIN operation ON operation.id=run.operation_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		JOIN matching_acquisition_phase phase ON phase.run_id=run.id AND phase.revision=acquisition.revision
		WHERE operation.id=? AND operation.kind='matching_evaluation_v1'
		AND operation.state='running' AND operation.stage='manual_review'
		AND run.state='waiting_policy' AND run.quiesced_at IS NULL
		AND run.current_river_job_id=operation.river_job_id
		AND acquisition.intent_version=2 AND acquisition.current_phase='waiting_policy'
		AND phase.phase='waiting_policy'
		AND phase.output->>'state'='waiting_policy'
		AND phase.output->>'reason' IN ('artist_search_no_results','artist_not_fit')
		AND jsonb_array_length(phase.response_provenance)=1
		AND NOT EXISTS (SELECT 1 FROM matching_provider_dependency dependency
			WHERE dependency.parent_run_id=run.id AND dependency.terminal_outcome IS NULL)
		AND EXISTS (SELECT 1 FROM matching_delivery delivery
			WHERE delivery.run_id=run.id AND delivery.river_job_id=run.current_river_job_id
			AND delivery.operation_attempt=operation.attempt AND delivery.consumed_at IS NOT NULL)`, operationID).Scan(ctx, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read automatic artist review wait: %w", err)
	}

	var phase struct {
		ResponseProvenance []byte `bun:"response_provenance"`
	}
	if err := repository.db.NewRaw(`SELECT response_provenance FROM matching_acquisition_phase
		WHERE run_id=? AND revision=(SELECT revision FROM matching_acquisition WHERE run_id=?)`, runID, runID).Scan(ctx, &phase); err != nil {
		return false, fmt.Errorf("read automatic artist review provenance: %w", err)
	}
	var provenance []matchingAcquisitionResponseProvenance
	if err := json.Unmarshal(phase.ResponseProvenance, &provenance); err != nil {
		return false, fmt.Errorf("decode automatic artist review provenance: %w", err)
	}
	if len(provenance) != 1 || provenance[0].RequestID == uuid.Nil || provenance[0].CacheKey == "" {
		return false, nil
	}

	current := false
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("check automatic artist review wait: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("check automatic artist review wait: lock source mutations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
			return fmt.Errorf("check automatic artist review wait: lock provider configuration: %w", err)
		}
		var source struct {
			ID           uuid.UUID `bun:"provider_source_id"`
			Identity     string    `bun:"provider_configuration_identity"`
			SourceID     uuid.UUID `bun:"id"`
			SourceConfig string    `bun:"configuration_identity"`
		}
		if err := tx.NewRaw(`SELECT run.provider_source_id,run.provider_configuration_identity,
			source.id,source.configuration_identity FROM matching_run run
			JOIN provider_source source ON source.id=run.provider_source_id WHERE run.id=? FOR SHARE OF source`, runID).
			Scan(ctx, &source.ID, &source.Identity, &source.SourceID, &source.SourceConfig); err != nil {
			return fmt.Errorf("read automatic artist review provider fence: %w", err)
		}
		var configIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &configIdentity); err != nil {
			return fmt.Errorf("read current MusicBrainz configuration: %w", err)
		}
		if source.ID != source.SourceID || source.Identity == "" || source.SourceConfig != source.Identity || configIdentity != source.Identity {
			return nil
		}
		if err := validateAutomaticArtistReviewPublicationEvidence(ctx, tx, runID, provenance[0], source.Identity); err != nil {
			if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrIncomingMatchConflict) {
				return nil
			}
			return fmt.Errorf("validate accepted artist-search publication evidence: %w", err)
		}
		if err := validateMatchingRunFence(ctx, tx, runID, 2); err != nil {
			if errors.Is(err, ErrIncomingMatchConflict) || errors.Is(err, ErrMatchingTargetNeedsRefresh) {
				return nil
			}
			return fmt.Errorf("validate current automatic artist review target: %w", err)
		}
		current = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return current, nil
}

// validateAutomaticArtistReviewPublicationEvidence uses the payload copied into
// the published evaluation evidence at acceptance time. The provider cache is
// mutable and may already contain a later response for the same key.
func validateAutomaticArtistReviewPublicationEvidence(ctx context.Context, tx bun.Tx, runID uuid.UUID, provenance matchingAcquisitionResponseProvenance, configurationIdentity string) error {
	var published struct {
		GroupID            uuid.UUID       `bun:"group_id"`
		EvaluationRevision string          `bun:"evaluation_revision"`
		Evidence           json.RawMessage `bun:"evaluation_evidence"`
		ProviderSourceID   uuid.UUID       `bun:"provider_source_id"`
		Configuration      string          `bun:"provider_configuration_identity"`
		AuthorityMode      string          `bun:"matching_authority_mode"`
		AuthorityRevision  int64           `bun:"matching_authority_revision"`
		FenceVersion       int             `bun:"fence_version"`
		TargetRunID        uuid.UUID       `bun:"target_run_id"`
		TargetDigest       []byte          `bun:"target_capture_digest"`
		CapturedTarget     json.RawMessage `bun:"target_capture"`
		ProviderRequest    json.RawMessage `bun:"provider_request"`
	}
	err := tx.NewRaw(`SELECT evaluation.group_id,evaluation.evaluation_revision,evaluation.evaluation_evidence,
		evaluation.provider_source_id,evaluation.provider_configuration_identity,evaluation.matching_authority_mode,
		evaluation.matching_authority_revision,evaluation.fence_version,evaluation.target_run_id,evaluation.target_capture_digest,target.target_capture,provider_request.input_snapshot AS provider_request
		FROM incoming_match_evaluation evaluation
		JOIN matching_run_target target ON target.run_id=? AND target.group_id=evaluation.group_id
		JOIN incoming_matching_generation generation ON generation.group_id=evaluation.group_id
		JOIN matching_run run ON run.id=target.run_id AND run.provider_source_id=evaluation.provider_source_id
		JOIN matching_provider_dependency dependency ON dependency.parent_run_id=run.id AND dependency.request_id=? AND dependency.cache_key=? AND dependency.terminal_outcome='succeeded'
		JOIN operation provider_request ON provider_request.id=dependency.request_id AND provider_request.kind='provider_artist_search'
		WHERE evaluation.group_id=target.group_id AND generation.current_run_id=target.run_id
		AND evaluation.target_capture_digest=target.target_capture_digest
		AND generation.high_water>0 AND generation.high_water=target.target_generation`, runID, provenance.RequestID, provenance.CacheKey).Scan(ctx, &published)
	if err != nil {
		return fmt.Errorf("read current artist-search review publication: %w", err)
	}
	// Revision zero is the implicit original automatic authority; the target
	// capture and live run fence below still require that exact authority revision.
	if published.GroupID == uuid.Nil || published.EvaluationRevision == "" || published.ProviderSourceID == uuid.Nil ||
		published.Configuration != configurationIdentity || published.AuthorityMode != "automatic" || published.AuthorityRevision < 0 ||
		published.FenceVersion != 2 || published.TargetRunID != runID || len(published.TargetDigest) != sha256.Size {
		return ErrIncomingMatchConflict
	}
	var target struct {
		GroupID           uuid.UUID `json:"group_id"`
		AuthorityRevision int64     `json:"authority_revision"`
	}
	if err := json.Unmarshal(published.CapturedTarget, &target); err != nil || target.GroupID != published.GroupID || target.AuthorityRevision != published.AuthorityRevision {
		return ErrIncomingMatchConflict
	}
	var evidence struct {
		Accepted struct {
			RequestID     uuid.UUID       `json:"request_id"`
			CacheKey      string          `json:"cache_key"`
			PayloadSHA    string          `json:"payload_sha256"`
			PayloadBase64 string          `json:"payload_base64"`
			Request       json.RawMessage `json:"request"`
		} `json:"accepted_artist_search"`
	}
	if err := json.Unmarshal(published.Evidence, &evidence); err != nil {
		return fmt.Errorf("decode published artist-search evidence: %w", err)
	}
	accepted := evidence.Accepted
	if accepted.RequestID != provenance.RequestID || accepted.CacheKey != provenance.CacheKey || accepted.PayloadSHA != provenance.PayloadSHA || accepted.PayloadBase64 == "" {
		return ErrIncomingMatchConflict
	}
	acceptedRequest, err := canonicalMatchingJSON(accepted.Request)
	if err != nil {
		return fmt.Errorf("canonicalize published artist-search request: %w", err)
	}
	providerRequest, err := canonicalMatchingJSON(published.ProviderRequest)
	if err != nil || !bytes.Equal(acceptedRequest, providerRequest) {
		return ErrIncomingMatchConflict
	}
	payload, err := base64.StdEncoding.DecodeString(accepted.PayloadBase64)
	if err != nil {
		return fmt.Errorf("decode published artist-search payload: %w", err)
	}
	digest := sha256.Sum256(payload)
	if fmt.Sprintf("%x", digest[:]) != provenance.PayloadSHA {
		return ErrIncomingMatchConflict
	}
	return nil
}

func (repository *SetupManagerRepository) recoverSealedMatchingWake(ctx context.Context, client RiverInserter, candidate matchingRuntimeRecoveryCandidate) (bool, error) {
	recovered := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("recover sealed matching wake: lock output gate: %w", err)
		}
		var source struct {
			ID       uuid.UUID `bun:"provider_source_id"`
			Identity string    `bun:"provider_configuration_identity"`
		}
		if err := tx.NewRaw(`SELECT provider_source_id,provider_configuration_identity FROM matching_run WHERE id=?`, candidate.RunID).Scan(ctx, &source); err != nil {
			return fmt.Errorf("recover sealed matching wake: read source fence: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("recover sealed matching wake: lock source mutation gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
			return fmt.Errorf("recover sealed matching wake: lock provider configuration: %w", err)
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, candidate.RunID)
		if err != nil {
			return err
		}
		var run struct {
			State       string     `bun:"state"`
			SourceID    uuid.UUID  `bun:"provider_source_id"`
			Identity    string     `bun:"provider_configuration_identity"`
			Fence       int        `bun:"fence_version"`
			Epoch       int64      `bun:"execution_epoch"`
			CurrentJob  *int64     `bun:"current_river_job_id"`
			ActiveJob   *int64     `bun:"active_river_job_id"`
			ActiveEpoch *int64     `bun:"active_execution_epoch"`
			ActiveTry   *int       `bun:"active_river_attempt"`
			QuiescedAt  *time.Time `bun:"quiesced_at"`
		}
		if err := tx.NewRaw(`SELECT state,provider_source_id,provider_configuration_identity,fence_version,execution_epoch,
				current_river_job_id,active_river_job_id,active_execution_epoch,active_river_attempt,quiesced_at
				FROM matching_run WHERE id=? FOR UPDATE`, candidate.RunID).Scan(ctx, &run); err != nil {
			return fmt.Errorf("recover sealed matching wake: lock run: %w", err)
		}
		if run.QuiescedAt != nil {
			return nil
		}
		if operation.Kind != "matching_evaluation_v1" || operation.Attempt != candidate.Attempt ||
			(operation.State != "queued" && operation.State != "running") || operation.RiverJobID == nil ||
			run.CurrentJob == nil || *run.CurrentJob != *operation.RiverJobID ||
			(run.State != "queued" && run.State != "running") {
			return nil
		}
		var handoff struct {
			WaitRevision int64  `bun:"wait_revision"`
			Digest       []byte `bun:"seal_digest"`
			JobID        int64  `bun:"river_job_id"`
			Attempt      int    `bun:"operation_attempt"`
		}
		if err := tx.NewRaw(`SELECT wait_revision,seal_digest,river_job_id,operation_attempt
			FROM matching_fallback_evaluation_handoff WHERE run_id=?`, candidate.RunID).Scan(ctx, &handoff); err != nil {
			return fmt.Errorf("recover sealed matching wake: read immutable handoff: %w", err)
		}
		if handoff.Attempt != operation.Attempt || handoff.JobID < 1 || len(handoff.Digest) != 32 {
			return nil
		}
		var acquisition struct {
			Phase    string `bun:"current_phase"`
			Revision int64  `bun:"revision"`
		}
		if err := tx.NewRaw(`SELECT current_phase,revision FROM matching_acquisition WHERE run_id=? FOR UPDATE`, candidate.RunID).Scan(ctx, &acquisition); err != nil {
			return fmt.Errorf("recover sealed matching wake: lock acquisition: %w", err)
		}
		if acquisition.Phase != MatchingAcquisitionFallbackEvaluationSealed || acquisition.Revision != handoff.WaitRevision+1 {
			return nil
		}
		var handoffRegistered bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_delivery WHERE run_id=? AND river_job_id=?
			AND operation_attempt=?)`, candidate.RunID, handoff.JobID, handoff.Attempt).Scan(ctx, &handoffRegistered); err != nil {
			return fmt.Errorf("recover sealed matching wake: check original handoff registration: %w", err)
		}
		if !handoffRegistered {
			return nil
		}
		if err := validateCurrentFallbackHandoffAuthority(ctx, tx, candidate.RunID, run.SourceID, run.Identity, run.Fence); err != nil {
			return fmt.Errorf("recover sealed matching wake: validate current authority: %w", err)
		}
		oldJobID := *run.CurrentJob
		var state string
		err = tx.NewRaw(`SELECT state FROM river_job WHERE id=? FOR UPDATE`, oldJobID).Scan(ctx, &state)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("recover sealed matching wake: lock current River job: %w", err)
		}
		if err == nil && state != "completed" && state != "cancelled" && state != "discarded" {
			return nil
		}
		// A dead current delivery may still have a durable claim left by a
		// crashed worker. Retiring it is safe only when any recorded owner names
		// this exact delivery; an unrelated owner is never displaced here.
		if run.ActiveJob != nil && *run.ActiveJob != oldJobID {
			return nil
		}
		inserted, err := client.InsertTx(ctx, tx.Tx, MatchingEvaluationWakeArgs{OperationID: candidate.RunID}, nil)
		if err != nil || inserted == nil || inserted.Job == nil || inserted.Job.ID < 1 {
			if err == nil {
				err = fmt.Errorf("river returned no registered wake identity")
			}
			return fmt.Errorf("recover sealed matching wake: insert replacement: %w", err)
		}
		newJobID := inserted.Job.ID
		now := time.Now().UTC()
		if _, err := tx.NewRaw(`UPDATE matching_delivery SET consumed_at=? WHERE run_id=? AND operation_attempt=? AND consumed_at IS NULL`, now, candidate.RunID, operation.Attempt).Exec(ctx); err != nil {
			return fmt.Errorf("recover sealed matching wake: consume obsolete registrations: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_delivery(run_id,river_job_id,operation_attempt) VALUES (?,?,?)`, candidate.RunID, newJobID, operation.Attempt).Exec(ctx); err != nil {
			return fmt.Errorf("recover sealed matching wake: register replacement: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE matching_run SET state='queued',execution_epoch=execution_epoch+1,current_river_job_id=?,
			active_river_job_id=NULL,active_river_attempt=NULL,active_execution_epoch=NULL,updated_at=? WHERE id=?`, newJobID, now, candidate.RunID).Exec(ctx); err != nil {
			return fmt.Errorf("recover sealed matching wake: advance execution fence: %w", err)
		}
		operation.State, operation.Stage, operation.RiverJobID = "queued", "queued", &newJobID
		operation.UpdatedAt = now
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "river_job_id", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("recover sealed matching wake: queue operation: %w", err)
		}
		recovered = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return recovered, nil
}
