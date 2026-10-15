package service

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
)

type sourceWalkFakeDirectory struct {
	entries []sourcefs.Entry
	readErr error
	dirs    map[string]*sourceWalkFakeDirectory
	dirErr  map[string]error
	files   map[string]*sourceWalkFakeFile
	fileErr map[string]error
	info    fs.FileInfo
	statErr error
	closed  int
}

func (directory *sourceWalkFakeDirectory) ReadDir(context.Context, int) ([]sourcefs.Entry, error) {
	entries := directory.entries
	directory.entries = nil
	return entries, directory.readErr
}

func (directory *sourceWalkFakeDirectory) OpenDir(_ context.Context, name string) (sourcefs.Directory, error) {
	if err := directory.dirErr[name]; err != nil {
		return nil, err
	}
	if child := directory.dirs[name]; child != nil {
		return child, nil
	}
	return nil, sourcefs.ErrNotDirectory
}

func (directory *sourceWalkFakeDirectory) OpenRegular(_ context.Context, name string) (sourcefs.RegularFile, error) {
	if err := directory.fileErr[name]; err != nil {
		return nil, err
	}
	if file := directory.files[name]; file != nil {
		return file, nil
	}
	return nil, sourcefs.ErrNotRegular
}

func (directory *sourceWalkFakeDirectory) Stat(context.Context) (fs.FileInfo, error) {
	return directory.info, directory.statErr
}

func (directory *sourceWalkFakeDirectory) Close() error {
	directory.closed++
	return nil
}

type sourceWalkFakeFile struct {
	info    fs.FileInfo
	statErr error
	closed  int
}

func (file *sourceWalkFakeFile) Stat(context.Context) (fs.FileInfo, error) {
	return file.info, file.statErr
}
func (*sourceWalkFakeFile) Borrow(context.Context, func(*os.File) error) error { return nil }
func (file *sourceWalkFakeFile) Close() error {
	file.closed++
	return nil
}

// sourceEnumerationModeFileInfo overrides only the reported mode so tests can
// model non-regular entries whose other stat results stay realistic.
type sourceEnumerationModeFileInfo struct {
	fs.FileInfo
	mode fs.FileMode
}

func (info sourceEnumerationModeFileInfo) Mode() fs.FileMode { return info.mode }

type sourceEnumerationTestOpener struct{ root *sourceWalkFakeDirectory }

func (opener sourceEnumerationTestOpener) OpenRoot(context.Context, string) (sourcefs.Directory, error) {
	return opener.root, nil
}

func TestEnumerateSourceTreeCollectsRootScope(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	scopes, err := EnumerateSourceTree(context.Background(), missing, func(SourceWalkEntry) error {
		t.Fatal("visitor called for a root that could not be opened")
		return nil
	})
	if err != nil {
		t.Fatalf("enumerate missing root: %v", err)
	}
	if len(scopes) != 1 || scopes[0] != (SourceEnumerationScope{Kind: "root"}) {
		t.Fatalf("unreadable scopes = %#v, want root scope", scopes)
	}
}

func TestEnumerateSourceTreeClassifiesUnreadableRootReadAsRootScope(t *testing.T) {
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	root := &sourceWalkFakeDirectory{readErr: os.ErrPermission, info: rootInfo}
	scopes, err := enumerateSourceTree(context.Background(), sourceEnumerationTestOpener{root: root}, rootPath, func(SourceWalkEntry) error {
		t.Fatal("visitor called after root directory read failed")
		return nil
	})
	if err != nil {
		t.Fatalf("enumerate unreadable root: %v", err)
	}
	if len(scopes) != 1 || scopes[0] != (SourceEnumerationScope{Kind: "root"}) {
		t.Fatalf("unreadable scopes = %#v, want the root scope", scopes)
	}
	if root.closed != 1 {
		t.Fatalf("root close count = %d, want 1", root.closed)
	}
}

func TestEnumerateSourceTreeAbortsOnVisitorErrorAndCancellation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "track.flac"), []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := errors.New("visitor failed")
	if _, err := EnumerateSourceTree(context.Background(), root, func(SourceWalkEntry) error { return want }); !errors.Is(err, want) {
		t.Fatalf("visitor error = %v, want %v", err, want)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = EnumerateSourceTree(ctx, root, func(SourceWalkEntry) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled enumeration error = %v, want context cancellation", err)
	}
}

