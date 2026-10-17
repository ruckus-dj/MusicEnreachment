package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type firstAbsentPublicationRepository interface {
	absentTargetInstallationRepository
	CommitFirstPublication(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
	ReleasePublicationInputHolds(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
}

// RunFirstAbsentPublication stages, exposes, and commits one first publication.
// It remains an internal service slice; it does not settle the operation or
// remove private publication artifacts.
func (runner PublicationStagingRunner) RunFirstAbsentPublication(
	ctx context.Context,
	scope *PublicationExecutionScope,
	input persistence.PublicationInputs,
	stage PreparedPublicationStage,
	repository firstAbsentPublicationRepository,
	intent persistence.PublicationExposureIntent,
	root, stageDir, targetParent sourcefs.OutputDirectory,
	stageName, targetName string,
) (result PublicationStageObservation, resultErr error) {
	if scope == nil || repository == nil {
		return result, errors.New("first-publication dependencies are unavailable")
	}
	guard := scope.guard
	pinned, ok := runner.PinnedTools.(*GuardedPublicationPinnedTools)
	if !ok || pinned == nil || pinned.scope != scope || pinned.resolver == nil || pinned.resolver.scope != scope || pinned.guard != guard || !scope.CanExecute() {
		return result, errors.New("publication pinned tools are not bound to the execution scope")
	}
	if intent.Expectation != "absent" {
		return result, errors.New("first publication requires an absent target")
	}
	result, resultErr = runner.runWithValidatedStage(ctx, scope, input, stage, func(ctx context.Context, validated persistence.PublicationStageObservation, stage PreparedPublicationStage) error {
		installer, err := newAbsentTargetInstaller(scope, repository, guard, intent, validated.Clone(), stage, root, stageDir, targetParent, stageName, targetName)
		if err != nil {
			return err
		}
		if err := installer.Install(ctx); err != nil {
			return err
		}
		// Once filesystem installation succeeded, any uncertain commit result must
		// retain the stage and resource locks. The deferred finalizer will observe
		// this fence and refuse cleanup/quiescence.
		committedAck := false
		defer func() {
			if !committedAck {
				scope.quarantineUncertain()
			}
		}()
		committed, err := repository.CommitFirstPublication(context.WithoutCancel(ctx), guard)
		if err != nil || !committed {
			return errors.Join(err, errors.New("first publication commit was not acknowledged"))
		}
		committedAck = true
		return nil
	})
	if resultErr != nil {
		return result, resultErr
	}
	released, err := repository.ReleasePublicationInputHolds(context.WithoutCancel(ctx), guard)
	if err != nil || !released {
		return result, errors.Join(err, fmt.Errorf("publication input holds were not released"))
	}
	return result, nil
}
