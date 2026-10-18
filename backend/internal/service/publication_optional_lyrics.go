package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// publicationOptionalLyricsResult describes what was actually observed after
// the optional lyrics attempt. A diagnostic is safe to report but is not
// evidence that any other optional asset is absent.
type publicationOptionalLyricsResult struct {
	Tags               map[string][]string
	Lyrics             persistence.PublicationLyricsOutcome
	OptionalDiagnostic string
}

type publicationOptionalLyricsRestorer interface {
	rebuildOutputCopy(context.Context, *PublicationExecutionScope, *publicationOptionalLyricsBorrow) error
}

// publicationOptionalLyricsRequirements keeps audio and required-asset evidence
// independent from the optional lyrics readback. Both callbacks are mandatory:
// rebuilding the native output invalidates any earlier asset observations.
type publicationOptionalLyricsRequirements struct {
	validateAudioAndAssets func(context.Context, string) error
	restoreRequiredAssets  func(context.Context, string, map[string][]string) error
	// recordOutcome is installed only by the V3 publication application entry.
	// The legacy V2 helper path deliberately has no durable-outcome authority.
	recordOutcome func(context.Context, publicationOptionalLyricsOutcomeEvidence) error
}

type publicationOptionalLyricsOutcomeEvidence struct {
	Kind       string
	Tags       map[string][]string
	Lyrics     persistence.PublicationLyricsOutcome
	Diagnostic string
}

type publicationOptionalLyricsOutcomeRepository interface {
	GuardedRecordPublicationOptionalLyricsOutcome(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationOptionalLyricsOutcomeEvidence) (persistence.PublicationOptionalLyricsOutcome, error)
}

// applyAndRecordPublicationManualLyrics is the private V3-only application path.
// Evidence is derived from the prepared native handles and the required final
// validator; callers cannot supply identities or a fabricated observation.
func applyAndRecordPublicationManualLyrics(
	ctx context.Context,
	scope *PublicationExecutionScope,
	reservation *publicationSyncReservation,
	stage PreparedPublicationStage,
	input persistence.PublicationInputs,
	writer PublicationMetadataWriter,
	reader interface {
		Read(context.Context, string, metadata.ReadRequest) (metadata.Capture, error)
	},
	validateAudioAndAssets func(context.Context, string) error,
	restoreRequiredAssets func(context.Context, string, map[string][]string) error,
	finalObservationAuthority func(context.Context, string, persistence.PublicationInputs) (persistence.PublicationStageObservation, error),
) (publicationOptionalLyricsResult, error) {
	prepared, ok := stage.(*preparedPublicationStage)
	if !ok || prepared == nil || prepared.repository == nil || finalObservationAuthority == nil || input.Version != persistence.PublicationInputsVersion3 || input.ManualLyrics == nil || input.Rewrite == nil {
		return publicationOptionalLyricsResult{}, errors.New("V3 optional lyrics evidence authority is unavailable")
	}
	repo, ok := prepared.repository.(publicationOptionalLyricsOutcomeRepository)
	if !ok {
		return publicationOptionalLyricsResult{}, errors.New("V3 optional lyrics outcome repository is unavailable")
	}
	requirements := publicationOptionalLyricsRequirements{validateAudioAndAssets: validateAudioAndAssets, restoreRequiredAssets: restoreRequiredAssets}
	requirements.recordOutcome = func(ctx context.Context, outcome publicationOptionalLyricsOutcomeEvidence) error {
		if err := checkOptionalLyricsStage(ctx, scope, stage); err != nil {
			return err
		}
		inputIdentity, outputIdentity, err := prepared.optionalLyricsFileIdentities(ctx)
		if err != nil {
			return err
		}
		ready := prepared.preparationRecord
		if ready.Step != "input_ready" || ready.CopiedInput == nil || ready.InputSize == nil || ready.InputMTime == nil {
			return errors.New("prepared publication input readiness evidence is unavailable")
		}
		copied := ready.CopiedInput
		if copied.SizeBytes == nil || copied.MTime == nil || inputIdentity.SizeBytes == nil || inputIdentity.MTime == nil || prepared.sourceFileInfo == nil || *inputIdentity.SizeBytes != *copied.SizeBytes || *inputIdentity.MTime != *copied.MTime || *inputIdentity.SizeBytes != prepared.sourceFileInfo.Size() || *inputIdentity.MTime != prepared.sourceFileInfo.ModTime().UnixMicro() || input.Rewrite.Ownership.SizeBytes != nil && *ready.InputSize != *input.Rewrite.Ownership.SizeBytes {
			return errors.New("prepared publication input identity does not match its frozen preparation")
		}
		if input.Rewrite.Ownership.SizeBytes != nil && *inputIdentity.SizeBytes != *input.Rewrite.Ownership.SizeBytes || input.Rewrite.Ownership.SHA256 != "" && inputIdentity.SHA256 != input.Rewrite.Ownership.SHA256 {
			return errors.New("prepared publication input identity does not match frozen rewrite ownership")
		}
		observation, err := finalObservationAuthority(ctx, prepared.paths.OutputPath, input.Clone())
		if err != nil {
			return fmt.Errorf("validate final publication stage for optional lyrics outcome: %w", err)
		}
		if observation.RelativePath != ready.OutputRel || observation.Ownership.ExpectedSize == nil || *observation.Ownership.ExpectedSize != *outputIdentity.SizeBytes || observation.Ownership.ExpectedMTime == nil || *observation.Ownership.ExpectedMTime != *outputIdentity.MTime || observation.PayloadSHA256 != outputIdentity.SHA256 {
			return errors.New("final publication observation does not match native output identity")
		}
		kind, diagnostic := persistence.PublicationOptionalLyricsExactRoundTrip, ""
		if outcome.Kind == "rebuilt_without_lyrics" {
			kind, diagnostic = persistence.PublicationOptionalLyricsRebuiltWithoutLyrics, "optional lyrics did not round-trip; rebuilt output without lyrics"
		}
		evidence := persistence.PublicationOptionalLyricsOutcomeEvidence{Kind: kind, ManualLyrics: *input.ManualLyrics, Preparation: ready, InputIdentity: inputIdentity, OutputIdentity: outputIdentity, FinalObservation: observation.Clone(), Diagnostic: diagnostic}
		result, err := repo.GuardedRecordPublicationOptionalLyricsOutcome(ctx, scope.guard, evidence)
		if err != nil {
			return err
		}
		if result != persistence.PublicationOptionalLyricsRecorded && result != persistence.PublicationOptionalLyricsAlreadyRecorded {
			return errors.New("publication delivery is stale while recording optional lyrics outcome")
		}
		return nil
	}
	return applyPublicationOptionalLyrics(ctx, scope, reservation, stage, input, writer, reader, requirements)
}

