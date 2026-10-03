//go:build !linux && !darwin && !windows

package sourcefs

func newPlatformOpener() Opener { return unsupportedOpener{} }
