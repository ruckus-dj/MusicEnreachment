package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"io"
	"os"
	"os/exec"
)

// maxProbeStderrBytes caps the diagnostic stream a technical probe reads. The
// short scan probe discards stderr entirely; the technical request bounds it so
// a chatty child can neither deadlock on a full pipe nor flood memory. The
// captured diagnostics are never returned to a caller.
const maxProbeStderrBytes = 64 << 10

// ProbeTechnicalFile performs the bounded structural probe against an already
// opened source descriptor. The descriptor is never converted to a pathname.
func (p *FFProbe) ProbeTechnicalFile(ctx context.Context, file sourcefs.RegularFile) ([]byte, error) {
	return p.probeTechnicalFile(ctx, file, true)
}

// ProbeTechnicalFileAnyStreams returns the same bounded technical JSON while
// accepting media with no audio stream.
func (p *FFProbe) ProbeTechnicalFileAnyStreams(ctx context.Context, file sourcefs.RegularFile) ([]byte, error) {
	return p.probeTechnicalFile(ctx, file, false)
}

// ProbeMediaFile is the full technical media probe; it preserves ffprobe's raw
// JSON so callers can retain tags and provenance without a second invocation.
func (p *FFProbe) ProbeMediaFile(ctx context.Context, file sourcefs.RegularFile) ([]byte, error) {
	return p.ProbeTechnicalFileAnyStreams(ctx, file)
}

func (p *FFProbe) probeTechnicalFile(ctx context.Context, file sourcefs.RegularFile, requireAudio bool) ([]byte, error) {
	if file == nil {
		return nil, errors.New("ffprobe source file is required")
	}
	var output []byte
	err := file.Borrow(ctx, func(handle *os.File) error {
		var err error
		output, err = p.runFileTechnical(ctx, handle, technicalFileArguments())
		if err != nil {
			return err
		}
		if err := validateTechnicalResponse(output, requireAudio); err != nil {
			output = nil
			return err
		}
		return nil
	})
	return output, err
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

// validateTechnicalResponse enforces the mandatory structure only: the reply
// must be a JSON object with a format object and a streams array of stream
// objects that each carry a string codec_type. Audio is mandatory by default;
// full-media probes can opt out. Numeric normalization, tags and unknown values
// are left to the typed parsing stage.
func validateTechnicalResponse(output []byte, requireAudio ...bool) error {
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
	needsAudio := len(requireAudio) == 0 || requireAudio[0]
	if needsAudio && !hasAudio {
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
