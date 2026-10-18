//go:build windows

package sourcefs

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Directory access mask. FILE_ADD_FILE and FILE_ADD_SUBDIRECTORY are the
// documented directory-access aliases of FILE_WRITE_DATA and FILE_APPEND_DATA;
// x/sys/windows exports only the latter names.
const windowsDirAccess = windows.FILE_LIST_DIRECTORY | windows.FILE_WRITE_DATA |
	windows.FILE_APPEND_DATA | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE

type windowsOutputOpener struct{}

func newPlatformOutputOpener() OutputOpener { return windowsOutputOpener{} }

func (windowsOutputOpener) OpenRoot(ctx context.Context, path string) (OutputDirectory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
		return nil, ErrInvalidPath
	}
	if strings.HasPrefix(path, `\\`) {
		return nil, ErrUnsupportedNetworkRoot
	}
	if !filepath.IsAbs(path) || len(path) < 3 || path[1] != ':' || (path[2] != '\\' && path[2] != '/') {
		return nil, ErrInvalidPath
	}
	parts := strings.FieldsFunc(path[3:], func(r rune) bool { return r == '/' || r == '\\' })
	for _, part := range parts {
		if invalidWindowsName(part) {
			return nil, ErrInvalidPath
		}
	}
	h, err := outputWindowsOpen(windows.InvalidHandle, `\??\`+path[:2]+`\`, windowsDirAccess, windows.FILE_OPEN, true)
	if err != nil {
		return nil, err
	}
	cur := &windowsOutputDir{handle: h, path: filepath.Clean(path), root: true}
	for _, part := range parts {
		next, e := cur.openDir(ctx, part)
		_ = cur.Close()
		if e != nil {
			return nil, e
		}
		cur = next
	}
	cur.path = filepath.Clean(path)
	cur.root = true
	return cur, nil
}

func outputWindowsOpen(parent windows.Handle, name string, access, disposition uint32, directory bool) (windows.Handle, error) {
	n, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return windows.InvalidHandle, ErrInvalidPath
	}
	root := parent
	if parent == windows.InvalidHandle {
		root = 0
	} else if invalidWindowsName(name) {
		return windows.InvalidHandle, ErrInvalidPath
	}
	a := windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: root, ObjectName: n, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE}
	var h windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	opts := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_FOR_BACKUP_INTENT)
	if directory {
		opts |= windows.FILE_DIRECTORY_FILE
	} else {
		opts |= windows.FILE_NON_DIRECTORY_FILE
	}
	status := windows.NtCreateFile(&h, access, &a, &iosb, nil, windows.FILE_ATTRIBUTE_NORMAL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, disposition, opts, 0, 0)
	runtime.KeepAlive(n)
	if status != nil {
		if nt, ok := status.(windows.NTStatus); ok {
			if nt == windows.STATUS_REPARSE_POINT_ENCOUNTERED || nt == windows.STATUS_IO_REPARSE_TAG_NOT_HANDLED {
				return windows.InvalidHandle, ErrLink
			}
			if nt == windows.STATUS_NOT_A_DIRECTORY {
				return windows.InvalidHandle, errors.Join(ErrNotDirectory, status)
			}
			if nt == windows.STATUS_FILE_IS_A_DIRECTORY {
				return windows.InvalidHandle, errors.Join(ErrNotRegular, status)
			}
		}
		return windows.InvalidHandle, status
	}
	if err := verifyHandle(h, directory); err != nil {
		_ = windows.CloseHandle(h)
		return windows.InvalidHandle, err
	}
	return h, nil
}

type windowsOutputDir struct {
	mu           sync.Mutex
	handle       windows.Handle
	closed, root bool
	path         string
}

func (d *windowsOutputDir) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return windows.CloseHandle(d.handle)
}
func (d *windowsOutputDir) openDir(ctx context.Context, name string) (*windowsOutputDir, error) {
	if invalidWindowsName(name) {
		return nil, ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, os.ErrClosed
	}
	h, err := outputWindowsOpen(d.handle, name, windowsDirAccess, windows.FILE_OPEN, true)
	d.mu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		if h != windows.InvalidHandle {
			_ = windows.CloseHandle(h)
		}
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}
	return &windowsOutputDir{handle: h}, nil
}
func (d *windowsOutputDir) OpenOrCreateDir(ctx context.Context, name string) (OutputDirectory, error) {
	if invalidWindowsName(name) {
		return nil, ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, os.ErrClosed
	}
	h, err := outputWindowsOpen(d.handle, name, windowsDirAccess, windows.FILE_CREATE, true)
	if nt, ok := err.(windows.NTStatus); ok && nt == windows.STATUS_OBJECT_NAME_COLLISION {
		h, err = outputWindowsOpen(d.handle, name, windowsDirAccess, windows.FILE_OPEN, true)
	}
	d.mu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		if h != windows.InvalidHandle {
			_ = windows.CloseHandle(h)
		}
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}
	return &windowsOutputDir{handle: h}, nil
}

func (d *windowsOutputDir) OpenExistingDir(ctx context.Context, name string) (OutputDirectory, error) {
	if invalidWindowsName(name) {
		return nil, ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, os.ErrClosed
	}
	h, err := outputWindowsOpen(d.handle, name, windowsDirAccess, windows.FILE_OPEN, true)
	d.mu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		if h != windows.InvalidHandle {
			_ = windows.CloseHandle(h)
		}
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}
	return &windowsOutputDir{handle: h}, nil
}

func (d *windowsOutputDir) CreateDirExclusive(ctx context.Context, name string) (OutputDirectory, error) {
	if invalidWindowsName(name) {
		return nil, ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, os.ErrClosed
	}
	h, err := outputWindowsOpen(d.handle, name, windowsDirAccess, windows.FILE_CREATE, true)
	d.mu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		if h != windows.InvalidHandle {
			_ = windows.CloseHandle(h)
		}
		return nil, ctxErr
	}
	if err != nil {
		if nt, ok := err.(windows.NTStatus); ok && nt == windows.STATUS_OBJECT_NAME_COLLISION {
			return nil, errors.Join(fs.ErrExist, err)
		}
		return nil, err
	}
	return &windowsOutputDir{handle: h}, nil
}

func (d *windowsOutputDir) Identity(ctx context.Context) (DirectoryIdentity, error) {
	if err := ctx.Err(); err != nil {
		return DirectoryIdentity{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return DirectoryIdentity{}, os.ErrClosed
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(d.handle, &info); err != nil {
		return DirectoryIdentity{}, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return DirectoryIdentity{}, ErrNotDirectory
	}
	fileID := uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
	return DirectoryIdentity{Platform: "windows", Volume: fmt.Sprintf("%08x", info.VolumeSerialNumber), FileID: fmt.Sprintf("%016x", fileID)}, nil
}

// SyncDirectory is unsupported until Windows provides an implementation with
// the required pinned-handle durability semantics. Never claim a fake flush.
func (d *windowsOutputDir) SyncDirectory(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return os.ErrClosed
	}
	return ErrUnsupported
}

func (d *windowsOutputDir) VerifyChild(ctx context.Context, name string, expected DirectoryIdentity) error {
	if invalidWindowsName(name) {
		return ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return os.ErrClosed
	}
	h, err := outputWindowsOpen(d.handle, name, windowsDirAccess, windows.FILE_OPEN, true)
	d.mu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		if h != windows.InvalidHandle {
			_ = windows.CloseHandle(h)
		}
		return ctxErr
	}
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	fileID := uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
	actual := DirectoryIdentity{Platform: "windows", Volume: fmt.Sprintf("%08x", info.VolumeSerialNumber), FileID: fmt.Sprintf("%016x", fileID)}
	if actual != expected {
		return errors.New("sourcefs: output namespace changed")
	}
	return nil
}

func (d *windowsOutputDir) CreateExclusive(ctx context.Context, name string) (OutputFile, error) {
	if invalidWindowsName(name) {
		return nil, ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, os.ErrClosed
	}
	h, err := outputWindowsOpen(d.handle, name, windows.GENERIC_WRITE|windows.GENERIC_READ|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_CREATE, false)
	d.mu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		if h != windows.InvalidHandle {
			_ = windows.CloseHandle(h)
		}
		return nil, ctxErr
	}
	if err != nil {
		if nt, ok := err.(windows.NTStatus); ok && nt == windows.STATUS_OBJECT_NAME_COLLISION {
			err = errors.Join(fs.ErrExist, err)
		}
		return nil, err
	}
	f := os.NewFile(uintptr(h), name)
	if f == nil {
		_ = windows.CloseHandle(h)
		return nil, ErrNotRegular
	}
	return &windowsOutputFile{file: f}, nil
}

func (d *windowsOutputDir) OpenExistingRegular(ctx context.Context, name string) (RegularFile, error) {
	if invalidWindowsName(name) {
		return nil, ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, os.ErrClosed
	}
	h, err := outputWindowsOpen(d.handle, name, windows.GENERIC_READ|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, false)
	d.mu.Unlock()
	if err != nil {
		if nt, ok := err.(windows.NTStatus); ok && (nt == windows.STATUS_OBJECT_NAME_NOT_FOUND || nt == windows.STATUS_OBJECT_PATH_NOT_FOUND) {
			return nil, errors.Join(os.ErrNotExist, err)
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	f := os.NewFile(uintptr(h), name)
	if f == nil {
		_ = windows.CloseHandle(h)
		return nil, ErrNotRegular
	}
	return newRegularFile(f), nil
}

func (d *windowsOutputDir) RenameNoReplace(ctx context.Context, sourceParent OutputDirectory, sourceName string, targetParent OutputDirectory, targetName string) error {
	if invalidWindowsName(sourceName) || invalidWindowsName(targetName) {
		return ErrInvalidPath
	}
	source, ok := sourceParent.(*windowsOutputDir)
	if !ok {
		return ErrUnsupported
	}
	target, ok := targetParent.(*windowsOutputDir)
	if !ok {
		return ErrUnsupported
	}
	if source == target && strings.EqualFold(sourceName, targetName) {
		return ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sourceHandle, err := duplicateOutputDirectoryHandle(source)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(sourceHandle) }()
	targetHandle, err := duplicateOutputDirectoryHandle(target)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(targetHandle) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	fileHandle, err := outputWindowsOpen(sourceHandle, sourceName, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, false)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(fileHandle) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := windows.UTF16FromString(targetName)
	if err != nil {
		return ErrInvalidPath
	}
	name = name[:len(name)-1]
	nameBytes := len(name) * 2
	rootOffset := (1 + int(unsafe.Sizeof(uintptr(0))) - 1) &^ (int(unsafe.Sizeof(uintptr(0))) - 1)
	fileNameOffset := rootOffset + int(unsafe.Sizeof(uintptr(0))) + 4
	buffer := make([]byte, fileNameOffset+nameBytes)
	buffer[0] = 0 // ReplaceIfExists = FALSE.
	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.LittleEndian.PutUint64(buffer[rootOffset:], uint64(targetHandle))
	} else {
		binary.LittleEndian.PutUint32(buffer[rootOffset:], uint32(targetHandle))
	}
	binary.LittleEndian.PutUint32(buffer[rootOffset+int(unsafe.Sizeof(uintptr(0))):], uint32(nameBytes))
	for i, unit := range name {
		binary.LittleEndian.PutUint16(buffer[fileNameOffset+i*2:], unit)
	}
	var iosb windows.IO_STATUS_BLOCK
	if err := windows.NtSetInformationFile(fileHandle, &iosb, &buffer[0], uint32(len(buffer)), 10); err != nil {
		return err
	}
	runtime.KeepAlive(buffer)
	return nil
}

// AtomicReplaceExisting validates both named regular files against snapshots
// obtained from open handles, then uses the native replace-existing rename
// operation. As on Unix, validation is not hostile-writer compare-and-swap.
func (d *windowsOutputDir) AtomicReplaceExisting(ctx context.Context, sourceParent OutputDirectory, sourceName string, expectedSource fs.FileInfo, targetName string, expectedTarget fs.FileInfo) error {
	if invalidWindowsName(sourceName) || invalidWindowsName(targetName) {
		return ErrInvalidPath
	}
	source, ok := sourceParent.(*windowsOutputDir)
	if !ok {
		return ErrUnsupported
	}
	if expectedSource == nil || !expectedSource.Mode().IsRegular() || expectedTarget == nil || !expectedTarget.Mode().IsRegular() {
		return ErrNotRegular
	}
	if source == d && strings.EqualFold(sourceName, targetName) {
		return ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sourceHandle, err := duplicateOutputDirectoryHandle(source)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(sourceHandle)
	targetHandle, err := duplicateOutputDirectoryHandle(d)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(targetHandle)
	check := func(parent windows.Handle, name string, expected fs.FileInfo, access uint32) (windows.Handle, *os.File, error) {
		h, openErr := outputWindowsOpen(parent, name, access|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, false)
		if openErr != nil {
			return windows.InvalidHandle, nil, openErr
		}
		file := os.NewFile(uintptr(h), name)
		actual, statErr := file.Stat()
		if statErr != nil || !actual.Mode().IsRegular() || !os.SameFile(expected, actual) {
			_ = file.Close()
			if statErr != nil {
				return windows.InvalidHandle, nil, statErr
			}
			if !actual.Mode().IsRegular() {
				return windows.InvalidHandle, nil, ErrNotRegular
			}
			return windows.InvalidHandle, nil, errors.New("sourcefs: output namespace changed")
		}
		return h, file, nil
	}
	fileHandle, sourceFile, err := check(sourceHandle, sourceName, expectedSource, windows.DELETE)
	if err != nil {
		return err
	}
	// os.NewFile owns fileHandle; do not close the raw handle separately.
	defer func() { _ = sourceFile.Close() }()
	_, targetOSFile, err := check(targetHandle, targetName, expectedTarget, 0)
	if err != nil {
		return err
	}
	_ = targetOSFile.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := windows.UTF16FromString(targetName)
	if err != nil {
		return ErrInvalidPath
	}
	name = name[:len(name)-1]
	nameBytes := len(name) * 2
	// FILE_RENAME_INFORMATION_EX begins with DWORD Flags, followed by an
	// aligned HANDLE RootDirectory and the filename length/name.
	rootOffset := (4 + int(unsafe.Sizeof(uintptr(0))) - 1) &^ (int(unsafe.Sizeof(uintptr(0))) - 1)
	fileNameOffset := rootOffset + int(unsafe.Sizeof(uintptr(0))) + 4
	buffer := make([]byte, fileNameOffset+nameBytes)
	flags := uint32(windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS)
	binary.LittleEndian.PutUint32(buffer, flags)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.LittleEndian.PutUint64(buffer[rootOffset:], uint64(targetHandle))
	} else {
		binary.LittleEndian.PutUint32(buffer[rootOffset:], uint32(targetHandle))
	}
	binary.LittleEndian.PutUint32(buffer[rootOffset+int(unsafe.Sizeof(uintptr(0))):], uint32(nameBytes))
	for i, unit := range name {
		binary.LittleEndian.PutUint16(buffer[fileNameOffset+i*2:], unit)
	}
	var iosb windows.IO_STATUS_BLOCK
	// FileRenameInformationEx is FILE_INFO_BY_HANDLE_CLASS 65. Do not fall
	// back to the legacy class: it cannot replace while the target is open.
	if err := windows.NtSetInformationFile(fileHandle, &iosb, &buffer[0], uint32(len(buffer)), 65); err != nil {
		return err
	}
	runtime.KeepAlive(buffer)
	return nil
}

func duplicateOutputDirectoryHandle(d *windowsOutputDir) (windows.Handle, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return windows.InvalidHandle, os.ErrClosed
	}
	process, err := windows.GetCurrentProcess()
	if err != nil {
		return windows.InvalidHandle, err
	}
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(process, d.handle, process, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return windows.InvalidHandle, err
	}
	return duplicate, nil
}

func (d *windowsOutputDir) Stat(ctx context.Context) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, os.ErrClosed
	}
	var dup windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), d.handle, windows.CurrentProcess(), &dup, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(dup), "")
	defer func() { _ = f.Close() }()
	return f.Stat()
}
func (d *windowsOutputDir) Verify(ctx context.Context) error {
	if !d.root {
		return ErrInvalidPath
	}
	other, e := newPlatformOutputOpener().OpenRoot(ctx, d.path)
	if e != nil {
		return e
	}
	defer func() { _ = other.Close() }()
	a, e := d.Stat(ctx)
	if e != nil {
		return e
	}
	b, e := other.Stat(ctx)
	if e != nil {
		return e
	}
	if !os.SameFile(a, b) {
		return errors.New("sourcefs: output namespace changed")
	}
	return nil
}

func (d *windowsOutputDir) RemoveOwnedRegular(ctx context.Context, name string, expectedParent DirectoryIdentity, expected fs.FileInfo) error {
	if invalidWindowsName(name) {
		return ErrInvalidPath
	}
	if expected == nil || !expected.Mode().IsRegular() {
		return ErrNotRegular
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return os.ErrClosed
	}
	parent := d.handle
	var parentInfo windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(parent, &parentInfo)
	if err == nil && windowsOutputIdentity(parentInfo) != expectedParent {
		err = errors.New("sourcefs: output namespace changed")
	}
	h := windows.InvalidHandle
	if err == nil {
		h, err = outputWindowsOpen(parent, name, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, false)
	}
	d.mu.Unlock()
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(h), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !os.SameFile(expected, info) {
		return errors.New("sourcefs: output namespace changed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return markWindowsDelete(h)
}

func (d *windowsOutputDir) RemoveEmptyChildDir(ctx context.Context, name string, expectedParent, expected DirectoryIdentity) error {
	if invalidWindowsName(name) {
		return ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return os.ErrClosed
	}
	var parentInfo windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(d.handle, &parentInfo)
	if err == nil && windowsOutputIdentity(parentInfo) != expectedParent {
		err = errors.New("sourcefs: output namespace changed")
	}
	h := windows.InvalidHandle
	if err == nil {
		h, err = outputWindowsOpen(d.handle, name, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, true)
	}
	d.mu.Unlock()
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	if windowsOutputIdentity(info) != expected {
		return errors.New("sourcefs: output namespace changed")
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrLink
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return markWindowsDelete(h)
}

func windowsOutputIdentity(info windows.ByHandleFileInformation) DirectoryIdentity {
	fileID := uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
	return DirectoryIdentity{Platform: "windows", Volume: fmt.Sprintf("%08x", info.VolumeSerialNumber), FileID: fmt.Sprintf("%016x", fileID)}
}

func markWindowsDelete(handle windows.Handle) error {
	delete := uint8(1)
	return windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, (*byte)(unsafe.Pointer(&delete)), uint32(unsafe.Sizeof(delete)))
}

type windowsOutputFile struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
}

func (f *windowsOutputFile) with(ctx context.Context, fn func(*os.File) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return os.ErrClosed
	}
	return fn(f.file)
}
func (f *windowsOutputFile) Write(ctx context.Context, b []byte) (int, error) {
	var n int
	e := f.with(ctx, func(file *os.File) error { var x error; n, x = file.Write(b); return x })
	return n, e
}
func (f *windowsOutputFile) Sync(ctx context.Context) error {
	return f.with(ctx, func(file *os.File) error { return file.Sync() })
}
func (f *windowsOutputFile) Stat(ctx context.Context) (fs.FileInfo, error) {
	var i fs.FileInfo
	e := f.with(ctx, func(file *os.File) error { var x error; i, x = file.Stat(); return x })
	return i, e
}
func (f *windowsOutputFile) BorrowRead(ctx context.Context, cb func(*os.File) error) error {
	if cb == nil {
		return ErrInvalidBorrow
	}
	return f.with(ctx, func(file *os.File) error {
		if _, e := file.Seek(0, 0); e != nil {
			return e
		}
		return cb(file)
	})
}
func (f *windowsOutputFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	return f.file.Close()
}
