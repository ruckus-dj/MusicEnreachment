package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeTechnicalProcess substitutes one launched technical ffprobe so bounded
// capture, failure and cancellation can be exercised without a real child.
type fakeTechnicalProcess struct {
	stdout     io.ReadCloser
	stderr     io.ReadCloser
	waitErr    error
	killed     bool
	executable string
	args       []string
}

func (p *fakeTechnicalProcess) stdoutPipe() (io.ReadCloser, error) { return p.stdout, nil }
func (p *fakeTechnicalProcess) stderrPipe() (io.ReadCloser, error) { return p.stderr, nil }
func (p *fakeTechnicalProcess) start() error                       { return nil }
func (p *fakeTechnicalProcess) wait() error                        { return p.waitErr }
func (p *fakeTechnicalProcess) kill() error                        { p.killed = true; return nil }

func fakeTechnicalProcessFor(stdout, stderr string) *fakeTechnicalProcess {
	return &fakeTechnicalProcess{
		stdout: io.NopCloser(strings.NewReader(stdout)),
		stderr: io.NopCloser(strings.NewReader(stderr)),
	}
}

// blockedTechnicalProcess keeps stdout open until its context ends, modelling a
// probe that outlives its deadline.
type blockedTechnicalProcess struct{ ctx context.Context }

func (p *blockedTechnicalProcess) stdoutPipe() (io.ReadCloser, error) { return p, nil }
func (p *blockedTechnicalProcess) stderrPipe() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (p *blockedTechnicalProcess) start() error { return nil }
func (p *blockedTechnicalProcess) wait() error  { return p.ctx.Err() }
func (p *blockedTechnicalProcess) kill() error  { return nil }
func (p *blockedTechnicalProcess) Read([]byte) (int, error) {
	<-p.ctx.Done()
	return 0, io.EOF
}
func (p *blockedTechnicalProcess) Close() error { return nil }

func newTechnicalFFProbe(t *testing.T, start technicalStarter) *FFProbe {
	t.Helper()
	probe, err := NewFFProbe(filepath.Join(t.TempDir(), "ffprobe"))
	if err != nil {
		t.Fatalf("NewFFProbe: %v", err)
	}
	probe.technical = start
	return probe
}

func fixedTechnicalStarter(process *fakeTechnicalProcess) technicalStarter {
	return func(_ context.Context, executable string, args []string) (technicalProcess, error) {
		process.executable, process.args = executable, args
		return process, nil
	}
}

const validTechnicalJSON = `{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac"},{"index":1,"codec_type":"video","codec_name":"mjpeg"}]}`

func TestTechnicalProbeUsesExactArgumentsAndAbsoluteExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "an album", "track 01.flac")
	process := fakeTechnicalProcessFor(validTechnicalJSON, "")
	probe := newTechnicalFFProbe(t, fixedTechnicalStarter(process))

	output, err := probe.ProbeTechnical(context.Background(), path)
	if err != nil {
		t.Fatalf("ProbeTechnical: %v", err)
	}
	if string(output) != validTechnicalJSON {
		t.Fatalf("output = %q, want the raw response unchanged", output)
	}
	if !filepath.IsAbs(process.executable) || process.executable != probe.executable {
		t.Fatalf("executable = %q, want the managed absolute path %q", process.executable, probe.executable)
	}
	want := []string{"-v", "error", "-show_format", "-show_streams", "-of", "json", path}
	if !slices.Equal(process.args, want) {
		t.Fatalf("args = %q, want %q", process.args, want)
	}
}

func TestTechnicalProbeKeepsEveryRawStreamIncludingVideo(t *testing.T) {
	process := fakeTechnicalProcessFor(validTechnicalJSON, "")
	output, err := newTechnicalFFProbe(t, fixedTechnicalStarter(process)).ProbeTechnical(context.Background(), sourcePath(t))
	if err != nil {
		t.Fatalf("ProbeTechnical: %v", err)
	}
	if !bytes.Contains(output, []byte(`"codec_type":"video"`)) {
		t.Fatalf("raw output dropped the video stream: %s", output)
	}
}

