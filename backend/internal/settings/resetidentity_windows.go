//go:build windows

package settings

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func durableDirectoryIdentity(path string, _ os.FileInfo) (string, error) {
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(path), windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0,
	)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", fmt.Errorf("path is not a non-reparse directory")
	}
	fileID := uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
	return fmt.Sprintf("windows:%08x:%016x", info.VolumeSerialNumber, fileID), nil
}
