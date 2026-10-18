package sourcefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOutputRenameNoReplaceMovesExistingRegularFile(t *testing.T) {
	ctx := context.Background()
	rootPath := outputRenameRoot(t)
	root := openOutputRenameRoot(t, rootPath)
	defer func() { _ = root.Close() }()
	target, err := root.CreateDirExclusive(ctx, "target")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()

	created, err := root.CreateExclusive(ctx, "source")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := created.Write(ctx, []byte("content")); err != nil {
		t.Fatal(err)
	}
	if err := created.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := root.(OutputMutation).OpenExistingRegular(ctx, "source")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = before.Close() }()
	beforeInfo, err := before.Stat(ctx)
	if err != nil {
		t.Fatal(err)
	}

	mutation := root.(OutputMutation)
	if err := mutation.RenameNoReplace(ctx, root, "source", target, "moved"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(rootPath, "source")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists or could not be inspected: %v", err)
	}
	after, err := target.(OutputMutation).OpenExistingRegular(ctx, "moved")
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, statErr := after.Stat(ctx)
	closeErr := after.Close()
	if statErr != nil {
		t.Fatal(statErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if !os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("rename changed file identity")
	}
	var content string
	if err := before.Borrow(ctx, func(file *os.File) error {
		bytes, err := io.ReadAll(file)
		content = string(bytes)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if content != "content" {
		t.Fatalf("opened source handle read %q", content)
	}
}

func TestOutputDirectorySyncPinnedHandleAndFailures(t *testing.T) {
	ctx := context.Background()
	root := openOutputRenameRoot(t, outputRenameRoot(t))
	if syncer, ok := root.(OutputDirectorySync); ok {
		err := syncer.SyncDirectory(ctx)
		if runtime.GOOS == "windows" {
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("Windows directory sync error = %v, want ErrUnsupported", err)
			}
		} else if err != nil {
			t.Fatalf("sync pinned directory: %v", err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if err := syncer.SyncDirectory(cancelled); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled directory sync error = %v, want context.Canceled", err)
		}
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
		if err := syncer.SyncDirectory(ctx); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("closed directory sync error = %v, want os.ErrClosed", err)
		}
		return
	}
	t.Fatal("output directory does not expose directory sync capability")
}

func TestOutputRenameNoReplacePreservesSourceAndCollisions(t *testing.T) {
	ctx := context.Background()
	rootPath := outputRenameRoot(t)
	root := openOutputRenameRoot(t, rootPath)
	defer func() { _ = root.Close() }()
	target, err := root.CreateDirExclusive(ctx, "target")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()

	for _, kind := range []string{"file", "directory", "symlink", "dangling-symlink"} {
		t.Run(kind, func(t *testing.T) {
			sourceName, targetName := "source-"+kind, "collision-"+kind
			file, err := root.CreateExclusive(ctx, sourceName)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write(ctx, []byte("source")); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(rootPath, "target", targetName)
			switch kind {
			case "file":
				if err := os.WriteFile(path, []byte("target"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink", "dangling-symlink":
				linkTarget := filepath.Join(rootPath, "source-"+kind)
				if kind == "dangling-symlink" {
					linkTarget = filepath.Join(rootPath, "missing-target")
				}
				if err := os.Symlink(linkTarget, path); err != nil {
					t.Skipf("symlink creation unavailable: %v", err)
				}
			}
			targetBefore, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := root.(OutputMutation).RenameNoReplace(ctx, root, sourceName, target, targetName); err == nil {
				t.Fatal("rename succeeded despite an existing target")
			}
			if _, err := os.Lstat(filepath.Join(rootPath, sourceName)); err != nil {
				t.Fatalf("source was removed: %v", err)
			}
			if content, err := os.ReadFile(filepath.Join(rootPath, sourceName)); err != nil || string(content) != "source" {
				t.Fatalf("source changed: %q, %v", content, err)
			}
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatalf("existing target changed: %v", err)
			}
			if !os.SameFile(targetBefore, info) {
				t.Fatal("existing target identity changed")
			}
			if (kind == "directory") != info.IsDir() || ((kind == "symlink" || kind == "dangling-symlink") != (info.Mode()&os.ModeSymlink != 0)) {
				t.Fatalf("target kind changed: mode %v", info.Mode())
			}
			switch kind {
			case "file":
				if content, err := os.ReadFile(path); err != nil || string(content) != "target" {
					t.Fatalf("existing file target changed: %q, %v", content, err)
				}
			case "symlink", "dangling-symlink":
				got, err := os.Readlink(path)
				if err != nil {
					t.Fatal(err)
				}
				want := filepath.Join(rootPath, "source-"+kind)
				if kind == "dangling-symlink" {
					want = filepath.Join(rootPath, "missing-target")
				}
				if got != want {
					t.Fatalf("existing symlink target changed to %q, want %q", got, want)
				}
			}
		})
	}
}

func TestOutputRenameNoReplaceValidationAndCancellation(t *testing.T) {
	ctx := context.Background()
	rootPath := outputRenameRoot(t)
	root := openOutputRenameRoot(t, rootPath)
	defer func() { _ = root.Close() }()
	mutation := root.(OutputMutation)
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "a:b"} {
		if _, err := mutation.OpenExistingRegular(ctx, name); err == nil {
			t.Errorf("OpenExistingRegular(%q) succeeded", name)
		}
		if err := mutation.RenameNoReplace(ctx, root, name, root, "target"); err == nil {
			t.Errorf("RenameNoReplace source %q succeeded", name)
		}
		if err := mutation.RenameNoReplace(ctx, root, "source", root, name); err == nil {
			t.Errorf("RenameNoReplace target %q succeeded", name)
		}
	}
	if err := mutation.RenameNoReplace(ctx, root, "same", root, "same"); err == nil {
		t.Fatal("same parent and name was accepted")
	}
	file, err := root.CreateExclusive(ctx, "source")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := mutation.RenameNoReplace(canceled, root, "source", root, "target"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled rename error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(rootPath, "source")); err != nil {
		t.Fatalf("canceled operation changed source: %v", err)
	}
	if err := mutation.RenameNoReplace(ctx, outputDirectoryWrapper{root}, "source", root, "target"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("wrapped parent error = %v", err)
	}
	closed, err := root.CreateDirExclusive(ctx, "closed")
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := closed.(OutputMutation).OpenExistingRegular(ctx, "source"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("open through closed capability error = %v", err)
	}
	if err := mutation.RenameNoReplace(ctx, closed, "source", root, "target"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("rename through closed capability error = %v", err)
	}
}

// Concurrent candidates for one target must preserve the no-overwrite invariant.
func TestOutputRenameNoReplaceConcurrentWritersHaveOneWinner(t *testing.T) {
	ctx := context.Background()
	rootPath := outputRenameRoot(t)
	root := openOutputRenameRoot(t, rootPath)
	defer func() { _ = root.Close() }()
	if err := os.Mkdir(filepath.Join(rootPath, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	target, err := root.OpenOrCreateDir(ctx, "target")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()

	const writers = 12
	type candidate struct {
		name    string
		content []byte
		info    os.FileInfo
	}
	candidates := make([]candidate, writers)
	type result struct {
		index int
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, writers)
	for i := 0; i < writers; i++ {
		name := fmt.Sprintf("source-%d", i)
		content := []byte(fmt.Sprintf("content-%d", i))
		file, err := root.CreateExclusive(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(ctx, content); err != nil {
			t.Fatal(err)
		}
		info, err := file.Stat(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		candidates[i] = candidate{name: name, content: content, info: info}
	}

	for i, source := range candidates {
		go func(index int, candidate candidate) {
			<-start
			results <- result{index: index, err: root.(OutputMutation).RenameNoReplace(ctx, root, candidate.name, target, "target")}
		}(i, source)
	}
	close(start)

	winner := -1
	wins := 0
	for range writers {
		result := <-results
		if result.err == nil {
			winner = result.index
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("successful concurrent renames = %d, want 1", wins)
	}
	targetPath := filepath.Join(rootPath, "target", "target")
	targetInfo, err := os.Stat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	winningSource := candidates[winner]
	if string(got) != string(winningSource.content) {
		t.Fatalf("target content = %q, want %q", got, winningSource.content)
	}
	if !os.SameFile(winningSource.info, targetInfo) {
		t.Fatal("target is not the winning source file")
	}
	for i, source := range candidates {
		path := filepath.Join(rootPath, source.name)
		info, err := os.Stat(path)
		if i == winner {
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("winning source stat error = %v, want not-exist", err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("losing source %q stat: %v", source.name, err)
		}
		if !os.SameFile(source.info, info) {
			t.Errorf("losing source %q identity changed", source.name)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(source.content) {
			t.Errorf("losing source %q content = %q, %v", source.name, got, err)
		}
	}
}

func outputRenameRoot(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func openOutputRenameRoot(t *testing.T, path string) OutputDirectory {
	t.Helper()
	dir, err := NewOutputOpener().OpenRoot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

type outputDirectoryWrapper struct{ OutputDirectory }
