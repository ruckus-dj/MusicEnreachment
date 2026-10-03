package service

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
)

type sourceWalkFakeDirectory struct {
	entries []sourcefs.Entry
	readErr error
	read    func(context.Context) ([]sourcefs.Entry, error)
	dirs    map[string]*sourceWalkFakeDirectory
	dirErr  map[string]error
	files   map[string]*sourceWalkFakeFile
	fileErr map[string]error
	info    fs.FileInfo
	closed  int
}

func (directory *sourceWalkFakeDirectory) ReadDir(ctx context.Context, _ int) ([]sourcefs.Entry, error) {
	if directory.read != nil {
		return directory.read(ctx)
	}
	entries := directory.entries
	directory.entries = nil
	return entries, directory.readErr
}

func (directory *sourceWalkFakeDirectory) OpenDir(_ context.Context, name string) (sourcefs.Directory, error) {
	if err := directory.dirErr[name]; err != nil {
		return nil, err
	}
	child := directory.dirs[name]
	if child == nil {
		return nil, sourcefs.ErrNotDirectory
	}
	return child, nil
}

func (directory *sourceWalkFakeDirectory) OpenRegular(_ context.Context, name string) (sourcefs.RegularFile, error) {
	if err := directory.fileErr[name]; err != nil {
		return nil, err
	}
	file := directory.files[name]
	if file == nil {
		return nil, sourcefs.ErrNotRegular
	}
	return file, nil
}

func (directory *sourceWalkFakeDirectory) Stat(context.Context) (fs.FileInfo, error) {
	return directory.info, nil
}

func (directory *sourceWalkFakeDirectory) Close() error {
	directory.closed++
	return nil
}

type sourceWalkFakeFile struct {
	info   fs.FileInfo
	closed int
}

func (file *sourceWalkFakeFile) Stat(context.Context) (fs.FileInfo, error)     { return file.info, nil }
func (*sourceWalkFakeFile) Borrow(context.Context, func(*os.File) error) error { return nil }
func (file *sourceWalkFakeFile) Close() error {
	file.closed++
	return nil
}

func TestWalkSourceTreeClassifiesUnknownEntriesSafely(t *testing.T) {
	fileInfo := sourceWalkTestInfo(t, "track.flac", false)
	dirInfo := sourceWalkTestInfo(t, "directory", true)
	regular := &sourceWalkFakeFile{info: fileInfo}
	child := &sourceWalkFakeDirectory{info: dirInfo}
	root := &sourceWalkFakeDirectory{
		entries: []sourcefs.Entry{
			{Name: "track.flac", Kind: sourcefs.KindUnknown},
			{Name: "album", Kind: sourcefs.KindUnknown},
			{Name: "linked.flac", Kind: sourcefs.KindUnknown},
			{Name: "pipe.flac", Kind: sourcefs.KindUnknown},
		},
		dirs: map[string]*sourceWalkFakeDirectory{"album": child},
		dirErr: map[string]error{
			"track.flac":  sourcefs.ErrNotDirectory,
			"linked.flac": sourcefs.ErrLink,
			"pipe.flac":   sourcefs.ErrNotDirectory,
		},
		files:   map[string]*sourceWalkFakeFile{"track.flac": regular},
		fileErr: map[string]error{"pipe.flac": sourcefs.ErrNotRegular},
	}
	var paths []string
	err := walkSourceRoot(context.Background(), root, func(entry SourceWalkEntry, _ sourcefs.RegularFile) error {
		paths = append(paths, entry.RelativePath)
		return nil
	})
	if err != nil {
		t.Fatalf("walk unknown entries: %v", err)
	}
	if len(paths) != 1 || paths[0] != "track.flac" {
		t.Fatalf("walked %v, want only unknown regular track.flac", paths)
	}
	if regular.closed != 1 || child.closed != 1 {
		t.Errorf("file closes = %d, directory closes = %d, want one close each", regular.closed, child.closed)
	}
}

func TestWalkSourceTreeKnownDirectoryLinkRaceFailsAndCloses(t *testing.T) {
	root := &sourceWalkFakeDirectory{
		entries: []sourcefs.Entry{{Name: "album", Kind: sourcefs.KindDir}},
		dirErr:  map[string]error{"album": sourcefs.ErrLink},
	}
	err := walkSourceRoot(context.Background(), root, func(SourceWalkEntry, sourcefs.RegularFile) error { return nil })
	if !errors.Is(err, sourcefs.ErrLink) {
		t.Fatalf("walk error = %v, want known-directory replacement link failure", err)
	}
}

func TestWalkSourceTreeClosesHandlesOnVisitorErrorAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name   string
		cancel bool
	}{
		{name: "visitor error"},
		{name: "visitor cancellation", cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := sourceWalkTestInfo(t, "track.flac", false)
			file := &sourceWalkFakeFile{info: info}
			root := &sourceWalkFakeDirectory{
				entries: []sourcefs.Entry{{Name: "track.flac", Kind: sourcefs.KindRegular}},
				files:   map[string]*sourceWalkFakeFile{"track.flac": file},
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wantErr := errors.New("visitor failed")
			err := walkSourceRoot(ctx, root, func(SourceWalkEntry, sourcefs.RegularFile) error {
				if test.cancel {
					cancel()
					return nil
				}
				return wantErr
			})
			if test.cancel && !errors.Is(err, context.Canceled) {
				t.Errorf("walk error = %v, want cancellation", err)
			}
			if !test.cancel && !errors.Is(err, wantErr) {
				t.Errorf("walk error = %v, want visitor error", err)
			}
			if file.closed != 1 {
				t.Errorf("regular file close count = %d, want one", file.closed)
			}
		})
	}
}

func TestWalkSourceTreeCancellationWinsOverEmptyEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	root := &sourceWalkFakeDirectory{read: func(context.Context) ([]sourcefs.Entry, error) {
		cancel()
		return nil, io.EOF
	}}
	err := walkSourceRoot(ctx, root, func(SourceWalkEntry, sourcefs.RegularFile) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("walk error = %v, want cancellation rather than empty EOF success", err)
	}
}

func sourceWalkTestInfo(t *testing.T, name string, directory bool) fs.FileInfo {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return sourceWalkFakeInfo{FileInfo: info, directory: directory}
}

type sourceWalkFakeInfo struct {
	fs.FileInfo
	directory bool
}

func (info sourceWalkFakeInfo) IsDir() bool { return info.directory }
func (info sourceWalkFakeInfo) Mode() fs.FileMode {
	if info.directory {
		return fs.ModeDir | 0o700
	}
	return 0o600
}
