package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	fpcalcTimeout        = probeTimeout
	maxFPCalcOutputBytes = 1 << 20
	maxFPCalcStderrBytes = 64 << 10
)

// FPCalc invokes the pinned managed fpcalc executable, never PATH.
type FPCalc struct {
	executable string
	timeout    time.Duration
	start      fpcalcStarter
}

func NewFPCalc(executable string) (*FPCalc, error) {
	if !filepath.IsAbs(executable) {
		return nil, errors.New("fpcalc executable must be an absolute path")
	}
	return &FPCalc{executable: executable, timeout: fpcalcTimeout, start: startFPCalcProcess}, nil
}

type FPCalcResult struct {
	Duration    float64
	Fingerprint string
	Algorithm   uint8
}

// Fingerprint analyzes the server-resolved source pathname. Callers must only
// pass an absolute path resolved from the pinned source root, never client input.
func (p *FPCalc) Fingerprint(ctx context.Context, absoluteServerPath string) (FPCalcResult, error) {
	if !filepath.IsAbs(absoluteServerPath) {
		return FPCalcResult{}, errors.New("fpcalc source path must be absolute")
	}
	args := []string{"-json", "--", absoluteServerPath}
	output, err := p.run(ctx, args)
	if err != nil {
		return FPCalcResult{}, err
	}
	result, err := parseFPCalcJSON(output)
	if err != nil {
		return FPCalcResult{}, err
	}
	return result, nil
}

// Version reports the literal version token and raw banner returned by fpcalc.
func (p *FPCalc) Version(ctx context.Context) (FPCalcVersion, error) {
	output, err := p.run(ctx, []string{"-version"})
	if err != nil {
		return FPCalcVersion{}, err
	}
	return parseFPCalcVersion(string(output))
}

type FPCalcVersion struct {
	Version         string
	Banner          string
	FFmpegLibraries map[string]string
}

var fpcalcVersionPattern = regexp.MustCompile(`(?i)^\s*fpcalc\s+version\s+([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)\b`)
var ffmpegLibraryPattern = regexp.MustCompile(`(?i)\b(avcodec|avformat|avutil|swresample|swscale)\s+([0-9]+(?:\.[0-9]+)+)`)
var ffmpegBuildIDPattern = regexp.MustCompile(`(?i)\b(lavc|lavf|swr|sws)([0-9]+(?:\.[0-9]+)+)`)

func parseFPCalcVersion(banner string) (FPCalcVersion, error) {
	trimmed := strings.TrimSpace(banner)
	match := fpcalcVersionPattern.FindStringSubmatch(trimmed)
	if len(match) != 2 {
		return FPCalcVersion{}, errors.New("fpcalc version output has no semantic version")
	}
	version := FPCalcVersion{Version: match[1], Banner: trimmed, FFmpegLibraries: map[string]string{}}
	for _, item := range ffmpegLibraryPattern.FindAllStringSubmatch(trimmed, -1) {
		version.FFmpegLibraries[strings.ToLower(item[1])] = item[2]
	}
	for _, item := range ffmpegBuildIDPattern.FindAllStringSubmatch(trimmed, -1) {
		name := map[string]string{"lavc": "avcodec", "lavf": "avformat", "swr": "swresample", "sws": "swscale"}[strings.ToLower(item[1])]
		version.FFmpegLibraries[name] = item[2]
	}
	return version, nil
}

