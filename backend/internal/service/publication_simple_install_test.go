package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestResolveSimpleAudioSourcePathUsesHeldRelativePath(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const relative = "disc/track.flac"
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	mtime := info.ModTime().UTC().Truncate(time.Microsecond)
	got, err := resolveSimpleAudioSourcePath(context.Background(), root, relative, info.Size(), mtime)
	if err != nil {
		t.Fatalf("resolve held source: %v", err)
	}
	if got != filename {
		t.Fatalf("resolved source = %q, want %q", got, filename)
	}
	if _, err := resolveSimpleAudioSourcePath(context.Background(), root, relative, info.Size()+1, mtime); err == nil {
		t.Fatal("changed source size was accepted")
	}
}

func TestSimplePublicationTempDirectoryCleanupRequiresCommandQuiescence(t *testing.T) {
	for _, quiescent := range []bool{true, false} {
		t.Run(map[bool]string{true: "known", false: "unknown"}[quiescent], func(t *testing.T) {
			dir := t.TempDir()
			privateInput := filepath.Join(dir, "private-input")
			if err := os.WriteFile(privateInput, []byte("private"), 0o600); err != nil {
				t.Fatal(err)
			}
			runner := newSimplePublicationTrackingRunner(simplePublicationCommandFake{result: PublicationCommandResult{QuiescenceEstablished: quiescent}})
			if _, err := runner.Run(context.Background(), "tool"); (err == nil) != quiescent {
				t.Fatalf("command quiescent=%v error=%v", quiescent, err)
			}
			if err := removeSimplePublicationTempDir(dir, runner); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(privateInput)
			if quiescent && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("known-quiescent private input remains: %v", err)
			}
			if !quiescent && err != nil {
				t.Fatalf("unknown-quiescence private input was not retained: %v", err)
			}
		})
	}
}

func TestSimplePublicationTrackingRunnerTracksFileQuiescence(t *testing.T) {
	base := newSimplePublicationTrackingRunner(simplePublicationCommandFake{result: PublicationCommandResult{QuiescenceEstablished: false}})
	tracked := base.PublicationCommandRunner()
	if _, ok := tracked.(PublicationFileCommandRunner); !ok {
		t.Fatal("tracking runner dropped delegated file-command support")
	}
	fileRunner := tracked.(PublicationFileCommandRunner)
	if _, err := fileRunner.RunFile(context.Background(), tools.FileCommandRequest{Executable: "tool", Input: os.Stdin}); err == nil {
		t.Fatal("unknown-quiescence file command returned no error")
	}
	if !base.QuiescenceUnknown() {
		t.Fatal("unknown file-command quiescence was not tracked")
	}

	withoutFiles := newSimplePublicationTrackingRunner(simplePublicationRunOnlyFake{result: PublicationCommandResult{QuiescenceEstablished: true}})
	if _, ok := withoutFiles.PublicationCommandRunner().(PublicationFileCommandRunner); ok {
		t.Fatal("tracking runner advertised unsupported file-command execution")
	}
}

func TestSimplePublicationTrackingRunnerRefusesLaunchAfterUnknownQuiescence(t *testing.T) {
	backend := new(countingSimplePublicationCommandFake)
	runner := newSimplePublicationTrackingRunner(backend)
	if _, err := runner.Run(context.Background(), "tool"); err == nil {
		t.Fatal("unknown-quiescence command returned no error")
	}
	if _, err := runner.Run(context.Background(), "tool"); err == nil {
		t.Fatal("command launched after quiescence became unknown")
	}
	fileRunner := runner.PublicationCommandRunner().(PublicationFileCommandRunner)
	if _, err := fileRunner.RunFile(context.Background(), tools.FileCommandRequest{Executable: "tool", Input: os.Stdin}); err == nil {
		t.Fatal("file command launched after quiescence became unknown")
	}
	if backend.calls != 1 {
		t.Fatalf("backend launches = %d, want 1", backend.calls)
	}
}

type countingSimplePublicationCommandFake struct{ calls int }

func (fake *countingSimplePublicationCommandFake) Run(context.Context, string, ...string) (PublicationCommandResult, error) {
	fake.calls++
	return PublicationCommandResult{}, nil
}

func (fake *countingSimplePublicationCommandFake) RunFile(context.Context, tools.FileCommandRequest) (PublicationCommandResult, error) {
	fake.calls++
	return PublicationCommandResult{}, nil
}

