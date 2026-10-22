package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type automaticLyricsRetryJournal interface {
	GuardedRecordAutomaticLyricsRetryAcquisition(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationAutomaticLyricsRetryAcquisition) (persistence.PublicationOptionalLyricsOutcome, error)
	GuardedRecordAutomaticLyricsRetryOutcome(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationAutomaticLyricsRetryOutcome) (persistence.PublicationOptionalLyricsOutcome, error)
	GuardedRecordAutomaticLyricsRetryRebuild(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationCandidateRebuildRecord) (persistence.PublicationOptionalLyricsOutcome, error)
}

type automaticLyricsRetryStageValidator interface {
	ValidateAutomaticLyricsRetry(context.Context, string, persistence.PublicationInputs, string) (PublicationStageObservation, error)
}

// runAutomaticLyricsRetryStage uses only the preparation copy of the current
// publication. In particular, it excludes the existing LYRICS tag from lookup
// inputs: only exact metadata plus the probed fractional duration is submitted.
func (runner PublicationStagingRunner) runAutomaticLyricsRetryStage(
	ctx context.Context, scope *PublicationExecutionScope, reservation *publicationSyncReservation,
	input persistence.PublicationInputs, prepared PreparedPublicationStage,
	transform func(context.Context) error, check func() error,
) (persistence.PublicationStageObservation, error) {
	stage, ok := prepared.(*preparedPublicationStage)
	if !ok || stage == nil || scope == nil || reservation == nil || transform == nil || check == nil || input.AutomaticLyricsRetry == nil {
		return persistence.PublicationStageObservation{}, errors.New("automatic lyrics retry requires its owned rewrite stage")
	}
	journal, ok := stage.repository.(automaticLyricsRetryJournal)
	if !ok {
		return persistence.PublicationStageObservation{}, errors.New("automatic lyrics retry journal is unavailable")
	}
	if err := check(); err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	capture, err := (metadata.Reader{}).Read(ctx, stage.paths.InputPath, metadata.ReadRequest{Properties: true, CoverInspection: true})
	if err != nil {
		return persistence.PublicationStageObservation{}, fmt.Errorf("read current publication metadata for lyrics retry: %w", err)
	}
	if err := check(); err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	lookupTags, lookupMetadata := automaticLyricsRetryLookupInputs(capture, 0)
	if err := stage.Validate(ctx); err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	pinned, ok := runner.PinnedTools.(*GuardedPublicationPinnedTools)
	if !ok || pinned.scope != scope || pinned.guard != scope.guard {
		return persistence.PublicationStageObservation{}, errors.New("retry probe tools are not bound to the execution scope")
	}
	probe, err := NewScopeBoundPublicationStageProbe(scope, pinned, input.Tools)
	if err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	ffprobe, err := pinned.Executable(ctx, input.Tools, "ffprobe")
	if err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	root, err := stage.opener.OpenRoot(ctx, stage.inputPath)
	if err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	file, err := sourcefs.OpenRegularAt(ctx, root, stage.inputName)
	if err != nil {
		_ = root.Close()
		return persistence.PublicationStageObservation{}, errors.New("current publication probe input is not regular")
	}
	raw, probeErr := probe.Probe(ctx, ffprobe, file, input.Rewrite.AudioFacts.Container)
	probeErr = errors.Join(probeErr, file.Close(), root.Close())
	if probeErr != nil {
		return persistence.PublicationStageObservation{}, fmt.Errorf("probe current publication for lyrics retry: %w", probeErr)
	}
	var probeFacts struct {
		Format struct {
			Duration json.Number `json:"duration"`
		} `json:"format"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&probeFacts); err != nil {
		return persistence.PublicationStageObservation{}, fmt.Errorf("decode current publication duration: %w", err)
	}
	seconds, err := probeFacts.Format.Duration.Float64()
	if err == nil && seconds > 0 {
		lookupMetadata.Duration = seconds
	}
	resolution, err := runner.Lyrics.Resolve(ctx, PublicationLyricsResolutionRequest{SelectedSourceTags: lookupTags, Metadata: lookupMetadata})
	if err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	classification := "no_result"
	if resolution.Source == "lrclib" {
		classification = "found"
	} else {
		switch resolution.WarningCode {
		case "lyrics_lookup_metadata_incomplete":
			classification = "metadata_incomplete"
		case "lyrics_lookup_failed":
			classification = "lookup_failed"
		case "lyrics_lookup_invalid_synced_text":
			classification = "invalid_synced_text"
		case "":
			if runner.Lyrics == nil {
				classification = "disabled"
			}
		default:
			return persistence.PublicationStageObservation{}, errors.New("automatic lyrics retry resolution returned an unsupported warning")
		}
	}
	acquisition := persistence.PublicationAutomaticLyricsRetryAcquisition{Version: 1, Kind: "publication_automatic_lyrics_retry_acquisition", Guard: scope.guard, Authority: *input.AutomaticLyricsRetry, Metadata: lookupMetadata, Classification: classification, Source: resolution.Source, Lyrics: resolution.Lyrics, SHA256: resolution.SHA256, Warning: resolution.WarningCode}
	if err := check(); err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	ack, err := journal.GuardedRecordAutomaticLyricsRetryAcquisition(ctx, scope.guard, acquisition)
	if err != nil || ack != persistence.PublicationOptionalLyricsRecorded {
		return persistence.PublicationStageObservation{}, errors.Join(errors.New("automatic lyrics retry acquisition was not acknowledged"), err)
	}
	if err := transform(ctx); err != nil {
		return persistence.PublicationStageObservation{}, fmt.Errorf("transform current publication for lyrics retry: %w", err)
	}
	requested := cloneTagMap(input.DesiredTags)
	rebuiltWithoutLyrics := false
	var currentCover *metadata.Cover
	if capture.CoverInspection != nil && capture.CoverInspection.Cover != nil {
		cover := *capture.CoverInspection.Cover
		cover.Data = append([]byte(nil), cover.Data...)
		currentCover = &cover
	}
	writer := runner.Metadata
	if writer == nil {
		writer = metadata.Writer{}
	}
	if resolution.Source == "lrclib" {
		requested["LYRICS"] = []string{resolution.Lyrics}
		readbackStatus, err := writeAutomaticLyricsRetryMetadata(ctx, stage, writer, requested, currentCover, input.Rewrite.AudioFacts.Container == publication.AudioContainerFLAC, true)
		if err != nil {
			return persistence.PublicationStageObservation{}, fmt.Errorf("write retry lyrics to current publication copy: %w", err)
		}
		if readbackStatus != metadata.OptionalLyricsVerified {
			rebuiltWithoutLyrics = true
			intent, capability, err := stage.prepareInitialLyricsRebuild(ctx, scope, reservation)
			if err != nil {
				return persistence.PublicationStageObservation{}, fmt.Errorf("prepare bounded retry lyrics rebuild: %w", err)
			}
			if err := stage.acknowledgeAutomaticRetryRebuildIntent(ctx, journal, capability); err != nil {
				return persistence.PublicationStageObservation{}, fmt.Errorf("acknowledge bounded retry lyrics rebuild: %w", err)
			}
			if err := check(); err != nil {
				return persistence.PublicationStageObservation{}, err
			}
			if err := stage.resetInitialLyricsCandidate(ctx, scope, capability); err != nil {
				return persistence.PublicationStageObservation{}, fmt.Errorf("reset acknowledged retry candidate: %w", err)
			}
			if err := transform(ctx); err != nil {
				return persistence.PublicationStageObservation{}, fmt.Errorf("rebuild retry candidate without lyrics: %w", err)
			}
			if _, err := writeAutomaticLyricsRetryMetadata(ctx, stage, writer, cloneTagMap(input.DesiredTags), currentCover, input.Rewrite.AudioFacts.Container == publication.AudioContainerFLAC, false); err != nil {
				return persistence.PublicationStageObservation{}, fmt.Errorf("restore mandatory tags and cover after retry rebuild: %w", err)
			}
			complete, err := stage.finishInitialLyricsRebuild(ctx, scope, reservation, intent)
			if err != nil {
				return persistence.PublicationStageObservation{}, fmt.Errorf("finish acknowledged retry rebuild: %w", err)
			}
			ack, err := journal.GuardedRecordAutomaticLyricsRetryRebuild(ctx, scope.guard, complete)
			if err != nil || ack != persistence.PublicationOptionalLyricsRecorded {
				return persistence.PublicationStageObservation{}, errors.Join(errors.New("automatic lyrics retry rebuild completion was not acknowledged"), err)
			}
		}
	} else {
		if _, err := writeAutomaticLyricsRetryMetadata(ctx, stage, writer, requested, currentCover, input.Rewrite.AudioFacts.Container == publication.AudioContainerFLAC, false); err != nil {
			return persistence.PublicationStageObservation{}, fmt.Errorf("write current publication metadata without lyrics: %w", err)
		}
	}
	if err := check(); err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	finalInput := input.Clone()
	finalLyrics := ""
	if resolution.Source == "lrclib" && !rebuiltWithoutLyrics {
		finalLyrics = resolution.Lyrics
	}
	validator, ok := runner.Validator.(automaticLyricsRetryStageValidator)
	if !ok {
		return persistence.PublicationStageObservation{}, errors.New("automatic lyrics retry validator does not support separate lyrics evidence")
	}
	observed, err := validator.ValidateAutomaticLyricsRetry(ctx, stage.paths.OutputPath, finalInput, finalLyrics)
	if err != nil {
		return persistence.PublicationStageObservation{}, fmt.Errorf("validate automatic lyrics retry output: %w", err)
	}
	inputIdentity, outputIdentity, err := stage.optionalLyricsFileIdentities(ctx)
	if err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	preparation := stage.preparationRecord
	if preparation.Step != "input_ready" {
		return persistence.PublicationStageObservation{}, errors.New("automatic lyrics retry preparation is not input-ready")
	}
	outcome := persistence.PublicationAutomaticLyricsRetryNoResult
	warning := ""
	if resolution.Source == "lrclib" {
		if rebuiltWithoutLyrics {
			outcome = persistence.PublicationOptionalLyricsRebuiltWithoutLyrics
			warning = "readback_mismatch"
		} else {
			outcome = persistence.PublicationOptionalLyricsExactRoundTrip
		}
	}
	observation := persistence.PublicationStageObservation{Version: 1, RelativePath: stage.paths.RelativePath, Ownership: observed.Ownership, PayloadSHA256: observed.SHA256, ActualAudio: observed.Audio, ActualMetadata: observed.Metadata, ActualAssets: observed.Assets, QualityPolicyVersion: observed.QualityPolicyVersion}
	record := persistence.PublicationAutomaticLyricsRetryOutcome{Version: 1, Kind: "publication_automatic_lyrics_retry_outcome", Guard: scope.guard, Outcome: outcome, Preparation: preparation, InputIdentity: inputIdentity, OutputIdentity: outputIdentity, FinalObservation: observation, Warning: warning}
	if err := check(); err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	ack, err = journal.GuardedRecordAutomaticLyricsRetryOutcome(ctx, scope.guard, record)
	if err != nil || ack != persistence.PublicationOptionalLyricsRecorded {
		return persistence.PublicationStageObservation{}, errors.Join(errors.New("automatic lyrics retry outcome was not acknowledged"), err)
	}
	return observation, nil
}

func writeAutomaticLyricsRetryMetadata(ctx context.Context, stage *preparedPublicationStage, writer PublicationMetadataWriter, tags map[string][]string, cover *metadata.Cover, preserveFlacArtwork, optionalLyrics bool) (metadata.OptionalLyricsWriteStatus, error) {
	if stage == nil {
		return metadata.OptionalLyricsUnobserved, errors.New("automatic lyrics retry stage is unavailable")
	}
	if preserveFlacArtwork {
		before, err := metadata.SnapshotFlacArtwork(ctx, stage.paths.OutputPath)
		if err != nil {
			return metadata.OptionalLyricsUnobserved, fmt.Errorf("snapshot native FLAC before retry tag write: %w", err)
		}
		flacWriter, ok := writer.(publicationFlacArtworkMetadataWriter)
		if !ok {
			flacWriter = nativeFlacArtworkMetadataWriter{}
		}
		result, err := flacWriter.WriteFlacTagsPreservingArtwork(ctx, stage.paths.OutputPath, tags, before)
		if err != nil {
			return metadata.OptionalLyricsUnobserved, err
		}
		if !optionalLyrics {
			return metadata.OptionalLyricsUnobserved, nil
		}
		expected := tags["LYRICS"]
		actual, exists := result.Tags["LYRICS"]
		if exists && reflect.DeepEqual(actual, expected) {
			return metadata.OptionalLyricsVerified, nil
		}
		if !exists {
			return metadata.OptionalLyricsAbsent, nil
		}
		return metadata.OptionalLyricsMismatch, nil
	}
	if optionalLyrics {
		optionalWriter, ok := writer.(publicationOptionalLyricsMetadataWriter)
		if !ok {
			optionalWriter = metadata.Writer{}
		}
		result, err := optionalWriter.WriteWithOptionalLyrics(ctx, stage.paths.OutputPath, tags, cover)
		return result.Status, err
	}
	if _, err := writer.Write(ctx, stage.paths.OutputPath, tags, cover); err != nil {
		return metadata.OptionalLyricsUnobserved, err
	}
	return metadata.OptionalLyricsUnobserved, nil
}

type nativeFlacArtworkMetadataWriter struct{}

func (nativeFlacArtworkMetadataWriter) WriteFlacTagsPreservingArtwork(ctx context.Context, path string, tags map[string][]string, before metadata.FlacArtworkSnapshot) (metadata.WriteResult, error) {
	return metadata.WriteFlacTagsPreservingArtwork(ctx, path, tags, before)
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
