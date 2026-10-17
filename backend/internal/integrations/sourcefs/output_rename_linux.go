//go:build linux

package sourcefs

import "golang.org/x/sys/unix"

func renameNoReplaceNative(sourceParent int, sourceName string, targetParent int, targetName string) error {
	return unix.Renameat2(sourceParent, sourceName, targetParent, targetName, unix.RENAME_NOREPLACE)
}
