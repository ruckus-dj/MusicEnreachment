package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestBrowseDirectoriesListsDirectoriesOnlyWithoutMutation(t *testing.T) {
	root := t.TempDir()
	// Temporary roots can be symlinked (on macOS /var resolves to /private/var),
	// and the listing reports the normalized path.
	wantRoot, err := settings.NormalizePath(root)
	if err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o750); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	listing, err := (*SetupService)(nil).BrowseDirectories(context.Background(), root, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	wantChild := filepath.Join(listing.CurrentPath, "child")
	if len(listing.Directories) != 1 || listing.Directories[0] != wantChild {
		t.Fatalf("directories = %v, want [%s]", listing.Directories, wantChild)
	}
	if listing.CurrentPath != wantRoot || listing.ParentPath != filepath.Dir(wantRoot) {
		t.Fatalf("unexpected navigation paths: %#v", listing)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("listing changed existing file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatalf("listing created a path: %v", err)
	}
}

func TestBrowseDirectoriesRejectsMissingAndRelativePaths(t *testing.T) {
	for _, path := range []string{"relative", filepath.Join(t.TempDir(), "missing")} {
		if _, err := (*SetupService)(nil).BrowseDirectories(context.Background(), path, 0, 200); err == nil {
			t.Errorf("BrowseDirectories(%q) succeeded", path)
		}
	}
}

func TestBrowseDirectoriesDefaultsToNativeRoot(t *testing.T) {
	want := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		workingDirectory, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		want = filepath.VolumeName(workingDirectory) + string(filepath.Separator)
	}
	want, err := settings.NormalizePath(want)
	if err != nil {
		t.Fatal(err)
	}
	listing, err := (*SetupService)(nil).BrowseDirectories(context.Background(), "", 0, 200)
	if err != nil {
		t.Fatalf("BrowseDirectories(\"\") failed on %s: %v", runtime.GOOS, err)
	}
	if listing.CurrentPath != want {
		t.Fatalf("current path = %q, want %q", listing.CurrentPath, want)
	}
	if !filepath.IsAbs(listing.CurrentPath) {
		t.Fatalf("current path %q is not absolute", listing.CurrentPath)
	}
	if listing.Offset != 0 {
		t.Fatalf("offset = %d, want 0", listing.Offset)
	}
	if len(listing.Roots) == 0 {
		t.Fatalf("roots = %v, want at least one root", listing.Roots)
	}
}

func TestBrowseDirectoriesRejectsInvalidLimit(t *testing.T) {
	root := t.TempDir()
	for _, limit := range []int{-1, 0, 201} {
		if _, err := (*SetupService)(nil).BrowseDirectories(context.Background(), root, 0, limit); err == nil {
			t.Errorf("BrowseDirectories(limit=%d) succeeded", limit)
		}
	}
}

func TestBrowseDirectoriesPaginatesAndRejectsUnavailableOffset(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	first, err := (*SetupService)(nil).BrowseDirectories(context.Background(), root, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Directories) != 2 || first.NextOffset == nil || *first.NextOffset != 2 {
		t.Fatalf("first page = %#v, want 2 directories and next offset 2", first)
	}
	last, err := (*SetupService)(nil).BrowseDirectories(context.Background(), root, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Directories) != 1 || last.NextOffset != nil {
		t.Fatalf("last page = %#v, want 1 directory and no next offset", last)
	}
	// An offset equal to the directory count is a valid empty page.
	empty, err := (*SetupService)(nil).BrowseDirectories(context.Background(), root, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Directories) != 0 || empty.NextOffset != nil {
		t.Fatalf("offset page = %#v, want no directories", empty)
	}
	if _, err := (*SetupService)(nil).BrowseDirectories(context.Background(), root, -1, 2); err == nil {
		t.Error("BrowseDirectories(offset=-1) succeeded")
	}
	if _, err := (*SetupService)(nil).BrowseDirectories(context.Background(), root, 4, 2); err == nil {
		t.Error("BrowseDirectories(offset=4) succeeded")
	}
}

func TestBrowseDirectoriesCapsRawEntryScanAndPaginatesEmptyPages(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 1001; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%04d", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	first, err := (*SetupService)(nil).BrowseDirectories(context.Background(), root, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Directories) != 0 || first.NextOffset == nil || *first.NextOffset != 1000 {
		t.Fatalf("first page = %#v, want empty page with next raw-entry offset 1000", first)
	}
	last, err := (*SetupService)(nil).BrowseDirectories(context.Background(), root, *first.NextOffset, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Directories) != 0 || last.NextOffset != nil {
		t.Fatalf("last page = %#v, want empty final page", last)
	}
}

func TestBrowseDirectoriesHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (*SetupService)(nil).BrowseDirectories(ctx, t.TempDir(), 0, 200); err == nil {
		t.Fatal("BrowseDirectories succeeded with canceled context")
	}
}
