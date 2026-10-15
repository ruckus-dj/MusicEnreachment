package tools

import (
	"testing"
)

func TestExecutableName(t *testing.T) {
	if got := executableName("ffmpeg", "windows"); got != "ffmpeg.exe" {
		t.Fatalf("Windows executable = %q", got)
	}
	if got := executableName("ffmpeg", "darwin"); got != "ffmpeg" {
		t.Fatalf("Darwin executable = %q", got)
	}
}
