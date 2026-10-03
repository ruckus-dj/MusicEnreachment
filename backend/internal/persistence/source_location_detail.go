package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// SourceLocationDetailSnapshot contains the rows that make up one consistent
// inspector read. Optional rows are nil when the location has no result or
// active analysis.
type SourceLocationDetailSnapshot struct {
	Root              *SourceRoot
	Location          *SourceLocation
	Variant           *MediaVariant
	ActiveOperationID *uuid.UUID
}

// ReadSourceLocationDetail reads the root, owned location, saved result and
// active analysis from one read-only repeatable-read snapshot. It takes no row or
// table locks, so unlinking or deleting an orphan variant cannot block the read.
func (repository *SourceInventoryRepository) ReadSourceLocationDetail(ctx context.Context, rootID, locationID uuid.UUID) (*SourceLocationDetailSnapshot, error) {
	var snapshot *SourceLocationDetailSnapshot
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		root := new(SourceRoot)
		if err := tx.NewSelect().Model(root).Where("id = ?", rootID).Scan(ctx); err != nil {
			return fmt.Errorf("read source location detail root: %w", err)
		}
		location := new(SourceLocation)
		if err := tx.NewSelect().Model(location).Where("id = ?", locationID).
			Where("source_root_id = ?", rootID).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("read source location detail location: %w", ErrSourceLocationNotFound)
			}
			return fmt.Errorf("read source location detail location: %w", err)
		}
		var variant *MediaVariant
		if location.MediaVariantID != nil {
			variant = new(MediaVariant)
			if err := tx.NewRaw("SELECT * FROM media_variant WHERE id = ? /* source_location_detail_variant */", *location.MediaVariantID).Scan(ctx, variant); err != nil {
				if err == sql.ErrNoRows {
					return fmt.Errorf("read source location detail variant: %w", ErrMediaVariantNotFound)
				}
				return fmt.Errorf("read source location detail variant: %w", err)
			}
		}
		var activeID uuid.UUID
		err := tx.NewRaw(
			`SELECT id FROM operation /* source_location_detail_active */
			 WHERE kind = ? AND state IN ('queued', 'running')
			   AND target_source_root_id = ? AND target_source_location_id = ?
			 ORDER BY created_at DESC
			 LIMIT 1`,
			analysisSourceOperationKind, rootID, locationID,
		).Scan(ctx, &activeID)
		var active *uuid.UUID
		if err == nil {
			active = &activeID
		} else if err != sql.ErrNoRows {
			return fmt.Errorf("read source location detail active analysis: %w", err)
		}
		snapshot = &SourceLocationDetailSnapshot{Root: root, Location: location, Variant: variant, ActiveOperationID: active}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read source location detail: %w", err)
	}
	return snapshot, nil
}
