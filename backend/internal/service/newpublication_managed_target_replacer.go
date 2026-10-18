package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type managedTargetReplacementRepository interface {
	RecordPublicationReplacement(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationReplacementEvidence) (persistence.PublicationReplacementOutcome, error)
}

// newPublicationManagedTargetReplacer performs only the native exposure phase.
// Its caller owns the scope's synchronous-work reservation and must not settle
// the operation: installed evidence intentionally leaves that scope fenced for
// the later replacement-completion transaction.
type newPublicationManagedTargetReplacer struct {
	scope              *PublicationExecutionScope
	repository         managedTargetReplacementRepository
	guard              persistence.PublicationDeliveryGuard
	intent             persistence.PublicationExposureIntent
	validated          persistence.PublicationStageObservation
	expectedGeneration *uuid.UUID
	stage              *preparedPublicationStage
	root               sourcefs.OutputDirectory
	stageDir           sourcefs.OutputDirectory
	targetDir          sourcefs.OutputDirectory
	stageName          string
	targetName         string
	reservation        *publicationSyncReservation
	completionToken    *replacementCompletionToken
}

func (replacer *newPublicationManagedTargetReplacer) Replace(ctx context.Context) (resultErr error) {
	return replacer.replace(ctx, false)
}

// replaceAndComplete is reserved for the same-live replacement publication
// path. The token must already be bound to this scope's exclusive completion
// fence; standalone Replace deliberately retains its legacy behavior.
func (replacer *newPublicationManagedTargetReplacer) replaceAndComplete(ctx context.Context) error {
	if replacer == nil || replacer.scope == nil {
		return errors.New("managed publication replacement completion scope is unavailable")
	}
	return replacer.replace(ctx, true)
}

