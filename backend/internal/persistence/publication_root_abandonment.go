package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// abandonFailedPublicationsForRoot retires only failed publication attempts
// whose retained source holds pin this root. The caller holds the output and
// source-root gates; all evidence and authority changes remain in that tx.
func abandonFailedPublicationsForRoot(ctx context.Context, tx bun.Tx, rootID uuid.UUID) error {
	var operations []uuid.UUID
	if err := tx.NewRaw(`SELECT DISTINCT op.id FROM operation op
		JOIN publication_source_hold hold ON hold.operation_id=op.id
		WHERE hold.source_root_id=? AND op.kind='publish_track_v1' AND op.state='failed'
		ORDER BY op.id`, rootID).Scan(ctx, &operations); err != nil {
		return err
	}
	if len(operations) == 0 {
		return nil
	}
	var cleanupOwner bool
	if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM source_cleanup_owner WHERE source_root_id=?)`, rootID).Scan(ctx, &cleanupOwner); err != nil {
		return err
	}
	if cleanupOwner {
		return fmt.Errorf("source cleanup ownership blocks publication abandonment")
	}
	// Preserve publication lock order: epoch, positions, attempts, operations,
	// River jobs, then execution claims.
	var epoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
		return err
	}
	var positions []uuid.UUID
	if err := tx.NewRaw(`SELECT DISTINCT attempt.position_id FROM track_publication_attempt attempt
		WHERE attempt.operation_id IN (?) ORDER BY attempt.position_id`, bun.List(operations)).Scan(ctx, &positions); err != nil {
		return err
	}
	if len(positions) != 0 {
		var locked []uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id IN (?) ORDER BY id FOR UPDATE`, bun.List(positions)).Scan(ctx, &locked); err != nil {
			return err
		}
	}
	var attempts []TrackPublicationAttempt
	if err := tx.NewSelect().Model(&attempts).Where("operation_id IN (?)", bun.List(operations)).Order("position_id", "operation_id").For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if len(attempts) != len(operations) {
		return fmt.Errorf("failed publication source hold has no matching attempt")
	}
	var opRows []Operation
	if err := tx.NewSelect().Model(&opRows).Where("id IN (?)", bun.List(operations)).Order("id").For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var jobs []int64
	if err := tx.NewRaw(`SELECT job.id FROM river_job job WHERE job.kind='publish_track_v1'
		AND EXISTS (SELECT 1 FROM operation op WHERE op.id IN (?)
			AND (op.river_job_id=job.id OR job.args->>'operation_id'=op.id::text))
		ORDER BY job.id FOR UPDATE`, bun.List(operations)).Scan(ctx, &jobs); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if len(jobs) != 0 {
		var states []string
		if err := tx.NewRaw(`SELECT state FROM river_job WHERE id IN (?) ORDER BY id`, bun.List(jobs)).Scan(ctx, &states); err != nil {
			return err
		}
		for _, state := range states {
			if state == "running" {
				return fmt.Errorf("publication delivery is still running")
			}
		}
	}
	var claims []PublicationExecutionClaim
	if err := tx.NewSelect().Model(&claims).Where("operation_id IN (?)", bun.List(operations)).Order("operation_id", "application_attempt").For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, attempt := range attempts {
		if attempt.Epoch != epoch {
			return fmt.Errorf("publication attempt epoch is not current")
		}
		var operation *Operation
		for i := range opRows {
			if opRows[i].ID == attempt.OperationID {
				operation = &opRows[i]
				break
			}
		}
		if operation == nil || operation.State != "failed" || operation.RiverJobID == nil {
			return fmt.Errorf("publication operation is not failed")
		}
		var currentJob struct {
			ID      int64  `bun:"id"`
			Kind    string `bun:"kind"`
			State   string `bun:"state"`
			Attempt int    `bun:"attempt"`
		}
		if err := tx.NewRaw(`SELECT id,kind,state,attempt FROM river_job WHERE id=?`, *operation.RiverJobID).Scan(ctx, &currentJob); err != nil {
			return err
		}
		version, err := frozenPublicationInputVersion(attempt.FrozenInputs)
		if err != nil {
			return fmt.Errorf("read failed publication frozen input version: %w", err)
		}
		var republishReturnEvidence json.RawMessage
		if version == PublicationSourceRepublishInputsVersion && operation.Stage == "source_republish_recovery_required" {
			input, err := DecodePublicationInputs(attempt.FrozenInputs)
			if err != nil || input.SourceRepublish == nil || attempt.IntentKind != "audio" ||
				attempt.ExpectedGeneration == nil || input.SourceRepublish.Current.Generation != *attempt.ExpectedGeneration ||
				input.SourceRepublish.SourceAuthority.PositionID != attempt.PositionID ||
				input.SourceRepublish.SourceAuthority.PublicationEpoch != attempt.Epoch ||
				input.SourceRepublish.SourceAuthority.PublicationGeneration != *attempt.ExpectedGeneration {
				return fmt.Errorf("interrupted source-republish frozen identity is invalid")
			}
			if operation.SafeError == nil || *operation.SafeError == "" || operation.FinishedAt == nil || operation.FinishedAt.IsZero() ||
				operation.Attempt <= 0 || currentJob.ID != *operation.RiverJobID || currentJob.Kind != "publish_track_v1" {
				return fmt.Errorf("interrupted source-republish operation is not durably failed")
			}
			var operationClaims []PublicationExecutionClaim
			for _, candidate := range claims {
				if candidate.OperationID == attempt.OperationID {
					operationClaims = append(operationClaims, candidate)
				}
			}
			if len(operationClaims) != 1 {
				return fmt.Errorf("interrupted source republish does not have one retained execution claim")
			}
			claim := operationClaims[0]
			if (claim.State != "recovery_required" && claim.State != "quiescent") || claim.ApplicationAttempt != operation.Attempt || claim.RiverJobID != *operation.RiverJobID ||
				claim.Epoch != attempt.Epoch || claim.RiverAttempt <= 0 || claim.ClaimUUID == uuid.Nil || claim.BootUUID == uuid.Nil || claim.DeliveryToken == "" {
				return fmt.Errorf("interrupted source-republish claim does not match retained authority")
			}
			originalClaim, err := json.Marshal(struct {
				OperationID        uuid.UUID `json:"operation_id"`
				ApplicationAttempt int       `json:"application_attempt"`
				RiverJobID         int64     `json:"river_job_id"`
				RiverAttempt       int       `json:"river_attempt"`
				ClaimUUID          uuid.UUID `json:"claim_uuid"`
				BootUUID           uuid.UUID `json:"boot_uuid"`
				DeliveryToken      string    `json:"delivery_token"`
				Epoch              uuid.UUID `json:"epoch"`
				State              string    `json:"state"`
			}{claim.OperationID, claim.ApplicationAttempt, claim.RiverJobID, claim.RiverAttempt, claim.ClaimUUID,
				claim.BootUUID, claim.DeliveryToken, claim.Epoch, claim.State})
			if err != nil {
				return fmt.Errorf("encode interrupted source-republish claim evidence: %w", err)
			}
			var exactJobs []struct {
				ID      int64  `bun:"id"`
				Kind    string `bun:"kind"`
				State   string `bun:"state"`
				Attempt int    `bun:"attempt"`
			}
			if err := tx.NewRaw(`SELECT id,kind,state,attempt FROM river_job WHERE kind='publish_track_v1'
				AND (id=? OR args->>'operation_id'=?) ORDER BY id`, *operation.RiverJobID, attempt.OperationID).Scan(ctx, &exactJobs); err != nil {
				return fmt.Errorf("read exact interrupted source-republish delivery: %w", err)
			}
			if len(exactJobs) != 1 || exactJobs[0].ID != claim.RiverJobID || exactJobs[0].Kind != "publish_track_v1" ||
				exactJobs[0].Attempt < claim.RiverAttempt || exactJobs[0].ID != *operation.RiverJobID ||
				(exactJobs[0].State != "completed" && exactJobs[0].State != "cancelled" && exactJobs[0].State != "discarded") {
				return fmt.Errorf("interrupted source-republish River delivery is not exactly finalized")
			}
			type terminalRiverDelivery struct {
				ID      int64  `json:"id"`
				Kind    string `json:"kind"`
				Attempt int    `json:"attempt"`
				State   string `json:"state"`
			}
			republishReturnEvidence, err = json.Marshal(struct {
				OriginalClaim json.RawMessage       `json:"original_claim"`
				TerminalJob   terminalRiverDelivery `json:"terminal_job"`
			}{
				OriginalClaim: originalClaim,
				TerminalJob:   terminalRiverDelivery{ID: exactJobs[0].ID, Kind: exactJobs[0].Kind, Attempt: exactJobs[0].Attempt, State: exactJobs[0].State},
			})
			if err != nil {
				return fmt.Errorf("encode interrupted source-republish return evidence: %w", err)
			}
			if claim.State == "recovery_required" {
				result, err := tx.NewRaw(`UPDATE publication_execution_claim SET state='quiescent'
					WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND river_attempt=? AND claim_uuid=?
					AND boot_uuid=? AND delivery_token=? AND epoch=? AND state='recovery_required'`,
					claim.OperationID, claim.ApplicationAttempt, claim.RiverJobID, claim.RiverAttempt, claim.ClaimUUID,
					claim.BootUUID, claim.DeliveryToken, claim.Epoch).Exec(ctx)
				if err != nil {
					return fmt.Errorf("settle finalized interrupted source-republish claim: %w", err)
				}
				if affected, err := result.RowsAffected(); err != nil || affected != 1 {
					return fmt.Errorf("settle finalized interrupted source-republish claim: rows=%d err=%v", affected, err)
				}
				for i := range claims {
					if claims[i].OperationID == claim.OperationID && claims[i].ApplicationAttempt == claim.ApplicationAttempt {
						claims[i].State = "quiescent"
					}
				}
			}
		}
		var hasClaim bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM publication_execution_claim WHERE operation_id=?)
			AND NOT EXISTS (SELECT 1 FROM publication_execution_claim WHERE operation_id=? AND state <> 'quiescent')`,
			attempt.OperationID, attempt.OperationID).Scan(ctx, &hasClaim); err != nil {
			return err
		}
		if !hasClaim && (attempt.Phase != "queued" || operation.StartedAt != nil || currentJob.State == "running") {
			return fmt.Errorf("publication delivery claim has not been returned")
		}
		// Keep exact operation-identity blockers for dependencies not retired by
		// this graph; unrelated cleanup at the same position is not authority.
		var unsupported bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM source_cleanup_item WHERE operation_id=?
			UNION ALL
			SELECT 1 FROM obsolete_publication_output_cleanup WHERE operation_id=?
		)`, attempt.OperationID, attempt.OperationID).Scan(ctx, &unsupported); err != nil {
			return err
		}
		if unsupported {
			return fmt.Errorf("publication has unsupported cleanup dependencies")
		}
		var holdRowsText, coverRowsText, claimRowsText, reservationRowsText, currentRowsText, currentPublicationText string
		if err := tx.NewRaw(`SELECT COALESCE(jsonb_agg(to_jsonb(h) ORDER BY h.operation_id),'[]'::jsonb)::text
			FROM publication_source_hold h WHERE h.operation_id=?`, attempt.OperationID).Scan(ctx, &holdRowsText); err != nil {
			return err
		}
		if err := tx.NewRaw(`SELECT COALESCE(jsonb_agg(to_jsonb(c)),'[]'::jsonb)::text FROM publication_attempt_cover_input c WHERE c.operation_id=?`, attempt.OperationID).Scan(ctx, &coverRowsText); err != nil {
			return err
		}
		if err := tx.NewRaw(`SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY c.id),'[]'::jsonb)::text
			FROM publication_namespace_claim c WHERE c.epoch=? AND c.id IN (
				SELECT r.namespace_claim_id FROM publication_namespace_reservation r
				WHERE r.position_id=? AND r.generation=?)`, attempt.Epoch, attempt.PositionID, attempt.CandidateGeneration).Scan(ctx, &claimRowsText); err != nil {
			return err
		}
		if err := tx.NewRaw(`SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.namespace_claim_id),'[]'::jsonb)::text
			FROM publication_namespace_reservation r WHERE r.position_id=? AND r.generation=?`, attempt.PositionID, attempt.CandidateGeneration).Scan(ctx, &reservationRowsText); err != nil {
			return err
		}
		if err := tx.NewRaw(`SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.namespace_claim_id),'[]'::jsonb)::text
			FROM publication_namespace_reservation r JOIN track_publication p
			ON p.position_id=r.position_id AND p.generation=r.generation
			WHERE r.position_id=? AND p.epoch=?`, attempt.PositionID, attempt.Epoch).Scan(ctx, &currentRowsText); err != nil {
			return err
		}
		if err := tx.NewRaw(`SELECT COALESCE((SELECT to_jsonb(p)::text FROM track_publication p
			WHERE p.position_id=? AND p.epoch=?),'null')`, attempt.PositionID, attempt.Epoch).Scan(ctx, &currentPublicationText); err != nil {
			return err
		}
		var namespaceIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT r.namespace_claim_id FROM publication_namespace_reservation r
			WHERE r.position_id=? AND r.generation=? ORDER BY r.namespace_claim_id`, attempt.PositionID, attempt.CandidateGeneration).Scan(ctx, &namespaceIDs); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var operationEvidence, attemptEvidence, claimEvidence string
		for _, item := range []struct {
			query string
			id    uuid.UUID
			dest  *string
		}{{`SELECT to_jsonb(op)::text FROM operation op WHERE op.id=?`, attempt.OperationID, &operationEvidence},
			{`SELECT to_jsonb(a)::text FROM track_publication_attempt a WHERE a.operation_id=?`, attempt.OperationID, &attemptEvidence},
			{`SELECT COALESCE(jsonb_agg(to_jsonb(c)),'[]'::jsonb)::text FROM publication_execution_claim c WHERE c.operation_id=?`, attempt.OperationID, &claimEvidence}} {
			if err := tx.NewRaw(item.query, item.id).Scan(ctx, item.dest); err != nil {
				return err
			}
		}
		evidenceFields := map[string]json.RawMessage{
			"operation": json.RawMessage(operationEvidence), "attempt": json.RawMessage(attemptEvidence),
			"source_holds": json.RawMessage(holdRowsText), "cover_input": json.RawMessage(coverRowsText),
			"namespace_claims": json.RawMessage(claimRowsText), "candidate_reservations": json.RawMessage(reservationRowsText),
			"current_reservations": json.RawMessage(currentRowsText), "current_publication": json.RawMessage(currentPublicationText),
			"execution_claims": json.RawMessage(claimEvidence),
		}
		if len(republishReturnEvidence) != 0 {
			evidenceFields["source_republish_return"] = republishReturnEvidence
		}
		evidence, err := json.Marshal(evidenceFields)
		if err != nil {
			return fmt.Errorf("encode publication abandonment evidence: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO publication_root_abandonment
			(operation_id,root_id,epoch,position_id,candidate_generation,namespace_claim_ids,evidence)
			VALUES (?,?,?,?,?,?::uuid[],?::jsonb)`, attempt.OperationID, rootID, attempt.Epoch, attempt.PositionID,
			attempt.CandidateGeneration, uuidArray(namespaceIDs), string(evidence)); err != nil {
			return err
		}
	}
	if _, err := tx.NewRaw(`DELETE FROM publication_source_hold WHERE operation_id IN (?)`, bun.List(operations)).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`DELETE FROM track_publication_attempt WHERE operation_id IN (?)`, bun.List(operations)).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`DELETE FROM publication_execution_claim WHERE operation_id IN (?)`, bun.List(operations)).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`UPDATE river_job job SET state='cancelled',finalized_at=now()
		WHERE job.kind='publish_track_v1' AND EXISTS (SELECT 1 FROM operation op WHERE op.id IN (?)
			AND (op.river_job_id=job.id OR job.args->>'operation_id'=op.id::text))
		AND job.state IN ('available','pending','retryable','scheduled')`, bun.List(operations)).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`DELETE FROM operation WHERE id IN (?)`, bun.List(operations)).Exec(ctx); err != nil {
		return err
	}
	return nil
}

