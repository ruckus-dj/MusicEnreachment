package service_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// sourceWalkTestExtensions lists the 13 approved audio extensions the walk must
// accept, spelled independently of the service so a typo there fails here.
var sourceWalkTestExtensions = []string{
	"flac", "wav", "aif", "aiff", "ape", "wv", "mp3", "m4a", "aac", "ogg", "opus", "wma", "mka",
}

var errStopSourceWalk = errors.New("stop the walk")

func TestWalkSourceTreeVisitsApprovedExtensionsIgnoringCase(t *testing.T) {
	root := t.TempDir()
	expected := map[string]string{}
	for index, extension := range sourceWalkTestExtensions {
		lower := fmt.Sprintf("%02d-lower.%s", index, extension)
		upper := filepath.Join("disc", fmt.Sprintf("%02d-upper.%s", index, strings.ToUpper(extension)))
		mixedExtension := strings.ToUpper(extension[:1]) + extension[1:]
		mixed := filepath.Join("disc", "nested", fmt.Sprintf("%02d-mixed.%s", index, mixedExtension))
		for _, relative := range []string{lower, upper, mixed} {
			content := "audio bytes of " + relative
			expected[relative] = content
			writeSourceWalkFile(t, filepath.Join(root, relative), content)
		}
	}
	// Files the inventory must never record: another format, an extensionless
	// file, a misleading double extension and a directory named like an approved
	// file.
	for _, relative := range []string{"cover.jpg", "notes.txt", "README", "album.flac.bak", filepath.Join("disc", "notes.flac.txt")} {
		writeSourceWalkFile(t, filepath.Join(root, relative), "not audio")
	}
	writeSourceWalkFile(t, filepath.Join(root, "disc", "nested", "album.flac", "inside.txt"), "not audio")
	if err := os.MkdirAll(filepath.Join(root, "disc", "empty"), 0o755); err != nil {
		t.Fatalf("create an empty directory: %v", err)
	}

	before := sourceWalkFingerprint(t, root)
	entries, err := collectSourceWalk(t, context.Background(), root)
	if err != nil {
		t.Fatalf("walk the source tree: %v", err)
	}
	if len(entries) != len(expected) {
		t.Fatalf("walked %d files, want %d approved files: %v", len(entries), len(expected), sourceWalkRelativePaths(entries))
	}
	visited := map[string]struct{}{}
	for _, entry := range entries {
		visited[entry.RelativePath] = struct{}{}
		content, approved := expected[entry.RelativePath]
		if !approved {
			t.Errorf("walked %q, which is not an approved audio file with its exact path", entry.RelativePath)
			continue
		}
		absolute := filepath.Join(root, filepath.FromSlash(entry.RelativePath))
		if entry.SizeBytes != int64(len(content)) {
			t.Errorf("size of %q = %d, want %d", entry.RelativePath, entry.SizeBytes, len(content))
		}
		info, err := os.Stat(absolute)
		if err != nil {
			t.Fatalf("stat %q: %v", absolute, err)
		}
		if !entry.Mtime.Equal(info.ModTime()) {
			t.Errorf("mtime of %q = %v, want the stat mtime %v", entry.RelativePath, entry.Mtime, info.ModTime())
		}
	}
	for relative := range expected {
		if _, ok := visited[relative]; !ok {
			t.Errorf("approved file %q was not visited", relative)
		}
	}
	if after := sourceWalkFingerprint(t, root); after != before {
		t.Errorf("the walk changed the source tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestWalkSourceTreePreservesExactPathCase(t *testing.T) {
	root := t.TempDir()
	writeSourceWalkFile(t, filepath.Join(root, "Album", "Track.FLAC"), "audio bytes")

	entries, err := collectSourceWalk(t, context.Background(), root)
	if err != nil {
		t.Fatalf("walk the source tree: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("walked %v, want the single file Album/Track.FLAC", sourceWalkRelativePaths(entries))
	}
	want := service.SourceWalkEntry{
		RelativePath: "Album/Track.FLAC",
		SizeBytes:    int64(len("audio bytes")),
	}
	if entries[0].RelativePath != want.RelativePath || entries[0].SizeBytes != want.SizeBytes {
		t.Errorf("walked %+v, want the exact case-preserving path %+v", entries[0], want)
	}
}

func TestWalkSourceTreeKeepsCaseDistinctPathsOnCaseSensitiveFilesystems(t *testing.T) {
	if !sourceWalkFilesystemIsCaseSensitive(t, t.TempDir()) {
		t.Skip("the filesystem folds case, so paths differing only in case cannot coexist")
	}
	root := t.TempDir()
	writeSourceWalkFile(t, filepath.Join(root, "Album", "track.flac"), "upper album")
	writeSourceWalkFile(t, filepath.Join(root, "album", "track.flac"), "lower album")

	entries, err := collectSourceWalk(t, context.Background(), root)
	if err != nil {
		t.Fatalf("walk the source tree: %v", err)
	}
	want := []string{"Album/track.flac", "album/track.flac"}
	if got := sourceWalkRelativePaths(entries); !slices.Equal(got, want) {
		t.Errorf("walked %v, want the two case-distinct paths %v", got, want)
	}
}

func TestWalkSourceTreeSkipsSymlinksAndCannotLeaveTheRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeSourceWalkFile(t, filepath.Join(root, "Music", "track.flac"), "inside")
	writeSourceWalkFile(t, filepath.Join(outside, "escaped.flac"), "outside")
	sourceWalkSymlink(t, filepath.Join(outside, "escaped.flac"), filepath.Join(root, "escape.flac"))
	sourceWalkSymlink(t, outside, filepath.Join(root, "escape-dir"))
	sourceWalkSymlink(t, root, filepath.Join(root, "loop"))
	sourceWalkSymlink(t, filepath.Join(root, "Music"), filepath.Join(root, "Music-link"))
	sourceWalkSymlink(t, filepath.Join(root, "missing.flac"), filepath.Join(root, "dangling.flac"))

	entries, err := collectSourceWalk(t, context.Background(), root)
	if err != nil {
		t.Fatalf("walk the source tree: %v", err)
	}
	want := []string{"Music/track.flac"}
	if got := sourceWalkRelativePaths(entries); !slices.Equal(got, want) {
		t.Errorf("walked %v, want only the file reachable without following a link: %v", got, want)
	}
}

func TestWalkSourceTreeFailsOnUnreadableSubtree(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	writeSourceWalkFile(t, filepath.Join(root, "aaa.flac"), "visited before the failure")
	writeSourceWalkFile(t, filepath.Join(root, "locked", "hidden.flac"), "behind an unreadable directory")
	locked := filepath.Join(root, "locked")
	lockSourceWalkDirectory(t, locked)

	entries, err := collectSourceWalk(t, context.Background(), root)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("walk error = %v, want an unreadable subtree failure", err)
	}
	for _, entry := range entries {
		if entry.RelativePath == filepath.Join("locked", "hidden.flac") {
			t.Errorf("walked %q inside an unreadable directory", entry.RelativePath)
		}
	}
}

func TestWalkSourceTreeFailsOnCancellation(t *testing.T) {
	root := t.TempDir()
	writeSourceWalkFile(t, filepath.Join(root, "a.flac"), "first")
	writeSourceWalkFile(t, filepath.Join(root, "b.flac"), "second")

	t.Run("before the first entry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		entries, err := collectSourceWalk(t, ctx, root)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("walk error = %v, want a cancellation", err)
		}
		if len(entries) != 0 {
			t.Errorf("walked %v with a canceled context, want nothing", sourceWalkRelativePaths(entries))
		}
	})

	t.Run("when the visitor cancels", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var visited []string
		err := service.WalkSourceTree(ctx, sourceWalkResolvedPath(t, root), func(entry service.SourceWalkEntry) error {
			visited = append(visited, entry.RelativePath)
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("walk error = %v, want a cancellation after the first visit", err)
		}
		if len(visited) != 1 {
			t.Errorf("visited %v, want exactly one entry before cancellation", visited)
		}
	})

	t.Run("when the only-file visitor cancels", func(t *testing.T) {
		singleRoot := t.TempDir()
		writeSourceWalkFile(t, filepath.Join(singleRoot, "only.flac"), "first")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var visited []string
		err := service.WalkSourceTree(ctx, sourceWalkResolvedPath(t, singleRoot), func(entry service.SourceWalkEntry) error {
			visited = append(visited, entry.RelativePath)
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("walk error = %v, want cancellation rather than successful short-batch completion", err)
		}
		if want := []string{"only.flac"}; !slices.Equal(visited, want) {
			t.Errorf("visited %v, want %v", visited, want)
		}
	})
}

