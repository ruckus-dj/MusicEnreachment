package tools

import (
	"context"
	"errors"
	"io"
	"os"
)

// JobCommandRequest describes a process launched by the platform command runner.
// Input is borrowed stdin and remains owned by its caller. FileInput is a
// borrowed already-open file transported using the platform's file-command
// descriptor convention (fd 3 on Unix, stdin on Windows). At most one may be
// set. A nil input uses the platform's null device. Stdout and Stderr receive
// the respective streams; nil writers discard those streams.
type JobCommandRequest struct {
	Executable string
	Args       []string
	Input      *os.File
	FileInput  *os.File
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
	if request.Input != nil && request.FileInput != nil {
		return JobCommandResult{QuiescenceEstablished: true}, errors.New("job command cannot use both stdin and file input")
	}
	return runPlatformJobCommand(ctx, request)
}
