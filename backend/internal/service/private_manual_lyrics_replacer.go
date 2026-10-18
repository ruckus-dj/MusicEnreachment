package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type privateManualLyricsReplacementRepository interface {
	GuardedRecordManualLyricsReplacement(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationReplacementEvidence) (persistence.PublicationReplacementOutcome, error)
}

// replacePrivateManualLyrics performs only the native installation phase for
// an admitted private manual-lyrics handoff. The owner retains and settles work.
func replacePrivateManualLyrics(
	ctx context.Context,
	scope *PublicationExecutionScope,
	work *privateManualLyricsWork,
	handoff *privateManualLyricsExposureHandoff,
) (_ *privateManualLyricsReplacementInstalled, resultErr error) {
	if scope == nil || work == nil || handoff == nil || work.stage == nil || handoff.stage != work.stage ||
		handoff.scope != scope || handoff.work != work || handoff.guard != scope.guard ||
		!scope.validPrivateManualLyricsWork(work, work.stage) {
		return nil, errors.New("private manual lyrics replacement requires its exact retained work")
	}
	if err := handoff.Bind(scope, work); err != nil {
		return nil, err
	}
	stage := work.stage
	repository, ok := stage.repository.(privateManualLyricsReplacementRepository)
	if !ok {
		return nil, errors.New("guarded manual lyrics replacement repository is unavailable")
	}
	if stage.guard != scope.guard || !stage.privatePrepMode || !stage.rewrite || stage.outputName == "" ||
		stage.preparationRecord.Step != "input_ready" || stage.preparationRecord.CopiedInput == nil ||
		stage.rewriteIdentity.SHA256 == "" || handoff.inputs.Rewrite == nil ||
		handoff.intent.Expectation != "expected_current" || handoff.intent.PrepOwner != stage.preparationRecord.OwnerToken ||
		handoff.observation.RelativePath != handoff.intent.StagedPath ||
		!validReplacementDigest(handoff.observation.PayloadSHA256) || handoff.targetInfo == nil || handoff.candidateInfo == nil {
		return nil, errors.New("private manual lyrics replacement facts are incomplete")
	}
	if err := stage.validatePrivateManualLyrics(ctx, scope, work); err != nil {
		return nil, fmt.Errorf("validate private manual lyrics stage before replacement: %w", err)
	}
	if err := stage.verifyInitialCopyInputs(ctx); err != nil {
		return nil, fmt.Errorf("verify private manual lyrics original backup: %w", err)
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return nil, fmt.Errorf("verify private manual lyrics stage directory: %w", err)
	}
	if err := handoff.RevalidateNamespace(ctx); err != nil {
		return nil, err
	}
	targetDir, ok := handoff.parent.(sourcefs.OutputMutation)
	if !ok {
		return nil, errors.New("private manual lyrics target mutation capability is unavailable")
	}
	atomicReplace, ok := handoff.parent.(sourcefs.OutputAtomicReplace)
	if !ok {
		return nil, errors.New("private manual lyrics atomic replacement capability is unavailable")
	}
	stageMutation, ok := stage.directory.(sourcefs.OutputMutation)
	if !ok {
		return nil, errors.New("private manual lyrics stage mutation capability is unavailable")
	}
	fileSync, ok := stage.directory.(sourcefs.OutputRegularFileSync)
	if !ok {
		return nil, errors.New("private manual lyrics native regular-file sync capability is unavailable")
	}
	backupSyncDir, ok := stage.directory.(sourcefs.OutputDirectorySync)
	if !ok {
		return nil, errors.New("private manual lyrics backup directory sync capability is unavailable")
	}
	stageSyncDir, ok := stage.directory.(sourcefs.OutputDirectorySync)
	if !ok {
		return nil, errors.New("private manual lyrics stage directory sync capability is unavailable")
	}
	targetSyncDir, ok := handoff.parent.(sourcefs.OutputDirectorySync)
	if !ok {
		return nil, errors.New("private manual lyrics target directory sync capability is unavailable")
	}
	rootSyncDir, ok := handoff.root.(sourcefs.OutputDirectorySync)
	if !ok {
		return nil, errors.New("private manual lyrics owning directory sync capability is unavailable")
	}

	// Re-hash both frozen original bytes and the validated staged candidate while
	// their native names still exist. The backup is the durable original after rename.
	backup, err := stageMutation.OpenExistingRegular(ctx, stage.inputName)
	if err != nil {
		return nil, fmt.Errorf("open private manual lyrics backup: %w", err)
	}
	backupInfo, statErr := backup.Stat(ctx)
	backupID, hashErr := managedReplacementIdentity(ctx, backup, backupInfo)
	closeErr := backup.Close()
	if statErr != nil || hashErr != nil || closeErr != nil {
		return nil, errors.Join(fmt.Errorf("verify private manual lyrics backup: %w", errors.Join(statErr, hashErr)), closeErr)
	}
	copied := stage.preparationRecord.CopiedInput
	if !samePublicationFileIdentity(backupID, stage.rewriteIdentity) || backupID.SHA256 != handoff.targetSHA256 ||
		copied.SizeBytes == nil || copied.MTime == nil || backupID.SizeBytes == nil || backupID.MTime == nil ||
		*copied.SizeBytes != *backupID.SizeBytes || *copied.MTime != *backupID.MTime || (copied.SHA256 != "" && copied.SHA256 != backupID.SHA256) ||
		stage.sourceFileInfo == nil || !os.SameFile(stage.sourceFileInfo, backupInfo) {
		return nil, errors.New("private manual lyrics backup differs from frozen original input")
	}
	target, err := targetDir.OpenExistingRegular(ctx, filepath.Base(handoff.intent.TargetRelativePath))
	if err != nil {
		return nil, fmt.Errorf("open private manual lyrics current target: %w", err)
	}
	targetInfo, statErr := target.Stat(ctx)
	targetID, hashErr := managedReplacementIdentity(ctx, target, targetInfo)
	closeErr = target.Close()
	if statErr != nil || hashErr != nil || closeErr != nil {
		return nil, errors.Join(fmt.Errorf("verify private manual lyrics current target: %w", errors.Join(statErr, hashErr)), closeErr)
	}
	if !os.SameFile(handoff.targetInfo, targetInfo) || !samePublicationFileIdentity(targetID, stage.rewriteIdentity) ||
		targetID.MTime == nil || stage.rewriteIdentity.MTime == nil || *targetID.MTime != *stage.rewriteIdentity.MTime {
		return nil, errors.New("private manual lyrics current target differs from frozen G0")
	}
	if err := fileSync.SyncExistingRegular(ctx, stage.inputName, backupInfo); err != nil {
		return nil, fmt.Errorf("sync private manual lyrics backup: %w", err)
	}
	candidateName := filepath.Base(stage.paths.OutputPath)
	candidate, err := stageMutation.OpenExistingRegular(ctx, candidateName)
	if err != nil {
		return nil, fmt.Errorf("open private manual lyrics candidate: %w", err)
	}
	candidateInfo, statErr := candidate.Stat(ctx)
	candidateID, hashErr := managedReplacementIdentity(ctx, candidate, candidateInfo)
	closeErr = candidate.Close()
	if statErr != nil || hashErr != nil || closeErr != nil {
		return nil, errors.Join(fmt.Errorf("verify private manual lyrics candidate: %w", errors.Join(statErr, hashErr)), closeErr)
	}
	if !os.SameFile(handoff.candidateInfo, candidateInfo) || candidateID.SHA256 != handoff.observation.PayloadSHA256 ||
		candidateID.SizeBytes == nil || handoff.observation.Ownership.ExpectedSize == nil || *candidateID.SizeBytes != *handoff.observation.Ownership.ExpectedSize ||
		candidateID.MTime == nil || handoff.observation.Ownership.ExpectedMTime == nil || *candidateID.MTime != *handoff.observation.Ownership.ExpectedMTime {
		return nil, errors.New("private manual lyrics candidate changed after acknowledgement")
	}
	if err := fileSync.SyncExistingRegular(ctx, candidateName, candidateInfo); err != nil {
		return nil, fmt.Errorf("sync private manual lyrics candidate: %w", err)
	}
	if err := backupSyncDir.SyncDirectory(ctx); err != nil {
		return nil, fmt.Errorf("sync private manual lyrics backup directory: %w", err)
	}
	if err := stageSyncDir.SyncDirectory(ctx); err != nil {
		return nil, fmt.Errorf("sync private manual lyrics stage directory: %w", err)
	}
	if err := rootSyncDir.SyncDirectory(ctx); err != nil {
		return nil, fmt.Errorf("sync private manual lyrics owning directory: %w", err)
	}

	evidence := persistence.PublicationReplacementEvidence{
		Version: 1, Kind: "publication_replacement", Guard: scope.guard,
		PositionID: scope.guard.ExpectedPositionID, Epoch: scope.guard.Epoch,
		CandidateGeneration: scope.guard.CandidateGeneration,
		ExpectedGeneration:  privateLyricsUUIDPointer(handoff.inputs.Rewrite.CurrentGeneration),
		Intent:              handoff.intent, Staged: handoff.observation.Clone(),
		Identity: targetID, BackupIdentity: backupID,
		BackupPath: filepath.ToSlash(filepath.Join(stage.directoryName, stage.inputName)),
	}
	mutationStarted := false
	defer func() {
		if mutationStarted {
			scope.quarantineUncertain()
		}
	}()
	record := func(from, to string) error {
		evidence.From, evidence.To = from, to
		outcome, recordErr := repository.GuardedRecordManualLyricsReplacement(context.WithoutCancel(ctx), scope.guard, evidence)
		if recordErr != nil || outcome != persistence.PublicationReplacementRecorded {
			return errors.Join(recordErr, fmt.Errorf("private manual lyrics replacement transition %s was not acknowledged", to))
		}
		return nil
	}
	mutationStarted = true
	if err := record("exposing", "backup_intent"); err != nil {
		return nil, err
	}
	if err := record("backup_intent", "backup_ready"); err != nil {
		return nil, err
	}
	if err := record("backup_ready", "replacement_intent"); err != nil {
		return nil, err
	}
	if err := handoff.RevalidateNamespace(ctx); err != nil {
		return nil, err
	}
	if err := atomicReplace.AtomicReplaceExisting(ctx, stage.directory, candidateName, candidateInfo, filepath.Base(handoff.intent.TargetRelativePath), handoff.targetInfo); err != nil {
		return nil, fmt.Errorf("atomically install private manual lyrics candidate: %w", err)
	}
	if err := targetSyncDir.SyncDirectory(ctx); err != nil {
		return nil, fmt.Errorf("sync installed private manual lyrics target directory: %w", err)
	}
	if err := stageSyncDir.SyncDirectory(ctx); err != nil {
		return nil, fmt.Errorf("sync private manual lyrics source directory: %w", err)
	}
	if err := rootSyncDir.SyncDirectory(ctx); err != nil {
		return nil, fmt.Errorf("sync private manual lyrics owning directory after install: %w", err)
	}
	if err := scope.validPrivateManualLyricsWork(work, stage); !err {
		return nil, errors.New("private manual lyrics work capability was lost after install")
	}
	reader, ok := stage.repository.(publicationManualLyricsCurrentInputReader)
	if !ok {
		return nil, errors.New("private manual lyrics current-input reader is unavailable after install")
	}
	current, valid, err := reader.PublicationManualLyricsCurrentInputs(ctx, scope.guard)
	if err != nil || !valid || !reflect.DeepEqual(current, stage.privateInputs) || !reflect.DeepEqual(current, handoff.inputs) {
		return nil, errors.Join(fmt.Errorf("verify frozen manual lyrics inputs after install: %w", err), errors.New("current manual lyrics inputs changed after install"))
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return nil, fmt.Errorf("verify private manual lyrics stage after install: %w", err)
	}
	if err := verifyPrivateManualLyricsPostInstallNamespace(ctx, handoff, stage); err != nil {
		return nil, err
	}
	stagedOutput, stagedErr := stageMutation.OpenExistingRegular(ctx, stage.outputName)
	if stagedErr == nil {
		return nil, errors.Join(errors.New("private manual lyrics staged output remains after installation"), stagedOutput.Close())
	}
	if !errors.Is(stagedErr, fs.ErrNotExist) {
		return nil, errors.New("private manual lyrics staged output remains after installation")
	}
	installed, err := targetDir.OpenExistingRegular(ctx, filepath.Base(handoff.intent.TargetRelativePath))
	if err != nil {
		return nil, fmt.Errorf("read back installed private manual lyrics target: %w", err)
	}
	installedInfo, statErr := installed.Stat(ctx)
	installedID, hashErr := managedReplacementIdentity(ctx, installed, installedInfo)
	closeErr = installed.Close()
	if statErr != nil || hashErr != nil || closeErr != nil {
		return nil, errors.Join(fmt.Errorf("verify installed private manual lyrics target: %w", errors.Join(statErr, hashErr)), closeErr)
	}
	if !os.SameFile(handoff.candidateInfo, installedInfo) || installedID.SHA256 != handoff.observation.PayloadSHA256 ||
		installedID.SizeBytes == nil || handoff.observation.Ownership.ExpectedSize == nil || *installedID.SizeBytes != *handoff.observation.Ownership.ExpectedSize ||
		installedID.MTime == nil || handoff.observation.Ownership.ExpectedMTime == nil || *installedID.MTime != *handoff.observation.Ownership.ExpectedMTime {
		return nil, errors.New("installed private manual lyrics target differs from acknowledged candidate")
	}
	backup, err = stageMutation.OpenExistingRegular(ctx, stage.inputName)
	if err != nil {
		return nil, fmt.Errorf("reopen frozen backup after install: %w", err)
	}
	backupInfo, statErr = backup.Stat(ctx)
	backupAfter, hashErr := managedReplacementIdentity(ctx, backup, backupInfo)
	closeErr = backup.Close()
	if statErr != nil || hashErr != nil || closeErr != nil || !samePublicationFileIdentity(backupAfter, stage.rewriteIdentity) ||
		backupAfter.MTime == nil || copied.MTime == nil || *backupAfter.MTime != *copied.MTime ||
		stage.sourceFileInfo == nil || !os.SameFile(stage.sourceFileInfo, backupInfo) {
		return nil, errors.Join(fmt.Errorf("verify frozen backup after install: %w", errors.Join(statErr, hashErr)), closeErr, errors.New("private manual lyrics backup changed after install"))
	}
	evidence.Identity = installedID
	if err := record("replacement_intent", "installed"); err != nil {
		return nil, err
	}
	proof := scope.recordPrivateManualLyricsReplacementInstalled(work)
	if proof == nil {
		return nil, errors.New("private manual lyrics installed acknowledgement lost its work capability")
	}
	mutationStarted = false
	return proof, nil
}

