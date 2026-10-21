package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// initialLyricsRebuildCapability is issued only after the exact intent has
// received a new durable acknowledgement. It is deliberately single-use.
type initialLyricsRebuildCapability struct {
	stage         *preparedPublicationStage
	scope         *PublicationExecutionScope
	reservation   *publicationSyncReservation
	intent        persistence.PublicationCandidateRebuildRecord
	candidate     os.FileInfo
	active        bool
	acknowledged  bool
	acknowledging bool
	fenced        bool
}

// prepareInitialLyricsRebuild freezes native evidence immediately before a
// rebuild may be considered. It does not mutate the candidate.
func (stage *preparedPublicationStage) prepareInitialLyricsRebuild(ctx context.Context, scope *PublicationExecutionScope, reservation *publicationSyncReservation) (persistence.PublicationCandidateRebuildRecord, *initialLyricsRebuildCapability, error) {
	if !stage.validInitialLyricsRebuildOwner(scope, reservation) {
		return persistence.PublicationCandidateRebuildRecord{}, nil, errors.New("initial lyrics rebuild requires the sole active publication reservation")
	}
	if !scope.beginInitialOutputCopy(reservation) {
		return persistence.PublicationCandidateRebuildRecord{}, nil, errors.New("initial lyrics rebuild cannot fence publication scope")
	}
	keepFence := false
	defer func() {
		if !keepFence {
			scope.endInitialOutputCopy(reservation, false)
		}
	}()
	if err := stage.Validate(ctx); err != nil {
		return persistence.PublicationCandidateRebuildRecord{}, nil, err
	}
	inputIdentity, err := stage.verifyInitialLyricsPreparedAudioInput(ctx)
	if err != nil {
		return persistence.PublicationCandidateRebuildRecord{}, nil, err
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return persistence.PublicationCandidateRebuildRecord{}, nil, err
	}
	candidateInfo, candidateHash, err := nativeInitialLyricsCandidateEvidence(ctx, stage)
	if err != nil {
		return persistence.PublicationCandidateRebuildRecord{}, nil, err
	}
	if inputIdentity.SHA256 == "" || inputIdentity.SizeBytes == nil || inputIdentity.MTime == nil {
		return persistence.PublicationCandidateRebuildRecord{}, nil, errors.New("initial lyrics rebuild input evidence is incomplete")
	}
	candidateIdentity := publicationFileIdentity(candidateInfo, candidateHash)
	if candidateIdentity.SizeBytes == nil || candidateIdentity.MTime == nil {
		return persistence.PublicationCandidateRebuildRecord{}, nil, errors.New("initial lyrics rebuild candidate evidence is incomplete")
	}
	record := persistence.PublicationCandidateRebuildRecord{
		Version: persistence.PublicationCandidateRebuildVersion, Kind: "publication_candidate_rebuild", Step: "intent", Guard: stage.guard,
		OwnerToken: stage.preparationRecord.OwnerToken, PrivateDirRel: stage.directoryName,
		InputRel: stage.preparationRecord.InputRel, OutputRel: stage.preparationRecord.OutputRel,
		DirectoryIdentity: stage.identity, Candidate: &candidateIdentity, Input: &inputIdentity,
	}
	capability := &initialLyricsRebuildCapability{stage: stage, scope: scope, reservation: reservation, intent: cloneRebuildRecord(record), candidate: candidateInfo, active: true, fenced: true}
	keepFence = true
	return record, capability, nil
}

