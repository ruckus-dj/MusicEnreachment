package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type publicationInitialLyricsJournal interface {
	GuardedRecordPublicationInitialLyricsAcquisition(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationInitialLyricsAcquisitionRecord) (persistence.PublicationOptionalLyricsOutcome, error)
	GuardedRecordPublicationInitialLyricsRebuild(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationCandidateRebuildRecord) (persistence.PublicationOptionalLyricsOutcome, error)
	GuardedRecordPublicationInitialLyricsOutcome(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationInitialLyricsOutcomeRecord) (persistence.PublicationOptionalLyricsOutcome, error)
}

// publicationInitialLyricsCallbacks are deliberately supplied by the eventual
// guarded executor. In particular, OwnedRebuild must be implemented by the
// prepared-stage native ownership boundary; this orchestration layer has no
// direct filesystem mutation capability.
type publicationInitialLyricsCallbacks struct {
	CapturePreparedInput func(context.Context) (metadata.Capture, persistence.PublicationPreparationRecord, persistence.PublicationFileIdentity, error)
	FrozenTransform      func(context.Context) error
	WriteOptional        func(context.Context, map[string][]string) (metadata.OptionalLyricsWriteResult, error)
	WriteMandatory       func(context.Context, map[string][]string) (metadata.WriteResult, error)
	// PrepareOwnedRebuild must observe the current candidate after the optional
	// write and build the intent from that fresh native identity.
	PrepareOwnedRebuild func(context.Context) (persistence.PublicationCandidateRebuildRecord, error)
	// ArmOwnedRebuildAfterAcknowledgedIntent performs the single durable intent
	// ACK and returns the one-use native reset capability.
	ArmOwnedRebuildAfterAcknowledgedIntent func(context.Context, persistence.PublicationCandidateRebuildRecord) (func(context.Context) error, error)
	// CaptureRebuildComplete must sync and identify the candidate after the
	// transform and strict mandatory-only write, then return its completion record.
	CaptureRebuildComplete   func(context.Context) (persistence.PublicationCandidateRebuildRecord, error)
	ValidateFinal            func(context.Context, map[string][]string) (persistence.PublicationStageObservation, persistence.PublicationFileIdentity, error)
	CheckBoundary            func(context.Context) error
	Quarantine               func()
	ValidationFailureSettled func() bool
}

type publicationInitialLyricsResult struct {
	Acquisition persistence.PublicationInitialLyricsAcquisitionRecord
	Outcome     persistence.PublicationInitialLyricsOutcomeRecord
}

