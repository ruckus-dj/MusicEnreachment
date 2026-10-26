package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type automaticLyricsRetryStageValidator interface {
	ValidateAutomaticLyricsRetry(context.Context, string, persistence.PublicationInputs, string) (PublicationStageObservation, error)
}

type nativeFlacArtworkMetadataWriter struct{}

func (nativeFlacArtworkMetadataWriter) WriteFlacTagsPreservingArtwork(ctx context.Context, path string, tags map[string][]string, before metadata.FlacArtworkSnapshot) (metadata.WriteResult, error) {
	return metadata.WriteFlacTagsPreservingArtwork(ctx, path, tags, before)
}

func optionalLyricsTags(mandatory map[string][]string, frozen *persistence.PublicationManualLyricsInput) (map[string][]string, string, error) {
	requested := cloneTagMap(mandatory)
	switch frozen.State {
	case "value":
		validated, err := lrclib.ValidateSyncedLyrics(frozen.Value)
		if err != nil || validated == "" || validated != frozen.Value {
			return nil, "", errors.New("frozen optional lyrics are not valid synced LRC")
		}
		requested["LYRICS"] = []string{frozen.Value}
		return requested, frozen.Value, nil
	case "explicit_empty":
		requested["LYRICS"] = []string{}
		return requested, "", nil
	default:
		return nil, "", fmt.Errorf("unsupported frozen optional lyrics state %q", frozen.State)
	}
}

func initialLyricsCapture(input persistence.PublicationInputs) metadata.Capture {
	capture := metadata.Capture{Tags: map[string][]string{}}
	if input.Audio == nil || input.Audio.AnalysedMetadata == nil {
		return capture
	}
	capture.Tags = cloneTagMap(input.Audio.AnalysedMetadata.Tags)
	return capture
}

func initialLyricsLookupMetadataFromFrozen(input persistence.PublicationInputs) lrclib.Metadata {
	if input.Audio == nil || input.Audio.AnalysedMetadata == nil {
		return initialLyricsLookupMetadata(initialLyricsCapture(input))
	}
	lookup := initialLyricsLookupMetadata(initialLyricsCapture(input))
	if duration := input.Audio.AnalysedMetadata.DurationSeconds; duration != nil {
		lookup.Duration = *duration
	}
	return lookup
}

func initialLyricsLookupMetadata(capture metadata.Capture) lrclib.Metadata {
	value := func(key string) string {
		if len(capture.Tags[key]) != 1 {
			return ""
		}
		return capture.Tags[key][0]
	}
	result := lrclib.Metadata{Artist: value("ARTIST"), Title: value("TITLE"), Album: value("ALBUM")}
	if capture.Properties != nil {
		result.Duration = capture.Properties.Length.Seconds()
	}
	return result
}

func automaticLyricsRetryLookupInputs(capture metadata.Capture, probedDuration float64) (map[string][]string, lrclib.Metadata) {
	tags := cloneTagMap(capture.Tags)
	for name := range tags {
		if strings.EqualFold(strings.TrimSpace(name), "LYRICS") {
			delete(tags, name)
		}
	}
	lookup := initialLyricsLookupMetadata(capture)
	lookup.Duration = probedDuration
	return tags, lookup
}

func validReplacementDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}
