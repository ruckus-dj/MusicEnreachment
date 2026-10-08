//go:build !linux && !darwin && !windows

package sourcefs

import "context"

func removeRegistered(context.Context, string, string) (CleanupOutcome, error) {
	return 0, ErrUnsupported
}
