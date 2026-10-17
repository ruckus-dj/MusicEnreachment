//go:build !windows

package tools

import (
	"context"
	"errors"
)

func runPlatformJobCommand(context.Context, JobCommandRequest) (JobCommandResult, error) {
	return JobCommandResult{}, errors.New("job command containment is unavailable on this platform")
}
