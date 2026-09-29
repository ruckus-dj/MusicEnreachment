package jobs

import (
	"os"
	"syscall"
)

func moveRestoreFileIdentity(file *os.File) (moveRestoreIdentity, uint64, error) {
	info, err := file.Stat()
	if err != nil {
		return moveRestoreIdentity{}, 0, err
	}
	stat := info.Sys().(*syscall.Stat_t)
	return moveRestoreIdentity{
		Volume:  uint64(stat.Dev),
		File:    uint64(stat.Ino),
		Created: stat.Birthtimespec.Sec*1e9 + stat.Birthtimespec.Nsec,
	}, uint64(stat.Nlink), nil
}
