//go:build linux || darwin

package sourcefs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

func unixTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnixOpenerPinsRootAndTreatsBackslashAsName(t *testing.T) {
	base := unixTempDir(t)
	rootPath := filepath.Join(base, "root")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, `name\part`), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := NewOpener().OpenRoot(context.Background(), rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close root: %v", err)
		}
	})
	if err := os.Rename(rootPath, rootPath+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, `name\part`), []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := root.OpenRegular(context.Background(), `name\part`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close file: %v", err)
		}
	})
	if err := file.Borrow(context.Background(), func(opened *os.File) error {
		contents, err := io.ReadAll(opened)
		if err == nil && string(contents) != "original" {
			err = errors.New("root handle was retargeted after rename")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUnixOpenerRejectsSymlinkRacesAndFIFO(t *testing.T) {
	base := unixTempDir(t)
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "file"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(base, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(target, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}

	rootRaceOpener := NewOpener().(*unixOpener)
	rootRaceOpener.openChild = func(parent int, name string, flags int) (int, error) {
		if name == "target" {
			if err := os.Rename(target, target+"-moved"); err != nil {
				return -1, err
			}
			if err := os.Symlink(unixTempDir(t), target); err != nil {
				return -1, err
			}
		}
		return platformOpenChild(parent, name, flags)
	}
	_, err := rootRaceOpener.OpenRoot(context.Background(), target)
	linkRejected := errors.Is(err, ErrLink)
	if runtime.GOOS == "darwin" && errors.Is(err, ErrNotDirectory) {
		linkRejected = true
	}
	if !linkRejected {
		t.Fatalf("root symlink race error = %v, want ErrLink", err)
	}

	root, err := NewOpener().OpenRoot(context.Background(), target+"-moved")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close root: %v", err)
		}
	})
	if _, err := root.OpenRegular(context.Background(), "pipe"); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("FIFO open error = %v, want ErrNotRegular", err)
	}

	fileRaceOpener := NewOpener().(*unixOpener)
	dir, err := fileRaceOpener.OpenRoot(context.Background(), target+"-moved")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := dir.Close(); err != nil {
			t.Errorf("close directory: %v", err)
		}
	})
	unixDir := dir.(*unixDirectory)
	unixDir.open = func(parent int, name string, flags int) (int, error) {
		if name == "file" {
			if err := os.Rename(filepath.Join(target+"-moved", name), filepath.Join(target+"-moved", "saved")); err != nil {
				return -1, err
			}
			if err := os.Symlink("saved", filepath.Join(target+"-moved", name)); err != nil {
				return -1, err
			}
		}
		return platformOpenChild(parent, name, flags)
	}
	if _, err := dir.OpenRegular(context.Background(), "file"); !errors.Is(err, ErrLink) {
		t.Fatalf("file symlink race error = %v, want ErrLink", err)
	}
}

func TestUnixOpenerChecksCancellationAfterOpenAndBoundsReadDir(t *testing.T) {
	path := unixTempDir(t)
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(path, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opener := NewOpener().(*unixOpener)
	ctx, cancel := context.WithCancel(context.Background())
	opener.openChild = func(parent int, name string, flags int) (int, error) {
		fd, err := platformOpenChild(parent, name, flags)
		if err == nil {
			cancel()
		}
		return fd, err
	}
	if _, err := opener.OpenRoot(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation after root component open = %v, want context.Canceled", err)
	}

	dir, err := NewOpener().OpenRoot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := dir.Close(); err != nil {
			t.Errorf("close directory: %v", err)
		}
	})
	entries, err := dir.ReadDir(context.Background(), 2)
	if err != nil || len(entries) != 2 {
		t.Fatalf("ReadDir(2) = %d entries, %v; want two entries", len(entries), err)
	}
	entries, err = dir.ReadDir(context.Background(), 2)
	if err != nil || len(entries) != 1 {
		t.Fatalf("second ReadDir(2) = %d entries, %v; want one entry", len(entries), err)
	}
	if entries, err = dir.ReadDir(context.Background(), 2); !errors.Is(err, io.EOF) || len(entries) != 0 {
		t.Fatalf("terminal ReadDir = %v, %v; want empty io.EOF", entries, err)
	}
}
