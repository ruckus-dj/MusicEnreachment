package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

// PublicationReconciliationJobArgs carries only durable operation identity.
type PublicationReconciliationJobArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (PublicationReconciliationJobArgs) Kind() string { return PublicationReconciliationOperationKind }

// This fence authorizes reconciliation database decisions, never filesystem
// exposure or takeover of an existing publication execution claim.
type PublicationReconciliationDeliveryGuard struct {
	OperationID        uuid.UUID
	ApplicationAttempt int
	RiverJobID         int64
	RiverAttempt       int
	OutputEpoch        uuid.UUID
	DeliveryEpoch      int64
	PositionID         uuid.UUID
	ObservedGeneration int64
	ClaimToken         uuid.UUID
}

type PublicationReconciliationClaimResult string

const (
	PublicationReconciliationNotClaimed PublicationReconciliationClaimResult = "not_claimed"
	PublicationReconciliationBusy       PublicationReconciliationClaimResult = "busy"
	PublicationReconciliationStale      PublicationReconciliationClaimResult = "stale"
	PublicationReconciliationClaimed    PublicationReconciliationClaimResult = "claimed"
)

const PublicationReconciliationBusyAttemptReason = "publication attempt is retained"
const PublicationReconciliationAcceptedGraphPendingReason = "accepted MusicBrainz release track graph is unavailable"

type publicationReconciliationEvent struct {
	ID                 uuid.UUID  `bun:"id"`
	OutputEpoch        uuid.UUID  `bun:"output_epoch"`
	PositionID         uuid.UUID  `bun:"position_id"`
	OperationID        uuid.UUID  `bun:"operation_id"`
	ObservedGeneration int64      `bun:"observed_generation"`
	State              string     `bun:"state"`
	RegisteredJobID    *int64     `bun:"registered_job_id"`
	DeliveryEpoch      int64      `bun:"delivery_epoch"`
	HighestAttempt     int        `bun:"highest_delivery_attempt"`
	Claimed            bool       `bun:"delivery_claimed"`
	ClaimToken         *uuid.UUID `bun:"claim_token"`
	CreatedAt          time.Time  `bun:"created_at"`
	CompletedAt        *time.Time `bun:"completed_at"`
}

type publicationReconciliationJob struct {
	State   string          `bun:"state"`
	Attempt int             `bun:"attempt"`
	Kind    string          `bun:"kind"`
	Args    json.RawMessage `bun:"args"`
}

