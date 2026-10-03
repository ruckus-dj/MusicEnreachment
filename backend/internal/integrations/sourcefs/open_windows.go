//go:build windows

package sourcefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows opens are relative to pinned directory handles.
// All three share modes are enabled: this permits concurrent readers/writers
// and directory renames, but the open handle remains pinned to the same object.
// It is not a snapshot and does not protect mutable file contents.
type windowsOpener struct{}

func newPlatformOpener() Opener { return windowsOpener{} }

func (windowsOpener) OpenRoot(ctx context.Context, absolute string) (Directory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.HasPrefix(absolute, `\\?\`) || strings.HasPrefix(absolute, `\\.\`) {
		return nil, ErrInvalidPath
	}
	if strings.HasPrefix(absolute, `\\`) {
		return nil, ErrUnsupported
	}
	if !filepath.IsAbs(absolute) || len(absolute) < 3 || absolute[1] != ':' || (absolute[2] != '\\' && absolute[2] != '/') || absolute[0] < 'A' || (absolute[0] > 'Z' && absolute[0] < 'a') || absolute[0] > 'z' {
		return nil, ErrInvalidPath
	}
	if strings.Contains(absolute[3:], `\\`) || strings.Contains(absolute[3:], `//`) || strings.Contains(absolute[3:], `/\`) || strings.Contains(absolute[3:], `\/`) {
		return nil, ErrInvalidPath
	}
	if len(absolute) > 3 && (absolute[2] == '\\' || absolute[2] == '/') && (absolute[3] == '\\' || absolute[3] == '/') {
		return nil, ErrInvalidPath
	}
	parts := strings.FieldsFunc(absolute[3:], func(r rune) bool { return r == '/' || r == '\\' })
	for _, part := range parts {
		if invalidWindowsName(part) {
			return nil, ErrInvalidPath
		}
	}
	anchor := `\??\` + absolute[:2] + `\`
	h, err := openNative(windows.InvalidHandle, anchor, true)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	current := &windowsDirectory{handle: h, first: true}
	for _, part := range parts {
		next, openErr := current.OpenDir(ctx, part)
		closeErr := current.Close()
		if openErr == nil && closeErr != nil {
			_ = next.Close()
			return nil, closeErr
		}
		if openErr != nil {
			return nil, openErr
		}
		current = next.(*windowsDirectory)
	}
	return current, nil
}

func invalidWindowsName(s string) bool {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, `/<>:"|?*\\`) || strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") {
		return true
	}
	base := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
	for _, r := range base {
		if r == '¹' {
			base = strings.ReplaceAll(base, "¹", "1")
		}
		if r == '²' {
			base = strings.ReplaceAll(base, "²", "2")
		}
		if r == '³' {
			base = strings.ReplaceAll(base, "³", "3")
		}
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	return len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9'
}

func openNative(parent windows.Handle, name string, directory bool) (windows.Handle, error) {
	return openNativeWithClose(parent, name, directory, windows.CloseHandle)
}

