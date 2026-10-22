//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package service

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
)

func TestPublicationJobCommandsFileInputHelper(t *testing.T) {
	if os.Getenv("GO_WANT_PUBLICATION_FILE_INPUT_HELPER") != "1" {
		return
	}
	file := os.NewFile(3, "publication-file-input")
	if file == nil {
		os.Exit(11)
	}
	_, _ = io.Copy(os.Stdout, file)
	os.Exit(0)
}

func TestPublicationJobCommandsRunFileUsesUnixFileDescriptor(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	if _, err := input.WriteString("publication-file-sentinel"); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	request, err := tools.BuildPublicationFileCommandRequest("/unused", "mp3", input)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	request.Executable = executable
	request.Args = []string{"-test.run=^TestPublicationJobCommandsFileInputHelper$"}
	t.Setenv("GO_WANT_PUBLICATION_FILE_INPUT_HELPER", "1")
	commands, err := NewPublicationJobCommands(1024, 1024)
	if err != nil {
		t.Fatal(err)
	}
	result, err := commands.RunFile(context.Background(), request)
	if err != nil || !result.QuiescenceEstablished || string(result.Output) != "publication-file-sentinel" {
		t.Fatalf("RunFile() = (%+v, %v), want child output from descriptor 3", result, err)
	}
	if _, err := input.Seek(0, io.SeekCurrent); err != nil {
		t.Fatalf("borrowed file input was closed: %v", err)
	}
	if strings.Contains(string(result.Output), "Bad file descriptor") {
		t.Fatalf("child could not read file descriptor: %q", result.Output)
	}
}