// DispatchPendingPublicationReconciliations handles a bounded batch. Existing
// requests are the only source of work: this never discovers local releases.
// Each position has its own transaction and locks request -> event -> old job
// -> operation. A busy dispatched event keeps its coalesced successor pending.
func (repository *SetupManagerRepository) DispatchPendingPublicationReconciliations(ctx context.Context, client RiverInserter) (int, error) {
	if client == nil {
		return 0, fmt.Errorf("dispatch publication reconciliations: River client is required")
	}
	if err := repository.wakeSettledPublicationReconciliations(ctx); err != nil {
		return 0, err
	}
	var candidates []struct {
		Epoch    uuid.UUID `bun:"output_epoch"`
		Position uuid.UUID `bun:"position_id"`
	}
	if err := repository.db.NewRaw(`SELECT r.output_epoch,r.position_id
		FROM publication_reconciliation_request r
		JOIN publication_namespace_state n ON n.singleton AND n.output_epoch=r.output_epoch
		WHERE EXISTS (SELECT 1 FROM publication_reconciliation_event e
			LEFT JOIN river_job j ON j.id=e.registered_job_id
			WHERE e.output_epoch=r.output_epoch AND e.position_id=r.position_id
			AND (e.state='pending' OR (e.state='dispatched' AND (j.id IS NULL OR j.state IN ('cancelled','discarded','completed')))))
		AND NOT EXISTS (SELECT 1 FROM publication_reconciliation_event active
			JOIN river_job active_job ON active_job.id=active.registered_job_id
			WHERE active.output_epoch=r.output_epoch AND active.position_id=r.position_id
			AND active.state='dispatched' AND active_job.state NOT IN ('cancelled','discarded','completed'))
		AND NOT (r.parked_reason IS NOT NULL AND r.evaluated_generation >= r.requested_generation)
		ORDER BY r.position_id LIMIT 64`).Scan(ctx, &candidates); err != nil {
		return 0, fmt.Errorf("select publication reconciliation dispatch batch: %w", err)
	}
	dispatched := 0
	for _, candidate := range candidates {
		changed := false
		err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			current, err := lockPublicationReconciliationRequest(ctx, tx, candidate.Epoch, candidate.Position)
			if err != nil || !current {
				return err
			}
			var parked bool
			if err := tx.NewRaw(`SELECT parked_reason IS NOT NULL AND evaluated_generation>=requested_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, candidate.Epoch, candidate.Position).Scan(ctx, &parked); err != nil {
				return fmt.Errorf("recheck publication reconciliation parking: %w", err)
			}
			if parked {
				return nil
			}
			var events []publicationReconciliationEvent
			if err := tx.NewRaw(`SELECT * FROM publication_reconciliation_event
				WHERE output_epoch=? AND position_id=? AND state IN ('pending','dispatched')
				ORDER BY CASE state WHEN 'dispatched' THEN 0 ELSE 1 END,created_at,id FOR UPDATE`, candidate.Epoch, candidate.Position).Scan(ctx, &events); err != nil {
				return fmt.Errorf("lock publication reconciliation events: %w", err)
			}
			if len(events) == 0 {
				return nil
			}
			event := events[0]
			if event.State == "dispatched" {
				if event.RegisteredJobID == nil {
					return fmt.Errorf("dispatched publication reconciliation has no registered job")
				}
				job, err := lockPublicationReconciliationJob(ctx, tx, *event.RegisteredJobID)
				if err != nil {
					return err
				}
				if job != nil && job.State != "cancelled" && job.State != "discarded" && job.State != "completed" {
					return nil
				}
			}
			op, err := repository.GetOperationForUpdate(ctx, tx, event.OperationID)
			if err != nil {
				return err
			}
			var held bool
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation_tool_read_hold WHERE operation_id=?)`, event.OperationID).Scan(ctx, &held); err != nil {
				return err
			}
			if held {
				return nil
			}
			if op.Kind != PublicationReconciliationOperationKind || (op.State != "queued" && op.State != "running" && op.State != "failed") {
				return fmt.Errorf("publication reconciliation operation is not dispatchable")
			}
			inserted, err := client.InsertTx(ctx, tx.Tx, PublicationReconciliationJobArgs{OperationID: event.OperationID}, &river.InsertOpts{Queue: "publication"})
			if err != nil {
				return fmt.Errorf("enqueue publication reconciliation: %w", err)
			}
			if inserted == nil || inserted.Job == nil || inserted.Job.ID <= 0 {
				return fmt.Errorf("enqueue publication reconciliation returned no job identity")
			}
			if _, err := tx.NewRaw(`UPDATE publication_reconciliation_event SET state='dispatched',registered_job_id=?,
				delivery_epoch=delivery_epoch+1,highest_delivery_attempt=0,delivery_claimed=false,claim_token=NULL WHERE id=?`, inserted.Job.ID, event.ID).Exec(ctx); err != nil {
				return fmt.Errorf("register publication reconciliation job: %w", err)
			}
			if _, err := tx.NewRaw(`UPDATE operation SET river_job_id=?,state='queued',stage='queued',safe_error=NULL,
				started_at=NULL,finished_at=NULL,updated_at=now(),attempt=attempt+CASE WHEN state='failed' THEN 1 ELSE 0 END WHERE id=?`, inserted.Job.ID, event.OperationID).Exec(ctx); err != nil {
				return fmt.Errorf("register publication reconciliation operation: %w", err)
			}
			changed = true
			return nil
		})
		if err != nil {
			return dispatched, err
		}
		if changed {
			dispatched++
		}
	}
	return dispatched, nil
}

