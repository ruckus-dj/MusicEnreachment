//go:build windows

package sourcefs

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsAdapterPinsDirectoryAndRejectsReparse(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "inside", "ok.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := newPlatformOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	pinned, err := root.OpenDir(ctx, "inside")
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	if err := os.Rename(filepath.Join(rootPath, "inside"), filepath.Join(rootPath, "moved")); err != nil {
		t.Fatal(err)
	}
	f, err := pinned.OpenRegular(ctx, "ok.txt")
	if err != nil {
		t.Fatalf("pinned parent did not remain usable after rename: %v", err)
	}
	_ = f.Close()
	link := filepath.Join(rootPath, "link")
	if err := os.Symlink(filepath.Join(rootPath, "moved"), link); err != nil {
		t.Skipf("symlink privilege unavailable: %v", err)
	}
	if _, err := root.OpenDir(ctx, "link"); !errors.Is(err, ErrLink) {
		t.Fatalf("OpenDir(link) error = %v, want ErrLink", err)
	}
	if err := os.Symlink(filepath.Join(rootPath, "moved", "ok.txt"), filepath.Join(rootPath, "file-link")); err != nil {
		t.Fatalf("create file symlink: %v", err)
	}
	if _, err := root.OpenRegular(ctx, "file-link"); !errors.Is(err, ErrLink) {
		t.Fatalf("OpenRegular(file-link) error = %v, want ErrLink", err)
	}
}

func TestWindowsAdapterReadDirCancelAndClose(t *testing.T) {
	path := t.TempDir()
	for _, name := range []string{"one", "two", "three"} {
		if err := os.WriteFile(filepath.Join(path, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := newPlatformOpener().OpenRoot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := dir.ReadDir(context.Background(), 2)
	if err != nil || len(entries) != 2 {
		t.Fatalf("ReadDir(2) = %d, %v", len(entries), err)
	}
	entries, err = dir.ReadDir(context.Background(), 20)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir remainder = %d, %v", len(entries), err)
	}
	if _, err = dir.ReadDir(context.Background(), 2); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadDir at end = %v, want EOF", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = dir.ReadDir(cancelled, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ReadDir = %v", err)
	}
	if err = dir.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = dir.ReadDir(context.Background(), 1); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("ReadDir after close = %v", err)
	}
}

func TestWindowsAdapterUsesRenameCompatibleSharing(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "file"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := newPlatformOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	file, err := dir.OpenRegular(ctx, "file")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.Rename(filepath.Join(rootPath, "file"), filepath.Join(rootPath, "renamed")); err != nil {
		t.Fatalf("rename while pinned handle is open: %v", err)
	}
	if _, err := file.Stat(ctx); err != nil {
		t.Fatalf("stat pinned file after rename: %v", err)
	}
}

func TestWindowsAdapterRejectsJunctionsAndClosesFailedOpens(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rootPath := filepath.Join(base, "root")
	external := filepath.Join(base, "external")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(external, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "outside.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(rootPath, "junction")
	makeWindowsJunction(t, junction, external)

	root, err := newPlatformOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	winRoot := root.(*windowsDirectory)
	var closedHandles []windows.Handle
	winRoot.close = func(handle windows.Handle) error {
		closedHandles = append(closedHandles, handle)
		return windows.CloseHandle(handle)
	}
	if _, err := root.OpenDir(ctx, "junction"); !errors.Is(err, ErrLink) {
		t.Fatalf("OpenDir(junction) error = %v, want ErrLink", err)
	}
	if len(closedHandles) != 1 {
		t.Fatalf("failed junction open closed %d handles, want 1", len(closedHandles))
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}

	// A junction in any ancestor component must be rejected, not traversed to
	// an external directory while resolving the root path.
	ancestor := filepath.Join(base, "ancestor")
	makeWindowsJunction(t, ancestor, external)
	if _, err := newPlatformOpener().OpenRoot(ctx, filepath.Join(ancestor, "child")); !errors.Is(err, ErrLink) {
		t.Fatalf("OpenRoot through external junction error = %v, want ErrLink", err)
	}
}

func makeWindowsJunction(t *testing.T, path, target string) {
	t.Helper()
	output, err := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput()
	if err != nil {
		t.Skipf("junction creation unavailable: %v: %s", err, output)
	}
}

func TestWindowsAdapterHonorsExplicitShareConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.txt")
	if err := os.WriteFile(path, []byte("locked"), 0o600); err != nil {
		t.Fatal(err)
	}
	widePath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := windows.CreateFile(widePath, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(lock)
	dir, err := newPlatformOpener().OpenRoot(context.Background(), filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if _, err := dir.OpenRegular(context.Background(), filepath.Base(path)); err == nil {
		t.Fatal("OpenRegular succeeded despite an incompatible exclusive share lock")
	}
}

func TestWindowsAdapterRejectsTypeMismatchesAndCloses(t *testing.T) {
	path := t.TempDir()
	if err := os.Mkdir(filepath.Join(path, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := newPlatformOpener().OpenRoot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dir.OpenDir(context.Background(), "file"); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("OpenDir(file) error = %v, want ErrNotDirectory", err)
	}
	if _, err := dir.OpenRegular(context.Background(), "directory"); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("OpenRegular(directory) error = %v, want ErrNotRegular", err)
	}
	if err := dir.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dir.Close(); err != nil {
		t.Fatalf("second Close() = %v, want nil", err)
	}
}

func TestWindowsAdapterClosesHandleWhenContextCancelsDuringOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var closed []windows.Handle
	dir := &windowsDirectory{
		handle: 1,
		open: func(windows.Handle, string, bool) (windows.Handle, error) {
			cancel()
			return 42, nil
		},
		close: func(handle windows.Handle) error {
			closed = append(closed, handle)
			return nil
		},
	}
	if _, err := dir.OpenDir(ctx, "child"); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenDir error = %v, want context.Canceled", err)
	}
	if len(closed) != 1 || closed[0] != 42 {
		t.Fatalf("closed handles = %v, want [42]", closed)
	}
}

