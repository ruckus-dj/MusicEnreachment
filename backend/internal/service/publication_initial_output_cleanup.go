package service

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type publicationInitialOutputCleanupRepository interface {
	BeginPublicationInitialOutputCleanup(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationInitialOutputCleanupWitness) (persistence.PublicationInitialOutputCleanupOutcome, error)
	RecordPublicationInitialOutputCleanupResult(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationInitialOutputCleanupWitness) (persistence.PublicationInitialOutputCleanupOutcome, error)
	SettlePublicationInitialOutputFailure(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationInitialOutputCleanupWitness, bool) (bool, error)
}

func (stage *preparedPublicationStage) verifyInitialLyricsWriterRetainedOutput(ctx context.Context, transformed os.FileInfo) error {
	if transformed == nil {
		return errors.New("initial publication transform did not retain native output ownership")
	}
	info, err := nativeInitialLyricsCandidateInfo(ctx, stage)
	if err != nil {
		return fmt.Errorf("verify metadata writer output identity: %w", err)
	}
	if !os.SameFile(transformed, info) {
		return errors.New("metadata writer replaced the owned initial publication output")
	}
	return nil
}

// cleanupInitialLyricsReturnedValidationFailure is callable only from the
// validator's ordinary returned-error branch. All filesystem identities are
// re-opened relative to pinned native directory capabilities and compared with
// the evidence captured immediately before validation.
func (stage *preparedPublicationStage) cleanupInitialLyricsReturnedValidationFailure(
	ctx context.Context,
	scope *PublicationExecutionScope,
	reservation *publicationSyncReservation,
	inputIdentity persistence.PublicationFileIdentity,
	outputInfo os.FileInfo,
	outputIdentity persistence.PublicationFileIdentity,
) error {
	capability := scope.beginInitialOutputCleanup(stage, reservation)
	if capability == nil {
		return errors.New("initial publication validation cleanup lacks its sole original-owner reservation")
	}
	repository, ok := stage.repository.(publicationInitialOutputCleanupRepository)
	if !ok {
		return errors.New("initial publication validation cleanup journal is unavailable")
	}
	directoryCleanup, directoryOK := stage.directory.(sourcefs.OutputCleanup)
	parentCleanup, parentOK := stage.parent.(sourcefs.OutputCleanup)
	mutation, mutationOK := stage.directory.(sourcefs.OutputMutation)
	if !directoryOK || !parentOK || !mutationOK || outputInfo == nil || !outputInfo.Mode().IsRegular() {
		return errors.New("native initial publication validation cleanup capability is unavailable")
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return fmt.Errorf("verify initial publication cleanup directory: %w", err)
	}
	inputFile, err := mutation.OpenExistingRegular(ctx, stage.inputName)
	if err != nil {
		return fmt.Errorf("open owned initial publication input for cleanup: %w", err)
	}
	inputInfo, inputStatErr := inputFile.Stat(ctx)
	var inputDigest [32]byte
	if inputStatErr == nil {
		inputStatErr = inputFile.Borrow(ctx, func(native *os.File) error {
			var hashErr error
			inputDigest, hashErr = hashBorrowedFile(ctx, native)
			return hashErr
		})
	}
	inputStatErr = errors.Join(inputStatErr, inputFile.Close())
	if inputStatErr != nil {
		return fmt.Errorf("identify owned initial publication input for cleanup: %w", inputStatErr)
	}
	actualInput := publicationFileIdentity(inputInfo, inputDigest)
	if !os.SameFile(inputInfo, stage.sourceFileInfo) || !sameInitialCleanupIdentity(actualInput, inputIdentity) {
		return errors.New("prepared initial publication input changed before cleanup")
	}
	outputFile, err := mutation.OpenExistingRegular(ctx, initialLyricsCandidateName(stage))
	if err != nil {
		return fmt.Errorf("open owned initial publication output for cleanup: %w", err)
	}
	currentOutputInfo, outputStatErr := outputFile.Stat(ctx)
	var outputDigest [32]byte
	if outputStatErr == nil {
		outputStatErr = outputFile.Borrow(ctx, func(native *os.File) error {
			var hashErr error
			outputDigest, hashErr = hashBorrowedFile(ctx, native)
			return hashErr
		})
	}
	outputStatErr = errors.Join(outputStatErr, outputFile.Close())
	if outputStatErr != nil {
		return fmt.Errorf("identify owned initial publication output for cleanup: %w", outputStatErr)
	}
	actualOutput := publicationFileIdentity(currentOutputInfo, outputDigest)
	if !os.SameFile(outputInfo, currentOutputInfo) || !sameInitialCleanupIdentity(actualOutput, outputIdentity) {
		return errors.New("initial publication output ownership changed before cleanup")
	}
	preparation := stage.preparationRecord
	if preparation.CopiedInput != nil {
		copied := clonePublicationIdentity(*preparation.CopiedInput)
		preparation.CopiedInput = &copied
	}
	witness := persistence.PublicationInitialOutputCleanupWitness{Preparation: preparation, Input: inputIdentity, Output: outputIdentity, OutputRel: preparation.OutputRel}
	intent, err := repository.BeginPublicationInitialOutputCleanup(ctx, stage.guard, witness)
	if err != nil || intent != persistence.PublicationInitialOutputCleanupRecorded {
		return errors.Join(errors.New("initial publication output cleanup intent was not acknowledged"), err)
	}
	parentID := sourcefs.DirectoryIdentity{Platform: preparation.ParentIdentity.Platform, Volume: preparation.ParentIdentity.Volume, FileID: preparation.ParentIdentity.FileID}
	directoryID := sourcefs.DirectoryIdentity{Platform: preparation.DirectoryIdentity.Platform, Volume: preparation.DirectoryIdentity.Volume, FileID: preparation.DirectoryIdentity.FileID}
	if err := directoryCleanup.RemoveOwnedRegular(ctx, stage.inputName, directoryID, inputInfo); err != nil {
		return fmt.Errorf("remove owned initial publication input: %w", err)
	}
	if err := directoryCleanup.RemoveOwnedRegular(ctx, initialLyricsCandidateName(stage), directoryID, currentOutputInfo); err != nil {
		return fmt.Errorf("remove owned initial publication output: %w", err)
	}
	syncDirectory, ok := stage.directory.(sourcefs.OutputDirectorySync)
	if !ok {
		return errors.New("initial publication staging directory durability is unavailable")
	}
	if err := syncDirectory.SyncDirectory(ctx); err != nil {
		return fmt.Errorf("sync initial publication staging cleanup: %w", err)
	}
	if err := stage.Close(); err != nil {
		return fmt.Errorf("close initial publication staging directory: %w", err)
	}
	parentIdentity, err := stage.parent.Identity(ctx)
	if err != nil {
		return fmt.Errorf("identify initial publication cleanup parent: %w", err)
	}
	if parentIdentity != parentID {
		return errors.New("initial publication cleanup parent identity changed")
	}
	if err := parentCleanup.RemoveEmptyChildDir(ctx, stage.directoryName, parentID, directoryID); err != nil {
		return fmt.Errorf("remove owned initial publication staging directory: %w", err)
	}
	parentSync, ok := stage.parent.(sourcefs.OutputDirectorySync)
	if !ok {
		return errors.New("initial publication cleanup parent durability is unavailable")
	}
	if err := parentSync.SyncDirectory(ctx); err != nil {
		return fmt.Errorf("sync initial publication cleanup parent: %w", err)
	}
	complete, err := repository.RecordPublicationInitialOutputCleanupResult(ctx, stage.guard, witness)
	if err != nil || complete != persistence.PublicationInitialOutputCleanupRecorded {
		return errors.Join(errors.New("initial publication output cleanup completion was not acknowledged"), err)
	}
	settled, err := repository.SettlePublicationInitialOutputFailure(ctx, stage.guard, witness, true)
	if err != nil || !settled {
		return errors.Join(errors.New("initial publication output failure settlement was not acknowledged"), err)
	}
	if !scope.finishInitialOutputCleanup(capability, true) {
		return errors.New("publication execution scope changed before initial output cleanup settlement")
	}
	return nil
}

func sameInitialCleanupIdentity(a, b persistence.PublicationFileIdentity) bool {
	return a.SizeBytes != nil && b.SizeBytes != nil && *a.SizeBytes == *b.SizeBytes && a.MTime != nil && b.MTime != nil && *a.MTime == *b.MTime && a.SHA256 != "" && a.SHA256 == b.SHA256
}