func TestEnumerateSourceTreeClosesOpenedHandles(t *testing.T) {
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(rootPath, "track.flac")
	if err := os.WriteFile(filePath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	file := &sourceWalkFakeFile{info: fileInfo}
	root := &sourceWalkFakeDirectory{
		entries: []sourcefs.Entry{{Name: "track.flac", Kind: sourcefs.KindRegular}},
		files:   map[string]*sourceWalkFakeFile{"track.flac": file}, info: rootInfo,
	}
	if _, err := enumerateSourceTree(context.Background(), sourceEnumerationTestOpener{root: root}, rootPath, func(SourceWalkEntry) error { return nil }); err != nil {
		t.Fatalf("enumerate fake tree: %v", err)
	}
	if root.closed != 1 || file.closed != 1 {
		t.Fatalf("root/file close counts = %d/%d, want 1/1", root.closed, file.closed)
	}
}

func TestEnumerateSourceTreeClosesHandlesAfterVisitorError(t *testing.T) {
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(rootPath, "track.flac")
	if err := os.WriteFile(filePath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	file := &sourceWalkFakeFile{info: fileInfo}
	root := &sourceWalkFakeDirectory{
		entries: []sourcefs.Entry{{Name: "track.flac", Kind: sourcefs.KindRegular}},
		files:   map[string]*sourceWalkFakeFile{"track.flac": file}, info: rootInfo,
	}
	wantErr := errors.New("visitor failed")
	if _, err := enumerateSourceTree(context.Background(), sourceEnumerationTestOpener{root: root}, rootPath, func(SourceWalkEntry) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("enumeration error = %v, want %v", err, wantErr)
	}
	if root.closed != 1 || file.closed != 1 {
		t.Fatalf("root/file close counts after error = %d/%d, want 1/1", root.closed, file.closed)
	}
}

func TestEnumerateSourceTreeCollectsSubtreeAndFileScopes(t *testing.T) {
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	denied := errors.New("permission denied")
	root := &sourceWalkFakeDirectory{
		entries: []sourcefs.Entry{
			{Name: "locked", Kind: sourcefs.KindDir},
			{Name: "unreadable.flac", Kind: sourcefs.KindRegular},
		},
		dirErr:  map[string]error{"locked": denied},
		fileErr: map[string]error{"unreadable.flac": denied},
		info:    rootInfo,
	}
	var visited []string
	scopes, err := enumerateSourceTree(context.Background(), sourceEnumerationTestOpener{root: root}, rootPath, func(entry SourceWalkEntry) error {
		visited = append(visited, entry.RelativePath)
		return nil
	})
	if err != nil {
		t.Fatalf("enumerate fake source tree: %v", err)
	}
	if len(visited) != 0 {
		t.Fatalf("visited files = %v, want none", visited)
	}
	want := []SourceEnumerationScope{{RelativePath: "locked", Kind: "subtree"}, {RelativePath: "unreadable.flac", Kind: "file"}}
	if len(scopes) != len(want) {
		t.Fatalf("unreadable scopes = %#v, want %#v", scopes, want)
	}
	for i := range want {
		if scopes[i] != want[i] {
			t.Fatalf("unreadable scopes = %#v, want %#v", scopes, want)
		}
	}
}

func TestEnumerateSourceTreeClassifiesUnknownEntriesWithoutFollowingLinks(t *testing.T) {
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(rootPath, "track.flac")
	if err := os.WriteFile(filePath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	regular := &sourceWalkFakeFile{info: fileInfo}
	child := &sourceWalkFakeDirectory{info: rootInfo}
	root := &sourceWalkFakeDirectory{
		entries: []sourcefs.Entry{
			{Name: "track.flac", Kind: sourcefs.KindUnknown},
			{Name: "album", Kind: sourcefs.KindUnknown},
			{Name: "locked", Kind: sourcefs.KindUnknown},
			{Name: "linked.flac", Kind: sourcefs.KindUnknown},
		},
		dirs: map[string]*sourceWalkFakeDirectory{"album": child},
		dirErr: map[string]error{
			"track.flac":  sourcefs.ErrNotDirectory,
			"locked":      fs.ErrPermission,
			"linked.flac": sourcefs.ErrLink,
		},
		files: map[string]*sourceWalkFakeFile{"track.flac": regular},
		info:  rootInfo,
	}
	var visited []string
	scopes, err := enumerateSourceTree(context.Background(), sourceEnumerationTestOpener{root: root}, rootPath, func(entry SourceWalkEntry) error {
		visited = append(visited, entry.RelativePath)
		return nil
	})
	if err != nil {
		t.Fatalf("enumerate unknown entries: %v", err)
	}
	if len(visited) != 1 || visited[0] != "track.flac" {
		t.Fatalf("visited entries = %v, want only track.flac", visited)
	}
	if len(scopes) != 1 || scopes[0] != (SourceEnumerationScope{RelativePath: "locked", Kind: "subtree"}) {
		t.Fatalf("unreadable scopes = %#v, want locked subtree", scopes)
	}
	if regular.closed != 1 || child.closed != 1 || root.closed != 1 {
		t.Fatalf("file/child/root close counts = %d/%d/%d, want 1/1/1", regular.closed, child.closed, root.closed)
	}
}

func TestEnumerateSourceTreeClassifiesUnknownEntryStatOutcomes(t *testing.T) {
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	trackPath := filepath.Join(rootPath, "track.flac")
	if err := os.WriteFile(trackPath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	trackInfo, err := os.Stat(trackPath)
	if err != nil {
		t.Fatal(err)
	}
	regular := &sourceWalkFakeFile{info: trackInfo}
	special := &sourceWalkFakeFile{info: sourceEnumerationModeFileInfo{FileInfo: trackInfo, mode: fs.ModeNamedPipe | 0o600}}
	statFailedFile := &sourceWalkFakeFile{statErr: errors.New("file stat failed")}
	child := &sourceWalkFakeDirectory{info: rootInfo}
	statFailedDirectory := &sourceWalkFakeDirectory{statErr: errors.New("directory stat failed")}
	root := &sourceWalkFakeDirectory{
		entries: []sourcefs.Entry{
			{Name: "track.flac", Kind: sourcefs.KindUnknown},
			{Name: "album", Kind: sourcefs.KindUnknown},
			{Name: "pipe.flac", Kind: sourcefs.KindUnknown},
			{Name: "stat-failed.flac", Kind: sourcefs.KindUnknown},
			{Name: "unstatable-directory", Kind: sourcefs.KindUnknown},
		},
		dirs: map[string]*sourceWalkFakeDirectory{"album": child, "unstatable-directory": statFailedDirectory},
		dirErr: map[string]error{
			"track.flac":       sourcefs.ErrNotDirectory,
			"pipe.flac":        sourcefs.ErrNotDirectory,
			"stat-failed.flac": sourcefs.ErrNotDirectory,
		},
		files: map[string]*sourceWalkFakeFile{"track.flac": regular, "pipe.flac": special, "stat-failed.flac": statFailedFile},
		info:  rootInfo,
	}
	var visited []string
	scopes, err := enumerateSourceTree(context.Background(), sourceEnumerationTestOpener{root: root}, rootPath, func(entry SourceWalkEntry) error {
		visited = append(visited, entry.RelativePath)
		return nil
	})
	if err != nil {
		t.Fatalf("enumerate unknown entries: %v", err)
	}
	if len(visited) != 1 || visited[0] != "track.flac" {
		t.Fatalf("visited entries = %v, want only the regular file", visited)
	}
	wantScopes := []SourceEnumerationScope{
		{RelativePath: "stat-failed.flac", Kind: "file"},
		{RelativePath: "unstatable-directory", Kind: "subtree"},
	}
	if !slices.Equal(scopes, wantScopes) {
		t.Fatalf("unreadable scopes = %#v, want %#v", scopes, wantScopes)
	}
	if regular.closed != 1 || special.closed != 1 || statFailedFile.closed != 1 || child.closed != 1 || statFailedDirectory.closed != 1 || root.closed != 1 {
		t.Fatalf("unknown-entry handle close counts are not exactly one: file=%d special=%d failed-file=%d child=%d failed-dir=%d root=%d", regular.closed, special.closed, statFailedFile.closed, child.closed, statFailedDirectory.closed, root.closed)
	}
}

func TestValidEnumerationScopeRequiresContainedRelativePath(t *testing.T) {
	for _, scope := range []SourceEnumerationScope{
		{Kind: "root"},
		{Kind: "subtree", RelativePath: "album/disc"},
		{Kind: "file", RelativePath: "album/track.flac"},
	} {
		if !validEnumerationScope(scope) {
			t.Errorf("valid scope rejected: %+v", scope)
		}
	}
	for _, scope := range []SourceEnumerationScope{
		{Kind: "other"},
		{Kind: "root", RelativePath: "album"},
		{Kind: "subtree", RelativePath: "../outside"},
		{Kind: "file", RelativePath: "/outside.flac"},
		{Kind: "file", RelativePath: `album\\track.flac`},
	} {
		if validEnumerationScope(scope) {
			t.Errorf("invalid scope accepted: %+v", scope)
		}
	}
}