type preparedManualLyricsFLACMode uint8

const (
	preparedManualLyricsOutcomeOnly preparedManualLyricsFLACMode = iota
	preparedManualLyricsThroughValidated
	preparedManualLyricsThroughExposureAdmission
)

type preparedManualLyricsStageRepository interface {
	GuardedAdvanceManualLyricsStage(context.Context, persistence.PublicationDeliveryGuard, string, persistence.PublicationStageObservation) (persistence.ManualLyricsStageOutcome, error)
}

// applyAndRecordPreparedManualLyricsFLAC preserves the private outcome-only
// entry point used while a delivery is preparing.
func applyAndRecordPreparedManualLyricsFLAC(
	ctx context.Context,
	scope *PublicationExecutionScope,
	reservation *publicationSyncReservation,
	initialCopy *publicationInitialOutputCopy,
	input persistence.PublicationInputs,
) (result publicationOptionalLyricsResult, resultErr error) {
	return applyPreparedManualLyricsFLAC(ctx, scope, reservation, initialCopy, input, preparedManualLyricsOutcomeOnly)
}

// validatePreparedManualLyricsFLAC owns the uninterrupted private handoff from
// the outcome acknowledgement through staged and validated acknowledgements.
func validatePreparedManualLyricsFLAC(
	ctx context.Context,
	scope *PublicationExecutionScope,
	reservation *publicationSyncReservation,
	initialCopy *publicationInitialOutputCopy,
	input persistence.PublicationInputs,
) (publicationOptionalLyricsResult, error) {
	return applyPreparedManualLyricsFLAC(ctx, scope, reservation, initialCopy, input, preparedManualLyricsThroughValidated)
}

// admitPreparedManualLyricsFLACExposure performs the same private rewrite and
// ordered stage acknowledgements as validation, then admits exposure without
// exposing caller-controlled intent or an observation value.
func admitPreparedManualLyricsFLACExposure(
	ctx context.Context,
	scope *PublicationExecutionScope,
	reservation *publicationSyncReservation,
	initialCopy *publicationInitialOutputCopy,
	input persistence.PublicationInputs,
) (publicationOptionalLyricsResult, error) {
	return applyPreparedManualLyricsFLAC(ctx, scope, reservation, initialCopy, input, preparedManualLyricsThroughExposureAdmission)
}

