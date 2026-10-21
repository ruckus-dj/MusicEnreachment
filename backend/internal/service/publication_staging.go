package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// PublicationStagePaths are capabilities supplied by an anchored preparer.
// The runner never derives a filesystem destination from frozen input.
type PublicationStagePaths struct {
	InputPath    string
	OutputPath   string
	RelativePath string
}

// PreparedPublicationStage owns the input and private output attempt directory.
// Validate rechecks anchored ownership; Close waits for outstanding loans.
type PreparedPublicationStage interface {
	Paths() PublicationStagePaths
	Validate(context.Context) error
	Close() error
}

// PublicationStageValidator must probe the completed file and return facts
// observed from that file, including stream topology and normalized metadata.
type PublicationStageValidator interface {
	Validate(context.Context, string, persistence.PublicationInputs) (PublicationStageObservation, error)
}

type supervisedPublicationStageValidator interface {
	validateSupervised(*PublicationExecutionScope) error
}

type PublicationMetadataWriter interface {
	Write(context.Context, string, map[string][]string, *metadata.Cover) (metadata.WriteResult, error)
}

// PublicationPinnedTools resolves only the executables admitted in the frozen
// contract; implementations must not consult mutable active-tool settings.
type PublicationPinnedTools interface {
	Executable(context.Context, []persistence.SourceAnalysisToolSelection, string) (string, error)
}

type PublicationStageObservation struct {
	Ownership            persistence.ArtifactOwnership
	Audio                json.RawMessage
	Metadata             json.RawMessage
	SHA256               string
	Assets               json.RawMessage
	QualityPolicyVersion int
}

