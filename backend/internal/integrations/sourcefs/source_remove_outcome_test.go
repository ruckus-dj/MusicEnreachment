package sourcefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSourceRemoverRemoveWithOutcome(t *testing.T) {
	t.Run("removed", func(t *testing.T) {
		root, path, info := sourceFixture(t)
		outcome, err := (SourceRemover{}).RemoveWithOutcome(context.Background(), root, "track.flac", info.Size(), info.ModTime())
		if err != nil || outcome != SourceRemovalRemoved {
			t.Fatalf("RemoveWithOutcome() = (%v, %v), want (%v, nil)", outcome, err, SourceRemovalRemoved)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("removed file remains or unexpected error: %v", err)
		}
	})

	t.Run("missing leaf", func(t *testing.T) {
		root := canonicalTempDir(t)
		outcome, err := (SourceRemover{}).RemoveWithOutcome(context.Background(), root, "missing.flac", 0, time.Time{})
		if outcome != SourceRemovalMissingLeaf || !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("RemoveWithOutcome() = (%v, %v), want (%v, os.ErrNotExist)", outcome, err, SourceRemovalMissingLeaf)
		}
		if ordinaryErr := (SourceRemover{}).Remove(context.Background(), root, "ordinary-missing.flac", 0, time.Time{}); !errors.Is(ordinaryErr, os.ErrNotExist) {
			t.Fatalf("Remove() error = %v, want os.ErrNotExist", ordinaryErr)
		}
	})

	t.Run("missing root", func(t *testing.T) {
		parent := canonicalTempDir(t)
		root := filepath.Join(parent, "missing-root")
		outcome, err := (SourceRemover{}).RemoveWithOutcome(context.Background(), root, "track.flac", 0, time.Time{})
		if outcome != SourceRemovalUnresolved || !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("RemoveWithOutcome() = (%v, %v), want (%v, os.ErrNotExist)", outcome, err, SourceRemovalUnresolved)
		}
	})

	t.Run("missing parent", func(t *testing.T) {
		root := canonicalTempDir(t)
		outcome, err := (SourceRemover{}).RemoveWithOutcome(context.Background(), root, "missing-parent/track.flac", 0, time.Time{})
		if outcome != SourceRemovalUnresolved || !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("RemoveWithOutcome() = (%v, %v), want (%v, os.ErrNotExist)", outcome, err, SourceRemovalUnresolved)
		}
	})

	for _, change := range []string{"size", "mtime"} {
		t.Run("changed "+change, func(t *testing.T) {
			root, path, info := sourceFixture(t)
			size, mtime := info.Size(), info.ModTime()
			if change == "size" {
				size++
			} else {
				mtime = mtime.Add(time.Second)
			}
			outcome, err := (SourceRemover{}).RemoveWithOutcome(context.Background(), root, "track.flac", size, mtime)
			if outcome != SourceRemovalUnresolved || !errors.Is(err, ErrSourceChanged) {
				t.Fatalf("RemoveWithOutcome() = (%v, %v), want (%v, ErrSourceChanged)", outcome, err, SourceRemovalUnresolved)
			}
			if _, statErr := os.Lstat(path); statErr != nil {
				t.Fatalf("changed file was removed: %v", statErr)
			}
		})
	}

	t.Run("terminal symlink", func(t *testing.T) {
		root, path, info := sourceFixture(t)
		link := filepath.Join(root, "link.flac")
		if err := os.Symlink(path, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		outcome, err := (SourceRemover{}).RemoveWithOutcome(context.Background(), root, "link.flac", info.Size(), info.ModTime())
		if outcome != SourceRemovalUnresolved || !errors.Is(err, ErrLink) {
			t.Fatalf("RemoveWithOutcome() = (%v, %v), want (%v, ErrLink)", outcome, err, SourceRemovalUnresolved)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		root := canonicalTempDir(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		outcome, err := (SourceRemover{}).RemoveWithOutcome(ctx, root, "missing.flac", 0, time.Time{})
		if outcome != SourceRemovalUnresolved || !errors.Is(err, context.Canceled) {
			t.Fatalf("RemoveWithOutcome() = (%v, %v), want (%v, context.Canceled)", outcome, err, SourceRemovalUnresolved)
		}
	})
}
