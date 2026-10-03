package tools

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
)

// FileTransportEvidence is the bounded result of verifying ffprobe's fd
// protocol and its ability to seek on a borrowed source file. Packet JSON is
// retained verbatim so callers can compare hashes with a trusted fixture.
type FileTransportEvidence struct {
	Protocols []string `json:"protocols"`
	Packets   []byte   `json:"packets"`
}

// CheckFileTransport verifies that this managed ffprobe supports descriptor
// input and backward seeking, using only a generated temporary WAV witness.
// Source scanning and analysis should wire this capability in a later stage;
// this method deliberately has no source-file input and does not stage sources.
// Platform acceptance evidence remains pending until this API is wired and run
// by the approved native-platform CI job.
func (p *FFProbe) CheckFileTransport(ctx context.Context) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "melotrove-ffprobe-transport-")
	if err != nil {
		return fmt.Errorf("create ffprobe transport witness directory: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove ffprobe transport witness directory: %w", cleanupErr))
		}
	}()

	path := filepath.Join(directory, "seek-witness.wav")
	if err := writeSeekWitness(ctx, path); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open ffprobe transport witness: %w", err)
	}
	witness := newTransportWitnessFile(file)
	defer func() {
		if closeErr := witness.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close ffprobe transport witness: %w", closeErr))
		}
	}()

	evidence, err := p.ProbeFileTransportCapabilities(ctx, witness)
	if err != nil {
		return fmt.Errorf("check ffprobe descriptor transport: %w", err)
	}
	baseline, err := p.probePacketSeekWitness(ctx, path)
	if err != nil {
		return fmt.Errorf("check ffprobe witness pathname baseline: %w", err)
	}
	if !bytes.Equal(baseline, evidence.Packets) {
		return errors.New("ffprobe descriptor seek differs from generated witness baseline")
	}
	return nil
}

const seekWitnessSampleRate = 48000

// writeSeekWitness creates ten seconds of deterministic mono PCM. At this rate
// the payload is substantially larger than ffprobe's input buffering, allowing
// a meaningful seek to 8 seconds followed by a seek back to zero.
func writeSeekWitness(ctx context.Context, path string) error {
	const durationSeconds = 10
	const channels = 1
	const bitsPerSample = 16
	dataSize := seekWitnessSampleRate * durationSeconds * channels * bitsPerSample / 8
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create ffprobe transport witness: %w", err)
	}
	writeErr := func() error {
		var header [44]byte
		copy(header[0:4], "RIFF")
		binary.LittleEndian.PutUint32(header[4:8], uint32(36+dataSize))
		copy(header[8:12], "WAVE")
		copy(header[12:16], "fmt ")
		binary.LittleEndian.PutUint32(header[16:20], 16)
		binary.LittleEndian.PutUint16(header[20:22], 1)
		binary.LittleEndian.PutUint16(header[22:24], channels)
		binary.LittleEndian.PutUint32(header[24:28], seekWitnessSampleRate)
		binary.LittleEndian.PutUint32(header[28:32], seekWitnessSampleRate*channels*bitsPerSample/8)
		binary.LittleEndian.PutUint16(header[32:34], channels*bitsPerSample/8)
		binary.LittleEndian.PutUint16(header[34:36], bitsPerSample)
		copy(header[36:40], "data")
		binary.LittleEndian.PutUint32(header[40:44], uint32(dataSize))
		if _, err := file.Write(header[:]); err != nil {
			return fmt.Errorf("write ffprobe transport witness header: %w", err)
		}
		const sampleCount = seekWitnessSampleRate * durationSeconds
		var samples [4096]byte
		for start := 0; start < sampleCount; {
			if err := ctx.Err(); err != nil {
				return err
			}
			count := min(len(samples)/2, sampleCount-start)
			for offset := 0; offset < count; offset++ {
				sample := int16(int32((uint32(start+offset)*7919)%60001) - 30000)
				binary.LittleEndian.PutUint16(samples[offset*2:], uint16(sample))
			}
			if _, err := file.Write(samples[:count*2]); err != nil {
				return fmt.Errorf("write ffprobe transport witness samples: %w", err)
			}
			start += count
		}
		return nil
	}()
	if closeErr := file.Close(); closeErr != nil {
		writeErr = errors.Join(writeErr, fmt.Errorf("close generated ffprobe transport witness: %w", closeErr))
	}
	if writeErr != nil {
		return writeErr
	}
	return nil
}