// acknowledgeInitialLyricsRebuildIntent records the intent here, rather than
// accepting a caller-provided boolean or treating an idempotent ACK as authority.
func (stage *preparedPublicationStage) acknowledgeInitialLyricsRebuildIntent(ctx context.Context, capability *initialLyricsRebuildCapability) error {
	if !stage.beginInitialLyricsCapabilityACK(capability) {
		return errors.New("initial lyrics rebuild intent is not owned by this publication stage")
	}
	scope, intent := capability.scope, capability.intent
	ackOK := false
	defer func() {
		if !ackOK {
			stage.endInitialLyricsRebuildFence(capability, true)
		}
	}()
	inputIdentity, err := stage.verifyInitialLyricsPreparedAudioInput(ctx)
	if err != nil {
		return err
	}
	currentInfo, currentHash, err := nativeInitialLyricsCandidateEvidence(ctx, stage)
	if err != nil {
		return err
	}
	if intent.Candidate == nil || intent.Input == nil || intent.OwnerToken != stage.preparationRecord.OwnerToken || intent.PrivateDirRel != stage.directoryName || intent.InputRel != stage.preparationRecord.InputRel || intent.OutputRel != stage.preparationRecord.OutputRel || intent.DirectoryIdentity != stage.identity || !sameRebuildIdentity(*intent.Candidate, publicationFileIdentity(currentInfo, currentHash)) || !sameRebuildIdentity(*intent.Input, inputIdentity) {
		return errors.New("initial lyrics rebuild intent does not match current prepared evidence")
	}
	journal, ok := stage.repository.(publicationInitialLyricsJournal)
	if !ok {
		return errors.New("initial lyrics rebuild journal is unavailable")
	}
	outcome, err := journal.GuardedRecordPublicationInitialLyricsRebuild(ctx, stage.guard, intent)
	if err != nil {
		return fmt.Errorf("acknowledge initial lyrics rebuild intent: %w", err)
	}
	if outcome != persistence.PublicationOptionalLyricsRecorded {
		return errors.New("initial lyrics rebuild intent was not newly acknowledged")
	}
	info, digest, err := nativeInitialLyricsCandidateEvidence(ctx, stage)
	if err != nil {
		scope.quarantineUncertain()
		return err
	}
	if intent.Candidate == nil || !sameRebuildIdentity(*intent.Candidate, publicationFileIdentity(info, digest)) {
		scope.quarantineUncertain()
		return errors.New("initial lyrics candidate changed before its intent was acknowledged")
	}
	scope.mu.Lock()
	capability.acknowledged = true
	scope.mu.Unlock()
	ackOK = true
	return nil
}

// resetInitialLyricsCandidate replaces only the exact acknowledged candidate
// with an exclusively created, synced empty output. It never copies or transforms.
func (stage *preparedPublicationStage) resetInitialLyricsCandidate(ctx context.Context, scope *PublicationExecutionScope, capability *initialLyricsRebuildCapability) error {
	if !stage.consumeInitialLyricsResetCapability(scope, capability) {
		return errors.New("initial lyrics candidate reset requires its acknowledged capability")
	}
	uncertain := true
	defer func() { stage.endInitialLyricsRebuildFence(capability, uncertain) }()
	if err := stage.Validate(ctx); err != nil {
		return err
	}
	if _, err := stage.verifyInitialLyricsPreparedAudioInput(ctx); err != nil {
		return err
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return err
	}
	currentInfo, currentHash, err := nativeInitialLyricsCandidateEvidence(ctx, stage)
	if err != nil {
		return err
	}
	if !os.SameFile(capability.candidate, currentInfo) || !sameRebuildIdentity(*capability.intent.Candidate, publicationFileIdentity(currentInfo, currentHash)) {
		return errors.New("initial lyrics candidate changed after rebuild intent")
	}
	cleanup, ok := stage.directory.(sourcefs.OutputCleanup)
	if !ok {
		return errors.New("native initial lyrics candidate cleanup is unavailable")
	}
	if err := cleanup.RemoveOwnedRegular(ctx, initialLyricsCandidateName(stage), sourcefs.DirectoryIdentity{Platform: stage.identity.Platform, Volume: stage.identity.Volume, FileID: stage.identity.FileID}, currentInfo); err != nil {
		return err
	}
	output, err := stage.directory.CreateExclusive(ctx, initialLyricsCandidateName(stage))
	if err != nil {
		return err
	}
	writeErr := output.Sync(ctx)
	closeErr := output.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	if syncer, ok := stage.directory.(sourcefs.OutputDirectorySync); ok {
		if err := syncer.SyncDirectory(ctx); err != nil {
			return err
		}
	}
	uncertain = false
	return nil
}

