package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// publicationInitialOutputCopy is an opaque stage-bound proof; it carries no
// descriptor and grants no authority to resume or rebuild the copy.
type publicationInitialOutputCopy struct {
	stage *preparedPublicationStage
}

// initialOutputCopy creates the prepared FLAC stage's one fixed initial output.
// Any error after entering the helper is conservatively quarantined because
// CreateExclusive may have mutated the stage before returning that error.
func (stage *preparedPublicationStage) initialOutputCopy(ctx context.Context, scope *PublicationExecutionScope, reservation *publicationSyncReservation) (result *publicationInitialOutputCopy, resultErr error) {
	if stage == nil || !stage.privatePrepMode || stage.outputName == "" || !stage.rewrite || !stage.rewriteIdentityHasSHA() || scope == nil || scope.guard != stage.guard {
		return nil, errors.New("initial output copy requires the private prepared FLAC stage")
	}
	if !scope.beginInitialOutputCopy(reservation) {
		return nil, errors.New("initial output copy requires the sole active publication reservation")
	}
	var inputRoot, currentRoot sourcefs.Directory
	var input, current sourcefs.RegularFile
	var namedOutput sourcefs.RegularFile
	var output sourcefs.OutputFile
	handlesClosed := false
	defer func() {
		if recovered := recover(); recovered != nil {
			if !handlesClosed {
				_ = errors.Join(closeMaybeRegular(namedOutput), closeMaybeOutput(output), closeMaybeRegular(current), closeMaybeDirectory(currentRoot), closeMaybeRegular(input), closeMaybeDirectory(inputRoot))
			}
			scope.endInitialOutputCopy(reservation, true)
			panic(recovered)
		}
		scope.endInitialOutputCopy(reservation, resultErr != nil)
	}()
	if err := stage.Validate(ctx); err != nil {
		return nil, err
	}
	if err := stage.verifyInitialCopyInputs(ctx); err != nil {
		return nil, err
	}
	_, ok := stage.directory.(sourcefs.OutputMutation)
	if !ok {
		return nil, errors.New("publication stage does not support safe output creation")
	}
	inputRoot, err := stage.opener.OpenRoot(ctx, stage.inputPath)
	if err != nil {
		return nil, err
	}
	input, err = sourcefs.OpenRegularAt(ctx, inputRoot, stage.inputName)
	if err != nil {
		return nil, errors.Join(err, inputRoot.Close())
	}
	currentRoot, err = stage.opener.OpenRoot(ctx, stage.sourceRootPath)
	if err != nil {
		return nil, errors.Join(err, input.Close(), inputRoot.Close())
	}
	current, err = sourcefs.OpenRegularAt(ctx, currentRoot, stage.sourceRelativePath)
	if err != nil {
		return nil, errors.Join(err, currentRoot.Close(), input.Close(), inputRoot.Close())
	}
	output, createErr := stage.directory.CreateExclusive(ctx, stage.outputName)
	if createErr != nil {
		closeErr := errors.Join(current.Close(), currentRoot.Close(), input.Close(), inputRoot.Close())
		handlesClosed = true
		return nil, errors.Join(createErr, closeErr)
	}
	var copyErr error
	var copiedHash [sha256.Size]byte
	copyErr = input.Borrow(ctx, func(in *os.File) error {
		return outputCopyNative(ctx, in, output, &copiedHash)
	})
	copyErr = errors.Join(copyErr, output.Sync(ctx))
	if copyErr == nil {
		mutation := stage.directory.(sourcefs.OutputMutation)
		namedOutput, copyErr = mutation.OpenExistingRegular(ctx, stage.outputName)
	}
	var outputInfo, namedInfo os.FileInfo
	if copyErr == nil {
		outputInfo, copyErr = output.Stat(ctx)
	}
	if copyErr == nil {
		namedInfo, copyErr = namedOutput.Stat(ctx)
	}
	if copyErr == nil && (!outputInfo.Mode().IsRegular() || !namedInfo.Mode().IsRegular() || !os.SameFile(outputInfo, namedInfo) || outputInfo.Size() != namedInfo.Size() || !outputInfo.ModTime().Equal(namedInfo.ModTime())) {
		copyErr = errors.New("initial publication output namespace changed")
	}
	var outputHash [sha256.Size]byte
	if copyErr == nil {
		copyErr = namedOutput.Borrow(ctx, func(file *os.File) error {
			var err error
			outputHash, err = hashBorrowedFile(ctx, file)
			return err
		})
	}
	frozenHash := stage.rewriteIdentity.SHA256
	if copyErr == nil && (hex.EncodeToString(copiedHash[:]) != frozenHash || hex.EncodeToString(outputHash[:]) != frozenHash) {
		copyErr = errors.New("initial publication output digest mismatch")
	}
	if copyErr == nil {
		copyErr = stage.verifyInitialOutputEntry(ctx, outputInfo)
	}
	inputInfo, inputStatErr := input.Stat(ctx)
	if copyErr == nil && inputStatErr == nil && (inputInfo.Size() != outputInfo.Size() || !os.SameFile(inputInfo, stage.sourceFileInfo)) {
		copyErr = errors.New("initial publication copy native input identity changed")
	}
	copyErr = errors.Join(copyErr, inputStatErr)
	copyErr = errors.Join(copyErr, closeMaybeRegular(namedOutput), output.Close(), current.Close(), currentRoot.Close(), input.Close(), inputRoot.Close())
	handlesClosed = true
	if copyErr != nil {
		return nil, copyErr
	}
	if err := stage.Validate(ctx); err != nil {
		return nil, err
	}
	if err := stage.verifyInitialCopyInputs(ctx); err != nil {
		return nil, err
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return nil, err
	}
	if err := stage.verifyInitialOutputEntry(ctx, outputInfo); err != nil {
		return nil, err
	}
	return &publicationInitialOutputCopy{stage: stage}, nil
}