func applyPreparedManualLyricsFLAC(
	ctx context.Context,
	scope *PublicationExecutionScope,
	reservation *publicationSyncReservation,
	initialCopy *publicationInitialOutputCopy,
	input persistence.PublicationInputs,
	mode preparedManualLyricsFLACMode,
) (result publicationOptionalLyricsResult, resultErr error) {
	switch mode {
	case preparedManualLyricsOutcomeOnly, preparedManualLyricsThroughValidated, preparedManualLyricsThroughExposureAdmission:
	default:
		return result, errors.New("unsupported prepared manual lyrics mode")
	}
	if initialCopy == nil || initialCopy.stage == nil {
		return result, errors.New("manual lyrics application requires its initial output copy")
	}
	stage := initialCopy.stage
	authority, err := newPublicationManualLyricsObservationAuthority(ctx, scope, reservation, initialCopy, input)
	if err != nil {
		return result, err
	}
	work, err := authority.acquireWork(initialCopy)
	if err != nil {
		return result, err
	}
	completed := false
	defer func() {
		if recovered := recover(); recovered != nil {
			scope.quarantineUncertain()
			panic(recovered)
		}
		if !completed {
			scope.quarantineUncertain()
		}
	}()

	mandatory := withoutLyrics(input.DesiredTags)
	artwork := authority.artwork
	writer := preparedManualLyricsFLACWriter{authority: authority}
	reader := metadata.Reader{}
	validate := func(ctx context.Context, path string) error {
		if err := artwork.VerifyEquivalent(ctx, path); err != nil {
			return err
		}
		capture, err := reader.Read(ctx, path, metadata.ReadRequest{CoverInspection: true})
		if err != nil {
			return err
		}
		if !equalTags(withoutLyrics(capture.Tags), mandatory) {
			return errors.New("required publication metadata did not round-trip")
		}
		if err := validateManualLyricsApprovedCover(artwork.ApprovedCoverFacts(), capture); err != nil {
			return err
		}
		return nil
	}
	requirements := publicationOptionalLyricsRequirements{validateAudioAndAssets: validate, restoreRequiredAssets: func(ctx context.Context, path string, _ map[string][]string) error {
		if err := artwork.VerifyEquivalent(ctx, path); err != nil {
			return err
		}
		capture, err := reader.Read(ctx, path, metadata.ReadRequest{CoverInspection: true})
		if err != nil {
			return err
		}
		if err := validateManualLyricsApprovedCover(artwork.ApprovedCoverFacts(), capture); err != nil {
			return err
		}
		return nil
	}}
	repo, ok := stage.repository.(publicationOptionalLyricsOutcomeRepository)
	if !ok {
		return result, errors.New("V3 optional lyrics outcome repository is unavailable")
	}
	var stageRepo preparedManualLyricsStageRepository
	if mode == preparedManualLyricsThroughValidated || mode == preparedManualLyricsThroughExposureAdmission {
		var capable bool
		stageRepo, capable = stage.repository.(preparedManualLyricsStageRepository)
		if !capable {
			return result, errors.New("manual lyrics stage acknowledgement repository is unavailable")
		}
		if _, capable := stage.repository.(publicationManualLyricsCurrentInputReader); !capable {
			return result, errors.New("private manual lyrics current-input reader is unavailable")
		}
	}
	if mode == preparedManualLyricsThroughExposureAdmission {
		if _, capable := stage.repository.(preparedManualLyricsExposureRepository); !capable {
			return result, errors.New("manual lyrics exposure repository is unavailable")
		}
	}
	var acknowledged persistence.PublicationStageObservation
	var acknowledgedOutputInfo os.FileInfo
	requirements.recordOutcome = func(ctx context.Context, outcome publicationOptionalLyricsOutcomeEvidence) error {
		observation, err := authority.observeBorrowed(ctx, work)
		if err != nil {
			return err
		}
		inputIdentity, outputIdentity, err := stage.optionalLyricsFileIdentities(ctx)
		if err != nil {
			return err
		}
		acknowledgedOutputInfo, err = stage.nativeOutputFileInfo(ctx)
		if err != nil {
			return err
		}
		ready := cloneManualLyricsPreparationRecord(stage.preparationRecord)
		if ready.Step != "input_ready" || ready.CopiedInput == nil || ready.InputSize == nil || ready.InputMTime == nil {
			return errors.New("prepared publication input readiness evidence is unavailable")
		}
		if inputIdentity.SHA256 != stage.rewriteIdentity.SHA256 || observation.PayloadSHA256 != outputIdentity.SHA256 || observation.RelativePath != ready.OutputRel {
			return errors.New("prepared manual lyrics identities differ from final native observation")
		}
		kind, diagnostic := persistence.PublicationOptionalLyricsExactRoundTrip, ""
		if outcome.Kind == "rebuilt_without_lyrics" {
			kind, diagnostic = persistence.PublicationOptionalLyricsRebuiltWithoutLyrics, "optional lyrics did not round-trip; rebuilt output without lyrics"
		}
		acknowledged = observation.Clone()
		evidence := persistence.PublicationOptionalLyricsOutcomeEvidence{Kind: kind, ManualLyrics: *input.ManualLyrics, Preparation: ready, InputIdentity: clonePublicationIdentity(inputIdentity), OutputIdentity: clonePublicationIdentity(outputIdentity), FinalObservation: observation.Clone(), Diagnostic: diagnostic}
		ack, err := repo.GuardedRecordPublicationOptionalLyricsOutcome(ctx, scope.guard, evidence)
		if err != nil {
			return err
		}
		if ack != persistence.PublicationOptionalLyricsRecorded && ack != persistence.PublicationOptionalLyricsAlreadyRecorded {
			return errors.New("publication delivery is stale while recording optional lyrics outcome")
		}
		return nil
	}
	result, err = applyPublicationOptionalLyricsWork(ctx, scope, work, stage, input, writer, reader, requirements)
	if err != nil {
		return result, err
	}
	if mode == preparedManualLyricsThroughValidated || mode == preparedManualLyricsThroughExposureAdmission {
		if !scope.validPrivateManualLyricsWork(work, stage) {
			return result, errors.New("manual lyrics publication work capability was lost before staged acknowledgement")
		}
		live, err := scope.repository.CheckPublicationDelivery(ctx, scope.guard)
		if err != nil {
			return result, fmt.Errorf("check publication delivery before staged acknowledgement: %w", err)
		}
		if !live {
			return result, errors.New("publication delivery is stale before staged acknowledgement")
		}
		if err := stage.Validate(ctx); err != nil {
			return result, err
		}
		if err := stage.checkPrivateManualLyricsCheckpoint(ctx, scope, work, acknowledged, acknowledgedOutputInfo); err != nil {
			return result, err
		}
		fresh, err := authority.observeBorrowed(ctx, work)
		if err != nil {
			return result, err
		}
		ackedJSON, ackedErr := json.Marshal(acknowledged)
		freshJSON, freshErr := json.Marshal(fresh)
		if ackedErr != nil || freshErr != nil || !reflect.DeepEqual(acknowledged, fresh) || !bytes.Equal(ackedJSON, freshJSON) {
			return result, errors.New("prepared manual lyrics observation changed after outcome acknowledgement")
		}
		staged, err := stageRepo.GuardedAdvanceManualLyricsStage(ctx, scope.guard, "preparing", acknowledged.Clone())
		if err != nil || (staged != persistence.ManualLyricsStageAdvanced && staged != persistence.ManualLyricsStageAlreadyRecorded) {
			if err != nil {
				return result, err
			}
			return result, errors.New("publication delivery is stale while acknowledging staged manual lyrics")
		}
		if err := stage.checkPrivateManualLyricsCheckpoint(ctx, scope, work, acknowledged, acknowledgedOutputInfo); err != nil {
			return result, err
		}
		validated, err := stageRepo.GuardedAdvanceManualLyricsStage(ctx, scope.guard, "staged", acknowledged.Clone())
		if err != nil || (validated != persistence.ManualLyricsStageAdvanced && validated != persistence.ManualLyricsStageAlreadyRecorded) {
			if err != nil {
				return result, err
			}
			return result, errors.New("publication delivery is stale while acknowledging validated manual lyrics")
		}
		if err := stage.checkPrivateManualLyricsPostValidationCheckpoint(ctx, scope, work, acknowledged, acknowledgedOutputInfo); err != nil {
			return result, err
		}
	}
	if mode == preparedManualLyricsThroughExposureAdmission {
		if err := admitPreparedManualLyricsExposure(ctx, scope, work, acknowledged); err != nil {
			return result, err
		}
	}
	if !scope.finishPrivateManualLyricsWork(work, true) {
		return result, errors.New("manual lyrics publication work capability was lost before acknowledgement")
	}
	completed = true
	return result, nil
}

