package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type publicationCandidateRebuildRecorder interface {
	RecordPublicationCandidateRebuild(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationCandidateRebuildRecord) error
}

// rebuildOutputCopy reconstructs a native candidate from the immutable prepared
// input. Once intent is acknowledged, every uncertain result fences the scope.
func (stage *preparedPublicationStage) rebuildOutputCopy(ctx context.Context, scope *PublicationExecutionScope, borrow *publicationOptionalLyricsBorrow) (resultErr error) {
	if stage == nil || scope == nil || borrow == nil || borrow.scope != scope || borrow.reservation == nil {
		return errors.New("publication candidate rebuild requires the optional lyrics reservation borrow")
	}
	scope.mu.Lock()
	reservation := borrow.reservation
	validScope := reservation.owner == scope && reservation.active && reservation.borrowed && reservation.borrow == borrow && !scope.unknown && !scope.finalized && !scope.finalizing && scope.syncWork == 1 && scope.guard == stage.guard
	scope.mu.Unlock()
	if !validScope {
		return errors.New("publication candidate rebuild scope is not active")
	}
	if err := stage.Validate(ctx); err != nil {
		return fmt.Errorf("validate prepared publication stage: %w", err)
	}
	mutator, ok := stage.directory.(sourcefs.OutputMutation)
	cleanup, cleanupOK := stage.directory.(sourcefs.OutputCleanup)
	if !ok || !cleanupOK {
		return errors.New("publication candidate rebuild native capabilities are unavailable")
	}
	recorder, ok := stage.repository.(publicationCandidateRebuildRecorder)
	if !ok {
		return errors.New("publication candidate rebuild journal is unavailable")
	}
	dirID, err := stage.directory.Identity(ctx)
	if err != nil {
		return err
	}
	if dirID.Platform != stage.identity.Platform || dirID.Volume != stage.identity.Volume || dirID.FileID != stage.identity.FileID {
		return errors.New("publication candidate rebuild directory ownership changed")
	}
	owner := stage.preparationRecord.OwnerToken
	if owner == uuid.Nil {
		return errors.New("prepared publication owner token is unavailable")
	}
	inName, outName := stage.inputName, filepath.Base(stage.paths.OutputPath)
	input, err := mutator.OpenExistingRegular(ctx, inName)
	if err != nil {
		return fmt.Errorf("open prepared publication input: %w", err)
	}
	inputClosed := false
	defer func() {
		if !inputClosed {
			resultErr = errors.Join(resultErr, input.Close())
		}
	}()
	inputInfo, err := input.Stat(ctx)
	if err != nil || !inputInfo.Mode().IsRegular() || inputInfo.Size() != stage.sourceFileInfo.Size() || !os.SameFile(stage.sourceFileInfo, inputInfo) || !sameSourceMtime(stage.sourceFileInfo.ModTime(), inputInfo.ModTime()) {
		return errors.Join(errors.New("prepared publication input identity changed"), err)
	}
	inputDigest, err := hashRegularFile(ctx, input)
	if err != nil {
		return err
	}
	inputFact := publicationRebuildFileIdentity(inputInfo, inputDigest)
	// The candidate produced by the optional writer is the only entry this
	// mutation may replace. Pin its native identity before recording intent.
	oldCandidate, err := mutator.OpenExistingRegular(ctx, outName)
	if err != nil {
		return fmt.Errorf("open publication candidate before rebuild: %w", err)
	}
	oldCandidateInfo, err := oldCandidate.Stat(ctx)
	if err != nil || oldCandidateInfo == nil || !oldCandidateInfo.Mode().IsRegular() {
		return errors.Join(errors.New("publication candidate ownership changed before rebuild"), err, oldCandidate.Close())
	}
	oldCandidateDigest, err := hashRegularFile(ctx, oldCandidate)
	if err != nil {
		return errors.Join(err, oldCandidate.Close())
	}
	oldCandidateFact := publicationRebuildFileIdentity(oldCandidateInfo, oldCandidateDigest)
	base := persistence.PublicationCandidateRebuildRecord{Version: persistence.PublicationCandidateRebuildVersion, Kind: "publication_candidate_rebuild", Guard: stage.guard, OwnerToken: owner, PrivateDirRel: stage.directoryName, InputRel: stage.directoryName + "/" + inName, OutputRel: stage.directoryName + "/" + outName, DirectoryIdentity: persistence.PublicationDirectoryIdentity{Platform: dirID.Platform, Volume: dirID.Volume, FileID: dirID.FileID}}
	intent := base
	intent.Step, intent.Input, intent.Candidate = "intent", &inputFact, &oldCandidateFact
	if err := recorder.RecordPublicationCandidateRebuild(ctx, stage.guard, intent); err != nil {
		_ = oldCandidate.Close()
		return fmt.Errorf("record publication candidate rebuild intent: %w", err)
	}
	intentAck := true
	candidateInfo := oldCandidateInfo
	defer func() {
		panicValue := recover()
		if intentAck && (resultErr != nil || panicValue != nil) {
			scope.quarantineUncertain()
			if candidateInfo != nil {
				resultErr = errors.Join(resultErr, cleanup.RemoveOwnedRegular(context.WithoutCancel(ctx), outName, dirID, candidateInfo))
			}
		}
		if panicValue != nil {
			panic(panicValue)
		}
	}()

	if err := oldCandidate.Close(); err != nil {
		return fmt.Errorf("close publication candidate before rebuild: %w", err)
	}
	if err := cleanup.RemoveOwnedRegular(ctx, outName, dirID, oldCandidateInfo); err != nil {
		return fmt.Errorf("remove owned publication candidate before rebuild: %w", err)
	}
	candidateInfo = nil
	output, err := stage.directory.CreateExclusive(ctx, outName)
	if err != nil {
		return fmt.Errorf("create publication candidate: %w", err)
	}
	outputClosed := false
	defer func() {
		if !outputClosed {
			var statErr error
			candidateInfo, statErr = output.Stat(context.WithoutCancel(ctx))
			resultErr = errors.Join(resultErr, statErr, output.Close())
		}
	}()
	var copiedHash [sha256.Size]byte
	copyErr := input.Borrow(ctx, func(file *os.File) error {
		hash := sha256.New()
		buffer := make([]byte, 64*1024)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, readErr := file.Read(buffer)
			if n > 0 {
				if _, err := hash.Write(buffer[:n]); err != nil {
					return err
				}
				written, err := output.Write(ctx, buffer[:n])
				if err != nil {
					return err
				}
				if written != n {
					return io.ErrShortWrite
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		copy(copiedHash[:], hash.Sum(nil))
		return nil
	})
	copyErr = errors.Join(copyErr, output.Sync(ctx))
	candidateInfo, err = output.Stat(ctx)
	outputClosed = true
	copyErr = errors.Join(copyErr, err, output.Close())
	if copyErr == nil && (candidateInfo == nil || !candidateInfo.Mode().IsRegular() || candidateInfo.Size() != inputInfo.Size() || hex.EncodeToString(copiedHash[:]) != hex.EncodeToString(inputDigest[:])) {
		copyErr = errors.New("publication candidate copy facts changed")
	}
	if copyErr == nil {
		after, statErr := input.Stat(ctx)
		if statErr != nil || !os.SameFile(inputInfo, after) || inputInfo.Size() != after.Size() || !sameSourceMtime(inputInfo.ModTime(), after.ModTime()) {
			copyErr = errors.Join(errors.New("prepared publication input changed during candidate copy"), statErr)
		} else {
			afterDigest, digestErr := hashRegularFile(ctx, input)
			if digestErr != nil || afterDigest != inputDigest {
				copyErr = errors.Join(errors.New("prepared publication input content changed during candidate copy"), digestErr)
			}
		}
	}
	if copyErr != nil {
		return fmt.Errorf("copy prepared publication input: %w", copyErr)
	}
	if err := input.Close(); err != nil {
		return fmt.Errorf("close prepared publication input before acknowledgement: %w", err)
	}
	inputClosed = true
	currentCandidate, openErr := mutator.OpenExistingRegular(ctx, outName)
	if openErr != nil {
		return fmt.Errorf("reopen publication candidate for identity check: %w", openErr)
	}
	currentInfo, statErr := currentCandidate.Stat(ctx)
	closeErr := currentCandidate.Close()
	if statErr != nil || closeErr != nil || currentInfo == nil || !os.SameFile(candidateInfo, currentInfo) {
		return errors.Join(errors.New("publication candidate namespace changed before acknowledgement"), statErr, closeErr)
	}
	currentDirID, err := stage.directory.Identity(ctx)
	if err != nil || currentDirID != dirID {
		return errors.Join(errors.New("publication candidate directory changed before acknowledgement"), err)
	}
	candidateFact := publicationRebuildFileIdentity(candidateInfo, copiedHash)
	complete := base
	complete.Step, complete.Input, complete.Candidate = "complete", &inputFact, &candidateFact
	if err := recorder.RecordPublicationCandidateRebuild(ctx, stage.guard, complete); err != nil {
		return fmt.Errorf("acknowledge publication candidate rebuild: %w", err)
	}
	intentAck = false
	return nil
}

func hashRegularFile(ctx context.Context, file sourcefs.RegularFile) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	err := file.Borrow(ctx, func(f *os.File) error {
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		copy(digest[:], h.Sum(nil))
		return nil
	})
	return digest, err
}

func publicationRebuildFileIdentity(info os.FileInfo, digest [sha256.Size]byte) persistence.PublicationFileIdentity {
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	return persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:])}
}
