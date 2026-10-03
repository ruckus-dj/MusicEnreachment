// Package sourcefs defines the handle-based, read-only contract for traversing a
// registered source tree.
//
// A successfully opened root pins a directory object, not a filesystem snapshot.
// The containing mount topology is trusted; renaming a pinned directory does not
// retarget its handle, but may change its visible pathname. File contents remain
// mutable while open, and context cancellation cannot forcibly interrupt an
// operating-system call already in progress.
//
// Adapters must open the root anchor and every child component without following
// links/reparse points. Terminal regular-file opens must not block on a FIFO
// before type validation. ReadDir must enumerate through the open directory
// handle, return no more than n entries when n > 0, and use io.EOF only when a
// bounded read returns no entries. Entry.Kind is advisory; callers must still
// open and verify an entry before relying on its type.
// Linux uses openat2 with RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS and deliberately
// has no weaker-kernel pathname fallback. Darwin uses single-component openat
// calls with O_NOFOLLOW from the pinned parent descriptor.
//
// Unix relative paths use '/' as the only separator; a backslash is an ordinary
// filename character. Windows adapters must reject rooted and drive-relative
// paths, user/device namespaces, alternate data streams, device names, and names
// ending in dots or spaces. UNC roots require an explicitly safe volume-anchor
// implementation; until one exists, an adapter must reject them as unsupported.
// Neither containment nor pinned handles provide immutable bytes or a filesystem
// transaction.
//
// Deterministic race tests should implement these interfaces with per-instance
// barriers: pause a parent handle immediately before a child open, pause an
// enumeration before its terminal result, or pause a terminal open before it
// returns. Do not use package-global hooks; separate adapter instances must be
// independently controllable.
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
)

var (
	// ErrInvalidPath indicates an invalid absolute root or relative path.
	ErrInvalidPath = errors.New("sourcefs: invalid path")
	// ErrLink indicates a symbolic link or reparse point was encountered.
	ErrLink = errors.New("sourcefs: links are not permitted")
	// ErrNotDirectory indicates the requested component is not a directory.
	ErrNotDirectory = errors.New("sourcefs: not a directory")
	// ErrNotRegular indicates the requested terminal object is not a regular file.
	ErrNotRegular = errors.New("sourcefs: not a regular file")
	// ErrUnsupported indicates no safe adapter is available for this operation.
	ErrUnsupported = errors.New("sourcefs: safe filesystem access is unsupported")
	// ErrUnsupportedNetworkRoot indicates that Windows UNC roots are not supported.
	ErrUnsupportedNetworkRoot = errors.New("windows UNC source roots are not supported yet; use a local drive path")
	// ErrInvalidBorrow indicates a nil file-borrow callback.
	ErrInvalidBorrow = errors.New("sourcefs: invalid file borrow callback")
)

// ValidateRootPathSupport rejects Windows UNC roots before callers perform any
// filesystem access.
func ValidateRootPathSupport(path string) error {
	return validateRootPathSupport(path, runtime.GOOS == "windows")
}

func validateRootPathSupport(path string, windows bool) error {
	if !windows {
		return nil
	}
	// Extended-length and NT device namespace paths can still name a network
	// share. Reject those UNC forms, but leave other device paths to their
	// existing validation contract.
	if isDeviceUNCPath(path) {
		return ErrUnsupportedNetworkRoot
	}
	if len(path) < 3 || !isPathSeparator(path[0]) || !isPathSeparator(path[1]) {
		return nil
	}
	if path[2] == '?' || path[2] == '.' {
		return nil
	}
	return ErrUnsupportedNetworkRoot
}

func isDeviceUNCPath(path string) bool {
	// Both the extended-length (`?`) and Win32 device (`.`) namespaces can name a
	// network share; match either, and leave other device paths to the opener's
	// existing invalid-path contract.
	if len(path) >= 8 && isPathSeparator(path[0]) && isPathSeparator(path[1]) &&
		(path[2] == '?' || path[2] == '.') && isPathSeparator(path[3]) &&
		strings.EqualFold(path[4:7], "UNC") && isPathSeparator(path[7]) {
		return true
	}
	if len(path) >= 8 && isPathSeparator(path[0]) && path[1] == '?' &&
		path[2] == '?' && isPathSeparator(path[3]) &&
		strings.EqualFold(path[4:7], "UNC") && isPathSeparator(path[7]) {
		return true
	}
	return len(path) >= 9 && isPathSeparator(path[0]) && isPathSeparator(path[1]) &&
		path[2] == '?' && path[3] == '?' && isPathSeparator(path[4]) &&
		strings.EqualFold(path[5:8], "UNC") && isPathSeparator(path[8])
}

func isPathSeparator(value byte) bool { return value == '/' || value == '\\' }

