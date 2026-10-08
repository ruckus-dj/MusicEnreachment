package sourcefs

import (
	"context"
	"io"
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
