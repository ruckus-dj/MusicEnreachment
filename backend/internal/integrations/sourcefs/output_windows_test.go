//go:build windows

package sourcefs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsOutputAdapterCreateExclusiveReportsExistingFile(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	const name = "foreign-output.bin"
	want := []byte("foreign file must remain unchanged")
	path := filepath.Join(rootPath, name)
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}

	root, err := NewOutputOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := root.CreateExclusive(ctx, name); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("CreateExclusive error = %v, want fs.ErrExist", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("existing file changed: got %q, want %q", got, want)
	}
}

func TestWindowsOutputAdapterOpenExistingRegularReportsMissingFile(t *testing.T) {
	ctx := context.Background()
	root, err := NewOutputOpener().OpenRoot(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	mutation := root.(OutputMutation)
	if _, err := mutation.OpenExistingRegular(ctx, "missing-output.bin"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenExistingRegular error = %v, want os.ErrNotExist", err)
	}
}

func TestWindowsOutputAdapterOpenExistingDirReportsMissingDirectory(t *testing.T) {
	ctx := context.Background()
	root, err := NewOutputOpener().OpenRoot(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	traversal := root.(OutputDirectoryTraversal)
	if _, err := traversal.OpenExistingDir(ctx, "missing-directory"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("OpenExistingDir error = %v, want fs.ErrNotExist", err)
	}
}

func TestWindowsOutputAdapterCreateDirExclusiveReportsCollision(t *testing.T) {
	ctx := context.Background()
	root, err := NewOutputOpener().OpenRoot(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	child, err := root.CreateDirExclusive(ctx, "existing-directory")
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if _, err := root.CreateDirExclusive(ctx, "existing-directory"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("CreateDirExclusive error = %v, want fs.ErrExist", err)
	}
}
