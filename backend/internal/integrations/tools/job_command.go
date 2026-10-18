package tools

import (
	"context"
	"io"
	"os"
)

// JobCommandRequest describes a process launched by the platform command runner.
// Input is borrowed and remains owned by its caller. A nil Input uses the
// platform's null device. Stdout and Stderr receive the respective streams; nil
// writers discard those streams.
type JobCommandRequest struct {
	Executable string
	Args       []string
	Input      *os.File
	Stdout     io.Writer
	Stderr     io.Writer
}

// JobCommandResult describes the process exit and whether the runner established
// that the main process has exited. This does not prove that descendants have
// stopped or can no longer access inherited resources.
type JobCommandResult struct {
	QuiescenceEstablished bool
	ExitCode              uint32
}

// RunJobCommand runs executable with args and streams its output. On Linux and
// macOS, quiescence means only that the main process has exited; descendants are
// not contained. Other platforms without a supported backend fail closed.
func RunJobCommand(ctx context.Context, request JobCommandRequest) (JobCommandResult, error) {
	return runPlatformJobCommand(ctx, request)
}
