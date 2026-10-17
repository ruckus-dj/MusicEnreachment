//go:build darwin

package sourcefs

import "golang.org/x/sys/unix"

func renameNoReplaceNative(sourceParent int, sourceName string, targetParent int, targetName string) error {
	return unix.RenameatxNp(sourceParent, sourceName, targetParent, targetName, unix.RENAME_EXCL)
}
