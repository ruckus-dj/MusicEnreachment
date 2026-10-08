package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

const sourceAnalysisCopyBufferSize = 128 * 1024

type sourceAnalysisInputRepository interface {
	GetNormalizedSourceAnalysisWork(context.Context, uuid.UUID) (*persistence.SourceAnalysisWork, *persistence.SourceLocation, error)
	GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error)
}

type sourceAnalysisArtifactRepository interface {
	Acquire(context.Context, uuid.UUID, persistence.SourceAnalysisArtifactFence) (*persistence.SourceAnalysisArtifact, error)
	MarkReady(context.Context, uuid.UUID, persistence.SourceAnalysisArtifactFence, int64) (*persistence.SourceAnalysisArtifact, error)
	ForgetUncreated(context.Context, uuid.UUID, persistence.SourceAnalysisArtifactFence) error
}

type sourceAnalysisOutputDirectoryReader interface {
	GetOutputDirectory(context.Context) (string, bool, error)
}

// SourceAnalysisPreparedInput pins one source identity and, in staged mode, its
// completed owned copy. Close releases handles; artifact cleanup is separate.
type SourceAnalysisPreparedInput struct {
	File       sourcefs.RegularFile
	ServerPath string
	ArtifactID *uuid.UUID
	validate   func(context.Context) error
	close      func() error
}

type SourceAnalysisInputPreparing interface {
	PrepareMode(context.Context, persistence.SourceAnalysisArtifactFence, string) (*SourceAnalysisPreparedInput, error)
}

func (input *SourceAnalysisPreparedInput) Validate(ctx context.Context) error {
	if input == nil || input.validate == nil {
		return fmt.Errorf("prepared source analysis input is unavailable")
	}
	return input.validate(ctx)
}

func (input *SourceAnalysisPreparedInput) Close() error {
	if input == nil || input.close == nil {
		return nil
	}
	close := input.close
	input.close = nil
	return close()
}

// SourceAnalysisInputPreparer makes a single sequential, fenced copy of the
// authoritative inventory input into the current managed output namespace.
type SourceAnalysisInputPreparer struct {
	repository sourceAnalysisInputRepository
	artifacts  sourceAnalysisArtifactRepository
	settings   sourceAnalysisOutputDirectoryReader
	sourceOpen sourcefs.Opener
	outputOpen sourcefs.OutputOpener
	newID      func() uuid.UUID
}

func NewSourceAnalysisInputPreparer(repository sourceAnalysisInputRepository, artifacts sourceAnalysisArtifactRepository, settings sourceAnalysisOutputDirectoryReader, sourceOpen sourcefs.Opener, outputOpen sourcefs.OutputOpener) *SourceAnalysisInputPreparer {
	if sourceOpen == nil {
		sourceOpen = sourcefs.NewOpener()
	}
	if outputOpen == nil {
		outputOpen = sourcefs.NewOutputOpener()
	}
	return &SourceAnalysisInputPreparer{repository: repository, artifacts: artifacts, settings: settings, sourceOpen: sourceOpen, outputOpen: outputOpen, newID: uuid.New}
}

// Prepare resolves all input paths from current persistence/settings state. The
// fence must identify the currently running delivery; the repository validates
// it again atomically at acquire and ready transitions.
func (preparer *SourceAnalysisInputPreparer) Prepare(ctx context.Context, fence persistence.SourceAnalysisArtifactFence) (*SourceAnalysisPreparedInput, error) {
	return preparer.PrepareMode(ctx, fence, "staged")
}

