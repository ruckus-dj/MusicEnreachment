package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type absentTargetInstallationRepository interface {
	BeginPublicationExposure(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationExposureIntent, persistence.PublicationStageObservation) (persistence.PublicationExposureOutcome, error)
	RecordPublicationCandidateInstalled(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationExposureIntent, persistence.PublicationStageObservation, persistence.PublicationCandidateInstalledObservation) (persistence.PublicationCandidateInstalledOutcome, error)
}

// absentTargetInstaller only performs one guarded absent-target exposure. The
// directory handles are retained capabilities; this is not inter-process proof.
type absentTargetInstaller struct {
	scope                                       *PublicationExecutionScope
	repository                                  absentTargetInstallationRepository
	guard                                       persistence.PublicationDeliveryGuard
	intent                                      persistence.PublicationExposureIntent
	validated                                   persistence.PublicationStageObservation
	stage                                       PreparedPublicationStage
	root, stageDir, targetParent                sourcefs.OutputDirectory
	stageName, targetName                       string
	rootIdentity, stageIdentity, targetIdentity sourcefs.DirectoryIdentity
}

func newAbsentTargetInstaller(scope *PublicationExecutionScope, repository absentTargetInstallationRepository, guard persistence.PublicationDeliveryGuard, intent persistence.PublicationExposureIntent, validated persistence.PublicationStageObservation, stage PreparedPublicationStage, root, stageDir, targetParent sourcefs.OutputDirectory, stageName, targetName string) (*absentTargetInstaller, error) {
	if scope == nil || repository == nil || stage == nil || root == nil || stageDir == nil || targetParent == nil || scope.guard != guard || intent.Expectation != "absent" || intent.TargetRelativePath == "" || intent.StagedPath == "" || strings.ContainsAny(stageName+targetName, "/\\") || stageName == "" || targetName == "" {
		return nil, errors.New("invalid absent-target publication installer dependencies")
	}
	return &absentTargetInstaller{scope: scope, repository: repository, guard: guard, intent: intent, validated: validated.Clone(), stage: stage, root: root, stageDir: stageDir, targetParent: targetParent, stageName: stageName, targetName: targetName}, nil
}

// Install serializes the whole operation against Run and Finalize. It never
// resumes or adopts an existing target after an ambiguous exposure transition.
func (installer *absentTargetInstaller) Install(ctx context.Context) (resultErr error) {
	scope := installer.scope
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.finalized || scope.finalizing || scope.unknown || scope.guard != installer.guard {
		return errors.New("publication execution scope is not installable")
	}
	if scope.unresolved != 0 {
		return errors.New("cannot install publication candidate with unresolved command execution")
	}
	uncertain := false
	defer func() {
		if uncertain {
			scope.unknown = true
			scope.quarantineLocked()
		}
	}()
	if err := installer.stage.Validate(ctx); err != nil {
		return fmt.Errorf("validate publication stage: %w", err)
	}
	if err := installer.verifyBindings(ctx); err != nil {
		return err
	}
	mutation, ok := installer.stageDir.(sourcefs.OutputMutation)
	if !ok {
		return errors.New("publication stage does not support native mutation")
	}
	targetMutation, ok := installer.targetParent.(sourcefs.OutputMutation)
	if !ok {
		return errors.New("publication target does not support native mutation")
	}
	candidate, observation, err := installer.inspectCandidate(ctx)
	if err != nil {
		return err
	}
	exposureStarted := false
	defer func() {
		if err := candidate.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close publication candidate: %w", err))
			if exposureStarted {
				uncertain = true
			}
		}
	}()
	outcome, err := installer.repository.BeginPublicationExposure(ctx, installer.guard, installer.intent, installer.validated)
	if err != nil || outcome != persistence.PublicationExposureRecorded {
		return errors.Join(err, fmt.Errorf("publication exposure was not newly recorded: %s", outcome))
	}
	exposureStarted = true
	uncertain = true
	if scope.stageClose == nil {
		scope.stageClose = installer.stage.Close
	}
	if err := installer.verifyBindings(ctx); err != nil {
		return err
	}
	if err := mutation.RenameNoReplace(ctx, installer.stageDir, installer.stageName, installer.targetParent, installer.targetName); err != nil {
		return fmt.Errorf("install publication candidate without replacement: %w", err)
	}
	installed, err := targetMutation.OpenExistingRegular(ctx, installer.targetName)
	if err != nil {
		return fmt.Errorf("open installed publication candidate: %w", err)
	}
	defer func() {
		if err := installed.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close installed publication candidate: %w", err))
			uncertain = true
		}
	}()
	if err := sameRegularFile(ctx, candidate, installed); err != nil {
		return err
	}
	actual, err := inspectRegular(ctx, installed)
	if err != nil {
		return err
	}
	if actual.SizeBytes == nil || *actual.SizeBytes != *observation.Identity.SizeBytes || actual.MTime == nil || *actual.MTime != *observation.Identity.MTime || actual.SHA256 != observation.Identity.SHA256 {
		return errors.New("installed publication candidate changed")
	}
	installedObservation := persistence.PublicationCandidateInstalledObservation{Version: persistence.PublicationCandidateInstalledVersion, TargetRelativePath: installer.intent.TargetRelativePath, Identity: actual}
	recorded, recordErr := installer.repository.RecordPublicationCandidateInstalled(ctx, installer.guard, installer.intent, installer.validated, installedObservation)
	if recordErr != nil || recorded != persistence.PublicationCandidateInstalledRecorded {
		return errors.Join(recordErr, fmt.Errorf("installed publication candidate was not newly recorded: %s", recorded))
	}
	uncertain = false
	return nil
}