func (replacer *newPublicationManagedTargetReplacer) replace(ctx context.Context, completing bool) (resultErr error) {
	if replacer == nil || replacer.scope == nil || replacer.repository == nil || replacer.stage == nil || replacer.root == nil || replacer.stageDir == nil || replacer.targetDir == nil || replacer.guard != replacer.scope.guard || replacer.stage.guard != replacer.guard || replacer.intent.PrepOwner != replacer.stage.preparationRecord.OwnerToken || replacer.intent.Expectation != "expected_current" || replacer.expectedGeneration == nil || *replacer.expectedGeneration == uuid.Nil || replacer.validated.Ownership.ExpectedSize == nil || replacer.validated.Ownership.ExpectedMTime == nil || *replacer.validated.Ownership.ExpectedSize < 0 || !validReplacementDigest(replacer.validated.PayloadSHA256) {
		return errors.New("managed publication replacement dependencies are unavailable")
	}
	replacer.validated = replacer.validated.Clone()
	reservation, ownedReservation := replacer.scope.reserveReplacementWork(replacer.reservation)
	if reservation == nil {
		return errors.New("publication execution scope is unavailable or already reserved")
	}
	if ownedReservation {
		defer replacer.scope.releaseReplacementWork(reservation, true)
	} else {
		defer replacer.scope.releaseReplacementWork(reservation, false)
	}
	// Bind every filesystem name and directory capability to the persisted
	// exposure intent before doing any I/O. A matching byte digest does not make
	// an arbitrary sibling target or stage directory authorized.
	targetParentPath := path.Dir(replacer.intent.TargetRelativePath)
	if path.Base(replacer.intent.TargetRelativePath) != replacer.targetName ||
		path.Dir(replacer.intent.StagedPath) != replacer.stage.directoryName ||
		(targetParentPath == "." && replacer.intent.TargetParent != replacer.intent.OutputRootParent) ||
		replacer.stageName != replacer.stage.directoryName ||
		path.Base(replacer.intent.StagedPath) != filepath.Base(replacer.stage.paths.OutputPath) ||
		replacer.stage.paths.RelativePath != replacer.intent.StagedPath ||
		replacer.validated.RelativePath != replacer.intent.StagedPath ||
		replacer.validated.Ownership.ExpectedSize == nil || replacer.validated.Ownership.ExpectedMTime == nil ||
		*replacer.validated.Ownership.ExpectedSize < 0 || !validReplacementDigest(replacer.validated.PayloadSHA256) {
		return errors.New("managed publication replacement paths do not match exposure intent")
	}
	if err := replacer.stage.Validate(ctx); err != nil {
		return fmt.Errorf("validate managed publication replacement stage: %w", err)
	}
	if err := replacer.root.Verify(ctx); err != nil {
		return fmt.Errorf("verify managed publication output root: %w", err)
	}
	if err := verifyManagedTargetParent(ctx, replacer.root, targetParentPath, sourcefs.DirectoryIdentity{Platform: replacer.intent.TargetParent.Platform, Volume: replacer.intent.TargetParent.Volume, FileID: replacer.intent.TargetParent.FileID}); err != nil {
		return fmt.Errorf("verify managed publication target parent: %w", err)
	}
	rootIdentity, err := replacer.root.Identity(ctx)
	if err != nil || rootIdentity != (sourcefs.DirectoryIdentity{Platform: replacer.intent.OutputRootParent.Platform, Volume: replacer.intent.OutputRootParent.Volume, FileID: replacer.intent.OutputRootParent.FileID}) {
		return errors.Join(fmt.Errorf("verify managed publication output root identity: %w", err), errors.New("managed publication output root identity changed"))
	}
	if err := replacer.root.VerifyChild(ctx, replacer.stage.directoryName, sourcefs.DirectoryIdentity{Platform: replacer.stage.identity.Platform, Volume: replacer.stage.identity.Volume, FileID: replacer.stage.identity.FileID}); err != nil {
		return fmt.Errorf("verify managed publication stage directory: %w", err)
	}
	stageDirIdentity, err := replacer.stageDir.Identity(ctx)
	if err != nil || stageDirIdentity != (sourcefs.DirectoryIdentity{Platform: replacer.stage.identity.Platform, Volume: replacer.stage.identity.Volume, FileID: replacer.stage.identity.FileID}) {
		return errors.Join(fmt.Errorf("verify passed managed publication stage directory: %w", err), errors.New("managed publication stage directory identity changed"))
	}
	targetParentIdentity, err := replacer.targetDir.Identity(ctx)
	if err != nil || targetParentIdentity != (sourcefs.DirectoryIdentity{Platform: replacer.intent.TargetParent.Platform, Volume: replacer.intent.TargetParent.Volume, FileID: replacer.intent.TargetParent.FileID}) {
		return errors.Join(fmt.Errorf("verify managed publication target parent: %w", err), errors.New("managed publication target parent identity changed"))
	}
	targetMutation, ok := replacer.targetDir.(sourcefs.OutputMutation)
	if !ok {
		return errors.New("native managed publication mutation capability is unavailable")
	}
	atomicReplace, ok := replacer.targetDir.(sourcefs.OutputAtomicReplace)
	if !ok {
		return errors.New("native managed publication replacement capability is unavailable")
	}
	stageDirectorySync, ok := replacer.stageDir.(sourcefs.OutputDirectorySync)
	if !ok {
		return errors.New("managed publication stage directory sync capability is unavailable")
	}
	backupDirectorySync, ok := replacer.stage.directory.(sourcefs.OutputDirectorySync)
	if !ok {
		return errors.New("managed publication backup directory sync capability is unavailable")
	}
	outputRootSync, ok := replacer.root.(sourcefs.OutputDirectorySync)
	if !ok {
		return errors.New("managed publication output root sync capability is unavailable")
	}
	targetDirectorySync, ok := replacer.targetDir.(sourcefs.OutputDirectorySync)
	if !ok {
		return errors.New("managed publication target directory sync capability is unavailable")
	}

	target, err := targetMutation.OpenExistingRegular(ctx, replacer.targetName)
	if err != nil {
		return fmt.Errorf("open expected managed publication target: %w", err)
	}
	targetInfo, err := target.Stat(ctx)
	if err != nil {
		_ = target.Close()
		return fmt.Errorf("stat expected managed publication target: %w", err)
	}
	identity, err := managedReplacementIdentity(ctx, target, targetInfo)
	closeErr := target.Close()
	if err != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("verify expected managed publication target: %w", err), closeErr)
	}
	if !samePublicationFileIdentity(identity, replacer.stage.rewriteIdentity) || identity.MTime == nil || replacer.stage.rewriteIdentity.MTime == nil || *identity.MTime != *replacer.stage.rewriteIdentity.MTime {
		return errors.New("managed publication target no longer matches the frozen current snapshot")
	}

	// The prepared input is the backup only when it is itself an owned frozen
	// copy and independently re-hashes to the frozen current-file digest.
	if replacer.stage.sourceFileInfo == nil || !replacer.stage.sourceFileInfo.Mode().IsRegular() || replacer.stage.sourceFileInfo.Size() != *identity.SizeBytes {
		return errors.New("owned managed publication backup does not match current target size")
	}
	stageMutation, ok := replacer.stage.directory.(sourcefs.OutputMutation)
	if !ok {
		return errors.New("native managed publication backup read capability is unavailable")
	}
	backup, err := stageMutation.OpenExistingRegular(ctx, replacer.stage.inputName)
	if err != nil {
		return fmt.Errorf("open owned managed publication backup: %w", err)
	}
	backupInfo, statErr := backup.Stat(ctx)
	backupIdentity, hashErr := managedReplacementIdentity(ctx, backup, backupInfo)
	backupCloseErr := backup.Close()
	if statErr != nil || hashErr != nil || backupCloseErr != nil {
		return errors.Join(fmt.Errorf("verify owned managed publication backup: %w", errors.Join(statErr, hashErr)), backupCloseErr)
	}
	if !samePublicationFileIdentity(identity, backupIdentity) {
		return errors.New("owned managed publication backup is not byte-identical to current target")
	}
	backup, err = stageMutation.OpenExistingRegular(ctx, replacer.stage.inputName)
	if err != nil {
		return fmt.Errorf("reopen owned managed publication backup for sync: %w", err)
	}
	backupSyncErr := syncManagedRegularFile(ctx, backup)
	backupCloseErr = backup.Close()
	if backupSyncErr != nil || backupCloseErr != nil {
		return errors.Join(fmt.Errorf("sync managed publication backup contents: %w", backupSyncErr), backupCloseErr)
	}
	if err := backupDirectorySync.SyncDirectory(ctx); err != nil {
		return fmt.Errorf("sync managed publication backup directory: %w", err)
	}
	if err := stageDirectorySync.SyncDirectory(ctx); err != nil {
		return fmt.Errorf("sync managed publication stage directory: %w", err)
	}
	if err := outputRootSync.SyncDirectory(ctx); err != nil {
		return fmt.Errorf("sync managed publication stage owning parent directory: %w", err)
	}
	backupPath := path.Join(replacer.stage.directoryName, replacer.stage.inputName)
	evidence := persistence.PublicationReplacementEvidence{Version: 1, Kind: "publication_replacement", From: "exposing", To: "backup_intent", Guard: replacer.guard, PositionID: replacer.guard.ExpectedPositionID, Epoch: replacer.guard.Epoch, CandidateGeneration: replacer.guard.CandidateGeneration, ExpectedGeneration: replacer.expectedGeneration, Intent: replacer.intent, Staged: replacer.validated.Clone(), Identity: identity, BackupIdentity: backupIdentity, BackupPath: backupPath}
	// From the first attempted durable backup-intent transition onward, even a
	// panic or ambiguous DB result must prevent legacy finalization clearing the
	// execution fence.
	mutationMayHaveStarted := true
	defer func() {
		if mutationMayHaveStarted {
			replacer.scope.quarantineUncertain()
		}
	}()
	if completing {
		sealed, sealErr := replacer.scope.sealReplacementCompletion(reservation)
		if sealErr != nil {
			return sealErr
		}
		replacer.completionToken = sealed
	}
	if err := replacer.record(ctx, evidence); err != nil {
		if completing {
			replacer.scope.quarantineUncertain()
		}
		return err
	}

	evidence.From, evidence.To = "backup_intent", "backup_ready"
	if err := replacer.record(ctx, evidence); err != nil {
		if completing {
			replacer.scope.quarantineUncertain()
		}
		return err
	}
	evidence.From, evidence.To = "backup_ready", "replacement_intent"
	if err := replacer.record(ctx, evidence); err != nil {
		if completing {
			replacer.scope.quarantineUncertain()
		}
		return err
	}
	candidate, err := stageMutation.OpenExistingRegular(ctx, filepath.Base(replacer.stage.paths.OutputPath))
	if err != nil {
		return fmt.Errorf("open validated managed publication candidate: %w", err)
	}
	candidateInfo, err := candidate.Stat(ctx)
	if err == nil {
		var candidateIdentity persistence.PublicationFileIdentity
		candidateIdentity, err = managedReplacementIdentity(ctx, candidate, candidateInfo)
		if err == nil && candidateIdentity.SHA256 != replacer.validated.PayloadSHA256 {
			err = errors.New("managed publication candidate changed after validation")
		}
	}
	candidateCloseErr := candidate.Close()
	if err != nil || candidateCloseErr != nil {
		return errors.Join(fmt.Errorf("verify managed publication candidate: %w", err), candidateCloseErr)
	}
	if candidateInfo.Size() != *replacer.validated.Ownership.ExpectedSize || candidateInfo.ModTime().UnixMicro() != *replacer.validated.Ownership.ExpectedMTime {
		return errors.New("managed publication candidate ownership changed after validation")
	}
	candidate, err = stageMutation.OpenExistingRegular(ctx, filepath.Base(replacer.stage.paths.OutputPath))
	if err != nil {
		return fmt.Errorf("reopen validated managed publication candidate for sync: %w", err)
	}
	candidateSyncErr := syncManagedRegularFile(ctx, candidate)
	candidateCloseErr = candidate.Close()
	if candidateSyncErr != nil || candidateCloseErr != nil {
		return errors.Join(fmt.Errorf("sync managed publication candidate contents: %w", candidateSyncErr), candidateCloseErr)
	}
	if err := replacer.root.Verify(ctx); err != nil {
		return fmt.Errorf("revalidate managed publication output root before replacement: %w", err)
	}
	if err := replacer.root.VerifyChild(ctx, replacer.stage.directoryName, sourcefs.DirectoryIdentity{Platform: replacer.stage.identity.Platform, Volume: replacer.stage.identity.Volume, FileID: replacer.stage.identity.FileID}); err != nil {
		return fmt.Errorf("revalidate managed publication stage directory before replacement: %w", err)
	}
	if err := verifyManagedTargetParent(ctx, replacer.root, targetParentPath, targetParentIdentity); err != nil {
		return fmt.Errorf("revalidate managed publication target parent before replacement: %w", err)
	}
	if id, err := replacer.stageDir.Identity(ctx); err != nil || id != stageDirIdentity {
		return errors.Join(fmt.Errorf("revalidate managed publication stage identity before replacement: %w", err), errors.New("managed publication stage directory identity changed"))
	}
	if id, err := replacer.targetDir.Identity(ctx); err != nil || id != targetParentIdentity {
		return errors.Join(fmt.Errorf("revalidate managed publication target parent before replacement: %w", err), errors.New("managed publication target parent identity changed"))
	}
	if err := atomicReplace.AtomicReplaceExisting(ctx, replacer.stageDir, filepath.Base(replacer.stage.paths.OutputPath), candidateInfo, replacer.targetName, targetInfo); err != nil {
		return fmt.Errorf("atomically replace managed publication target: %w", err)
	}
	if err := targetDirectorySync.SyncDirectory(ctx); err != nil {
		return fmt.Errorf("sync installed managed publication target parent directory: %w", err)
	}
	if stageDirIdentity != targetParentIdentity {
		if err := stageDirectorySync.SyncDirectory(ctx); err != nil {
			return fmt.Errorf("sync managed publication source parent directory: %w", err)
		}
	}

	installed, err := targetMutation.OpenExistingRegular(ctx, replacer.targetName)
	if err != nil {
		return fmt.Errorf("read back installed managed publication target: %w", err)
	}
	installedInfo, statErr := installed.Stat(ctx)
	installedIdentity, hashErr := managedReplacementIdentity(ctx, installed, installedInfo)
	installedCloseErr := installed.Close()
	if statErr != nil || hashErr != nil || installedCloseErr != nil {
		return errors.Join(fmt.Errorf("verify installed managed publication target: %w", errors.Join(statErr, hashErr)), installedCloseErr)
	}
	if installedIdentity.SizeBytes == nil || *installedIdentity.SizeBytes != *replacer.validated.Ownership.ExpectedSize || installedIdentity.SHA256 != replacer.validated.PayloadSHA256 {
		return errors.New("installed managed publication target does not match validated candidate")
	}
	evidence.From, evidence.To = "replacement_intent", "installed"
	evidence.Identity = installedIdentity
	if err := replacer.record(ctx, evidence); err != nil {
		if completing {
			replacer.scope.quarantineUncertain()
		}
		return err
	}
	if completing {
		mutationMayHaveStarted = false
		return nil
	}
	return errors.New("managed publication replacement is installed and awaits phase-two completion")
}

