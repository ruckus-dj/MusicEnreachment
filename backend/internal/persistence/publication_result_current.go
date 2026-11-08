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
		if err != nil || (!input.SupportsExecution() && !validManualLyricsRewrite(input) && !validAutomaticAudioUpgrade(input) && !validProviderTagRewrite(input) && !validSourceRepublishPreparation(input)) || input.IntentKind != attempt.IntentKind {
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
		if validSourceRepublishPreparation(input) {
			if attempt.ExpectedGeneration == nil || *attempt.ExpectedGeneration != input.SourceRepublish.Current.Generation {
				return nil
			}
			var publication TrackPublication
			if err := tx.NewSelect().Model(&publication).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("check publication result freshness: read source-republish CURRENT: %w", err)
			}
			var authority PositionSourceAuthority
			if err := tx.NewSelect().Model(&authority).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("check publication result freshness: read source-republish authority: %w", err)
			}
			if !sourceRepublishCurrent(publication, authority, input, attempt.Epoch) {
				return nil
			}
			lyricsCurrent, err := validatePublicationLyricsCurrentAuthority(ctx, tx, attempt.PositionID,
				input.SourceRepublish.LyricsAuthority.ExpectedMetadataRevision, input.SourceRepublish.LyricsAuthority.Desired)
			if err != nil {
				return fmt.Errorf("check publication result freshness: validate source-republish lyrics authority: %w", err)
			}
			if !lyricsCurrent {
				return nil
			}
			coverCurrent, err := validateSourceRepublishCoverAuthorityTx(ctx, tx, attempt.PositionID, input.SourceRepublish.Cover, false)
			if err != nil {
				return fmt.Errorf("check publication result freshness: validate source-republish cover authority: %w", err)
			}
			current = coverCurrent
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

func validSourceRepublishPreparation(input PublicationInputs) bool {
	return input.Version == PublicationSourceRepublishInputsVersion && input.IntentKind == "audio" &&
		input.SourceRepublish != nil && input.SourceRepublish.SourceAuthority.Present && input.Validate() == nil
}

func sourceRepublishCurrent(current TrackPublication, authority PositionSourceAuthority, input PublicationInputs, epoch uuid.UUID) bool {
	if !validSourceRepublishPreparation(input) || input.SourceRepublish.SourceAuthority.PositionID != current.PositionID ||
		!publicationCurrentMatchesWitness(current, input.SourceRepublish.Current, epoch) {
		return false
	}
	frozen := input.SourceRepublish.SourceAuthority
	return frozen.Validate(input.SourceRepublish.Current.Generation) == nil &&
		authority.PositionID == frozen.PositionID && authority.Mode == frozen.Mode && authority.Revision == frozen.Revision &&
		authority.PublicationEpoch == frozen.PublicationEpoch && authority.PublicationGeneration == frozen.PublicationGeneration &&
		authority.SourceLocationID == frozen.SourceLocationID && authority.MediaVariantID == frozen.MediaVariantID &&
		authority.PublicationEpoch == epoch && authority.PublicationGeneration == current.Generation
}

