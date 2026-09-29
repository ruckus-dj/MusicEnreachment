package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type fakeRunner struct {
	t    *testing.T
	path string
}

func (r fakeRunner) Run(_ context.Context, path string, args ...string) ([]byte, error) {
	r.t.Helper()
	if path != r.path || len(args) != 1 || args[0] != versionArgument {
		r.t.Fatalf("unexpected command: %q %q", path, args)
	}
	return []byte("ffmpeg version test\n"), nil
}

func TestCheckUsesManagedBinaryAndVersionFlag(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, executableName("ffmpeg", runtime.GOOS))
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(directory, fakeRunner{t: t, path: path})
	if err != nil {
		t.Fatal(err)
	}
	installation, err := manager.Check(context.Background(), "ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	if installation.Version != "ffmpeg version test" {
		t.Fatalf("version = %q", installation.Version)
	}
}

func TestNewManagerRejectsRelativeDirectory(t *testing.T) {
	if _, err := NewManager("tools", fakeRunner{}); err == nil {
		t.Fatal("NewManager accepted a relative path")
	}
}

func TestExecutableName(t *testing.T) {
	if got := executableName("ffmpeg", "windows"); got != "ffmpeg.exe" {
		t.Fatalf("Windows executable = %q", got)
	}
	if got := executableName("ffmpeg", "darwin"); got != "ffmpeg" {
		t.Fatalf("Darwin executable = %q", got)
	}
}
