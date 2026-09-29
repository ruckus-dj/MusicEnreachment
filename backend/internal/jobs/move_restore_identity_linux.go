package jobs

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func moveRestoreFileIdentity(file *os.File) (moveRestoreIdentity, uint64, error) {
	var stat unix.Statx_t
	if err := unix.Statx(int(file.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_INO|unix.STATX_BTIME|unix.STATX_NLINK, &stat); err != nil {
		return moveRestoreIdentity{}, 0, err
	}
	if stat.Mask&unix.STATX_BTIME == 0 {
		return moveRestoreIdentity{}, 0, fmt.Errorf("source filesystem cannot identify restore inode creation time")
	}
	return moveRestoreIdentity{
		Volume:  uint64(stat.Dev_major)<<32 | uint64(stat.Dev_minor),
		File:    stat.Ino,
		Created: stat.Btime.Sec*1e9 + int64(stat.Btime.Nsec),
	}, uint64(stat.Nlink), nil
}
