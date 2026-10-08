//go:build windows

package sourcefs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

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
	h, err := outputWindowsOpen(windows.InvalidHandle, `\??\`+path[:2]+`\`, windows.FILE_LIST_DIRECTORY|windows.FILE_ADD_FILE|windows.FILE_ADD_SUBDIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, true)
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
	h, err := outputWindowsOpen(d.handle, name, windows.FILE_LIST_DIRECTORY|windows.FILE_ADD_FILE|windows.FILE_ADD_SUBDIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, true)
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
	h, err := outputWindowsOpen(d.handle, name, windows.FILE_LIST_DIRECTORY|windows.FILE_ADD_FILE|windows.FILE_ADD_SUBDIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_CREATE, true)
	if nt, ok := err.(windows.NTStatus); ok && nt == windows.STATUS_OBJECT_NAME_COLLISION {
		h, err = outputWindowsOpen(d.handle, name, windows.FILE_LIST_DIRECTORY|windows.FILE_ADD_FILE|windows.FILE_ADD_SUBDIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, true)
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
		return nil, err
	}
	f := os.NewFile(uintptr(h), name)
	if f == nil {
		_ = windows.CloseHandle(h)
		return nil, ErrNotRegular
	}
	return &windowsOutputFile{file: f}, nil
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
