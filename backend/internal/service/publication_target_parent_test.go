package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// noSyncPublicationOutputDirectory deliberately hides the optional native
// directory-sync capability. Opening the approved target parent must not depend
// on that capability.
type noSyncPublicationOutputDirectory struct {
	sourcefs.OutputDirectory
	traversal sourcefs.OutputDirectoryTraversal
	children  []*noSyncPublicationOutputDirectory
	closed    bool
}

func newNoSyncPublicationOutputDirectory(directory sourcefs.OutputDirectory) *noSyncPublicationOutputDirectory {
	traversal, _ := directory.(sourcefs.OutputDirectoryTraversal)
	return &noSyncPublicationOutputDirectory{OutputDirectory: directory, traversal: traversal}
}

func (d *noSyncPublicationOutputDirectory) OpenOrCreateDir(ctx context.Context, name string) (sourcefs.OutputDirectory, error) {
	child, err := d.OutputDirectory.OpenOrCreateDir(ctx, name)
	if err != nil {
		return nil, err
	}
	return d.wrap(child), nil
}

func (d *noSyncPublicationOutputDirectory) CreateDirExclusive(ctx context.Context, name string) (sourcefs.OutputDirectory, error) {
	child, err := d.OutputDirectory.CreateDirExclusive(ctx, name)
	if err != nil {
		return nil, err
	}
	return d.wrap(child), nil
}

func (d *noSyncPublicationOutputDirectory) OpenExistingDir(ctx context.Context, name string) (sourcefs.OutputDirectory, error) {
	if d.traversal == nil {
		return nil, sourcefs.ErrUnsupported
	}
	child, err := d.traversal.OpenExistingDir(ctx, name)
	if err != nil {
		return nil, err
	}
	return d.wrap(child), nil
}

func (d *noSyncPublicationOutputDirectory) wrap(child sourcefs.OutputDirectory) sourcefs.OutputDirectory {
	wrapped := newNoSyncPublicationOutputDirectory(child)
	d.children = append(d.children, wrapped)
	return wrapped
}

func (d *noSyncPublicationOutputDirectory) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	return d.OutputDirectory.Close()
}

func TestOpenPublicationTargetParentWorksWithoutDirectorySync(t *testing.T) {
	ctx := context.Background()
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nativeRoot, err := sourcefs.NewOutputOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	root := newNoSyncPublicationOutputDirectory(nativeRoot)
	t.Cleanup(func() { _ = root.Close() })
	if _, ok := any(root).(sourcefs.OutputDirectorySync); ok {
		t.Fatal("test output directory unexpectedly exposes directory sync")
	}
	if _, ok := any(root).(sourcefs.OutputDirectoryTraversal); !ok {
		t.Fatal("test output directory does not expose traversal")
	}

	nodes := []persistence.PublicationNamespaceNode{
		{CanonicalKey: "Artist", RelativePath: "Artist", NodeKind: "dir", DirectoryScope: "artist"},
		{CanonicalKey: "Artist/Album", RelativePath: "Artist/Album", NodeKind: "dir", DirectoryScope: "album"},
		{CanonicalKey: "Artist/Album/track.flac", RelativePath: "Artist/Album/track.flac", NodeKind: "file"},
	}
	parent, err := openPublicationTargetParent(ctx, root, "Artist/Album/track.flac", nodes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })

	if _, err := root.Stat(ctx); err != nil {
		t.Fatalf("root was closed while the target parent is still open: %v", err)
	}
	if len(root.children) != 1 {
		t.Fatalf("opened intermediate directories = %d, want 1", len(root.children))
	}
	intermediate := root.children[0]
	if !intermediate.closed {
		t.Fatal("intermediate approved directory was not closed after descending")
	}
	if _, err := intermediate.Stat(ctx); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("intermediate directory stat error = %v, want os.ErrClosed", err)
	}

	file, err := parent.CreateExclusive(ctx, "track.flac")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(ctx, []byte("audio")); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(ctx); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(rootPath, "Artist", "Album", "track.flac"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "audio" {
		t.Fatalf("track contents = %q, want %q", body, "audio")
	}
	for _, directory := range []string{"Artist", filepath.Join("Artist", "Album")} {
		info, err := os.Lstat(filepath.Join(rootPath, directory))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("expected ordinary created directory %q, info=%v err=%v", directory, info, err)
		}
	}

	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.Stat(ctx); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("target parent stat error = %v, want os.ErrClosed", err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Stat(ctx); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("root stat error = %v, want os.ErrClosed", err)
	}
}
