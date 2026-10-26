package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
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
		if err := lockProviderTagDeliveryAuthority(ctx, tx, guard); err != nil {
			return err
		}
		attempt, operation, _, ok, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "read publication preparation inputs")
		if err != nil || !ok || attempt.Phase != "preparing" || operation.Stage != "preparing" {
			return err
		}
		decoded, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil {
			return fmt.Errorf("read publication preparation inputs: decode frozen inputs: %w", err)
		}
		if !decoded.SupportsExecution() && !validProviderTagRewrite(decoded) && (!manualLyricsFLAC || !validManualLyricsRewrite(decoded)) {
			return fmt.Errorf("read publication preparation inputs: frozen publication contract is admitted but not executable")
		}
		if decoded.ProviderTags != nil {
			var current TrackPublication
			if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("read provider-tag preparation CURRENT: %w", err)
			}
			if !providerTagCurrent(current, decoded, guard.Epoch) {
				return nil
			}
		}
		if decoded.AudioUpgrade != nil {
			if attempt.ExpectedGeneration == nil || *attempt.ExpectedGeneration != decoded.AudioUpgrade.Current.Generation {
				return nil
			}
			var current TrackPublication
			if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("read publication preparation inputs: lock audio upgrade CURRENT: %w", err)
			}
			var authority PositionSourceAuthority
			if err := tx.NewSelect().Model(&authority).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("read publication preparation inputs: lock audio upgrade authority: %w", err)
			}
			if !automaticAudioUpgradeCurrent(current, authority, decoded, guard.Epoch) {
				return nil
			}
		}
		if manualLyricsFLAC {
			if !validManualLyricsRewrite(decoded) || attempt.IntentKind != "rewrite" || attempt.Epoch != guard.Epoch || attempt.CandidateGeneration != guard.CandidateGeneration || attempt.PositionID != guard.ExpectedPositionID {
				return nil
			}
			var current TrackPublication
			if err := tx.NewSelect().Model(&current).Where("position_id=?", guard.ExpectedPositionID).For("UPDATE").Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("read publication preparation inputs: read current publication: %w", err)
			}
			if !manualLyricsRewriteMatchesCurrent(current, guard.Epoch, *decoded.Rewrite) {
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

func validManualLyricsRewrite(input PublicationInputs) bool {
	if input.Rewrite == nil {
		return false
	}
	facts := input.Rewrite.AudioFacts
	containerCodec := facts.Container == publication.AudioContainerFLAC && facts.Codec == "flac" ||
		facts.Container == publication.AudioContainerMKA && facts.Codec == "mp3"
	return input.Version == PublicationInputsVersion && input.QualityPolicyVersion == publication.QualityPolicyVersion &&
		input.IntentKind == "rewrite" && input.Rewrite != nil && input.ManualLyrics != nil && input.Audio == nil &&
		input.Format == publication.AudioFormatSource && containerCodec && facts.SupportedSourceContainer && input.Rewrite.Ownership.SHA256 != ""
}

func manualLyricsRewriteMatchesCurrent(current TrackPublication, epoch uuid.UUID, rewrite PublicationRewriteInput) bool {
	if !publicationRewriteMatchesCurrent(current, epoch, rewrite) {
		return false
	}
	if rewrite.AudioFacts.Container != publication.AudioContainerMKA {
		return true
	}
	assets, err := DecodePublicationAssetOutcomes(current.ActualAssets)
	return err == nil && assets.Cover.State == PublicationAssetAbsent
}