type simplePublicationCommandFake struct{ result PublicationCommandResult }

func (fake simplePublicationCommandFake) Run(context.Context, string, ...string) (PublicationCommandResult, error) {
	return fake.result, nil
}

func (fake simplePublicationCommandFake) RunFile(context.Context, tools.FileCommandRequest) (PublicationCommandResult, error) {
	return fake.result, nil
}

type simplePublicationRunOnlyFake struct{ result PublicationCommandResult }

func (fake simplePublicationRunOnlyFake) Run(context.Context, string, ...string) (PublicationCommandResult, error) {
	return fake.result, nil
}

// TestSimplePublicationSecondPendingInstallAtomicallyReplacesExistingTarget
// covers the retry policy of one admitted first-audio delivery: the whole
// candidate is prepared again, and the destination left by the earlier native
// install is replaced atomically instead of removed or rejected. No database
// commit is involved, and the source is not consulted.
func TestSimplePublicationSecondPendingInstallAtomicallyReplacesExistingTarget(t *testing.T) {
	ctx := context.Background()
	outputRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destination, err := sourcefs.NewOutputOpener().OpenRoot(ctx, outputRoot)
	if errors.Is(err, sourcefs.ErrUnsupported) {
		t.Skip("native managed-output capabilities are unavailable")
	}
	if err != nil {
		t.Fatalf("open controlled output root: %v", err)
	}
	defer func() { _ = destination.Close() }()
	mutation, ok := destination.(sourcefs.OutputMutation)
	if !ok {
		t.Fatal("controlled output root does not provide no-replace installation")
	}
	replacer, ok := destination.(sourcefs.OutputAtomicReplace)
	if !ok {
		t.Fatal("controlled output root does not provide atomic replacement")
	}
	const name = "track.flac"
	first := newControlledPublicationCandidate(t, ctx, outputRoot, name, "first pending candidate")
	if err := mutation.RenameNoReplace(ctx, first.root, name, destination, name); err != nil {
		t.Fatalf("install first pending candidate: %v", err)
	}
	second := newControlledPublicationCandidate(t, ctx, outputRoot, name, "second pending candidate")
	if err := mutation.RenameNoReplace(ctx, second.root, name, destination, name); err == nil {
		t.Fatal("no-replace installation unexpectedly overwrote the pending target")
	}
	current, err := mutation.OpenExistingRegular(ctx, name)
	if err != nil {
		t.Fatalf("open pending target: %v", err)
	}
	currentInfo, statErr := current.Stat(ctx)
	closeErr := current.Close()
	if err := errors.Join(statErr, closeErr); err != nil {
		t.Fatalf("snapshot pending target: %v", err)
	}
	if err := replacer.AtomicReplaceExisting(ctx, second.root, name, second.info, name, currentInfo); err != nil {
		t.Fatalf("retry pending first publication atomically: %v", err)
	}
	installed, err := mutation.OpenExistingRegular(ctx, name)
	if err != nil {
		t.Fatalf("open retried pending target: %v", err)
	}
	var got []byte
	borrowErr := installed.Borrow(ctx, func(native *os.File) error {
		var readErr error
		got, readErr = io.ReadAll(native)
		return readErr
	})
	closeErr = installed.Close()
	if err := errors.Join(borrowErr, closeErr); err != nil {
		t.Fatalf("read retried pending target: %v", err)
	}
	if string(got) != "second pending candidate" {
		t.Fatalf("retried pending target = %q, want the second candidate", got)
	}
}

type controlledPublicationCandidate struct {
	root sourcefs.OutputDirectory
	info fs.FileInfo
}