// executeInitialPublicationLyrics composes the guarded initial-lyrics protocol.
// ACK results other than newly recorded are never treated as permission to
// repeat an operation. All errors after acquisition are opaque and fatal.
func executeInitialPublicationLyrics(
	ctx context.Context,
	guard persistence.PublicationDeliveryGuard,
	inputs persistence.PublicationInputs,
	resolver *PublicationLyricsResolver,
	lookupMetadata lrclib.Metadata,
	journal publicationInitialLyricsJournal,
	callbacks publicationInitialLyricsCallbacks,
) (result publicationInitialLyricsResult, resultErr error) {
	if journal == nil || callbacks.CapturePreparedInput == nil || callbacks.FrozenTransform == nil || callbacks.ValidateFinal == nil || callbacks.CheckBoundary == nil || callbacks.Quarantine == nil {
		return result, errors.New("initial publication lyrics dependencies are unavailable")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			callbacks.Quarantine()
			panic(recovered)
		}
		if resultErr != nil && (callbacks.ValidationFailureSettled == nil || !callbacks.ValidationFailureSettled()) {
			callbacks.Quarantine()
		}
	}()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if inputs.IntentKind != "audio" || inputs.InitialLyrics == nil {
		return result, errors.New("initial publication lyrics require frozen audio inputs")
	}
	inputs = inputs.Clone()
	if err := callbacks.CheckBoundary(ctx); err != nil {
		return result, fmt.Errorf("check initial publication guard before input capture: %w", err)
	}
	capture, preparation, inputIdentity, err := callbacks.CapturePreparedInput(ctx)
	if err != nil {
		return result, fmt.Errorf("capture prepared initial publication input: %w", err)
	}
	result.Acquisition, err = resolveInitialPublicationLyrics(ctx, resolver, *inputs.InitialLyrics, capture.Tags, lookupMetadata)
	if err != nil {
		return result, err
	}
	if callbacks.WriteMandatory == nil {
		return result, errors.New("initial publication mandatory metadata writer is unavailable")
	}
	if result.Acquisition.Source != "none" && (callbacks.WriteOptional == nil || callbacks.PrepareOwnedRebuild == nil || callbacks.ArmOwnedRebuildAfterAcknowledgedIntent == nil || callbacks.CaptureRebuildComplete == nil) {
		return result, errors.New("initial publication optional lyrics mutation capabilities are unavailable")
	}
	result.Acquisition.Guard = guard
	if err := callbacks.CheckBoundary(ctx); err != nil {
		return result, fmt.Errorf("check initial publication guard before acquisition ACK: %w", err)
	}
	ack, err := journal.GuardedRecordPublicationInitialLyricsAcquisition(ctx, guard, cloneInitialLyricsAcquisitionRecord(result.Acquisition))
	if err != nil {
		return result, fmt.Errorf("acknowledge initial publication lyrics acquisition: %w", err)
	}
	if ack != persistence.PublicationOptionalLyricsRecorded {
		return result, errors.New("initial publication lyrics acquisition was not newly acknowledged")
	}

	if err := callbacks.CheckBoundary(ctx); err != nil {
		return result, fmt.Errorf("check initial publication guard before transform: %w", err)
	}
	if err := callbacks.FrozenTransform(ctx); err != nil {
		return result, fmt.Errorf("run frozen initial publication transform: %w", err)
	}
	lyricsRequested := result.Acquisition.Source != "none"
	if lyricsRequested {
		if callbacks.WriteOptional == nil {
			return result, errors.New("initial publication optional lyrics writer is unavailable")
		}
		requestedTags := cloneTagMap(withoutLyrics(inputs.DesiredTags))
		requestedTags["LYRICS"] = []string{result.Acquisition.Lyrics}
		if err := callbacks.CheckBoundary(ctx); err != nil {
			return result, fmt.Errorf("check initial publication guard before optional write: %w", err)
		}
		writeResult, writeErr := callbacks.WriteOptional(ctx, requestedTags)
		if writeErr != nil {
			return result, fmt.Errorf("write initial publication optional lyrics: %w", writeErr)
		}
		switch writeResult.Status {
		case metadata.OptionalLyricsVerified:
			result.Outcome.Outcome = persistence.PublicationOptionalLyricsExactRoundTrip
		case metadata.OptionalLyricsAbsent, metadata.OptionalLyricsMismatch:
			if callbacks.ArmOwnedRebuildAfterAcknowledgedIntent == nil {
				return result, errors.New("initial publication acknowledged rebuild capability is unavailable")
			}
			if err := callbacks.CheckBoundary(ctx); err != nil {
				return result, fmt.Errorf("check initial publication guard before rebuild intent: %w", err)
			}
			intent, prepareErr := callbacks.PrepareOwnedRebuild(ctx)
			if prepareErr != nil {
				return result, fmt.Errorf("prepare initial publication candidate rebuild: %w", prepareErr)
			}
			intent.Step = "intent"
			resetOwnedCandidate, armErr := callbacks.ArmOwnedRebuildAfterAcknowledgedIntent(ctx, intent)
			if armErr != nil {
				return result, fmt.Errorf("acknowledge and arm initial publication candidate rebuild: %w", armErr)
			}
			if resetOwnedCandidate == nil {
				return result, errors.New("initial publication rebuild ACK did not provide its native reset capability")
			}
			if err := callbacks.CheckBoundary(ctx); err != nil {
				return result, fmt.Errorf("check initial publication guard before candidate reset: %w", err)
			}
			if err := resetOwnedCandidate(ctx); err != nil {
				return result, fmt.Errorf("rebuild owned initial publication candidate: %w", err)
			}
			if err := callbacks.CheckBoundary(ctx); err != nil {
				return result, fmt.Errorf("check initial publication guard before fallback transform: %w", err)
			}
			if err := callbacks.FrozenTransform(ctx); err != nil {
				return result, fmt.Errorf("rerun frozen initial publication transform after rebuild: %w", err)
			}
			mandatoryTags := cloneTagMap(withoutLyrics(inputs.DesiredTags))
			if err := callbacks.CheckBoundary(ctx); err != nil {
				return result, fmt.Errorf("check initial publication guard before fallback mandatory write: %w", err)
			}
			if _, err := callbacks.WriteMandatory(ctx, mandatoryTags); err != nil {
				return result, fmt.Errorf("write mandatory metadata after initial lyrics rebuild: %w", err)
			}
			// The callback must sync the final candidate and collect its identity only
			// after the transform and strict mandatory metadata write have completed.
			if err := callbacks.CheckBoundary(ctx); err != nil {
				return result, fmt.Errorf("check initial publication guard before rebuild completion capture: %w", err)
			}
			complete, rebuildErr := callbacks.CaptureRebuildComplete(ctx)
			if rebuildErr != nil {
				return result, fmt.Errorf("capture completed initial publication candidate rebuild: %w", rebuildErr)
			}
			complete.Step = "complete"
			if err := callbacks.CheckBoundary(ctx); err != nil {
				return result, fmt.Errorf("check initial publication guard before rebuild completion ACK: %w", err)
			}
			ack, err = journal.GuardedRecordPublicationInitialLyricsRebuild(ctx, guard, complete)
			if err != nil {
				return result, fmt.Errorf("acknowledge initial publication candidate rebuild completion: %w", err)
			}
			if ack != persistence.PublicationOptionalLyricsRecorded {
				return result, errors.New("initial publication candidate rebuild completion was not newly acknowledged")
			}
			result.Outcome.Outcome = persistence.PublicationOptionalLyricsRebuiltWithoutLyrics
			result.Outcome.WarningCode = "readback_mismatch"
		default:
			return result, errors.New("initial publication optional lyrics readback was not classified")
		}
	} else {
		result.Outcome.Outcome = persistence.PublicationInitialLyricsNotRequested
		mandatoryTags := cloneTagMap(withoutLyrics(inputs.DesiredTags))
		if err := callbacks.CheckBoundary(ctx); err != nil {
			return result, fmt.Errorf("check initial publication guard before mandatory write: %w", err)
		}
		if _, err := callbacks.WriteMandatory(ctx, mandatoryTags); err != nil {
			return result, fmt.Errorf("write mandatory metadata for initial publication: %w", err)
		}
	}

	expectedTags := cloneTagMap(withoutLyrics(inputs.DesiredTags))
	if result.Outcome.Outcome == persistence.PublicationOptionalLyricsExactRoundTrip {
		expectedTags["LYRICS"] = []string{result.Acquisition.Lyrics}
	}
	if err := callbacks.CheckBoundary(ctx); err != nil {
		return result, fmt.Errorf("check initial publication guard before final validation: %w", err)
	}
	observation, finalIdentity, err := callbacks.ValidateFinal(ctx, cloneTagMap(expectedTags))
	if err != nil {
		return result, fmt.Errorf("independently validate initial publication output: %w", err)
	}
	result.Outcome.Version = persistence.PublicationInitialLyricsVersion
	result.Outcome.Kind = persistence.PublicationInitialLyricsOutcomeKind
	result.Outcome.Guard = guard
	result.Outcome.Preparation = preparation
	result.Outcome.InputIdentity = inputIdentity
	result.Outcome.OutputIdentity = finalIdentity
	result.Outcome.FinalObservation = observation
	if err := callbacks.CheckBoundary(ctx); err != nil {
		return result, fmt.Errorf("check initial publication guard before outcome ACK: %w", err)
	}
	ack, err = journal.GuardedRecordPublicationInitialLyricsOutcome(ctx, guard, result.Outcome)
	if err != nil {
		return result, fmt.Errorf("acknowledge initial publication lyrics outcome: %w", err)
	}
	if ack != persistence.PublicationOptionalLyricsRecorded {
		return result, errors.New("initial publication lyrics outcome was not newly acknowledged")
	}
	return result, nil
}

