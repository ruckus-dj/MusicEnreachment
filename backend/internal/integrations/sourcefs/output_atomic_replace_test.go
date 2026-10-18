package sourcefs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOutputAtomicReplaceExisting(t *testing.T) {
	ctx := context.Background()
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir, err := NewOutputOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dir.Close(); err != nil {
			t.Errorf("close output directory: %v", err)
		}
	}()
	mutation, ok := dir.(OutputAtomicReplace)
	if !ok {
		t.Fatal("native output directory lacks atomic replacement capability")
	}

	create := func(name, data string) OutputFile {
		t.Helper()
		file, createErr := dir.CreateExclusive(ctx, name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = file.Write(ctx, []byte(data)); createErr != nil {
			t.Fatal(createErr)
		}
		return file
	}
	old := create("managed", "old")
	oldInfo, err := old.Stat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	candidate := create("candidate", "new")
	candidateInfo, err := candidate.Stat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := mutation.AtomicReplaceExisting(ctx, dir, "candidate", candidateInfo, "managed", candidateInfo); err == nil {
		t.Fatal("replacement accepted a mismatched target identity")
	}
	if got, err := os.ReadFile(filepath.Join(rootPath, "managed")); err != nil || string(got) != "old" {
		t.Fatalf("managed target changed after identity mismatch: %q, %v", got, err)
	}
	if err := mutation.AtomicReplaceExisting(ctx, dir, "candidate", candidateInfo, "managed", oldInfo); err != nil {
		t.Fatal(err)
	}
	if err := old.BorrowRead(ctx, func(file *os.File) error {
		data, readErr := io.ReadAll(file)
		if readErr == nil && string(data) != "old" {
			t.Errorf("open old handle reads %q", data)
		}
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(rootPath, "managed"))
	if err != nil || string(got) != "new" {
		t.Fatalf("managed contents = %q, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(rootPath, "candidate")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate still exists: %v", err)
	}

	missing := create("missing-candidate", "next")
	missingInfo, err := missing.Stat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := mutation.AtomicReplaceExisting(ctx, dir, "missing-candidate", missingInfo, "absent-target", oldInfo); err == nil {
		t.Fatal("replacement unexpectedly created a missing target")
	}
	if _, err := os.Lstat(filepath.Join(rootPath, "absent-target")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing target was created: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(rootPath, "managed")); err != nil || string(got) != "new" {
		t.Fatalf("managed target changed after failed replacement: %q, %v", got, err)
	}
	_ = missing.Close()

	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkName := filepath.Join(rootPath, "linked-target")
	if err := os.Symlink(outside, linkName); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linkCandidate := create("link-candidate", "replacement")
	linkInfo, err := linkCandidate.Stat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := mutation.AtomicReplaceExisting(ctx, dir, "link-candidate", linkInfo, "linked-target", oldInfo); err == nil {
		t.Fatal("replacement followed or replaced a symlink target")
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "outside" {
		t.Fatalf("symlink referent changed: %q, %v", got, err)
	}
	if _, err := os.Lstat(linkName); err != nil {
		t.Fatalf("symlink entry changed: %v", err)
	}
	_ = linkCandidate.Close()
}
