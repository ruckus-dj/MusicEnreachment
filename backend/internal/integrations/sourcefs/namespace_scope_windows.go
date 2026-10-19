//go:build windows

package sourcefs

import (
	"context"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

type fileCaseSensitiveInfo struct{ Flags uint32 }

func verifyNativeNamespaceProbeScope(ctx context.Context, parent, scratch OutputDirectory) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	parent = unwrapNamespaceScopeDirectory(parent)
	scratch = unwrapNamespaceScopeDirectory(scratch)
	p, ok := parent.(*windowsOutputDir)
	if !ok {
		return errors.New("sourcefs: native parent capability unavailable")
	}
	s, ok := scratch.(*windowsOutputDir)
	if !ok {
		return errors.New("sourcefs: native scratch capability unavailable")
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return windows.ERROR_INVALID_HANDLE
	}
	ph := p.handle
	p.mu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return windows.ERROR_INVALID_HANDLE
	}
	sh := s.handle
	s.mu.Unlock()
	pi, err := p.Identity(ctx)
	if err != nil {
		return err
	}
	si, err := s.Identity(ctx)
	if err != nil {
		return err
	}
	if pi.Volume != si.Volume {
		return errors.New("sourcefs: probe crossed volume boundary")
	}
	var parentInfo, scratchInfo fileCaseSensitiveInfo
	if err = windows.GetFileInformationByHandleEx(ph, windows.FileCaseSensitiveInfo, (*byte)(unsafe.Pointer(&parentInfo)), uint32(unsafe.Sizeof(parentInfo))); err != nil {
		return err
	}
	if parentInfo.Flags&^windows.FILE_CS_FLAG_CASE_SENSITIVE_DIR != 0 {
		return errors.New("sourcefs: unknown Windows case-sensitivity flags")
	}
	if err = windows.GetFileInformationByHandleEx(sh, windows.FileCaseSensitiveInfo, (*byte)(unsafe.Pointer(&scratchInfo)), uint32(unsafe.Sizeof(scratchInfo))); err != nil {
		return err
	}
	if scratchInfo.Flags&^windows.FILE_CS_FLAG_CASE_SENSITIVE_DIR != 0 {
		return errors.New("sourcefs: unknown Windows scratch case-sensitivity flags")
	}
	if scratchInfo != parentInfo {
		if err = windows.SetFileInformationByHandle(sh, windows.FileCaseSensitiveInfo, (*byte)(unsafe.Pointer(&parentInfo)), uint32(unsafe.Sizeof(parentInfo))); err != nil {
			return err
		}
		if err = windows.GetFileInformationByHandleEx(sh, windows.FileCaseSensitiveInfo, (*byte)(unsafe.Pointer(&scratchInfo)), uint32(unsafe.Sizeof(scratchInfo))); err != nil {
			return err
		}
	}
	if scratchInfo != parentInfo {
		return errors.New("sourcefs: scratch case-sensitivity mode did not match parent")
	}
	return nil
}