// wakeSettledPublicationReconciliations repairs only already-requested busy
// obligations. It never discovers positions. The request-row lock protects the
// decision and successor generation; the shared output gate fences output reset,
// not concurrent settlement. The attempt check deliberately remains non-locking.
func (repository *SetupManagerRepository) wakeSettledPublicationReconciliations(ctx context.Context) error {
	var candidates []struct {
		Epoch    uuid.UUID `bun:"output_epoch"`
		Position uuid.UUID `bun:"position_id"`
	}
	if err := repository.db.NewRaw(`SELECT r.output_epoch,r.position_id
		FROM publication_reconciliation_request r
		JOIN publication_namespace_state n ON n.singleton AND n.output_epoch=r.output_epoch
		WHERE r.parked_reason=? AND r.evaluated_generation>=r.requested_generation
		AND EXISTS (SELECT 1 FROM publication_reconciliation_dirty_scope d
			WHERE d.output_epoch=r.output_epoch AND d.position_id=r.position_id)
		AND NOT EXISTS (SELECT 1 FROM track_publication_attempt a WHERE a.position_id=r.position_id)
		ORDER BY r.position_id LIMIT 64`, PublicationReconciliationBusyAttemptReason).Scan(ctx, &candidates); err != nil {
		return fmt.Errorf("select settled busy publication reconciliations: %w", err)
	}
	for _, candidate := range candidates {
		err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			current, err := lockPublicationReconciliationRequest(ctx, tx, candidate.Epoch, candidate.Position)
			if err != nil || !current {
				return err
			}
			var eligible bool
			if err := tx.NewRaw(`SELECT r.parked_reason=? AND r.evaluated_generation>=r.requested_generation
				AND EXISTS (SELECT 1 FROM publication_reconciliation_dirty_scope d WHERE d.output_epoch=r.output_epoch AND d.position_id=r.position_id)
				AND NOT EXISTS (SELECT 1 FROM track_publication_attempt a WHERE a.position_id=r.position_id)
				FROM publication_reconciliation_request r WHERE r.output_epoch=? AND r.position_id=?`,
				PublicationReconciliationBusyAttemptReason, candidate.Epoch, candidate.Position).Scan(ctx, &eligible); err != nil {
				return fmt.Errorf("recheck settled busy publication reconciliation: %w", err)
			}
			if !eligible {
				return nil
			}
			var scopes []string
			if err := tx.NewRaw(`SELECT scope FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=? ORDER BY scope`, candidate.Epoch, candidate.Position).Scan(ctx, &scopes); err != nil {
				return fmt.Errorf("read settled busy reconciliation scopes: %w", err)
			}
			if len(scopes) == 0 {
				return nil
			}
			if err := repository.RequestPublicationReconciliationWith(ctx, tx, candidate.Epoch, []uuid.UUID{candidate.Position}, scopes); err != nil {
				return fmt.Errorf("wake settled busy publication reconciliation: %w", err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (repository *SetupManagerRepository) ClaimPublicationReconciliationDelivery(ctx context.Context, operationID uuid.UUID, jobID int64, riverAttempt int) (PublicationReconciliationDeliveryGuard, PublicationReconciliationClaimResult, error) {
	var guard PublicationReconciliationDeliveryGuard
	result := PublicationReconciliationNotClaimed
	if operationID == uuid.Nil || jobID <= 0 || riverAttempt <= 0 {
		return guard, result, fmt.Errorf("claim publication reconciliation: valid operation, job and attempt are required")
	}
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// The first read only identifies the request row. Reload the event after
		// taking that row lock: a dispatcher may have replaced its delivery.
		var hint publicationReconciliationEvent
		err := tx.NewRaw(`SELECT output_epoch,position_id FROM publication_reconciliation_event WHERE operation_id=?`, operationID).Scan(ctx, &hint)
		if err == sql.ErrNoRows {
			result = PublicationReconciliationStale
			return nil
		}
		if err != nil {
			return fmt.Errorf("read publication reconciliation identity: %w", err)
		}
		current, err := lockPublicationReconciliationRequest(ctx, tx, hint.OutputEpoch, hint.PositionID)
		if err != nil || !current {
			result = PublicationReconciliationStale
			return err
		}
		var event publicationReconciliationEvent
		err = tx.NewRaw(`SELECT * FROM publication_reconciliation_event WHERE operation_id=? FOR UPDATE`, operationID).Scan(ctx, &event)
		if err == sql.ErrNoRows {
			result = PublicationReconciliationStale
			return nil
		}
		if err != nil {
			return err
		}
		if event.State != "dispatched" || event.RegisteredJobID == nil || *event.RegisteredJobID != jobID || event.HighestAttempt > riverAttempt {
			result = PublicationReconciliationStale
			return nil
		}
		job, err := lockPublicationReconciliationJob(ctx, tx, jobID)
		if err != nil {
			return err
		}
		if !publicationReconciliationJobMatches(job, operationID, riverAttempt) {
			result = PublicationReconciliationStale
			return nil
		}
		op, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if op.Kind != PublicationReconciliationOperationKind || (op.State != "queued" && op.State != "running") || op.Attempt <= 0 || op.RiverJobID == nil || *op.RiverJobID != jobID {
			result = PublicationReconciliationStale
			return nil
		}
		if event.HighestAttempt == riverAttempt && event.Claimed {
			result = PublicationReconciliationBusy
			return nil
		}
		var held bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation_tool_read_hold WHERE operation_id=?)`, operationID).Scan(ctx, &held); err != nil {
			return err
		}
		if held {
			result = PublicationReconciliationBusy
			return nil
		}
		token := uuid.New()
		if _, err := tx.NewRaw(`UPDATE publication_reconciliation_event SET highest_delivery_attempt=?,delivery_claimed=true,claim_token=? WHERE id=?`, riverAttempt, token, event.ID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE operation SET state='running',stage='reconciling',started_at=COALESCE(started_at,now()),updated_at=now() WHERE id=?`, operationID).Exec(ctx); err != nil {
			return err
		}
		guard = PublicationReconciliationDeliveryGuard{OperationID: operationID, ApplicationAttempt: op.Attempt, RiverJobID: jobID, RiverAttempt: riverAttempt, OutputEpoch: event.OutputEpoch, DeliveryEpoch: event.DeliveryEpoch, PositionID: event.PositionID, ObservedGeneration: event.ObservedGeneration, ClaimToken: token}
		result = PublicationReconciliationClaimed
		return nil
	})
	if err != nil {
		return PublicationReconciliationDeliveryGuard{}, PublicationReconciliationNotClaimed, err
	}
	return guard, result, nil
}