// transportWitnessFile is a private regular-file loan implementation for the
// generated trusted witness. It intentionally does not expose sourcefs's unsafe
// constructor and permits only one synchronous descriptor borrower.
type transportWitnessFile struct {
	mu     sync.Mutex
	cond   *sync.Cond
	file   *os.File
	closed bool
	active bool
}

func newTransportWitnessFile(file *os.File) *transportWitnessFile {
	witness := &transportWitnessFile{file: file}
	witness.cond = sync.NewCond(&witness.mu)
	return witness
}

func (f *transportWitnessFile) Stat(context.Context) (os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, os.ErrClosed
	}
	return f.file.Stat()
}

func (f *transportWitnessFile) Borrow(ctx context.Context, callback func(*os.File) error) error {
	f.mu.Lock()
	if f.closed || f.active {
		f.mu.Unlock()
		return os.ErrClosed
	}
	f.active = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active = false
		f.cond.Broadcast()
		f.mu.Unlock()
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := f.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return callback(f.file)
}

func (f *transportWitnessFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for f.active {
		f.cond.Wait()
	}
	if f.closed {
		return nil
	}
	f.closed = true
	return f.file.Close()
}

type fileTechnicalStarter func(context.Context, string, []string, *os.File) (technicalProcess, error)

// ProbeTechnicalFile runs the same structural technical probe as
// probePacketSeekWitness runs the backward packet-seek witness against a
// generated test or capability witness pathname. It is never used for sources.
func (p *FFProbe) probePacketSeekWitness(ctx context.Context, absolutePath string) ([]byte, error) {
	if !filepath.IsAbs(absolutePath) {
		return nil, errors.New("source path must be an absolute path")
	}
	output, err := p.runTechnical(ctx, packetSeekPathArguments(absolutePath), func(ctx context.Context, executable string, args []string) (technicalProcess, error) {
		return p.technical(ctx, executable, args)
	})
	if err != nil {
		return nil, err
	}
	if err := validatePacketSeek(output); err != nil {
		return nil, err
	}
	return output, nil
}

// ProbePacketSeekFile runs the same backward packet-seek request through a
// borrowed descriptor. Its subprocess lifecycle is contained within Borrow.
func (p *FFProbe) ProbePacketSeekFile(ctx context.Context, file sourcefs.RegularFile) ([]byte, error) {
	if file == nil {
		return nil, errors.New("ffprobe source file is required")
	}
	var output []byte
	err := file.Borrow(ctx, func(handle *os.File) error {
		var err error
		output, err = p.runFileTechnical(ctx, handle, packetSeekArguments())
		if err != nil {
			return err
		}
		return validatePacketSeek(output)
	})
	return output, err
}