// PrepareMode uses the mode captured by the running work execution.
func (preparer *SourceAnalysisInputPreparer) PrepareMode(ctx context.Context, fence persistence.SourceAnalysisArtifactFence, mode string) (*SourceAnalysisPreparedInput, error) {
	if fence.WorkID == uuid.Nil || fence.OperationID == uuid.Nil || fence.OperationAttempt <= 0 || fence.JobID <= 0 {
		return nil, fmt.Errorf("prepare source analysis input: valid delivery fence is required")
	}
	work, location, err := preparer.repository.GetNormalizedSourceAnalysisWork(ctx, fence.WorkID)
	if err != nil {
		return nil, fmt.Errorf("prepare source analysis input: read work and location: %w", err)
	}
	if work == nil || location == nil || work.ID != fence.WorkID {
		return nil, fmt.Errorf("prepare source analysis input: current work and location are required")
	}
	root, err := preparer.repository.GetSourceRoot(ctx, work.SourceRootID)
	if err != nil {
		return nil, fmt.Errorf("prepare source analysis input: read source root: %w", err)
	}
	if root == nil || !root.Enabled || root.Stale() || root.InventoryPath == nil ||
		*root.InventoryPath != root.ConfiguredPath || work.ConfiguredPath != root.ConfiguredPath || work.InventoryPath != *root.InventoryPath ||
		work.SourceRootID != root.ID || location.ID != work.LocationID || location.SourceRootID != root.ID ||
		location.RelativePath != work.RelativePath || location.SizeBytes != work.SizeBytes || !sameSourceMtime(location.Mtime, work.Mtime) {
		return nil, fmt.Errorf("prepare source analysis input: source work is not current")
	}
	if mode == "in_place" {
		pinnedRoot, openErr := preparer.sourceOpen.OpenRoot(ctx, work.InventoryPath)
		if openErr != nil {
			return nil, fmt.Errorf("open pinned inventory root: %w", openErr)
		}
		file, openErr := sourcefs.OpenRegularAt(ctx, pinnedRoot, work.RelativePath)
		if openErr != nil {
			_ = pinnedRoot.Close()
			return nil, fmt.Errorf("open pinned inventory file: %w", openErr)
		}
		info, statErr := file.Stat(ctx)
		if statErr != nil || !matchesSourceIdentity(info, work.SizeBytes, work.Mtime) {
			_ = file.Close()
			_ = pinnedRoot.Close()
			if statErr != nil {
				return nil, fmt.Errorf("stat source analysis input: %w", statErr)
			}
			return nil, fmt.Errorf("source analysis input changed")
		}
		input := &SourceAnalysisPreparedInput{File: file, ServerPath: filepath.Join(work.InventoryPath, filepath.FromSlash(work.RelativePath))}
		input.validate = func(ctx context.Context) error {
			current, err := file.Stat(ctx)
			if err != nil {
				return err
			}
			if !matchesSourceIdentity(current, work.SizeBytes, work.Mtime) {
				return fmt.Errorf("source analysis input changed")
			}
			if err := verifySourceNamespace(ctx, preparer.sourceOpen, pinnedRoot, work.InventoryPath); err != nil {
				return err
			}
			return verifySourceFileNamespace(ctx, pinnedRoot, work.RelativePath, current)
		}
		input.close = func() error { return errors.Join(file.Close(), pinnedRoot.Close()) }
		return input, nil
	}
	if mode != "staged" {
		return nil, fmt.Errorf("prepare source analysis input: unsupported processing mode")
	}
	outputPath, configured, err := preparer.settings.GetOutputDirectory(ctx)
	if err != nil {
		return nil, fmt.Errorf("prepare source analysis input: read output directory: %w", err)
	}
	if !configured || outputPath == "" || !filepath.IsAbs(outputPath) {
		return nil, fmt.Errorf("prepare source analysis input: managed output directory is not configured")
	}
	artifactID := preparer.newID()
	artifact, err := preparer.artifacts.Acquire(ctx, artifactID, fence)
	if err != nil {
		return nil, fmt.Errorf("prepare source analysis input: acquire staged artifact: %w", err)
	}
	expectedRelativePath := filepath.ToSlash(filepath.Join("analysis", "staging", root.ID.String(), work.ID.String(), artifactID.String()))
	if artifact == nil || artifact.ID != artifactID || artifact.WorkID != work.ID || artifact.RelativeOutputPath != expectedRelativePath ||
		artifact.SourceSizeBytes != work.SizeBytes || !sameSourceMtime(artifact.SourceMtime, work.Mtime) ||
		artifact.OwnerOperationID != fence.OperationID || artifact.OwnerOperationAttempt != fence.OperationAttempt || artifact.OwnerJobID != fence.JobID ||
		artifact.State != persistence.SourceAnalysisArtifactAcquiring {
		return nil, fmt.Errorf("prepare source analysis input: artifact acquisition returned an invalid owner")
	}

	forgetUncreated := func(primary error) error {
		if forgetErr := preparer.artifacts.ForgetUncreated(ctx, artifactID, fence); forgetErr != nil {
			return errors.Join(primary, fmt.Errorf("forget uncreated source analysis artifact: %w", forgetErr))
		}
		return primary
	}
	outputRoot, err := preparer.outputOpen.OpenRoot(ctx, outputPath)
	if err != nil {
		return nil, forgetUncreated(fmt.Errorf("open managed output directory: %w", err))
	}
	ownedDirs := []sourcefs.OutputDirectory{outputRoot}
	closeDirs := func() error {
		var closeErr error
		for i := len(ownedDirs) - 1; i >= 0; i-- {
			closeErr = errors.Join(closeErr, ownedDirs[i].Close())
		}
		return closeErr
	}
	currentDir := outputRoot
	for _, part := range []string{"analysis", "staging", root.ID.String(), work.ID.String()} {
		next, openErr := currentDir.OpenOrCreateDir(ctx, part)
		if openErr != nil {
			_ = closeDirs()
			return nil, forgetUncreated(fmt.Errorf("create staged output directory: %w", openErr))
		}
		ownedDirs = append(ownedDirs, next)
		currentDir = next
	}
	outputFile, err := currentDir.CreateExclusive(ctx, artifactID.String())
	if err != nil {
		_ = closeDirs()
		createErr := fmt.Errorf("create staged output file exclusively: %w", err)
		if errors.Is(err, fs.ErrExist) || os.IsExist(err) {
			return nil, forgetUncreated(createErr)
		}
		// A platform adapter can fail after the exclusive create has taken effect
		// (for example while validating the new handle). Without a returned handle
		// we cannot prove the path is absent, so preserve its ownership row.
		return nil, createErr
	}
	created := true
	var sourceRoot sourcefs.Directory
	var sourceFile sourcefs.RegularFile
	closeEverything := func() error {
		var closeErr error
		if outputFile != nil {
			closeErr = errors.Join(closeErr, outputFile.Close())
		}
		closeErr = errors.Join(closeErr, closeDirs())
		if sourceFile != nil {
			closeErr = errors.Join(closeErr, sourceFile.Close())
			sourceFile = nil
		}
		if sourceRoot != nil {
			closeErr = errors.Join(closeErr, sourceRoot.Close())
			sourceRoot = nil
		}
		return closeErr
	}
	failCreated := func(primary error) (*SourceAnalysisPreparedInput, error) {
		if created {
			_ = closeEverything()
		}
		return nil, primary
	}

	sourceRoot, err = preparer.sourceOpen.OpenRoot(ctx, work.InventoryPath)
	if err != nil {
		return failCreated(fmt.Errorf("open pinned inventory root: %w", err))
	}
	sourceFile, err = sourcefs.OpenRegularAt(ctx, sourceRoot, work.RelativePath)
	if err != nil {
		return failCreated(fmt.Errorf("open pinned inventory file: %w", err))
	}
	before, err := sourceFile.Stat(ctx)
	if err != nil {
		return failCreated(fmt.Errorf("stat source before copy: %w", err))
	}
	if !matchesSourceIdentity(before, work.SizeBytes, work.Mtime) {
		return failCreated(fmt.Errorf("source changed before staged copy"))
	}
	if err := verifySourceNamespace(ctx, preparer.sourceOpen, sourceRoot, work.InventoryPath); err != nil {
		return failCreated(fmt.Errorf("verify inventory namespace before copy: %w", err))
	}
	copied, err := copySourceOnce(ctx, sourceFile, outputFile, work.SizeBytes)
	if err != nil {
		return failCreated(fmt.Errorf("copy source into staged artifact: %w", err))
	}
	if err := outputFile.Sync(ctx); err != nil {
		return failCreated(fmt.Errorf("sync staged source artifact: %w", err))
	}
	after, err := sourceFile.Stat(ctx)
	if err != nil {
		return failCreated(fmt.Errorf("stat source after copy: %w", err))
	}
	if !matchesSourceIdentity(after, work.SizeBytes, work.Mtime) || copied != work.SizeBytes {
		return failCreated(fmt.Errorf("source changed during staged copy"))
	}
	destinationInfo, err := outputFile.Stat(ctx)
	if err != nil {
		return failCreated(fmt.Errorf("stat staged source artifact: %w", err))
	}
	if !destinationInfo.Mode().IsRegular() || destinationInfo.Size() != work.SizeBytes {
		return failCreated(fmt.Errorf("staged source artifact has an unexpected size or type"))
	}
	if err := verifySourceNamespace(ctx, preparer.sourceOpen, sourceRoot, work.InventoryPath); err != nil {
		return failCreated(fmt.Errorf("verify inventory namespace after copy: %w", err))
	}
	if err := verifySourceFileNamespace(ctx, sourceRoot, work.RelativePath, after); err != nil {
		return failCreated(fmt.Errorf("verify inventory file namespace after copy: %w", err))
	}
	if err := outputRoot.Verify(ctx); err != nil {
		return failCreated(fmt.Errorf("verify managed output namespace after copy: %w", err))
	}
	currentOutput, hasOutput, err := preparer.settings.GetOutputDirectory(ctx)
	if err != nil {
		return failCreated(fmt.Errorf("re-read managed output directory: %w", err))
	}
	if hasOutput && currentOutput != "" {
		currentNamespace, openErr := preparer.outputOpen.OpenRoot(ctx, currentOutput)
		if openErr != nil {
			return failCreated(fmt.Errorf("verify current managed output path: %w", openErr))
		}
		_ = currentNamespace.Close()
	}
	if !hasOutput || currentOutput != outputPath {
		return failCreated(fmt.Errorf("managed output directory changed during staged copy"))
	}
	if err := verifyOutputNamespace(ctx, preparer.outputOpen, outputRoot, outputPath); err != nil {
		return failCreated(fmt.Errorf("verify managed output path after copy: %w", err))
	}
	if err := verifyOutputFileNamespace(ctx, preparer.sourceOpen, outputPath, artifact.RelativeOutputPath, destinationInfo); err != nil {
		return failCreated(fmt.Errorf("verify staged artifact namespace after copy: %w", err))
	}
	readyArtifact, err := preparer.artifacts.MarkReady(ctx, artifactID, fence, copied)
	if err != nil {
		return failCreated(fmt.Errorf("mark staged source artifact ready: %w", err))
	}
	if readyArtifact == nil || readyArtifact.ID != artifactID || readyArtifact.WorkID != work.ID || readyArtifact.State != persistence.SourceAnalysisArtifactReady ||
		readyArtifact.OwnerOperationID != fence.OperationID || readyArtifact.OwnerOperationAttempt != fence.OperationAttempt || readyArtifact.OwnerJobID != fence.JobID {
		return failCreated(fmt.Errorf("mark staged source artifact ready returned an invalid owner"))
	}
	created = false
	input := &SourceAnalysisPreparedInput{File: outputReadAdapter{OutputFile: outputFile}, ServerPath: filepath.Join(outputPath, filepath.FromSlash(artifact.RelativeOutputPath)), ArtifactID: &artifactID, close: closeEverything}
	input.validate = func(ctx context.Context) error {
		current, err := sourceFile.Stat(ctx)
		if err != nil {
			return err
		}
		if !matchesSourceIdentity(current, work.SizeBytes, work.Mtime) {
			return fmt.Errorf("source analysis input changed")
		}
		if err := verifySourceNamespace(ctx, preparer.sourceOpen, sourceRoot, work.InventoryPath); err != nil {
			return err
		}
		if err := verifySourceFileNamespace(ctx, sourceRoot, work.RelativePath, current); err != nil {
			return err
		}
		outputInfo, err := outputFile.Stat(ctx)
		if err != nil {
			return err
		}
		if !outputInfo.Mode().IsRegular() || outputInfo.Size() != work.SizeBytes || !os.SameFile(destinationInfo, outputInfo) {
			return fmt.Errorf("staged source artifact changed")
		}
		if err := outputRoot.Verify(ctx); err != nil {
			return err
		}
		if err := verifyOutputNamespace(ctx, preparer.outputOpen, outputRoot, outputPath); err != nil {
			return err
		}
		return verifyOutputFileNamespace(ctx, preparer.sourceOpen, outputPath, artifact.RelativeOutputPath, destinationInfo)
	}
	return input, nil
}

