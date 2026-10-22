package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// compareAndSetPositionSourceAuthorityTx binds one authority revision to the
// publication generation. A nil expected revision means authority is unknown.
func compareAndSetPositionSourceAuthorityTx(ctx context.Context, tx bun.Tx, expectedRevision *uuid.UUID, authority PositionSourceAuthority) (bool, error) {
	if authority.PositionID == uuid.Nil || authority.Revision == uuid.Nil || authority.PublicationGeneration == uuid.Nil || authority.PublicationEpoch == uuid.Nil || authority.SourceLocationID == uuid.Nil || authority.MediaVariantID == uuid.Nil || (authority.Mode != "manual" && authority.Mode != "automatic") {
		return false, fmt.Errorf("compare position source authority: invalid authority")
	}
	var current struct {
		Generation uuid.UUID `bun:"generation,type:uuid"`
		Epoch      uuid.UUID `bun:"epoch,type:uuid"`
	}
	if err := tx.NewRaw(`SELECT generation,epoch FROM track_publication WHERE position_id=? FOR UPDATE`, authority.PositionID).Scan(ctx, &current); err != nil || current.Generation != authority.PublicationGeneration || current.Epoch != authority.PublicationEpoch {
		return false, fmt.Errorf("compare position source authority: current publication changed: %w", ErrPublicationAdmissionConflict)
	}
	var result sql.Result
	var err error
	if expectedRevision == nil {
		result, err = tx.NewRaw(`INSERT INTO position_source_authority(position_id,mode,revision,publication_epoch,publication_generation,source_location_id,media_variant_id,updated_at) VALUES (?,?,?,?,?,?,?,?) ON CONFLICT (position_id) DO NOTHING`, authority.PositionID, authority.Mode, authority.Revision, authority.PublicationEpoch, authority.PublicationGeneration, authority.SourceLocationID, authority.MediaVariantID, time.Now().UTC()).Exec(ctx)
	} else {
		result, err = tx.NewRaw(`UPDATE position_source_authority SET mode=?,revision=?,publication_epoch=?,publication_generation=?,source_location_id=?,media_variant_id=?,updated_at=? WHERE position_id=? AND revision=?`, authority.Mode, authority.Revision, authority.PublicationEpoch, authority.PublicationGeneration, authority.SourceLocationID, authority.MediaVariantID, time.Now().UTC(), authority.PositionID, *expectedRevision).Exec(ctx)
	}
	if err != nil {
		return false, fmt.Errorf("compare position source authority: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// carryPositionSourceAuthorityTx advances only an existing, generation-matched
// current authority record. Missing legacy evidence remains unknown.
func carryPositionSourceAuthorityTx(ctx context.Context, tx bun.Tx, previous, next TrackPublication) error {
	var authority PositionSourceAuthority
	err := tx.NewSelect().Model(&authority).Where("position_id = ?", previous.PositionID).For("UPDATE").Scan(ctx)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("carry position source authority: read current authority: %w", err)
	}
	if authority.PublicationEpoch != previous.Epoch || authority.PublicationGeneration != previous.Generation {
		return fmt.Errorf("carry position source authority: current authority is stale: %w", ErrPublicationAdmissionConflict)
	}
	authority.PublicationEpoch = next.Epoch
	authority.PublicationGeneration = next.Generation
	updated, err := compareAndSetPositionSourceAuthorityTx(ctx, tx, &authority.Revision, authority)
	if err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("carry position source authority: revision changed: %w", ErrPublicationAdmissionConflict)
	}
	return nil
}

// ReadPositionSourceAuthority returns nil for unknown or stale evidence. In
// particular, a legacy publication is never assigned authority by inference.
func (repository *SetupManagerRepository) ReadPositionSourceAuthority(ctx context.Context, positionID uuid.UUID) (*PositionSourceAuthority, error) {
	var authority PositionSourceAuthority
	err := repository.db.NewSelect().Model(&authority).Join("JOIN track_publication AS current ON current.position_id = position_source_authority.position_id").
		Where("position_source_authority.position_id = ? AND position_source_authority.publication_epoch = current.epoch AND position_source_authority.publication_generation = current.generation", positionID).Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read position source authority: %w", err)
	}
	return &authority, nil
}
