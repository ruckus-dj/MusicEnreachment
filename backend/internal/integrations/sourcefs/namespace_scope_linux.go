//go:build linux

package sourcefs

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

const fsCasefoldFlag = 0x40000000

func verifyNativeNamespaceProbeScope(ctx context.Context, parent, scratch OutputDirectory) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	parent = unwrapNamespaceScopeDirectory(parent)
	scratch = unwrapNamespaceScopeDirectory(scratch)
	p, ok := parent.(*unixOutputDir)
	if !ok {
		return errors.New("sourcefs: native parent capability unavailable")
	}
	s, ok := scratch.(*unixOutputDir)
	if !ok {
		return errors.New("sourcefs: native scratch capability unavailable")
	}
	pfd, err := duplicateOutputDirFD(p)
	if err != nil {
		return err
	}
	defer unix.Close(pfd)
	sfd, err := duplicateOutputDirFD(s)
	if err != nil {
		return err
	}
	defer unix.Close(sfd)
	var ps, ss unix.Stat_t
	if err = unix.Fstat(pfd, &ps); err != nil {
		return err
	}
	if err = unix.Fstat(sfd, &ss); err != nil {
		return err
	}
	if ps.Dev != ss.Dev {
		return errors.New("sourcefs: probe crossed filesystem boundary")
	}
	var pf, sf unix.Statfs_t
	if err = unix.Fstatfs(pfd, &pf); err != nil {
		return err
	}
	if err = unix.Fstatfs(sfd, &sf); err != nil {
		return err
	}
	if pf.Type != sf.Type {
		return errors.New("sourcefs: probe filesystem type differs from parent")
	}
	pflags, err := linuxDirectoryFlags(pfd)
	if err != nil {
		return err
	}
	sflags, err := linuxDirectoryFlags(sfd)
	if err != nil {
		return err
	}
	if pflags&fsCasefoldFlag != sflags&fsCasefoldFlag {
		return fmt.Errorf("sourcefs: probe casefold mode differs from parent")
	}
	return nil
}

func linuxDirectoryFlags(fd int) (int, error) {
	flags, err := unix.IoctlGetInt(fd, unix.FS_IOC_GETFLAGS)
	if err != nil {
		return 0, fmt.Errorf("sourcefs: cannot verify directory casefold mode: %w", err)
	}
	return flags, nil
}
