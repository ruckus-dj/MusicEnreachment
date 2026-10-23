package service

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
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
