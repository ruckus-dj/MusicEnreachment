// Package tools defines the technical boundary for managed external binaries.
package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var Required = []string{"ffmpeg", "ffprobe", "fpcalc"}

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type SystemRunner struct{}

func (SystemRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type Installation struct {
	Name    string
	Path    string
	Version string
}

// Manager owns a persistent directory selected by a future Setup Manager. It
// deliberately does not download, update, or activate binaries: that lifecycle
// is a separate implementation stage. Approved sources live in docs/design.
type Manager struct {
	Directory string
	runner    CommandRunner
}

func NewManager(directory string, runner CommandRunner) (*Manager, error) {
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("tools directory must be an absolute path")
	}
	if runner == nil {
		runner = SystemRunner{}
	}
	return &Manager{Directory: directory, runner: runner}, nil
}

func (m *Manager) EnsureDirectory() error {
	return os.MkdirAll(m.Directory, 0o755)
}

func (m *Manager) Check(ctx context.Context, name string) (Installation, error) {
	if !isRequired(name) {
		return Installation{}, fmt.Errorf("unsupported tool %q", name)
	}
	path := filepath.Join(m.Directory, executableName(name, runtime.GOOS))
	if _, err := os.Stat(path); err != nil {
		return Installation{}, fmt.Errorf("inspect %s: %w", name, err)
	}
	output, err := m.runner.Run(ctx, path, versionArgument)
	if err != nil {
		return Installation{}, fmt.Errorf("check %s version: %w", name, err)
	}
	return Installation{Name: name, Path: path, Version: strings.TrimSpace(string(output))}, nil
}

func isRequired(name string) bool {
	for _, required := range Required {
		if name == required {
			return true
		}
	}
	return false
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
