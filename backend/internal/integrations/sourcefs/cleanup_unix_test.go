//go:build linux || darwin

package sourcefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// The Windows native cleaner is covered separately by a future
// cleanup_windows_test.go; this file exercises the no-follow, no-prune
// behavior of the Unix cleaner against real symlinks and special files.

func TestCleanerRejectsSymlinkedParentWithoutTouchingTarget(t *testing.T) {
	root := unixTempDir(t)
	outside := unixTempDir(t)
	foreign := filepath.Join(outside, "foreign")
	if err := os.WriteFile(foreign, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "analysis", "staging"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The first staging component resolves through a symlink into an
	// attacker-controlled tree; cleanup must not follow it or prune it.
	if err := os.Symlink(outside, filepath.Join(root, "analysis", "staging", "00000000-0000-0000-0000-000000000001")); err != nil {
		t.Fatal(err)
	}

	outcome, err := NewCleaner().RemoveRegistered(context.Background(), root, cleanupArtifactPath)
	if !errors.Is(err, ErrLink) && !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("RemoveRegistered(symlinked parent) = (%v, %v), want a no-follow rejection", outcome, err)
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "unchanged" {
		t.Fatalf("foreign file after rejected cleanup = %q, %v; want untouched", got, err)
	}
}

func TestCleanerRejectsSymlinkedLeafWithoutTouchingTarget(t *testing.T) {
	root := unixTempDir(t)
	outside := unixTempDir(t)
	foreign := filepath.Join(outside, "foreign")
	if err := os.WriteFile(foreign, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(root, filepath.FromSlash(cleanupArtifactPath))
	if err := os.MkdirAll(filepath.Dir(artifact), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, artifact); err != nil {
		t.Fatal(err)
	}

	outcome, err := NewCleaner().RemoveRegistered(context.Background(), root, cleanupArtifactPath)
	if !errors.Is(err, ErrLink) {
		t.Fatalf("RemoveRegistered(symlinked leaf) = (%v, %v), want ErrLink", outcome, err)
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "unchanged" {
		t.Fatalf("foreign file after rejected cleanup = %q, %v; want untouched", got, err)
	}
	if info, err := os.Lstat(artifact); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("leaf after rejected cleanup = (%v, %v), want untouched symlink", info, err)
	}
}

func TestCleanerLeavesDirectoryLeafUntouched(t *testing.T) {
	root := unixTempDir(t)
	artifact := filepath.Join(root, filepath.FromSlash(cleanupArtifactPath))
	if err := os.MkdirAll(artifact, 0o700); err != nil {
		t.Fatal(err)
	}

	outcome, err := NewCleaner().RemoveRegistered(context.Background(), root, cleanupArtifactPath)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("RemoveRegistered(directory leaf) = (%v, %v), want ErrNotRegular", outcome, err)
	}
	if info, err := os.Stat(artifact); err != nil || !info.IsDir() {
		t.Fatalf("directory leaf after rejected cleanup = (%v, %v), want untouched directory", info, err)
	}
}

func TestCleanerLeavesNonRegularLeafUntouched(t *testing.T) {
	root := unixTempDir(t)
	artifact := filepath.Join(root, filepath.FromSlash(cleanupArtifactPath))
	if err := os.MkdirAll(filepath.Dir(artifact), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(artifact, 0o600); err != nil {
		t.Fatal(err)
	}

	outcome, err := NewCleaner().RemoveRegistered(context.Background(), root, cleanupArtifactPath)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("RemoveRegistered(FIFO leaf) = (%v, %v), want ErrNotRegular", outcome, err)
	}
	if info, err := os.Lstat(artifact); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO after rejected cleanup = (%v, %v), want untouched pipe", info, err)
	}
}
