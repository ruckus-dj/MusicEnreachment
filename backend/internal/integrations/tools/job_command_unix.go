//go:build linux || darwin

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// jobCommandWaitDelay limits how long Wait waits for output-copy goroutines
// after the main process exits. Descendants may have inherited pipe handles;
// their continued existence is deliberately not treated as containment.
const jobCommandWaitDelay = 250 * time.Millisecond

type unixJobOutputWriter struct {
	mu     sync.Mutex
	writer io.Writer
	err    error
}

func (writer *unixJobOutputWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.err != nil {
		return len(data), nil
	}
	written, err := writer.writer.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		writer.err = err
		// Keep draining the child pipe after a downstream failure, so the
		// process can exit and its wait result remains independent.
		return len(data), nil
	}
	return written, nil
}

func (writer *unixJobOutputWriter) Err() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.err
}

func runPlatformJobCommand(ctx context.Context, request JobCommandRequest) (result JobCommandResult, returnedErr error) {
	if request.Executable == "" || !filepath.IsAbs(request.Executable) {
		return result, errors.New("job command executable must be an absolute path")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	command := exec.CommandContext(ctx, request.Executable, request.Args...)
	command.Stdin = request.Input // borrowed: exec does not close the caller's file
	if request.Input == nil {
		command.Stdin = nil
	}
	if request.FileInput != nil {
		if err := configureFileCommandTransport(command, request.FileInput); err != nil {
			return result, fmt.Errorf("configure job command file input: %w", err)
		}
	}
	stdout := &unixJobOutputWriter{writer: request.Stdout}
	if stdout.writer == nil {
		stdout.writer = io.Discard
	}
	stderr := &unixJobOutputWriter{writer: request.Stderr}
	if stderr.writer == nil {
		stderr.writer = io.Discard
	}
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = jobCommandWaitDelay
	if err := command.Start(); err != nil {
		return result, fmt.Errorf("start job command: %w", err)
	}

	waitErr := command.Wait()
	outputErr := errors.Join(outputWriterError("stdout", stdout.Err()), outputWriterError("stderr", stderr.Err()))
	// Wait's successful return or error establishes the main process's wait
	// completion. Its error is kept independently (e.g. nonzero exit/cancel).
	result.QuiescenceEstablished = true
	if command.ProcessState != nil {
		code := command.ProcessState.ExitCode()
		if code >= 0 {
			result.ExitCode = uint32(code)
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		waitErr = errors.Join(waitErr, ctxErr)
	}
	waitErr = errors.Join(waitErr, outputErr)
	if waitErr != nil {
		return result, fmt.Errorf("wait for job command: %w", waitErr)
	}
	return result, nil
}

func outputWriterError(stream string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("write job command %s: %w", stream, err)
}