func (repository *SetupManagerRepository) ReleasePublicationReconciliationDelivery(ctx context.Context, guard PublicationReconciliationDeliveryGuard) (bool, error) {
	var released bool
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		event, err := repository.lockPublicationReconciliationDelivery(ctx, tx, guard)
		if err != nil || event == nil {
			return err
		}
		if err := rejectPublicationReconciliationToolHold(ctx, tx, guard.OperationID); err != nil {
			return err
		}
		if _, err = tx.NewRaw(`UPDATE publication_reconciliation_event SET delivery_claimed=false,claim_token=NULL WHERE id=?`, event.ID).Exec(ctx); err == nil {
			_, err = tx.NewRaw(`UPDATE operation SET state='queued',stage='waiting',safe_error=NULL,started_at=NULL,finished_at=NULL,updated_at=now() WHERE id=?`, guard.OperationID).Exec(ctx)
		}
		released = err == nil
		return err
	})
	return released && err == nil, err
}

// ParkPublicationReconciliationDelivery records an evaluated-but-unsatisfied
// result without acknowledging dirty work. Newer requests invalidate the result.
func (repository *SetupManagerRepository) ParkPublicationReconciliationDelivery(ctx context.Context, guard PublicationReconciliationDeliveryGuard, evaluatedGeneration int64, reason string) (bool, error) {
	if evaluatedGeneration < 0 || reason == "" {
		return false, fmt.Errorf("park publication reconciliation: generation and reason are required")
	}
	var parked bool
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		parked, err = repository.parkPublicationReconciliationDeliveryWith(ctx, tx, guard, evaluatedGeneration, reason, false)
		return err
	})
	return parked && err == nil, err
}

// ParkPublicationReconciliationInputsPending re-evaluates the current
// publication and accepted-graph condition before durably parking this delivery.
// A changed proof or a newer request leaves the obligation available for retry.
func (repository *SetupManagerRepository) ParkPublicationReconciliationInputsPending(ctx context.Context, witness *PublicationReconciliationInputsPending) (bool, error) {
	if witness == nil || witness.guard.OperationID == uuid.Nil || witness.generation <= 0 {
		return false, fmt.Errorf("park publication reconciliation: invalid pending-input witness")
	}
	var parked bool
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		decision, err := repository.evaluatePublicationReconciliationCurrentTx(ctx, tx, witness.releaseID, witness.guard, witness.endpoint, witness.configuration)
		if err != nil {
			return err
		}
		if decision.Action != InputsPending || decision.InputsPending == nil || *decision.InputsPending != *witness {
			return nil
		}
		parked, err = repository.parkPublicationReconciliationDeliveryWith(ctx, tx, witness.guard, witness.generation, PublicationReconciliationAcceptedGraphPendingReason, false)
		return err
	})
	return parked && err == nil, err
}

