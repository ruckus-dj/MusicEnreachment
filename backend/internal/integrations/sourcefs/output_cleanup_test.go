package sourcefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOutputCleanupRemovesOnlyOwnedEntries(t *testing.T) {
	ctx := context.Background()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := NewOutputOpener().OpenRoot(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Errorf("close output root: %v", err)
		}
	}()
	cleanup, ok := root.(OutputCleanup)
	if !ok {
		t.Fatal("native output directory does not implement OutputCleanup")
	}
	parentID, err := root.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}

	ownedFile, err := root.CreateExclusive(ctx, "owned")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownedFile.Write(ctx, []byte("owned")); err != nil {
		t.Fatal(err)
	}
	owned, err := ownedFile.Stat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := ownedFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cleanup.RemoveOwnedRegular(ctx, "owned", parentID, owned); err != nil {
		t.Fatalf("remove owned file: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(path, "owned")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned file remains: %v", err)
	}

	foreignFile, err := root.CreateExclusive(ctx, "foreign")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreignFile.Write(ctx, []byte("foreign")); err != nil {
		t.Fatal(err)
	}
	foreign, err := foreignFile.Stat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := foreignFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "replacement"), []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(path, "replacement"), filepath.Join(path, "foreign")); err != nil {
		t.Fatal(err)
	}
	if err := cleanup.RemoveOwnedRegular(ctx, "foreign", parentID, foreign); err == nil {
		t.Fatal("removed substituted file")
	}
	if _, err := os.Stat(filepath.Join(path, "foreign")); err != nil {
		t.Fatalf("replacement was not preserved: %v", err)
	}

	if err := os.Mkdir(filepath.Join(path, "link-target"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(path, "link-target"), filepath.Join(path, "link")); err == nil {
		if err := cleanup.RemoveEmptyChildDir(ctx, "link", parentID, DirectoryIdentity{}); err == nil {
			t.Fatal("removed symlink as a child directory")
		}
		if _, err := os.Stat(filepath.Join(path, "link-target")); err != nil {
			t.Fatalf("symlink target changed: %v", err)
		}
	}

	child, err := root.CreateDirExclusive(ctx, "empty")
	if err != nil {
		t.Fatal(err)
	}
	childID, err := child.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cleanup.RemoveEmptyChildDir(ctx, "empty", parentID, childID); err != nil {
		t.Fatalf("remove empty child: %v", err)
	}
	if err := os.Mkdir(filepath.Join(path, "nonempty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "nonempty", "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	child, err = root.OpenOrCreateDir(ctx, "nonempty")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := child.Identity(ctx)
	_ = child.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup.RemoveEmptyChildDir(ctx, "nonempty", parentID, identity); err == nil {
		t.Fatal("removed non-empty directory")
	}
	if _, err := os.Stat(filepath.Join(path, "nonempty", "keep")); err != nil {
		t.Fatalf("foreign child was not preserved: %v", err)
	}
}