// MarkPublicationDeliveryReturned records that the exact delivery owner has
// stopped using its native inputs. It deliberately does not require a live
// operation or current-generation authority at return time.
func (repository *SetupManagerRepository) MarkPublicationDeliveryReturned(ctx context.Context, guard PublicationDeliveryGuard) error {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return err
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("return publication delivery: lock output admission: %w", err)
		}
		var epoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
			return fmt.Errorf("return publication delivery: lock epoch: %w", err)
		}
		// Find the position without locking the attempt first, preserving the
		// publication order: epoch, position, attempt, operation, job, claim.
		var positionID uuid.UUID
		if err := tx.NewRaw(`SELECT position_id FROM track_publication_attempt WHERE operation_id=?`, guard.OperationID).Scan(ctx, &positionID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil // A successful commit has already retired the attempt/claim.
			}
			return fmt.Errorf("return publication delivery: read attempt position: %w", err)
		}
		if positionID != guard.ExpectedPositionID {
			return nil
		}
		var lockedPosition uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id=? FOR UPDATE`, positionID).Scan(ctx, &lockedPosition); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("return publication delivery: lock position: %w", err)
		}
		var attempt TrackPublicationAttempt
		if err := tx.NewSelect().Model(&attempt).Where("operation_id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("return publication delivery: lock attempt: %w", err)
		}
		var op Operation
		if err := tx.NewSelect().Model(&op).Where("id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("return publication delivery: lock operation: %w", err)
		}
		if op.Kind != "publish_track_v1" || op.Attempt != guard.ApplicationAttempt || op.RiverJobID == nil || *op.RiverJobID != guard.RiverJobID ||
			attempt.Epoch != guard.Epoch || attempt.PositionID != guard.ExpectedPositionID || attempt.CandidateGeneration != guard.CandidateGeneration {
			return nil
		}
		// Lock the job when it still exists, but do not require it to be running,
		// current, or even retained: the stored execution claim is the returned
		// delivery's fence after the worker has quiesced.
		var lockedJobID int64
		if err := tx.NewRaw(`SELECT id FROM river_job WHERE id=? FOR UPDATE`, guard.RiverJobID).Scan(ctx, &lockedJobID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("return publication delivery: lock River job: %w", err)
		}
		var claim PublicationExecutionClaim
		if err := tx.NewSelect().Model(&claim).Where("operation_id=? AND application_attempt=?", guard.OperationID, guard.ApplicationAttempt).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil // Commit or a successor delivery removed/replaced this claim.
			}
			return fmt.Errorf("return publication delivery: lock execution claim: %w", err)
		}
		if !publicationClaimMatches(claim, guard) {
			return nil // Never return a successor's claim.
		}
		if claim.State == "quiescent" {
			return nil
		}
		if claim.State != "executing" {
			return nil
		}
		if _, err := tx.NewRaw(`UPDATE publication_execution_claim SET state='quiescent'
			WHERE operation_id=? AND application_attempt=? AND state='executing'
			AND river_job_id=? AND river_attempt=? AND claim_uuid=? AND boot_uuid=?
			AND delivery_token=? AND epoch=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID,
			guard.RiverAttempt, guard.ClaimUUID, guard.BootUUID, guard.DeliveryToken, guard.Epoch).Exec(ctx); err != nil {
			return fmt.Errorf("return publication delivery: record quiescence: %w", err)
		}
		return nil
	})
}