func cloneManualLyricsPreparationRecord(record persistence.PublicationPreparationRecord) persistence.PublicationPreparationRecord {
	if record.DirectoryIdentity != nil {
		value := *record.DirectoryIdentity
		record.DirectoryIdentity = &value
	}
	if record.InputSize != nil {
		value := *record.InputSize
		record.InputSize = &value
	}
	if record.InputMTime != nil {
		value := *record.InputMTime
		record.InputMTime = &value
	}
	if record.CopiedInput != nil {
		value := clonePublicationIdentity(*record.CopiedInput)
		record.CopiedInput = &value
	}
	return record
}

// checkPrivateManualLyricsCheckpoint validates the generic stage and revalidates
// the exact private output without deriving new observation evidence.
func (stage *preparedPublicationStage) checkPrivateManualLyricsCheckpoint(
	ctx context.Context,
	scope *PublicationExecutionScope,
	work *privateManualLyricsWork,
	acknowledged persistence.PublicationStageObservation,
	expectedNative os.FileInfo,
) error {
	if !scope.validPrivateManualLyricsWork(work, stage) {
		return errors.New("manual lyrics publication work capability was lost at staged checkpoint")
	}
	live, err := scope.repository.CheckPublicationDelivery(ctx, scope.guard)
	if err != nil {
		return fmt.Errorf("check publication delivery at staged checkpoint: %w", err)
	}
	if !live {
		return errors.New("publication delivery is stale at staged checkpoint")
	}
	if err := stage.Validate(ctx); err != nil {
		return err
	}
	return stage.checkPrivateManualLyricsNativeCheckpoint(ctx, acknowledged, expectedNative)
}

