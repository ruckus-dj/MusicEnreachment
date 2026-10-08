//go:build linux || darwin

package sourcefs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

type unixOutputOpener struct {
	open func(int, string, int) (int, error)
}

func newPlatformOutputOpener() OutputOpener { return &unixOutputOpener{open: platformOpenChild} }

func outputOpenAt(parent int, name string, flags, mode int) (int, error) {
	return unix.Openat(parent, name, flags, uint32(mode))
}

func (opener *unixOutputOpener) OpenRoot(ctx context.Context, absolute string) (OutputDirectory, error) {
	if absolute == "" || !filepath.IsAbs(absolute) || strings.ContainsRune(absolute, '\x00') {
		return nil, ErrInvalidPath
	}
	clean := filepath.Clean(absolute)
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(clean, "/"), "/") {
		if part == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
		next, openErr := opener.open(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, classifyUnixError(openErr, true)
		}
		fd = next
	}
	if err := ctx.Err(); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return &unixOutputDir{file: os.NewFile(uintptr(fd), "sourcefs-output"), open: opener.open, path: clean, root: true}, nil
}

type unixOutputDir struct {
	mu     sync.Mutex
	file   *os.File
	open   func(int, string, int) (int, error)
	path   string
	root   bool
	closed bool
}

func (d *unixOutputDir) OpenOrCreateDir(ctx context.Context, name string) (OutputDirectory, error) {
	if err := validateComponent(name); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, os.ErrClosed
	}
	parent := int(d.file.Fd())
	if err := unix.Mkdirat(parent, name, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		d.mu.Unlock()
		return nil, err
	}
	fd, err := d.open(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW)
	d.mu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		return nil, ctxErr
	}
	if err != nil {
		return nil, classifyUnixError(err, true)
	}
	return &unixOutputDir{file: os.NewFile(uintptr(fd), "sourcefs-output-child"), open: d.open}, nil
}

func (d *unixOutputDir) CreateExclusive(ctx context.Context, name string) (OutputFile, error) {
	if err := validateComponent(name); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, os.ErrClosed
	}
	fd, err := outputOpenAt(int(d.file.Fd()), name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o600)
	d.mu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		return nil, ctxErr
	}
	if err != nil {
		return nil, classifyUnixError(err, false)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, ErrNotRegular
	}
	return &unixOutputFile{file: os.NewFile(uintptr(fd), "sourcefs-output-file")}, nil
}

func (d *unixOutputDir) Stat(ctx context.Context) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, os.ErrClosed
	}
	return d.file.Stat()
}

func (d *unixOutputDir) Verify(ctx context.Context) error {
	if !d.root {
		return ErrInvalidPath
	}
	current, err := newPlatformOutputOpener().OpenRoot(ctx, d.path)
	if err != nil {
		return err
	}
	defer func() { _ = current.Close() }()
	pinnedInfo, err := d.Stat(ctx)
	if err != nil {
		return err
	}
	currentInfo, err := current.Stat(ctx)
	if err != nil {
		return err
	}
	if !os.SameFile(pinnedInfo, currentInfo) {
		return errors.New("sourcefs: output namespace changed")
	}
	return nil
}

func (d *unixOutputDir) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.file.Close()
}

type unixOutputFile struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
}

func (f *unixOutputFile) with(ctx context.Context, fn func(*os.File) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return os.ErrClosed
	}
	return fn(f.file)
}
func (f *unixOutputFile) Write(ctx context.Context, data []byte) (int, error) {
	var n int
	err := f.with(ctx, func(file *os.File) error { var e error; n, e = file.Write(data); return e })
	return n, err
}
func (f *unixOutputFile) Sync(ctx context.Context) error {
	return f.with(ctx, func(file *os.File) error { return file.Sync() })
}
func (f *unixOutputFile) Stat(ctx context.Context) (fs.FileInfo, error) {
	var info fs.FileInfo
	err := f.with(ctx, func(file *os.File) error { var e error; info, e = file.Stat(); return e })
	return info, err
}
func (f *unixOutputFile) BorrowRead(ctx context.Context, callback func(*os.File) error) error {
	if callback == nil {
		return ErrInvalidBorrow
	}
	return f.with(ctx, func(file *os.File) error {
		if _, err := file.Seek(0, 0); err != nil {
			return err
		}
		return callback(file)
	})
}
func (f *unixOutputFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	return f.file.Close()
}
