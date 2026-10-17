package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
)

type publicationJobCommandBackend func(context.Context, tools.JobCommandRequest) (tools.JobCommandResult, error)

// PublicationJobCommands adapts the contained job-command backend to publication
// command runners while keeping stdout and stderr independently bounded.
type PublicationJobCommands struct {
	stdoutLimit int
	stderrLimit int
	run         publicationJobCommandBackend
}

func NewPublicationJobCommands(stdoutLimit, stderrLimit int) (*PublicationJobCommands, error) {
	return newPublicationJobCommands(stdoutLimit, stderrLimit, tools.RunJobCommand)
}

func newPublicationJobCommands(stdoutLimit, stderrLimit int, run publicationJobCommandBackend) (*PublicationJobCommands, error) {
	if stdoutLimit <= 0 || stderrLimit <= 0 {
		return nil, errors.New("publication command output limits must be positive")
	}
	if run == nil {
		return nil, errors.New("publication command backend is unavailable")
	}
	return &PublicationJobCommands{stdoutLimit: stdoutLimit, stderrLimit: stderrLimit, run: run}, nil
}

func (commands *PublicationJobCommands) Run(ctx context.Context, executable string, args ...string) (PublicationCommandResult, error) {
	return commands.runCommand(ctx, tools.JobCommandRequest{Executable: executable, Args: append([]string(nil), args...)})
}

func (commands *PublicationJobCommands) RunFile(ctx context.Context, request tools.FileCommandRequest) (PublicationCommandResult, error) {
	if request.Input == nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("publication file command input is unavailable")
	}
	return commands.runCommand(ctx, tools.JobCommandRequest{
		Executable: request.Executable,
		Args:       append([]string(nil), request.Args...),
		Input:      request.Input,
	})
}

func (commands *PublicationJobCommands) runCommand(ctx context.Context, request tools.JobCommandRequest) (PublicationCommandResult, error) {
	if commands == nil || commands.run == nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("publication command backend is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, fmt.Errorf("publication command canceled before launch: %w", err)
	}
	stdout, err := NewBoundedOutputCollector(commands.stdoutLimit)
	if err != nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, err
	}
	stderr, err := NewBoundedOutputCollector(commands.stderrLimit)
	if err != nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, err
	}
	request.Stdout = stdout
	request.Stderr = stderr
	backendResult, runErr := commands.run(ctx, request)
	output, _, stdoutOverflow := stdout.Snapshot()
	_, stderrBytes, stderrOverflow := stderr.Snapshot()
	result := PublicationCommandResult{Output: output, QuiescenceEstablished: backendResult.QuiescenceEstablished}
	if stdoutOverflow || stderrOverflow {
		streams := ""
		if stdoutOverflow {
			streams = "stdout"
		}
		if stderrOverflow {
			if streams != "" {
				streams += " and "
			}
			streams += "stderr"
		}
		return result, errors.Join(runErr, fmt.Errorf("publication command %s exceeded configured output limit", streams))
	}
	if runErr != nil {
		return result, fmt.Errorf("publication command failed (stderr: %d bytes): %w", stderrBytes, runErr)
	}
	return result, nil
}

var _ PublicationCommandRunner = (*PublicationJobCommands)(nil)
var _ PublicationFileCommandRunner = (*PublicationJobCommands)(nil)
