package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// PublicationSimplePreparation contains only the dependencies needed to build
// and inspect a private candidate. It has no repository, delivery scope, or
// journal dependency; its caller owns the candidate paths and later installation.
type PublicationSimplePreparation struct {
	Commands       PublicationCommandRunner
	FFmpegPath     string
	FFprobePath    string
	Validator      PublicationStageValidator
	Metadata       PublicationMetadataWriter
	MetadataReader interface {
		Read(context.Context, string, metadata.ReadRequest) (metadata.Capture, error)
	}
	Lyrics     *PublicationLyricsResolver
	CoverInput *persistence.PublicationAttemptCoverInput
}

// PublicationSimplePreparationResult carries read-back evidence and a bounded
// lyrics projection for the committing caller to bind to its generation.
type PublicationSimplePreparationResult struct {
	Observation      persistence.PublicationStageObservation
	LyricsOutcome    *persistence.PublicationLyricsOutcome
	LyricsResolution *PublicationLyricsResolution
	// Generation is intentionally left unset here; the committing caller binds it.
	InitialLyrics  *persistence.PublicationInitialLyricsProjection
	CoverEmbedding *persistence.PublicationCoverEmbeddingProjection
}

// Prepare builds a fresh candidate at outputPath. For rewrites inputPath must
// already name the resolved current publication; its current bytes are copied
// to tempDir before transformation, never checked against frozen old identity.
func (preparation PublicationSimplePreparation) Prepare(
	ctx context.Context,
	input persistence.PublicationInputs,
	inputPath, tempDir, outputPath string,
) (PublicationSimplePreparationResult, error) {
	if input.Version != persistence.PublicationInputsVersion || (input.Rewrite == nil && input.Audio == nil) || inputPath == "" || tempDir == "" || outputPath == "" ||
		!filepath.IsAbs(inputPath) || !filepath.IsAbs(tempDir) || !filepath.IsAbs(outputPath) || preparation.Commands == nil ||
		preparation.Validator == nil || preparation.Metadata == nil || ((input.Format != publication.AudioFormatSource || input.Audio != nil) && preparation.FFmpegPath == "") {
		return PublicationSimplePreparationResult{}, errors.New("simple publication preparation dependencies or rewrite input are incomplete")
	}
	if input.AutomaticLyricsRetry != nil && (preparation.FFprobePath == "" || preparation.MetadataReader == nil) {
		return PublicationSimplePreparationResult{}, errors.New("optional lyrics preparation requires pinned probing and metadata readers")
	}
	if input.Audio != nil && preparation.CoverInput != nil && preparation.CoverInput.State == "value" && preparation.MetadataReader == nil {
		return PublicationSimplePreparationResult{}, errors.New("first-audio cover validation requires a metadata reader")
	}
	if err := input.Validate(); err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("validate frozen publication input: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return PublicationSimplePreparationResult{}, err
	}
	if _, err := os.Lstat(outputPath); err == nil {
		return PublicationSimplePreparationResult{}, errors.New("publication candidate output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return PublicationSimplePreparationResult{}, fmt.Errorf("inspect publication candidate output: %w", err)
	}
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("create publication temporary directory: %w", err)
	}
	sourceFacts := inputAudioFacts(input)
	tempInput := filepath.Join(tempDir, "current-input"+"."+string(sourceFacts.Container))
	if err := copyCurrentPublication(inputPath, tempInput); err != nil {
		return PublicationSimplePreparationResult{}, err
	}
	facts := sourceFacts.AudioSourceFacts()
	if input.Rewrite != nil && input.Format == publication.AudioFormatSource {
		// Rewrites retain the current container and its artwork; a transform is
		// needed only when the requested format differs from the current one.
		if err := copyCurrentPublication(tempInput, outputPath); err != nil {
			return PublicationSimplePreparationResult{}, fmt.Errorf("copy native publication candidate: %w", err)
		}
	} else {
		if err := buildSimpleAudioCandidate(ctx, preparation.Commands, preparation.FFmpegPath, input.Format, facts, tempInput, outputPath); err != nil {
			return PublicationSimplePreparationResult{}, fmt.Errorf("transform publication candidate: %w", err)
		}
	}
	if input.Audio != nil {
		if err := metadata.ClearArtwork(ctx, outputPath); err != nil {
			return PublicationSimplePreparationResult{}, fmt.Errorf("clear source artwork from first publication candidate: %w", err)
		}
	}
	tags := cloneTagMap(input.DesiredTags)
	prepared := PublicationSimplePreparationResult{}
	if input.ManualLyrics != nil {
		var err error
		var lyrics string
		tags, lyrics, err = optionalLyricsTags(tags, input.ManualLyrics)
		if err != nil {
			return PublicationSimplePreparationResult{}, err
		}
		if lyrics == "" {
			prepared.LyricsOutcome = &persistence.PublicationLyricsOutcome{State: persistence.PublicationAssetAbsent}
		} else {
			digest := sha256.Sum256([]byte(lyrics))
			prepared.LyricsOutcome = &persistence.PublicationLyricsOutcome{State: persistence.PublicationAssetEmbedded, SHA256: hex.EncodeToString(digest[:]), Format: "lrc"}
		}
	}
	if input.InitialLyrics != nil {
		capture := initialLyricsCapture(input)
		lookupMetadata := initialLyricsLookupMetadataFromFrozen(input)
		acquisition, err := resolveInitialPublicationLyrics(ctx, preparation.Lyrics, *input.InitialLyrics, capture.Tags, lookupMetadata)
		if err != nil {
			return PublicationSimplePreparationResult{}, fmt.Errorf("resolve initial publication lyrics: %w", err)
		}
		projection, err := simpleInitialLyricsProjection(input.InitialLyrics.ExpectedMetadataRevision, input.InitialLyrics.Desired, acquisition.Source, acquisition.SHA256, acquisition.WarningCode, persistence.PublicationInitialLyricsNotRequested, "")
		if err != nil {
			return PublicationSimplePreparationResult{}, err
		}
		prepared.InitialLyrics = projection
		prepared.LyricsResolution = &PublicationLyricsResolution{Source: acquisition.Source, SHA256: acquisition.SHA256, WarningCode: acquisition.WarningCode}
		deleteTag(tags, "LYRICS")
		if acquisition.Source != "none" {
			tags["LYRICS"] = []string{acquisition.Lyrics}
			prepared.LyricsOutcome = &persistence.PublicationLyricsOutcome{State: persistence.PublicationAssetEmbedded, SHA256: acquisition.SHA256, Format: "lrc"}
		} else {
			prepared.LyricsOutcome = &persistence.PublicationLyricsOutcome{State: persistence.PublicationAssetAbsent}
		}
	}
	if input.AutomaticLyricsRetry != nil {
		capture, err := preparation.MetadataReader.Read(ctx, inputPath, metadata.ReadRequest{CoverInspection: true})
		if err != nil {
			return PublicationSimplePreparationResult{}, fmt.Errorf("read current publication metadata: %w", err)
		}
		probe, probeErr := preparation.Commands.Run(ctx, preparation.FFprobePath, "-v", "error", "-show_entries", "format=duration", "-of", "json", inputPath)
		if !probe.QuiescenceEstablished {
			return PublicationSimplePreparationResult{}, errors.Join(probeErr, errors.New("publication duration probe quiescence was not established"))
		}
		if probeErr != nil {
			return PublicationSimplePreparationResult{}, fmt.Errorf("probe current publication duration: %w", probeErr)
		}
		var data struct {
			Format struct {
				Duration string `json:"duration"`
			} `json:"format"`
		}
		if err := json.Unmarshal(probe.Output, &data); err != nil {
			return PublicationSimplePreparationResult{}, fmt.Errorf("decode current publication duration: %w", err)
		}
		duration, err := strconv.ParseFloat(strings.TrimSpace(data.Format.Duration), 64)
		if err != nil || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
			duration = 0
		}
		lookupTags, lookupMetadata := automaticLyricsRetryLookupInputs(capture, duration)
		resolved, err := preparation.Lyrics.Resolve(ctx, PublicationLyricsResolutionRequest{SelectedSourceTags: lookupTags, Metadata: lookupMetadata})
		if err != nil {
			return PublicationSimplePreparationResult{}, fmt.Errorf("resolve current publication lyrics: %w", err)
		}
		prepared.LyricsResolution = &resolved
		outcome := persistence.PublicationAutomaticLyricsRetryNoResult
		if resolved.Lyrics != "" {
			tags["LYRICS"] = []string{resolved.Lyrics}
			prepared.LyricsOutcome = &persistence.PublicationLyricsOutcome{State: persistence.PublicationAssetEmbedded, SHA256: resolved.SHA256, Format: "lrc"}
			outcome = persistence.PublicationOptionalLyricsExactRoundTrip
		} else {
			delete(tags, "LYRICS")
			prepared.LyricsOutcome = &persistence.PublicationLyricsOutcome{State: persistence.PublicationAssetAbsent}
		}
		projection, err := simpleInitialLyricsProjection(input.AutomaticLyricsRetry.ExpectedMetadataRevision, input.AutomaticLyricsRetry.Desired, resolved.Source, resolved.SHA256, resolved.WarningCode, outcome, "")
		if err != nil {
			return PublicationSimplePreparationResult{}, err
		}
		prepared.InitialLyrics = projection
	}
	writeTags := func() error {
		if input.Format == publication.AudioFormatSource && facts.Container == publication.AudioContainerFLAC {
			snapshot, snapshotErr := metadata.SnapshotFlacArtwork(ctx, outputPath)
			if snapshotErr != nil {
				return snapshotErr
			}
			nativeWriter, ok := preparation.Metadata.(publicationFlacArtworkMetadataWriter)
			if !ok {
				nativeWriter = nativeFlacArtworkMetadataWriter{}
			}
			_, err := nativeWriter.WriteFlacTagsPreservingArtwork(ctx, outputPath, tags, snapshot)
			return err
		}
		_, err := preparation.Metadata.Write(ctx, outputPath, tags, nil)
		return err
	}
	writeErr := writeTags()
	if writeErr != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("write publication candidate metadata: %w", writeErr)
	}
	if (input.InitialLyrics != nil || input.AutomaticLyricsRetry != nil) && prepared.LyricsOutcome != nil && prepared.LyricsOutcome.State == persistence.PublicationAssetEmbedded {
		readback, readErr := preparation.MetadataReader.Read(ctx, outputPath, metadata.ReadRequest{})
		wanted := tags["LYRICS"]
		if readErr != nil || len(wanted) != 1 || len(readback.Tags["LYRICS"]) != 1 || readback.Tags["LYRICS"][0] != wanted[0] {
			// Rebuild only this private candidate from the copied input. This
			// bounded path has no journal or acknowledgement protocol.
			if err := os.Remove(outputPath); err != nil {
				return PublicationSimplePreparationResult{}, fmt.Errorf("remove initial publication candidate after lyrics readback mismatch: %w", err)
			}
			if input.Rewrite != nil && input.Format == publication.AudioFormatSource {
				if err := copyCurrentPublication(tempInput, outputPath); err != nil {
					return PublicationSimplePreparationResult{}, fmt.Errorf("rebuild initial publication candidate without lyrics: %w", err)
				}
			} else if err := buildSimpleAudioCandidate(ctx, preparation.Commands, preparation.FFmpegPath, input.Format, facts, tempInput, outputPath); err != nil {
				return PublicationSimplePreparationResult{}, fmt.Errorf("rebuild initial publication candidate without lyrics: %w", err)
			}
			deleteTag(tags, "LYRICS")
			if err := writeTags(); err != nil {
				return PublicationSimplePreparationResult{}, fmt.Errorf("write initial publication candidate without lyrics: %w", err)
			}
			prepared.LyricsOutcome = &persistence.PublicationLyricsOutcome{State: persistence.PublicationAssetAbsent}
			prepared.InitialLyrics.Outcome = persistence.PublicationOptionalLyricsRebuiltWithoutLyrics
			prepared.InitialLyrics.OutcomeWarningCode = "readback_mismatch"
		} else {
			prepared.InitialLyrics.Outcome = persistence.PublicationOptionalLyricsExactRoundTrip
		}
	} else if input.InitialLyrics != nil {
		prepared.InitialLyrics.Outcome = persistence.PublicationInitialLyricsNotRequested
	}
	var firstAudioCoverExpected *persistence.PublicationCoverOutcome
	if input.Audio != nil && preparation.CoverInput != nil && preparation.CoverInput.State == "value" {
		receipt := preparation.CoverInput
		baselineCapture, readErr := preparation.MetadataReader.Read(ctx, outputPath, metadata.ReadRequest{CoverInspection: true})
		if readErr != nil {
			return PublicationSimplePreparationResult{}, fmt.Errorf("read first-audio cover baseline: %w", readErr)
		}
		baselineAssets, assetsErr := ObservePublicationAssets(baselineCapture)
		if assetsErr != nil {
			return PublicationSimplePreparationResult{}, fmt.Errorf("observe first-audio cover baseline: %w", assetsErr)
		}
		optionalWriter, ok := preparation.Metadata.(interface {
			WriteWithOptionalAssets(context.Context, string, map[string][]string, *metadata.Cover) (metadata.OptionalAssetsWriteResult, error)
		})
		if !ok {
			return PublicationSimplePreparationResult{}, errors.New("optional publication cover writer is unavailable")
		}
		baseline := filepath.Join(tempDir, "cover-baseline."+string(sourceFacts.Container))
		if err := copyCurrentPublication(outputPath, baseline); err != nil {
			return PublicationSimplePreparationResult{}, err
		}
		writeResult, err := optionalWriter.WriteWithOptionalAssets(ctx, outputPath, tags, &metadata.Cover{Data: receipt.Bytes, MIME: *receipt.MIME})
		if err != nil {
			return PublicationSimplePreparationResult{}, fmt.Errorf("write publication candidate optional assets: %w", err)
		}
		outcome, warning := "verified", ""
		switch writeResult.CoverStatus {
		case metadata.OptionalCoverVerified:
		case metadata.OptionalCoverWriteFailed:
			outcome, warning = "write_failed", "cover_write_failed"
		case metadata.OptionalCoverReadFailed:
			outcome, warning = "read_failed", "cover_read_failed"
		case metadata.OptionalCoverMismatch:
			outcome, warning = "mismatch", "cover_mismatch"
		default:
			return PublicationSimplePreparationResult{}, errors.New("optional publication cover outcome is missing")
		}
		if warning != "" {
			if err := os.Remove(outputPath); err != nil {
				return PublicationSimplePreparationResult{}, err
			}
			if err := copyCurrentPublication(baseline, outputPath); err != nil {
				return PublicationSimplePreparationResult{}, fmt.Errorf("restore candidate after optional cover failure: %w", err)
			}
		}
		prepared.CoverEmbedding = &persistence.PublicationCoverEmbeddingProjection{Version: persistence.PublicationCoverEmbeddingProjectionVersion, ReceiptOperationID: receipt.OperationID, ReceiptReleaseID: receipt.ReleaseID, ReceiptRevision: receipt.Revision, ReceiptMode: receipt.Mode, RequestedGeneration: *receipt.Generation, RequestedSHA256: hex.EncodeToString(receipt.SHA256), Outcome: outcome, WarningCode: warning}
		preparedCoverOutcome := baselineAssets.Cover
		if outcome == "verified" {
			size := int64(len(receipt.Bytes))
			preparedCoverOutcome = persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, SHA256: hex.EncodeToString(receipt.SHA256), MIME: *receipt.MIME, SizeBytes: &size}
		}
		firstAudioCoverExpected = &preparedCoverOutcome
	}
	validationInput := input.Clone()
	if input.AutomaticLyricsRetry == nil {
		validationInput.DesiredTags = tags
	}
	var observed PublicationStageObservation
	var err error
	if input.AutomaticLyricsRetry != nil {
		lyrics := ""
		if values := tags["LYRICS"]; len(values) == 1 {
			lyrics = values[0]
		}
		validator, ok := preparation.Validator.(automaticLyricsRetryStageValidator)
		if !ok {
			return PublicationSimplePreparationResult{}, errors.New("automatic lyrics candidate validator is unavailable")
		}
		observed, err = validator.ValidateAutomaticLyricsRetry(ctx, outputPath, validationInput, lyrics)
	} else if firstAudioCoverExpected != nil {
		validator, ok := preparation.Validator.(interface {
			ValidateFirstAudioCover(context.Context, string, persistence.PublicationInputs, persistence.PublicationCoverOutcome) (PublicationStageObservation, error)
		})
		if !ok {
			return PublicationSimplePreparationResult{}, errors.New("first-audio cover candidate validator is unavailable")
		}
		observed, err = validator.ValidateFirstAudioCover(ctx, outputPath, validationInput, *firstAudioCoverExpected)
	} else {
		observed, err = preparation.Validator.Validate(ctx, outputPath, validationInput)
	}
	if err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("validate publication candidate: %w", err)
	}
	prepared.Observation = persistence.PublicationStageObservation{
		Version:              1,
		RelativePath:         filepath.Base(outputPath),
		Ownership:            observed.Ownership,
		PayloadSHA256:        observed.SHA256,
		ActualAudio:          observed.Audio,
		ActualMetadata:       observed.Metadata,
		ActualAssets:         observed.Assets,
		QualityPolicyVersion: observed.QualityPolicyVersion,
	}
	if prepared.LyricsOutcome != nil {
		assets := persistence.PublicationAssetOutcomes{Version: persistence.PublicationAssetOutcomesVersion, Lyrics: *prepared.LyricsOutcome}
		if input.AutomaticLyricsRetry != nil {
			assets.Cover = input.AutomaticLyricsRetry.CurrentAssets.Cover
		} else if len(prepared.Observation.ActualAssets) != 0 {
			observedAssets, decodeErr := persistence.DecodePublicationAssetOutcomes(prepared.Observation.ActualAssets)
			if decodeErr != nil {
				return PublicationSimplePreparationResult{}, fmt.Errorf("decode observed publication assets: %w", decodeErr)
			}
			if observedAssets.Lyrics.State != prepared.LyricsOutcome.State || observedAssets.Lyrics.SHA256 != prepared.LyricsOutcome.SHA256 || observedAssets.Lyrics.Format != prepared.LyricsOutcome.Format {
				return PublicationSimplePreparationResult{}, errors.New("observed publication lyrics do not match the prepared outcome")
			}
			assets = observedAssets
		} else {
			return PublicationSimplePreparationResult{}, errors.New("publication validator omitted required asset observations")
		}
		prepared.Observation.ActualAssets, _ = json.Marshal(assets)
	}
	return prepared, nil
}