// verifyManagedTargetParent walks every portable relative-path component from
// the pinned output root, comparing identities only after reaching the target.
func verifyManagedTargetParent(ctx context.Context, root sourcefs.OutputDirectory, relative string, expected sourcefs.DirectoryIdentity) error {
	if relative == "." {
		actual, err := root.Identity(ctx)
		if err != nil {
			return err
		}
		if actual != expected {
			return errors.New("managed publication target parent identity changed")
		}
		return nil
	}
	if relative == "" || path.IsAbs(relative) || path.Clean(relative) != relative {
		return errors.New("managed publication target parent path is invalid")
	}
	current := root
	owned := false
	for _, component := range strings.Split(relative, "/") {
		if component == "" || component == "." || component == ".." || strings.ContainsAny(component, `\\:`) {
			if owned {
				_ = current.Close()
			}
			return errors.New("managed publication target parent path is invalid")
		}
		traversal, ok := current.(sourcefs.OutputDirectoryTraversal)
		if !ok {
			if owned {
				_ = current.Close()
			}
			return errors.New("managed publication target parent read-only traversal capability is unavailable")
		}
		next, err := traversal.OpenExistingDir(ctx, component)
		if owned {
			if closeErr := current.Close(); closeErr != nil {
				if err == nil {
					_ = next.Close()
				}
				return closeErr
			}
		}
		if err != nil {
			return err
		}
		current, owned = next, true
	}
	defer func() { _ = current.Close() }()
	actual, err := current.Identity(ctx)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("managed publication target parent identity changed")
	}
	return nil
}