// CheckPublicationTargetReplacement permits replacement unless the exact
// frozen target is held only by an archived, non-CURRENT publication claim.
func (repository *SetupManagerRepository) CheckPublicationTargetReplacement(ctx context.Context, guard PublicationDeliveryGuard, relativePath string) (bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil || relativePath == "" {
		return false, errInvalidPublicationDeliveryGuard
	}
	var allowed bool
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, _, claim, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "check publication target replacement")
		if err != nil {
			return err
		}
		if !live || attempt == nil || claim == nil || claim.State != "executing" {
			return fmt.Errorf("check publication target replacement: stale delivery guard")
		}
		version, err := frozenPublicationInputVersion(attempt.FrozenInputs)
		if err != nil {
			return fmt.Errorf("check publication target replacement: read frozen input version: %w", err)
		}
		if version == PublicationSourceRepublishInputsVersion {
			input, err := DecodePublicationInputs(attempt.FrozenInputs)
			if err != nil {
				return fmt.Errorf("check publication target replacement: decode source-republish inputs: %w", err)
			}
			if !validSourceRepublishPreparation(input) || input.SourceRepublish == nil || attempt.ExpectedGeneration == nil ||
				*attempt.ExpectedGeneration != input.SourceRepublish.Current.Generation ||
				relativePath != input.SourceRepublish.Current.RelativePath {
				return fmt.Errorf("check publication target replacement: source-republish target differs from frozen CURRENT")
			}
			if _, err := validateSourceRepublishAttemptNamespace(ctx, tx, attempt, input); err != nil {
				return fmt.Errorf("check publication target replacement: validate source-republish CURRENT namespace: %w", err)
			}
		}
		var pathMatches bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM publication_namespace_reservation r
			JOIN publication_namespace_claim c ON c.id=r.namespace_claim_id
			WHERE r.position_id=? AND r.generation=? AND c.relative_path=?)`,
			attempt.PositionID, attempt.CandidateGeneration, relativePath).Scan(ctx, &pathMatches); err != nil {
			return err
		}
		if !pathMatches {
			return fmt.Errorf("check publication target replacement: target differs from frozen namespace")
		}
		var abandoned bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM publication_root_abandonment a
			JOIN publication_namespace_claim c ON c.id=ANY(a.namespace_claim_ids)
			WHERE c.epoch=? AND c.relative_path=? AND NOT EXISTS (
				SELECT 1 FROM track_publication p JOIN publication_namespace_reservation r
				ON r.position_id=p.position_id AND r.generation=p.generation
				WHERE p.position_id=a.position_id AND p.epoch=a.epoch
				AND r.namespace_claim_id=c.id))`, attempt.Epoch, relativePath).Scan(ctx, &abandoned); err != nil {
			return err
		}
		allowed = !abandoned
		return nil
	})
	return allowed, err
}
