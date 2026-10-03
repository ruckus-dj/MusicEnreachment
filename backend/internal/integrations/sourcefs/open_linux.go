//go:build linux

package sourcefs

import (
	"golang.org/x/sys/unix"
)

func newPlatformOpener() Opener { return newUnixOpener(platformOpenChild) }

func platformOpenChild(parent int, name string, flags int) (int, error) {
	return openChildLinux(parent, name, flags)
}

func openChildLinux(parent int, name string, flags int) (int, error) {
	how := &unix.OpenHow{
		Flags:   uint64(flags),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS,
	}
	return unix.Openat2(parent, name, how)
}
