package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var ErrSourceCleanupOwned = errors.New("source variant is owned by cleanup")

// rejectCleanupOwnedSourceVariant protects track/source associations while a
// cleanup owner retains the work identity for the variant. Owners remain
// authoritative independently of the associated operation's state.
func rejectCleanupOwnedSourceVariant(ctx context.Context, tx bun.IDB, variantID uuid.UUID) error {
	var owned bool
	err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1
		FROM source_work_variant_identity identity
		JOIN source_cleanup_owner owner ON owner.work_id=identity.work_id
		WHERE identity.media_variant_id=?
	)`, variantID).Scan(ctx, &owned)
	if err != nil {
		return fmt.Errorf("check source cleanup ownership: %w", err)
	}
	if owned {
		return ErrSourceCleanupOwned
	}
	return nil
}
