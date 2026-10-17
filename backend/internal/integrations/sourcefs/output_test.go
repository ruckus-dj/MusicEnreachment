package sourcefs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestOutputCapabilityCreateWriteReadAndExclusive(t *testing.T) {
	ctx := context.Background()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir, err := NewOutputOpener().OpenRoot(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Verify(ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	child, err := dir.OpenOrCreateDir(ctx, "stage")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Close() }()
	file, err := child.CreateExclusive(ctx, "00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(ctx, []byte("artifact")); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := child.CreateExclusive(ctx, "00000000-0000-0000-0000-000000000001"); err == nil {
		t.Fatal("CreateExclusive succeeded on collision")
	}
	if err := file.BorrowRead(ctx, func(f *os.File) error {
		got, e := io.ReadAll(f)
		if e == nil && string(got) != "artifact" {
			t.Errorf("read %q", got)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(path, "stage", "00000000-0000-0000-0000-000000000001"))
	if err != nil || string(got) != "artifact" {
		t.Fatalf("created content = %q, %v", got, err)
	}
}

func TestOutputDirectoryCreateDirExclusiveAndIdentity(t *testing.T) {
	ctx := context.Background()
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := NewOutputOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	child, err := root.CreateDirExclusive(ctx, "exclusive")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Close() }()
	if _, err := root.CreateDirExclusive(ctx, "exclusive"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second CreateDirExclusive error = %v, want fs.ErrExist", err)
	}
	first, err := child.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := child.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.Platform == "" || first.Volume == "" || first.FileID == "" {
		t.Fatalf("identity is not stable and complete: first=%+v second=%+v", first, second)
	}

	other, err := root.CreateDirExclusive(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	otherIdentity, err := other.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first == otherIdentity {
		t.Fatalf("distinct directories have same identity: %+v", first)
	}

	movedPath := filepath.Join(rootPath, "moved")
	if err := os.Rename(filepath.Join(rootPath, "exclusive"), movedPath); err != nil {
		t.Fatal(err)
	}
	afterRename, err := child.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first != afterRename {
		t.Fatalf("identity changed after rename: before=%+v after=%+v", first, afterRename)
	}
}

func TestOutputDirectoryCreateDirExclusiveCanceledBeforeCreate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := NewOutputOpener().OpenRoot(context.Background(), rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if _, err := root.CreateDirExclusive(ctx, "not-created"); !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateDirExclusive error = %v, want context.Canceled", err)
	}
	if _, err := os.Lstat(filepath.Join(rootPath, "not-created")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("canceled call left an entry: %v", err)
	}
}

func TestOutputDirectoryCreateDirExclusiveRejectsUnsafeComponents(t *testing.T) {
	ctx := context.Background()
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := NewOutputOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "nul\x00byte"} {
		if _, err := root.CreateDirExclusive(ctx, name); err == nil {
			t.Errorf("CreateDirExclusive(%q) succeeded", name)
		}
	}
}

func TestOutputCapabilityVerifyDetectsNamespaceReplacement(t *testing.T) {
	ctx := context.Background()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "output")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := NewOutputOpener().OpenRoot(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	if err := os.Rename(path, path+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := dir.Verify(ctx); err == nil {
		t.Fatal("Verify accepted a replacement at the configured path")
	}
}
