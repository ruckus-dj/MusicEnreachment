package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// PublicationExecutor owns the entry point for one River publication delivery.
type PublicationExecutor struct {
	repository     publicationExecutorRepository
	registry       *settings.Registry
	platform       tools.Platform
	coordinator    *publication.Coordinator
	bootUUID       uuid.UUID
	commands       PublicationCommandRunner
	lyrics         *PublicationLyricsResolver
	stageValidator PublicationStageValidator
	metadata       PublicationMetadataWriter
}

type publicationExecutorRepository interface {
	publicationPinnedToolsRepository
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	ReadPublicationAttempt(context.Context, uuid.UUID) (*persistence.TrackPublicationAttempt, error)
	ClaimPublicationDelivery(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error)
	MarkPublicationDeliveryReturned(context.Context, persistence.PublicationDeliveryGuard) error
	PublicationPreparationInputs(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error)
	PublicationManualLyricsPreparationInputs(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error)
	PublicationAttemptCoverInputForDelivery(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationAttemptCoverInput, bool, error)
	PublicationSourceHoldForDelivery(context.Context, persistence.PublicationDeliveryGuard) (uuid.UUID, bool, error)
	CheckPublicationSourceHoldForDelivery(context.Context, persistence.PublicationDeliveryGuard, uuid.UUID) (persistence.PublicationSourceHold, bool, error)
	ReadPublicationObsoleteOutputCandidate(context.Context, persistence.PublicationDeliveryGuard, string) (*persistence.PublicationObsoleteOutputCandidate, error)
	CheckPublicationResultCurrent(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
	CheckPublicationTargetReplacement(context.Context, persistence.PublicationDeliveryGuard, string) (bool, error)
	CommitPublicationResult(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationResult) (bool, error)
}

var _ publicationExecutorRepository = (*persistence.SetupManagerRepository)(nil)

// GlobalPublicationMutationResource serializes publication execution with
// process-local mutations to publication metadata and the output namespace.
const GlobalPublicationMutationResource = "publication-global-mutation"

func NewPublicationExecutor(
	repository *persistence.SetupManagerRepository,
	registry *settings.Registry,
	platform tools.Platform,
	coordinator *publication.Coordinator,
	bootUUID uuid.UUID,
	commands PublicationCommandRunner,
) *PublicationExecutor {
	var repositoryDependency publicationExecutorRepository
	if repository != nil {
		repositoryDependency = repository
	}
	return newPublicationExecutor(repositoryDependency, registry, platform, coordinator, bootUUID, commands)
}

func newPublicationExecutor(
	repository publicationExecutorRepository,
	registry *settings.Registry,
	platform tools.Platform,
	coordinator *publication.Coordinator,
	bootUUID uuid.UUID,
	commands PublicationCommandRunner,
) *PublicationExecutor {
	return &PublicationExecutor{
		repository: repository, registry: registry, platform: platform,
		coordinator: coordinator, bootUUID: bootUUID, commands: commands,
		lyrics: newPublicationLyricsResolver(registry),
	}
}

func newPublicationLyricsResolver(registry publicationLyricsSettings) *PublicationLyricsResolver {
	client, _ := lrclib.NewClient(&http.Client{Timeout: 10 * time.Second})
	return NewPublicationLyricsResolver(registry, client)
}

// NewDefaultPublicationLyricsResolver constructs the LRCLIB resolver shared by
// first-publication planning and publication execution.
func NewDefaultPublicationLyricsResolver(registry *settings.Registry) *PublicationLyricsResolver {
	return newPublicationLyricsResolver(registry)
}

// WithLyricsResolver supplies the resolver shared with preview planning.
func (executor *PublicationExecutor) WithLyricsResolver(resolver *PublicationLyricsResolver) *PublicationExecutor {
	executor.lyrics = resolver
	return executor
}

// ExecutePublicationDelivery reloads durable state using the operation ID in
// the River args. The actual River row identity is passed separately so an
// unrelated or stale job cannot claim the publication attempt.
func (executor *PublicationExecutor) ExecutePublicationDelivery(ctx context.Context, operationID uuid.UUID, riverJobID int64, riverAttempt int) (resultErr error) {
	if executor == nil || executor.repository == nil || executor.registry == nil || executor.coordinator == nil || executor.bootUUID == uuid.Nil || executor.commands == nil {
		return errors.New("publication executor dependencies are unavailable")
	}
	if operationID == uuid.Nil || riverJobID <= 0 || riverAttempt <= 0 {
		return errors.New("publication delivery identity is invalid")
	}
	releaseMutation, err := executor.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
	if err != nil {
		return err
	}
	commands := newSimplePublicationTrackingRunner(executor.commands)
	var releaseResources func()
	defer func() {
		if !commands.QuiescenceUnknown() {
			if releaseResources != nil {
				releaseResources()
			}
			releaseMutation()
		}
	}()
	operation, err := executor.repository.GetOperation(ctx, operationID)
	if err != nil {
		return fmt.Errorf("read publication operation: %w", err)
	}
	if operation == nil || operation.Kind != "publish_track_v1" || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID {
		return errors.New("publication River job does not match the running operation")
	}
	// River may redeliver after the database transaction committed but before
	// River recorded the successful return. The attempt is intentionally gone at
	// this point; the operation is the durable completion receipt.
	if operation.State == "succeeded" {
		return nil
	}
	if (operation.State != "queued" && operation.State != "running") || operation.Attempt <= 0 {
		return errors.New("publication River job does not match the running operation")
	}
	attempt, err := executor.repository.ReadPublicationAttempt(ctx, operationID)
	if err != nil {
		return fmt.Errorf("read publication attempt: %w", err)
	}
	if attempt == nil || attempt.Phase != "queued" && attempt.Phase != "preparing" {
		return errors.New("publication attempt is unavailable")
	}
	input, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
	if err != nil {
		return fmt.Errorf("decode frozen publication inputs: %w", err)
	}
	if input.ManualLyrics != nil {
		if attempt.IntentKind != "rewrite" || input.IntentKind != "rewrite" || input.Rewrite == nil || input.Format != publication.AudioFormatSource || !manualLyricsAudioSupported(input.Rewrite.AudioFacts) {
			return errors.New("manual lyrics publication requires a supported current-container rewrite")
		}
	}
	outputPath, configured, err := executor.registry.GetOutputDirectory(ctx)
	if err != nil || !configured || !filepath.IsAbs(outputPath) {
		return errors.Join(errors.New("publication output directory is unavailable"), err)
	}
	root, err := sourcefs.NewOutputOpener().OpenRoot(ctx, outputPath)
	if err != nil {
		return fmt.Errorf("open publication output directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	guard := persistence.PublicationDeliveryGuard{
		OperationID: operationID, ApplicationAttempt: operation.Attempt,
		RiverJobID: riverJobID, RiverAttempt: riverAttempt,
		ClaimUUID: uuid.New(), BootUUID: executor.bootUUID, DeliveryToken: uuid.NewString(),
		Epoch: attempt.Epoch, CandidateGeneration: attempt.CandidateGeneration, ExpectedPositionID: attempt.PositionID,
	}
	// One output-root lock deliberately serializes publication deliveries, including
	// shared directory creation. Finer-grained scheduling can be added separately.
	resources := []publication.ResourceLock{
		{Key: "publication-output:" + filepath.Clean(outputPath), Mode: publication.Exclusive},
		{Key: "publication-position:" + attempt.PositionID.String(), Mode: publication.Exclusive},
	}
	if input.Audio != nil {
		resources = append(resources, publication.ResourceLock{Key: "source-root:" + input.Audio.RootID.String(), Mode: publication.Shared})
	}
	release, err := executor.coordinator.Acquire(ctx, resources)
	if err != nil {
		return err
	}
	releaseResources = release
	claim, err := executor.repository.ClaimPublicationDelivery(ctx, guard)
	if err != nil || claim != persistence.PublicationDeliveryAcquired && claim != persistence.PublicationDeliveryInterrupted &&
		claim != persistence.PublicationDeliverySourceRepublishRecovery && claim != persistence.PublicationDeliveryReprepare {
		return errors.Join(err, errors.New("publication delivery claim was not acquired"))
	}
	defer func() {
		if commands.QuiescenceUnknown() {
			return
		}
		markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, executor.repository.MarkPublicationDeliveryReturned(markCtx, guard))
	}()
	defer func() {
		if recovered := recover(); recovered != nil {
			resultErr = fmt.Errorf("publication execution panicked: %v", recovered)
		}
	}()
	var authorizedObservedSourceRepublishTarget persistence.PublicationFileIdentity
	if claim == persistence.PublicationDeliverySourceRepublishRecovery {
		attempt, err = executor.repository.ReadPublicationAttempt(ctx, operationID)
		if err != nil || attempt == nil || attempt.ResumeMode != persistence.PublicationResumeModeInspectSourceRepublishTarget {
			return errors.Join(err, errors.New("source-republish recovery attempt is unavailable"))
		}
	}
	var guarded persistence.PublicationInputs
	var live bool
	if input.ManualLyrics != nil {
		guarded, live, err = executor.repository.PublicationManualLyricsPreparationInputs(ctx, guard)
	} else {
		guarded, live, err = executor.repository.PublicationPreparationInputs(ctx, guard)
	}
	if err != nil || !live {
		return errors.Join(errors.New("publication frozen inputs are unavailable for this delivery"), err)
	}
	input = guarded
	switch claim {
	case persistence.PublicationDeliverySourceRepublishRecovery:
		recovered, resume, err := executor.recoverInterruptedSourceRepublish(ctx, attempt, input, guard, outputPath, root, commands, &authorizedObservedSourceRepublishTarget)
		if err != nil {
			return err
		}
		if recovered {
			return nil
		}
		if !resume {
			return errors.New("source-republish recovery did not authorize preparation")
		}
		if authorizedObservedSourceRepublishTarget.SHA256 == "" {
			attempt.ResumeMode = persistence.PublicationResumeModePrepare
		}
	case persistence.PublicationDeliveryInterrupted:
		recovered, err := executor.recoverInterruptedTagRewrite(ctx, attempt, input, guard, outputPath, root, commands)
		if err != nil {
			return err
		}
		if recovered {
			return nil
		}
		recovered, err = executor.recoverInterruptedFirstAudio(ctx, attempt, input, guard, outputPath, root, commands)
		if err != nil {
			return err
		}
		if recovered {
			return nil
		}
	}
	allowSourceRepublishReprepare := claim == persistence.PublicationDeliveryReprepare || guard.ApplicationAttempt > 1
	return executor.executeSimplePublication(ctx, attempt, input, guard, outputPath, root, commands, allowSourceRepublishReprepare, authorizedObservedSourceRepublishTarget)
}
