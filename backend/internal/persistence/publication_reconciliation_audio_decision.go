package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

func publicationReconciliationAudioUpgradeRequired(
	ctx context.Context,
	tx bun.Tx,
	trackID uuid.UUID,
	current TrackPublication,
	authority PositionSourceAuthority,
	format publication.AudioPublicationFormat,
) (bool, string, error) {
	if authority.Revision == uuid.Nil || authority.SourceLocationID == uuid.Nil || authority.MediaVariantID == uuid.Nil || authority.PositionID != current.PositionID || authority.PublicationEpoch != current.Epoch || authority.PublicationGeneration != current.Generation {
		return false, "", fmt.Errorf("reconcile publication audio: source authority is stale: %w", ErrPublicationAdmissionConflict)
	}
	switch authority.Mode {
	case "manual":
		return false, "manual", nil
	case "automatic":
	default:
		return false, "", fmt.Errorf("reconcile publication audio: source authority mode is invalid: %w", ErrPublicationAdmissionConflict)
	}

	currentFacts, ok := publicationAudioFactsFromObservation(current.ActualAudio)
	if !ok {
		return false, "", fmt.Errorf("reconcile publication audio: current audio facts are invalid: %w", ErrPublicationAdmissionConflict)
	}

	var sources []FirstPublicationSource
	var associations []PublicationSourceAssociation
	var blockers []string
	if err := readFirstPublicationSources(ctx, tx, trackID, &sources, &associations, &blockers); err != nil {
		return false, "", fmt.Errorf("reconcile publication audio: read sources: %w", err)
	}
	encoded, err := json.Marshal([]any{sources, associations, blockers})
	if err != nil {
		return false, "", fmt.Errorf("reconcile publication audio: encode source witness: %w", err)
	}
	digest := sha256.Sum256(encoded)
	witness := hex.EncodeToString(digest[:])

	candidates := make([]publication.QualityCandidate, 0, len(sources))
	for _, source := range sources {
		if source.RequiresExplicitStreamSelection || source.AudioStreamCount != 1 {
			continue
		}
		if _, err := publication.PlanAudioTransform(format, source.Quality.AudioSourceFacts()); err != nil {
			continue
		}
		candidates = append(candidates, publication.QualityCandidate{
			VariantUUID:  source.Selection.MediaVariantID.String(),
			LocationUUID: source.Selection.LocationID.String(),
			StreamIndex:  source.Quality.StreamIndex,
			Lossless:     source.Quality.Lossless,
			Codec:        source.Quality.Codec,
			Channels:     source.Quality.Channels,
			Layout:       source.ChannelLayout,
			BitDepth:     source.Quality.BitDepth,
			SampleRate:   source.Quality.SampleRate,
			Bitrate:      source.Quality.Bitrate,
		})
	}

	currentCandidate := qualityCandidateFromPersisted(currentFacts)
	selection := publication.SelectQuality(candidates, &currentCandidate)
	if selection.RequiresManual {
		return false, "", fmt.Errorf("reconcile publication audio: source quality selection requires manual review: %w", ErrPublicationAdmissionConflict)
	}
	return selection.ReplacesCurrent && selection.Candidate != nil, witness, nil
}