// checkPrivateManualLyricsPostValidationCheckpoint requires the private
// manual-lyrics validation capability after the validated acknowledgement.
func (stage *preparedPublicationStage) checkPrivateManualLyricsPostValidationCheckpoint(
	ctx context.Context,
	scope *PublicationExecutionScope,
	work *privateManualLyricsWork,
	acknowledged persistence.PublicationStageObservation,
	expectedNative os.FileInfo,
) error {
	if !scope.validPrivateManualLyricsWork(work, stage) {
		return errors.New("manual lyrics publication work capability was lost after validated checkpoint")
	}
	live, err := scope.repository.CheckPublicationDelivery(ctx, scope.guard)
	if err != nil {
		return fmt.Errorf("check publication delivery after validated checkpoint: %w", err)
	}
	if !live {
		return errors.New("publication delivery is stale after validated checkpoint")
	}
	if err := stage.validatePrivateManualLyrics(ctx, scope, work); err != nil {
		return err
	}
	return stage.checkPrivateManualLyricsNativeCheckpoint(ctx, acknowledged, expectedNative)
}

func (stage *preparedPublicationStage) checkPrivateManualLyricsNativeCheckpoint(
	ctx context.Context,
	acknowledged persistence.PublicationStageObservation,
	expectedNative os.FileInfo,
) error {
	if err := stage.verifyInitialCopyInputs(ctx); err != nil {
		return err
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return err
	}
	if expectedNative == nil || acknowledged.PayloadSHA256 == "" || acknowledged.Ownership.ExpectedSize == nil || acknowledged.Ownership.ExpectedMTime == nil {
		return errors.New("manual lyrics staged checkpoint lacks native output ownership")
	}
	mutation, ok := stage.directory.(sourcefs.OutputMutation)
	if !ok {
		return errors.New("native publication output identity capability is unavailable")
	}
	file, err := mutation.OpenExistingRegular(ctx, stage.outputName)
	if err != nil {
		return err
	}
	info, statErr := file.Stat(ctx)
	if statErr == nil && (info == nil || !info.Mode().IsRegular() || !os.SameFile(expectedNative, info) || info.Size() != *acknowledged.Ownership.ExpectedSize || info.ModTime().UnixMicro() != *acknowledged.Ownership.ExpectedMTime) {
		statErr = errors.New("manual lyrics output identity changed after acknowledgement")
	}
	var digest [sha256.Size]byte
	if statErr == nil {
		statErr = file.Borrow(ctx, func(handle *os.File) error {
			var hashErr error
			digest, hashErr = hashBorrowedFile(ctx, handle)
			return hashErr
		})
	}
	if statErr == nil && hex.EncodeToString(digest[:]) != acknowledged.PayloadSHA256 {
		statErr = errors.New("manual lyrics output digest changed after acknowledgement")
	}
	if statErr == nil {
		after, err := file.Stat(ctx)
		if err != nil {
			statErr = err
		} else if !os.SameFile(info, after) || after.Size() != info.Size() || after.ModTime().UnixMicro() != info.ModTime().UnixMicro() {
			statErr = errors.New("manual lyrics output changed while hashing checkpoint")
		}
	}
	statErr = errors.Join(statErr, file.Close())
	if statErr != nil {
		return statErr
	}
	return stage.verifyInitialOutputEntry(ctx, expectedNative)
}

type preparedManualLyricsFLACWriter struct {
	authority *publicationManualLyricsObservationAuthority
}

func (writer preparedManualLyricsFLACWriter) Write(ctx context.Context, path string, tags map[string][]string, cover *metadata.Cover) (metadata.WriteResult, error) {
	if cover != nil || writer.authority == nil || writer.authority.stage == nil || path != writer.authority.stage.paths.OutputPath {
		return metadata.WriteResult{}, errors.New("manual lyrics FLAC writer requires its owned path and no cover")
	}
	if err := writer.authority.artwork.VerifyEquivalent(ctx, path); err != nil {
		return metadata.WriteResult{}, err
	}
	before, err := metadata.SnapshotFlacArtwork(ctx, path)
	if err != nil {
		return metadata.WriteResult{}, err
	}
	if err := writer.authority.artwork.VerifyEquivalent(ctx, path); err != nil {
		return metadata.WriteResult{}, err
	}
	result, err := metadata.WriteFlacTagsPreservingArtwork(ctx, path, tags, before)
	if err != nil {
		return metadata.WriteResult{}, err
	}
	if err := writer.authority.artwork.VerifyEquivalent(ctx, path); err != nil {
		return metadata.WriteResult{}, err
	}
	return result, nil
}

