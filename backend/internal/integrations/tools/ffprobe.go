package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
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
func (p *FFProbe) ProbeFile(ctx context.Context, file sourcefs.RegularFile) (bool, error) {
	if file == nil {
		return false, errors.New("ffprobe source file is required")
	}
	var hasAudio bool
	err := file.Borrow(ctx, func(handle *os.File) error {
		output, err := p.runFileTechnical(ctx, handle, quickFileArguments())
		if err != nil {
			return err
		}
		hasAudio, err = probeHasAudio(output)
		return err
	})
	return hasAudio, err
}

func quickFileArguments() []string {
	return []string{"-v", "error", "-protocol_whitelist", "fd", "-fd", fileTransportDescriptor(), "-print_format", "json", "-show_entries", "stream=codec_type", "fd:"}
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
