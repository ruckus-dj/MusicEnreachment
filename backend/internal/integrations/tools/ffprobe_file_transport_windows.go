package tools

import (
	"context"
	"os"
	"os/exec"
)

func fileTransportDescriptor() string { return "0" }

func startFileTechnicalProcess(ctx context.Context, executable string, args []string, file *os.File) (technicalProcess, error) {
	command := exec.CommandContext(ctx, executable, args...)
	command.Stdin = file
	return &execTechnicalProcess{command: command}, nil
}
