//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package settings

import (
	"fmt"
	"os"
	"syscall"
)

func durableDirectoryIdentity(path string, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("filesystem does not expose a durable directory identity")
	}
	// Device and inode identify the directory without changing when its contents
	// change. Inode reuse after deletion can produce an ABA match, so this is an
	// ownership check against ordinary replacement, not proof against hostile
	// concurrent actors or inode-reuse races.
	return fmt.Sprintf("unix:%d:%d", stat.Dev, stat.Ino), nil
}
