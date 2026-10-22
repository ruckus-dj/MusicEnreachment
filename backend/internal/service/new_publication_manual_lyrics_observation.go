package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// publicationManualLyricsObservationAuthority is the private, native readback
// authority for a completed manual-lyrics FLAC rewrite.
type publicationManualLyricsObservationAuthority struct {
	stage       *preparedPublicationStage
	input       persistence.PublicationInputs
	artwork     metadata.FlacArtworkSnapshot
	cover       *metadata.Cover
	scope       *PublicationExecutionScope
	reservation *publicationSyncReservation
	work        *privateManualLyricsWork
}

type publicationManualLyricsPreparationInputReader interface {
	PublicationManualLyricsPreparationInputs(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error)
}

func newPublicationManualLyricsObservationAuthority(
	ctx context.Context,
	scope *PublicationExecutionScope,
	reservation *publicationSyncReservation,
	initialCopy *publicationInitialOutputCopy,
	input persistence.PublicationInputs,
) (*publicationManualLyricsObservationAuthority, error) {
	if initialCopy == nil || initialCopy.stage == nil {
		return nil, errors.New("manual lyrics observation requires the native initial output copy")
	}
	stage := initialCopy.stage
	if !stage.privatePrepMode || !stage.rewrite || stage.outputName == "" || stage.rewriteIdentity.SHA256 == "" ||
		input.Version != persistence.PublicationInputsVersion || input.IntentKind != "rewrite" || input.Rewrite == nil ||
		input.ManualLyrics == nil || input.Validate() != nil ||
		!reflect.DeepEqual(input.Rewrite.Ownership, stage.rewriteIdentity) || scope == nil || scope.guard != stage.guard {
		return nil, errors.New("manual lyrics observation does not match the private prepared rewrite")
	}
	if scope.repository == nil {
		return nil, errors.New("manual lyrics observation repository is unavailable")
	}
	reader, ok := scope.repository.(publicationManualLyricsPreparationInputReader)
	if !ok {
		return nil, errors.New("manual lyrics preparation input reader is unavailable")
	}
	scope.mu.Lock()
	reserved := reservation != nil && reservation.owner == scope && reservation.active && !reservation.borrowed && scope.syncWork == 1 && scope.unresolved == 0 && !scope.unknown && !scope.finalized && !scope.finalizing
	scope.mu.Unlock()
	if !reserved {
		return nil, errors.New("manual lyrics observation requires the sole unborrowed active publication reservation")
	}
	frozen, valid, err := reader.PublicationManualLyricsPreparationInputs(ctx, stage.guard)
	if err != nil {
		return nil, err
	}
	if !valid || !reflect.DeepEqual(frozen, input) {
		return nil, errors.New("manual lyrics observation input differs from guarded frozen preparation")
	}
	if err := stage.Validate(ctx); err != nil {
		return nil, err
	}
	if err := stage.verifyInitialCopyInputs(ctx); err != nil {
		return nil, err
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return nil, err
	}
	var artwork metadata.FlacArtworkSnapshot
	var cover *metadata.Cover
	if input.Rewrite.AudioFacts.Container == "flac" {
		artwork, err = metadata.SnapshotFlacArtwork(ctx, stage.paths.InputPath)
	} else {
		capture, readErr := (metadata.Reader{}).Read(ctx, stage.paths.InputPath, metadata.ReadRequest{CoverInspection: true})
		err = readErr
		if err == nil {
			if capture.CoverInspection == nil || !capture.CoverInspection.Observed {
				err = errors.New("manual lyrics cover inspection is unavailable")
			} else if capture.CoverInspection.Cover != nil {
				value := *capture.CoverInspection.Cover
				value.Data = append([]byte(nil), value.Data...)
				cover = &value
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if input.Rewrite.AudioFacts.Container == publication.AudioContainerMKA && cover != nil {
		return nil, errors.New("MKA manual lyrics rewrite requires absent cover artwork")
	}
	return &publicationManualLyricsObservationAuthority{stage: stage, input: input.Clone(), artwork: artwork, cover: cover, scope: scope, reservation: reservation}, nil
}

func (authority *publicationManualLyricsObservationAuthority) verifyArtwork(ctx context.Context, path string) error {
	if authority.input.Rewrite.AudioFacts.Container == "flac" {
		return authority.artwork.VerifyEquivalent(ctx, path)
	}
	if authority.input.Rewrite.AudioFacts.Container == publication.AudioContainerMKA {
		if authority.work == nil || authority.scope == nil || authority.stage == nil || authority.stage.manualProbe == nil || path != authority.stage.paths.OutputPath ||
			!samePublicationToolSelections(authority.stage.manualProbe.selections, authority.input.Tools) {
			return errors.New("MKA manual lyrics artwork verification requires its exact private work")
		}
		if err := authority.stage.Validate(ctx); err != nil {
			return err
		}
		if err := authority.stage.verifyInitialCopyInputs(ctx); err != nil {
			return err
		}
		if err := authority.stage.verifyOutputDirectory(ctx); err != nil {
			return err
		}
		inputRoot, err := authority.stage.opener.OpenRoot(ctx, authority.stage.inputPath)
		if err != nil {
			return err
		}
		input, err := sourcefs.OpenRegularAt(ctx, inputRoot, authority.stage.inputName)
		if err != nil {
			return errors.Join(err, inputRoot.Close())
		}
		inputRaw, inputErr := authority.stage.manualProbe.probePrivateManualLyrics(ctx, authority.work, authority.stage, input)
		inputErr = errors.Join(inputErr, input.Close(), inputRoot.Close())
		if inputErr != nil {
			return fmt.Errorf("probe immutable MKA publication input: %w", inputErr)
		}
		inputAnalysis, err := ParseSourceTechnicalAnalysis(inputRaw)
		if err != nil {
			return err
		}
		inputFacts, err := observedManualLyricsMKAAudioFacts(inputRaw, inputAnalysis, authority.input.Rewrite.AudioFacts)
		if err != nil {
			return fmt.Errorf("validate immutable MKA publication input: %w", err)
		}
		if !reflect.DeepEqual(inputFacts, authority.input.Rewrite.AudioFacts) {
			return errors.New("immutable MKA publication input differs from frozen audio facts")
		}
		mutation, ok := authority.stage.directory.(sourcefs.OutputMutation)
		if !ok {
			return errors.New("native publication output identity capability is unavailable")
		}
		candidate, err := mutation.OpenExistingRegular(ctx, authority.stage.outputName)
		if err != nil {
			return err
		}
		candidateInfo, statErr := candidate.Stat(ctx)
		if statErr == nil {
			statErr = authority.stage.verifyInitialOutputEntry(ctx, candidateInfo)
		}
		if statErr != nil {
			return errors.Join(statErr, candidate.Close())
		}
		candidateRaw, candidateErr := authority.stage.manualProbe.probePrivateManualLyrics(ctx, authority.work, authority.stage, candidate)
		candidateErr = errors.Join(candidateErr, candidate.Close())
		if candidateErr != nil {
			return fmt.Errorf("probe MKA manual lyrics candidate: %w", candidateErr)
		}
		analysis, err := ParseSourceTechnicalAnalysis(candidateRaw)
		if err != nil {
			return err
		}
		candidateFacts, err := observedManualLyricsMKAAudioFacts(candidateRaw, analysis, authority.input.Rewrite.AudioFacts)
		if err != nil {
			return fmt.Errorf("validate MKA manual lyrics candidate: %w", err)
		}
		if !reflect.DeepEqual(candidateFacts, inputFacts) {
			return errors.New("MKA manual lyrics candidate differs from immutable input audio facts")
		}
		if err := authority.stage.verifyInitialOutputEntry(ctx, candidateInfo); err != nil {
			return err
		}
		if err := authority.stage.verifyInitialCopyInputs(ctx); err != nil {
			return err
		}
	}
	capture, err := (metadata.Reader{}).Read(ctx, path, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		return err
	}
	if capture.CoverInspection == nil || !capture.CoverInspection.Observed || !reflect.DeepEqual(capture.CoverInspection.Cover, authority.cover) {
		return errors.New("manual lyrics cover differs from the approved current artifact")
	}
	return nil
}

func (authority *publicationManualLyricsObservationAuthority) validateArtworkCapture(capture metadata.Capture) error {
	if authority.input.Rewrite.AudioFacts.Container == "flac" {
		return validateManualLyricsApprovedCover(authority.artwork.ApprovedCoverFacts(), capture)
	}
	if capture.CoverInspection == nil || !capture.CoverInspection.Observed || !reflect.DeepEqual(capture.CoverInspection.Cover, authority.cover) {
		return errors.New("manual lyrics cover differs from the approved current artifact")
	}
	return nil
}

func (authority *publicationManualLyricsObservationAuthority) acquireWork(proof *publicationInitialOutputCopy) (*privateManualLyricsWork, error) {
	if authority == nil || authority.scope == nil || authority.stage == nil || proof == nil || proof.stage != authority.stage {
		return nil, errors.New("manual lyrics work requires the authority's initial-copy proof")
	}
	work := authority.scope.acquirePrivateManualLyricsWork(authority.reservation, authority.stage, proof)
	if work == nil {
		return nil, errors.New("manual lyrics work requires the sole unborrowed publication reservation")
	}
	authority.work = work
	return work, nil
}

func (authority *publicationManualLyricsObservationAuthority) Observe(ctx context.Context) (result persistence.PublicationStageObservation, resultErr error) {
	if authority == nil || authority.stage == nil {
		return persistence.PublicationStageObservation{}, errors.New("manual lyrics observation authority is unavailable")
	}
	if authority.input.Rewrite.AudioFacts.Container == publication.AudioContainerMKA {
		return persistence.PublicationStageObservation{}, errors.New("MKA manual lyrics observation requires its private work capability")
	}
	if authority.scope == nil || !authority.scope.beginInitialOutputCopy(authority.reservation) {
		return persistence.PublicationStageObservation{}, errors.New("manual lyrics observation could not acquire its publication reservation")
	}
	completed := false
	defer func() { authority.scope.endInitialOutputCopy(authority.reservation, !completed) }()
	result, err := authority.observeBody(ctx)
	completed = err == nil
	return result, err
}

// observeBorrowed reads back under the same private mutation borrow; unlike
// Observe it does not acquire the initial-copy fence or release the borrow.
func (authority *publicationManualLyricsObservationAuthority) observeBorrowed(ctx context.Context, work *privateManualLyricsWork) (persistence.PublicationStageObservation, error) {
	if authority == nil || authority.scope == nil || authority.work != work || !authority.scope.validPrivateManualLyricsWork(work, authority.stage) {
		return persistence.PublicationStageObservation{}, errors.New("manual lyrics observation requires its exact private work capability")
	}
	return authority.observeBody(ctx)
}

func (authority *publicationManualLyricsObservationAuthority) observeBody(ctx context.Context) (result persistence.PublicationStageObservation, resultErr error) {
	stage := authority.stage
	if err := stage.Validate(ctx); err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	if err := stage.verifyInitialCopyInputs(ctx); err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	mutation, ok := stage.directory.(sourcefs.OutputMutation)
	if !ok {
		return persistence.PublicationStageObservation{}, errors.New("native publication output identity capability is unavailable")
	}
	name := stage.outputName
	native, err := mutation.OpenExistingRegular(ctx, name)
	if err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	info, err := native.Stat(ctx)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		err = errors.New("native publication output is not a regular file")
	}
	var digest [sha256.Size]byte
	if err == nil {
		err = native.Borrow(ctx, func(file *os.File) error {
			var hashErr error
			digest, hashErr = hashBorrowedFile(ctx, file)
			return hashErr
		})
	}
	if err == nil {
		err = stage.verifyInitialOutputEntry(ctx, info)
	}
	if err != nil {
		return persistence.PublicationStageObservation{}, errors.Join(err, native.Close())
	}
	var observedAudio persistence.PersistedAudioFacts
	frozen := authority.input.Rewrite.AudioFacts
	if frozen.Container == publication.AudioContainerMKA {
		if stage.manualProbe == nil || !samePublicationToolSelections(stage.manualProbe.selections, authority.input.Tools) {
			return persistence.PublicationStageObservation{}, errors.Join(errors.New("scope-bound manual lyrics audio probe is unavailable"), native.Close())
		}
		if err := native.Borrow(ctx, func(file *os.File) error {
			raw, err := stage.manualProbe.probePrivateManualLyrics(ctx, authority.work, stage, publicationStageFile{file})
			if err != nil {
				return err
			}
			analysis, err := ParseSourceTechnicalAnalysis(raw)
			if err != nil {
				return err
			}
			observedAudio, err = observedManualLyricsMKAAudioFacts(raw, analysis, frozen)
			return err
		}); err != nil {
			return persistence.PublicationStageObservation{}, errors.Join(fmt.Errorf("probe manual lyrics output audio: %w", err), native.Close())
		}
	} else {
		observedAudio = frozen
	}
	outputPathInfo, err := os.Stat(stage.paths.OutputPath)
	if err == nil && !os.SameFile(info, outputPathInfo) {
		err = errors.New("publication output path is not bound to the native staged file")
	}
	if err != nil {
		return persistence.PublicationStageObservation{}, errors.Join(err, native.Close())
	}
	syncer, ok := stage.directory.(sourcefs.OutputRegularFileSync)
	if !ok {
		return persistence.PublicationStageObservation{}, errors.Join(sourcefs.ErrUnsupported, native.Close())
	}
	if err := syncer.SyncExistingRegular(ctx, name, info); err != nil {
		return persistence.PublicationStageObservation{}, errors.Join(err, native.Close())
	}
	capture, err := (metadata.Reader{}).Read(ctx, stage.paths.OutputPath, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		return persistence.PublicationStageObservation{}, errors.Join(err, native.Close())
	}
	wantTags := make(map[string][]string, len(authority.input.DesiredTags))
	for key, values := range authority.input.DesiredTags {
		if key != "LYRICS" {
			wantTags[key] = append([]string(nil), values...)
		}
	}
	actualMandatory := make(map[string][]string, len(capture.Tags))
	for key, values := range capture.Tags {
		if key != "LYRICS" {
			actualMandatory[key] = values
		}
	}
	if !equalTags(actualMandatory, wantTags) {
		return persistence.PublicationStageObservation{}, errors.Join(errors.New("native publication metadata differs from frozen required tags"), native.Close())
	}
	if frozen.Container == publication.AudioContainerFLAC {
		if err := authority.artwork.VerifyEquivalent(ctx, stage.paths.OutputPath); err != nil {
			return persistence.PublicationStageObservation{}, errors.Join(err, native.Close())
		}
	} else if err := authority.verifyArtwork(ctx, stage.paths.OutputPath); err != nil {
		return persistence.PublicationStageObservation{}, errors.Join(err, native.Close())
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		return persistence.PublicationStageObservation{}, errors.Join(err, native.Close())
	}
	if frozen.Container == publication.AudioContainerFLAC {
		if err := validateManualLyricsApprovedCover(authority.artwork.ApprovedCoverFacts(), capture); err != nil {
			return persistence.PublicationStageObservation{}, errors.Join(err, native.Close())
		}
	} else if assets.Cover.State != persistence.PublicationAssetAbsent {
		return persistence.PublicationStageObservation{}, errors.Join(errors.New("MKA manual lyrics rewrite requires absent cover artwork"), native.Close())
	}
	if err := stage.Validate(ctx); err != nil {
		return result, errors.Join(err, native.Close())
	}
	if err := stage.verifyInitialCopyInputs(ctx); err != nil {
		return result, errors.Join(err, native.Close())
	}
	frozenInputs, valid, err := stage.repository.(publicationManualLyricsPreparationInputReader).PublicationManualLyricsPreparationInputs(ctx, stage.guard)
	if err != nil || !valid || !reflect.DeepEqual(frozenInputs, authority.input) {
		return result, errors.Join(errors.New("manual lyrics observation input differs from guarded frozen preparation"), err, native.Close())
	}
	pathInfo, err := os.Stat(stage.paths.OutputPath)
	if err != nil {
		return result, errors.Join(err, native.Close())
	}
	nativeAfter, err := native.Stat(ctx)
	if err != nil {
		return result, errors.Join(err, native.Close())
	}
	if !os.SameFile(info, pathInfo) || !os.SameFile(info, nativeAfter) || info.Size() != nativeAfter.Size() || info.ModTime().UnixMicro() != nativeAfter.ModTime().UnixMicro() {
		return result, errors.Join(errors.New("publication output identity or facts changed during readback"), native.Close())
	}
	var afterDigest [sha256.Size]byte
	if err := native.Borrow(ctx, func(file *os.File) error {
		var hashErr error
		afterDigest, hashErr = hashBorrowedFile(ctx, file)
		return hashErr
	}); err != nil {
		return result, errors.Join(err, native.Close())
	}
	if digest != afterDigest {
		return result, errors.Join(errors.New("publication output digest changed during readback"), native.Close())
	}
	if err := stage.verifyInitialOutputEntry(ctx, nativeAfter); err != nil {
		return result, errors.Join(err, native.Close())
	}
	if err := stage.Validate(ctx); err != nil {
		return result, errors.Join(err, native.Close())
	}
	if err := stage.verifyInitialCopyInputs(ctx); err != nil {
		return result, errors.Join(err, native.Close())
	}
	if err := native.Close(); err != nil {
		return result, err
	}
	audioFacts := observedAudio
	if frozen.Container == publication.AudioContainerFLAC {
		audioFacts = frozen
	}
	audio, err := json.Marshal(persistence.PublicationCurrentAudio{Version: 1, Facts: audioFacts})
	if err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	commitMetadata, err := json.Marshal(persistence.PublicationCommitMetadata{Version: 1, Tags: capture.Tags})
	if err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	assetJSON, err := json.Marshal(assets)
	if err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	actualSize, actualMTime := info.Size(), info.ModTime().UnixMicro()
	result = persistence.PublicationStageObservation{Version: 1, RelativePath: stage.paths.RelativePath, Ownership: persistence.ArtifactOwnership{ExpectedSize: &actualSize, ExpectedMTime: &actualMTime}, PayloadSHA256: hex.EncodeToString(digest[:]), ActualAudio: audio, ActualMetadata: commitMetadata, ActualAssets: assetJSON, QualityPolicyVersion: authority.input.QualityPolicyVersion}
	return result, nil
}

func observedManualLyricsMKAAudioFacts(raw []byte, analysis SourceTechnicalAnalysis, frozen persistence.PersistedAudioFacts) (persistence.PersistedAudioFacts, error) {
	if err := validatePublicationTopology(raw); err != nil {
		return persistence.PersistedAudioFacts{}, err
	}
	if len(analysis.Streams) != 1 || analysis.Container.Name == nil || strings.TrimSpace(*analysis.Container.Name) == "" {
		return persistence.PersistedAudioFacts{}, errors.New("manual lyrics output must contain one observed audio stream and container")
	}
	actual := analysis.Streams[0]
	if actual.Index == nil || *actual.Index != int64(frozen.StreamIndex) || actual.CodecName == nil || *actual.CodecName != frozen.Codec ||
		!publicationContainerMatches(*analysis.Container.Name, frozen.Container) ||
		(frozen.Channels != nil && (actual.Channels == nil || *actual.Channels != int64(*frozen.Channels))) ||
		!publicationStageSampleRateMatches(frozen.SampleRate, actual.SampleRateHz) ||
		(frozen.Bitrate != nil && (actual.BitRate == nil || *actual.BitRate != int64(*frozen.Bitrate))) ||
		(frozen.BitDepth != nil && (actual.BitsPerSample == nil || *actual.BitsPerSample != int64(*frozen.BitDepth))) {
		return persistence.PersistedAudioFacts{}, errors.New("manual lyrics output audio facts do not preserve the current published stream")
	}
	lossless, known := publicationCodecLossless(*actual.CodecName)
	if !known {
		return persistence.PersistedAudioFacts{}, errors.New("manual lyrics output codec losslessness is unknown")
	}
	return persistedPublicationStageAudioFacts(int(*actual.Index), *actual.CodecName, lossless, actual.Channels, actual.BitRate, actual.BitsPerSample, actual.SampleRateHz, frozen.Container)
}

// validateManualLyricsApprovedCover permits only absence or the exact single
// native FLAC picture captured before rewriting. CoverInspection is TagLib's
// bounded first-picture observation, not a complete inventory.
func validateManualLyricsApprovedCover(approved metadata.ApprovedCoverFacts, capture metadata.Capture) error {
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		return err
	}
	switch approved.Count {
	case 0:
		if assets.Cover.State != persistence.PublicationAssetAbsent {
			return errors.New("native publication cover differs from frozen artwork")
		}
	case 1:
		if assets.Cover.State != persistence.PublicationAssetEmbedded || assets.Cover.SHA256 != hex.EncodeToString(approved.ImageSHA256[:]) || assets.Cover.SizeBytes == nil || *assets.Cover.SizeBytes != int64(approved.ImageSize) {
			return errors.New("native publication cover differs from frozen artwork")
		}
	default:
		return errors.New("native publication artwork count is unsupported")
	}
	return nil
}
