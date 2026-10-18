//go:build !windows && !linux && !darwin

package tools

import (
	"context"
	"errors"
)

func runPlatformJobCommand(context.Context, JobCommandRequest) (JobCommandResult, error) {
	return JobCommandResult{}, errors.New("job command backend is unavailable on this platform")
}