// ParkPublicationReconciliationBusyAttempt atomically revalidates a delivery,
// confirms an attempt still exists without locking that attempt row, and parks
// the delivery without acknowledging or deleting any dirty scopes.
func (repository *SetupManagerRepository) ParkPublicationReconciliationBusyAttempt(ctx context.Context, guard PublicationReconciliationDeliveryGuard, evaluatedGeneration int64, reason string) (bool, error) {
	if evaluatedGeneration < 0 || reason == "" {
		return false, fmt.Errorf("park publication reconciliation: generation and reason are required")
	}
	var parked bool
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		parked, err = repository.parkPublicationReconciliationDeliveryWith(ctx, tx, guard, evaluatedGeneration, reason, true)
		return err
	})
	return parked && err == nil, err
}

func (repository *SetupManagerRepository) parkPublicationReconciliationDeliveryWith(ctx context.Context, tx bun.Tx, guard PublicationReconciliationDeliveryGuard, evaluatedGeneration int64, reason string, requireBusyAttempt bool) (bool, error) {
	event, err := repository.lockPublicationReconciliationDelivery(ctx, tx, guard)
	if err != nil || event == nil {
		return false, err
	}
	if err := rejectPublicationReconciliationToolHold(ctx, tx, guard.OperationID); err != nil {
		return false, err
	}
	var requested int64
	if err := tx.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, guard.OutputEpoch, guard.PositionID).Scan(ctx, &requested); err != nil {
		return false, err
	}
	if evaluatedGeneration != requested || evaluatedGeneration < event.ObservedGeneration {
		return false, nil
	}
	if requireBusyAttempt {
		var busy bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM track_publication_attempt WHERE position_id=?)`, guard.PositionID).Scan(ctx, &busy); err != nil {
			return false, err
		}
		if !busy {
			return false, nil
		}
	}
	if _, err := tx.NewRaw(`UPDATE publication_reconciliation_request SET evaluated_generation=?,parked_reason=?,retry_not_before=NULL WHERE output_epoch=? AND position_id=?`, evaluatedGeneration, reason, guard.OutputEpoch, guard.PositionID).Exec(ctx); err != nil {
		return false, err
	}
	if _, err := tx.NewRaw(`UPDATE publication_reconciliation_event SET state='completed',completed_at=now(),delivery_claimed=false,claim_token=NULL WHERE id=?`, event.ID).Exec(ctx); err != nil {
		return false, err
	}
	if _, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='waiting',safe_error=?,finished_at=now(),updated_at=now() WHERE id=?`, reason, guard.OperationID).Exec(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// PublicationReconciliationContext is a short-transaction factual snapshot for
// an owned delivery. It contains no settings, filesystem state, or policy.
type PublicationReconciliationContext struct {
	ReleaseID             uuid.UUID
	RequestedGeneration   int64
	DirtyScopes           []PublicationReconciliationDirtyScope
	HasCurrentPublication bool
	HasActiveAttempt      bool
}

type PublicationReconciliationDirtyScope struct {
	Scope      string
	Generation int64
}

// ResolvePublicationReconciliationContext validates the full live delivery
// fence and reads only database facts; callers perform external I/O afterward.
func (repository *SetupManagerRepository) ResolvePublicationReconciliationContext(ctx context.Context, guard PublicationReconciliationDeliveryGuard) (*PublicationReconciliationContext, error) {
	var result *PublicationReconciliationContext
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		event, err := repository.lockPublicationReconciliationDelivery(ctx, tx, guard)
		if err != nil {
			return err
		}
		if event == nil {
			return fmt.Errorf("resolve publication reconciliation context: delivery fence is stale")
		}
		var snapshot PublicationReconciliationContext
		if err := tx.NewRaw(`SELECT rt.release_id,r.requested_generation,
			EXISTS (SELECT 1 FROM track_publication p WHERE p.position_id=r.position_id) AS has_current_publication,
			EXISTS (SELECT 1 FROM track_publication_attempt a
				WHERE a.position_id=r.position_id) AS has_active_attempt
			FROM publication_reconciliation_request r JOIN release_track rt ON rt.id=r.position_id
			WHERE r.output_epoch=? AND r.position_id=?`, guard.OutputEpoch, guard.PositionID).Scan(ctx, &snapshot); err != nil {
			return fmt.Errorf("read publication reconciliation facts: %w", err)
		}
		if err := tx.NewRaw(`SELECT scope,generation FROM publication_reconciliation_dirty_scope
			WHERE output_epoch=? AND position_id=? ORDER BY scope`, guard.OutputEpoch, guard.PositionID).Scan(ctx, &snapshot.DirtyScopes); err != nil {
			return fmt.Errorf("read publication reconciliation dirty scopes: %w", err)
		}
		result = &snapshot
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// AcknowledgePublicationReconciliationDeliveryWith belongs in the transaction
// which records the successful reconciliation decision. Callers acquire their
// domain/admission resources before this request/event/job/operation lock chain.
func (repository *SetupManagerRepository) AcknowledgePublicationReconciliationDeliveryWith(ctx context.Context, tx bun.Tx, guard PublicationReconciliationDeliveryGuard) (bool, error) {
	if tx.Tx == nil {
		return false, fmt.Errorf("acknowledge publication reconciliation: transaction is required")
	}
	event, err := repository.lockPublicationReconciliationDelivery(ctx, tx, guard)
	if err != nil || event == nil {
		return false, err
	}
	if err := rejectPublicationReconciliationToolHold(ctx, tx, guard.OperationID); err != nil {
		return false, err
	}
	if _, err := tx.NewRaw(`DELETE FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=? AND generation<=?`, guard.OutputEpoch, guard.PositionID, event.ObservedGeneration).Exec(ctx); err != nil {
		return false, err
	}
	if _, err := tx.NewRaw(`UPDATE publication_reconciliation_request SET reconciled_generation=GREATEST(reconciled_generation,?) WHERE output_epoch=? AND position_id=?`, event.ObservedGeneration, guard.OutputEpoch, guard.PositionID).Exec(ctx); err != nil {
		return false, err
	}
	if _, err := tx.NewRaw(`UPDATE publication_reconciliation_event SET state='completed',completed_at=now(),delivery_claimed=false,claim_token=NULL WHERE id=?`, event.ID).Exec(ctx); err != nil {
		return false, err
	}
	if _, err := tx.NewRaw(`UPDATE operation SET state='succeeded',stage='completed',safe_error=NULL,finished_at=now(),updated_at=now() WHERE id=?`, guard.OperationID).Exec(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func lockPublicationReconciliationRequest(ctx context.Context, tx bun.Tx, epoch, position uuid.UUID) (bool, error) {
	if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
		return false, fmt.Errorf("lock publication reconciliation output admission: %w", err)
	}
	var current uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &current); err != nil {
		return false, err
	}
	if current != epoch {
		return false, nil
	}
	var generation int64
	err := tx.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=? FOR UPDATE`, epoch, position).Scan(ctx, &generation)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func lockPublicationReconciliationJob(ctx context.Context, tx bun.Tx, id int64) (*publicationReconciliationJob, error) {
	var job publicationReconciliationJob
	err := tx.NewRaw(`SELECT state,attempt,kind,args FROM river_job WHERE id=? FOR UPDATE`, id).Scan(ctx, &job)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock publication reconciliation River delivery: %w", err)
	}
	return &job, nil
}

func publicationReconciliationJobMatches(job *publicationReconciliationJob, operationID uuid.UUID, attempt int) bool {
	if job == nil || job.Kind != PublicationReconciliationOperationKind || job.State != "running" || job.Attempt != attempt {
		return false
	}
	var args PublicationReconciliationJobArgs
	decoder := json.NewDecoder(bytes.NewReader(job.Args))
	decoder.DisallowUnknownFields()
	return decoder.Decode(&args) == nil && args.OperationID == operationID
}

func rejectPublicationReconciliationToolHold(ctx context.Context, tx bun.Tx, operationID uuid.UUID) error {
	var held bool
	if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation_tool_read_hold WHERE operation_id=?)`, operationID).Scan(ctx, &held); err != nil {
		return err
	}
	if held {
		return fmt.Errorf("publication reconciliation delivery retains a live tools read hold")
	}
	return nil
}

func (repository *SetupManagerRepository) lockPublicationReconciliationDelivery(ctx context.Context, tx bun.Tx, guard PublicationReconciliationDeliveryGuard) (*publicationReconciliationEvent, error) {
	if guard.OperationID == uuid.Nil || guard.ClaimToken == uuid.Nil || guard.OutputEpoch == uuid.Nil || guard.PositionID == uuid.Nil || guard.ApplicationAttempt <= 0 || guard.RiverJobID <= 0 || guard.RiverAttempt <= 0 || guard.DeliveryEpoch <= 0 || guard.ObservedGeneration <= 0 {
		return nil, fmt.Errorf("publication reconciliation requires a valid delivery fence")
	}
	current, err := lockPublicationReconciliationRequest(ctx, tx, guard.OutputEpoch, guard.PositionID)
	if err != nil || !current {
		return nil, err
	}
	var event publicationReconciliationEvent
	err = tx.NewRaw(`SELECT * FROM publication_reconciliation_event WHERE operation_id=? FOR UPDATE`, guard.OperationID).Scan(ctx, &event)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if event.OutputEpoch != guard.OutputEpoch || event.PositionID != guard.PositionID || event.ObservedGeneration != guard.ObservedGeneration || event.State != "dispatched" || event.RegisteredJobID == nil || *event.RegisteredJobID != guard.RiverJobID || event.DeliveryEpoch != guard.DeliveryEpoch || event.HighestAttempt != guard.RiverAttempt || !event.Claimed || event.ClaimToken == nil || *event.ClaimToken != guard.ClaimToken {
		return nil, nil
	}
	job, err := lockPublicationReconciliationJob(ctx, tx, guard.RiverJobID)
	if err != nil || !publicationReconciliationJobMatches(job, guard.OperationID, guard.RiverAttempt) {
		return nil, err
	}
	op, err := repository.GetOperationForUpdate(ctx, tx, guard.OperationID)
	if err != nil {
		return nil, err
	}
	if op.Kind != PublicationReconciliationOperationKind || op.State != "running" || op.Attempt != guard.ApplicationAttempt || op.RiverJobID == nil || *op.RiverJobID != guard.RiverJobID {
		return nil, nil
	}
	return &event, nil
}

// lockPublicationReconciliationToolHoldOwner verifies the original application's
// claim authority without requiring River to still consider that delivery live.
// A held tool installation cannot be handed to a successor, so River state and
// attempt are deliberately not part of the release fence.
func (repository *SetupManagerRepository) lockPublicationReconciliationToolHoldOwner(ctx context.Context, tx bun.Tx, guard PublicationReconciliationDeliveryGuard) (*publicationReconciliationEvent, error) {
	if guard.OperationID == uuid.Nil || guard.ClaimToken == uuid.Nil || guard.OutputEpoch == uuid.Nil || guard.PositionID == uuid.Nil || guard.ApplicationAttempt <= 0 || guard.RiverJobID <= 0 || guard.RiverAttempt <= 0 || guard.DeliveryEpoch <= 0 || guard.ObservedGeneration <= 0 {
		return nil, fmt.Errorf("publication reconciliation requires a valid release authority")
	}
	current, err := lockPublicationReconciliationRequest(ctx, tx, guard.OutputEpoch, guard.PositionID)
	if err != nil || !current {
		return nil, err
	}
	var event publicationReconciliationEvent
	err = tx.NewRaw(`SELECT * FROM publication_reconciliation_event WHERE operation_id=? FOR UPDATE`, guard.OperationID).Scan(ctx, &event)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if event.OutputEpoch != guard.OutputEpoch || event.PositionID != guard.PositionID || event.ObservedGeneration != guard.ObservedGeneration || event.State != "dispatched" || event.RegisteredJobID == nil || *event.RegisteredJobID != guard.RiverJobID || event.DeliveryEpoch != guard.DeliveryEpoch || !event.Claimed || event.ClaimToken == nil || *event.ClaimToken != guard.ClaimToken {
		return nil, nil
	}
	job, err := lockPublicationReconciliationJob(ctx, tx, guard.RiverJobID)
	if err != nil {
		return nil, err
	}
	if job == nil || job.Kind != PublicationReconciliationOperationKind {
		return nil, nil
	}
	var args PublicationReconciliationJobArgs
	decoder := json.NewDecoder(bytes.NewReader(job.Args))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil || args.OperationID != guard.OperationID {
		return nil, nil
	}
	op, err := repository.GetOperationForUpdate(ctx, tx, guard.OperationID)
	if err != nil {
		return nil, err
	}
	if op.Kind != PublicationReconciliationOperationKind || op.Attempt != guard.ApplicationAttempt || op.RiverJobID == nil || *op.RiverJobID != guard.RiverJobID {
		return nil, nil
	}
	return &event, nil
}
