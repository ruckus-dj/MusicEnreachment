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
)

// maxProbeStderrBytes caps the diagnostic stream a technical probe reads. The
// short scan probe discards stderr entirely; the technical request bounds it so
// a chatty child can neither deadlock on a full pipe nor flood memory. The
// captured diagnostics are never returned to a caller.
const maxProbeStderrBytes = 64 << 10

// ProbeTechnical runs one bounded technical request against the managed ffprobe
// and returns the raw JSON describing the container format and every stream,
// including video and attached-picture streams, which the caller stores
// unchanged. It never filters or normalizes: mandatory structural checks live
// here, while typed parsing of the values is a separate stage.
//
// The stdout stream is bounded while it is read (not after an unbounded
// CombinedOutput), stderr is bounded likewise, and the whole call is bound by
// the probe timeout. Empty, malformed or non-object JSON, a response without a
// streams array, or a response with no audio stream is an error. A command
// failure, an oversized stream, a timeout or a cancellation is an error too.
// No returned error carries the probed path or any ffprobe stderr content.
func (p *FFProbe) ProbeTechnical(ctx context.Context, absoluteServerPath string) ([]byte, error) {
	if !filepath.IsAbs(absoluteServerPath) {
		return nil, fmt.Errorf("source path must be an absolute path")
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	process, err := p.technical(ctx, p.executable, technicalArguments(absoluteServerPath))
	if err != nil {
		return nil, fmt.Errorf("start ffprobe: %w", err)
	}
	stdout, err := process.stdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("read ffprobe output: %w", err)
	}
	stderr, err := process.stderrPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, fmt.Errorf("read ffprobe output: %w", err)
	}
	if err := process.start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start ffprobe: %w", err)
	}

	// Both pipes are drained concurrently so a child that fills either one
	// cannot block waiting for the reader. Each read buffers at most limit+1
	// bytes, so the bound holds during capture, not after it.
	stdoutResult := make(chan boundedRead, 1)
	stderrResult := make(chan boundedRead, 1)
	go func() { stdoutResult <- readBounded(stdout, maxProbeOutputBytes) }()
	go func() { stderrResult <- readBounded(stderr, maxProbeStderrBytes) }()

	var out, diagnostics boundedRead
	for received := 0; received < 2; received++ {
		select {
		case out = <-stdoutResult:
		case diagnostics = <-stderrResult:
		case <-ctx.Done():
			return nil, stopTechnicalProcess(process, stdout, stderr, fmt.Errorf("ffprobe did not finish: %w", ctx.Err()))
		}
		if out.overflow || diagnostics.overflow {
			if out.overflow {
				return nil, stopTechnicalProcess(process, stdout, stderr, fmt.Errorf("ffprobe stdout exceeded %d bytes", maxProbeOutputBytes))
			}
			return nil, stopTechnicalProcess(process, stdout, stderr, fmt.Errorf("ffprobe stderr exceeded %d bytes", maxProbeStderrBytes))
		}
	}
	if out.err != nil {
		return nil, stopTechnicalProcess(process, stdout, stderr, fmt.Errorf("read ffprobe output: %w", out.err))
	}
	if diagnostics.err != nil {
		return nil, stopTechnicalProcess(process, stdout, stderr, fmt.Errorf("read ffprobe output: %w", diagnostics.err))
	}
	if err := process.wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("ffprobe did not finish: %w", ctxErr)
		}
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}
	if err := validateTechnicalResponse(out.data); err != nil {
		return nil, err
	}
	return out.data, nil
}

// stopTechnicalProcess tears a launched process down and returns the error the
// caller chose. A probe that is killed or canceled must not outlive its bound.
func stopTechnicalProcess(process technicalProcess, stdout, stderr io.ReadCloser, cause error) error {
	_ = process.kill()
	_ = stdout.Close()
	_ = stderr.Close()
	_ = process.wait()
	return cause
}

// technicalArguments is the single, fixed request shape for the technical
// probe: quiet mode, format, all streams and a JSON response, with exactly one
// server-controlled filename appended last.
func technicalArguments(absoluteServerPath string) []string {
	return []string{"-v", "error", "-show_format", "-show_streams", "-of", "json", absoluteServerPath}
}

// validateTechnicalResponse enforces the mandatory structure only: the reply
// must be a JSON object with a format object, a streams array of stream
// objects that each carry a string codec_type, and at least one audio stream.
// It deliberately leaves numeric normalization, tag collection and unknown
// values to the typed parsing stage.
func validateTechnicalResponse(output []byte) error {
	if len(bytes.TrimSpace(output)) == 0 {
		return errors.New("ffprobe returned no output")
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(output, &response); err != nil {
		return fmt.Errorf("ffprobe returned malformed output: %w", err)
	}
	if response == nil {
		return errors.New("ffprobe returned a non-object response")
	}
	rawFormat, ok := response["format"]
	if !ok {
		return errors.New("ffprobe response has no format")
	}
	var format map[string]json.RawMessage
	if err := json.Unmarshal(rawFormat, &format); err != nil || format == nil {
		return errors.New("ffprobe response format is not an object")
	}
	rawStreams, ok := response["streams"]
	if !ok {
		return errors.New("ffprobe response has no streams")
	}
	var streams []json.RawMessage
	if err := json.Unmarshal(rawStreams, &streams); err != nil || streams == nil {
		return errors.New("ffprobe response streams are not an array")
	}
	hasAudio := false
	for _, raw := range streams {
		var stream map[string]json.RawMessage
		if err := json.Unmarshal(raw, &stream); err != nil || stream == nil {
			return errors.New("ffprobe response stream is not an object")
		}
		rawCodec, ok := stream["codec_type"]
		if !ok {
			return errors.New("ffprobe response stream has no codec_type")
		}
		// A decoded *string distinguishes JSON null (left nil) from a real
		// string, which unmarshalling into a plain string cannot: JSON null
		// into a string is a silent no-op that would leave "".
		var codec *string
		if err := json.Unmarshal(rawCodec, &codec); err != nil || codec == nil {
			return errors.New("ffprobe response stream codec_type is not a string")
		}
		if *codec == "audio" {
			hasAudio = true
		}
	}
	if !hasAudio {
		return errors.New("ffprobe response has no audio stream")
	}
	return nil
}

// technicalProcess is one launched technical ffprobe. Unlike probeProcess it
// also exposes stderr, which must be read and bounded during the run.
type technicalProcess interface {
	stdoutPipe() (io.ReadCloser, error)
	stderrPipe() (io.ReadCloser, error)
	start() error
	wait() error
	kill() error
}

// technicalStarter launches the managed executable; tests substitute a fake.
type technicalStarter func(context.Context, string, []string) (technicalProcess, error)

type execTechnicalProcess struct{ command *exec.Cmd }

func startTechnicalProcess(ctx context.Context, executable string, args []string) (technicalProcess, error) {
	return &execTechnicalProcess{command: exec.CommandContext(ctx, executable, args...)}, nil
}

func (p *execTechnicalProcess) stdoutPipe() (io.ReadCloser, error) { return p.command.StdoutPipe() }
func (p *execTechnicalProcess) stderrPipe() (io.ReadCloser, error) { return p.command.StderrPipe() }
func (p *execTechnicalProcess) start() error                       { return p.command.Start() }
func (p *execTechnicalProcess) wait() error                        { return p.command.Wait() }

func (p *execTechnicalProcess) kill() error {
	if p.command.Process == nil {
		return nil
	}
	return p.command.Process.Kill()
}