func (stage *preparedPublicationStage) optionalLyricsFileIdentities(ctx context.Context) (persistence.PublicationFileIdentity, persistence.PublicationFileIdentity, error) {
	inputRoot, err := stage.opener.OpenRoot(ctx, stage.inputPath)
	if err != nil {
		return persistence.PublicationFileIdentity{}, persistence.PublicationFileIdentity{}, err
	}
	input, err := sourcefs.OpenRegularAt(ctx, inputRoot, stage.inputName)
	if err != nil {
		return persistence.PublicationFileIdentity{}, persistence.PublicationFileIdentity{}, errors.Join(err, inputRoot.Close())
	}
	inputIdentity, err := optionalLyricsIdentity(ctx, input)
	err = errors.Join(err, input.Close(), inputRoot.Close())
	if err != nil {
		return persistence.PublicationFileIdentity{}, persistence.PublicationFileIdentity{}, err
	}
	mutation, ok := stage.directory.(sourcefs.OutputMutation)
	if !ok {
		return persistence.PublicationFileIdentity{}, persistence.PublicationFileIdentity{}, errors.New("native publication output identity capability is unavailable")
	}
	name := filepath.Base(stage.paths.OutputPath)
	output, err := mutation.OpenExistingRegular(ctx, name)
	if err != nil {
		return persistence.PublicationFileIdentity{}, persistence.PublicationFileIdentity{}, err
	}
	outputIdentity, err := optionalLyricsIdentity(ctx, output)
	err = errors.Join(err, output.Close())
	return inputIdentity, outputIdentity, err
}

func (stage *preparedPublicationStage) nativeOutputFileInfo(ctx context.Context) (os.FileInfo, error) {
	mutation, ok := stage.directory.(sourcefs.OutputMutation)
	if !ok {
		return nil, errors.New("native publication output identity capability is unavailable")
	}
	file, err := mutation.OpenExistingRegular(ctx, stage.outputName)
	if err != nil {
		return nil, err
	}
	info, statErr := file.Stat(ctx)
	if statErr == nil && (info == nil || !info.Mode().IsRegular()) {
		statErr = errors.New("publication identity handle is not a regular file")
	}
	return info, errors.Join(statErr, file.Close())
}

func optionalLyricsIdentity(ctx context.Context, file sourcefs.RegularFile) (persistence.PublicationFileIdentity, error) {
	info, err := file.Stat(ctx)
	if err != nil {
		return persistence.PublicationFileIdentity{}, err
	}
	if info == nil || !info.Mode().IsRegular() {
		return persistence.PublicationFileIdentity{}, errors.New("publication identity handle is not a regular file")
	}
	var digest [sha256.Size]byte
	err = file.Borrow(ctx, func(handle *os.File) error {
		var hashErr error
		digest, hashErr = hashBorrowedFile(ctx, handle)
		return hashErr
	})
	if err != nil {
		return persistence.PublicationFileIdentity{}, err
	}
	after, err := file.Stat(ctx)
	if err != nil {
		return persistence.PublicationFileIdentity{}, err
	}
	if after.Size() != info.Size() || after.ModTime().UnixMicro() != info.ModTime().UnixMicro() {
		return persistence.PublicationFileIdentity{}, errors.New("publication file changed while deriving outcome identity")
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	return persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:])}, nil
}

// applyPublicationOptionalLyrics applies frozen V3 lyrics intent without
// changing the V3 execution gate. The stage is already prepared and exclusively
// owned by scope; the writer is always given the complete required tag map
// because TagLib's Clear option removes every normalized tag.
func applyPublicationOptionalLyrics(
	ctx context.Context,
	scope *PublicationExecutionScope,
	reservation *publicationSyncReservation,
	stage PreparedPublicationStage,
	input persistence.PublicationInputs,
	writer PublicationMetadataWriter,
	reader interface {
		Read(context.Context, string, metadata.ReadRequest) (metadata.Capture, error)
	},
	requirements publicationOptionalLyricsRequirements,
) (result publicationOptionalLyricsResult, resultErr error) {
	if err := validateOptionalLyricsRequest(ctx, scope, reservation, stage, input, writer, reader, requirements); err != nil {
		return result, err
	}
	borrow := scope.borrowOptionalLyricsWork(reservation)
	if borrow == nil {
		return result, errors.New("optional lyrics helper could not borrow its active reservation")
	}
	result, err := applyPublicationOptionalLyricsBody(ctx, scope, borrow, stage, input, writer, reader, requirements)
	if err == nil {
		scope.releaseOptionalLyricsWork(borrow)
	}
	return result, err
}