func buildSimpleAudioCandidate(
	ctx context.Context,
	commands PublicationCommandRunner,
	ffmpegPath string,
	format publication.AudioPublicationFormat,
	facts publication.AudioSourceFacts,
	inputPath, outputPath string,
) error {
	plan, err := publication.PlanAudioTransform(format, facts)
	if err != nil {
		return fmt.Errorf("plan publication audio transform: %w", err)
	}
	args, err := plan.FFmpegArgs(inputPath, outputPath, facts.StreamIndex, facts.Container)
	if err != nil {
		return err
	}
	command, runErr := commands.Run(ctx, ffmpegPath, args...)
	if !command.QuiescenceEstablished {
		return errors.Join(runErr, errors.New("publication transform quiescence was not established"))
	}
	if runErr != nil {
		return runErr
	}
	return nil
}

func deleteTag(tags map[string][]string, name string) {
	for key := range tags {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			delete(tags, key)
		}
	}
}

func simpleInitialLyricsProjection(
	metadataRevision int64,
	desired *librarymetadata.FieldValue,
	source, digest, resolutionWarning, outcome, outcomeWarning string,
) (*persistence.PublicationInitialLyricsProjection, error) {
	projection := &persistence.PublicationInitialLyricsProjection{
		Version: persistence.PublicationInitialLyricsVersion, Source: source, SHA256: digest, Outcome: outcome,
		ResolutionWarningCode: resolutionWarning, OutcomeWarningCode: outcomeWarning,
		AuthorityMetadataRevision: metadataRevision, AuthorityState: string(librarymetadata.StateAbsent),
		Authority: string(librarymetadata.AuthorityAutomatic), AuthorityProvenance: string(librarymetadata.ProvenanceSourceMetadata),
	}
	if desired != nil {
		projection.AuthorityState = string(desired.State)
		projection.Authority = string(desired.Authority)
		projection.AuthorityProvenance = string(desired.Provenance.Kind)
		frozen, err := json.Marshal(desired)
		if err != nil {
			return nil, fmt.Errorf("encode frozen initial lyrics authority: %w", err)
		}
		fingerprint := sha256.Sum256(frozen)
		projection.AuthorityFingerprintSHA256 = hex.EncodeToString(fingerprint[:])
	}
	return projection, nil
}

func copyCurrentPublication(source, destination string) (resultErr error) {
	sourceFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open current publication for rewrite: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, sourceFile.Close()) }()
	info, err := sourceFile.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.Join(errors.New("current publication rewrite input is not a regular file"), err)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create current publication temporary input: %w", err)
	}
	_, copyErr := io.Copy(output, sourceFile)
	syncErr := output.Sync()
	closeErr := output.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("copy current publication to temporary input: %w", err)
	}
	return nil
}
