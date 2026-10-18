package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type replacementPublicationRepository interface {
	managedTargetReplacementRepository
	CommitReplacementPublication(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
	BeginPublicationReplacementCleanup(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationReplacementCleanupWitness) (persistence.PublicationReplacementCleanupOutcome, error)
	BeginPublicationReplacementDirectoryCleanup(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationReplacementCleanupWitness) (persistence.PublicationReplacementCleanupOutcome, error)
	RecordPublicationReplacementCleanupResult(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationReplacementCleanupWitness, string, string) (persistence.PublicationReplacementCleanupOutcome, error)
	SettleReplacementPublication(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationReplacementCleanupWitness, bool) (bool, error)
}

// RunReplacementPublication stages and rewrites one same-path publication.
// From backup-intent onward every uncertain result retains the scope fence.
func (runner PublicationStagingRunner) RunReplacementPublication(
	ctx context.Context,
	scope *PublicationExecutionScope,
	input persistence.PublicationInputs,
	stage PreparedPublicationStage,
	repository replacementPublicationRepository,
	intent persistence.PublicationExposureIntent,
	expectedGeneration *uuid.UUID,
	root, stageDir, targetParent sourcefs.OutputDirectory,
	stageName, targetName string,
) (result PublicationStageObservation, resultErr error) {
	if scope == nil || repository == nil || intent.Expectation != "expected_current" {
		return result, errors.New("replacement publication dependencies are unavailable")
	}
	pinned, ok := runner.PinnedTools.(*GuardedPublicationPinnedTools)
	if !ok || pinned == nil || pinned.scope != scope || pinned.resolver == nil || pinned.resolver.scope != scope || pinned.guard != scope.guard || !scope.CanExecute() {
		return result, errors.New("publication pinned tools are not bound to the execution scope")
	}
	prepared, ok := stage.(*preparedPublicationStage)
	if !ok || prepared == nil || !prepared.rewrite {
		return result, errors.New("replacement publication requires an owned rewrite stage")
	}
	result, resultErr = runner.runWithValidatedStage(ctx, scope, input, stage, func(ctx context.Context, validated persistence.PublicationStageObservation, stage PreparedPublicationStage, reservation *publicationSyncReservation) (callbackErr error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				scope.quarantineUncertain()
				callbackErr = fmt.Errorf("replacement completion panicked: %v", recovered)
			}
		}()
		replacer := &newPublicationManagedTargetReplacer{
			scope: scope, repository: repository, guard: scope.guard, intent: intent,
			validated: validated.Clone(), expectedGeneration: expectedGeneration,
			stage: prepared, root: root, stageDir: stageDir, targetDir: targetParent,
			stageName: stageName, targetName: targetName, reservation: reservation,
		}
		if err := replacer.replaceAndComplete(context.WithoutCancel(ctx)); err != nil {
			return err
		}
		token := replacer.completionToken
		if token == nil || !token.active {
			scope.quarantineUncertain()
			return errors.New("replacement completion was not sealed")
		}
		committed, err := repository.CommitReplacementPublication(context.WithoutCancel(ctx), scope.guard)
		if err != nil || !committed {
			scope.quarantineUncertain()
			return errors.Join(err, errors.New("replacement publication commit was not acknowledged"))
		}
		witness, backupInfo, cleanup, dirCleanup, err := replacementCleanupWitness(ctx, prepared, stageDir, root, targetParent, targetName, validated)
		if err != nil {
			scope.quarantineUncertain()
			return err
		}
		if err := finishReplacementPublication(context.WithoutCancel(ctx), scope, prepared, token, repository, witness, backupInfo, cleanup, dirCleanup); err != nil {
			scope.quarantineUncertain()
			return err
		}
		return nil
	})
	return result, resultErr
}

