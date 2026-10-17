package tools

import (
	"context"
	"io"
	"os"
)

// JobCommandRequest describes a process launched in a platform containment job.
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

// JobCommandResult describes the process exit and whether the runner proved all
// members of its containment job have stopped. It does not prove containment of
// brokered or otherwise arbitrary descendants outside that job.
type JobCommandResult struct {
	QuiescenceEstablished bool
	ExitCode              uint32
}

// RunJobCommand runs executable with args and streams its output. On platforms
// without a supported containment backend it fails closed without executing.
func RunJobCommand(ctx context.Context, request JobCommandRequest) (JobCommandResult, error) {
	return runPlatformJobCommand(ctx, request)
}
