//go:build linux || darwin

package sourcefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func removeRegistered(ctx context.Context, outputRoot, relative string) (CleanupOutcome, error) {
	rootCapability, err := NewOpener().OpenRoot(ctx, outputRoot)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rootCapability.Close() }()
	root, ok := rootCapability.(*unixDirectory)
	if !ok {
		return 0, ErrUnsupported
	}
	rootInfo, err := root.Stat(ctx)
	if err != nil {
		return 0, err
	}
	parts := strings.Split(relative, "/")
	leaf := parts[len(parts)-1]
	parentFD, err := unix.Dup(int(root.file.Fd()))
	if err != nil {
		return 0, err
	}
	defer func() { _ = unix.Close(parentFD) }()
	for _, component := range parts[:len(parts)-1] {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		next, openErr := unix.Openat(parentFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			if errors.Is(openErr, unix.ENOENT) {
				return CleanupMissing, nil
			}
			return 0, classifyUnixError(openErr, true)
		}
		_ = unix.Close(parentFD)
		parentFD = next
	}

	file, err := OpenRegularAt(ctx, root, relative)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return CleanupMissing, nil
		}
		return 0, err
	}
	defer func() { _ = file.Close() }()
	var opened, named unix.Stat_t
	if err := file.Borrow(ctx, func(f *os.File) error { return unix.Fstat(int(f.Fd()), &opened) }); err != nil {
		return 0, err
	}
	if err := unix.Fstatat(parentFD, leaf, &named, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return CleanupMissing, nil
		}
		return 0, err
	}
	if named.Mode&unix.S_IFMT != unix.S_IFREG {
		return 0, ErrNotRegular
	}
	if opened.Dev != named.Dev || opened.Ino != named.Ino {
		return 0, errors.New("sourcefs: registered artifact namespace changed")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := verifyCleanupUnixRoot(ctx, outputRoot, rootInfo); err != nil {
		return 0, err
	}
	if err := unix.Unlinkat(parentFD, leaf, 0); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return CleanupMissing, nil
		}
		return 0, err
	}
	return CleanupDeleted, nil
}

func verifyCleanupUnixRoot(ctx context.Context, path string, expected os.FileInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := NewOpener().OpenRoot(ctx, filepath.Clean(path))
	if err != nil {
		return err
	}
	defer func() { _ = current.Close() }()
	info, err := current.Stat(ctx)
	if err != nil {
		return err
	}
	if !os.SameFile(expected, info) {
		return errors.New("sourcefs: output root namespace changed")
	}
	return ctx.Err()
}
