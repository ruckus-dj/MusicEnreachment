package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationSourceHoldForDelivery returns the source hold already
// owned by this live delivery. It never creates or replaces source ownership.
func (repository *SetupManagerRepository) PublicationSourceHoldForDelivery(ctx context.Context, guard PublicationDeliveryGuard) (uuid.UUID, bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return uuid.Nil, false, err
	}
	var holdUUID uuid.UUID
	valid := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, _, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "publication source hold")
		if err != nil || !live {
			return err
		}
		err = tx.NewRaw(`SELECT hold_uuid FROM publication_source_hold WHERE operation_id=? AND application_attempt=? AND river_job_id=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Scan(ctx, &holdUUID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("publication source hold: read existing hold: %w", err)
		}
		valid = true
		return nil
	})
	if err != nil {
		return uuid.Nil, false, err
	}
	return holdUUID, valid, nil
}

// GuardedPublicationDeliveryDiagnostic stores a caller-supplied safe error
// only while the exact delivery is executing. It leaves operation state and
// all delivery provenance untouched.
func (repository *SetupManagerRepository) GuardedPublicationDeliveryDiagnostic(ctx context.Context, guard PublicationDeliveryGuard, safeError string) (bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return false, err
	}
	if strings.TrimSpace(safeError) == "" {
		return false, errors.New("publication delivery diagnostic must not be empty")
	}
	changed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, operation, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "add publication delivery diagnostic")
		if err != nil || !live {
			return err
		}
		if operation.State != "running" {
			return nil
		}
		if _, err := tx.NewRaw(`UPDATE operation SET safe_error=?, updated_at=now() WHERE id=? AND state='running'`, safeError, guard.OperationID).Exec(ctx); err != nil {
			return fmt.Errorf("add publication delivery diagnostic: update operation: %w", err)
		}
		changed = true
		return nil
	})
	return changed, err
}
