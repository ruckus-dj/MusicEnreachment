//go:build linux || darwin

package sourcefs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

type unixOpener struct {
	openChild func(int, string, int) (int, error)
}

func newUnixOpener(openChild func(int, string, int) (int, error)) *unixOpener {
	return &unixOpener{openChild: openChild}
}

func (opener *unixOpener) OpenRoot(ctx context.Context, absolute string) (Directory, error) {
	if !filepath.IsAbs(absolute) || absolute == "" || strings.ContainsRune(absolute, '\x00') {
		return nil, ErrInvalidPath
	}
	absolute = filepath.Clean(absolute)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	current := fd
	components := strings.Split(strings.TrimPrefix(absolute, "/"), "/")
	if absolute == "/" {
		components = nil
	}
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			_ = unix.Close(current)
			return nil, ErrInvalidPath
		}
		next, openErr := opener.openChild(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW)
		_ = unix.Close(current)
		if openErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, classifyUnixError(openErr, true)
		}
		if err := ctx.Err(); err != nil {
			_ = unix.Close(next)
			return nil, err
		}
		current = next
	}
	return directoryFromFD(current, opener.openChild), nil
}

func classifyUnixError(err error, directory bool) error {
	if runtime.GOOS == "linux" && (errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL)) {
		return errors.Join(ErrUnsupported, err)
	}
	if errors.Is(err, unix.ELOOP) {
		return errors.Join(ErrLink, err)
	}
	if directory && errors.Is(err, unix.ENOTDIR) {
		return errors.Join(ErrNotDirectory, err)
	}
	return err
}

func validateComponent(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return ErrInvalidPath
	}
	return nil
}

type unixDirectory struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
	open   func(int, string, int) (int, error)
}

func directoryFromFD(fd int, open func(int, string, int) (int, error)) Directory {
	return &unixDirectory{file: os.NewFile(uintptr(fd), "sourcefs-directory"), open: open}
}

func (directory *unixDirectory) ReadDir(ctx context.Context, n int) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	if directory.closed {
		return nil, os.ErrClosed
	}
	items, err := directory.file.ReadDir(n)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		kind := KindUnknown
		if item.IsDir() {
			kind = KindDir
		} else if item.Type().IsRegular() {
			kind = KindRegular
		} else if item.Type()&os.ModeSymlink != 0 || item.Type() != 0 {
			kind = KindExcluded
		}
		entries = append(entries, Entry{Name: item.Name(), Kind: kind})
	}
	if n > 0 && len(entries) == 0 && errors.Is(err, io.EOF) {
		return nil, io.EOF
	}
	return entries, err
}

func (directory *unixDirectory) OpenDir(ctx context.Context, name string) (Directory, error) {
	fd, err := directory.openComponent(ctx, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW)
	if err != nil {
		return nil, classifyUnixError(err, true)
	}
	return directoryFromFD(fd, directory.open), nil
}

func (directory *unixDirectory) OpenRegular(ctx context.Context, name string) (RegularFile, error) {
	fd, err := directory.openComponent(ctx, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK)
	if err != nil {
		return nil, classifyUnixError(err, false)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, ErrNotRegular
	}
	return newRegularFile(os.NewFile(uintptr(fd), "sourcefs-file")), nil
}

func (directory *unixDirectory) openComponent(ctx context.Context, name string, flags int) (int, error) {
	if err := validateComponent(name); err != nil {
		return -1, err
	}
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	if directory.closed {
		return -1, os.ErrClosed
	}
	fd, err := directory.open(int(directory.file.Fd()), name, flags)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return -1, ctxErr
		}
		return -1, err
	}
	if err := ctx.Err(); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func (directory *unixDirectory) Stat(ctx context.Context) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	if directory.closed {
		return nil, os.ErrClosed
	}
	info, err := directory.file.Stat()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return info, err
}

func (directory *unixDirectory) Close() error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	if directory.closed {
		return nil
	}
	directory.closed = true
	return directory.file.Close()
}