func privateLyricsUUIDPointer(value uuid.UUID) *uuid.UUID { return &value }

// verifyPrivateManualLyricsPostInstallNamespace pins the output namespace
// without inspecting the G0 target path, which the atomic replacement changed.
func verifyPrivateManualLyricsPostInstallNamespace(ctx context.Context, handoff *privateManualLyricsExposureHandoff, stage *preparedPublicationStage) error {
	if handoff == nil || stage == nil || handoff.parent == nil || handoff.root == nil || stage.parent == nil {
		return errors.New("private manual lyrics post-install namespace capabilities are unavailable")
	}
	if err := verifyManagedTargetParent(ctx, handoff.root, handoff.parentPath, handoff.parentIdentity); err != nil {
		return fmt.Errorf("verify private manual lyrics target parent after install: %w", err)
	}
	rootPath, err := os.Stat(stage.sourceRootPath)
	if err != nil {
		return fmt.Errorf("stat private manual lyrics output root after install: %w", err)
	}
	stageRoot, err := stage.parent.Stat(ctx)
	if err != nil {
		return fmt.Errorf("stat pinned private manual lyrics output root after install: %w", err)
	}
	handoffRoot, err := handoff.root.Stat(ctx)
	if err != nil {
		return fmt.Errorf("stat retained private manual lyrics output root after install: %w", err)
	}
	if !rootPath.IsDir() || !stageRoot.IsDir() || !handoffRoot.IsDir() ||
		!os.SameFile(rootPath, stageRoot) || !os.SameFile(rootPath, handoffRoot) {
		return errors.New("private manual lyrics output root namespace changed after install")
	}
	return nil
}
