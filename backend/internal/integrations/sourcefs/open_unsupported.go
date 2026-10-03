//go:build !linux && !darwin

package sourcefs

func newPlatformOpener() Opener { return unsupportedOpener{} }
