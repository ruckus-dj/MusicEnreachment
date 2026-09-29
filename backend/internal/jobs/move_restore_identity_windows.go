package jobs

import (
	"os"
	"syscall"
)

func moveRestoreFileIdentity(file *os.File) (moveRestoreIdentity, uint64, error) {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info); err != nil {
		return moveRestoreIdentity{}, 0, err
	}
	return moveRestoreIdentity{
		Volume:  uint64(info.VolumeSerialNumber),
		File:    uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow),
		Created: info.CreationTime.Nanoseconds(),
	}, uint64(info.NumberOfLinks), nil
}
