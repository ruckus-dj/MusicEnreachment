package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// PublicationStagePreparationRepository exposes only the guarded operations
// required to stage one publication attempt.
type PublicationStagePreparationRepository interface {
	PublicationPreparationInputs(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error)
	CheckPublicationSourceHoldForDelivery(context.Context, persistence.PublicationDeliveryGuard, uuid.UUID) (persistence.PublicationSourceHold, bool, error)
	RecordPublicationPreparation(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationPreparationRecord) (persistence.PublicationPreparationOutcome, error)
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
	if preparer.Repository == nil || preparer.SourceOpener == nil || preparer.OutputParent == nil || preparer.HoldUUID == uuid.Nil {
		return nil, errors.New("publication stage preparation dependencies are unavailable")
	}
	if claim != persistence.PublicationDeliveryAcquired || !resourcesHeld {
		return nil, errors.New("publication delivery is not newly acquired under coordinator resources")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input, valid, err := preparer.Repository.PublicationPreparationInputs(ctx, guard)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, errors.New("publication delivery is stale")
	}
	if input.IntentKind != "audio" || input.Audio == nil {
		return nil, errors.New("rewrite stage preparation is unsupported")
	}
	hold, valid, err := preparer.Repository.CheckPublicationSourceHoldForDelivery(ctx, guard, preparer.HoldUUID)
	if err != nil {
		return nil, err
	}
	if !valid || hold.RelativePath != input.Audio.RelativePath || hold.SizeBytes != input.Audio.SizeBytes || !hold.Mtime.Equal(input.Audio.MTime) {
		return nil, errors.New("publication source hold is stale")
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
	inputName, outputName := "input"+ext, "output"+ext
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
	root, err := preparer.SourceOpener.OpenRoot(ctx, input.Audio.InventoryRootPath)
	if err != nil {
		return nil, fmt.Errorf("open held publication source root: %w", err)
	}
	rootClosed := false
	defer func() {
		if !rootClosed {
			resultErr = errors.Join(resultErr, root.Close())
		}
	}()
	source, err := sourcefs.OpenRegularAt(ctx, root, input.Audio.RelativePath)
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
	if err != nil || !matchesSourceIdentity(sourceInfo, input.Audio.SizeBytes, input.Audio.MTime) {
		if err != nil {
			return nil, fmt.Errorf("stat held publication source: %w", err)
		}
		return nil, errors.New("held publication source facts changed")
	}
	if err := verifySourceNamespace(ctx, preparer.SourceOpener, root, input.Audio.InventoryRootPath); err != nil {
		return nil, fmt.Errorf("verify held publication source root namespace: %w", err)
	}
	if err := verifySourceFileNamespace(ctx, root, input.Audio.RelativePath, sourceInfo); err != nil {
		return nil, fmt.Errorf("verify held publication source namespace: %w", err)
	}
	if _, valid, err := preparer.Repository.CheckPublicationSourceHoldForDelivery(ctx, guard, preparer.HoldUUID); err != nil {
		return nil, err
	} else if !valid {
		return nil, errors.New("publication source hold is stale before copy")
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
	_, copyErr := copySourceOnce(ctx, source, destination, input.Audio.SizeBytes)
	copyErr = errors.Join(copyErr, destination.Sync(ctx))
	after, statErr := source.Stat(ctx)
	if copyErr == nil && (statErr != nil || !matchesSourceIdentity(after, input.Audio.SizeBytes, input.Audio.MTime)) {
		copyErr = errors.New("publication source changed while copying")
	}
	if copyErr == nil {
		if namespaceErr := verifySourceNamespace(ctx, preparer.SourceOpener, root, input.Audio.InventoryRootPath); namespaceErr != nil {
			copyErr = fmt.Errorf("verify held publication source root after copy: %w", namespaceErr)
		} else if namespaceErr = verifySourceFileNamespace(ctx, root, input.Audio.RelativePath, sourceInfo); namespaceErr != nil {
			copyErr = fmt.Errorf("verify held publication source after copy: %w", namespaceErr)
		}
	}
	if copyErr == nil {
		if _, valid, holdErr := preparer.Repository.CheckPublicationSourceHoldForDelivery(ctx, guard, preparer.HoldUUID); holdErr != nil {
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
	if inputInfo == nil || !inputInfo.Mode().IsRegular() || inputInfo.Size() != input.Audio.SizeBytes {
		return nil, errors.New("copied publication input facts changed")
	}
	if err := verifyPublicationDirectoryNamespace(ctx, preparer.OutputParent, privateName, dir, dirIdentity, preparer.SourceOpener, filepath.Join(preparer.ParentPath, privateName)); err != nil {
		return nil, err
	}
	if err := verifyOutputNamespace(ctx, sourcefs.NewOutputOpener(), preparer.OutputParent, preparer.ParentPath); err != nil {
		return nil, err
	}
	size, mtime := input.Audio.SizeBytes, input.Audio.MTime.UnixMicro()
	ready := created
	ready.Step = "input_ready"
	ready.InputSize = &size
	ready.InputMTime = &mtime
	outcome, err = preparer.Repository.RecordPublicationPreparation(ctx, guard, ready)
	if err != nil {
		return nil, err
	}
	if outcome != persistence.PublicationPreparationRecorded {
		return nil, errors.New("publication input journal replay; recovery required")
	}
	stage := &preparedPublicationStage{paths: PublicationStagePaths{InputPath: filepath.Join(preparer.ParentPath, privateName, inputName), OutputPath: filepath.Join(preparer.ParentPath, privateName, outputName), RelativePath: input.Audio.RelativePath}, parent: preparer.OutputParent, directory: dir, directoryName: privateName, identity: dirID, repository: preparer.Repository, opener: preparer.SourceOpener, sourceRootPath: input.Audio.InventoryRootPath, sourceRelativePath: input.Audio.RelativePath, sourceSize: input.Audio.SizeBytes, sourceMTime: input.Audio.MTime, sourceFileInfo: inputInfo, inputPath: filepath.Join(preparer.ParentPath, privateName), inputName: inputName, guard: guard, hold: preparer.HoldUUID}
	// Transfer directory ownership only after all preparation observations have been journaled.
	transferred = true
	return stage, nil
}

func publicationStageExtension(input persistence.PublicationInputs) string {
	switch input.Format {
	case "mka":
		return ".mka"
	case "m4a":
		return ".m4a"
	default:
		return filepath.Ext(input.Audio.RelativePath)
	}
}

type preparedPublicationStage struct {
	paths              PublicationStagePaths
	parent, directory  sourcefs.OutputDirectory
	directoryName      string
	identity           persistence.PublicationDirectoryIdentity
	repository         PublicationStagePreparationRepository
	opener             sourcefs.Opener
	sourceRootPath     string
	sourceRelativePath string
	sourceSize         int64
	sourceMTime        time.Time
	sourceFileInfo     os.FileInfo
	inputPath          string
	inputName          string
	guard              persistence.PublicationDeliveryGuard
	hold               uuid.UUID
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
	statErr = errors.Join(statErr, input.Close(), inputRoot.Close())
	if statErr != nil {
		return statErr
	}
	_, valid, err := stage.repository.CheckPublicationSourceHoldForDelivery(ctx, stage.guard, stage.hold)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("publication source hold is stale")
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
