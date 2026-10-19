//go:build darwin && cgo

package sourcefs

/*
#include <errno.h>
#include <sys/attr.h>
#include <unistd.h>

static int melo_fgetattrlist(int fd, void *attrs, void *buffer, size_t size) {
	if (fgetattrlist(fd, (struct attrlist *)attrs, buffer, size, 0) == -1) {
		return errno;
	}
	return 0;
}
*/
import "C"

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

func darwinFGetAttrList(fd int, attrs, buffer unsafe.Pointer, size uintptr) error {
	if errno := C.melo_fgetattrlist(C.int(fd), attrs, buffer, C.size_t(size)); errno != 0 {
		return unix.Errno(errno)
	}
	return nil
}