type publicationStageRepository interface {
	CheckPublicationDelivery(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
	AdvancePublicationStage(context.Context, persistence.PublicationDeliveryGuard, string, persistence.PublicationStageObservation) (bool, error)
	MarkPublicationQuiescent(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
}

// PublicationStagingRunner performs preparation only. It deliberately does
// not settle an operation or expose/replace the current publication.
type PublicationStagingRunner struct {
	Repository  publicationStageRepository
	PinnedTools PublicationPinnedTools
	Metadata    PublicationMetadataWriter
	Validator   PublicationStageValidator
	Lyrics      *PublicationLyricsResolver
}

// Run executes only under a newly acquired scope bound to the same delivery.
func (runner PublicationStagingRunner) Run(ctx context.Context, scope *PublicationExecutionScope, input persistence.PublicationInputs, stage PreparedPublicationStage) (result PublicationStageObservation, resultErr error) {
	return runner.runWithValidatedStage(ctx, scope, input, stage, nil)
}

// runWithValidatedStage continues only after the durable validated transition
// has been acknowledged. The callback receives detached evidence and runs
// before deferred scope finalization.
func (runner PublicationStagingRunner) runWithValidatedStage(ctx context.Context, scope *PublicationExecutionScope, input persistence.PublicationInputs, stage PreparedPublicationStage, continuation func(context.Context, persistence.PublicationStageObservation, PreparedPublicationStage, *publicationSyncReservation) error) (result PublicationStageObservation, resultErr error) {
	if !input.SupportsExecution() {
		return result, fmt.Errorf("publication inputs version %d does not support execution", input.Version)
	}
	if runner.Repository == nil || stage == nil {
		return result, errors.New("publication staging dependencies are unavailable")
	}
	if scope == nil || scope.repository == nil {
		return result, errors.New("publication execution scope is unavailable")
	}
	reservation := scope.reserveSynchronousWork()
	if reservation == nil {
		return result, errors.New("publication execution scope is unavailable")
	}
	guard := scope.guard
	defer func() {
		// Release the reservation before Finalize; first-absent continuation
		// transfers it to its settlement fence instead.
		scope.releaseSynchronousWork(reservation)
		if err := scope.Finalize(ctx, stage.Close); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	if runner.PinnedTools == nil || runner.Validator == nil {
		return result, errors.New("publication staging dependencies are unavailable")
	}
	if bound, ok := runner.PinnedTools.(*GuardedPublicationPinnedTools); ok && (bound.scope != scope || bound.guard != guard) {
		return result, errors.New("publication pinned tools are not bound to the execution scope")
	}
	switch validator := runner.Validator.(type) {
	case *RealPublicationStageValidator:
		if validator == nil {
			return result, errors.New("supervised publication validator is unavailable")
		}
		if validator.Probe != nil {
			return result, errors.New("supervised publication validator does not accept injected probes")
		}
		pinned, ok := runner.PinnedTools.(*GuardedPublicationPinnedTools)
		if !ok {
			return result, errors.New("supervised publication validator requires scope-bound pinned tools")
		}
		probe, err := NewScopeBoundPublicationStageProbe(scope, pinned, input.Tools)
		if err != nil {
			return result, err
		}
		validator.Tools = pinned
		validator.supervisedProbe = probe
	case RealPublicationStageValidator:
		if validator.Probe != nil {
			return result, errors.New("supervised publication validator does not accept injected probes")
		}
		pinned, ok := runner.PinnedTools.(*GuardedPublicationPinnedTools)
		if !ok {
			return result, errors.New("supervised publication validator requires scope-bound pinned tools")
		}
		probe, err := NewScopeBoundPublicationStageProbe(scope, pinned, input.Tools)
		if err != nil {
			return result, err
		}
		validator.Tools = pinned
		validator.supervisedProbe = probe
		runner.Validator = validator
	}
	if validator, ok := runner.Validator.(supervisedPublicationStageValidator); ok {
		if err := validator.validateSupervised(scope); err != nil {
			return result, err
		}
	}
	if err := input.Validate(); err != nil {
		return result, fmt.Errorf("validate frozen publication inputs: %w", err)
	}
	var sourceFacts persistence.PersistedAudioFacts
	if input.Audio != nil {
		sourceFacts = input.Audio.AudioFacts
	} else if input.Rewrite != nil {
		sourceFacts = input.Rewrite.AudioFacts
	} else {
		return result, errors.New("publication audio facts are unavailable")
	}
	paths := stage.Paths()
	if !filepath.IsAbs(paths.InputPath) || !filepath.IsAbs(paths.OutputPath) || paths.InputPath == paths.OutputPath || paths.RelativePath == "" {
		return result, errors.New("anchored publication stage paths are incomplete")
	}
	check := func() error {
		valid, err := runner.Repository.CheckPublicationDelivery(ctx, guard)
		if err != nil {
			return err
		}
		if !valid {
			return errors.New("publication delivery guard is no longer live")
		}
		return stage.Validate(ctx)
	}
	if err := check(); err != nil {
		return result, fmt.Errorf("validate publication stage before execution: %w", err)
	}
	format := input.Format
	if input.Rewrite != nil {
		// Rewrites are metadata-only: preserve the verified current container and
		// copy its selected stream, regardless of the publication format setting.
		format = publication.AudioFormatSource
	}
	plan, err := publication.PlanAudioTransform(format, sourceFacts.AudioSourceFacts())
	if err != nil {
		return result, fmt.Errorf("plan publication audio transform: %w", err)
	}
	args, err := plan.FFmpegArgs(paths.InputPath, paths.OutputPath, sourceFacts.StreamIndex, sourceFacts.Container)
	if err != nil {
		return result, err
	}
	if err := check(); err != nil {
		return result, fmt.Errorf("validate publication stage before ffmpeg: %w", err)
	}
	ffmpeg, err := runner.PinnedTools.Executable(ctx, input.Tools, "ffmpeg")
	if err != nil || !filepath.IsAbs(ffmpeg) {
		if err == nil {
			err = errors.New("pinned ffmpeg path is not absolute")
		}
		return result, fmt.Errorf("resolve pinned publication ffmpeg: %w", err)
	}
	if input.IntentKind == "audio" {
		observed, err := runner.runInitialLyricsStage(ctx, scope, reservation, input, stage, func(ctx context.Context) error {
			if err := check(); err != nil {
				return err
			}
			_, err := scope.Run(ctx, ffmpeg, args...)
			return err
		}, check)
		if err != nil {
			return result, err
		}
		for _, phase := range []string{"preparing", "staged"} {
			if err := check(); err != nil {
				return result, err
			}
			if changed, err := runner.Repository.AdvancePublicationStage(ctx, guard, phase, observed.Clone()); err != nil || !changed {
				return result, errors.Join(errors.New("initial publication stage transition was not accepted"), err)
			}
		}
		result = PublicationStageObservation{Ownership: observed.Ownership, Audio: observed.ActualAudio, Metadata: observed.ActualMetadata,
			SHA256: observed.PayloadSHA256, Assets: observed.ActualAssets, QualityPolicyVersion: observed.QualityPolicyVersion}
		if continuation != nil {
			if err := continuation(ctx, observed.Clone(), stage, reservation); err != nil {
				return result, err
			}
		}
		return result, nil
	}
	if _, err := scope.Run(ctx, ffmpeg, args...); err != nil {
		return result, fmt.Errorf("prepare publication audio: %w", err)
	}
	if err := check(); err != nil {
		return result, fmt.Errorf("validate publication stage before metadata write: %w", err)
	}
	metadataWriter := runner.Metadata
	if metadataWriter == nil {
		metadataWriter = metadata.Writer{}
	}
	if _, err := metadataWriter.Write(ctx, paths.OutputPath, input.Clone().DesiredTags, nil); err != nil {
		return result, fmt.Errorf("write publication metadata: %w", err)
	}
	if err := check(); err != nil {
		return result, fmt.Errorf("validate publication stage before staged transition: %w", err)
	}
	if _, err := os.Stat(paths.OutputPath); err != nil {
		return result, fmt.Errorf("stat prepared publication output: %w", err)
	}
	staged := persistence.PublicationStageObservation{Version: 1, RelativePath: paths.RelativePath,
		ActualAudio: json.RawMessage(`{"pending_validation":true}`), ActualMetadata: json.RawMessage(`{"pending_validation":true}`)}
	if changed, err := runner.Repository.AdvancePublicationStage(ctx, guard, "preparing", staged); err != nil || !changed {
		if err != nil {
			return result, fmt.Errorf("record staged publication: %w", err)
		}
		return result, errors.New("publication stage transition was not accepted")
	}
	if err := check(); err != nil {
		return result, fmt.Errorf("validate publication stage before post-write probe: %w", err)
	}
	result, err = runner.Validator.Validate(ctx, paths.OutputPath, input)
	if err != nil {
		return result, fmt.Errorf("validate prepared publication output: %w", err)
	}
	if len(result.Audio) == 0 || len(result.Metadata) == 0 {
		return result, errors.New("publication validator returned incomplete actual observations")
	}
	validated := persistence.PublicationStageObservation{Version: 1, RelativePath: paths.RelativePath,
		Ownership: result.Ownership, PayloadSHA256: result.SHA256, ActualAudio: result.Audio, ActualMetadata: result.Metadata,
		ActualAssets: append(json.RawMessage(nil), result.Assets...), QualityPolicyVersion: result.QualityPolicyVersion}
	if err := check(); err != nil {
		return result, fmt.Errorf("validate publication stage before validated transition: %w", err)
	}
	if changed, err := runner.Repository.AdvancePublicationStage(ctx, guard, "staged", validated); err != nil || !changed {
		if err != nil {
			return result, fmt.Errorf("record validated publication stage: %w", err)
		}
		return result, errors.New("publication validation transition was not accepted")
	}
	if continuation != nil {
		if err := continuation(ctx, validated.Clone(), stage, reservation); err != nil {
			return result, err
		}
	}
	return result, nil
}