func parseFPCalcJSON(output []byte) (FPCalcResult, error) {
	var raw struct {
		Duration    json.RawMessage `json:"duration"`
		Fingerprint string          `json:"fingerprint"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	if err := decoder.Decode(&raw); err != nil {
		return FPCalcResult{}, fmt.Errorf("fpcalc returned malformed output: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return FPCalcResult{}, errors.New("fpcalc returned trailing output")
	}
	if len(raw.Duration) == 0 || string(raw.Duration) == "null" {
		return FPCalcResult{}, errors.New("fpcalc response has no duration")
	}
	var duration float64
	if err := json.Unmarshal(raw.Duration, &duration); err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 0 {
		return FPCalcResult{}, errors.New("fpcalc response duration is invalid")
	}
	algorithm, err := validateCompressedFingerprint(raw.Fingerprint)
	if err != nil {
		return FPCalcResult{}, err
	}
	return FPCalcResult{Duration: duration, Fingerprint: raw.Fingerprint, Algorithm: algorithm}, nil
}

// validateCompressedFingerprint validates Chromaprint's compressed framing
// without allocating according to its 24-bit word count.
func validateCompressedFingerprint(encoded string) (uint8, error) {
	if encoded == "" || strings.ContainsAny(encoded, "=\r\n") {
		return 0, errors.New("fpcalc fingerprint is invalid")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) < 5 {
		return 0, errors.New("fpcalc fingerprint is invalid")
	}
	algorithm := decoded[0]
	if algorithm > 4 {
		return 0, errors.New("fpcalc fingerprint algorithm is invalid")
	}
	wordCount := int(decoded[1])<<16 | int(decoded[2])<<8 | int(decoded[3])
	if wordCount <= 0 {
		return 0, errors.New("fpcalc fingerprint has no words")
	}
	availableBits := (len(decoded) - 4) * 8
	if wordCount > availableBits/3 {
		return 0, errors.New("fpcalc fingerprint payload is truncated")
	}
	// The header counts zero delimiters, not 3-bit codes. Locate the end of
	// the variable-length code stream before interpreting its exception block.
	base := decoded[4:]
	var normalBits, delimiters int
	var exceptions int
	for normalBits+3 <= availableBits && delimiters < wordCount {
		value := packedBits(base, normalBits, 3)
		normalBits += 3
		if value == 0 {
			delimiters++
			continue
		}
		if value == 7 {
			exceptions++
		}
		// Codes 1..6 directly encode small deltas; 7 consumes the next
		// five-bit exception value in the following block.
	}
	if delimiters != wordCount {
		return 0, errors.New("fpcalc fingerprint payload is truncated")
	}
	baseBytes := (normalBits + 7) / 8
	if baseBytes > len(decoded)-4 || !zeroPadding(decoded[4:4+baseBytes], normalBits) {
		return 0, errors.New("fpcalc fingerprint padding is malformed")
	}
	extraBytes := (5*exceptions + 7) / 8
	if 4+baseBytes+extraBytes != len(decoded) {
		return 0, errors.New("fpcalc fingerprint payload length is invalid")
	}
	base = decoded[4 : 4+baseBytes]
	extra := decoded[4+baseBytes:]
	extraPosition := 0
	wordPosition := uint64(0)
	for bit := 0; bit < normalBits; bit += 3 {
		value := packedBits(base, bit, 3)
		if value == 0 {
			wordPosition = 0
			continue
		}
		delta := uint64(value)
		if value == 7 {
			delta += uint64(packedBits(extra, extraPosition, 5))
			extraPosition += 5
		}
		wordPosition += delta
		if wordPosition > 32 {
			return 0, errors.New("fpcalc fingerprint delta overflows")
		}
	}
	if extraPosition != exceptions*5 || !zeroPadding(extra, exceptions*5) {
		return 0, errors.New("fpcalc fingerprint padding is malformed")
	}
	return algorithm, nil
}

func packedBits(data []byte, bit, width int) uint8 {
	var value uint8
	for offset := 0; offset < width; offset++ {
		at := bit + offset
		if at/8 < len(data) && data[at/8]&(1<<uint(at%8)) != 0 {
			value |= 1 << uint(offset)
		}
	}
	return value
}

func zeroPadding(data []byte, usedBits int) bool {
	for bit := usedBits; bit < len(data)*8; bit++ {
		if packedBits(data, bit, 1) != 0 {
			return false
		}
	}
	return true
}

type fpcalcProcess interface {
	stdoutPipe() (io.ReadCloser, error)
	stderrPipe() (io.ReadCloser, error)
	start() error
	wait() error
	kill() error
}
type fpcalcStarter func(context.Context, string, []string) (fpcalcProcess, error)
type execFPCalcProcess struct{ command *exec.Cmd }

func startFPCalcProcess(ctx context.Context, executable string, args []string) (fpcalcProcess, error) {
	return &execFPCalcProcess{command: exec.CommandContext(ctx, executable, args...)}, nil
}
func (p *execFPCalcProcess) stdoutPipe() (io.ReadCloser, error) { return p.command.StdoutPipe() }
func (p *execFPCalcProcess) stderrPipe() (io.ReadCloser, error) { return p.command.StderrPipe() }
func (p *execFPCalcProcess) start() error                       { return p.command.Start() }
func (p *execFPCalcProcess) wait() error                        { return p.command.Wait() }
func (p *execFPCalcProcess) kill() error {
	if p.command.Process == nil {
		return nil
	}
	return p.command.Process.Kill()
}

func (p *FPCalc) run(parent context.Context, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	process, err := p.start(ctx, p.executable, args)
	if err != nil {
		return nil, fmt.Errorf("start fpcalc: %w", err)
	}
	stdout, err := process.stdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("read fpcalc output: %w", err)
	}
	stderr, err := process.stderrPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, fmt.Errorf("read fpcalc output: %w", err)
	}
	if err := process.start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start fpcalc: %w", err)
	}
	outCh := make(chan boundedRead, 1)
	errCh := make(chan boundedRead, 1)
	go func() { outCh <- readBounded(stdout, maxFPCalcOutputBytes) }()
	go func() { errCh <- readBounded(stderr, maxFPCalcStderrBytes) }()
	var out, diagnostic boundedRead
	for received := 0; received < 2; received++ {
		select {
		case out = <-outCh:
		case diagnostic = <-errCh:
		case <-ctx.Done():
			return nil, stopFPCalcProcess(process, stdout, stderr, fmt.Errorf("fpcalc did not finish: %w", ctx.Err()))
		}
		if out.overflow {
			return nil, stopFPCalcProcess(process, stdout, stderr, fmt.Errorf("fpcalc stdout exceeded %d bytes", maxFPCalcOutputBytes))
		}
		if diagnostic.overflow {
			return nil, stopFPCalcProcess(process, stdout, stderr, fmt.Errorf("fpcalc stderr exceeded %d bytes", maxFPCalcStderrBytes))
		}
	}
	if out.err != nil {
		return nil, stopFPCalcProcess(process, stdout, stderr, fmt.Errorf("read fpcalc output: %w", out.err))
	}
	if diagnostic.err != nil {
		return nil, stopFPCalcProcess(process, stdout, stderr, fmt.Errorf("read fpcalc output: %w", diagnostic.err))
	}
	waitResult := make(chan error, 1)
	go func() { waitResult <- process.wait() }()
	var waitErr error
	select {
	case waitErr = <-waitResult:
	case <-ctx.Done():
		_ = process.kill()
		_ = stdout.Close()
		_ = stderr.Close()
		<-waitResult
		return nil, fmt.Errorf("fpcalc did not finish: %w", ctx.Err())
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("fpcalc did not finish: %w", ctx.Err())
		}
		return nil, errors.New("fpcalc failed")
	}
	return out.data, nil
}

func stopFPCalcProcess(process fpcalcProcess, stdout, stderr io.ReadCloser, cause error) error {
	_ = process.kill()
	_ = stdout.Close()
	_ = stderr.Close()
	_ = process.wait()
	return cause
}
