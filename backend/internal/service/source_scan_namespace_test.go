package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnumerateSourceTreeRejectsReplacedRootNamespace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not allow renaming a source root while enumeration holds its directory handle")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeSourceScanFile(t, root, "album/track.flac", "original")
	replacement := t.TempDir()
	writeSourceScanFile(t, replacement, "album/track.flac", "replacement")
	moved := root + "-pinned"
	t.Cleanup(func() {
		_ = os.RemoveAll(moved)
	})
	var replaced bool
	_, err = EnumerateSourceTree(context.Background(), root, func(SourceWalkEntry) error {
		if replaced {
			return nil
		}
		if err := os.Rename(root, moved); err != nil {
			return err
		}
		if err := os.Rename(replacement, root); err != nil {
			return err
		}
		replaced = true
		return nil
	})
	if err == nil {
		t.Fatal("enumeration succeeded after the pathname root was replaced")
	}
	if !replaced {
		t.Fatal("enumeration did not visit the source file")
	}
}
