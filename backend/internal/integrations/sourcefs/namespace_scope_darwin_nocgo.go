//go:build darwin && !cgo

package sourcefs

import (
	"unsafe"
)

func darwinFGetAttrList(int, unsafe.Pointer, unsafe.Pointer, uintptr) error {
	return ErrUnsupported
}
