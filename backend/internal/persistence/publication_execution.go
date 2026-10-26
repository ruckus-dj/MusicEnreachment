package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationDeliveryGuard identifies one publication delivery and the exact
// frozen attempt it is permitted to execute. It is a database claim only; a
// caller must also coordinate local mutators and prove filesystem ownership.
type PublicationDeliveryGuard struct {
	OperationID         uuid.UUID
	ApplicationAttempt  int
	RiverJobID          int64
	RiverAttempt        int
	ClaimUUID           uuid.UUID
	BootUUID            uuid.UUID
	DeliveryToken       string
	Epoch               uuid.UUID
	CandidateGeneration uuid.UUID
	ExpectedPositionID  uuid.UUID
}

type PublicationDeliveryClaimOutcome string

const (
	PublicationDeliveryAcquired     PublicationDeliveryClaimOutcome = "acquired"
	PublicationDeliveryAlreadyOwned PublicationDeliveryClaimOutcome = "already_owned"
	PublicationDeliveryBlocked      PublicationDeliveryClaimOutcome = "blocked"
)

var errInvalidPublicationDeliveryGuard = errors.New("invalid publication delivery guard")

// ClaimPublicationDelivery durably claims a queued delivery or a River retry
// of the same still-live operation. A retry or redelivery gets a new delivery
// identity while retaining the attempt's frozen inputs and other admission
// state. A fresh identity replaces a claim from the same or an older River
// attempt, but never a newer one.
func (repository *SetupManagerRepository) ClaimPublicationDelivery(ctx context.Context, guard PublicationDeliveryGuard) (PublicationDeliveryClaimOutcome, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return PublicationDeliveryBlocked, err
	}
	outcome := PublicationDeliveryBlocked
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Lock order: output gate, position, attempt, operation, River job.
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("claim publication delivery: acquire output admission: %w", err)
		}
		var epoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim publication delivery: read namespace epoch: %w", err)
		}
		if epoch != guard.Epoch {
			return nil
		}

		var lockedPosition uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id=? FOR UPDATE`, guard.ExpectedPositionID).Scan(ctx, &lockedPosition); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim publication delivery: lock position: %w", err)
		}

		attempt := new(TrackPublicationAttempt)
		if err := tx.NewSelect().Model(attempt).Where("operation_id = ?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim publication delivery: lock publication attempt: %w", err)
		}
		op := new(Operation)
		if err := tx.NewSelect().Model(op).Where("id = ?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim publication delivery: lock operation: %w", err)
		}
		var riverState string
		var riverAttempt int
		if err := tx.NewRaw(`SELECT state, attempt FROM river_job WHERE id=? FOR UPDATE`, guard.RiverJobID).Scan(ctx, &riverState, &riverAttempt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim publication delivery: lock River job: %w", err)
		}
		if op.Kind != "publish_track_v1" || (op.State != "queued" && op.State != "running") ||
			op.Attempt != guard.ApplicationAttempt || op.RiverJobID == nil || *op.RiverJobID != guard.RiverJobID ||
			riverState != "running" || riverAttempt != guard.RiverAttempt ||
			attempt.PositionID != guard.ExpectedPositionID || attempt.Epoch != guard.Epoch ||
			attempt.CandidateGeneration != guard.CandidateGeneration {
			return nil
		}

		var currentGeneration *uuid.UUID
		err := tx.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, guard.ExpectedPositionID).Scan(ctx, &currentGeneration)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("claim publication delivery: read current publication: %w", err)
		}
		if !sameOptionalUUID(currentGeneration, attempt.ExpectedGeneration) {
			return nil
		}

		var existing []PublicationExecutionClaim
		err = tx.NewSelect().Model(&existing).Where("operation_id = ?", guard.OperationID).For("UPDATE").Scan(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("claim publication delivery: read existing claim: %w", err)
		}
		if len(existing) > 0 {
			claim := existing[0]
			if publicationClaimMatches(claim, guard) && claim.State == "executing" {
				outcome = PublicationDeliveryAlreadyOwned
				return nil
			}
			// A running delivery of this exact operation and application attempt may
			// replace its prior delivery identity regardless of that claim's state.
			// River returns interrupted jobs at the same attempt after shutdown, so
			// an equal attempt is a legitimate redelivery; only an older attempt is
			// stale. The River row was locked and matched to guard above.
			if op.State != "running" || attempt.Phase == "committed" || attempt.Phase == "cleaning" || attempt.Phase == "cleanup_pending" ||
				claim.ApplicationAttempt != guard.ApplicationAttempt ||
				claim.RiverJobID != guard.RiverJobID || claim.RiverAttempt > guard.RiverAttempt || claim.Epoch != guard.Epoch ||
				len(attempt.FrozenInputs) == 0 {
				return nil
			}
			if _, err := tx.NewRaw(`UPDATE publication_execution_claim
					SET river_attempt=?, claim_uuid=?, boot_uuid=?, delivery_token=?, state='executing'
					WHERE operation_id=? AND application_attempt=?`, guard.RiverAttempt, guard.ClaimUUID, guard.BootUUID,
				guard.DeliveryToken, guard.OperationID, guard.ApplicationAttempt).Exec(ctx); err != nil {
				return fmt.Errorf("claim publication delivery: replace prior delivery: %w", err)
			}
			if _, err := tx.NewRaw(`UPDATE operation SET stage='preparing', updated_at=now() WHERE id=? AND state='running'`, guard.OperationID).Exec(ctx); err != nil {
				return fmt.Errorf("claim publication delivery: restart operation preparation: %w", err)
			}
			if _, err := tx.NewRaw(`UPDATE track_publication_attempt SET phase='preparing' WHERE operation_id=?`, guard.OperationID).Exec(ctx); err != nil {
				return fmt.Errorf("claim publication delivery: restart attempt preparation: %w", err)
			}
			outcome = PublicationDeliveryAcquired
			return nil
		}
		// A running operation without its durable claim cannot safely be borrowed.
		if op.State != "queued" || attempt.Phase != "queued" {
			return nil
		}
		claim := PublicationExecutionClaim{
			OperationID: guard.OperationID, ApplicationAttempt: guard.ApplicationAttempt,
			RiverJobID: guard.RiverJobID, RiverAttempt: guard.RiverAttempt,
			ClaimUUID: guard.ClaimUUID, BootUUID: guard.BootUUID,
			DeliveryToken: guard.DeliveryToken, Epoch: guard.Epoch, State: "executing",
		}
		if _, err := tx.NewInsert().Model(&claim).Exec(ctx); err != nil {
			return fmt.Errorf("claim publication delivery: insert claim: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE operation SET state='running', stage='preparing', started_at=COALESCE(started_at, now()), updated_at=now() WHERE id=? AND state='queued'`, guard.OperationID).Exec(ctx); err != nil {
			return fmt.Errorf("claim publication delivery: start operation: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE track_publication_attempt SET phase='preparing' WHERE operation_id=? AND phase='queued'`, guard.OperationID).Exec(ctx); err != nil {
			return fmt.Errorf("claim publication delivery: prepare publication attempt: %w", err)
		}
		outcome = PublicationDeliveryAcquired
		return nil
	})
	if err != nil {
		return PublicationDeliveryBlocked, err
	}
	return outcome, nil
}

