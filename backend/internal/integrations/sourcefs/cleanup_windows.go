//go:build windows

package sourcefs

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func removeRegistered(ctx context.Context, outputRoot, relative string) (CleanupOutcome, error) {
	opened, err := NewOutputOpener().OpenRoot(ctx, outputRoot)
	if err != nil {
		return 0, err
	}
	root, ok := opened.(*windowsOutputDir)
	if !ok {
		_ = opened.Close()
		return 0, ErrUnsupported
	}
	defer func() { _ = root.Close() }()
	if err := root.Verify(ctx); err != nil {
		return 0, err
	}
	parts := strings.Split(relative, "/")
	parent := root
	owned := false
	for _, component := range parts[:len(parts)-1] {
		if err := ctx.Err(); err != nil {
			if owned {
				_ = parent.Close()
			}
			return 0, err
		}
		nextDir, openErr := parent.openDir(ctx, component)
		if openErr != nil {
			if owned {
				_ = parent.Close()
			}
			if isWindowsCleanupMissing(openErr) {
				return CleanupMissing, nil
			}
			return 0, openErr
		}
		if owned {
			_ = parent.Close()
		}
		parent = nextDir
		owned = true
	}
	if owned {
		defer func() { _ = parent.Close() }()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var handle windows.Handle
	parent.mu.Lock()
	if parent.closed {
		err = errors.New("sourcefs: output directory is closed")
	} else {
		handle, err = openCleanupWindowsFile(parent.handle, parts[len(parts)-1])
	}
	parent.mu.Unlock()
	if err != nil {
		if isWindowsCleanupMissing(err) {
			return CleanupMissing, nil
		}
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := root.Verify(ctx); err != nil {
		return 0, err
	}
	var disposition byte = 1
	if err := windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &disposition, uint32(unsafe.Sizeof(disposition))); err != nil {
		if isWindowsCleanupMissing(err) {
			return CleanupMissing, nil
		}
		return 0, err
	}
	return CleanupDeleted, nil
}

func openCleanupWindowsFile(parent windows.Handle, name string) (windows.Handle, error) {
	if invalidWindowsName(name) {
		return windows.InvalidHandle, ErrInvalidPath
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return windows.InvalidHandle, ErrInvalidPath
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	var handle windows.Handle
	var statusBlock windows.IO_STATUS_BLOCK
	status := windows.NtCreateFile(
		&handle,
		windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		&attributes,
		&statusBlock,
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_OPEN,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	)
	runtime.KeepAlive(objectName)
	if status != nil {
		return windows.InvalidHandle, status
	}
	if err := verifyHandle(handle, false); err != nil {
		_ = windows.CloseHandle(handle)
		return windows.InvalidHandle, err
	}
	return handle, nil
}

func isWindowsCleanupMissing(err error) bool {
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return true
	}
	status, ok := err.(windows.NTStatus)
	return ok && (status == windows.STATUS_OBJECT_NAME_NOT_FOUND || status == windows.STATUS_OBJECT_PATH_NOT_FOUND)
}