func cloneInitialLyricsAcquisitionRecord(record persistence.PublicationInitialLyricsAcquisitionRecord) persistence.PublicationInitialLyricsAcquisitionRecord {
	inputs := persistence.PublicationInputs{InitialLyrics: &record.Authority}.Clone()
	record.Authority = *inputs.InitialLyrics
	return record
}

// resolveInitialPublicationLyrics classifies the frozen local authority before
// consulting automatic sources. The supplied source tags and lookup metadata
// must have been read from the held prepared input; this function never applies
// a local manual value to the output.
func resolveInitialPublicationLyrics(
	ctx context.Context,
	resolver *PublicationLyricsResolver,
	authority persistence.PublicationInitialLyricsInput,
	preparedSourceTags map[string][]string,
	lookupMetadata lrclib.Metadata,
) (persistence.PublicationInitialLyricsAcquisitionRecord, error) {
	record := persistence.PublicationInitialLyricsAcquisitionRecord{
		Version:   persistence.PublicationInitialLyricsVersion,
		Kind:      persistence.PublicationInitialLyricsAcquisitionKind,
		Authority: authority,
		Source:    "none",
	}

	field := authority.Desired
	if field != nil && field.State != librarymetadata.StateAbsent && (field.Authority == librarymetadata.AuthorityManual || field.State == librarymetadata.StateExplicitEmpty) {
		return record, nil
	}

	resolution, err := resolver.Resolve(ctx, PublicationLyricsResolutionRequest{
		SelectedSourceTags: cloneTagMap(preparedSourceTags),
		Metadata:           lookupMetadata,
	})
	if err != nil {
		return persistence.PublicationInitialLyricsAcquisitionRecord{}, err
	}
	record.Source = resolution.Source
	record.Lyrics = resolution.Lyrics
	record.SHA256 = resolution.SHA256
	record.WarningCode = resolution.WarningCode
	return record, nil
}
