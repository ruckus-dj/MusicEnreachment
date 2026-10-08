package service

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestSourceAnalysisInputPreparerCopiesOneBorrowedSourceAndClosesWithoutDeleting(t *testing.T) {
	fixture := newInputPreparerFixture(t, nil)
	input, err := fixture.preparer.Prepare(context.Background(), fixture.fence)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.source.borrowCount != 1 {
		t.Fatalf("source borrowed %d times, want exactly one", fixture.source.borrowCount)
	}
	if fixture.artifacts.readyCalls != 1 || fixture.artifacts.forgetCalls != 0 {
		t.Fatalf("artifact transitions: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
	}
	if err := input.Validate(context.Background()); err != nil {
		t.Fatalf("Validate staged input: %v", err)
	}
	if !strings.HasSuffix(input.ServerPath, filepath.Join(fixture.artifactID.String())) {
		t.Fatalf("unexpected transient server path %q", input.ServerPath)
	}
	if err := input.File.Borrow(context.Background(), func(file *os.File) error {
		copied, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		if string(copied) != fixture.contents {
			t.Fatalf("staged bytes differ from source")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(input.ServerPath); err != nil {
		t.Fatalf("Close removed staged artifact: %v", err)
	}
}

func TestSourceAnalysisInputPreparerUsesExecutionModeInsteadOfCurrentRootMode(t *testing.T) {
	fixture := newInputPreparerFixture(t, nil)
	// The current root is staged, but this already-running execution was pinned
	// as in-place; its selected mode must not be redirected by the root edit.
	input, err := fixture.preparer.PrepareMode(context.Background(), fixture.fence, "in_place")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	if input.ArtifactID != nil || input.ServerPath != filepath.Join(fixture.sourcePath, "audio.bin") {
		t.Fatalf("in-place input was redirected: artifact=%v path=%q", input.ArtifactID, input.ServerPath)
	}
	if fixture.artifacts.readyCalls != 0 || fixture.artifacts.forgetCalls != 0 {
		t.Fatalf("in-place execution acquired staged artifact: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
	}
	if err := input.Validate(context.Background()); err != nil {
		t.Fatalf("Validate in-place input: %v", err)
	}
}

func TestSourceAnalysisInputPreparerRetainsPartialArtifactAfterWriteFailure(t *testing.T) {
	writeErr := errors.New("no space left on device")
	fixture := newInputPreparerFixture(t, func(file sourcefs.OutputFile) sourcefs.OutputFile {
		return &failingOutputFile{OutputFile: file, after: 37, err: writeErr}
	})
	if _, err := fixture.preparer.Prepare(context.Background(), fixture.fence); !errors.Is(err, writeErr) {
		t.Fatalf("Prepare error = %v, want write failure", err)
	}
	if fixture.artifacts.readyCalls != 0 || fixture.artifacts.forgetCalls != 0 {
		t.Fatalf("partial copy transitions: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
	}
	if got, err := os.Stat(fixture.finalPath()); err != nil || got.Size() == 0 {
		t.Fatalf("partial owned artifact not retained: stat=%v error=%v", got, err)
	}
}

func TestSourceAnalysisInputPreparerRetainsArtifactAfterSyncFailure(t *testing.T) {
	syncErr := errors.New("sync failed")
	fixture := newInputPreparerFixture(t, func(file sourcefs.OutputFile) sourcefs.OutputFile {
		return &syncFailingOutputFile{OutputFile: file, err: syncErr}
	})
	if _, err := fixture.preparer.Prepare(context.Background(), fixture.fence); !errors.Is(err, syncErr) {
		t.Fatalf("Prepare error = %v, want sync failure", err)
	}
	if fixture.artifacts.readyCalls != 0 || fixture.artifacts.forgetCalls != 0 {
		t.Fatalf("sync failure transitions: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
	}
	if _, err := os.Stat(fixture.finalPath()); err != nil {
		t.Fatalf("artifact not retained after sync failure: %v", err)
	}
}

func TestSourceAnalysisInputPreparerCancellationDuringCopyRetainsArtifact(t *testing.T) {
	fixture := newInputPreparerFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.preparer.outputOpen = wrappingOutputOpener{OutputOpener: sourcefs.NewOutputOpener(), wrap: func(file sourcefs.OutputFile) sourcefs.OutputFile {
		return &mutatingOutputFile{OutputFile: file, mutate: func() error { cancel(); return nil }}
	}}
	if _, err := fixture.preparer.Prepare(ctx, fixture.fence); !errors.Is(err, context.Canceled) {
		t.Fatalf("Prepare error = %v, want context cancellation", err)
	}
	if fixture.artifacts.readyCalls != 0 || fixture.artifacts.forgetCalls != 0 {
		t.Fatalf("canceled copy transitions: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
	}
	if _, err := os.Stat(fixture.finalPath()); err != nil {
		t.Fatalf("artifact not retained after cancellation: %v", err)
	}
}

func TestSourceAnalysisInputPreparerAcceptsZeroByteSource(t *testing.T) {
	fixture := newInputPreparerFixture(t, nil)
	path := filepath.Join(fixture.sourcePath, "audio.bin")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture.work.SizeBytes, fixture.work.Mtime = 0, info.ModTime()
	fixture.location.SizeBytes, fixture.location.Mtime = 0, info.ModTime()
	fixture.artifacts.size, fixture.artifacts.mtime = 0, info.ModTime()
	input, err := fixture.preparer.Prepare(context.Background(), fixture.fence)
	if err != nil {
		t.Fatalf("Prepare zero-byte source: %v", err)
	}
	defer func() { _ = input.Close() }()
	if fixture.artifacts.readyCalls != 1 || fixture.artifacts.forgetCalls != 0 {
		t.Fatalf("zero-byte transitions: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
	}
	if info, err := os.Stat(fixture.finalPath()); err != nil || info.Size() != 0 {
		t.Fatalf("zero-byte staged artifact: info=%v error=%v", info, err)
	}
}

func TestSourceAnalysisInputPreparerRetainsReadyFileWhenReadyCommitFails(t *testing.T) {
	commitErr := errors.New("ready transaction failed")
	fixture := newInputPreparerFixture(t, nil)
	fixture.artifacts.readyErr = commitErr
	if _, err := fixture.preparer.Prepare(context.Background(), fixture.fence); !errors.Is(err, commitErr) {
		t.Fatalf("Prepare error = %v, want commit error", err)
	}
	if fixture.artifacts.readyCalls != 1 || fixture.artifacts.forgetCalls != 0 {
		t.Fatalf("commit failure transitions: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
	}
	if _, err := os.Stat(fixture.finalPath()); err != nil {
		t.Fatalf("completed artifact was not retained: %v", err)
	}
}

func TestSourceAnalysisInputPreparerDoesNotPublishWhenFenceChangesDuringCopy(t *testing.T) {
	fixture := newInputPreparerFixture(t, nil)
	fixture.preparer.outputOpen = wrappingOutputOpener{OutputOpener: sourcefs.NewOutputOpener(), wrap: func(file sourcefs.OutputFile) sourcefs.OutputFile {
		return &mutatingOutputFile{OutputFile: file, mutate: func() error { fixture.artifacts.fence.JobID++; return nil }}
	}}
	if _, err := fixture.preparer.Prepare(context.Background(), fixture.fence); err == nil {
		t.Fatal("Prepare accepted a stale fence")
	}
	if fixture.artifacts.readyCalls != 1 || fixture.artifacts.forgetCalls != 0 {
		t.Fatalf("stale fence transitions: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
	}
	if _, err := os.Stat(fixture.finalPath()); err != nil {
		t.Fatalf("stale-fence artifact was not retained: %v", err)
	}
}

func TestSourceAnalysisInputPreparerForgetsRowButPreservesCollisionBytes(t *testing.T) {
	fixture := newInputPreparerFixture(t, nil)
	if err := os.MkdirAll(filepath.Dir(fixture.finalPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	const foreign = "foreign bytes"
	if err := os.WriteFile(fixture.finalPath(), []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.preparer.Prepare(context.Background(), fixture.fence); err == nil {
		t.Fatal("Prepare unexpectedly overwrote a colliding artifact")
	}
	if fixture.artifacts.forgetCalls != 1 || fixture.artifacts.readyCalls != 0 {
		t.Fatalf("collision transitions: forget=%d ready=%d", fixture.artifacts.forgetCalls, fixture.artifacts.readyCalls)
	}
	got, err := os.ReadFile(fixture.finalPath())
	if err != nil || string(got) != foreign {
		t.Fatalf("collision bytes changed: %q, error=%v", got, err)
	}
}

func TestSourceAnalysisInputPreparerRetainsArtifactWhenSourceIdentityMismatches(t *testing.T) {
	fixture := newInputPreparerFixture(t, nil)
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(fixture.sourcePath, "audio.bin"), future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.preparer.Prepare(context.Background(), fixture.fence); err == nil {
		t.Fatal("Prepare accepted a source with a different observed mtime")
	}
	if fixture.artifacts.forgetCalls != 0 || fixture.artifacts.readyCalls != 0 {
		t.Fatalf("mismatch transitions: forget=%d ready=%d", fixture.artifacts.forgetCalls, fixture.artifacts.readyCalls)
	}
	if _, err := os.Stat(fixture.finalPath()); err != nil {
		t.Fatalf("mismatched source artifact was not retained: %v", err)
	}
}

func TestSourceAnalysisInputPreparerRetainsArtifactWhenSourceChangesDuringCopy(t *testing.T) {
	fixture := newInputPreparerFixture(t, nil)
	fixture.preparer.outputOpen = wrappingOutputOpener{OutputOpener: sourcefs.NewOutputOpener(), wrap: func(file sourcefs.OutputFile) sourcefs.OutputFile {
		return &mutatingOutputFile{OutputFile: file, mutate: func() error {
			path := filepath.Join(fixture.sourcePath, "audio.bin")
			if err := os.WriteFile(path, []byte(fixture.contents), 0o600); err != nil {
				return err
			}
			future := time.Now().Add(2 * time.Second)
			return os.Chtimes(path, future, future)
		}}
	}}
	if _, err := fixture.preparer.Prepare(context.Background(), fixture.fence); err == nil {
		t.Fatal("Prepare accepted a source whose mtime changed during copy")
	}
	if fixture.artifacts.readyCalls != 0 || fixture.artifacts.forgetCalls != 0 {
		t.Fatalf("post-copy mismatch transitions: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
	}
	if _, err := os.Stat(fixture.finalPath()); err != nil {
		t.Fatalf("changed-source artifact was not retained: %v", err)
	}
}

func TestSourceAnalysisInputPreparerRejectsReplacedSourceNamespace(t *testing.T) {
	for _, replaceParent := range []bool{false, true} {
		name := "leaf"
		if replaceParent {
			name = "parent"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newInputPreparerFixture(t, nil)
			originalRelative := "audio.bin"
			if replaceParent {
				originalRelative = filepath.Join("nested", "audio.bin")
				if err := os.Mkdir(filepath.Join(fixture.sourcePath, "nested"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Join(fixture.sourcePath, "audio.bin"), filepath.Join(fixture.sourcePath, originalRelative)); err != nil {
					t.Fatal(err)
				}
				fixture.work.RelativePath = originalRelative
				fixture.location.RelativePath = originalRelative
			}
			originalPath := filepath.Join(fixture.sourcePath, originalRelative)
			info, err := os.Stat(originalPath)
			if err != nil {
				t.Fatal(err)
			}
			fixture.work.SizeBytes = info.Size()
			fixture.work.Mtime = info.ModTime()
			fixture.location.SizeBytes = info.Size()
			fixture.location.Mtime = info.ModTime()
			fixture.artifacts.size = info.Size()
			fixture.artifacts.mtime = info.ModTime()
			fixture.preparer.outputOpen = wrappingOutputOpener{OutputOpener: sourcefs.NewOutputOpener(), wrap: func(file sourcefs.OutputFile) sourcefs.OutputFile {
				return &mutatingOutputFile{OutputFile: file, mutate: func() error {
					if replaceParent {
						parent := filepath.Dir(originalPath)
						moved := parent + "-original"
						if err := os.Rename(parent, moved); err != nil {
							return err
						}
						if err := os.Mkdir(parent, 0o700); err != nil {
							return err
						}
					} else if err := os.Rename(originalPath, originalPath+"-original"); err != nil {
						return err
					}
					if err := os.WriteFile(originalPath, []byte(fixture.contents), 0o600); err != nil {
						return err
					}
					return os.Chtimes(originalPath, info.ModTime(), info.ModTime())
				}}
			}}
			if _, err := fixture.preparer.Prepare(context.Background(), fixture.fence); err == nil {
				t.Fatal("Prepare accepted a replaced source namespace")
			}
			if fixture.artifacts.readyCalls != 0 || fixture.artifacts.forgetCalls != 0 {
				t.Fatalf("replaced source transitions: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
			}
		})
	}
}

func TestSourceAnalysisInputPreparerRejectsReplacedOutputDescendant(t *testing.T) {
	fixture := newInputPreparerFixture(t, nil)
	fixture.preparer.outputOpen = wrappingOutputOpener{OutputOpener: sourcefs.NewOutputOpener(), wrap: func(file sourcefs.OutputFile) sourcefs.OutputFile {
		return &mutatingOutputFile{OutputFile: file, mutate: func() error {
			parent := filepath.Dir(filepath.Dir(fixture.finalPath()))
			moved := parent + "-original"
			if err := os.Rename(parent, moved); err != nil {
				return err
			}
			return os.Mkdir(parent, 0o700)
		}}
	}}
	if _, err := fixture.preparer.Prepare(context.Background(), fixture.fence); err == nil {
		t.Fatal("Prepare accepted a replaced output descendant")
	}
	if fixture.artifacts.readyCalls != 0 || fixture.artifacts.forgetCalls != 0 {
		t.Fatalf("replaced output transitions: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
	}
	if _, err := os.Stat(fixture.finalPath()); !os.IsNotExist(err) {
		t.Fatalf("reopened namespace unexpectedly recreated or retained artifact at path: %v", err)
	}
}

func TestSourceAnalysisInputPreparerBoundsCopyWhenSourceGrows(t *testing.T) {
	fixture := newInputPreparerFixture(t, nil)
	fixture.preparer.outputOpen = wrappingOutputOpener{OutputOpener: sourcefs.NewOutputOpener(), wrap: func(file sourcefs.OutputFile) sourcefs.OutputFile {
		return &mutatingOutputFile{OutputFile: file, mutate: func() error {
			input, err := os.OpenFile(filepath.Join(fixture.sourcePath, "audio.bin"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			if _, err := input.Write([]byte("growth")); err != nil {
				_ = input.Close()
				return err
			}
			return input.Close()
		}}
	}}
	if _, err := fixture.preparer.Prepare(context.Background(), fixture.fence); err == nil {
		t.Fatal("Prepare accepted a source that grew during copy")
	}
	if fixture.artifacts.readyCalls != 0 || fixture.artifacts.forgetCalls != 0 {
		t.Fatalf("growing source transitions: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
	}
	info, err := os.Stat(fixture.finalPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != fixture.work.SizeBytes {
		t.Fatalf("copied %d bytes, want bounded artifact size %d", info.Size(), fixture.work.SizeBytes)
	}
}

func TestSourceAnalysisInputPreparerRejectsChangedOutputSymlinkNamespace(t *testing.T) {
	fixture := newInputPreparerFixture(t, nil)
	linkPath := filepath.Join(fixture.temp, "output-link")
	if err := os.Symlink(fixture.outputPath, linkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	fixture.settings.changeOnSecondRead = linkPath
	if _, err := fixture.preparer.Prepare(context.Background(), fixture.fence); err == nil {
		t.Fatal("Prepare accepted a changed output namespace")
	}
	if fixture.artifacts.readyCalls != 0 || fixture.artifacts.forgetCalls != 0 {
		t.Fatalf("changed namespace transitions: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
	}
	if _, err := os.Stat(fixture.finalPath()); err != nil {
		t.Fatalf("failed artifact should remain registered on disk: %v", err)
	}
}

func TestSourceAnalysisInputPreparerRejectsSourceSymlink(t *testing.T) {
	fixture := newInputPreparerFixture(t, nil)
	linked := filepath.Join(fixture.sourcePath, "linked.bin")
	if err := os.Symlink(filepath.Join(fixture.sourcePath, "audio.bin"), linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	fixture.work.RelativePath = "linked.bin"
	fixture.location.RelativePath = "linked.bin"
	if _, err := fixture.preparer.Prepare(context.Background(), fixture.fence); err == nil {
		t.Fatal("Prepare accepted a symlinked source file")
	}
	if fixture.artifacts.readyCalls != 0 || fixture.artifacts.forgetCalls != 0 {
		t.Fatalf("symlink transitions: ready=%d forget=%d", fixture.artifacts.readyCalls, fixture.artifacts.forgetCalls)
	}
	if _, err := os.Stat(fixture.finalPath()); err != nil {
		t.Fatalf("symlink failure artifact was not retained: %v", err)
	}
}

type inputPreparerFixture struct {
	t          *testing.T
	temp       string
	sourcePath string
	outputPath string
	contents   string
	work       *persistence.SourceAnalysisWork
	location   *persistence.SourceLocation
	fence      persistence.SourceAnalysisArtifactFence
	artifactID uuid.UUID
	preparer   *SourceAnalysisInputPreparer
	artifacts  *inputArtifactRepository
	settings   *inputOutputSettings
	source     *countBorrowOpener
}

func newInputPreparerFixture(t *testing.T, wrap func(sourcefs.OutputFile) sourcefs.OutputFile) *inputPreparerFixture {
	t.Helper()
	temp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sourcePath, outputPath := filepath.Join(temp, "source"), filepath.Join(temp, "output")
	if err := os.MkdirAll(sourcePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outputPath, 0o700); err != nil {
		t.Fatal(err)
	}
	contents := strings.Repeat("sample-audio-data", 20_000)
	if err := os.WriteFile(filepath.Join(sourcePath, "audio.bin"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(sourcePath, "audio.bin"))
	if err != nil {
		t.Fatal(err)
	}
	rootID, workID, locationID := uuid.New(), uuid.New(), uuid.New()
	inventoryPath := sourcePath
	work := &persistence.SourceAnalysisWork{ID: workID, LocationID: locationID, SourceRootID: rootID, ConfiguredPath: sourcePath,
		InventoryPath: sourcePath, RelativePath: "audio.bin", SizeBytes: info.Size(), Mtime: info.ModTime()}
	location := &persistence.SourceLocation{ID: locationID, SourceRootID: rootID, RelativePath: work.RelativePath, SizeBytes: info.Size(), Mtime: info.ModTime()}
	root := &persistence.SourceRoot{ID: rootID, ConfiguredPath: sourcePath, InventoryPath: &inventoryPath, ProcessingMode: "staged", Enabled: true}
	fence := persistence.SourceAnalysisArtifactFence{WorkID: workID, OperationID: uuid.New(), OperationAttempt: 1, JobID: 71}
	artifactID := uuid.New()
	repository := &inputPreparerRepository{work: work, location: location, root: root}
	artifacts := &inputArtifactRepository{rootID: rootID, workID: workID, artifactID: artifactID, fence: fence, size: info.Size(), mtime: info.ModTime()}
	settings := &inputOutputSettings{path: outputPath}
	counted := &countBorrowOpener{Opener: sourcefs.NewOpener()}
	outputOpener := sourcefs.NewOutputOpener()
	if wrap != nil {
		outputOpener = wrappingOutputOpener{OutputOpener: outputOpener, wrap: wrap}
	}
	preparer := NewSourceAnalysisInputPreparer(repository, artifacts, settings, counted, outputOpener)
	preparer.newID = func() uuid.UUID { return artifactID }
	return &inputPreparerFixture{t: t, temp: temp, sourcePath: sourcePath, outputPath: outputPath, contents: contents,
		work: work, location: location, fence: fence, artifactID: artifactID, preparer: preparer, artifacts: artifacts, settings: settings, source: counted}
}

func (fixture *inputPreparerFixture) finalPath() string {
	return filepath.Join(fixture.outputPath, "analysis", "staging", fixture.work.SourceRootID.String(), fixture.work.ID.String(), fixture.artifactID.String())
}

type inputPreparerRepository struct {
	work     *persistence.SourceAnalysisWork
	location *persistence.SourceLocation
	root     *persistence.SourceRoot
}

func (repository *inputPreparerRepository) GetNormalizedSourceAnalysisWork(context.Context, uuid.UUID) (*persistence.SourceAnalysisWork, *persistence.SourceLocation, error) {
	return repository.work, repository.location, nil
}
func (repository *inputPreparerRepository) GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error) {
	return repository.root, nil
}

type inputArtifactRepository struct {
	rootID      uuid.UUID
	workID      uuid.UUID
	artifactID  uuid.UUID
	fence       persistence.SourceAnalysisArtifactFence
	size        int64
	mtime       time.Time
	readyCalls  int
	forgetCalls int
	readyErr    error
}

func (repository *inputArtifactRepository) Acquire(_ context.Context, id uuid.UUID, fence persistence.SourceAnalysisArtifactFence) (*persistence.SourceAnalysisArtifact, error) {
	if id != repository.artifactID || fence != repository.fence {
		return nil, errors.New("unexpected acquire")
	}
	return &persistence.SourceAnalysisArtifact{ID: id, WorkID: repository.workID, RelativeOutputPath: filepath.ToSlash(filepath.Join("analysis", "staging", repository.rootID.String(), repository.workID.String(), id.String())),
		SourceSizeBytes: repository.size, SourceMtime: repository.mtime, OwnerOperationID: fence.OperationID, OwnerOperationAttempt: fence.OperationAttempt, OwnerJobID: fence.JobID,
		State: persistence.SourceAnalysisArtifactAcquiring}, nil
}
func (repository *inputArtifactRepository) MarkReady(_ context.Context, id uuid.UUID, fence persistence.SourceAnalysisArtifactFence, _ int64) (*persistence.SourceAnalysisArtifact, error) {
	repository.readyCalls++
	if repository.readyErr != nil {
		return nil, repository.readyErr
	}
	if fence != repository.fence {
		return nil, errors.New("stale delivery fence")
	}
	return &persistence.SourceAnalysisArtifact{ID: id, WorkID: repository.workID, State: persistence.SourceAnalysisArtifactReady,
		OwnerOperationID: fence.OperationID, OwnerOperationAttempt: fence.OperationAttempt, OwnerJobID: fence.JobID}, nil
}

type syncFailingOutputFile struct {
	sourcefs.OutputFile
	err error
}

func (file *syncFailingOutputFile) Sync(context.Context) error { return file.err }
func (repository *inputArtifactRepository) ForgetUncreated(_ context.Context, id uuid.UUID, fence persistence.SourceAnalysisArtifactFence) error {
	repository.forgetCalls++
	if id != repository.artifactID || fence != repository.fence {
		return errors.New("unexpected forget")
	}
	return nil
}

type inputOutputSettings struct {
	path               string
	reads              int
	changeOnSecondRead string
}

func (settings *inputOutputSettings) GetOutputDirectory(context.Context) (string, bool, error) {
	settings.reads++
	if settings.reads >= 2 && settings.changeOnSecondRead != "" {
		return settings.changeOnSecondRead, true, nil
	}
	return settings.path, true, nil
}

type countBorrowOpener struct {
	sourcefs.Opener
	borrowCount int
}

func (opener *countBorrowOpener) OpenRoot(ctx context.Context, path string) (sourcefs.Directory, error) {
	directory, err := opener.Opener.OpenRoot(ctx, path)
	if err != nil {
		return nil, err
	}
	return countBorrowDirectory{Directory: directory, opener: opener}, nil
}

type countBorrowDirectory struct {
	sourcefs.Directory
	opener *countBorrowOpener
}

func (directory countBorrowDirectory) OpenDir(ctx context.Context, name string) (sourcefs.Directory, error) {
	next, err := directory.Directory.OpenDir(ctx, name)
	if err != nil {
		return nil, err
	}
	return countBorrowDirectory{Directory: next, opener: directory.opener}, nil
}
func (directory countBorrowDirectory) OpenRegular(ctx context.Context, name string) (sourcefs.RegularFile, error) {
	file, err := directory.Directory.OpenRegular(ctx, name)
	if err != nil {
		return nil, err
	}
	return countBorrowFile{RegularFile: file, opener: directory.opener}, nil
}

type countBorrowFile struct {
	sourcefs.RegularFile
	opener *countBorrowOpener
}

func (file countBorrowFile) Borrow(ctx context.Context, callback func(*os.File) error) error {
	file.opener.borrowCount++
	return file.RegularFile.Borrow(ctx, callback)
}

type wrappingOutputOpener struct {
	sourcefs.OutputOpener
	wrap func(sourcefs.OutputFile) sourcefs.OutputFile
}

func (opener wrappingOutputOpener) OpenRoot(ctx context.Context, path string) (sourcefs.OutputDirectory, error) {
	directory, err := opener.OutputOpener.OpenRoot(ctx, path)
	if err != nil {
		return nil, err
	}
	return wrappingOutputDirectory{OutputDirectory: directory, wrap: opener.wrap}, nil
}

type wrappingOutputDirectory struct {
	sourcefs.OutputDirectory
	wrap func(sourcefs.OutputFile) sourcefs.OutputFile
}

func (directory wrappingOutputDirectory) OpenOrCreateDir(ctx context.Context, name string) (sourcefs.OutputDirectory, error) {
	next, err := directory.OutputDirectory.OpenOrCreateDir(ctx, name)
	if err != nil {
		return nil, err
	}
	return wrappingOutputDirectory{OutputDirectory: next, wrap: directory.wrap}, nil
}
func (directory wrappingOutputDirectory) CreateExclusive(ctx context.Context, name string) (sourcefs.OutputFile, error) {
	file, err := directory.OutputDirectory.CreateExclusive(ctx, name)
	if err != nil {
		return nil, err
	}
	return directory.wrap(file), nil
}

type failingOutputFile struct {
	sourcefs.OutputFile
	after   int64
	written int64
	err     error
}

type mutatingOutputFile struct {
	sourcefs.OutputFile
	mutate func() error
	done   bool
}

func (file *mutatingOutputFile) Write(ctx context.Context, content []byte) (int, error) {
	if !file.done {
		file.done = true
		if err := file.mutate(); err != nil {
			return 0, err
		}
	}
	return file.OutputFile.Write(ctx, content)
}

func (file *failingOutputFile) Write(ctx context.Context, content []byte) (int, error) {
	remaining := file.after - file.written
	if remaining <= 0 {
		return 0, file.err
	}
	if int64(len(content)) > remaining {
		content = content[:remaining]
	}
	written, err := file.OutputFile.Write(ctx, content)
	file.written += int64(written)
	if err != nil {
		return written, err
	}
	if file.written >= file.after {
		return written, file.err
	}
	return written, nil
}