// finishInitialLyricsRebuild captures complete evidence after the caller reruns
// the same frozen transform and strict write, without altering its output.
func (stage *preparedPublicationStage) finishInitialLyricsRebuild(ctx context.Context, scope *PublicationExecutionScope, reservation *publicationSyncReservation, intent persistence.PublicationCandidateRebuildRecord) (persistence.PublicationCandidateRebuildRecord, error) {
	if !stage.validInitialLyricsRebuildOwner(scope, reservation) || intent.Step != "intent" || intent.Guard != stage.guard || intent.Candidate == nil || intent.Input == nil || intent.OwnerToken != stage.preparationRecord.OwnerToken || intent.PrivateDirRel != stage.directoryName || intent.InputRel != stage.preparationRecord.InputRel || intent.OutputRel != stage.preparationRecord.OutputRel || intent.DirectoryIdentity != stage.identity {
		return persistence.PublicationCandidateRebuildRecord{}, errors.New("initial lyrics rebuild completion requires the sole active publication reservation")
	}
	if !scope.beginInitialOutputCopy(reservation) {
		return persistence.PublicationCandidateRebuildRecord{}, errors.New("initial lyrics rebuild cannot fence publication scope for completion")
	}
	uncertain := true
	defer func() { scope.endInitialOutputCopy(reservation, uncertain) }()
	if err := stage.Validate(ctx); err != nil {
		return persistence.PublicationCandidateRebuildRecord{}, err
	}
	inputIdentity, err := stage.verifyInitialLyricsPreparedAudioInput(ctx)
	if err != nil {
		return persistence.PublicationCandidateRebuildRecord{}, err
	}
	if !sameRebuildIdentity(*intent.Input, inputIdentity) {
		return persistence.PublicationCandidateRebuildRecord{}, errors.New("initial lyrics rebuild input changed before completion")
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return persistence.PublicationCandidateRebuildRecord{}, err
	}
	beforeInfo, beforeHash, err := nativeInitialLyricsCandidateEvidence(ctx, stage)
	if err != nil {
		return persistence.PublicationCandidateRebuildRecord{}, err
	}
	syncer, ok := stage.directory.(sourcefs.OutputRegularFileSync)
	if !ok {
		return persistence.PublicationCandidateRebuildRecord{}, errors.New("native initial lyrics candidate sync capability is unavailable")
	}
	if err := syncer.SyncExistingRegular(ctx, initialLyricsCandidateName(stage), beforeInfo); err != nil {
		return persistence.PublicationCandidateRebuildRecord{}, err
	}
	if syncer, ok := stage.directory.(sourcefs.OutputDirectorySync); ok {
		if err := syncer.SyncDirectory(ctx); err != nil {
			return persistence.PublicationCandidateRebuildRecord{}, err
		}
	}
	info, digest, err := nativeInitialLyricsCandidateEvidence(ctx, stage)
	if err != nil {
		return persistence.PublicationCandidateRebuildRecord{}, err
	}
	if !os.SameFile(beforeInfo, info) || !sameRebuildIdentity(publicationFileIdentity(beforeInfo, beforeHash), publicationFileIdentity(info, digest)) {
		return persistence.PublicationCandidateRebuildRecord{}, errors.New("initial lyrics candidate changed while syncing")
	}
	complete := intent
	complete.Step = "complete"
	identity := publicationFileIdentity(info, digest)
	if identity.SizeBytes == nil || identity.MTime == nil || identity.SHA256 == "" {
		scope.quarantineUncertain()
		return persistence.PublicationCandidateRebuildRecord{}, errors.New("rebuilt initial lyrics candidate differs from frozen input")
	}
	complete.Candidate = &identity
	complete.Input = cloneCandidateIdentity(intent.Input)
	uncertain = false
	return complete, nil
}

func (stage *preparedPublicationStage) beginInitialLyricsCapabilityACK(capability *initialLyricsRebuildCapability) bool {
	if stage == nil || capability == nil || capability.stage != stage || capability.scope == nil || capability.intent.Step != "intent" {
		return false
	}
	scope := capability.scope
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if !capability.active || !capability.fenced || capability.acknowledged || capability.acknowledging || reservationNotFenced(scope, capability.reservation) || scope.unknown || scope.finalized || scope.finalizing || scope.unresolved != 0 {
		return false
	}
	capability.acknowledging = true
	return true
}