// ReadPublicationAttempt returns the persisted attempt as-is. It does not
// synthesize settled attempts or reconstruct frozen inputs.
func (repository *SetupManagerRepository) ReadPublicationAttempt(ctx context.Context, operationID uuid.UUID) (*TrackPublicationAttempt, error) {
	attempt := new(TrackPublicationAttempt)
	if err := repository.db.NewSelect().Model(attempt).Where("operation_id = ?", operationID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read publication attempt: %w", err)
	}
	return attempt, nil
}

func validatePublicationDeliveryGuard(guard PublicationDeliveryGuard) error {
	if guard.OperationID == uuid.Nil || guard.ApplicationAttempt <= 0 || guard.RiverJobID <= 0 || guard.RiverAttempt <= 0 ||
		guard.ClaimUUID == uuid.Nil || guard.BootUUID == uuid.Nil || guard.DeliveryToken == "" || guard.Epoch == uuid.Nil ||
		guard.CandidateGeneration == uuid.Nil || guard.ExpectedPositionID == uuid.Nil {
		return errInvalidPublicationDeliveryGuard
	}
	return nil
}

func publicationClaimMatches(claim PublicationExecutionClaim, guard PublicationDeliveryGuard) bool {
	return claim.OperationID == guard.OperationID && claim.ApplicationAttempt == guard.ApplicationAttempt &&
		claim.RiverJobID == guard.RiverJobID && claim.RiverAttempt == guard.RiverAttempt &&
		claim.ClaimUUID == guard.ClaimUUID && claim.BootUUID == guard.BootUUID &&
		claim.DeliveryToken == guard.DeliveryToken && claim.Epoch == guard.Epoch
}
