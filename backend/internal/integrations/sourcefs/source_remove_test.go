package sourcefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// canonicalTempDir returns t.TempDir with every link-resolved ancestor. The
// opener resolves root components relative to their parent handle without
// following links, so a symlinked ancestor (for example /var on macOS, which
// links to /private/var) is correctly refused as a non-directory. Tests must
// therefore pass the canonical path, matching how production roots are named.
func canonicalTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func sourceFixture(t *testing.T) (string, string, os.FileInfo) {
	t.Helper()
	root := canonicalTempDir(t)
	path := filepath.Join(root, "track.flac")
	if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return root, path, info
}

func TestSourceRemoverRemovesOnlyNamedFile(t *testing.T) {
	root, path, info := sourceFixture(t)
	sibling := filepath.Join(root, "keep.flac")
	if err := os.WriteFile(sibling, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := (SourceRemover{}).Remove(context.Background(), root, "track.flac", info.Size(), info.ModTime().Truncate(time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target remains or unexpected error: %v", err)
	}
	if _, err := os.Lstat(sibling); err != nil {
		t.Fatalf("sibling was removed: %v", err)
	}
}

func TestSourceRemoverRejectsChangedFile(t *testing.T) {
	for _, change := range []string{"size", "mtime"} {
		t.Run(change, func(t *testing.T) {
			root, path, info := sourceFixture(t)
			size, mtime := info.Size(), info.ModTime().Truncate(time.Microsecond)
			if change == "size" {
				size++
			} else {
				mtime = mtime.Add(time.Second)
			}
			if err := (SourceRemover{}).Remove(context.Background(), root, "track.flac", size, mtime); !errors.Is(err, ErrSourceChanged) {
				t.Fatalf("Remove() error = %v, want ErrSourceChanged", err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("changed file was removed: %v", err)
			}
		})
	}
}

func TestSourceRemoverValidatesPathAndTerminal(t *testing.T) {
	root, path, info := sourceFixture(t)
	if err := (SourceRemover{}).Remove(context.Background(), "relative-root", "track.flac", info.Size(), info.ModTime()); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("relative root Remove() error = %v, want ErrInvalidPath", err)
	}
	for _, relative := range []string{"../track.flac", filepath.Join("..", "escape"), ""} {
		if err := (SourceRemover{}).Remove(context.Background(), root, relative, info.Size(), info.ModTime()); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Remove(%q) error = %v, want ErrInvalidPath", relative, err)
		}
	}
	link := filepath.Join(root, "link.flac")
	if err := os.Symlink(path, link); err == nil {
		if err := (SourceRemover{}).Remove(context.Background(), root, "link.flac", info.Size(), info.ModTime()); !errors.Is(err, ErrLink) {
			t.Errorf("symlink Remove() error = %v, want ErrLink", err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (SourceRemover{}).Remove(context.Background(), root, "directory", 0, info.ModTime()); !errors.Is(err, ErrNotRegular) {
		t.Errorf("directory Remove() error = %v, want ErrNotRegular", err)
	}
}

func TestSourceRemoverMissingAndCancelled(t *testing.T) {
	root, path, info := sourceFixture(t)
	remover := SourceRemover{}
	if err := remover.Remove(context.Background(), root, "missing", 0, info.ModTime()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing Remove() error = %v, want os.ErrNotExist", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := remover.Remove(ctx, root, "track.flac", info.Size(), info.ModTime()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Remove() error = %v, want context.Canceled", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("cancelled operation removed file: %v", err)
	}
}

func TestSourceRemoverLeavesHardlinkAlias(t *testing.T) {
	root, path, info := sourceFixture(t)
	alias := filepath.Join(root, "alias.flac")
	if err := os.Link(path, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if err := (SourceRemover{}).Remove(context.Background(), root, "track.flac", info.Size(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(alias); err != nil {
		t.Fatalf("hard-link alias was affected: %v", err)
	}
}