// verifyInitialOutputEntry performs a bounded final namespace check by reopening
// the fixed name relative to the pinned output directory. It detects replacement
// during hashing and validation without claiming protection against a concurrent
// hostile writer racing this final check.
func (stage *preparedPublicationStage) verifyInitialOutputEntry(ctx context.Context, expected os.FileInfo) error {
	mutation, ok := stage.directory.(sourcefs.OutputMutation)
	if !ok {
		return errors.New("publication stage does not support safe output verification")
	}
	named, err := mutation.OpenExistingRegular(ctx, stage.outputName)
	if err != nil {
		return err
	}
	info, statErr := named.Stat(ctx)
	closeErr := named.Close()
	if statErr == nil && (expected == nil || !info.Mode().IsRegular() || !os.SameFile(expected, info) || expected.Size() != info.Size() || !expected.ModTime().Equal(info.ModTime())) {
		statErr = errors.New("initial publication output namespace changed")
	}
	return errors.Join(statErr, closeErr)
}

func closeMaybeOutput(file sourcefs.OutputFile) error {
	if file != nil {
		return file.Close()
	}
	return nil
}

func closeMaybeRegular(file sourcefs.RegularFile) error {
	if file != nil {
		return file.Close()
	}
	return nil
}

func closeMaybeDirectory(directory sourcefs.Directory) error {
	if directory != nil {
		return directory.Close()
	}
	return nil
}

func (stage *preparedPublicationStage) rewriteIdentityHasSHA() bool {
	return stage.rewriteIdentity.SHA256 != ""
}

func (stage *preparedPublicationStage) verifyOutputDirectory(ctx context.Context) error {
	if err := stage.parent.Verify(ctx); err != nil {
		return err
	}
	parentIdentity, err := stage.parent.Identity(ctx)
	if err != nil {
		return err
	}
	if parentIdentity != stage.parentIdentity {
		return errors.New("publication output parent identity changed")
	}
	if err := stage.parent.VerifyChild(ctx, stage.directoryName, sourcefs.DirectoryIdentity{Platform: stage.identity.Platform, Volume: stage.identity.Volume, FileID: stage.identity.FileID}); err != nil {
		return err
	}
	return verifyPublicationDirectoryNamespace(ctx, stage.parent, stage.directoryName, stage.directory, sourcefs.DirectoryIdentity{Platform: stage.identity.Platform, Volume: stage.identity.Volume, FileID: stage.identity.FileID}, stage.opener, stage.inputPath)
}

