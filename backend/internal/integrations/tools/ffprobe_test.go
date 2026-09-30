package tools

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeProbeProcess struct {
	stdout     io.ReadCloser
	waitErr    error
	killed     bool
	executable string
	args       []string
}

func (p *fakeProbeProcess) stdoutPipe() (io.ReadCloser, error) { return p.stdout, nil }
func (p *fakeProbeProcess) start() error                       { return nil }
func (p *fakeProbeProcess) wait() error                        { return p.waitErr }
func (p *fakeProbeProcess) kill() error                        { p.killed = true; return nil }

type blockedProbeProcess struct{ ctx context.Context }

func (p *blockedProbeProcess) stdoutPipe() (io.ReadCloser, error) { return p, nil }
func (p *blockedProbeProcess) start() error                       { return nil }
func (p *blockedProbeProcess) wait() error                        { return p.ctx.Err() }
func (p *blockedProbeProcess) kill() error                        { return nil }
func (p *blockedProbeProcess) Read([]byte) (int, error) {
	<-p.ctx.Done()
	return 0, io.EOF
}
func (p *blockedProbeProcess) Close() error { return nil }

// stalledProbeProcess is an ffprobe whose output never ends: its read blocks
// until the test releases it, long past the probe's deadline.
type stalledProbeProcess struct {
	release chan struct{}
	killed  bool
}

func (p *stalledProbeProcess) stdoutPipe() (io.ReadCloser, error) { return p, nil }
func (p *stalledProbeProcess) start() error                       { return nil }
func (p *stalledProbeProcess) wait() error                        { return errors.New("signal: killed") }
func (p *stalledProbeProcess) kill() error                        { p.killed = true; return nil }
func (p *stalledProbeProcess) Close() error                       { return nil }
func (p *stalledProbeProcess) Read([]byte) (int, error) {
	<-p.release
	return 0, io.EOF
}

func newFFProbe(t *testing.T, start probeStarter) *FFProbe {
	t.Helper()
	probe, err := NewFFProbe(filepath.Join(t.TempDir(), "ffprobe"))
	if err != nil {
		t.Fatalf("NewFFProbe: %v", err)
	}
	probe.start = start
	return probe
}

func fixedStarter(process *fakeProbeProcess) probeStarter {
	return func(_ context.Context, executable string, args []string) (probeProcess, error) {
		process.executable, process.args = executable, args
		return process, nil
	}
}

func sourcePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "track.flac")
}

func requireProbeErrorDoesNotLeakPath(t *testing.T, err error, path string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), path) {
		t.Fatalf("error leaked the source path: %v", err)
	}
}

func TestProbeRunsManagedExecutableOnTheProbedPath(t *testing.T) {
	path := sourcePath(t)
	process := &fakeProbeProcess{stdout: io.NopCloser(strings.NewReader(`{"streams":[{"codec_type":"audio"}]}`))}
	probe := newFFProbe(t, fixedStarter(process))

	hasAudio, err := probe.Probe(context.Background(), path)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !hasAudio {
		t.Fatal("Probe did not report the audio stream")
	}
	if process.executable != probe.executable {
		t.Fatalf("executable = %q, want the managed path %q", process.executable, probe.executable)
	}
	if len(process.args) == 0 || process.args[len(process.args)-1] != path {
		t.Fatalf("args = %q, want the probed path last", process.args)
	}
	if !slices.Contains(process.args, "json") {
		t.Fatalf("args = %q, want a JSON response format", process.args)
	}
}

func TestProbeStreamResults(t *testing.T) {
	cases := []struct {
		name     string
		output   string
		hasAudio bool
	}{
		{"audio stream", `{"streams":[{"codec_type":"audio"}]}`, true},
		{"audio stream among video", `{"streams":[{"codec_type":"video"},{"codec_type":"audio"}]}`, true},
		{"video only", `{"streams":[{"codec_type":"video"}]}`, false},
		{"empty streams", `{"streams":[]}`, false},
		{"streams absent", `{}`, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			process := &fakeProbeProcess{stdout: io.NopCloser(strings.NewReader(test.output))}
			hasAudio, err := newFFProbe(t, fixedStarter(process)).Probe(context.Background(), sourcePath(t))
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if hasAudio != test.hasAudio {
				t.Fatalf("hasAudio = %t, want %t", hasAudio, test.hasAudio)
			}
		})
	}
}

