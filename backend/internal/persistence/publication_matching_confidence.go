package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationMatchingScorer calculates one pair without acquiring external
// evidence or writing state. Admission owns the data and the frozen score.
type PublicationMatchingScorer func(IncomingGroupingCapture, PublicationProviderTagContext) (*float64, error)

// PublicationLayoutMatchingScorer evaluates one exact Local capture against a
// single selected provider position. trackEvidence reports whether the score
// carries the audio-to-track evidence required by automatic layout selection;
// a nil score means the pair is not actionable.
type PublicationLayoutMatchingScorer func(IncomingGroupingCapture, PublicationProviderTagContext) (*float64, bool, error)

func (repository *SetupManagerRepository) SetPublicationMatchingScorer(scorer PublicationMatchingScorer) {
	repository.publicationScorer = scorer
}

func (repository *SetupManagerRepository) calculatePublicationMatchingConfidence(ctx context.Context, tx bun.Tx, releaseID, positionID uuid.UUID, input PublicationInputs) (*float64, error) {
	if repository.publicationScorer == nil {
		return nil, nil
	}
	location, variant, work := uuid.Nil, uuid.Nil, uuid.Nil
	if input.Audio != nil {
		location, variant, work = input.Audio.LocationID, input.Audio.MediaVariantID, input.Audio.WorkID
	} else if input.Rewrite != nil {
		var current PositionSourceAuthority
		err := tx.NewSelect().Model(&current).Where("position_id=? AND publication_generation=?", positionID, input.Rewrite.CurrentGeneration).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read current publication source for matching: %w", err)
		}
		location, variant = current.SourceLocationID, current.MediaVariantID
	}
	if location == uuid.Nil || variant == uuid.Nil {
		return nil, nil
	}
	captures, err := loadIncomingGroupingCapturesForLocations(ctx, tx, []uuid.UUID{location})
	if err != nil {
		return nil, err
	}
	if len(captures) != 1 {
		return nil, nil
	}
	capture := captures[0]
	if !capture.Ready || capture.CaptureAnalysisID == nil || capture.MediaVariantID == nil || *capture.MediaVariantID != variant ||
		work != uuid.Nil && capture.WorkID != work {
		return nil, nil
	}
	if input.Audio != nil && (input.Audio.AnalysedMetadata == nil || *capture.CaptureAnalysisID != input.Audio.AnalysedMetadata.MetadataResultID) {
		return nil, nil
	}

	provider, err := publicationMatchingProvider(ctx, tx, releaseID, positionID)
	if err != nil || provider == nil {
		return nil, err
	}
	score, err := repository.publicationScorer(capture, *provider)
	if err != nil {
		return nil, err
	}
	if score != nil && (math.IsNaN(*score) || *score < 0 || *score > 1) {
		return nil, fmt.Errorf("invalid calculated publication matching confidence")
	}
	return score, nil
}

func publicationMatchingProvider(ctx context.Context, tx bun.Tx, releaseID, positionID uuid.UUID) (*PublicationProviderTagContext, error) {
	var source ProviderSource
	err := tx.NewRaw(`SELECT source.id,source.endpoint,source.configuration_identity
		FROM provider_source source
		JOIN provider ON provider.id=source.provider_id AND provider.code='musicbrainz'
		JOIN app_setting config ON config.setting_name='musicbrainz_config_identity'
			AND config.setting_value=source.configuration_identity
		ORDER BY source.id LIMIT 1`).Scan(ctx, &source)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read provider scope for publication matching: %w", err)
	}
	if err == nil {
		var provider *PublicationProviderTagContext
		var pending bool
		err := readPublicationProviderTagContextForPositionTxWithAvailability(ctx, tx, releaseID, positionID, source.Endpoint, source.ConfigurationIdentity, false, &provider, &pending)
		if err != nil && !errors.Is(err, ErrPublicationAdmissionConflict) {
			return nil, fmt.Errorf("read provider facts for publication matching: %w", err)
		}
		if provider != nil || pending {
			return provider, nil
		}
	}

	var binding struct {
		ProviderReleaseID uuid.UUID `bun:"provider_release_id"`
		ProviderTrackID   uuid.UUID `bun:"provider_track_id"`
	}
	err = tx.NewRaw(`SELECT bound_release.provider_release_id,bound_track.provider_track_id
		FROM release_track position
		JOIN provider_local_release bound_release ON bound_release.local_release_id=position.release_id AND bound_release.provider_id=?
		JOIN provider_local_track bound_track ON bound_track.local_track_id=position.track_id AND bound_track.provider_id=bound_release.provider_id
		JOIN provider_release_track track ON track.id=bound_track.provider_track_id AND track.release_id=bound_release.provider_release_id AND track.present
		WHERE position.id=? AND position.release_id=?`, localProviderID, positionID, releaseID).Scan(ctx, &binding)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Local position for publication matching: %w", err)
	}
	snapshot, err := readMatchingLocalSelectionSnapshotTx(ctx, tx, []uuid.UUID{binding.ProviderReleaseID})
	if err != nil {
		return nil, err
	}
	for _, release := range snapshot.Releases {
		for _, track := range release.Tracks {
			if track.ID == binding.ProviderTrackID {
				return &PublicationProviderTagContext{Release: release, Position: track}, nil
			}
		}
	}
	return nil, nil
}