func TestTechnicalProbeRejectsStructurallyInvalidResponses(t *testing.T) {
	cases := []struct {
		name   string
		output string
	}{
		{"empty", ""},
		{"whitespace", "   \n"},
		{"malformed", "ffprobe: this is not JSON"},
		{"json array", `[{"codec_type":"audio"}]`},
		{"json string", `"audio"`},
		{"json number", `123`},
		{"json null", `null`},
		{"no format", `{"streams":[{"codec_type":"audio"}]}`},
		{"format not an object", `{"format":"flac","streams":[{"codec_type":"audio"}]}`},
		{"format null", `{"format":null,"streams":[{"codec_type":"audio"}]}`},
		{"format array", `{"format":[],"streams":[{"codec_type":"audio"}]}`},
		{"no streams", `{"format":{"format_name":"flac"}}`},
		{"streams not an array", `{"format":{},"streams":{}}`},
		{"streams null", `{"format":{},"streams":null}`},
		{"stream not an object", `{"format":{},"streams":["audio"]}`},
		{"stream null", `{"format":{},"streams":[null]}`},
		{"stream without codec_type", `{"format":{},"streams":[{"codec_name":"flac"}]}`},
		{"codec_type not a string", `{"format":{},"streams":[{"codec_type":1}]}`},
		{"null codec_type beside audio", `{"format":{},"streams":[{"codec_type":"audio"},{"codec_type":null}]}`},
		{"video only", `{"format":{},"streams":[{"codec_type":"video"}]}`},
		{"empty streams", `{"format":{},"streams":[]}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := sourcePath(t)
			process := fakeTechnicalProcessFor(test.output, "")
			output, err := newTechnicalFFProbe(t, fixedTechnicalStarter(process)).ProbeTechnical(context.Background(), path)
			if err == nil {
				t.Fatalf("ProbeTechnical accepted %q", test.output)
			}
			if output != nil {
				t.Fatalf("invalid response returned output: %q", output)
			}
			requireProbeErrorDoesNotLeakPath(t, err, path)
		})
	}
}

func TestTechnicalProbeBoundsStdoutDuringCapture(t *testing.T) {
	path := sourcePath(t)
	process := fakeTechnicalProcessFor(strings.Repeat("x", maxProbeOutputBytes+1), "")
	output, err := newTechnicalFFProbe(t, fixedTechnicalStarter(process)).ProbeTechnical(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "stdout exceeded") {
		t.Fatalf("err = %v, want a stdout-limit error", err)
	}
	if output != nil {
		t.Fatalf("oversized response returned output: %q", output)
	}
	if !process.killed {
		t.Fatal("oversized stdout did not stop the process")
	}
}

func TestTechnicalProbeBoundsStderrDuringCapture(t *testing.T) {
	path := sourcePath(t)
	process := fakeTechnicalProcessFor(validTechnicalJSON, strings.Repeat("e", maxProbeStderrBytes+1))
	output, err := newTechnicalFFProbe(t, fixedTechnicalStarter(process)).ProbeTechnical(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "stderr exceeded") {
		t.Fatalf("err = %v, want a stderr-limit error", err)
	}
	if output != nil {
		t.Fatalf("oversized stderr returned output: %q", output)
	}
	if !process.killed {
		t.Fatal("oversized stderr did not stop the process")
	}
}

func TestTechnicalProbeReportsNonzeroExitWithoutStderr(t *testing.T) {
	path := sourcePath(t)
	cause := errors.New("exit status 3")
	process := fakeTechnicalProcessFor(validTechnicalJSON, "secret diagnostic that must never reach the user")
	process.waitErr = cause

	output, err := newTechnicalFFProbe(t, fixedTechnicalStarter(process)).ProbeTechnical(context.Background(), path)
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want %v", err, cause)
	}
	if output != nil {
		t.Fatalf("failed probe returned output: %q", output)
	}
	if strings.Contains(err.Error(), "secret diagnostic") {
		t.Fatalf("error leaked stderr: %v", err)
	}
	requireProbeErrorDoesNotLeakPath(t, err, path)
}

func TestTechnicalProbeReportsDeadlineAndCancellation(t *testing.T) {
	probe := newTechnicalFFProbe(t, func(ctx context.Context, _ string, _ []string) (technicalProcess, error) {
		return &blockedTechnicalProcess{ctx: ctx}, nil
	})

	expired, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Minute))
	defer cancelDeadline()
	if _, err := probe.ProbeTechnical(expired, sourcePath(t)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v, want %v", err, context.DeadlineExceeded)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := probe.ProbeTechnical(cancelled, sourcePath(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v, want %v", err, context.Canceled)
	}
}

func TestTechnicalProbeRejectsRelativePath(t *testing.T) {
	probe, err := NewFFProbe(filepath.Join(t.TempDir(), "ffprobe"))
	if err != nil {
		t.Fatalf("NewFFProbe: %v", err)
	}
	if _, err := probe.ProbeTechnical(context.Background(), "relative.flac"); err == nil {
		t.Fatal("ProbeTechnical accepted a relative source path")
	}
}

// buildFFProbeHelper compiles the real subprocess helper for one test.
func buildFFProbeHelper(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "ffprobehelper")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./testdata/ffprobehelper")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build ffprobe helper: %v", err)
	}
	return binary
}

func TestTechnicalProbeBoundsRealSubprocessOutput(t *testing.T) {
	helper := buildFFProbeHelper(t)
	path := filepath.Join(t.TempDir(), "track with spaces.flac")

	cases := []struct {
		mode      string
		wantError string
	}{
		{"valid", ""},
		{"oversize-stdout", "ffprobe stdout exceeded"},
		{"oversize-stderr", "ffprobe stderr exceeded"},
		{"nonzero", "ffprobe failed"},
	}
	for _, test := range cases {
		t.Run(test.mode, func(t *testing.T) {
			t.Setenv("FFPROBE_HELPER_MODE", test.mode)
			probe, err := NewFFProbe(helper)
			if err != nil {
				t.Fatalf("NewFFProbe: %v", err)
			}
			output, err := probe.ProbeTechnical(context.Background(), path)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("mode %s error = %v, want %q", test.mode, err, test.wantError)
				}
				if strings.Contains(err.Error(), "secret diagnostic") {
					t.Fatalf("error leaked real stderr: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ProbeTechnical: %v", err)
			}
			if !bytes.Contains(output, []byte(`"codec_type":"audio"`)) {
				t.Fatalf("valid response lost its audio stream: %s", output)
			}
		})
	}
}

func TestTechnicalProbeDeliversSpacedFilenameAsOneArgument(t *testing.T) {
	helper := buildFFProbeHelper(t)
	argvFile := filepath.Join(t.TempDir(), "argv.json")
	t.Setenv("FFPROBE_HELPER_MODE", "valid")
	t.Setenv("FFPROBE_HELPER_ARGV_FILE", argvFile)
	path := filepath.Join(t.TempDir(), "an album", "track 01.flac")

	probe, err := NewFFProbe(helper)
	if err != nil {
		t.Fatalf("NewFFProbe: %v", err)
	}
	if _, err := probe.ProbeTechnical(context.Background(), path); err != nil {
		t.Fatalf("ProbeTechnical: %v", err)
	}
	encoded, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read helper argv: %v", err)
	}
	var got []string
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode helper argv: %v", err)
	}
	want := []string{helper, "-v", "error", "-show_format", "-show_streams", "-of", "json", path}
	if !slices.Equal(got, want) {
		t.Fatalf("child argv = %q, want %q", got, want)
	}
}

func TestTechnicalProbeStopsRealSubprocessOnCancellation(t *testing.T) {
	helper := buildFFProbeHelper(t)

	// The test owns the barrier: the helper dials this loopback listener before
	// doing anything else, so the readiness byte is the exact event the action
	// waits on. No polling or sleeping is involved.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close readiness listener: %v", err)
		}
	}()
	t.Setenv("FFPROBE_HELPER_MODE", "block-until-killed")
	t.Setenv("FFPROBE_HELPER_READY_ADDR", listener.Addr().String())

	probe, err := NewFFProbe(helper)
	if err != nil {
		t.Fatalf("NewFFProbe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := sourcePath(t)

	done := make(chan error, 1)
	go func() {
		_, err := probe.ProbeTechnical(ctx, path)
		done <- err
	}()

	tcp, ok := listener.(*net.TCPListener)
	if !ok {
		t.Fatalf("listener is %T, want *net.TCPListener", listener)
	}
	if err := tcp.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatalf("set listen deadline: %v", err)
	}
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("helper never connected: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close readiness connection: %v", err)
		}
	}()
	if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	readiness := make([]byte, 1)
	if _, err := io.ReadFull(conn, readiness); err != nil {
		t.Fatalf("read helper readiness: %v", err)
	}
	if readiness[0] != 'R' {
		t.Fatalf("readiness byte = %q, want 'R'", readiness)
	}

	// The exact barrier is now armed: the child is running and blocked.
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("probe did not return after cancellation")
	}
}
