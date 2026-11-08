package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

type sourceRepublishTargetEvidenceKind uint8

const (
	sourceRepublishTargetObserved sourceRepublishTargetEvidenceKind = iota + 1
	sourceRepublishTargetMissing
)

type sourceRepublishTargetEvidence struct {
	kind     sourceRepublishTargetEvidenceKind
	identity PublicationFileIdentity
}

// ResumeSourceRepublishPreparation consumes inspection mode only after the
// guarded delivery has validated that CURRENT and its frozen source, lyrics,
// and cover authorities are still current. observed is the service's typed
// filesystem observation of the restored old target.
func (repository *SetupManagerRepository) ResumeSourceRepublishPreparation(
	ctx context.Context,
	guard PublicationDeliveryGuard,
	observed PublicationFileIdentity,
) (bool, error) {
	return repository.resumeSourceRepublishPreparation(ctx, guard, sourceRepublishTargetEvidence{
		kind: sourceRepublishTargetObserved, identity: observed,
	})
}

// ResumeMissingSourceRepublishPreparation consumes inspection mode after the
// caller has established that the CURRENT target is genuinely absent using a
// no-follow filesystem lookup. The database validates all retained authority;
// it does not infer filesystem absence from an empty identity.
func (repository *SetupManagerRepository) ResumeMissingSourceRepublishPreparation(
	ctx context.Context,
	guard PublicationDeliveryGuard,
) (bool, error) {
	return repository.resumeSourceRepublishPreparation(ctx, guard, sourceRepublishTargetEvidence{
		kind: sourceRepublishTargetMissing,
	})
}

func (repository *SetupManagerRepository) resumeSourceRepublishPreparation(
	ctx context.Context,
	guard PublicationDeliveryGuard,
	evidence sourceRepublishTargetEvidence,
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
			*attempt.ExpectedGeneration != input.SourceRepublish.Current.Generation {
			return nil
		}
		switch evidence.kind {
		case sourceRepublishTargetObserved:
			if !sameJSONValue(mustPublicationJSON(evidence.identity), mustPublicationJSON(input.SourceRepublish.Current.Ownership)) {
				return nil
			}
		case sourceRepublishTargetMissing:
		default:
			return errors.New("resume source-republish preparation: target evidence is invalid")
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
		if _, err := validateSourceRepublishAttemptNamespace(ctx, tx, attempt, input); err != nil {
			if errors.Is(err, ErrPublicationAdmissionConflict) || errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("resume source-republish preparation: validate CURRENT namespace: %w", err)
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