func replacementCleanupWitness(ctx context.Context, stage *preparedPublicationStage, stageDir, root, targetDir sourcefs.OutputDirectory, targetName string, validated persistence.PublicationStageObservation) (persistence.PublicationReplacementCleanupWitness, fs.FileInfo, sourcefs.OutputCleanup, sourcefs.OutputCleanup, error) {
	if stage == nil || stageDir == nil || root == nil || targetDir == nil {
		return persistence.PublicationReplacementCleanupWitness{}, nil, nil, nil, errors.New("replacement cleanup capabilities are unavailable")
	}
	fileCleanup, fileOK := stageDir.(sourcefs.OutputCleanup)
	dirCleanup, dirOK := root.(sourcefs.OutputCleanup)
	mutation, mutationOK := stageDir.(sourcefs.OutputMutation)
	if !fileOK || !dirOK || !mutationOK {
		return persistence.PublicationReplacementCleanupWitness{}, nil, nil, nil, errors.New("native replacement cleanup capability is unavailable")
	}
	if err := root.Verify(ctx); err != nil {
		return persistence.PublicationReplacementCleanupWitness{}, nil, nil, nil, fmt.Errorf("verify replacement cleanup root: %w", err)
	}
	recordedIdentity := sourcefs.DirectoryIdentity{Platform: stage.identity.Platform, Volume: stage.identity.Volume, FileID: stage.identity.FileID}
	pinnedIdentity, err := stageDir.Identity(ctx)
	if err != nil {
		return persistence.PublicationReplacementCleanupWitness{}, nil, nil, nil, fmt.Errorf("verify pinned replacement backup directory: %w", err)
	}
	if pinnedIdentity != recordedIdentity {
		return persistence.PublicationReplacementCleanupWitness{}, nil, nil, nil, errors.New("replacement backup directory identity changed")
	}
	if err := root.VerifyChild(ctx, stage.directoryName, recordedIdentity); err != nil {
		return persistence.PublicationReplacementCleanupWitness{}, nil, nil, nil, fmt.Errorf("verify replacement backup directory: %w", err)
	}
	parentIdentity, err := stage.parent.Identity(ctx)
	if err != nil || parentIdentity != (sourcefs.DirectoryIdentity{Platform: stage.parentIdentity.Platform, Volume: stage.parentIdentity.Volume, FileID: stage.parentIdentity.FileID}) {
		return persistence.PublicationReplacementCleanupWitness{}, nil, nil, nil, errors.Join(fmt.Errorf("verify replacement backup parent: %w", err), errors.New("replacement backup parent changed"))
	}
	targetMutation, ok := targetDir.(sourcefs.OutputMutation)
	if !ok {
		return persistence.PublicationReplacementCleanupWitness{}, nil, nil, nil, errors.New("native replacement target read capability is unavailable")
	}
	target, err := targetMutation.OpenExistingRegular(ctx, targetName)
	if err != nil {
		return persistence.PublicationReplacementCleanupWitness{}, nil, nil, nil, err
	}
	targetInfo, statErr := target.Stat(ctx)
	targetIdentityEvidence, hashErr := managedReplacementIdentity(ctx, target, targetInfo)
	closeErr := target.Close()
	if statErr != nil || hashErr != nil || closeErr != nil || targetIdentityEvidence.SizeBytes == nil || *targetIdentityEvidence.SizeBytes != *validated.Ownership.ExpectedSize || targetIdentityEvidence.SHA256 != validated.PayloadSHA256 {
		return persistence.PublicationReplacementCleanupWitness{}, nil, nil, nil, errors.Join(errors.New("installed replacement changed before cleanup"), statErr, hashErr, closeErr)
	}
	backupName := stage.inputName
	backup, err := mutation.OpenExistingRegular(ctx, backupName)
	if err != nil {
		return persistence.PublicationReplacementCleanupWitness{}, nil, nil, nil, fmt.Errorf("open native replacement backup: %w", err)
	}
	backupInfo, statErr := backup.Stat(ctx)
	backupIdentity, hashErr := managedReplacementIdentity(ctx, backup, backupInfo)
	closeErr = backup.Close()
	if statErr != nil || hashErr != nil || closeErr != nil || stage.sourceFileInfo == nil || !os.SameFile(backupInfo, stage.sourceFileInfo) || !samePublicationFileIdentity(backupIdentity, stage.rewriteIdentity) || backupIdentity.MTime == nil || *backupIdentity.MTime != stage.sourceFileInfo.ModTime().UnixMicro() {
		return persistence.PublicationReplacementCleanupWitness{}, nil, nil, nil, errors.Join(errors.New("native replacement backup changed"), statErr, hashErr, closeErr)
	}
	return persistence.PublicationReplacementCleanupWitness{
		PrivateDirRel: stage.directoryName,
		BackupRel:     path.Join(stage.directoryName, backupName),
		Directory:     persistence.PublicationDirectoryIdentity{Platform: stage.identity.Platform, Volume: stage.identity.Volume, FileID: stage.identity.FileID},
		Parent:        persistence.PublicationDirectoryIdentity{Platform: stage.parentIdentity.Platform, Volume: stage.parentIdentity.Volume, FileID: stage.parentIdentity.FileID},
		Backup:        backupIdentity,
	}, backupInfo, fileCleanup, dirCleanup, nil
}

