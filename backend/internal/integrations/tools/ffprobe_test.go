package tools

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func newQuickFFProbe(t *testing.T, process *fakeTechnicalProcess) (*FFProbe, *borrowedFixture) {
	t.Helper()
	probe := newFileProbe(t)
	fixture := newBorrowedFixture(t)
	probe.fileTech = func(ctx context.Context, executable string, args []string, _ *os.File) (technicalProcess, error) {
		process.executable, process.args = executable, args
		return process, nil
	}
	return probe, fixture
}

func TestProbeFileUsesManagedExecutableAndFixedDescriptorArguments(t *testing.T) {
	process := fakeTechnicalProcessFor(`{"streams":[{"codec_type":"audio"}]}`, "")
	probe, fixture := newQuickFFProbe(t, process)
	hasAudio, err := probe.ProbeFile(context.Background(), fixture)
	if err != nil {
		t.Fatalf("ProbeFile: %v", err)
	}
	if !hasAudio {
		t.Fatal("ProbeFile did not report the audio stream")
	}
	if process.executable != probe.executable || !filepath.IsAbs(process.executable) {
		t.Fatalf("executable = %q, want managed absolute path %q", process.executable, probe.executable)
	}
	if !slices.Equal(process.args, quickFileArguments()) {
		t.Fatalf("args = %q, want %q", process.args, quickFileArguments())
	}
	if slices.Contains(process.args, fixture.file.Name()) {
		t.Fatalf("source pathname leaked into argv: %q", process.args)
	}
}

func TestProbeFileStreamResults(t *testing.T) {
	cases := []struct {
		name     string
		output   string
		hasAudio bool
	}{
		{"audio stream", `{"streams":[{"codec_type":"audio"}]}`, true},
		{"audio among video", `{"streams":[{"codec_type":"video"},{"codec_type":"audio"}]}`, true},
		{"video only", `{"streams":[{"codec_type":"video"}]}`, false},
		{"empty streams", `{"streams":[]}`, false},
		{"streams absent", `{}`, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			probe, fixture := newQuickFFProbe(t, fakeTechnicalProcessFor(test.output, ""))
			got, err := probe.ProbeFile(context.Background(), fixture)
			if err != nil {
				t.Fatalf("ProbeFile: %v", err)
			}
			if got != test.hasAudio {
				t.Fatalf("hasAudio = %t, want %t", got, test.hasAudio)
			}
		})
	}
}

func TestProbeFileRejectsMalformedAndOversizedOutput(t *testing.T) {
	for _, test := range []struct {
		name   string
		output io.Reader
	}{
		{"malformed", strings.NewReader("not JSON")},
		{"oversized", bytes.NewReader(bytes.Repeat([]byte("x"), maxProbeOutputBytes+1))},
	} {
		t.Run(test.name, func(t *testing.T) {
			process := &fakeTechnicalProcess{stdout: io.NopCloser(test.output), stderr: io.NopCloser(strings.NewReader(""))}
			probe, fixture := newQuickFFProbe(t, process)
			if got, err := probe.ProbeFile(context.Background(), fixture); err == nil || got {
				t.Fatalf("ProbeFile = (%t, %v), want error and no audio", got, err)
			}
			if test.name == "oversized" && !process.killed {
				t.Fatal("oversized output did not kill ffprobe")
			}
		})
	}
}

func TestProbeFileReportsCommandFailure(t *testing.T) {
	cause := errors.New("exit status 1")
	process := fakeTechnicalProcessFor(`{"streams":[]}`, "")
	process.waitErr = cause
	probe, fixture := newQuickFFProbe(t, process)
	if _, err := probe.ProbeFile(context.Background(), fixture); !errors.Is(err, cause) {
		t.Fatalf("error = %v, want %v", err, cause)
	}
}

func TestNewFFProbeRejectsRelativePath(t *testing.T) {
	if _, err := NewFFProbe("ffprobe"); err == nil {
		t.Fatal("NewFFProbe accepted a relative path")
	}
}

func TestReadBoundedStopsAtLimit(t *testing.T) {
	exact := readBounded(strings.NewReader(strings.Repeat("x", 16)), 16)
	if exact.overflow || len(exact.data) != 16 {
		t.Fatalf("exact limit result = (%d bytes, overflow %t)", len(exact.data), exact.overflow)
	}
	over := readBounded(strings.NewReader(strings.Repeat("x", 17)), 16)
	if !over.overflow || len(over.data) != 16 {
		t.Fatalf("over limit result = (%d bytes, overflow %t)", len(over.data), over.overflow)
	}
}
