package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// CheckPublicationResultCurrent verifies that a simple publication delivery
// still owns its frozen inputs immediately before filesystem installation.
func (repository *SetupManagerRepository) CheckPublicationResultCurrent(ctx context.Context, guard PublicationDeliveryGuard) (bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return false, err
	}
	current := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, _, claim, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "check publication result freshness")
		if err != nil || !live {
			return err
		}
		if claim.State != "executing" {
			return nil
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || (!input.SupportsExecution() && !validManualLyricsRewrite(input) && !validAutomaticAudioUpgrade(input)) || input.IntentKind != attempt.IntentKind {
			return nil
		}
		if validAutomaticAudioUpgrade(input) {
			var publication TrackPublication
			if err := tx.NewSelect().Model(&publication).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("check publication result freshness: read current publication: %w", err)
			}
			var authority PositionSourceAuthority
			if err := tx.NewSelect().Model(&authority).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("check publication result freshness: read current source authority: %w", err)
			}
			current = automaticAudioUpgradeCurrent(publication, authority, input, attempt.Epoch)
			return nil
		}
		if input.ManualLyrics != nil {
			var publication TrackPublication
			err := tx.NewSelect().Model(&publication).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("check publication result freshness: read current publication: %w", err)
			}
			current = manualLyricsResultCurrent(ctx, tx, attempt.PositionID, *input.ManualLyrics, publication)
			return nil
		}
		if input.AutomaticLyricsRetry != nil {
			matches, err := validateAutomaticLyricsRetryCurrentAuthority(ctx, tx, attempt.PositionID, *input.AutomaticLyricsRetry)
			if err != nil {
				return fmt.Errorf("check publication result freshness: validate automatic lyrics authority: %w", err)
			}
			if !matches {
				return nil
			}
		}
		current = true
		return nil
	})
	return current, err
}