func (installer *absentTargetInstaller) verifyBindings(ctx context.Context) error {
	rootID, err := installer.root.Identity(ctx)
	if err != nil {
		return err
	}
	stageID, err := installer.stageDir.Identity(ctx)
	if err != nil {
		return err
	}
	targetID, err := installer.targetParent.Identity(ctx)
	if err != nil {
		return err
	}
	if !matchesPublicationDirectoryIdentity(rootID, installer.intent.OutputRootParent) || !matchesPublicationDirectoryIdentity(targetID, installer.intent.TargetParent) {
		return errors.New("publication directory identity differs from exposure intent")
	}
	if installer.rootIdentity == (sourcefs.DirectoryIdentity{}) {
		installer.rootIdentity, installer.stageIdentity, installer.targetIdentity = rootID, stageID, targetID
	} else if rootID != installer.rootIdentity || stageID != installer.stageIdentity || targetID != installer.targetIdentity {
		return errors.New("publication directory capability changed")
	}
	if err := installer.root.Verify(ctx); err != nil {
		return err
	}
	if err := installer.root.VerifyChild(ctx, filepathDir(installer.intent.StagedPath), stageID); err != nil {
		return fmt.Errorf("verify staged publication directory binding: %w", err)
	}
	return nil
}

func (installer *absentTargetInstaller) inspectCandidate(ctx context.Context) (sourcefs.RegularFile, persistence.PublicationCandidateInstalledObservation, error) {
	mutation, ok := installer.stageDir.(sourcefs.OutputMutation)
	if !ok {
		return nil, persistence.PublicationCandidateInstalledObservation{}, errors.New("publication stage does not support native mutation")
	}
	file, err := mutation.OpenExistingRegular(ctx, installer.stageName)
	if err != nil {
		return nil, persistence.PublicationCandidateInstalledObservation{}, err
	}
	identity, err := inspectRegular(ctx, file)
	if err != nil {
		_ = file.Close()
		return nil, persistence.PublicationCandidateInstalledObservation{}, err
	}
	if installer.validated.Ownership.ExpectedSize == nil || installer.validated.Ownership.ExpectedMTime == nil || *identity.SizeBytes != *installer.validated.Ownership.ExpectedSize || *identity.MTime != *installer.validated.Ownership.ExpectedMTime || identity.SHA256 != installer.validated.PayloadSHA256 {
		_ = file.Close()
		return nil, persistence.PublicationCandidateInstalledObservation{}, errors.New("staged publication candidate differs from validated evidence")
	}
	return file, persistence.PublicationCandidateInstalledObservation{Version: persistence.PublicationCandidateInstalledVersion, TargetRelativePath: installer.intent.TargetRelativePath, Identity: identity}, nil
}

func inspectRegular(ctx context.Context, file sourcefs.RegularFile) (persistence.PublicationFileIdentity, error) {
	before, err := file.Stat(ctx)
	if err != nil {
		return persistence.PublicationFileIdentity{}, err
	}
	if !before.Mode().IsRegular() {
		return persistence.PublicationFileIdentity{}, sourcefs.ErrNotRegular
	}
	hash := sha256.New()
	err = file.Borrow(ctx, func(f *os.File) error { _, err := io.Copy(hash, f); return err })
	if err != nil {
		return persistence.PublicationFileIdentity{}, err
	}
	after, err := file.Stat(ctx)
	if err != nil {
		return persistence.PublicationFileIdentity{}, err
	}
	if !after.Mode().IsRegular() || before.Size() != after.Size() || !before.ModTime().Truncate(time.Microsecond).Equal(after.ModTime().Truncate(time.Microsecond)) {
		return persistence.PublicationFileIdentity{}, errors.New("publication candidate changed while hashing")
	}
	size, mtime := after.Size(), after.ModTime().UnixMicro()
	return persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func sameRegularFile(ctx context.Context, left, right sourcefs.RegularFile) error {
	leftInfo, err := left.Stat(ctx)
	if err != nil {
		return err
	}
	rightInfo, err := right.Stat(ctx)
	if err != nil {
		return err
	}
	if !os.SameFile(leftInfo, rightInfo) {
		return errors.New("installed publication target is not the retained candidate")
	}
	return nil
}

func matchesPublicationDirectoryIdentity(actual sourcefs.DirectoryIdentity, expected persistence.PublicationDirectoryIdentity) bool {
	return actual.Platform == expected.Platform && actual.Volume == expected.Volume && actual.FileID == expected.FileID
}

func filepathDir(path string) string {
	path = strings.TrimSuffix(path, "/")
	if index := strings.LastIndex(path, "/"); index >= 0 {
		return path[:index]
	}
	return "."
}
