package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type preparedManualLyricsExposureRepository interface {
	ReadPublicationAttempt(context.Context, uuid.UUID) (*persistence.TrackPublicationAttempt, error)
	BeginManualLyricsExposure(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationExposureIntent, persistence.PublicationStageObservation) (persistence.PublicationExposureOutcome, error)
}

type frozenPublicationNamespace struct {
	Version int                                    `json:"version"`
	Nodes   []persistence.PublicationNamespaceNode `json:"nodes"`
}

// admitPreparedManualLyricsExposure derives the exposure request solely from
// the guarded frozen attempt and the preparation owner retained by the stage.
// It deliberately performs no replacement or recovery work.
func admitPreparedManualLyricsExposure(ctx context.Context, scope *PublicationExecutionScope, work *privateManualLyricsWork, acknowledged persistence.PublicationStageObservation) error {
	if scope == nil || work == nil || work.stage == nil || !scope.validPrivateManualLyricsWork(work, work.stage) {
		return errors.New("manual lyrics exposure requires its exact private work capability")
	}
	stage := work.stage
	repository, ok := stage.repository.(preparedManualLyricsExposureRepository)
	if !ok {
		return errors.New("manual lyrics exposure repository is unavailable")
	}
	attempt, err := repository.ReadPublicationAttempt(ctx, scope.guard.OperationID)
	if err != nil {
		return fmt.Errorf("read frozen publication attempt for manual lyrics exposure: %w", err)
	}
	if attempt == nil || attempt.OperationID != scope.guard.OperationID || attempt.Phase != "validated" {
		return errors.New("manual lyrics publication attempt is not validated")
	}
	var inputs persistence.PublicationInputs
	if err := strictExposureJSON(attempt.FrozenInputs, &inputs); err != nil || inputs.Version != persistence.PublicationInputsVersion3 || inputs.Rewrite == nil {
		return errors.New("frozen manual lyrics publication inputs are invalid")
	}
	var manifest frozenPublicationNamespace
	if err := strictExposureJSON(attempt.NamespaceManifest, &manifest); err != nil || manifest.Version != 1 {
		return errors.New("frozen publication namespace manifest is invalid")
	}
	target := inputs.Rewrite.RelativePath
	var node persistence.PublicationNamespaceNode
	matches := 0
	for _, candidate := range manifest.Nodes {
		if candidate.NodeKind == "file" && candidate.RelativePath == target {
			node = candidate
			matches++
		}
	}
	if matches != 1 {
		return errors.New("frozen publication namespace must identify exactly one rewrite target")
	}
	if target == "" || node.RelativePath != target || node.CanonicalKey == "" || !portableExposureTarget(target) {
		return errors.New("frozen publication namespace does not identify the rewrite target")
	}
	ready := stage.preparationRecord
	if ready.Step != "input_ready" || ready.OwnerToken == uuid.Nil || ready.OutputRel != acknowledged.RelativePath || ready.ParentIdentity != persistenceDirectoryIdentity(stage.parentIdentity) {
		return errors.New("manual lyrics preparation owner does not match staged acknowledgement")
	}
	if err := stage.verifyInitialCopyInputs(ctx); err != nil {
		return err
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return err
	}
	if err := stage.parent.Verify(ctx); err != nil {
		return err
	}
	rootID, err := stage.parent.Identity(ctx)
	if err != nil || rootID != stage.parentIdentity {
		return errors.Join(fmt.Errorf("verify manual lyrics output root identity: %w", err), errors.New("manual lyrics output root identity changed"))
	}
	currentRoot, err := os.Stat(stage.sourceRootPath)
	if err != nil {
		return fmt.Errorf("stat current manual lyrics source root: %w", err)
	}
	pinnedRoot, err := stage.opener.OpenRoot(ctx, stage.sourceRootPath)
	if err != nil {
		return fmt.Errorf("open pinned manual lyrics source root: %w", err)
	}
	pinnedRootInfo, rootStatErr := pinnedRoot.Stat(ctx)
	outputRootInfo, outputRootStatErr := stage.parent.Stat(ctx)
	rootStatErr = errors.Join(rootStatErr, outputRootStatErr)
	if rootStatErr == nil && (pinnedRootInfo == nil || outputRootInfo == nil || !os.SameFile(pinnedRootInfo, outputRootInfo)) {
		rootStatErr = errors.New("manual lyrics source root differs from the pinned output root")
	}
	if rootStatErr == nil && (pinnedRootInfo == nil || !os.SameFile(currentRoot, pinnedRootInfo)) {
		rootStatErr = errors.New("manual lyrics source root changed")
	}
	if rootStatErr == nil {
		pinnedIdentity, identityErr := sourcefsDirectoryIdentifier(ctx, pinnedRoot)
		currentIdentityRoot, currentIdentityErr := stage.opener.OpenRoot(ctx, stage.sourceRootPath)
		var currentIdentity persistence.PublicationDirectoryIdentity
		if currentIdentityErr == nil {
			currentIdentity, currentIdentityErr = sourcefsDirectoryIdentifier(ctx, currentIdentityRoot)
			currentIdentityErr = errors.Join(currentIdentityErr, currentIdentityRoot.Close())
		}
		rootStatErr = errors.Join(identityErr, currentIdentityErr)
		if rootStatErr == nil && pinnedIdentity != currentIdentity {
			rootStatErr = errors.New("manual lyrics source root identity changed")
		}
	}
	rootStatErr = errors.Join(rootStatErr, pinnedRoot.Close())
	if rootStatErr != nil {
		return rootStatErr
	}
	current, err := stage.opener.OpenRoot(ctx, stage.sourceRootPath)
	if err != nil {
		return err
	}
	currentFile, currentErr := sourcefs.OpenRegularAt(ctx, current, stage.sourceRelativePath)
	if currentErr == nil {
		info, infoErr := currentFile.Stat(ctx)
		if infoErr == nil && (info.Size() != *stage.rewriteIdentity.SizeBytes || info.ModTime().UnixMicro() != *stage.rewriteIdentity.MTime) {
			infoErr = errors.New("current manual lyrics source identity changed")
		}
		if infoErr == nil {
			var digest [32]byte
			infoErr = currentFile.Borrow(ctx, func(file *os.File) error {
				var hashErr error
				digest, hashErr = hashBorrowedFile(ctx, file)
				return hashErr
			})
			if infoErr == nil && hex.EncodeToString(digest[:]) != stage.rewriteIdentity.SHA256 {
				infoErr = errors.New("current manual lyrics source digest changed")
			}
		}
		currentErr = errors.Join(infoErr, currentFile.Close())
	}
	currentErr = errors.Join(currentErr, current.Close())
	if currentErr != nil {
		return currentErr
	}
	parentRel := path.Dir(target)
	parent, owned, err := openExposureTargetParent(ctx, stage.parent, parentRel)
	if err != nil {
		return err
	}
	parentClose := func() error {
		if owned {
			return parent.Close()
		}
		return nil
	}
	parentID, err := parent.Identity(ctx)
	if err != nil {
		return errors.Join(err, parentClose())
	}
	mutation, ok := parent.(sourcefs.OutputMutation)
	if !ok {
		return errors.Join(errors.New("manual lyrics target native read capability is unavailable"), parentClose())
	}
	targetFile, err := mutation.OpenExistingRegular(ctx, path.Base(target))
	if err != nil {
		return errors.Join(fmt.Errorf("open current manual lyrics target: %w", err), parentClose())
	}
	targetInfo, statErr := targetFile.Stat(ctx)
	var targetNativeInfo os.FileInfo
	if statErr == nil {
		targetNativeInfo = targetInfo
	}
	var digest [32]byte
	if statErr == nil && (targetInfo == nil || !targetInfo.Mode().IsRegular() || stage.rewriteIdentity.SizeBytes == nil || stage.rewriteIdentity.MTime == nil || targetInfo.Size() != *stage.rewriteIdentity.SizeBytes || targetInfo.ModTime().UnixMicro() != *stage.rewriteIdentity.MTime) {
		statErr = errors.New("current manual lyrics target differs from frozen ownership")
	}
	if statErr == nil {
		statErr = targetFile.Borrow(ctx, func(file *os.File) error {
			var hashErr error
			digest, hashErr = hashBorrowedFile(ctx, file)
			return hashErr
		})
	}
	if statErr == nil && hex.EncodeToString(digest[:]) != stage.rewriteIdentity.SHA256 {
		statErr = errors.New("current manual lyrics target digest differs from frozen ownership")
	}
	statErr = errors.Join(statErr, targetFile.Close())
	if statErr != nil {
		return errors.Join(statErr, parentClose())
	}
	checkParent, checkOwned, err := openExposureTargetParent(ctx, stage.parent, parentRel)
	if err != nil {
		return errors.Join(fmt.Errorf("revalidate manual lyrics target namespace: %w", err), parentClose())
	}
	checkID, checkErr := checkParent.Identity(ctx)
	if checkOwned {
		checkErr = errors.Join(checkErr, checkParent.Close())
	}
	if checkErr != nil || checkID != parentID {
		return errors.Join(errors.New("manual lyrics target parent changed during admission"), checkErr, parentClose())
	}
	// Reopen by name relative to the anchored parent after hashing. The earlier
	// descriptor proves what was hashed; this descriptor proves that the same
	// regular file still occupies the admitted name.
	currentTarget, err := mutation.OpenExistingRegular(ctx, path.Base(target))
	if err != nil {
		return errors.Join(fmt.Errorf("reopen current manual lyrics target: %w", err), parentClose())
	}
	currentTargetInfo, verifyErr := currentTarget.Stat(ctx)
	if verifyErr == nil && (currentTargetInfo == nil || targetNativeInfo == nil || !os.SameFile(targetNativeInfo, currentTargetInfo) || currentTargetInfo.Size() != targetNativeInfo.Size() || !currentTargetInfo.ModTime().Equal(targetNativeInfo.ModTime())) {
		verifyErr = errors.New("manual lyrics target namespace changed after hashing")
	}
	verifyErr = errors.Join(verifyErr, currentTarget.Close())
	if verifyErr != nil {
		return errors.Join(verifyErr, parentClose())
	}
	// Recheck both the source name and its contents after the final target hash,
	// immediately before the database-only admission.
	if err := stage.verifyInitialCopyInputs(ctx); err != nil {
		return errors.Join(err, parentClose())
	}
	finalRootInfo, err := os.Stat(stage.sourceRootPath)
	if err != nil || finalRootInfo == nil || !os.SameFile(outputRootInfo, finalRootInfo) {
		return errors.Join(errors.New("manual lyrics source root namespace changed before admission"), err, parentClose())
	}
	// Admission itself is the only durable action. The repository rechecks the
	// live delivery and current publication snapshot transactionally.
	intent := persistence.PublicationExposureIntent{
		Version: persistence.PublicationExposureVersion, StagedPath: ready.OutputRel,
		TargetCanonicalKey: node.CanonicalKey, TargetRelativePath: target,
		Expectation: "expected_current", PrepOwner: ready.OwnerToken,
		OutputRootParent: persistenceDirectoryIdentity(rootID),
		TargetParent:     persistenceDirectoryIdentity(parentID),
	}
	if err := parentClose(); err != nil {
		return fmt.Errorf("close manual lyrics target parent: %w", err)
	}
	ack := acknowledged.Clone()
	outcome, err := repository.BeginManualLyricsExposure(ctx, scope.guard, intent, ack)
	if err != nil {
		return err
	}
	if outcome != persistence.PublicationExposureRecorded && outcome != persistence.PublicationExposureAlreadyRecorded {
		return errors.New("publication delivery is stale while admitting manual lyrics exposure")
	}
	return nil
}

func strictExposureJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func portableExposureTarget(value string) bool {
	if value == "" || strings.ContainsAny(value, `\\:`) || strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func openExposureTargetParent(ctx context.Context, root sourcefs.OutputDirectory, relative string) (sourcefs.OutputDirectory, bool, error) {
	if relative == "." {
		return root, false, nil
	}
	if !portableExposureTarget(relative) {
		return nil, false, errors.New("manual lyrics target parent path is unsafe")
	}
	current := root
	owned := false
	for _, component := range strings.Split(relative, "/") {
		traversal, ok := current.(sourcefs.OutputDirectoryTraversal)
		if !ok {
			err := errors.New("manual lyrics target parent traversal is unavailable")
			if owned {
				err = errors.Join(err, current.Close())
			}
			return nil, false, err
		}
		next, err := traversal.OpenExistingDir(ctx, component)
		if owned {
			if closeErr := current.Close(); closeErr != nil {
				if next != nil {
					err = errors.Join(err, next.Close())
				}
				return nil, false, errors.Join(err, closeErr)
			}
		}
		if err != nil {
			return nil, false, err
		}
		current, owned = next, true
	}
	return current, owned, nil
}
