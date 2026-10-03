package sourcefs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type directoryFixture struct {
	name       string
	openedDirs []string
	openedFile []string
	closed     int
	onOpenDir  func(string)
	next       *directoryFixture
	dirErr     error
	fileErr    error
	file       RegularFile
	onReadDir  func(int)
	onOpenFile func(string)
}

func (directory *directoryFixture) ReadDir(_ context.Context, n int) ([]Entry, error) {
	if directory.onReadDir != nil {
		directory.onReadDir(n)
	}
	return nil, io.EOF
}

func (directory *directoryFixture) OpenDir(_ context.Context, name string) (Directory, error) {
	directory.openedDirs = append(directory.openedDirs, name)
	if directory.onOpenDir != nil {
		directory.onOpenDir(name)
	}
	if directory.dirErr != nil {
		return nil, directory.dirErr
	}
	if directory.next != nil {
		return directory.next, nil
	}
	return &directoryFixture{name: name, file: directory.file, fileErr: directory.fileErr}, nil
}

func (directory *directoryFixture) OpenRegular(_ context.Context, name string) (RegularFile, error) {
	directory.openedFile = append(directory.openedFile, name)
	if directory.fileErr != nil {
		return nil, directory.fileErr
	}
	if directory.onOpenFile != nil {
		directory.onOpenFile(name)
	}
	return directory.file, nil
}

func (directory *directoryFixture) Stat(context.Context) (fs.FileInfo, error) { return nil, nil }
func (directory *directoryFixture) Close() error                              { directory.closed++; return nil }

func TestOpenRegularAtWalksHandlesAndClosesIntermediateDirectories(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "source")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("source bytes"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	root := &directoryFixture{next: &directoryFixture{file: newRegularFile(file)}}
	var barriers []string
	root.onOpenDir = func(name string) { barriers = append(barriers, "parent-before-child:"+name) }
	opened, err := OpenRegularAt(context.Background(), root, "disc/track.flac")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(root.openedDirs, []string{"disc"}) || !reflect.DeepEqual(root.next.openedFile, []string{"track.flac"}) {
		t.Fatalf("unexpected handle walk: dirs=%q files=%q", root.openedDirs, root.next.openedFile)
	}
	if !reflect.DeepEqual(barriers, []string{"parent-before-child:disc"}) {
		t.Fatalf("per-instance barrier did not run at parent/child boundary: %q", barriers)
	}
	if root.closed != 0 {
		t.Fatalf("caller-owned root was closed %d times", root.closed)
	}
	if root.next.closed != 1 {
		t.Fatalf("intermediate directory closed %d times, want once", root.next.closed)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFakeAdapterProvidesInstanceScopedEnumerationAndOpenBarriers(t *testing.T) {
	first := &directoryFixture{}
	second := &directoryFixture{}
	var events []string
	first.onReadDir = func(n int) { events = append(events, "first-enumeration-terminal") }
	second.onReadDir = func(n int) { events = append(events, "second-enumeration-terminal") }
	first.onOpenFile = func(name string) { events = append(events, "first-open-return:"+name) }
	second.onOpenFile = func(name string) { events = append(events, "second-open-return:"+name) }
	first.file = &directoryFixtureFile{}
	second.file = &directoryFixtureFile{}

	if _, err := first.ReadDir(context.Background(), 7); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal ReadDir error = %v, want io.EOF", err)
	}
	if _, err := second.ReadDir(context.Background(), 11); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal ReadDir error = %v, want io.EOF", err)
	}
	if _, err := first.OpenRegular(context.Background(), "one.flac"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.OpenRegular(context.Background(), "two.flac"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"first-enumeration-terminal", "second-enumeration-terminal",
		"first-open-return:one.flac", "second-open-return:two.flac",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("barrier events = %q, want %q", events, want)
	}
}

type directoryFixtureFile struct{}

func (*directoryFixtureFile) Stat(context.Context) (fs.FileInfo, error) { return nil, nil }
func (*directoryFixtureFile) Borrow(context.Context, func(*os.File) error) error {
	return nil
}
func (*directoryFixtureFile) Close() error { return nil }

func TestOpenRegularAtValidatesEntirePathBeforeAnyAdapterCall(t *testing.T) {
	root := &directoryFixture{}
	for _, path := range []string{"", "/absolute/file", "a//b", "a/./b", "a/../b", "a/"} {
		t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			if _, err := OpenRegularAt(context.Background(), root, path); !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("OpenRegularAt(%q) error = %v, want ErrInvalidPath", path, err)
			}
		})
	}
	if len(root.openedDirs)+len(root.openedFile) != 0 {
		t.Fatalf("invalid paths reached adapter: dirs=%q files=%q", root.openedDirs, root.openedFile)
	}
	if _, err := validateRelativePath(`folder\name/file`, false); err != nil {
		t.Fatalf("Unix grammar must treat backslash as a filename character: %v", err)
	}
}