func (stage *preparedPublicationStage) consumeInitialLyricsResetCapability(scope *PublicationExecutionScope, capability *initialLyricsRebuildCapability) bool {
	if stage == nil || capability == nil || capability.stage != stage || capability.scope != scope || scope == nil {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if !capability.active || !capability.fenced || !capability.acknowledged || reservationNotFenced(scope, capability.reservation) || scope.unknown || scope.finalized || scope.finalizing || scope.unresolved != 0 {
		return false
	}
	capability.active = false
	return true
}

func reservationNotFenced(scope *PublicationExecutionScope, reservation *publicationSyncReservation) bool {
	return reservation == nil || reservation.owner != scope || !reservation.active || !reservation.borrowed || !scope.initialCopy || scope.syncWork != 1 || scope.preInputCleanup != nil
}

func (stage *preparedPublicationStage) endInitialLyricsRebuildFence(capability *initialLyricsRebuildCapability, uncertain bool) {
	if capability == nil || capability.scope == nil {
		return
	}
	capability.scope.mu.Lock()
	if !capability.fenced {
		capability.scope.mu.Unlock()
		return
	}
	capability.fenced = false
	capability.scope.mu.Unlock()
	capability.scope.endInitialOutputCopy(capability.reservation, uncertain)
}

func (stage *preparedPublicationStage) validInitialLyricsRebuildOwner(scope *PublicationExecutionScope, reservation *publicationSyncReservation) bool {
	if stage == nil || stage.privatePrepMode || stage.rewrite || stage.hold == uuid.Nil || stage.preparationRecord.Step != "input_ready" || stage.preparationRecord.OperationID != stage.guard.OperationID || stage.preparationRecord.ClaimUUID != stage.guard.ClaimUUID || stage.preparationRecord.PrivateDirRel != stage.directoryName || stage.preparationRecord.CopiedInput == nil || stage.preparationRecord.CopiedInput.SizeBytes == nil || stage.preparationRecord.CopiedInput.MTime == nil || scope == nil || scope.guard != stage.guard || reservation == nil {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return reservation.owner == scope && reservation.active && !reservation.borrowed && scope.syncWork == 1 && !scope.unknown && !scope.finalized && !scope.finalizing && scope.preInputCleanup == nil && scope.unresolved == 0
}

func initialLyricsCandidateName(stage *preparedPublicationStage) string {
	if stage.outputName != "" {
		return stage.outputName
	}
	return filepath.Base(stage.paths.OutputPath)
}

func nativeInitialLyricsCandidateEvidence(ctx context.Context, stage *preparedPublicationStage) (os.FileInfo, [32]byte, error) {
	var digest [32]byte
	mutation, ok := stage.directory.(sourcefs.OutputMutation)
	if !ok {
		return nil, digest, errors.New("native initial lyrics candidate read capability is unavailable")
	}
	file, err := mutation.OpenExistingRegular(ctx, initialLyricsCandidateName(stage))
	if err != nil {
		return nil, digest, err
	}
	info, statErr := file.Stat(ctx)
	if statErr == nil && (info == nil || !info.Mode().IsRegular()) {
		statErr = errors.New("initial lyrics candidate is not a regular file")
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

// verifyInitialLyricsPreparedAudioInput binds evidence to a current held audio
// source and its native prepared copy. Preparation's copied-input record omits a
// digest, so the copy and current source are independently hashed here.
func (stage *preparedPublicationStage) verifyInitialLyricsPreparedAudioInput(ctx context.Context) (persistence.PublicationFileIdentity, error) {
	if stage == nil || stage.rewrite || stage.hold == uuid.Nil || stage.preparationRecord.Step != "input_ready" || stage.preparationRecord.CopiedInput == nil || stage.preparationRecord.CopiedInput.SizeBytes == nil || stage.preparationRecord.CopiedInput.MTime == nil || stage.preparationRecord.InputSize == nil || stage.preparationRecord.InputMTime == nil {
		return persistence.PublicationFileIdentity{}, errors.New("initial lyrics rebuild requires a prepared held audio input")
	}
	if err := stage.verifyOutputDirectory(ctx); err != nil {
		return persistence.PublicationFileIdentity{}, err
	}
	mutation, ok := stage.directory.(sourcefs.OutputMutation)
	if !ok {
		return persistence.PublicationFileIdentity{}, errors.New("native prepared audio input reader is unavailable")
	}
	input, err := mutation.OpenExistingRegular(ctx, stage.inputName)
	if err != nil {
		return persistence.PublicationFileIdentity{}, err
	}
	info, statErr := input.Stat(ctx)
	var copiedDigest [32]byte
	if statErr == nil {
		statErr = input.Borrow(ctx, func(file *os.File) error {
			var hashErr error
			copiedDigest, hashErr = hashBorrowedFile(ctx, file)
			return hashErr
		})
	}
	statErr = errors.Join(statErr, input.Close())
	if statErr != nil {
		return persistence.PublicationFileIdentity{}, statErr
	}
	identity := publicationFileIdentity(info, copiedDigest)
	if !info.Mode().IsRegular() || !os.SameFile(info, stage.sourceFileInfo) || identity.SizeBytes == nil || identity.MTime == nil || *identity.SizeBytes != *stage.preparationRecord.CopiedInput.SizeBytes || *identity.MTime != *stage.preparationRecord.CopiedInput.MTime || *identity.SizeBytes != *stage.preparationRecord.InputSize || *stage.preparationRecord.InputMTime != stage.sourceMTime.UnixMicro() {
		return persistence.PublicationFileIdentity{}, errors.New("prepared audio input differs from durable preparation facts")
	}
	root, err := stage.opener.OpenRoot(ctx, stage.sourceRootPath)
	if err != nil {
		return persistence.PublicationFileIdentity{}, err
	}
	source, err := sourcefs.OpenRegularAt(ctx, root, stage.sourceRelativePath)
	if err != nil {
		return persistence.PublicationFileIdentity{}, errors.Join(err, root.Close())
	}
	sourceInfo, sourceErr := source.Stat(ctx)
	var sourceDigest [32]byte
	if sourceErr == nil {
		sourceErr = source.Borrow(ctx, func(file *os.File) error {
			var hashErr error
			sourceDigest, hashErr = hashBorrowedFile(ctx, file)
			return hashErr
		})
	}
	if sourceErr == nil && (!matchesSourceIdentity(sourceInfo, stage.sourceSize, stage.sourceMTime) || hexDigest(sourceDigest) != identity.SHA256) {
		sourceErr = errors.New("held audio source differs from its prepared copy")
	}
	if sourceErr == nil {
		sourceErr = verifySourceNamespace(ctx, stage.opener, root, stage.sourceRootPath)
	}
	if sourceErr == nil {
		sourceErr = verifySourceFileNamespace(ctx, root, stage.sourceRelativePath, sourceInfo)
	}
	sourceErr = errors.Join(sourceErr, source.Close(), root.Close())
	if sourceErr == nil {
		sourceErr = stage.verifyOutputDirectory(ctx)
	}
	if sourceErr == nil {
		sourceErr = stage.verifyInitialLyricsPreparedInputEntry(ctx, info)
	}
	return identity, sourceErr
}

func (stage *preparedPublicationStage) verifyInitialLyricsPreparedInputEntry(ctx context.Context, expected os.FileInfo) error {
	mutation, ok := stage.directory.(sourcefs.OutputMutation)
	if !ok {
		return errors.New("native prepared audio input verification is unavailable")
	}
	file, err := mutation.OpenExistingRegular(ctx, stage.inputName)
	if err != nil {
		return err
	}
	info, statErr := file.Stat(ctx)
	if statErr == nil && (expected == nil || info == nil || !info.Mode().IsRegular() || !os.SameFile(expected, info) || expected.Size() != info.Size() || !expected.ModTime().Equal(info.ModTime())) {
		statErr = errors.New("prepared audio input namespace changed")
	}
	return errors.Join(statErr, file.Close())
}

func hexDigest(digest [32]byte) string { return fmt.Sprintf("%x", digest[:]) }

func publicationFileIdentity(info os.FileInfo, digest [32]byte) persistence.PublicationFileIdentity {
	if info == nil {
		return persistence.PublicationFileIdentity{}
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	return persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: fmt.Sprintf("%x", digest[:])}
}

func cloneCandidateIdentity(identity *persistence.PublicationFileIdentity) *persistence.PublicationFileIdentity {
	if identity == nil {
		return nil
	}
	clone := clonePublicationIdentity(*identity)
	return &clone
}

func cloneRebuildRecord(record persistence.PublicationCandidateRebuildRecord) persistence.PublicationCandidateRebuildRecord {
	record.Candidate = cloneCandidateIdentity(record.Candidate)
	record.Input = cloneCandidateIdentity(record.Input)
	return record
}

func sameRebuildIdentity(a, b persistence.PublicationFileIdentity) bool {
	return a.SizeBytes != nil && b.SizeBytes != nil && *a.SizeBytes == *b.SizeBytes && a.MTime != nil && b.MTime != nil && *a.MTime == *b.MTime && a.SHA256 != "" && a.SHA256 == b.SHA256
}