// applyPublicationOptionalLyricsWork is the only mutation entry for private
// prepared manual-lyrics work. Pointer identity and stage/proof binding are
// checked before any metadata or filesystem mutation.
func applyPublicationOptionalLyricsWork(
	ctx context.Context,
	scope *PublicationExecutionScope,
	work *privateManualLyricsWork,
	stage PreparedPublicationStage,
	input persistence.PublicationInputs,
	writer PublicationMetadataWriter,
	reader interface {
		Read(context.Context, string, metadata.ReadRequest) (metadata.Capture, error)
	},
	requirements publicationOptionalLyricsRequirements,
) (publicationOptionalLyricsResult, error) {
	prepared, ok := stage.(*preparedPublicationStage)
	if !ok || prepared == nil || scope == nil || !scope.validPrivateManualLyricsWork(work, prepared) || work.borrow == nil || work.borrow != work.reservation.borrow {
		return publicationOptionalLyricsResult{}, errors.New("optional lyrics mutation requires its exact private work capability")
	}
	return applyPublicationOptionalLyricsBody(ctx, scope, work.borrow, stage, input, writer, reader, requirements)
}

func applyPublicationOptionalLyricsBody(
	ctx context.Context,
	scope *PublicationExecutionScope,
	borrow *publicationOptionalLyricsBorrow,
	stage PreparedPublicationStage,
	input persistence.PublicationInputs,
	writer PublicationMetadataWriter,
	reader interface {
		Read(context.Context, string, metadata.ReadRequest) (metadata.Capture, error)
	},
	requirements publicationOptionalLyricsRequirements,
) (result publicationOptionalLyricsResult, resultErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			scope.quarantineUncertain()
			panic(recovered)
		}
		if resultErr != nil {
			scope.quarantineUncertain()
			return
		}
	}()
	paths := stage.Paths()
	mandatory := withoutLyrics(input.DesiredTags)
	requested, expectedLyrics, err := optionalLyricsTags(mandatory, input.ManualLyrics)
	if err != nil {
		return result, err
	}
	if err := checkOptionalLyricsStage(ctx, scope, stage); err != nil {
		return result, err
	}
	if err := requirements.validateAudioAndAssets(ctx, paths.OutputPath); err != nil {
		return result, fmt.Errorf("validate required publication audio and assets before optional lyrics write: %w", err)
	}
	// Any writer error is opaque: it may represent a failed mandatory write,
	// unsafe filesystem operation, cancellation, or TagLib failure. Do not
	// downgrade it to an optional warning.
	if _, err := writer.Write(ctx, paths.OutputPath, requested, nil); err != nil {
		scope.quarantineUncertain()
		return result, fmt.Errorf("write publication metadata and optional lyrics: %w", err)
	}
	if err := requirements.restoreRequiredAssets(ctx, paths.OutputPath, requested); err != nil {
		scope.quarantineUncertain()
		return result, fmt.Errorf("restore required publication assets after optional lyrics write: %w", err)
	}
	observed, err := reader.Read(ctx, paths.OutputPath, metadata.ReadRequest{})
	if err != nil {
		return result, fmt.Errorf("inspect publication metadata after optional lyrics: %w", err)
	}
	if err := checkOptionalLyricsStage(ctx, scope, stage); err != nil {
		return result, err
	}
	if err := requirements.validateAudioAndAssets(ctx, paths.OutputPath); err != nil {
		return result, fmt.Errorf("validate publication audio and assets after optional lyrics write: %w", err)
	}
	if !equalTags(withoutLyrics(observed.Tags), mandatory) {
		return result, errors.New("required publication metadata did not round-trip")
	}
	lyrics, optionalMismatch := inspectOptionalLyrics(observed.Tags, expectedLyrics)
	if optionalMismatch {
		restorer, ok := stage.(publicationOptionalLyricsRestorer)
		if !ok {
			return result, errors.New("publication stage cannot safely restore optional lyrics write")
		}
		if err := checkOptionalLyricsStage(ctx, scope, stage); err != nil {
			return result, err
		}
		if err := restorer.rebuildOutputCopy(ctx, scope, borrow); err != nil {
			return result, fmt.Errorf("restore publication output after optional lyrics mismatch: %w", err)
		}
		if err := checkOptionalLyricsStage(ctx, scope, stage); err != nil {
			return result, err
		}
		// Reapply the complete mandatory metadata map after restoring the clean
		// immutable-input copy. Do not infer cover preservation from this step.
		if _, err := writer.Write(ctx, paths.OutputPath, mandatory, nil); err != nil {
			scope.quarantineUncertain()
			return result, fmt.Errorf("restore required publication metadata after optional lyrics mismatch: %w", err)
		}
		// Restore format-specific required assets only after the mandatory metadata
		// write; native FLAC artwork is preserved and audited separately.
		if err := requirements.restoreRequiredAssets(ctx, paths.OutputPath, mandatory); err != nil {
			scope.quarantineUncertain()
			return result, fmt.Errorf("restore required publication assets after optional lyrics mismatch: %w", err)
		}
		clean, err := reader.Read(ctx, paths.OutputPath, metadata.ReadRequest{})
		if err != nil {
			return result, fmt.Errorf("inspect rebuilt publication metadata: %w", err)
		}
		if !equalTags(withoutLyrics(clean.Tags), mandatory) {
			return result, errors.New("required publication metadata did not survive clean output rebuild")
		}
		if _, present := clean.Tags["LYRICS"]; present {
			return result, errors.New("LYRICS metadata remained after clean output rebuild")
		}
		if err := requirements.validateAudioAndAssets(ctx, paths.OutputPath); err != nil {
			return result, fmt.Errorf("validate rebuilt publication audio and assets after required metadata write: %w", err)
		}
		result.Tags = clean.Tags
		result.Lyrics = persistence.PublicationLyricsOutcome{State: persistence.PublicationAssetAbsent}
		result.OptionalDiagnostic = "optional lyrics did not round-trip; rebuilt output without lyrics"
		if requirements.recordOutcome != nil {
			if err := requirements.recordOutcome(ctx, publicationOptionalLyricsOutcomeEvidence{Kind: "rebuilt_without_lyrics", Tags: result.Tags, Lyrics: result.Lyrics, Diagnostic: result.OptionalDiagnostic}); err != nil {
				return result, fmt.Errorf("record rebuilt optional lyrics outcome: %w", err)
			}
		}
		return result, nil
	}
	result.Tags = observed.Tags
	result.Lyrics = lyrics
	if requirements.recordOutcome != nil {
		if err := requirements.recordOutcome(ctx, publicationOptionalLyricsOutcomeEvidence{Kind: "exact_roundtrip", Tags: result.Tags, Lyrics: result.Lyrics}); err != nil {
			return result, fmt.Errorf("record optional lyrics outcome: %w", err)
		}
	}
	return result, nil
}

