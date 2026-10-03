//go:build darwin

package sourcefs

import "golang.org/x/sys/unix"

func newPlatformOpener() Opener { return newUnixOpener(platformOpenChild) }

func platformOpenChild(parent int, name string, flags int) (int, error) {
	return unix.Openat(parent, name, flags, 0)
}
