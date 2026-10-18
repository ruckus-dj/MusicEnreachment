package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestOpenPublicationTargetParentCreatesOnlyManifestApprovedDirectories(t *testing.T) {
	ctx := context.Background()
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := sourcefs.NewOutputOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	nodes := []persistence.PublicationNamespaceNode{
		{CanonicalKey: "Artist", RelativePath: "Artist", NodeKind: "dir", DirectoryScope: "artist"},
		{CanonicalKey: "Artist/Album", RelativePath: "Artist/Album", NodeKind: "dir", DirectoryScope: "album"},
		{CanonicalKey: "Artist/Album/track.flac", RelativePath: "Artist/Album/track.flac", NodeKind: "file"},
	}
	parent, err := openPublicationTargetParent(ctx, root, "Artist/Album/track.flac", nodes)
	if runtime.GOOS == "windows" {
		if !errors.Is(err, sourcefs.ErrUnsupported) {
			t.Fatalf("openPublicationTargetParent error = %v, want sourcefs.ErrUnsupported on Windows", err)
		}
		if _, statErr := os.Lstat(filepath.Join(rootPath, "Artist")); statErr != nil {
			t.Fatalf("created approved Artist directory was not retained: %v", statErr)
		}
		if _, statErr := os.Lstat(filepath.Join(rootPath, "Artist", "Album")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("Album directory should not be created after unsupported sync: %v", statErr)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"Artist", filepath.Join("Artist", "Album")} {
		info, err := os.Lstat(filepath.Join(rootPath, directory))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("expected ordinary created directory %q, info=%v err=%v", directory, info, err)
		}
	}
}

func TestOpenPublicationTargetParentRejectsUnapprovedAncestor(t *testing.T) {
	ctx := context.Background()
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := sourcefs.NewOutputOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	_, err = openPublicationTargetParent(ctx, root, "Artist/Album/track.flac", []persistence.PublicationNamespaceNode{
		{CanonicalKey: "Artist", RelativePath: "Artist", NodeKind: "dir", DirectoryScope: "artist"},
		{CanonicalKey: "Artist/Album/track.flac", RelativePath: "Artist/Album/track.flac", NodeKind: "file"},
	})
	if err == nil {
		t.Fatal("expected missing manifest directory to be rejected")
	}
	if _, statErr := os.Lstat(filepath.Join(rootPath, "Artist")); statErr != nil {
		t.Fatalf("approved ancestor was not retained: %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(rootPath, "Artist", "Album")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unapproved ancestor was created: %v", statErr)
	}
}
