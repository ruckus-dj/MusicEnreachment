package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type firstAbsentPublicationRepository interface {
	absentTargetInstallationRepository
	CommitFirstPublication(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
	ReleasePublicationInputHolds(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
}

type firstAbsentCompletionRepository interface {
	BeginPublicationFirstAbsentCleanup(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationFirstAbsentCleanupWitness) (persistence.PublicationFirstAbsentCleanupOutcome, error)
	BeginPublicationFirstAbsentDirectoryCleanup(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationFirstAbsentCleanupWitness) (persistence.PublicationFirstAbsentCleanupOutcome, error)
	RecordPublicationFirstAbsentCleanupResult(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationFirstAbsentCleanupWitness, string, string) (persistence.PublicationFirstAbsentCleanupOutcome, error)
	SettleFirstAbsentPublication(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationFirstAbsentCleanupWitness, bool) (bool, error)
}

// RunFirstAbsentPublication stages, exposes, and commits one first publication.
// Completion cleanup runs under the same sealed delivery scope as exposure.
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
	result, resultErr = runner.runWithValidatedStage(ctx, scope, input, stage, func(ctx context.Context, validated persistence.PublicationStageObservation, stage PreparedPublicationStage, reservation *publicationSyncReservation) error {
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
		settlementAck := false
		defer func() {
			if !settlementAck {
				scope.quarantineUncertain()
			}
		}()
		committed, err := repository.CommitFirstPublication(context.WithoutCancel(ctx), guard)
		if err != nil || !committed {
			return errors.Join(err, errors.New("first publication commit was not acknowledged"))
		}
		if err := scope.beginFirstAbsentFinalization(reservation); err != nil {
			scope.quarantineUncertain()
			return err
		}
		completion, ok := repository.(firstAbsentCompletionRepository)
		if !ok {
			scope.quarantineUncertain()
			return errors.New("first-publication completion repository is unavailable")
		}
		if err := finishFirstAbsentPublication(context.WithoutCancel(ctx), scope, stage, completion); err != nil {
			scope.quarantineUncertain()
			return err
		}
		// finishFirstAbsentPublication returns only after settlement and the
		// local acknowledgement have both completed.
		settlementAck = true
		return nil
	})
	if resultErr != nil {
		return result, resultErr
	}
	return result, nil
}

// finishFirstAbsentPublication performs only the two identity-bound mutations
// recorded by the persistence journal, then closes the stage before settlement.
func finishFirstAbsentPublication(ctx context.Context, scope *PublicationExecutionScope, stage PreparedPublicationStage, repository firstAbsentCompletionRepository) error {
	prepared, ok := stage.(*preparedPublicationStage)
	if !ok || prepared == nil {
		return errors.New("first-publication stage does not expose owned cleanup evidence")
	}
	witness, inputCleanup, directoryCleanup, err := prepared.firstAbsentCleanupWitness(ctx)
	if err != nil {
		return err
	}
	guard := scope.guard
	ctx = context.WithoutCancel(ctx)
	begin, err := repository.BeginPublicationFirstAbsentCleanup(ctx, guard, witness)
	if err != nil || begin != persistence.PublicationFirstAbsentCleanupRecorded {
		return errors.Join(err, errors.New("first-publication input cleanup intent was not acknowledged"))
	}
	err = inputCleanup.RemoveOwnedRegular(ctx, prepared.inputName, sourcefs.DirectoryIdentity{Platform: prepared.identity.Platform, Volume: prepared.identity.Volume, FileID: prepared.identity.FileID}, prepared.sourceFileInfo)
	result := "removed"
	if errors.Is(err, fs.ErrNotExist) {
		err, result = nil, "already_absent"
	}
	if err != nil {
		return fmt.Errorf("remove owned first-publication input: %w", err)
	}
	out, err := repository.RecordPublicationFirstAbsentCleanupResult(ctx, guard, witness, "input", result)
	if err != nil || out != persistence.PublicationFirstAbsentCleanupRecorded {
		return errors.Join(err, errors.New("first-publication input cleanup result was not acknowledged"))
	}
	begin, err = repository.BeginPublicationFirstAbsentDirectoryCleanup(ctx, guard, witness)
	if err != nil || begin != persistence.PublicationFirstAbsentCleanupRecorded {
		return errors.Join(err, errors.New("first-publication directory cleanup intent was not acknowledged"))
	}
	if err := prepared.Close(); err != nil {
		return fmt.Errorf("close first-publication stage capability: %w", err)
	}
	err = directoryCleanup.RemoveEmptyChildDir(ctx, prepared.directoryName, prepared.parentIdentity, sourcefs.DirectoryIdentity{Platform: prepared.identity.Platform, Volume: prepared.identity.Volume, FileID: prepared.identity.FileID})
	result = "removed"
	if errors.Is(err, fs.ErrNotExist) {
		err, result = nil, "already_absent"
	}
	if err != nil {
		return fmt.Errorf("remove empty first-publication directory: %w", err)
	}
	out, err = repository.RecordPublicationFirstAbsentCleanupResult(ctx, guard, witness, "directory", result)
	if err != nil || out != persistence.PublicationFirstAbsentCleanupRecorded {
		return errors.Join(err, errors.New("first-publication directory cleanup result was not acknowledged"))
	}
	settled, err := repository.SettleFirstAbsentPublication(ctx, guard, witness, true)
	if err != nil || !settled {
		return errors.Join(err, errors.New("first-publication settlement was not acknowledged"))
	}
	scope.acknowledgeFirstAbsentFinalization()
	return nil
}
