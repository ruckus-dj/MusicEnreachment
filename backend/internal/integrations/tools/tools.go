// Package tools defines the technical boundary for managed external binaries.
package tools

import (
	"context"
	"os/exec"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type SystemRunner struct{}

func (SystemRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// versionArgument queries the version of a managed executable. It must stay the
// single-dash form: Chromaprint's fpcalc rejects "--version", and the macOS
// ffmpeg builds print their banner and then exit non-zero for the double-dash
// spelling.
const versionArgument = "-version"

func executableName(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}
