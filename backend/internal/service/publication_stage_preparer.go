package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// PublicationStagePreparationRepository exposes only the guarded operations
// required to stage one publication attempt.
type PublicationStagePreparationRepository interface {
	PublicationPreparationInputs(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error)
	CheckPublicationRewriteForDelivery(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationRewritePreparationFacts, bool, error)
	CheckPublicationSourceHoldForDelivery(context.Context, persistence.PublicationDeliveryGuard, uuid.UUID) (persistence.PublicationSourceHold, bool, error)
	RecordPublicationPreparation(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationPreparationRecord) (persistence.PublicationPreparationOutcome, error)
}

type publicationManualLyricsPreparationInputs interface {
	PublicationManualLyricsPreparationInputs(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error)
}

// PublicationStagePreparer copies held source material into a newly-created,
// private publication directory rooted at an already-pinned output capability.
type PublicationStagePreparer struct {
	Repository   PublicationStagePreparationRepository
	SourceOpener sourcefs.Opener
	OutputParent sourcefs.OutputDirectory
	ParentPath   string
	HoldUUID     uuid.UUID
}

// Prepare requires the caller to hold coordinator resources and to present a
// newly acquired delivery claim. It deliberately does not resume journal steps.
func (preparer PublicationStagePreparer) Prepare(ctx context.Context, guard persistence.PublicationDeliveryGuard, claim persistence.PublicationDeliveryClaimOutcome, resourcesHeld bool) (prepared PreparedPublicationStage, resultErr error) {
	return preparer.prepare(ctx, guard, claim, resourcesHeld, false)
}

// prepareManualLyricsFLAC is the private staging entry point for the supported
// manual-lyrics FLAC rewrite capability. Generic callers remain V3-ineligible.
func (preparer PublicationStagePreparer) prepareManualLyricsFLAC(ctx context.Context, guard persistence.PublicationDeliveryGuard, claim persistence.PublicationDeliveryClaimOutcome, resourcesHeld bool) (PreparedPublicationStage, error) {
	if _, ok := preparer.Repository.(publicationManualLyricsPreparationInputs); !ok {
		return nil, errors.New("manual lyrics publication preparation capability is unavailable")
	}
	return preparer.prepare(ctx, guard, claim, resourcesHeld, true)
}

