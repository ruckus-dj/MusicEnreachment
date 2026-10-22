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
	repository     *persistence.SetupManagerRepository
	registry       *settings.Registry
	platform       tools.Platform
	coordinator    *publication.Coordinator
	bootUUID       uuid.UUID
	commands       PublicationCommandRunner
	lyrics         *PublicationLyricsResolver
	stageValidator PublicationStageValidator
	metadata       PublicationMetadataWriter
}

func NewPublicationExecutor(
	repository *persistence.SetupManagerRepository,
	registry *settings.Registry,
	platform tools.Platform,
	coordinator *publication.Coordinator,
	bootUUID uuid.UUID,
	commands PublicationCommandRunner,
) *PublicationExecutor {
	client, _ := lrclib.NewClient(&http.Client{Timeout: 10 * time.Second})
	return &PublicationExecutor{
		repository: repository, registry: registry, platform: platform,
		coordinator: coordinator, bootUUID: bootUUID, commands: commands,
		lyrics: NewPublicationLyricsResolver(registry, client),
	}
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
	operation, err := executor.repository.GetOperation(ctx, operationID)
	if err != nil {
		return fmt.Errorf("read publication operation: %w", err)
	}
	if operation == nil || operation.Kind != "publish_track_v1" || operation.State != "queued" || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID || operation.Attempt <= 0 {
		return errors.New("publication River job does not match the running operation")
	}
	attempt, err := executor.repository.ReadPublicationAttempt(ctx, operationID)
	if err != nil {
		return fmt.Errorf("read publication attempt: %w", err)
	}
	if attempt == nil || attempt.Phase != "queued" {
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
	scope, err := NewPublicationExecutionScope(ctx, executor.coordinator, resources, guard, executor.repository.ClaimPublicationDelivery, executor.commands, executor.repository)
	if err != nil {
		return err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			resultErr = fmt.Errorf("publication execution panicked: %v", recovered)
		}
		if resultErr != nil && !scope.failureSettled() {
			scope.quarantineUncertain()
			_, diagnosticErr := executor.repository.GuardedPublicationDeliveryDiagnostic(context.WithoutCancel(ctx), guard, "Publication could not finish; its retained state requires reconciliation.")
			resultErr = errors.Join(resultErr, diagnosticErr)
		}
	}()
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
	preparer := PublicationStagePreparer{Repository: executor.repository, SourceOpener: sourcefs.NewOpener(), OutputParent: root, ParentPath: outputPath}
	if input.ManualLyrics == nil {
		if input.Audio != nil {
			hold, found, holdErr := executor.repository.PublicationSourceHoldForDelivery(ctx, guard)
			if holdErr != nil || !found {
				return errors.Join(errors.New("publication source hold is unavailable"), holdErr)
			}
			preparer.HoldUUID = hold
		}
		_, err := executeGenericPublication(ctx, scope, executor.repository, input, preparer, attempt, executor.platform, executor.lyrics, executor.stageValidator, executor.metadata)
		return err
	}
	prepared, err := preparer.prepareManualLyricsFLAC(ctx, guard, persistence.PublicationDeliveryAcquired, true)
	if err != nil {
		return err
	}
	stage, ok := prepared.(*preparedPublicationStage)
	if !ok {
		return errors.New("publication preparation did not return a native stage")
	}
	pinnedTools, err := NewScopeBoundPublicationPinnedTools(executor.repository, scope, guard, executor.platform)
	if err != nil {
		return err
	}
	if input.Rewrite.AudioFacts.Container == publication.AudioContainerMKA {
		stage.manualProbe, err = NewScopeBoundPublicationStageProbe(scope, pinnedTools, input.Tools)
		if err != nil {
			return err
		}
	}
	reservation := scope.reserveSynchronousWork()
	if reservation == nil {
		return errors.New("publication synchronous work reservation is unavailable")
	}
	proof, err := stage.initialOutputCopy(ctx, scope, reservation)
	if err != nil {
		return err
	}
	_, err = replacePreparedManualLyricsFLAC(ctx, scope, reservation, proof, input)
	return err
}