func TestWindowsRelativePathGrammarFailsClosed(t *testing.T) {
	for _, path := range []string{`C:file`, `C:/file`, `/rooted`, `folder\file`, `folder/file:stream`, `CON.txt`, `Lpt9`, `folder./file`, `folder /file`, `//server/share`} {
		if _, err := validateRelativePath(path, true); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Windows path %q error = %v, want ErrInvalidPath", path, err)
		}
	}
	if _, err := validateRelativePath("folder/file.flac", true); err != nil {
		t.Fatalf("valid Windows relative path rejected: %v", err)
	}
}

func TestOpenRegularAtAcceptsNativeWindowsRelativePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows paths are only accepted by the Windows adapter")
	}
	root := &directoryFixture{next: &directoryFixture{file: &directoryFixtureFile{}}}
	if _, err := OpenRegularAt(context.Background(), root, filepath.Join("album", "track.flac")); err != nil {
		t.Fatalf("OpenRegularAt(native path) error = %v", err)
	}
	if !reflect.DeepEqual(root.openedDirs, []string{"album"}) || !reflect.DeepEqual(root.next.openedFile, []string{"track.flac"}) {
		t.Fatalf("native Windows path was not split into components: dirs=%q files=%q", root.openedDirs, root.next.openedFile)
	}
}

func TestOpenRegularAtPreservesUnixBackslashFilename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("backslash is a path separator on Windows")
	}
	root := &directoryFixture{next: &directoryFixture{file: &directoryFixtureFile{}}}
	if _, err := OpenRegularAt(context.Background(), root, `album\name/track.flac`); err != nil {
		t.Fatalf("OpenRegularAt(Unix backslash filename) error = %v", err)
	}
	if !reflect.DeepEqual(root.openedDirs, []string{`album\name`}) || !reflect.DeepEqual(root.next.openedFile, []string{"track.flac"}) {
		t.Fatalf("Unix backslash filename was normalized: dirs=%q files=%q", root.openedDirs, root.next.openedFile)
	}
}

func TestOpenRegularAtClosesIntermediateOnFailure(t *testing.T) {
	want := errors.New("child open failed")
	root := &directoryFixture{dirErr: want}
	if _, err := OpenRegularAt(context.Background(), root, "sub/file"); !errors.Is(err, want) {
		t.Fatalf("OpenRegularAt error = %v, want %v", err, want)
	}
	if root.closed != 0 {
		t.Fatalf("failed child open closed caller-owned root %d times", root.closed)
	}
	root = &directoryFixture{next: &directoryFixture{dirErr: want}}
	if _, err := OpenRegularAt(context.Background(), root, "one/two/file"); !errors.Is(err, want) {
		t.Fatalf("nested open error = %v, want %v", err, want)
	}
	if root.next.closed != 1 {
		t.Fatalf("intermediate directory closed %d times, want once", root.next.closed)
	}
}

func TestRegularFileBorrowRewindsSerializesAndClosesIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regular")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	underlying, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	file := newRegularFile(underlying)
	if err := file.Borrow(context.Background(), func(f *os.File) error {
		buffer := make([]byte, 2)
		_, err := f.Read(buffer)
		if err == nil && string(buffer) != "ab" {
			err = errors.New("first loan did not start at offset zero")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := file.Borrow(context.Background(), func(f *os.File) error {
		buffer := make([]byte, 3)
		_, err := f.Read(buffer)
		if err == nil && string(buffer) != "abc" {
			err = errors.New("second loan was not rewound")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("second close must be idempotent: %v", err)
	}
	if err := file.Borrow(context.Background(), func(*os.File) error { return nil }); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Borrow after Close error = %v, want os.ErrClosed", err)
	}
}

func TestRegularFileBorrowRejectsInvalidAndCancelledLoans(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regular")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	underlying, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	file := newRegularFile(underlying)
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	if err := file.Borrow(context.Background(), nil); !errors.Is(err, ErrInvalidBorrow) {
		t.Fatalf("nil callback error = %v, want ErrInvalidBorrow", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if err := file.Borrow(ctx, func(*os.File) error { called = true; return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Borrow error = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("callback ran for cancelled loan")
	}
	want := errors.New("callback failed")
	if err := file.Borrow(context.Background(), func(*os.File) error { return want }); !errors.Is(err, want) {
		t.Fatalf("callback error = %v, want %v", err, want)
	}
}

func TestUnsupportedOpenerFailsClosedWithoutPathnameAccess(t *testing.T) {
	ctx := context.Background()
	if _, err := (unsupportedOpener{}).OpenRoot(ctx, filepath.Join(t.TempDir(), "not-created")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("valid root error = %v, want ErrUnsupported", err)
	}
	if _, err := (unsupportedOpener{}).OpenRoot(ctx, "relative"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("relative root error = %v, want ErrInvalidPath", err)
	}
}
