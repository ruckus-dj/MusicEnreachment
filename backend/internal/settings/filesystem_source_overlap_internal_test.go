package settings

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type sourceOverlapFileInfo struct{ id string }

func (i sourceOverlapFileInfo) Name() string     { return i.id }
func (sourceOverlapFileInfo) Size() int64        { return 0 }
func (sourceOverlapFileInfo) Mode() os.FileMode  { return os.ModeDir | 0o755 }
func (sourceOverlapFileInfo) ModTime() time.Time { return time.Time{} }
func (sourceOverlapFileInfo) IsDir() bool        { return true }
func (i sourceOverlapFileInfo) Sys() any         { return i.id }

func withSourceOverlapFilesystem(t *testing.T, entries map[string]string, statError map[string]error) *int {
	t.Helper()
	previous := sourcePathFilesystem
	creates := 0
	restoreCreateHook := SetFilesystemCreateAttemptHook(func(string) { creates++ })
	sourcePathFilesystem.stat = func(path string) (os.FileInfo, error) {
		if err := statError[path]; err != nil {
			return nil, err
		}
		id, ok := entries[path]
		if !ok {
			return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrNotExist}
		}
		return sourceOverlapFileInfo{id: id}, nil
	}
	sourcePathFilesystem.evalSymlinks = func(path string) (string, error) {
		if err := statError[path]; err != nil {
			return "", err
		}
		if _, ok := entries[path]; ok {
			return path, nil
		}
		return "", &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrNotExist}
	}
	sourcePathFilesystem.sameFile = func(first, second os.FileInfo) bool {
		return first.(sourceOverlapFileInfo).id == second.(sourceOverlapFileInfo).id
	}
	t.Cleanup(func() {
		restoreCreateHook()
		sourcePathFilesystem = previous
	})
	return &creates
}

func TestSourcePathsOverlapMixedCaseAliasesContinueComponentComparison(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "fixture")
	source := filepath.Join(root, "Music", "Album")
	managed := filepath.Join(root, "music", "album", "output")
	withSourceOverlapFilesystem(t, map[string]string{
		root: "root", filepath.Join(root, "Music"): "music", filepath.Join(root, "music"): "music",
		filepath.Join(root, "Music", "Album"): "upper-album", filepath.Join(root, "music", "album"): "lower-album",
	}, nil)
	got, err := SourcePathsOverlap(source, managed)
	if err != nil || got {
		t.Fatalf("mixed case aliases overlap = %t, %v; want distinct", got, err)
	}
}

func TestSourcePathsOverlapExistingUnicodeAliases(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "fixture")
	nfc := filepath.Join(root, "caf\u00e9")
	nfd := filepath.Join(root, "cafe\u0301")
	withSourceOverlapFilesystem(t, map[string]string{root: "root", nfc: "cafe", nfd: "cafe"}, nil)
	got, err := SourcePathsOverlap(nfc, filepath.Join(nfd, "prospective"))
	if err != nil || !got {
		t.Fatalf("existing Unicode aliases overlap = %t, %v; want true", got, err)
	}
}

func TestSourcePathsOverlapProspectiveUnicodeAmbiguityFailsClosed(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "fixture")
	source := filepath.Join(root, "caf\u00e9")
	managed := filepath.Join(root, "cafe\u0301", "prospective")
	withSourceOverlapFilesystem(t, map[string]string{root: "root", filepath.Join(root, "caf\u00e9"): "nfc"}, nil)
	got, err := SourcePathsOverlap(source, managed)
	if err == nil || got {
		t.Fatalf("prospective Unicode ambiguity = %t, %v; want an error", got, err)
	}
}

func TestSourcePathsOverlapPermissionErrorsFailClosedWithoutCreateAttempts(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "fixture")
	source := filepath.Join(root, "source")
	managed := filepath.Join(root, "managed")
	denied := &fs.PathError{Op: "stat", Path: managed, Err: fs.ErrPermission}
	creates := withSourceOverlapFilesystem(t, map[string]string{root: "root", source: "source"}, map[string]error{managed: denied})
	got, err := SourcePathsOverlap(source, managed)
	if err == nil || got || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("permission failure = %t, %v; want fail-closed permission error", got, err)
	}
	if *creates != 0 {
		t.Fatalf("read-only overlap check attempted %d filesystem creates", *creates)
	}
}
