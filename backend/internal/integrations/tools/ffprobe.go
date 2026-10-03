package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	// probeTimeout bounds one ffprobe invocation so a hung probe cannot stall a scan.
	probeTimeout = 120 * time.Second
	// maxProbeOutputBytes caps the JSON response a probe holds in memory.
	maxProbeOutputBytes = 1 << 20
)

// FFProbe confirms the audio stream of a source file with a managed ffprobe
// executable. The caller supplies the absolute path of the active managed
// installation; ffprobe is never resolved from PATH.
type FFProbe struct {
	executable string
	timeout    time.Duration
	start      probeStarter
	technical  technicalStarter
	fileTech   fileTechnicalStarter
}

func NewFFProbe(executable string) (*FFProbe, error) {
	if !filepath.IsAbs(executable) {
		return nil, fmt.Errorf("ffprobe executable must be an absolute path")
	}
	return &FFProbe{
		executable: executable,
		timeout:    probeTimeout,
		start:      startExecProcess,
		technical:  startTechnicalProcess,
		fileTech:   startFileTechnicalProcess,
	}, nil
}

// Probe reports whether the file at absoluteServerPath carries at least one
// audio stream. A successful ffprobe response without one is a valid "no
// audio". A command failure, a timeout or cancellation, a malformed response
// and a response past the byte limit are errors, which the caller maps to the
// probe_error state. The returned error carries neither the probed path nor
// ffprobe stderr.
//
// The timeout is a hard bound: a probe that outlives it is stopped, its output
// pipe released and the process reaped, so a read that never ends cannot
// outlast the probe itself.
func (p *FFProbe) Probe(ctx context.Context, absoluteServerPath string) (bool, error) {
	if !filepath.IsAbs(absoluteServerPath) {
		return false, fmt.Errorf("source path must be an absolute path")
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	process, err := p.start(ctx, p.executable, probeArguments(absoluteServerPath))
	if err != nil {
		return false, fmt.Errorf("start ffprobe: %w", err)
	}
	stdout, err := process.stdoutPipe()
	if err != nil {
		return false, fmt.Errorf("read ffprobe output: %w", err)
	}
	if err := process.start(); err != nil {
		return false, fmt.Errorf("start ffprobe: %w", err)
	}

	output := make(chan boundedRead, 1)
	go func() { output <- readBounded(stdout, maxProbeOutputBytes) }()
	var result boundedRead
	select {
	case result = <-output:
	case <-ctx.Done():
		// ffprobe outlived its deadline, or the caller canceled. Waiting for the
		// pipe to close would make the probe outlast its own timeout whenever the
		// process keeps its output open, so stop it here instead.
		_ = process.kill()
		_ = stdout.Close()
		_ = process.wait()
		return false, fmt.Errorf("ffprobe did not finish: %w", ctx.Err())
	}
	if result.overflow {
		_ = process.kill()
		_ = process.wait()
		return false, fmt.Errorf("ffprobe output exceeded %d bytes", maxProbeOutputBytes)
	}
	if result.err != nil {
		_ = process.kill()
		_ = process.wait()
		return false, fmt.Errorf("read ffprobe output: %w", result.err)
	}
	if err := process.wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, fmt.Errorf("ffprobe did not finish: %w", ctxErr)
		}
		return false, fmt.Errorf("ffprobe failed: %w", err)
	}
	return probeHasAudio(result.data)
}

func probeArguments(absoluteServerPath string) []string {
	return []string{"-v", "error", "-print_format", "json", "-show_entries", "stream=codec_type", absoluteServerPath}
}

func probeHasAudio(output []byte) (bool, error) {
	var response struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return false, fmt.Errorf("ffprobe returned malformed output: %w", err)
	}
	for _, stream := range response.Streams {
		if stream.CodecType == "audio" {
			return true, nil
		}
	}
	return false, nil
}

type boundedRead struct {
	data     []byte
	overflow bool
	err      error
}

// readBounded buffers at most limit+1 bytes so it can tell a response of
// exactly limit bytes from an oversized one.
func readBounded(reader io.Reader, limit int64) boundedRead {
	buffer := bytes.NewBuffer(make([]byte, 0, 4096))
	read, err := io.CopyN(buffer, reader, limit+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return boundedRead{err: err}
	}
	if read > limit {
		return boundedRead{data: buffer.Bytes()[:limit], overflow: true}
	}
	return boundedRead{data: buffer.Bytes()}
}

// probeStarter launches the managed executable; tests substitute a fake.
type probeStarter func(context.Context, string, []string) (probeProcess, error)

// probeProcess is one launched ffprobe process. Tests substitute a fake to
// exercise bounded output, failures and cancellation without spawning ffprobe.
type probeProcess interface {
	stdoutPipe() (io.ReadCloser, error)
	start() error
	wait() error
	kill() error
}

type execProcess struct{ command *exec.Cmd }

// startExecProcess leaves stderr unset so the child writes it to the null
// device: ffprobe diagnostics are never reported and must not be buffered.
func startExecProcess(ctx context.Context, executable string, args []string) (probeProcess, error) {
	return &execProcess{command: exec.CommandContext(ctx, executable, args...)}, nil
}

func (p *execProcess) stdoutPipe() (io.ReadCloser, error) { return p.command.StdoutPipe() }

func (p *execProcess) start() error { return p.command.Start() }

func (p *execProcess) wait() error { return p.command.Wait() }

func (p *execProcess) kill() error {
	if p.command.Process == nil {
		return nil
	}
	return p.command.Process.Kill()
}
