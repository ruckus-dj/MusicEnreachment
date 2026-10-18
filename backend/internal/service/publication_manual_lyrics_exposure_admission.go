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

// privateManualLyricsExposureHandoff is the retained native authority produced
// only by this admission path. Its pointer identity is intentionally private.
type privateManualLyricsExposureHandoff struct {
	scope           *PublicationExecutionScope
	work            *privateManualLyricsWork
	stage           *preparedPublicationStage
	guard           persistence.PublicationDeliveryGuard
	inputs          persistence.PublicationInputs
	intent          persistence.PublicationExposureIntent
	observation     persistence.PublicationStageObservation
	root            sourcefs.OutputDirectory
	parent          sourcefs.OutputDirectory
	parentPath      string
	parentIdentity  sourcefs.DirectoryIdentity
	ownsParent      bool
	targetInfo      os.FileInfo
	targetSHA256    string
	candidateInfo   os.FileInfo
	candidateSHA256 string
}

func (handoff *privateManualLyricsExposureHandoff) Close() error {
	if handoff == nil || !handoff.ownsParent || handoff.parent == nil {
		return nil
	}
	parent := handoff.parent
	handoff.parent = nil
	handoff.ownsParent = false
	return parent.Close()
}

// Bind confirms this exact retained handoff still belongs to the live private
// work and its frozen inputs. It does not transfer or extend scope ownership.
func (handoff *privateManualLyricsExposureHandoff) Bind(scope *PublicationExecutionScope, work *privateManualLyricsWork) error {
	if handoff == nil || scope == nil || work == nil || handoff.scope != scope || handoff.work != work ||
		handoff.stage != work.stage || handoff.guard != scope.guard || !scope.validPrivateManualLyricsWork(work, handoff.stage) {
		return errors.New("manual lyrics exposure handoff is not bound to the active private work")
	}
	return nil
}

// RevalidateNamespace is PRE-REPLACEMENT-ONLY: it proves the retained parent
// and frozen G0 target while the old target name still exists. Callers must
// not invoke it after installation, when the G0 name has intentionally changed.
func (handoff *privateManualLyricsExposureHandoff) RevalidateNamespace(ctx context.Context) error {
	if handoff == nil || handoff.parent == nil || handoff.scope == nil || handoff.work == nil ||
		handoff.Bind(handoff.scope, handoff.work) != nil {
		return errors.New("manual lyrics exposure handoff is closed")
	}
	if err := handoff.stage.verifyInitialCopyInputs(ctx); err != nil {
		return err
	}
	if err := verifyManagedTargetParent(ctx, handoff.root, handoff.parentPath, handoff.parentIdentity); err != nil {
		return err
	}
	currentRoot, err := os.Stat(handoff.stage.sourceRootPath)
	if err != nil {
		return fmt.Errorf("stat pinned manual lyrics root namespace before replacement: %w", err)
	}
	pinnedRoot, err := handoff.root.Stat(ctx)
	if err != nil {
		return err
	}
	if currentRoot == nil || pinnedRoot == nil || !os.SameFile(currentRoot, pinnedRoot) {
		return errors.New("manual lyrics root namespace changed before replacement")
	}
	mutation, ok := handoff.parent.(sourcefs.OutputMutation)
	if !ok {
		return errors.New("manual lyrics target native read capability is unavailable")
	}
	file, err := mutation.OpenExistingRegular(ctx, path.Base(handoff.intent.TargetRelativePath))
	if err != nil {
		return err
	}
	info, statErr := file.Stat(ctx)
	var digest [32]byte
	if statErr == nil && (info == nil || handoff.targetInfo == nil || !os.SameFile(info, handoff.targetInfo) ||
		info.Size() != handoff.targetInfo.Size() || !info.ModTime().Equal(handoff.targetInfo.ModTime())) {
		statErr = errors.New("manual lyrics target identity changed before replacement")
	}
	if statErr == nil {
		statErr = file.Borrow(ctx, func(native *os.File) error {
			var hashErr error
			digest, hashErr = hashBorrowedFile(ctx, native)
			return hashErr
		})
	}
	if statErr == nil && hex.EncodeToString(digest[:]) != handoff.targetSHA256 {
		statErr = errors.New("manual lyrics target digest changed before replacement")
	}
	statErr = errors.Join(statErr, file.Close())
	if statErr != nil {
		return statErr
	}
	reopened, err := mutation.OpenExistingRegular(ctx, path.Base(handoff.intent.TargetRelativePath))
	if err != nil {
		return err
	}
	reopenedInfo, reopenErr := reopened.Stat(ctx)
	if reopenErr == nil && (reopenedInfo == nil || !os.SameFile(handoff.targetInfo, reopenedInfo) ||
		reopenedInfo.Size() != handoff.targetInfo.Size() || !reopenedInfo.ModTime().Equal(handoff.targetInfo.ModTime())) {
		reopenErr = errors.New("manual lyrics target name changed after hashing")
	}
	statErr = errors.Join(reopenErr, reopened.Close())
	return statErr
}