// prepare is shared only at the input-staging layer. V3's manual-lyrics path
// has a separate private entry point; ordinary publication preparation must
// never admit that contract.
func (preparer PublicationStagePreparer) prepare(ctx context.Context, guard persistence.PublicationDeliveryGuard, claim persistence.PublicationDeliveryClaimOutcome, resourcesHeld bool, manualLyricsFLAC bool) (prepared PreparedPublicationStage, resultErr error) {
	if preparer.Repository == nil || preparer.SourceOpener == nil || preparer.OutputParent == nil {
		return nil, errors.New("publication stage preparation dependencies are unavailable")
	}
	if claim != persistence.PublicationDeliveryAcquired || !resourcesHeld {
		return nil, errors.New("publication delivery is not newly acquired under coordinator resources")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var input persistence.PublicationInputs
	var valid bool
	var err error
	if manualLyricsFLAC {
		reader := preparer.Repository.(publicationManualLyricsPreparationInputs)
		input, valid, err = reader.PublicationManualLyricsPreparationInputs(ctx, guard)
	} else {
		input, valid, err = preparer.Repository.PublicationPreparationInputs(ctx, guard)
	}
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, errors.New("publication delivery is stale")
	}
	if input.Version == persistence.PublicationInputsVersion3 && !manualLyricsFLAC {
		return nil, errors.New("V3 publication inputs require the private manual lyrics FLAC preparation path")
	}
	if manualLyricsFLAC && (input.Version != persistence.PublicationInputsVersion3 || input.IntentKind != "rewrite" || input.Rewrite == nil || input.ManualLyrics == nil || input.Format != publication.AudioFormatSource || input.Rewrite.AudioFacts.Container != publication.AudioContainerFLAC) {
		return nil, errors.New("private manual lyrics preparation requires a V3 FLAC rewrite")
	}
	var sourceRootPath, sourceRelativePath string
	var sourceSize int64
	var sourceMTime time.Time
	var hold uuid.UUID
	isRewrite := input.IntentKind == "rewrite"
	if isRewrite {
		facts, current, err := preparer.Repository.CheckPublicationRewriteForDelivery(ctx, guard)
		if err != nil {
			return nil, err
		}
		if !current || facts.Input.IntentKind != "rewrite" || facts.Input.Rewrite == nil || !reflect.DeepEqual(input, facts.Input) {
			return nil, errors.New("publication rewrite is stale")
		}
		rewrite := input.Rewrite
		sourceRootPath, sourceRelativePath = preparer.ParentPath, rewrite.RelativePath
		if !filepath.IsAbs(sourceRootPath) {
			return nil, errors.New("publication output parent path must be absolute")
		}
		root, err := preparer.SourceOpener.OpenRoot(ctx, sourceRootPath)
		if err != nil {
			return nil, fmt.Errorf("open current published artifact root: %w", err)
		}
		source, err := sourcefs.OpenRegularAt(ctx, root, sourceRelativePath)
		if err != nil {
			_ = root.Close()
			return nil, fmt.Errorf("open current published artifact: %w", err)
		}
		info, statErr := source.Stat(ctx)
		if statErr == nil && (info == nil || !info.Mode().IsRegular() || (rewrite.Ownership.SizeBytes != nil && info.Size() != *rewrite.Ownership.SizeBytes) || (rewrite.Ownership.MTime != nil && *rewrite.Ownership.MTime != info.ModTime().UnixMicro())) {
			statErr = errors.New("current published artifact ownership facts changed")
		}
		if statErr == nil && rewrite.Ownership.SHA256 != "" {
			var digest [sha256.Size]byte
			statErr = source.Borrow(ctx, func(file *os.File) error {
				var err error
				digest, err = hashBorrowedFile(ctx, file)
				return err
			})
			if statErr == nil && hex.EncodeToString(digest[:]) != rewrite.Ownership.SHA256 {
				statErr = errors.New("current published artifact digest changed")
			}
		}
		if statErr == nil {
			statErr = verifySourceFileNamespace(ctx, root, sourceRelativePath, info)
		}
		statErr = errors.Join(statErr, source.Close(), root.Close())
		if statErr != nil {
			return nil, statErr
		}
		sourceSize = info.Size()
		sourceMTime = info.ModTime()
	} else {
		if input.IntentKind != "audio" || input.Audio == nil || preparer.HoldUUID == uuid.Nil {
			return nil, errors.New("publication audio input is invalid")
		}
		holdFacts, current, err := preparer.Repository.CheckPublicationSourceHoldForDelivery(ctx, guard, preparer.HoldUUID)
		if err != nil {
			return nil, err
		}
		if !current || holdFacts.RelativePath != input.Audio.RelativePath || holdFacts.SizeBytes != input.Audio.SizeBytes || !holdFacts.Mtime.Equal(input.Audio.MTime) {
			return nil, errors.New("publication source hold is stale")
		}
		sourceRootPath, sourceRelativePath, sourceSize, sourceMTime, hold = input.Audio.InventoryRootPath, input.Audio.RelativePath, input.Audio.SizeBytes, input.Audio.MTime, preparer.HoldUUID
	}
	if !filepath.IsAbs(preparer.ParentPath) {
		return nil, errors.New("publication output parent path must be absolute")
	}
	if err := verifyOutputNamespace(ctx, sourcefs.NewOutputOpener(), preparer.OutputParent, preparer.ParentPath); err != nil {
		return nil, fmt.Errorf("verify publication output parent namespace: %w", err)
	}
	parentIdentity, err := preparer.OutputParent.Identity(ctx)
	if err != nil {
		return nil, fmt.Errorf("identify publication output parent: %w", err)
	}
	owner := uuid.New()
	privateName := guard.OperationID.String() + "-" + guard.ClaimUUID.String()
	ext := publicationStageExtension(input)
	outputExt := publicationOutputStageExtension(input)
	if ext == "" || outputExt == "" {
		return nil, errors.New("publication stage container is unsupported")
	}
	inputName, outputName := "input"+ext, "output"+outputExt
	privateRel := privateName
	record := persistence.PublicationPreparationRecord{Version: persistence.PublicationPreparationVersion, Kind: "publication_preparation", Step: "intent", OperationID: guard.OperationID, ClaimUUID: guard.ClaimUUID, OwnerToken: owner, PrivateDirRel: privateRel, InputRel: privateRel + "/" + inputName, OutputRel: privateRel + "/" + outputName, ParentIdentity: persistence.PublicationDirectoryIdentity{Platform: parentIdentity.Platform, Volume: parentIdentity.Volume, FileID: parentIdentity.FileID}}
	outcome, err := preparer.Repository.RecordPublicationPreparation(ctx, guard, record)
	if err != nil {
		return nil, err
	}
	if outcome != persistence.PublicationPreparationRecorded {
		return nil, errors.New("publication preparation intent already exists; recovery required")
	}
	dir, err := preparer.OutputParent.CreateDirExclusive(ctx, privateName)
	if err != nil {
		return nil, fmt.Errorf("create private publication directory: %w", err)
	}
	transferred := false
	defer func() {
		if !transferred && dir != nil {
			if closeErr := dir.Close(); closeErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("close private publication directory: %w", closeErr))
			}
		}
	}()
	dirIdentity, err := dir.Identity(ctx)
	if err != nil {
		return nil, err
	}
	dirID := persistence.PublicationDirectoryIdentity{Platform: dirIdentity.Platform, Volume: dirIdentity.Volume, FileID: dirIdentity.FileID}
	created := record
	created.Step = "directory_created"
	created.DirectoryIdentity = &dirID
	outcome, err = preparer.Repository.RecordPublicationPreparation(ctx, guard, created)
	if err != nil {
		return nil, err
	}
	if outcome != persistence.PublicationPreparationRecorded {
		return nil, errors.New("publication directory journal replay; recovery required")
	}
	root, err := preparer.SourceOpener.OpenRoot(ctx, sourceRootPath)
	if err != nil {
		return nil, fmt.Errorf("open held publication source root: %w", err)
	}
	rootClosed := false
	defer func() {
		if !rootClosed {
			resultErr = errors.Join(resultErr, root.Close())
		}
	}()
	source, err := sourcefs.OpenRegularAt(ctx, root, sourceRelativePath)
	if err != nil {
		return nil, fmt.Errorf("open held publication source: %w", err)
	}
	sourceClosed := false
	defer func() {
		if !sourceClosed {
			resultErr = errors.Join(resultErr, source.Close())
		}
	}()
	sourceInfo, err := source.Stat(ctx)
	if err != nil || !matchesSourceIdentity(sourceInfo, sourceSize, sourceMTime) {
		if err != nil {
			return nil, fmt.Errorf("stat held publication source: %w", err)
		}
		return nil, errors.New("held publication source facts changed")
	}
	rewriteUsed := input.Rewrite != nil
	if rewriteUsed {
		if rewrite := input.Rewrite; rewrite.Ownership.SizeBytes != nil && sourceInfo.Size() != *rewrite.Ownership.SizeBytes {
			return nil, errors.New("current published artifact size changed before copy")
		}
		if input.Rewrite.Ownership.MTime != nil && *input.Rewrite.Ownership.MTime != sourceInfo.ModTime().UnixMicro() {
			return nil, errors.New("current published artifact modification time changed before copy")
		}
		if input.Rewrite.Ownership.SHA256 != "" {
			var actual string
			if err := source.Borrow(ctx, func(file *os.File) error {
				digest, err := hashBorrowedFile(ctx, file)
				actual = hex.EncodeToString(digest[:])
				return err
			}); err != nil {
				return nil, err
			}
			if actual != input.Rewrite.Ownership.SHA256 {
				return nil, errors.New("current published artifact digest changed before copy")
			}
		}
	}
	if err := verifySourceNamespace(ctx, preparer.SourceOpener, root, sourceRootPath); err != nil {
		return nil, fmt.Errorf("verify held publication source root namespace: %w", err)
	}
	if err := verifySourceFileNamespace(ctx, root, sourceRelativePath, sourceInfo); err != nil {
		return nil, fmt.Errorf("verify held publication source namespace: %w", err)
	}
	if !isRewrite {
		if _, valid, err := preparer.Repository.CheckPublicationSourceHoldForDelivery(ctx, guard, hold); err != nil {
			return nil, err
		} else if !valid {
			return nil, errors.New("publication source hold is stale before copy")
		}
	} else if _, valid, err := preparer.Repository.CheckPublicationRewriteForDelivery(ctx, guard); err != nil {
		return nil, err
	} else if !valid {
		return nil, errors.New("publication rewrite is stale before copy")
	}
	destination, err := dir.CreateExclusive(ctx, inputName)
	if err != nil {
		return nil, fmt.Errorf("create publication input: %w", err)
	}
	destinationClosed := false
	defer func() {
		if !destinationClosed {
			resultErr = errors.Join(resultErr, destination.Close())
		}
	}()
	_, copyErr := copySourceOnce(ctx, source, destination, sourceSize)
	copyErr = errors.Join(copyErr, destination.Sync(ctx))
	after, statErr := source.Stat(ctx)
	if copyErr == nil && (statErr != nil || !matchesSourceIdentity(after, sourceSize, sourceMTime)) {
		copyErr = errors.New("publication source changed while copying")
	}
	if copyErr == nil {
		if namespaceErr := verifySourceNamespace(ctx, preparer.SourceOpener, root, sourceRootPath); namespaceErr != nil {
			copyErr = fmt.Errorf("verify held publication source root after copy: %w", namespaceErr)
		} else if namespaceErr = verifySourceFileNamespace(ctx, root, sourceRelativePath, sourceInfo); namespaceErr != nil {
			copyErr = fmt.Errorf("verify held publication source after copy: %w", namespaceErr)
		}
	}
	if copyErr == nil {
		if isRewrite {
			_, valid, guardErr := preparer.Repository.CheckPublicationRewriteForDelivery(ctx, guard)
			if guardErr != nil {
				copyErr = guardErr
			} else if !valid {
				copyErr = errors.New("publication rewrite is stale after copy")
			}
		} else if _, valid, holdErr := preparer.Repository.CheckPublicationSourceHoldForDelivery(ctx, guard, hold); holdErr != nil {
			copyErr = holdErr
		} else if !valid {
			copyErr = errors.New("publication source hold is stale after copy")
		}
	}
	inputInfo, inputStatErr := destination.Stat(ctx)
	copyErr = errors.Join(copyErr, inputStatErr)
	copyErr = errors.Join(copyErr, destination.Close())
	destinationClosed = true
	copyErr = errors.Join(copyErr, source.Close())
	sourceClosed = true
	copyErr = errors.Join(copyErr, root.Close())
	rootClosed = true
	if copyErr != nil {
		return nil, fmt.Errorf("copy publication source: %w", copyErr)
	}
	if inputInfo == nil || !inputInfo.Mode().IsRegular() || inputInfo.Size() != sourceSize {
		return nil, errors.New("copied publication input facts changed")
	}
	copiedSize, copiedMTime := inputInfo.Size(), inputInfo.ModTime().UnixMicro()
	if err := verifyPublicationDirectoryNamespace(ctx, preparer.OutputParent, privateName, dir, dirIdentity, preparer.SourceOpener, filepath.Join(preparer.ParentPath, privateName)); err != nil {
		return nil, err
	}
	if err := verifyOutputNamespace(ctx, sourcefs.NewOutputOpener(), preparer.OutputParent, preparer.ParentPath); err != nil {
		return nil, err
	}
	size, mtime := sourceSize, sourceMTime.UnixMicro()
	ready := created
	ready.Step = "input_ready"
	ready.InputSize = &size
	ready.InputMTime = &mtime
	ready.CopiedInput = &persistence.PublicationFileIdentity{SizeBytes: &copiedSize, MTime: &copiedMTime}
	outcome, err = preparer.Repository.RecordPublicationPreparation(ctx, guard, ready)
	if err != nil {
		return nil, err
	}
	if outcome != persistence.PublicationPreparationRecorded {
		return nil, errors.New("publication input journal replay; recovery required")
	}
	var rewriteIdentity persistence.PublicationFileIdentity
	if input.Rewrite != nil {
		rewriteIdentity = clonePublicationIdentity(input.Rewrite.Ownership)
	}
	stage := &preparedPublicationStage{paths: PublicationStagePaths{InputPath: filepath.Join(preparer.ParentPath, privateName, inputName), OutputPath: filepath.Join(preparer.ParentPath, privateName, outputName), RelativePath: record.OutputRel}, parent: preparer.OutputParent, directory: dir, directoryName: privateName, identity: dirID, parentIdentity: parentIdentity, repository: preparer.Repository, opener: preparer.SourceOpener, sourceRootPath: sourceRootPath, sourceRelativePath: sourceRelativePath, sourceSize: sourceSize, sourceMTime: sourceMTime, sourceFileInfo: inputInfo, inputPath: filepath.Join(preparer.ParentPath, privateName), inputName: inputName, guard: guard, hold: hold, rewrite: isRewrite, rewriteIdentity: rewriteIdentity, preparationRecord: ready}
	// Transfer directory ownership only after all preparation observations have been journaled.
	transferred = true
	return stage, nil
}