func TestWalkSourceTreeStopsOnVisitorError(t *testing.T) {
	root := t.TempDir()
	writeSourceWalkFile(t, filepath.Join(root, "a.flac"), "first")
	writeSourceWalkFile(t, filepath.Join(root, "b.flac"), "second")

	var visited []string
	err := service.WalkSourceTree(context.Background(), sourceWalkResolvedPath(t, root), func(entry service.SourceWalkEntry) error {
		visited = append(visited, entry.RelativePath)
		return errStopSourceWalk
	})
	if !errors.Is(err, errStopSourceWalk) {
		t.Fatalf("walk error = %v, want the visitor error", err)
	}
	if len(visited) != 1 {
		t.Errorf("visited %v, want exactly one entry before the visitor error", visited)
	}
}

func TestWalkSourceTreeRejectsRootsThatAreNotDirectories(t *testing.T) {
	ctx := context.Background()
	t.Run("a missing root", func(t *testing.T) {
		_, err := collectSourceWalk(t, ctx, filepath.Join(t.TempDir(), "missing"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("walk error = %v, want a missing root failure", err)
		}
	})
	t.Run("a regular file", func(t *testing.T) {
		root := t.TempDir()
		writeSourceWalkFile(t, filepath.Join(root, "track.flac"), "audio")
		if _, err := collectSourceWalk(t, ctx, filepath.Join(root, "track.flac")); err == nil {
			t.Fatalf("a regular file was accepted as a source root")
		}
	})
	t.Run("a relative root", func(t *testing.T) {
		if _, err := collectSourceWalk(t, ctx, filepath.Join("relative", "root")); err == nil {
			t.Fatalf("a relative path was accepted as a source root")
		}
	})
	t.Run("an empty directory", func(t *testing.T) {
		entries, err := collectSourceWalk(t, ctx, t.TempDir())
		if err != nil {
			t.Fatalf("walk an empty source tree: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("walked %v from an empty tree", sourceWalkRelativePaths(entries))
		}
	})
}

func collectSourceWalk(t *testing.T, ctx context.Context, root string) ([]service.SourceWalkEntry, error) {
	t.Helper()
	var entries []service.SourceWalkEntry
	err := service.WalkSourceTree(ctx, sourceWalkResolvedPath(t, root), func(entry service.SourceWalkEntry) error {
		entries = append(entries, entry)
		return nil
	})
	return entries, err
}

func sourceWalkResolvedPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	parent, parentErr := filepath.EvalSymlinks(filepath.Dir(path))
	if parentErr == nil && errors.Is(err, fs.ErrNotExist) {
		return filepath.Join(parent, filepath.Base(path))
	}
	return path
}

func sourceWalkRelativePaths(entries []service.SourceWalkEntry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.RelativePath)
	}
	slices.Sort(paths)
	return paths
}