func TestWindowsAdapterContextCancellationWinsOverFailedOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var closed []windows.Handle
	dir := &windowsDirectory{
		handle: 1,
		open: func(windows.Handle, string, bool) (windows.Handle, error) {
			cancel()
			return 42, errors.New("native open failed")
		},
		close: func(handle windows.Handle) error {
			closed = append(closed, handle)
			return nil
		},
	}
	if _, err := dir.OpenDir(ctx, "child"); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenDir error = %v, want context.Canceled", err)
	}
	if len(closed) != 1 || closed[0] != 42 {
		t.Fatalf("closed handles = %v, want [42]", closed)
	}
}

func TestWindowsAdapterReadDirBoundsBatchAfterPendingEntries(t *testing.T) {
	dir := &windowsDirectory{
		handle:  1,
		first:   true,
		pending: []Entry{{Name: "pending"}},
		readDir: func(_ windows.Handle, _ uint32, buf []byte) error {
			copy(buf, windowsDirectoryRecords("one", "two", "three"))
			return nil
		},
	}
	entries, err := dir.ReadDir(context.Background(), 3)
	if err != nil || len(entries) != 3 {
		t.Fatalf("ReadDir(3) = %v, %v", entries, err)
	}
	if entries[0].Name != "pending" || entries[1].Name != "one" || entries[2].Name != "two" {
		t.Fatalf("ReadDir(3) entries = %v", entries)
	}
	if len(dir.pending) != 1 || dir.pending[0].Name != "three" {
		t.Fatalf("pending entries = %v, want [three]", dir.pending)
	}
}

func TestWindowsAdapterReadDirCancellationWinsOverNativeErrorWithPendingEntries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := 0
	dir := &windowsDirectory{
		handle:  1,
		pending: []Entry{{Name: "pending"}},
		readDir: func(_ windows.Handle, _ uint32, _ []byte) error {
			reads++
			cancel()
			return errors.New("native read failed")
		},
	}
	entries, err := dir.ReadDir(ctx, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadDir(0) error = %v, want context.Canceled", err)
	}
	if entries != nil {
		t.Fatalf("ReadDir(0) entries = %v, want nil on cancellation", entries)
	}
	if reads != 1 {
		t.Fatalf("native reads = %d, want 1", reads)
	}
}

func windowsDirectoryRecords(names ...string) []byte {
	var records []byte
	for i, name := range names {
		nameBytes := utf16.Encode([]rune(name))
		recordSize := 104 + len(nameBytes)*2
		if i < len(names)-1 {
			recordSize = (recordSize + 7) &^ 7
		}
		record := make([]byte, recordSize)
		if i < len(names)-1 {
			*(*uint32)(unsafe.Pointer(&record[0])) = uint32(recordSize)
		}
		*(*uint32)(unsafe.Pointer(&record[60])) = uint32(len(nameBytes) * 2)
		for index, unit := range nameBytes {
			*(*uint16)(unsafe.Pointer(&record[104+index*2])) = unit
		}
		records = append(records, record...)
	}
	return records
}

func TestWindowsAdapterRejectsUnsafeComponents(t *testing.T) {
	for _, name := range []string{`C:relative`, `\\?\C:\x`, `file:stream`, `CON.txt`, `COM¹.log`, `name.`, `name `, `a\b`, `..`} {
		if !invalidWindowsName(name) && name != `C:relative` && name != `\\?\C:\x` {
			t.Errorf("invalidWindowsName(%q) = false", name)
		}
	}
	if _, err := newPlatformOpener().OpenRoot(context.Background(), `\\server\share`); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("UNC root error = %v", err)
	}
}
