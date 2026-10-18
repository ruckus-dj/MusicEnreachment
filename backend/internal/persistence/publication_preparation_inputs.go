package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

// PublicationPreparationInputs returns frozen inputs only while the complete
// delivery guard is current and the attempt remains in preparation.
func (repository *SetupManagerRepository) PublicationPreparationInputs(ctx context.Context, guard PublicationDeliveryGuard) (PublicationInputs, bool, error) {
	return repository.publicationPreparationInputs(ctx, guard, false)
}

// PublicationManualLyricsPreparationInputs is a private-capability reader for
// the one manual-lyrics FLAC rewrite executor. It is intentionally not part of
// any HTTP-facing repository contract.
func (repository *SetupManagerRepository) PublicationManualLyricsPreparationInputs(ctx context.Context, guard PublicationDeliveryGuard) (PublicationInputs, bool, error) {
	return repository.publicationPreparationInputs(ctx, guard, true)
}

func (repository *SetupManagerRepository) publicationPreparationInputs(ctx context.Context, guard PublicationDeliveryGuard, manualLyricsFLAC bool) (PublicationInputs, bool, error) {
	var input PublicationInputs
	valid := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, ok, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "read publication preparation inputs")
		if err != nil || !ok || attempt.Phase != "preparing" || operation.Stage != "preparing" {
			return err
		}
		decoded, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil {
			return fmt.Errorf("read publication preparation inputs: decode frozen inputs: %w", err)
		}
		if !decoded.SupportsExecution() && (!manualLyricsFLAC || !validManualLyricsFLACRewrite(decoded)) {
			return fmt.Errorf("read publication preparation inputs: frozen publication contract is admitted but not executable")
		}
		if manualLyricsFLAC {
			if !validManualLyricsFLACRewrite(decoded) || attempt.IntentKind != "rewrite" || attempt.Epoch != guard.Epoch || attempt.CandidateGeneration != guard.CandidateGeneration || attempt.PositionID != guard.ExpectedPositionID {
				return nil
			}
			var current TrackPublication
			if err := tx.NewSelect().Model(&current).Where("position_id=?", guard.ExpectedPositionID).For("UPDATE").Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("read publication preparation inputs: read current publication: %w", err)
			}
			if !publicationRewriteMatchesCurrent(current, guard.Epoch, *decoded.Rewrite) {
				return nil
			}
			if err := validatePublicationRewriteNamespace(ctx, tx, attempt, decoded.Rewrite.RelativePath); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("read publication preparation inputs: validate rewrite namespace: %w", err)
			}
		}
		input, valid = decoded, true
		return nil
	})
	if err != nil {
		return PublicationInputs{}, false, err
	}
	if !valid {
		return PublicationInputs{}, false, nil
	}
	if err := input.Validate(); err != nil {
		return PublicationInputs{}, false, errors.New("read publication preparation inputs: frozen inputs are invalid")
	}
	return input.Clone(), true, nil
}

func validManualLyricsFLACRewrite(input PublicationInputs) bool {
	return input.Version == PublicationInputsVersion3 && input.QualityPolicyVersion == publication.QualityPolicyVersion &&
		input.IntentKind == "rewrite" && input.Rewrite != nil && input.ManualLyrics != nil && input.Audio == nil &&
		input.Format == publication.AudioFormatSource && input.Rewrite.AudioFacts.Container == publication.AudioContainerFLAC &&
		input.Rewrite.AudioFacts.Codec == "flac" && input.Rewrite.AudioFacts.SupportedSourceContainer && input.Rewrite.Ownership.SHA256 != ""
}

// PublicationRewritePreparationFacts are the persisted facts which authorize
// copying the current published artifact for a rewrite.
type PublicationRewritePreparationFacts struct {
	Input PublicationInputs
}

// CheckPublicationRewriteForDelivery validates the frozen rewrite contract and
// current publication while holding the normal publication lock order.
func (repository *SetupManagerRepository) CheckPublicationRewriteForDelivery(ctx context.Context, guard PublicationDeliveryGuard) (PublicationRewritePreparationFacts, bool, error) {
	var facts PublicationRewritePreparationFacts
	valid := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, ok, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "check publication rewrite")
		if err != nil || !ok || (attempt.Phase != "preparing" && attempt.Phase != "staged") || operation.Stage != attempt.Phase {
			return err
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil {
			return fmt.Errorf("check publication rewrite: decode frozen inputs: %w", err)
		}
		if input.IntentKind != "rewrite" || input.Rewrite == nil || attempt.IntentKind != "rewrite" || attempt.Epoch != guard.Epoch || attempt.CandidateGeneration != guard.CandidateGeneration || attempt.PositionID != guard.ExpectedPositionID {
			return nil
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", guard.ExpectedPositionID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("check publication rewrite: read current publication: %w", err)
		}
		if !publicationRewriteMatchesCurrent(current, guard.Epoch, *input.Rewrite) {
			return nil
		}
		facts, valid = PublicationRewritePreparationFacts{Input: input.Clone()}, true
		return nil
	})
	if err != nil {
		return PublicationRewritePreparationFacts{}, false, err
	}
	return facts, valid, nil
}
