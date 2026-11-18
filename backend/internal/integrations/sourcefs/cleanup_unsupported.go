//go:build !linux && !darwin && !windows

package sourcefs

import (
	"context"
	"os"
)

func removeRegistered(context.Context, string, string, os.FileInfo) (CleanupOutcome, error) {
	return 0, ErrUnsupported
}