// validateSourceRepublishCoverAuthorityTx compares frozen selected-cover
// authority in the caller's guarded transaction. Result commits materialize
// implicit defaults and lock the row; freshness readers instead hold a SHARE
// table lock so an implicit default cannot race a concurrent insertion.
func validateSourceRepublishCoverAuthorityTx(
	ctx context.Context,
	tx bun.Tx,
	positionID uuid.UUID,
	frozen PublicationRepublishCover,
	materialize bool,
) (bool, error) {
	var releaseID uuid.UUID
	if err := tx.NewRaw(`SELECT release_id FROM release_track WHERE id=? FOR UPDATE`, positionID).Scan(ctx, &releaseID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("lock source-republish cover position: %w", err)
	}
	if releaseID != frozen.ReleaseID {
		return false, nil
	}
	if materialize {
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_publication_cover(release_id) VALUES (?) ON CONFLICT (release_id) DO NOTHING`, frozen.ReleaseID); err != nil {
			return false, fmt.Errorf("materialize source-republish cover baseline: %w", err)
		}
		var locked uuid.UUID
		if err := tx.NewRaw(`SELECT release_id FROM release_publication_cover WHERE release_id=? FOR UPDATE`, frozen.ReleaseID).Scan(ctx, &locked); err != nil {
			return false, fmt.Errorf("lock source-republish cover authority: %w", err)
		}
	} else if _, err := tx.ExecContext(ctx, `LOCK TABLE release_publication_cover IN SHARE MODE`); err != nil {
		return false, fmt.Errorf("serialize source-republish cover freshness: %w", err)
	}
	cover, err := readReleaseCover(ctx, tx, frozen.ReleaseID)
	if errors.Is(err, sql.ErrNoRows) {
		cover = ReleaseCover{ReleaseID: frozen.ReleaseID, Revision: 1, Mode: "automatic"}
	} else if err != nil {
		return false, fmt.Errorf("read source-republish cover authority: %w", err)
	}
	return sourceRepublishCoverAuthorityMatches(frozen, cover), nil
}

func sourceRepublishCoverAuthorityMatches(frozen PublicationRepublishCover, cover ReleaseCover) bool {
	if cover.ReleaseID != frozen.ReleaseID || cover.Revision != frozen.AuthorityRevision || cover.Mode != frozen.AuthorityMode ||
		cover.PendingGeneration != nil || cover.AcquisitionOwner != nil {
		return false
	}
	switch frozen.Derivation {
	case "absent":
		return cover.SelectedGeneration == nil
	case "ready", "optional_failure":
		// Frozen source-republish inputs bind the selected tuple only. Pending
		// promotions are conservatively rejected above; they are never applied here.
		return cover.SelectedGeneration != nil && *cover.SelectedGeneration == frozen.SelectedGeneration &&
			cover.SelectedMIME != nil && *cover.SelectedMIME == frozen.OriginalMIME &&
			cover.SelectedSize != nil && frozen.OriginalSizeBytes != nil && *cover.SelectedSize == *frozen.OriginalSizeBytes &&
			fmt.Sprintf("%x", cover.SelectedSHA256) == frozen.OriginalSHA256
	default:
		return false
	}
}

func providerTagCurrent(current TrackPublication, input PublicationInputs, epoch uuid.UUID) bool {
	provider := input.ProviderTags
	return provider != nil && publicationCurrentMatchesWitness(current, provider.Current, epoch)
}

// publicationCurrentMatchesWitness compares every database-backed CURRENT
// projection field against a frozen witness. Filesystem identity is checked by
// the caller's existing delivery path; this helper covers epoch, generation,
// path, ownership, audio, metadata tags, and assets.
func publicationCurrentMatchesWitness(current TrackPublication, witness PublicationCurrentWitness, epoch uuid.UUID) bool {
	var ownership PublicationCurrentOwnership
	if current.Epoch != epoch || current.Generation != witness.Generation || current.RelativePath != witness.RelativePath ||
		decodePublicationCurrentContract(current.Ownership, &ownership) != nil ||
		!sameJSONValue(mustPublicationJSON(ownership.Identity), mustPublicationJSON(witness.Ownership)) ||
		!sameJSONValue(current.ActualAssets, mustPublicationJSON(witness.Assets)) {
		return false
	}
	var currentMetadata PublicationCommitMetadata
	if strictMetadata(current.ActualMetadata, &currentMetadata) != nil ||
		!sameJSONValue(mustPublicationJSON(currentMetadata.Tags), mustPublicationJSON(witness.Tags)) {
		return false
	}
	facts, ok := publicationAudioFactsFromObservation(current.ActualAudio)
	return ok && sameJSONValue(mustPublicationJSON(facts), mustPublicationJSON(witness.AudioFacts))
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
