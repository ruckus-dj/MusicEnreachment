//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package settings

import (
	"fmt"
	"os"
)

func durableDirectoryIdentity(string, os.FileInfo) (string, error) {
	return "", fmt.Errorf("filesystem does not expose a durable directory identity")
}