// Kind is an advisory classification returned by directory enumeration.
type Kind uint8

const (
	KindUnknown Kind = iota
	KindDir
	KindRegular
	KindExcluded
)

// Entry is one directory entry. Kind is advisory and is not a substitute for
// safely opening and checking the object.
type Entry struct {
	Name string
	Kind Kind
}

// Opener anchors an absolute path to a pinned directory handle.
type Opener interface {
	// OpenRoot opens the directory named by absolute. Adapters must resolve
	// every component of the root path from the filesystem/volume anchor
	// without following links or reparse points, opening each component
	// relative to its already-open parent handle. A single os.Open (or
	// equivalent) of the full concatenated path is not acceptable: it would
	// let the operating system traverse links between the anchor and the
	// target. The returned Directory owns the pinned handle.
	OpenRoot(ctx context.Context, absolute string) (Directory, error)
}

// Directory is an opened directory handle. Close must be idempotent. Each
// successful OpenDir/OpenRegular returns a separately owned handle.
type Directory interface {
	ReadDir(ctx context.Context, n int) ([]Entry, error)
	OpenDir(ctx context.Context, oneComponent string) (Directory, error)
	OpenRegular(ctx context.Context, oneComponent string) (RegularFile, error)
	Stat(ctx context.Context) (fs.FileInfo, error)
	Close() error
}

// RegularFile is an opened regular file. Borrow is a synchronous exclusive loan:
// the callback owns use of the underlying descriptor until it returns. Each loan
// starts rewound to offset zero; callers must not retain or use the pointer after
// callback return. Close must be idempotent and wait for an active loan.
type RegularFile interface {
	Stat(ctx context.Context) (fs.FileInfo, error)
	Borrow(ctx context.Context, callback func(*os.File) error) error
	Close() error
}

type unsupportedOpener struct{}

// NewOpener returns the platform opener.
func NewOpener() Opener { return newPlatformOpener() }

func (unsupportedOpener) OpenRoot(_ context.Context, absolute string) (Directory, error) {
	if !filepath.IsAbs(absolute) || absolute == "" {
		return nil, ErrInvalidPath
	}
	return nil, ErrUnsupported
}

// OpenRegularAt validates the complete relative path before making any adapter
// calls, then resolves each component from the current directory handle. The
// caller retains ownership of root; all intermediate handles are closed.
func OpenRegularAt(ctx context.Context, root Directory, relativePath string) (RegularFile, error) {
	if runtime.GOOS == "windows" {
		relativePath = filepath.ToSlash(relativePath)
	}
	components, err := validateRelativePath(relativePath, runtime.GOOS == "windows")
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current := root
	owned := false
	for _, component := range components[:len(components)-1] {
		next, openErr := current.OpenDir(ctx, component)
		if owned {
			closeErr := current.Close()
			if openErr == nil && closeErr != nil {
				_ = next.Close()
				return nil, closeErr
			}
		}
		if openErr != nil {
			return nil, openErr
		}
		current, owned = next, true
	}
	file, openErr := current.OpenRegular(ctx, components[len(components)-1])
	if owned {
		closeErr := current.Close()
		if openErr == nil && closeErr != nil {
			_ = file.Close()
			return nil, closeErr
		}
	}
	return file, openErr
}

func validateRelativePath(value string, windows bool) ([]string, error) {
	if value == "" || strings.HasPrefix(value, "/") || strings.ContainsRune(value, '\x00') {
		return nil, ErrInvalidPath
	}
	if windows && (strings.Contains(value, `\`) || strings.HasPrefix(value, "//")) {
		return nil, ErrInvalidPath
	}
	components := strings.Split(value, "/")
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return nil, ErrInvalidPath
		}
		if windows && invalidWindowsComponent(component) {
			return nil, ErrInvalidPath
		}
	}
	return components, nil
}

func invalidWindowsComponent(component string) bool {
	if strings.ContainsAny(component, `<>:"|?*`) || strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
		return true
	}
	base := strings.ToUpper(strings.SplitN(component, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}

// regularFile is the common ownership/borrowing implementation adapters can
// embed around an already safely opened descriptor.
type regularFile struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
}

func newRegularFile(file *os.File) RegularFile { return &regularFile{file: file} }

func (file *regularFile) Stat(ctx context.Context) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed {
		return nil, os.ErrClosed
	}
	return file.file.Stat()
}

func (file *regularFile) Borrow(ctx context.Context, callback func(*os.File) error) error {
	if callback == nil {
		return ErrInvalidBorrow
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed {
		return os.ErrClosed
	}
	if _, err := file.file.Seek(0, 0); err != nil {
		return err
	}
	return callback(file.file)
}

func (file *regularFile) Close() error {
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed {
		return nil
	}
	file.closed = true
	return file.file.Close()
}