func finishReplacementPublication(ctx context.Context, scope *PublicationExecutionScope, stage *preparedPublicationStage, token *replacementCompletionToken, repository replacementPublicationRepository, witness persistence.PublicationReplacementCleanupWitness, backupInfo fs.FileInfo, fileCleanup, dirCleanup sourcefs.OutputCleanup) error {
	guard := scope.guard
	begin, err := repository.BeginPublicationReplacementCleanup(ctx, guard, witness)
	if err != nil || begin != persistence.PublicationReplacementCleanupRecorded {
		return errors.Join(err, errors.New("replacement backup cleanup intent was not acknowledged"))
	}
	err = fileCleanup.RemoveOwnedRegular(ctx, path.Base(witness.BackupRel), sourcefs.DirectoryIdentity{Platform: stage.identity.Platform, Volume: stage.identity.Volume, FileID: stage.identity.FileID}, backupInfo)
	result := "removed"
	if errors.Is(err, fs.ErrNotExist) {
		err, result = nil, "already_absent"
	}
	if err != nil {
		return fmt.Errorf("remove owned replacement backup: %w", err)
	}
	out, err := repository.RecordPublicationReplacementCleanupResult(ctx, guard, witness, "backup", result)
	if err != nil || out != persistence.PublicationReplacementCleanupRecorded {
		return errors.Join(err, errors.New("replacement backup cleanup result was not acknowledged"))
	}
	begin, err = repository.BeginPublicationReplacementDirectoryCleanup(ctx, guard, witness)
	if err != nil || begin != persistence.PublicationReplacementCleanupRecorded {
		return errors.Join(err, errors.New("replacement directory cleanup intent was not acknowledged"))
	}
	if err := stage.Close(); err != nil {
		return fmt.Errorf("close replacement stage: %w", err)
	}
	err = dirCleanup.RemoveEmptyChildDir(ctx, stage.directoryName, stage.parentIdentity, sourcefs.DirectoryIdentity{Platform: stage.identity.Platform, Volume: stage.identity.Volume, FileID: stage.identity.FileID})
	result = "removed"
	if errors.Is(err, fs.ErrNotExist) {
		err, result = nil, "already_absent"
	}
	if err != nil {
		return fmt.Errorf("remove empty replacement directory: %w", err)
	}
	out, err = repository.RecordPublicationReplacementCleanupResult(ctx, guard, witness, "directory", result)
	if err != nil || out != persistence.PublicationReplacementCleanupRecorded {
		return errors.Join(err, errors.New("replacement directory cleanup result was not acknowledged"))
	}
	settled, err := repository.SettleReplacementPublication(ctx, guard, witness, true)
	if err != nil || !settled {
		return errors.Join(err, errors.New("replacement publication settlement was not acknowledged"))
	}
	return scope.acknowledgeReplacementCompletion(token)
}