// sourceWalkFingerprint records everything about the tree that a read-only walk
// must leave alone: the set of entries, their type, size, mtime, symlink targets
// and contents.
func sourceWalkFingerprint(t *testing.T, root string) string {
	t.Helper()
	var records []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return relativeErr
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		// Windows updates directory mtimes lazily, so reading a directory
		// can make its reported timestamp change without modifying the tree.
		// Keep checking directory mtimes on Unix and every file on all hosts.
		record := fmt.Sprintf("%s|%d", info.Mode(), info.Size())
		if !info.IsDir() || runtime.GOOS != "windows" {
			record += "|" + info.ModTime().UTC().Format(time.RFC3339Nano)
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, linkErr := os.Readlink(path)
			if linkErr != nil {
				return linkErr
			}
			record += "|->" + target
		case info.Mode().IsRegular():
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			record += "|" + string(content)
		}
		records = append(records, relative+"="+record)
		return nil
	})
	if err != nil {
		t.Fatalf("fingerprint the source tree %q: %v", root, err)
	}
	slices.Sort(records)
	return strings.Join(records, "\n")
}

func sourceWalkFilesystemIsCaseSensitive(t *testing.T, probe string) bool {
	t.Helper()
	lower := filepath.Join(probe, "case-probe-lower")
	if err := os.WriteFile(lower, []byte("probe"), 0o644); err != nil {
		t.Fatalf("write the case probe: %v", err)
	}
	_, err := os.Stat(filepath.Join(probe, "CASE-PROBE-LOWER"))
	if err == nil {
		return false
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat the case probe: %v", err)
	}
	return true
}

func writeSourceWalkFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func lockSourceWalkDirectory(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Mode bits do not restrict enumeration on Windows. Deny only directory
		// listing to Everyone, then remove the ACE before TempDir cleanup.
		if output, err := exec.Command("icacls", path, "/deny", "*S-1-1-0:(RD)").CombinedOutput(); err != nil {
			t.Fatalf("deny directory listing: %v: %s", err, output)
		}
		t.Cleanup(func() {
			if output, err := exec.Command("icacls", path, "/remove:d", "*S-1-1-0").CombinedOutput(); err != nil {
				t.Errorf("restore directory listing: %v: %s", err, output)
			}
		})
	} else {
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("lock the subdirectory: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
	}
	if _, err := os.ReadDir(path); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("locked directory read = %v, want a permission failure", err)
	}
}

func sourceWalkSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create the symlink %q -> %q: %v", link, target, err)
	}
}

// TestWalkSourceTreeMarksOnlyAnInaccessibleRoot proves the marker the scan
// treats as an unavailable root is set by the registered directory alone: an
// unreadable subtree and a canceled walk fail without it, while the root
// directory itself failing to read carries it.
func TestWalkSourceTreeMarksOnlyAnInaccessibleRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	t.Run("unreadable subtree", func(t *testing.T) {
		root := t.TempDir()
		locked := filepath.Join(root, "locked")
		if err := os.MkdirAll(locked, 0o755); err != nil {
			t.Fatalf("create the subtree: %v", err)
		}
		writeSourceWalkFile(t, filepath.Join(locked, "hidden.flac"), "audio bytes")
		lockSourceWalkDirectory(t, locked)

		err := service.WalkSourceTree(context.Background(), sourceWalkResolvedPath(t, root), func(service.SourceWalkEntry) error { return nil })

		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("walk error = %v, want a permission failure", err)
		}
		if errors.Is(err, service.ErrSourceRootInaccessible) {
			t.Fatalf("walk error = %v, want an unreadable subtree to leave the root accessible", err)
		}
	})
	t.Run("canceled walk", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := service.WalkSourceTree(ctx, sourceWalkResolvedPath(t, t.TempDir()), func(service.SourceWalkEntry) error { return nil })

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("walk error = %v, want the cancellation", err)
		}
		if errors.Is(err, service.ErrSourceRootInaccessible) {
			t.Fatalf("walk error = %v, want a canceled walk to leave the root accessible", err)
		}
	})
	t.Run("missing root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "gone")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("create the root: %v", err)
		}
		if err := os.Remove(root); err != nil {
			t.Fatalf("remove the root: %v", err)
		}

		err := service.WalkSourceTree(context.Background(), sourceWalkResolvedPath(t, root), func(service.SourceWalkEntry) error { return nil })

		if !errors.Is(err, service.ErrSourceRootInaccessible) {
			t.Fatalf("walk error = %v, want the root access marker", err)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("walk error = %v, want the underlying not-exist failure", err)
		}
	})
}
