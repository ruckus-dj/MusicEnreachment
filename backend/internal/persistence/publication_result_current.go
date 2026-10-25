package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
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
		if err := lockProviderTagDeliveryAuthority(ctx, tx, guard); err != nil {
			return err
		}
		attempt, _, claim, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "check publication result freshness")
		if err != nil || !live {
			return err
		}
		if claim.State != "executing" {
			return nil
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || (!input.SupportsExecution() && !validManualLyricsRewrite(input) && !validAutomaticAudioUpgrade(input) && !validProviderTagRewrite(input)) || input.IntentKind != attempt.IntentKind {
			return nil
		}
		if validProviderTagRewrite(input) {
			var publication TrackPublication
			if err := tx.NewSelect().Model(&publication).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("check publication result freshness: read current publication: %w", err)
			}
			current = providerTagCurrent(publication, input, attempt.Epoch)
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

func validProviderTagRewrite(input PublicationInputs) bool {
	return input.Version == PublicationProviderTagInputsVersion && input.IntentKind == "rewrite" && input.Rewrite != nil && input.ProviderTags != nil && input.Validate() == nil
}

func providerTagCurrent(current TrackPublication, input PublicationInputs, epoch uuid.UUID) bool {
	provider := input.ProviderTags
	var ownership PublicationCurrentOwnership
	if provider == nil || current.Epoch != epoch || current.Generation != provider.Current.Generation || current.RelativePath != provider.Current.RelativePath ||
		decodePublicationCurrentContract(current.Ownership, &ownership) != nil || !sameJSONValue(mustPublicationJSON(ownership.Identity), mustPublicationJSON(provider.Current.Ownership)) || !sameJSONValue(current.ActualAssets, mustPublicationJSON(provider.Current.Assets)) {
		return false
	}
	var metadata PublicationCommitMetadata
	if strictMetadata(current.ActualMetadata, &metadata) != nil || !sameJSONValue(mustPublicationJSON(metadata.Tags), mustPublicationJSON(provider.Current.Tags)) {
		return false
	}
	facts, ok := publicationAudioFactsFromObservation(current.ActualAudio)
	return ok && sameJSONValue(mustPublicationJSON(facts), mustPublicationJSON(provider.Current.AudioFacts))
}

func providerTagResultCurrent(current TrackPublication, observation PublicationStageObservation, input PublicationInputs) bool {
	if !providerTagCurrent(current, input, current.Epoch) {
		return false
	}
	var before, after PublicationCommitMetadata
	if strictMetadata(current.ActualMetadata, &before) != nil || strictMetadata(observation.ActualMetadata, &after) != nil || !sameJSONValue(mustPublicationJSON(after.Tags), mustPublicationJSON(input.DesiredTags)) {
		return false
	}
	beforeAssets, err := DecodePublicationAssetOutcomes(current.ActualAssets)
	if err != nil {
		return false
	}
	afterAssets, err := DecodePublicationAssetOutcomes(observation.ActualAssets)
	if err != nil || !sameJSONValue(mustPublicationJSON(beforeAssets), mustPublicationJSON(afterAssets)) {
		return false
	}
	beforeFacts, ok := publicationAudioFactsFromObservation(current.ActualAudio)
	afterFacts, afterOK := publicationAudioFactsFromObservation(observation.ActualAudio)
	return ok && afterOK && beforeFacts.Codec == afterFacts.Codec && beforeFacts.Container == afterFacts.Container &&
		beforeFacts.Lossless != nil && afterFacts.Lossless != nil && *beforeFacts.Lossless == *afterFacts.Lossless &&
		sameOptionalInt(beforeFacts.Channels, afterFacts.Channels) && beforeFacts.ChannelLayout == afterFacts.ChannelLayout &&
		sameOptionalInt(beforeFacts.BitDepth, afterFacts.BitDepth) && sameOptionalInt(beforeFacts.SampleRate, afterFacts.SampleRate)
}

func sameOptionalInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