func newControlledPublicationCandidate(t *testing.T, ctx context.Context, outputRoot, name, content string) controlledPublicationCandidate {
	t.Helper()
	dir, err := os.MkdirTemp(outputRoot, ".publication-delivery-")
	if err != nil {
		t.Fatalf("create controlled candidate directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root, err := sourcefs.NewOutputOpener().OpenRoot(ctx, dir)
	if err != nil {
		t.Fatalf("open controlled candidate directory: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	file, err := root.CreateExclusive(ctx, name)
	if err != nil {
		t.Fatalf("create controlled candidate: %v", err)
	}
	if _, err := file.Write(ctx, []byte(content)); err != nil {
		_ = file.Close()
		t.Fatalf("write controlled candidate: %v", err)
	}
	if err := file.Sync(ctx); err != nil {
		_ = file.Close()
		t.Fatalf("sync controlled candidate: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close controlled candidate: %v", err)
	}
	mutation, ok := root.(sourcefs.OutputMutation)
	if !ok {
		t.Fatal("controlled candidate directory does not provide no-replace installation")
	}
	opened, err := mutation.OpenExistingRegular(ctx, name)
	if err != nil {
		t.Fatalf("open completed controlled candidate: %v", err)
	}
	info, statErr := opened.Stat(ctx)
	closeErr := opened.Close()
	if err := errors.Join(statErr, closeErr); err != nil {
		t.Fatalf("snapshot completed controlled candidate: %v", err)
	}
	return controlledPublicationCandidate{root: root, info: info}
}

func TestCopyHeldPublicationSourceRejectsChangedSizeAndMtime(t *testing.T) {
	for _, changed := range []string{"size", "mtime"} {
		t.Run(changed, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			sourcePath := filepath.Join(root, "source.flac")
			original := []byte("frozen source")
			if err := os.WriteFile(sourcePath, original, 0o600); err != nil {
				t.Fatal(err)
			}
			originalInfo, err := os.Stat(sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			input := &persistence.PublicationAudioInput{
				ConfiguredRootPath: root, RelativePath: "source.flac", SizeBytes: int64(len(original)), MTime: sourceScanMtime(originalInfo.ModTime()),
			}
			if _, err := resolveSimpleAudioSourcePath(context.Background(), root, input.RelativePath, input.SizeBytes, input.MTime); err != nil {
				t.Fatalf("initial frozen-source check failed: %v", err)
			}

			mutated := append([]byte(nil), original...)
			if changed == "size" {
				mutated = append(mutated, '!')
			} else {
				mutated[0] = 'F'
			}
			if err := os.WriteFile(sourcePath, mutated, 0o600); err != nil {
				t.Fatal(err)
			}
			if changed == "size" {
				if err := os.Chtimes(sourcePath, originalInfo.ModTime(), originalInfo.ModTime()); err != nil {
					t.Fatal(err)
				}
			} else {
				changedTime := originalInfo.ModTime().Add(2 * time.Second)
				if err := os.Chtimes(sourcePath, changedTime, changedTime); err != nil {
					t.Fatal(err)
				}
			}

			destination := filepath.Join(t.TempDir(), "private-source.flac")
			if err := copyHeldPublicationSource(context.Background(), input, destination); err == nil {
				t.Fatal("changed source was copied under its frozen identity")
			}
			if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected source left a private copy: %v", err)
			}
		})
	}
}

func TestCopyHeldPublicationSourceCopiesFrozenBytes(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(root, "source.flac")
	want := []byte("frozen source")
	if err := os.WriteFile(sourcePath, want, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "private-source.flac")
	input := &persistence.PublicationAudioInput{
		ConfiguredRootPath: root, RelativePath: "source.flac", SizeBytes: int64(len(want)), MTime: sourceScanMtime(info.ModTime()),
	}
	if err := copyHeldPublicationSource(context.Background(), input, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("private source = %q, want %q", got, want)
	}
}

func TestCopyHeldPublicationSourceAcceptsMaximumLengthInputName(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A 255-byte component is a valid source name; the former
	// "source-input-"+basename form exceeded that limit.
	name := strings.Repeat("a", 250) + ".flac"
	if got := len("source-input-" + name); got <= 255 {
		t.Fatalf("prefixed source name length = %d, want overflow beyond 255", got)
	}
	sourcePath := filepath.Join(root, name)
	contents := []byte("frozen source")
	if err := os.WriteFile(sourcePath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), simplePublicationPrivateInputName)
	input := &persistence.PublicationAudioInput{
		ConfiguredRootPath: root, RelativePath: name, SizeBytes: int64(len(contents)), MTime: sourceScanMtime(info.ModTime()),
	}
	if err := copyHeldPublicationSource(context.Background(), input, destination); err != nil {
		t.Fatalf("copy maximum-length source name: %v", err)
	}
	if base := filepath.Base(destination); base != simplePublicationPrivateInputName || len(base) > 255 {
		t.Fatalf("private input name = %q, want bounded %q", base, simplePublicationPrivateInputName)
	}
	got, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(got, contents) {
		t.Fatalf("private source = %q, %v; want %q", got, err, contents)
	}
}
