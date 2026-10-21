package service

import (
	"context"
	"errors"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type publicationOptionalLyricsMetadataWriter interface {
	WriteWithOptionalLyrics(context.Context, string, map[string][]string, *metadata.Cover) (metadata.OptionalLyricsWriteResult, error)
}

// runInitialLyricsStage adapts the initial-lyrics protocol to the native
// prepared-stage ownership boundary. The transform is always the frozen
// publication transform supplied by the caller.
func (runner PublicationStagingRunner) runInitialLyricsStage(
	ctx context.Context,
	scope *PublicationExecutionScope,
	reservation *publicationSyncReservation,
	input persistence.PublicationInputs,
	prepared PreparedPublicationStage,
	transform func(context.Context) error,
	check func() error,
) (persistence.PublicationStageObservation, error) {
	stage, ok := prepared.(*preparedPublicationStage)
	if !ok || stage == nil || scope == nil || reservation == nil || transform == nil || check == nil {
		return persistence.PublicationStageObservation{}, errors.New("initial publication lyrics require a native prepared stage")
	}
	journal, ok := stage.repository.(publicationInitialLyricsJournal)
	if !ok {
		return persistence.PublicationStageObservation{}, errors.New("initial publication lyrics journal is unavailable")
	}
	paths := stage.Paths()
	if err := check(); err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	if err := stage.Validate(ctx); err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	inputIdentity, err := stage.verifyInitialLyricsPreparedAudioInput(ctx)
	if err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	inputCapture, err := (metadata.Reader{}).Read(ctx, paths.InputPath, metadata.ReadRequest{Properties: true})
	if err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	if err := check(); err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	preparation := stage.preparationRecord
	if preparation.CopiedInput != nil {
		copied := clonePublicationIdentity(*preparation.CopiedInput)
		preparation.CopiedInput = &copied
	}
	var rebuildIntent persistence.PublicationCandidateRebuildRecord
	var rebuildCapability *initialLyricsRebuildCapability

	callbacks := publicationInitialLyricsCallbacks{
		CheckBoundary: func(boundaryCtx context.Context) error {
			if err := boundaryCtx.Err(); err != nil {
				return err
			}
			if !scope.CanExecute() {
				scope.mu.Lock()
				ownedRebuild := rebuildCapability != nil && rebuildCapability.stage == stage && rebuildCapability.scope == scope &&
					rebuildCapability.reservation == reservation && rebuildCapability.active && rebuildCapability.fenced &&
					!reservationNotFenced(scope, reservation) && !scope.unknown && !scope.finalized && !scope.finalizing && scope.unresolved == 0
				scope.mu.Unlock()
				if !ownedRebuild {
					return errors.New("publication execution scope is not launchable")
				}
			}
			return check()
		},
		Quarantine: scope.quarantineUncertain,
		CapturePreparedInput: func(captureCtx context.Context) (metadata.Capture, persistence.PublicationPreparationRecord, persistence.PublicationFileIdentity, error) {
			if err := check(); err != nil {
				return metadata.Capture{}, persistence.PublicationPreparationRecord{}, persistence.PublicationFileIdentity{}, err
			}
			capture := inputCapture
			capture.Tags = cloneTagMap(inputCapture.Tags)
			return capture, preparation, inputIdentity, nil
		},
		FrozenTransform: func(transformCtx context.Context) error {
			if err := check(); err != nil {
				return err
			}
			if err := transform(transformCtx); err != nil {
				return err
			}
			return check()
		},
		WriteMandatory: func(writeCtx context.Context, tags map[string][]string) (metadata.WriteResult, error) {
			if err := check(); err != nil {
				return metadata.WriteResult{}, err
			}
			writer := runner.Metadata
			if writer == nil {
				writer = metadata.Writer{}
			}
			result, err := writer.Write(writeCtx, paths.OutputPath, tags, nil)
			if err != nil {
				return result, err
			}
			return result, check()
		},
		WriteOptional: func(writeCtx context.Context, tags map[string][]string) (metadata.OptionalLyricsWriteResult, error) {
			if err := check(); err != nil {
				return metadata.OptionalLyricsWriteResult{}, err
			}
			writer, ok := runner.Metadata.(publicationOptionalLyricsMetadataWriter)
			if !ok {
				writer = metadata.Writer{}
			}
			result, err := writer.WriteWithOptionalLyrics(writeCtx, paths.OutputPath, tags, nil)
			if err != nil {
				return result, err
			}
			return result, check()
		},
		PrepareOwnedRebuild: func(rebuildCtx context.Context) (persistence.PublicationCandidateRebuildRecord, error) {
			if err := check(); err != nil {
				return persistence.PublicationCandidateRebuildRecord{}, err
			}
			record, capability, err := stage.prepareInitialLyricsRebuild(rebuildCtx, scope, reservation)
			if err != nil {
				return persistence.PublicationCandidateRebuildRecord{}, err
			}
			rebuildIntent, rebuildCapability = record, capability
			return record, nil
		},
		ArmOwnedRebuildAfterAcknowledgedIntent: func(rebuildCtx context.Context, intent persistence.PublicationCandidateRebuildRecord) (func(context.Context) error, error) {
			if err := check(); err != nil {
				return nil, err
			}
			if rebuildCapability == nil || intent.Step != "intent" || intent.Guard != scope.guard {
				return nil, errors.New("initial publication rebuild capability is unavailable")
			}
			if err := stage.acknowledgeInitialLyricsRebuildIntent(rebuildCtx, rebuildCapability); err != nil {
				return nil, err
			}
			return func(resetCtx context.Context) error {
				return stage.resetInitialLyricsCandidate(resetCtx, scope, rebuildCapability)
			}, nil
		},
		CaptureRebuildComplete: func(rebuildCtx context.Context) (persistence.PublicationCandidateRebuildRecord, error) {
			if err := check(); err != nil {
				return persistence.PublicationCandidateRebuildRecord{}, err
			}
			return stage.finishInitialLyricsRebuild(rebuildCtx, scope, reservation, rebuildIntent)
		},
		ValidateFinal: func(validateCtx context.Context, desiredTags map[string][]string) (persistence.PublicationStageObservation, persistence.PublicationFileIdentity, error) {
			if err := check(); err != nil {
				return persistence.PublicationStageObservation{}, persistence.PublicationFileIdentity{}, err
			}
			finalInput := input.Clone()
			finalInput.DesiredTags = cloneTagMap(desiredTags)
			observed, err := runner.Validator.Validate(validateCtx, paths.OutputPath, finalInput)
			if err != nil {
				return persistence.PublicationStageObservation{}, persistence.PublicationFileIdentity{}, err
			}
			if err := check(); err != nil {
				return persistence.PublicationStageObservation{}, persistence.PublicationFileIdentity{}, err
			}
			stageObservation := persistence.PublicationStageObservation{
				Version: 1, RelativePath: paths.RelativePath, Ownership: observed.Ownership,
				PayloadSHA256: observed.SHA256, ActualAudio: observed.Audio,
				ActualMetadata: observed.Metadata, ActualAssets: observed.Assets,
				QualityPolicyVersion: observed.QualityPolicyVersion,
			}
			identity := persistence.PublicationFileIdentity{SizeBytes: observed.Ownership.ExpectedSize, MTime: observed.Ownership.ExpectedMTime, SHA256: observed.SHA256}
			return stageObservation, identity, nil
		},
	}

	result, err := executeInitialPublicationLyrics(ctx, scope.guard, input, runner.Lyrics, initialLyricsLookupMetadata(inputCapture), journal, callbacks)
	if err != nil {
		return persistence.PublicationStageObservation{}, err
	}
	return result.Outcome.FinalObservation.Clone(), nil
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