// admitPreparedManualLyricsExposure derives the exposure request solely from
// the guarded frozen attempt and the preparation owner retained by the stage.
// It deliberately performs no replacement or recovery work.
func admitPreparedManualLyricsExposure(ctx context.Context, scope *PublicationExecutionScope, work *privateManualLyricsWork, acknowledged persistence.PublicationStageObservation) error {
	handoff, err := prepareAndAdmitPrivateManualLyricsExposure(ctx, scope, work, acknowledged)
	if err != nil {
		return err
	}
	return handoff.Close()
}

func prepareAndAdmitPrivateManualLyricsExposure(ctx context.Context, scope *PublicationExecutionScope, work *privateManualLyricsWork, acknowledged persistence.PublicationStageObservation) (result *privateManualLyricsExposureHandoff, resultErr error) {
	if scope == nil || work == nil || work.stage == nil || !scope.validPrivateManualLyricsWork(work, work.stage) {
		return nil, errors.New("manual lyrics exposure requires its exact private work capability")
	}
	stage := work.stage
	repository, ok := stage.repository.(preparedManualLyricsExposureRepository)
	if !ok {
		return nil, errors.New("manual lyrics exposure repository is unavailable")
	}
	attempt, err := repository.ReadPublicationAttempt(ctx, scope.guard.OperationID)
	if err != nil {
		return nil, fmt.Errorf("read frozen publication attempt for manual lyrics exposure: %w", err)
	}
	if attempt == nil || attempt.OperationID != scope.guard.OperationID || attempt.Phase != "validated" {
		return nil, errors.New("manual lyrics publication attempt is not validated")
	}
	var inputs persistence.PublicationInputs
	if err := strictExposureJSON(attempt.FrozenInputs, &inputs); err != nil || inputs.Version != persistence.PublicationInputsVersion3 || inputs.Rewrite == nil {
		return nil, errors.New("frozen manual lyrics publication inputs are invalid")
	}
	inputs = inputs.Clone()
	var manifest frozenPublicationNamespace
	if err := strictExposureJSON(attempt.NamespaceManifest, &manifest); err != nil || manifest.Version != 1 {
		return nil, errors.New("frozen publication namespace manifest is invalid")
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
		return nil, errors.New("frozen publication namespace must identify exactly one rewrite target")
	}
	if target == "" || node.RelativePath != target || node.CanonicalKey == "" || !portableExposureTarget(target) {
		return nil, errors.New("frozen publication namespace does not identify the rewrite target")
	}
	ready := stage.preparationRecord
	if ready.Step != "input_ready" || ready.OwnerToken == uuid.Nil || ready.OutputRel != acknowledged.RelativePath || ready.ParentIdentity != persistenceDirectoryIdentity(stage.parentIdentity) {
		return nil, errors.New("manual lyrics preparation owner does not match staged acknowledgement")
	}
	if err := stage.verifyInitialCopyInputs(ctx); err != nil {
		return nil, err
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return nil, err
	}
	if err := stage.parent.Verify(ctx); err != nil {
		return nil, err
	}
	rootID, err := stage.parent.Identity(ctx)
	if err != nil || rootID != stage.parentIdentity {
		return nil, errors.Join(fmt.Errorf("verify manual lyrics output root identity: %w", err), errors.New("manual lyrics output root identity changed"))
	}
	currentRoot, err := os.Stat(stage.sourceRootPath)
	if err != nil {
		return nil, fmt.Errorf("stat current manual lyrics source root: %w", err)
	}
	pinnedRoot, err := stage.opener.OpenRoot(ctx, stage.sourceRootPath)
	if err != nil {
		return nil, fmt.Errorf("open pinned manual lyrics source root: %w", err)
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
		return nil, rootStatErr
	}
	current, err := stage.opener.OpenRoot(ctx, stage.sourceRootPath)
	if err != nil {
		return nil, err
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
		return nil, currentErr
	}
	parentRel := path.Dir(target)
	parent, owned, err := openExposureTargetParent(ctx, stage.parent, parentRel)
	if err != nil {
		return nil, err
	}
	parentClosed := false
	parentClose := func() error {
		if owned && !parentClosed {
			parentClosed = true
			return parent.Close()
		}
		return nil
	}
	transferred := false
	defer func() {
		if transferred {
			return
		}
		closeErr := parentClose()
		if recovered := recover(); recovered != nil {
			if closeErr != nil {
				panic(fmt.Errorf("panic during manual lyrics admission; close target parent: %w (panic: %v)", closeErr, recovered))
			}
			panic(recovered)
		}
		resultErr = errors.Join(resultErr, closeErr)
	}()
	parentID, err := parent.Identity(ctx)
	if err != nil {
		return nil, errors.Join(err, parentClose())
	}
	mutation, ok := parent.(sourcefs.OutputMutation)
	if !ok {
		return nil, errors.Join(errors.New("manual lyrics target native read capability is unavailable"), parentClose())
	}
	targetFile, err := mutation.OpenExistingRegular(ctx, path.Base(target))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open current manual lyrics target: %w", err), parentClose())
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
		return nil, errors.Join(statErr, parentClose())
	}
	checkParent, checkOwned, err := openExposureTargetParent(ctx, stage.parent, parentRel)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("revalidate manual lyrics target namespace: %w", err), parentClose())
	}
	checkID, checkErr := checkParent.Identity(ctx)
	if checkOwned {
		checkErr = errors.Join(checkErr, checkParent.Close())
	}
	if checkErr != nil || checkID != parentID {
		return nil, errors.Join(errors.New("manual lyrics target parent changed during admission"), checkErr, parentClose())
	}
	// Reopen by name relative to the anchored parent after hashing. The earlier
	// descriptor proves what was hashed; this descriptor proves that the same
	// regular file still occupies the admitted name.
	currentTarget, err := mutation.OpenExistingRegular(ctx, path.Base(target))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("reopen current manual lyrics target: %w", err), parentClose())
	}
	currentTargetInfo, verifyErr := currentTarget.Stat(ctx)
	if verifyErr == nil && (currentTargetInfo == nil || targetNativeInfo == nil || !os.SameFile(targetNativeInfo, currentTargetInfo) || currentTargetInfo.Size() != targetNativeInfo.Size() || !currentTargetInfo.ModTime().Equal(targetNativeInfo.ModTime())) {
		verifyErr = errors.New("manual lyrics target namespace changed after hashing")
	}
	verifyErr = errors.Join(verifyErr, currentTarget.Close())
	if verifyErr != nil {
		return nil, errors.Join(verifyErr, parentClose())
	}
	// Recheck both the source name and its contents after the final target hash,
	// immediately before the database-only admission.
	if err := stage.verifyInitialCopyInputs(ctx); err != nil {
		return nil, errors.Join(err, parentClose())
	}
	finalRootInfo, err := os.Stat(stage.sourceRootPath)
	if err != nil || finalRootInfo == nil || !os.SameFile(outputRootInfo, finalRootInfo) {
		return nil, errors.Join(errors.New("manual lyrics source root namespace changed before admission"), err, parentClose())
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
	// Capture native identity and the full digest before the durable ACK. An
	// inode first observed after ACK cannot prove which candidate was admitted.
	candidateInfo, candidateDigest, err := nativeManualLyricsCandidateEvidence(ctx, stage)
	if err != nil {
		return nil, errors.Join(err, parentClose())
	}
	if candidateInfo == nil || acknowledged.Ownership.ExpectedSize == nil || acknowledged.Ownership.ExpectedMTime == nil ||
		candidateInfo.Size() != *acknowledged.Ownership.ExpectedSize || candidateInfo.ModTime().UnixMicro() != *acknowledged.Ownership.ExpectedMTime ||
		!validReplacementDigest(acknowledged.PayloadSHA256) || hex.EncodeToString(candidateDigest[:]) != acknowledged.PayloadSHA256 {
		return nil, errors.Join(errors.New("manual lyrics candidate differs from acknowledged native evidence"), parentClose())
	}
	ack := acknowledged.Clone()
	outcome, err := repository.BeginManualLyricsExposure(ctx, scope.guard, intent, ack)
	if err != nil {
		return nil, errors.Join(err, parentClose())
	}
	if outcome != persistence.PublicationExposureRecorded && outcome != persistence.PublicationExposureAlreadyRecorded {
		return nil, errors.Join(errors.New("publication delivery is stale while admitting manual lyrics exposure"), parentClose())
	}
	// The ACK transaction may have taken time. Re-open after it and prove that
	// the same candidate inode still occupies the stage name with identical bytes.
	postAckInfo, postAckDigest, err := nativeManualLyricsCandidateEvidence(ctx, stage)
	if err != nil || postAckInfo == nil || !os.SameFile(candidateInfo, postAckInfo) ||
		postAckInfo.Size() != candidateInfo.Size() || !postAckInfo.ModTime().Equal(candidateInfo.ModTime()) || postAckDigest != candidateDigest {
		return nil, errors.Join(errors.New("manual lyrics candidate changed during exposure acknowledgement"), err, parentClose())
	}
	transferred = true
	return &privateManualLyricsExposureHandoff{
		scope: scope, work: work, stage: stage, guard: scope.guard,
		inputs: inputs.Clone(), intent: intent, observation: ack.Clone(),
		root: stage.parent, parent: parent, parentPath: parentRel,
		parentIdentity: parentID, ownsParent: owned,
		targetInfo: targetNativeInfo, targetSHA256: hex.EncodeToString(digest[:]),
		candidateInfo: candidateInfo, candidateSHA256: hex.EncodeToString(candidateDigest[:]),
	}, nil
}

func nativeManualLyricsCandidateEvidence(ctx context.Context, stage *preparedPublicationStage) (os.FileInfo, [32]byte, error) {
	var digest [32]byte
	mutation, ok := stage.directory.(sourcefs.OutputMutation)
	if !ok {
		return nil, digest, errors.New("manual lyrics candidate native read capability is unavailable")
	}
	file, err := mutation.OpenExistingRegular(ctx, stage.outputName)
	if err != nil {
		return nil, digest, err
	}
	info, statErr := file.Stat(ctx)
	if statErr == nil && (info == nil || !info.Mode().IsRegular()) {
		statErr = errors.New("manual lyrics candidate is not a regular file")
	}
	if statErr == nil {
		statErr = file.Borrow(ctx, func(native *os.File) error {
			var hashErr error
			digest, hashErr = hashBorrowedFile(ctx, native)
			return hashErr
		})
	}
	return info, digest, errors.Join(statErr, file.Close())
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
