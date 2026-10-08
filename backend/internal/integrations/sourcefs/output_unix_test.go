//go:build linux || darwin

package sourcefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOutputCapabilityRejectsSymlinkDirectoryAndFinal(t *testing.T) {
	ctx := context.Background()
	rootPath := unixTempDir(t)
	outside := filepath.Join(unixTempDir(t), "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(outside, "foreign")
	if err := os.WriteFile(foreign, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(rootPath, "directory-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, filepath.Join(rootPath, "file-link")); err != nil {
		t.Fatal(err)
	}
	root, err := NewOutputOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if _, err := root.OpenOrCreateDir(ctx, "directory-link"); !errors.Is(err, ErrLink) && !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("OpenOrCreateDir symlink = %v, want a no-follow rejection", err)
	}
	if _, err := root.CreateExclusive(ctx, "file-link"); err == nil {
		t.Fatal("CreateExclusive followed an existing symlink")
	}
	got, err := os.ReadFile(foreign)
	if err != nil || string(got) != "unchanged" {
		t.Fatalf("foreign file after rejected operations = %q, %v", got, err)
	}
}