func clonePublicationIdentity(identity persistence.PublicationFileIdentity) persistence.PublicationFileIdentity {
	if identity.SizeBytes != nil {
		value := *identity.SizeBytes
		identity.SizeBytes = &value
	}
	if identity.MTime != nil {
		value := *identity.MTime
		identity.MTime = &value
	}
	return identity
}

func hashBorrowedFile(ctx context.Context, file *os.File) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if _, err := file.Seek(0, 0); err != nil {
		return digest, err
	}
	hash := sha256.New()
	buffer := make([]byte, sourceAnalysisCopyBufferSize)
	var readErr error
	for {
		if err := ctx.Err(); err != nil {
			readErr = err
			break
		}
		count, err := file.Read(buffer)
		if count > 0 {
			_, _ = hash.Write(buffer[:count])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			readErr = err
			break
		}
	}
	_, seekErr := file.Seek(0, 0)
	copy(digest[:], hash.Sum(nil))
	return digest, errors.Join(readErr, seekErr)
}

func publicationStageExtension(input persistence.PublicationInputs) string {
	var facts persistence.PersistedAudioFacts
	if input.Rewrite != nil {
		facts = input.Rewrite.AudioFacts
	} else if input.Audio != nil {
		facts = input.Audio.AudioFacts
	}
	// The staged input is the verified container itself. Its canonical suffix
	// depends on whether we know how to demux that container, not whether the
	// container is eligible to be published unchanged.
	if demuxer, _ := publication.InputDemuxer(facts.Container); demuxer == "" {
		return ""
	}
	return "." + string(facts.Container)
}