func validateOptionalLyricsRequest(
	ctx context.Context,
	scope *PublicationExecutionScope,
	reservation *publicationSyncReservation,
	stage PreparedPublicationStage,
	input persistence.PublicationInputs,
	writer PublicationMetadataWriter,
	reader interface {
		Read(context.Context, string, metadata.ReadRequest) (metadata.Capture, error)
	},
	requirements publicationOptionalLyricsRequirements,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if scope == nil || reservation == nil || stage == nil || writer == nil || reader == nil || requirements.validateAudioAndAssets == nil || requirements.restoreRequiredAssets == nil {
		return errors.New("optional lyrics publication dependencies are unavailable")
	}
	if input.Version != persistence.PublicationInputsVersion3 || input.ManualLyrics == nil {
		return errors.New("optional lyrics helper requires frozen V3 manual lyrics input")
	}
	scope.mu.Lock()
	reservationValid := reservation.owner == scope && reservation.active && !reservation.borrowed && scope.syncWork == 1 && !scope.finalized && !scope.finalizing && !scope.unknown
	scope.mu.Unlock()
	if !reservationValid {
		return errors.New("optional lyrics helper requires the active synchronous reservation")
	}
	return nil
}

func checkOptionalLyricsStage(ctx context.Context, scope *PublicationExecutionScope, stage PreparedPublicationStage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if scope == nil || !scope.CanExecute() {
		return errors.New("publication execution scope is no longer active")
	}
	valid, err := scope.repository.CheckPublicationDelivery(ctx, scope.guard)
	if err != nil {
		return fmt.Errorf("check publication delivery before optional lyrics work: %w", err)
	}
	if !valid {
		return errors.New("publication delivery guard is no longer live")
	}
	return stage.Validate(ctx)
}

func withoutLyrics(tags map[string][]string) map[string][]string {
	result := make(map[string][]string, len(tags))
	for key, values := range tags {
		if strings.EqualFold(key, "LYRICS") {
			continue
		}
		result[key] = append([]string(nil), values...)
	}
	return result
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

func inspectOptionalLyrics(tags map[string][]string, expected string) (persistence.PublicationLyricsOutcome, bool) {
	values, present := tags["LYRICS"]
	if expected == "" {
		return persistence.PublicationLyricsOutcome{State: persistence.PublicationAssetAbsent}, present
	}
	if !present || len(values) != 1 || values[0] != expected {
		return persistence.PublicationLyricsOutcome{}, true
	}
	return persistence.PublicationLyricsOutcome{State: persistence.PublicationAssetEmbedded, SHA256: publicationLyricsDigest(expected), Format: "lrc"}, false
}

func cloneTagMap(tags map[string][]string) map[string][]string {
	result := make(map[string][]string, len(tags))
	for key, values := range tags {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func publicationLyricsDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
