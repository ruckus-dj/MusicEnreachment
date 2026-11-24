package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type LibraryTagComparison struct {
	Provider  LibraryTagComparisonProvider
	Overrides map[string][]string
}

func (r *LibraryRepository) ReadPositionTagComparison(ctx context.Context, releaseID, positionID uuid.UUID) (LibraryTagComparison, error) {
	result := LibraryTagComparison{}
	err := r.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var position struct {
			TrackID     uuid.UUID  `bun:"track_id"`
			RecordingID *uuid.UUID `bun:"recording_id"`
		}
		err := tx.NewSelect().Table("release_track").
			Column("track_id", "recording_id").
			Where("release_id = ? AND id = ?", releaseID, positionID).
			Scan(ctx, &position)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryPositionNotFound
		}
		if err != nil {
			return fmt.Errorf("read position ownership for tag comparison: %w", err)
		}
		result.Provider, err = readLibraryTagComparisonProvider(ctx, tx, releaseID, position.TrackID)
		if err != nil {
			return fmt.Errorf("read provider tags: %w", err)
		}
		result.Overrides, err = readLibraryTagComparisonOverrides(ctx, tx, releaseID, positionID, position.TrackID, position.RecordingID)
		if err != nil {
			return fmt.Errorf("read local tag overrides: %w", err)
		}
		if result.Overrides == nil {
			result.Overrides = map[string][]string{}
		}
		return nil
	})
	if err != nil {
		return LibraryTagComparison{}, fmt.Errorf("read position tag comparison: %w", err)
	}
	return result, nil
}