func publicationOutputStageExtension(input persistence.PublicationInputs) string {
	format := input.Format
	if input.Rewrite != nil {
		format = publication.AudioFormatSource
	}
	plan, err := publication.PlanAudioTransform(format, inputAudioFacts(input).AudioSourceFacts())
	if err != nil {
		return ""
	}
	return "." + plan.Extension
}

type preparedPublicationStage struct {
	paths              PublicationStagePaths
	parent, directory  sourcefs.OutputDirectory
	directoryName      string
	identity           persistence.PublicationDirectoryIdentity
	parentIdentity     sourcefs.DirectoryIdentity
	repository         PublicationStagePreparationRepository
	opener             sourcefs.Opener
	sourceRootPath     string
	sourceRelativePath string
	sourceSize         int64
	sourceMTime        time.Time
	sourceFileInfo     os.FileInfo
	rewriteIdentity    persistence.PublicationFileIdentity
	preparationRecord  persistence.PublicationPreparationRecord
	inputPath          string
	inputName          string
	guard              persistence.PublicationDeliveryGuard
	hold               uuid.UUID
	rewrite            bool
	mu                 sync.Mutex
	closed             bool
}

func (stage *preparedPublicationStage) Paths() PublicationStagePaths { return stage.paths }
func (stage *preparedPublicationStage) Validate(ctx context.Context) error {
	stage.mu.Lock()
	defer stage.mu.Unlock()
	if stage.closed {
		return os.ErrClosed
	}
	if err := stage.parent.Verify(ctx); err != nil {
		return err
	}
	if err := verifyPublicationDirectoryNamespace(ctx, stage.parent, stage.directoryName, stage.directory, sourcefs.DirectoryIdentity{Platform: stage.identity.Platform, Volume: stage.identity.Volume, FileID: stage.identity.FileID}, stage.opener, stage.inputPath); err != nil {
		return err
	}
	id, err := stage.directory.Identity(ctx)
	if err != nil {
		return err
	}
	if id.Platform != stage.identity.Platform || id.Volume != stage.identity.Volume || id.FileID != stage.identity.FileID {
		return errors.New("publication stage directory ownership changed")
	}
	inputRoot, err := stage.opener.OpenRoot(ctx, stage.inputPath)
	if err != nil {
		return fmt.Errorf("open prepared publication input directory: %w", err)
	}
	inputRootInfo, rootStatErr := inputRoot.Stat(ctx)
	directoryInfo, directoryStatErr := stage.directory.Stat(ctx)
	if rootStatErr == nil && directoryStatErr == nil && !os.SameFile(inputRootInfo, directoryInfo) {
		rootStatErr = errors.New("prepared publication input directory namespace changed")
	}
	if rootStatErr != nil || directoryStatErr != nil {
		return errors.Join(rootStatErr, directoryStatErr, inputRoot.Close())
	}
	input, err := sourcefs.OpenRegularAt(ctx, inputRoot, stage.inputName)
	if err != nil {
		return errors.Join(err, inputRoot.Close())
	}
	inputInfo, statErr := input.Stat(ctx)
	if statErr == nil && (inputInfo.Size() != stage.sourceSize || !os.SameFile(stage.sourceFileInfo, inputInfo) || !sameSourceMtime(stage.sourceFileInfo.ModTime(), inputInfo.ModTime())) {
		statErr = errors.New("prepared publication input namespace or facts changed")
	}
	if statErr == nil {
		statErr = verifySourceFileNamespace(ctx, inputRoot, stage.inputName, inputInfo)
	}
	if statErr == nil && stage.rewriteIdentity.SHA256 != "" {
		var digest [sha256.Size]byte
		statErr = input.Borrow(ctx, func(file *os.File) error {
			var err error
			digest, err = hashBorrowedFile(ctx, file)
			return err
		})
		if statErr == nil && hex.EncodeToString(digest[:]) != stage.rewriteIdentity.SHA256 {
			statErr = errors.New("prepared publication input digest changed")
		}
	}
	if statErr == nil {
		after, err := input.Stat(ctx)
		if err != nil {
			statErr = err
		} else if after.Size() != inputInfo.Size() || !sameSourceMtime(after.ModTime(), inputInfo.ModTime()) {
			statErr = errors.New("prepared publication input facts changed while hashing")
		} else {
			statErr = verifySourceFileNamespace(ctx, inputRoot, stage.inputName, after)
		}
	}
	statErr = errors.Join(statErr, input.Close(), inputRoot.Close())
	if statErr != nil {
		return statErr
	}
	if stage.rewrite {
		_, valid, err := stage.repository.CheckPublicationRewriteForDelivery(ctx, stage.guard)
		if err != nil {
			return err
		}
		if !valid {
			return errors.New("publication rewrite is stale")
		}
	} else {
		_, valid, err := stage.repository.CheckPublicationSourceHoldForDelivery(ctx, stage.guard, stage.hold)
		if err != nil {
			return err
		}
		if !valid {
			return errors.New("publication source hold is stale")
		}
	}
	root, err := stage.opener.OpenRoot(ctx, stage.sourceRootPath)
	if err != nil {
		return fmt.Errorf("reopen held publication source root: %w", err)
	}
	if err := verifySourceNamespace(ctx, stage.opener, root, stage.sourceRootPath); err != nil {
		return errors.Join(err, root.Close())
	}
	file, err := sourcefs.OpenRegularAt(ctx, root, stage.sourceRelativePath)
	if err != nil {
		return errors.Join(err, root.Close())
	}
	info, statErr := file.Stat(ctx)
	if statErr == nil && !matchesSourceIdentity(info, stage.sourceSize, stage.sourceMTime) {
		statErr = errors.New("held publication source facts changed")
	}
	if statErr == nil {
		statErr = verifySourceFileNamespace(ctx, root, stage.sourceRelativePath, info)
	}
	if statErr == nil {
		statErr = stage.parent.VerifyChild(ctx, stage.directoryName, sourcefs.DirectoryIdentity{Platform: stage.identity.Platform, Volume: stage.identity.Volume, FileID: stage.identity.FileID})
	}
	if statErr == nil && stage.rewrite {
		_, valid, guardErr := stage.repository.CheckPublicationRewriteForDelivery(ctx, stage.guard)
		if guardErr != nil {
			statErr = guardErr
		} else if !valid {
			statErr = errors.New("publication rewrite is stale during validation")
		}
	}
	return errors.Join(statErr, file.Close(), root.Close())
}
func (stage *preparedPublicationStage) Close() error {
	stage.mu.Lock()
	defer stage.mu.Unlock()
	if stage.closed {
		return nil
	}
	stage.closed = true
	return errors.Join(stage.directory.Close())
}