func TestProbeRejectsMalformedOutput(t *testing.T) {
	path := sourcePath(t)
	process := &fakeProbeProcess{stdout: io.NopCloser(strings.NewReader("ffprobe: this is not JSON"))}

	hasAudio, err := newFFProbe(t, fixedStarter(process)).Probe(context.Background(), path)
	requireProbeErrorDoesNotLeakPath(t, err, path)
	if hasAudio {
		t.Fatal("malformed output reported audio")
	}
}

func TestProbeReportsCommandFailure(t *testing.T) {
	path := sourcePath(t)
	cause := errors.New("exit status 1")
	process := &fakeProbeProcess{stdout: io.NopCloser(strings.NewReader("")), waitErr: cause}

	hasAudio, err := newFFProbe(t, fixedStarter(process)).Probe(context.Background(), path)
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want %v", err, cause)
	}
	if hasAudio {
		t.Fatal("failed ffprobe reported audio")
	}
	requireProbeErrorDoesNotLeakPath(t, err, path)
}

func TestProbeKillsProcessOnOversizedOutput(t *testing.T) {
	path := sourcePath(t)
	oversized := bytes.Repeat([]byte("x"), maxProbeOutputBytes+1)
	process := &fakeProbeProcess{stdout: io.NopCloser(bytes.NewReader(oversized))}

	hasAudio, err := newFFProbe(t, fixedStarter(process)).Probe(context.Background(), path)
	requireProbeErrorDoesNotLeakPath(t, err, path)
	if hasAudio {
		t.Fatal("oversized output reported audio")
	}
	if !process.killed {
		t.Fatal("oversized output did not kill the process")
	}
}

func TestProbeReportsDeadlineAndCancellation(t *testing.T) {
	probe := newFFProbe(t, func(ctx context.Context, _ string, _ []string) (probeProcess, error) {
		return &blockedProbeProcess{ctx: ctx}, nil
	})

	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Minute))
	defer cancelDeadline()
	if _, err := probe.Probe(deadline, sourcePath(t)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v, want %v", err, context.DeadlineExceeded)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := probe.Probe(cancelled, sourcePath(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v, want %v", err, context.Canceled)
	}
}

// A deadline is a hard bound: an output that never closes must not keep the
// probe alive past it.
func TestProbeReturnsAtItsDeadlineWhenOutputNeverEnds(t *testing.T) {
	process := &stalledProbeProcess{release: make(chan struct{})}
	t.Cleanup(func() { close(process.release) })
	probe := newFFProbe(t, func(context.Context, string, []string) (probeProcess, error) {
		return process, nil
	})
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Minute))
	defer cancel()

	hasAudio, err := probe.Probe(expired, sourcePath(t))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want %v", err, context.DeadlineExceeded)
	}
	if hasAudio {
		t.Fatal("a probe that never finished reported audio")
	}
	if !process.killed {
		t.Fatal("the deadline did not stop the process")
	}
}

func TestProbeFailsWhenManagedExecutableIsMissing(t *testing.T) {
	path := sourcePath(t)
	probe, err := NewFFProbe(filepath.Join(t.TempDir(), "missing-ffprobe"))
	if err != nil {
		t.Fatalf("NewFFProbe: %v", err)
	}

	hasAudio, err := probe.Probe(context.Background(), path)
	requireProbeErrorDoesNotLeakPath(t, err, path)
	if hasAudio {
		t.Fatal("missing executable reported audio")
	}
}

func TestNewFFProbeRejectsRelativePath(t *testing.T) {
	if _, err := NewFFProbe("ffprobe"); err == nil {
		t.Fatal("NewFFProbe accepted a relative path")
	}
}

func TestReadBoundedStopsAtLimit(t *testing.T) {
	exact := readBounded(strings.NewReader(strings.Repeat("x", 16)), 16)
	if exact.overflow {
		t.Fatal("output of exactly the limit was reported as overflow")
	}
	if len(exact.data) != 16 {
		t.Fatalf("data = %d bytes, want 16", len(exact.data))
	}

	over := readBounded(strings.NewReader(strings.Repeat("x", 17)), 16)
	if !over.overflow {
		t.Fatal("output past the limit was not reported as overflow")
	}
	if len(over.data) != 16 {
		t.Fatalf("data = %d bytes, want 16", len(over.data))
	}
}
