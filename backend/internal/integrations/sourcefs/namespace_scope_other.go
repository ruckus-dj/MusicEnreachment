//go:build !linux && !windows && !darwin

package sourcefs

import (
	"context"
	"errors"
)

func verifyNativeNamespaceProbeScope(context.Context, OutputDirectory, OutputDirectory) error {
	return errors.New("sourcefs: native namespace scope verification is unavailable")
}