func (stage *preparedPublicationStage) firstAbsentCleanupWitness(ctx context.Context) (persistence.PublicationFirstAbsentCleanupWitness, sourcefs.OutputCleanup, sourcefs.OutputCleanup, error) {
	inputCleanup, inputOK := stage.directory.(sourcefs.OutputCleanup)
	directoryCleanup, directoryOK := stage.parent.(sourcefs.OutputCleanup)
	if !inputOK || !directoryOK {
		return persistence.PublicationFirstAbsentCleanupWitness{}, nil, nil, errors.New("native publication cleanup capability is unavailable")
	}
	if stage.sourceFileInfo == nil || !stage.sourceFileInfo.Mode().IsRegular() {
		return persistence.PublicationFirstAbsentCleanupWitness{}, nil, nil, errors.New("prepared publication input identity is unavailable")
	}
	mutation, ok := stage.directory.(sourcefs.OutputMutation)
	if !ok {
		return persistence.PublicationFirstAbsentCleanupWitness{}, nil, nil, errors.New("native publication mutation capability is unavailable")
	}
	stagedOutput, err := mutation.OpenExistingRegular(ctx, filepath.Base(stage.paths.OutputPath))
	if err == nil {
		closeErr := stagedOutput.Close()
		return persistence.PublicationFirstAbsentCleanupWitness{}, nil, nil, errors.Join(errors.New("staged publication output was not renamed"), closeErr)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return persistence.PublicationFirstAbsentCleanupWitness{}, nil, nil, fmt.Errorf("verify staged publication output was renamed: %w", err)
	}
	inputSize, inputMTime := stage.sourceFileInfo.Size(), stage.sourceFileInfo.ModTime().UnixMicro()
	inputRel := path.Join(stage.directoryName, stage.inputName)
	witness := persistence.PublicationFirstAbsentCleanupWitness{
		PrivateDirRel: stage.directoryName,
		InputRel:      inputRel,
		Directory:     persistence.PublicationDirectoryIdentity{Platform: stage.identity.Platform, Volume: stage.identity.Volume, FileID: stage.identity.FileID},
		Input:         persistence.PublicationFileIdentity{SizeBytes: &inputSize, MTime: &inputMTime},
	}
	if stage.parentIdentity.Platform == "" {
		return persistence.PublicationFirstAbsentCleanupWitness{}, nil, nil, errors.New("publication parent identity is unavailable")
	}
	return witness, inputCleanup, directoryCleanup, nil
}

func verifyPublicationDirectoryNamespace(ctx context.Context, parent sourcefs.OutputDirectory, name string, pinned sourcefs.OutputDirectory, expected sourcefs.DirectoryIdentity, opener sourcefs.Opener, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := parent.VerifyChild(ctx, name, expected); err != nil {
		return err
	}
	current, err := opener.OpenRoot(ctx, path)
	if err != nil {
		return err
	}
	pinnedInfo, pinnedErr := pinned.Stat(ctx)
	currentInfo, currentErr := current.Stat(ctx)
	if pinnedErr == nil && currentErr == nil && !os.SameFile(pinnedInfo, currentInfo) {
		currentErr = errors.New("publication stage directory namespace changed")
	}
	return errors.Join(pinnedErr, currentErr, current.Close(), parent.VerifyChild(ctx, name, expected))
}