func openNativeWithClose(parent windows.Handle, name string, directory bool, closeHandle func(windows.Handle) error) (windows.Handle, error) {
	var n *windows.NTUnicodeString
	var err error
	if parent == windows.InvalidHandle {
		n, err = windows.NewNTUnicodeString(name)
	} else {
		if invalidWindowsName(name) {
			return windows.InvalidHandle, ErrInvalidPath
		}
		n, err = windows.NewNTUnicodeString(name)
	}
	if err != nil {
		return windows.InvalidHandle, ErrInvalidPath
	}
	root := parent
	if parent == windows.InvalidHandle {
		root = 0
	}
	a := windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: root, ObjectName: n, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE}
	h := windows.InvalidHandle
	var iosb windows.IO_STATUS_BLOCK
	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_FOR_BACKUP_INTENT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	status := windows.NtCreateFile(&h, windows.FILE_READ_DATA|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, &a, &iosb, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN, options, 0, 0)
	runtime.KeepAlive(n)
	if status != nil {
		if ntstatus, ok := status.(windows.NTStatus); ok && (ntstatus == windows.STATUS_REPARSE_POINT_ENCOUNTERED || ntstatus == windows.STATUS_IO_REPARSE_TAG_NOT_HANDLED) {
			return windows.InvalidHandle, ErrLink
		}
		if ntstatus, ok := status.(windows.NTStatus); ok {
			switch ntstatus {
			case windows.STATUS_NOT_A_DIRECTORY:
				return windows.InvalidHandle, errors.Join(ErrNotDirectory, status)
			case windows.STATUS_FILE_IS_A_DIRECTORY:
				return windows.InvalidHandle, errors.Join(ErrNotRegular, status)
			case windows.STATUS_ACCESS_DENIED:
				return windows.InvalidHandle, errors.Join(fs.ErrPermission, status)
			case windows.STATUS_OBJECT_NAME_NOT_FOUND, windows.STATUS_OBJECT_PATH_NOT_FOUND, windows.STATUS_NO_SUCH_FILE:
				return windows.InvalidHandle, errors.Join(fs.ErrNotExist, status)
			}
		}
		return windows.InvalidHandle, status
	}
	if err = verifyHandle(h, directory); err != nil {
		_ = closeHandle(h)
		return windows.InvalidHandle, err
	}
	return h, nil
}

func verifyHandle(h windows.Handle, directory bool) error {
	var info [8]byte
	if err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, &info[0], uint32(len(info))); err != nil {
		return err
	}
	attrs := *(*uint32)(unsafe.Pointer(&info[0]))
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrLink
	}
	if attrs&windows.FILE_ATTRIBUTE_DEVICE != 0 {
		return ErrNotRegular
	}
	if (attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
		if directory {
			return ErrNotDirectory
		}
		return ErrNotRegular
	}
	return nil
}

type windowsDirectory struct {
	mu      sync.Mutex
	handle  windows.Handle
	closed  bool
	first   bool
	pending []Entry
	open    func(windows.Handle, string, bool) (windows.Handle, error)
	close   func(windows.Handle) error
	readDir func(windows.Handle, uint32, []byte) error
}

func (d *windowsDirectory) openChild(parent windows.Handle, name string, directory bool) (windows.Handle, error) {
	if d.open != nil {
		return d.open(parent, name, directory)
	}
	return openNativeWithClose(parent, name, directory, d.closeHandle)
}

func (d *windowsDirectory) closeHandle(handle windows.Handle) error {
	if d.close != nil {
		return d.close(handle)
	}
	return windows.CloseHandle(handle)
}

func (d *windowsDirectory) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.closeHandle(d.handle)
}
func (d *windowsDirectory) with(ctx context.Context, fn func(windows.Handle) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.closed {
		return os.ErrClosed
	}
	return fn(d.handle)
}
func (d *windowsDirectory) OpenDir(ctx context.Context, name string) (Directory, error) {
	if invalidWindowsName(name) {
		return nil, ErrInvalidPath
	}
	h := windows.InvalidHandle
	err := d.with(ctx, func(parent windows.Handle) error { var e error; h, e = d.openChild(parent, name, true); return e })
	if ctxErr := ctx.Err(); ctxErr != nil {
		if h != windows.InvalidHandle {
			_ = d.closeHandle(h)
		}
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}
	return &windowsDirectory{handle: h, first: true, open: d.open, close: d.close, readDir: d.readDir}, nil
}
func (d *windowsDirectory) OpenRegular(ctx context.Context, name string) (RegularFile, error) {
	if invalidWindowsName(name) {
		return nil, ErrInvalidPath
	}
	h := windows.InvalidHandle
	err := d.with(ctx, func(parent windows.Handle) error { var e error; h, e = d.openChild(parent, name, false); return e })
	if ctxErr := ctx.Err(); ctxErr != nil {
		if h != windows.InvalidHandle {
			_ = d.closeHandle(h)
		}
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), name)
	if f == nil {
		_ = d.closeHandle(h)
		return nil, ErrNotRegular
	}
	return newRegularFile(f), nil
}
func (d *windowsDirectory) Stat(ctx context.Context) (fs.FileInfo, error) {
	var info fs.FileInfo
	err := d.with(ctx, func(h windows.Handle) error {
		var duplicate windows.Handle
		if e := windows.DuplicateHandle(windows.CurrentProcess(), h, windows.CurrentProcess(), &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); e != nil {
			return e
		}
		f := os.NewFile(uintptr(duplicate), "")
		defer f.Close()
		var e error
		info, e = f.Stat()
		return e
	})
	return info, err
}

