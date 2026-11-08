package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// ResumeSourceRepublishPreparation consumes inspection mode only after the
// guarded delivery has validated that CURRENT and its frozen source, lyrics,
// and cover authorities are still current. observed is the service's typed
// filesystem observation of the restored old target.
func (repository *SetupManagerRepository) ResumeSourceRepublishPreparation(
	ctx context.Context,
	guard PublicationDeliveryGuard,
	observed PublicationFileIdentity,
) (bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return false, err
	}
	resumed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockProviderTagDeliveryAuthority(ctx, tx, guard); err != nil {
			return err
		}
		attempt, _, claim, live, err := lockAndValidatePublicationDelivery(
			ctx, tx, guard, "resume source-republish preparation",
		)
		if err != nil || !live {
			return err
		}
		if claim.State != "executing" || attempt.ResumeMode != PublicationResumeModeInspectSourceRepublishTarget {
			return nil
		}

		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil {
			return fmt.Errorf("resume source-republish preparation: decode frozen inputs: %w", err)
		}
		if !validSourceRepublishPreparation(input) || input.SourceRepublish == nil ||
			attempt.IntentKind != input.IntentKind || attempt.ExpectedGeneration == nil ||
			*attempt.ExpectedGeneration != input.SourceRepublish.Current.Generation ||
			!sameJSONValue(mustPublicationJSON(observed), mustPublicationJSON(input.SourceRepublish.Current.Ownership)) {
			return nil
		}

		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("resume source-republish preparation: read CURRENT: %w", err)
		}
		var authority PositionSourceAuthority
		if err := tx.NewSelect().Model(&authority).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("resume source-republish preparation: read source authority: %w", err)
		}
		if !sourceRepublishCurrent(current, authority, input, guard.Epoch) {
			return nil
		}
		lyricsCurrent, err := validatePublicationLyricsCurrentAuthority(ctx, tx, attempt.PositionID,
			input.SourceRepublish.LyricsAuthority.ExpectedMetadataRevision, input.SourceRepublish.LyricsAuthority.Desired)
		if err != nil {
			return fmt.Errorf("resume source-republish preparation: validate lyrics authority: %w", err)
		}
		if !lyricsCurrent {
			return nil
		}
		coverCurrent, err := validateSourceRepublishCoverAuthorityTx(ctx, tx, attempt.PositionID, input.SourceRepublish.Cover, false)
		if err != nil {
			return fmt.Errorf("resume source-republish preparation: validate cover authority: %w", err)
		}
		if !coverCurrent {
			return nil
		}

		result, err := tx.NewRaw(`UPDATE track_publication_attempt SET resume_mode=?,phase='preparing'
			WHERE operation_id=? AND resume_mode=?`,
			PublicationResumeModePrepare, guard.OperationID, PublicationResumeModeInspectSourceRepublishTarget).Exec(ctx)
		if err != nil {
			return fmt.Errorf("resume source-republish preparation: clear inspection mode: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("resume source-republish preparation: count cleared inspection mode: %w", err)
		} else if affected != 1 {
			return nil
		}
		operationResult, err := tx.NewRaw(`UPDATE operation SET stage='preparing',updated_at=now()
			WHERE id=? AND state='running'`, guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("resume source-republish preparation: restore preparation stage: %w", err)
		}
		if affected, err := operationResult.RowsAffected(); err != nil {
			return fmt.Errorf("resume source-republish preparation: count restored preparation stage: %w", err)
		} else if affected != 1 {
			return errors.New("resume source-republish preparation: operation is no longer running")
		}
		resumed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return resumed, nil
}