func (replacer *newPublicationManagedTargetReplacer) record(ctx context.Context, evidence persistence.PublicationReplacementEvidence) error {
	outcome, err := replacer.repository.RecordPublicationReplacement(context.WithoutCancel(ctx), replacer.guard, evidence)
	if err != nil || outcome != persistence.PublicationReplacementRecorded {
		return errors.Join(err, fmt.Errorf("managed publication replacement transition %s was not acknowledged", evidence.To))
	}
	return nil
}

func managedReplacementIdentity(ctx context.Context, file sourcefs.RegularFile, info fs.FileInfo) (persistence.PublicationFileIdentity, error) {
	if info == nil || !info.Mode().IsRegular() || info.Size() < 0 {
		return persistence.PublicationFileIdentity{}, errors.New("managed publication replacement file is not regular")
	}
	digest := sha256.New()
	err := file.Borrow(ctx, func(open *os.File) error {
		if _, err := open.Seek(0, io.SeekStart); err != nil {
			return err
		}
		_, err := io.Copy(digest, open)
		return err
	})
	if err != nil {
		return persistence.PublicationFileIdentity{}, err
	}
	return persistence.PublicationFileIdentity{SizeBytes: replacementInt64Pointer(info.Size()), MTime: replacementInt64Pointer(info.ModTime().UnixMicro()), SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

func syncManagedRegularFile(ctx context.Context, file sourcefs.RegularFile) error {
	syncer, ok := file.(sourcefs.RegularFileSync)
	if !ok {
		return fmt.Errorf("native managed publication file sync capability is unavailable: %w", sourcefs.ErrUnsupported)
	}
	return syncer.Sync(ctx)
}

func replacementInt64Pointer(value int64) *int64 { return &value }

func validReplacementDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

func samePublicationFileIdentity(left, right persistence.PublicationFileIdentity) bool {
	return left.SizeBytes != nil && right.SizeBytes != nil && *left.SizeBytes == *right.SizeBytes && left.SHA256 != "" && left.SHA256 == right.SHA256
}
