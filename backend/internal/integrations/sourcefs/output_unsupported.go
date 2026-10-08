//go:build !linux && !darwin && !windows

package sourcefs

import (
	"context"
	"path/filepath"
)

func newPlatformOutputOpener() OutputOpener { return unsupportedOutputOpener{} }

type unsupportedOutputOpener struct{}

func (unsupportedOutputOpener) OpenRoot(_ context.Context, absolute string) (OutputDirectory, error) {
	if absolute == "" || !filepath.IsAbs(absolute) {
		return nil, ErrInvalidPath
	}
	return nil, ErrUnsupported
}