// Directory record layout for FILE_ID_BOTH_DIR_INFO; names start at byte 104.
func (d *windowsDirectory) ReadDir(ctx context.Context, n int) ([]Entry, error) {
	if n < 0 {
		n = 0
	}
	var entries []Entry
	err := d.with(ctx, func(h windows.Handle) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(d.pending) > 0 {
			count := len(d.pending)
			if n > 0 && count > n-len(entries) {
				count = n - len(entries)
			}
			entries = append(entries, d.pending[:count]...)
			d.pending = d.pending[count:]
			if n > 0 && len(entries) >= n {
				return ctx.Err()
			}
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			buf := make([]byte, 64*1024)
			class := uint32(windows.FileIdBothDirectoryInfo)
			if d.first {
				class = uint32(windows.FileIdBothDirectoryRestartInfo)
			}
			var e error
			if d.readDir != nil {
				e = d.readDir(h, class, buf)
			} else {
				e = windows.GetFileInformationByHandleEx(h, class, &buf[0], uint32(len(buf)))
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if e != nil {
				if errors.Is(e, windows.ERROR_NO_MORE_FILES) {
					break
				}
				return e
			}
			d.first = false
			records, parseErr := parseWindowsDirectoryEntries(buf)
			if parseErr != nil {
				return parseErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if n > 0 && len(records) > n-len(entries) {
				count := n - len(entries)
				entries = append(entries, records[:count]...)
				d.pending = append(d.pending, records[count:]...)
				return nil
			}
			entries = append(entries, records...)
			if n > 0 && len(entries) >= n {
				return nil
			}
		}
		return nil
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}
	if n > 0 && len(entries) == 0 {
		return nil, io.EOF
	}
	return entries, nil
}

func parseWindowsDirectoryEntries(buf []byte) ([]Entry, error) {
	var entries []Entry
	for off := 0; ; {
		if len(buf)-off < 104 {
			return nil, fmt.Errorf("sourcefs: malformed Windows directory record")
		}
		next := int(*(*uint32)(unsafe.Pointer(&buf[off])))
		if next != 0 && (next < 104 || next%8 != 0 || next > len(buf)-off) {
			return nil, fmt.Errorf("sourcefs: malformed Windows directory offset")
		}
		nameLen := int(*(*uint32)(unsafe.Pointer(&buf[off+60])))
		recordLength := len(buf) - off - 104
		if next != 0 {
			recordLength = next - 104
		}
		if nameLen == 0 || nameLen%2 != 0 || nameLen > recordLength {
			return nil, fmt.Errorf("sourcefs: malformed Windows directory name")
		}
		name := string(utf16.Decode((*[1 << 15]uint16)(unsafe.Pointer(&buf[off+104]))[:nameLen/2]))
		if name != "." && name != ".." {
			attrs := *(*uint32)(unsafe.Pointer(&buf[off+56]))
			kind := KindUnknown
			if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
				kind = KindExcluded
			} else if attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
				kind = KindDir
			} else {
				kind = KindRegular
			}
			entries = append(entries, Entry{Name: name, Kind: kind})
		}
		if next == 0 {
			return entries, nil
		}
		off += next
	}
}