// ProbeFileTransportCapabilities verifies that the managed ffprobe advertises
// the fd protocol and can perform a non-linear packet seek on witness. The
// caller should provide a trusted seek-capable fixture, not an arbitrary source
// selected during traversal. Both child invocations finish before Borrow
// returns.
func (p *FFProbe) ProbeFileTransportCapabilities(ctx context.Context, witness sourcefs.RegularFile) (FileTransportEvidence, error) {
	if witness == nil {
		return FileTransportEvidence{}, errors.New("ffprobe witness file is required")
	}
	var evidence FileTransportEvidence
	err := witness.Borrow(ctx, func(handle *os.File) error {
		protocols, err := p.runFileTechnical(ctx, handle, []string{"-protocols"})
		if err != nil {
			return fmt.Errorf("list ffprobe protocols: %w", err)
		}
		evidence.Protocols = parseInputProtocols(protocols)
		if !containsString(evidence.Protocols, "fd") {
			return errors.New("ffprobe does not advertise the fd input protocol")
		}
		return nil
	})
	if err != nil {
		return FileTransportEvidence{}, err
	}
	err = witness.Borrow(ctx, func(handle *os.File) error {
		var err error
		evidence.Packets, err = p.runFileTechnical(ctx, handle, packetSeekArguments())
		if err != nil {
			return fmt.Errorf("seek ffprobe witness: %w", err)
		}
		if err := validatePacketSeek(evidence.Packets); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return FileTransportEvidence{}, err
	}
	return evidence, nil
}

func (p *FFProbe) runFileTechnical(parent context.Context, handle *os.File, args []string) ([]byte, error) {
	return p.runTechnical(parent, args, func(ctx context.Context, executable string, args []string) (technicalProcess, error) {
		return p.fileTech(ctx, executable, args, handle)
	})
}

type technicalProcessStarter func(context.Context, string, []string) (technicalProcess, error)

func (p *FFProbe) runTechnical(parent context.Context, args []string, start technicalProcessStarter) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	process, err := start(ctx, p.executable, args)
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
		if out.overflow {
			return nil, stopTechnicalProcess(process, stdout, stderr, fmt.Errorf("ffprobe stdout exceeded %d bytes", maxProbeOutputBytes))
		}
		if diagnostics.overflow {
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
	return out.data, nil
}

func technicalFileArguments() []string {
	return []string{"-v", "error", "-protocol_whitelist", "fd", "-fd", fileTransportDescriptor(), "-show_format", "-show_streams", "-of", "json", "fd:"}
}

func packetSeekArguments() []string {
	return []string{"-v", "error", "-protocol_whitelist", "fd", "-fd", fileTransportDescriptor(), "-read_intervals", "8%+1,0%+1", "-show_packets", "-show_entries", "packet=pts_time,data_hash", "-show_data_hash", "sha256", "-of", "json", "fd:"}
}

func packetSeekPathArguments(path string) []string {
	args := packetSeekArguments()
	args[3] = "file"
	args[len(args)-1] = path
	return args
}

func parseInputProtocols(output []byte) []string {
	text := strings.ReplaceAll(string(output), "\r\n", "\n")
	section := false
	var result []string
	for _, line := range strings.Split(text, "\n") {
		switch strings.TrimSpace(line) {
		case "Input:":
			section = true
			continue
		case "Output:":
			section = false
			continue
		}
		if section && strings.TrimSpace(line) != "" {
			result = append(result, strings.TrimSpace(line))
		}
	}
	return result
}

func validatePacketSeek(output []byte) error {
	var response struct {
		Packets []struct {
			PTS  string `json:"pts_time"`
			Hash string `json:"data_hash"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return fmt.Errorf("ffprobe returned malformed packet seek output: %w", err)
	}
	if len(response.Packets) < 2 {
		return errors.New("ffprobe witness seek returned fewer than two packets")
	}
	for _, packet := range response.Packets {
		if packet.PTS == "" || packet.Hash == "" {
			return errors.New("ffprobe witness seek returned a packet without timestamp or hash")
		}
		if !strings.HasPrefix(packet.Hash, "SHA256:") {
			return errors.New("ffprobe witness seek returned a packet without a SHA-256 hash")
		}
	}
	previous, err := strconv.ParseFloat(response.Packets[0].PTS, 64)
	if err != nil {
		return errors.New("ffprobe witness seek returned an invalid packet timestamp")
	}
	sawBackwardSeek := false
	for _, packet := range response.Packets[1:] {
		current, parseErr := strconv.ParseFloat(packet.PTS, 64)
		if parseErr != nil {
			return errors.New("ffprobe witness seek returned an invalid packet timestamp")
		}
		if current < previous {
			sawBackwardSeek = true
		}
		previous = current
	}
	if !sawBackwardSeek {
		return errors.New("ffprobe witness seek did not return packets in backward-seek order")
	}
	return nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