type outputReadAdapter struct{ sourcefs.OutputFile }

func (file outputReadAdapter) Borrow(ctx context.Context, callback func(*os.File) error) error {
	return file.BorrowRead(ctx, callback)
}

func copySourceOnce(ctx context.Context, source sourcefs.RegularFile, destination sourcefs.OutputFile, expectedSize int64) (int64, error) {
	if expectedSize < 0 {
		return 0, fmt.Errorf("expected source size cannot be negative")
	}
	var total int64
	err := source.Borrow(ctx, func(file *os.File) error {
		buffer := make([]byte, sourceAnalysisCopyBufferSize)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			if total == expectedSize {
				var extra [1]byte
				n, readErr := file.Read(extra[:])
				if n > 0 {
					return fmt.Errorf("source grew beyond expected size %d", expectedSize)
				}
				if errors.Is(readErr, io.EOF) {
					return nil
				}
				if readErr != nil {
					return readErr
				}
				continue
			}
			readBuffer := buffer
			remaining := expectedSize - total
			if int64(len(readBuffer)) > remaining {
				readBuffer = readBuffer[:remaining]
			}
			n, readErr := file.Read(readBuffer)
			if n > 0 {
				written := 0
				for written < n {
					count, writeErr := destination.Write(ctx, buffer[written:n])
					if count > 0 {
						written += count
						total += int64(count)
					}
					if writeErr != nil {
						return writeErr
					}
					if count == 0 {
						return io.ErrShortWrite
					}
				}
			}
			if errors.Is(readErr, io.EOF) {
				if total != expectedSize {
					return fmt.Errorf("source ended after %d bytes, expected %d", total, expectedSize)
				}
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	})
	return total, err
}

func matchesSourceIdentity(info os.FileInfo, size int64, mtime time.Time) bool {
	return info != nil && info.Mode().IsRegular() && info.Size() == size && sameSourceMtime(mtime, info.ModTime())
}

func sameSourceMtime(left, right time.Time) bool {
	return left.Truncate(time.Microsecond).Equal(right.Truncate(time.Microsecond))
}

func verifySourceNamespace(ctx context.Context, opener sourcefs.Opener, pinned sourcefs.Directory, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pinnedInfo, err := pinned.Stat(ctx)
	if err != nil {
		return err
	}
	fresh, err := opener.OpenRoot(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = fresh.Close() }()
	freshInfo, err := fresh.Stat(ctx)
	if err != nil {
		return err
	}
	if !os.SameFile(pinnedInfo, freshInfo) {
		return fmt.Errorf("inventory root namespace changed")
	}
	return nil
}

func verifySourceFileNamespace(ctx context.Context, root sourcefs.Directory, relativePath string, expected os.FileInfo) error {
	current, err := sourcefs.OpenRegularAt(ctx, root, relativePath)
	if err != nil {
		return err
	}
	defer func() { _ = current.Close() }()
	currentInfo, err := current.Stat(ctx)
	if err != nil {
		return err
	}
	if !os.SameFile(expected, currentInfo) {
		return fmt.Errorf("inventory file namespace changed")
	}
	return nil
}

func verifyOutputFileNamespace(ctx context.Context, opener sourcefs.Opener, rootPath, relativePath string, expected os.FileInfo) error {
	root, err := opener.OpenRoot(ctx, rootPath)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	current, err := sourcefs.OpenRegularAt(ctx, root, relativePath)
	if err != nil {
		return err
	}
	defer func() { _ = current.Close() }()
	currentInfo, err := current.Stat(ctx)
	if err != nil {
		return err
	}
	if !os.SameFile(expected, currentInfo) {
		return fmt.Errorf("staged artifact namespace changed")
	}
	return nil
}

func verifyOutputNamespace(ctx context.Context, opener sourcefs.OutputOpener, pinned sourcefs.OutputDirectory, path string) error {
	fresh, err := opener.OpenRoot(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = fresh.Close() }()
	pinnedInfo, err := pinned.Stat(ctx)
	if err != nil {
		return err
	}
	if err := pinned.Verify(ctx); err != nil {
		return err
	}
	if err := fresh.Verify(ctx); err != nil {
		return err
	}
	freshInfo, err := fresh.Stat(ctx)
	if err != nil {
		return err
	}
	if !os.SameFile(pinnedInfo, freshInfo) {
		return fmt.Errorf("managed output namespace changed")
	}
	return nil
}