func (stage *preparedPublicationStage) verifyInitialCopyInputs(ctx context.Context) error {
	if stage.preparationRecord.Step != "input_ready" || stage.preparationRecord.OperationID != stage.guard.OperationID || stage.preparationRecord.ClaimUUID != stage.guard.ClaimUUID || stage.preparationRecord.OwnerToken == [16]byte{} || stage.preparationRecord.PrivateDirRel != stage.directoryName || stage.preparationRecord.InputRel != stage.directoryName+"/"+stage.inputName || stage.preparationRecord.OutputRel != stage.directoryName+"/"+stage.outputName || stage.preparationRecord.ParentIdentity != persistenceDirectoryIdentity(stage.parentIdentity) || stage.preparationRecord.DirectoryIdentity == nil || *stage.preparationRecord.DirectoryIdentity != stage.identity || stage.preparationRecord.CopiedInput == nil || stage.preparationRecord.CopiedInput.SizeBytes == nil || stage.preparationRecord.CopiedInput.MTime == nil {
		return errors.New("prepared publication input journal ownership is invalid")
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return err
	}
	root, err := stage.opener.OpenRoot(ctx, stage.inputPath)
	if err != nil {
		return err
	}
	input, err := sourcefs.OpenRegularAt(ctx, root, stage.inputName)
	if err != nil {
		return errors.Join(err, root.Close())
	}
	info, statErr := input.Stat(ctx)
	var actual [sha256.Size]byte
	if statErr == nil {
		statErr = input.Borrow(ctx, func(file *os.File) error {
			var err error
			actual, err = hashBorrowedFile(ctx, file)
			return err
		})
	}
	if statErr == nil && (info.Size() != *stage.preparationRecord.CopiedInput.SizeBytes || info.ModTime().UnixMicro() != *stage.preparationRecord.CopiedInput.MTime || !os.SameFile(info, stage.sourceFileInfo) || hex.EncodeToString(actual[:]) != stage.rewriteIdentity.SHA256) {
		statErr = errors.New("prepared publication input differs from frozen ownership")
	}
	if statErr == nil {
		statErr = verifySourceFileNamespace(ctx, root, stage.inputName, info)
	}
	statErr = errors.Join(statErr, input.Close(), root.Close())
	if statErr != nil {
		return statErr
	}
	currentRoot, err := stage.opener.OpenRoot(ctx, stage.sourceRootPath)
	if err != nil {
		return err
	}
	current, err := sourcefs.OpenRegularAt(ctx, currentRoot, stage.sourceRelativePath)
	if err != nil {
		return errors.Join(err, currentRoot.Close())
	}
	currentInfo, statErr := current.Stat(ctx)
	var digest [sha256.Size]byte
	if statErr == nil {
		statErr = current.Borrow(ctx, func(file *os.File) error {
			var err error
			digest, err = hashBorrowedFile(ctx, file)
			return err
		})
	}
	if statErr == nil && (stage.rewriteIdentity.SizeBytes == nil || stage.rewriteIdentity.MTime == nil || currentInfo.Size() != *stage.rewriteIdentity.SizeBytes || currentInfo.ModTime().UnixMicro() != *stage.rewriteIdentity.MTime || hex.EncodeToString(digest[:]) != stage.rewriteIdentity.SHA256) {
		statErr = errors.New("current publication differs from frozen ownership")
	}
	if statErr == nil {
		statErr = verifySourceNamespace(ctx, stage.opener, currentRoot, stage.sourceRootPath)
	}
	if statErr == nil {
		statErr = verifySourceFileNamespace(ctx, currentRoot, stage.sourceRelativePath, currentInfo)
	}
	statErr = errors.Join(statErr, current.Close(), currentRoot.Close())
	return statErr
}

func persistenceDirectoryIdentity(identity sourcefs.DirectoryIdentity) persistence.PublicationDirectoryIdentity {
	return persistence.PublicationDirectoryIdentity{Platform: identity.Platform, Volume: identity.Volume, FileID: identity.FileID}
}

func outputCopyNative(ctx context.Context, input *os.File, output sourcefs.OutputFile, copiedHash *[sha256.Size]byte) error {
	if _, err := input.Seek(0, 0); err != nil {
		return err
	}
	hash := sha256.New()
	buffer := make([]byte, sourceAnalysisCopyBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, err := input.Read(buffer)
		if read > 0 {
			_, _ = hash.Write(buffer[:read])
			written, writeErr := output.Write(ctx, buffer[:read])
			if writeErr != nil {
				return writeErr
			}
			if written != read {
				return io.ErrShortWrite
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	copy(copiedHash[:], hash.Sum(nil))
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("initial publication copy canceled: %w", err)
	}
	return nil
}
