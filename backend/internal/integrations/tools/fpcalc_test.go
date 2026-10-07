package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const upstreamFingerprint = "AQAAEwkjrUmSJQpUHflR9mjSJMdZpcO_Imdw9dCO9Clu4_wQPvhCB01w6xAtXNcAp5RASgDBhDSCGGIAcwA"

func TestValidateCompressedFingerprint(t *testing.T) {
	algorithm, err := validateCompressedFingerprint(upstreamFingerprint)
	if err != nil || algorithm != 1 {
		t.Fatalf("validate upstream vector: algorithm=%d err=%v", algorithm, err)
	}
	for algorithm := byte(0); algorithm <= 4; algorithm++ {
		encoded := base64.RawURLEncoding.EncodeToString([]byte{algorithm, 0, 0, 1, 7, 0})
		if _, err := validateCompressedFingerprint(encoded); err != nil {
			t.Fatalf("algorithm %d: %v", algorithm, err)
		}
	}
	for name, value := range map[string]string{
		"bad alphabet": "AAAAAQc!", "padding": "AAAAAQcA=",
		// Canonical unpadded zero-word header (four header bytes plus one
		// payload byte) so the case reaches the word-count check instead of
		// the '=' alphabet rejection.
		"empty words": base64.RawURLEncoding.EncodeToString([]byte{0, 0, 0, 0, 0}),
		"truncated":   "AAAAAQ", "trailing payload": "AAAAAQcAAA", "line break": "AAAA\nAQcA",
		"algorithm": base64.RawURLEncoding.EncodeToString([]byte{5, 0, 0, 1, 7, 0}),
		"delta 33":  base64.RawURLEncoding.EncodeToString([]byte{0, 0, 0, 1, 7, 26}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateCompressedFingerprint(value); err == nil {
				t.Fatalf("accepted %q", value)
			}
		})
	}
}

// TestValidateCompressedFingerprintWordCount pins the header boundary: a
// canonical zero word count is rejected with the no-words error before any
// payload is interpreted, while a single zero-valued word terminated by a
// delimiter is a valid fingerprint.
func TestValidateCompressedFingerprintWordCount(t *testing.T) {
	zeroWords := base64.RawURLEncoding.EncodeToString([]byte{0, 0, 0, 0, 0})
	if _, err := validateCompressedFingerprint(zeroWords); err == nil || !strings.Contains(err.Error(), "no words") {
		t.Fatalf("zero word count error = %v, want a no-words error", err)
	}
	zeroValuedWord := base64.RawURLEncoding.EncodeToString([]byte{0, 0, 0, 1, 0})
	algorithm, err := validateCompressedFingerprint(zeroValuedWord)
	if err != nil || algorithm != 0 {
		t.Fatalf("zero-valued word algorithm=%d err=%v", algorithm, err)
	}
}

// TestValidateCompressedFingerprintDirectDeltas packs two normal tokens into a
// single payload byte: a small delta 1..6 followed by a zero delimiter, with
// the unused bits clear. Every value must be accepted.
func TestValidateCompressedFingerprintDirectDeltas(t *testing.T) {
	for delta := byte(1); delta <= 6; delta++ {
		encoded := base64.RawURLEncoding.EncodeToString([]byte{0, 0, 0, 1, delta})
		algorithm, err := validateCompressedFingerprint(encoded)
		if err != nil || algorithm != 0 {
			t.Fatalf("direct delta %d: algorithm=%d err=%v", delta, algorithm, err)
		}
	}
}

// TestValidateCompressedFingerprintOverflowAndPadding rejects the malformed
// framings the word-count check alone cannot catch: a delta that crosses the
// 32-word bound, including one accumulated across two tokens, and non-zero
// padding in either the normal or the exception block.
func TestValidateCompressedFingerprintOverflowAndPadding(t *testing.T) {
	cases := map[string]struct {
		encoded string
		want    string
	}{
		"delta 33":            {base64.RawURLEncoding.EncodeToString([]byte{0, 0, 0, 1, 7, 26}), "overflows"},
		"cumulative overflow": {base64.RawURLEncoding.EncodeToString([]byte{0, 0, 0, 1, 0x3E, 0x00, 0x14}), "overflows"},
		"normal padding":      {base64.RawURLEncoding.EncodeToString([]byte{0, 0, 0, 1, 0x47, 0x00}), "padding"},
		"exception padding":   {base64.RawURLEncoding.EncodeToString([]byte{0, 0, 0, 1, 0x07, 0x20}), "padding"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := validateCompressedFingerprint(test.encoded); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseFPCalcJSONAndVersion(t *testing.T) {
	result, err := parseFPCalcJSON([]byte(`{"duration":20,"fingerprint":"` + upstreamFingerprint + `"}`))
	if err != nil || result.Duration != 20 || result.Algorithm != 1 || result.Fingerprint != upstreamFingerprint {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, input := range []string{
		`{"duration":-1,"fingerprint":"` + upstreamFingerprint + `"}`,
		`{"duration":null,"fingerprint":"` + upstreamFingerprint + `"}`,
		`{"duration":1e999,"fingerprint":"` + upstreamFingerprint + `"}`,
		`{"duration":1,"fingerprint":"` + upstreamFingerprint + `"} false`,
		`{"duration":1,"fingerprint":"` + upstreamFingerprint + `"} {}`,
	} {
		if _, err := parseFPCalcJSON([]byte(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	version, err := parseFPCalcVersion("fpcalc version 1.6.1 (FFmpeg Lavc62.11.100 Lavf62.3.100 SwR6.1.100)")
	if err != nil || version.Version != "1.6.1" || version.Banner == "" || version.FFmpegLibraries["avcodec"] != "62.11.100" || version.FFmpegLibraries["avformat"] != "62.3.100" || version.FFmpegLibraries["swresample"] != "6.1.100" {
		t.Fatalf("version=%+v err=%v", version, err)
	}
	if _, err := parseFPCalcVersion("malformed FFmpeg banner 1.2.3"); err == nil {
		t.Fatal("accepted unrelated semantic version")
	}
}

func TestFPCalcFingerprintRunnerArgumentsAndExit(t *testing.T) {
	process := &fakeFPCalcProcess{stdout: `{"duration":1,"fingerprint":"` + upstreamFingerprint + `"}`}
	executable := filepath.Join(t.TempDir(), "fpcalc")
	source := filepath.Join(t.TempDir(), "source.wav")
	probe, err := NewFPCalc(executable)
	if err != nil {
		t.Fatal(err)
	}
	probe.start = func(_ context.Context, gotExecutable string, args []string) (fpcalcProcess, error) {
		if gotExecutable != executable || !reflect.DeepEqual(args, []string{"-json", "--", source}) {
			t.Fatalf("invocation %q %#v", gotExecutable, args)
		}
		return process, nil
	}
	if _, err := probe.Fingerprint(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if !process.waited {
		t.Fatal("process was not reaped")
	}
	process = &fakeFPCalcProcess{stdout: "fpcalc version 1.6.1 (FFmpeg Lavc62.11.100)"}
	probe.start = func(_ context.Context, gotExecutable string, args []string) (fpcalcProcess, error) {
		if gotExecutable != executable || !reflect.DeepEqual(args, []string{"-version"}) {
			t.Fatalf("version invocation %q %#v", gotExecutable, args)
		}
		return process, nil
	}
	if version, err := probe.Version(context.Background()); err != nil || version.Version != "1.6.1" {
		t.Fatalf("version=%+v err=%v", version, err)
	}
	process = &fakeFPCalcProcess{stdout: `{"duration":1,"fingerprint":"` + upstreamFingerprint + `"}`, waitErr: errors.New("exit 1")}
	probe.start = func(context.Context, string, []string) (fpcalcProcess, error) { return process, nil }
	if _, err := probe.Fingerprint(context.Background(), source); err == nil {
		t.Fatal("accepted nonzero exit")
	}
}

func TestFPCalcTimeoutAndOutputBounds(t *testing.T) {
	probe, err := NewFPCalc(filepath.Join(t.TempDir(), "fpcalc"))
	if err != nil {
		t.Fatal(err)
	}
	probe.timeout = 10 * time.Millisecond
	process := &fakeFPCalcProcess{block: true}
	probe.start = func(context.Context, string, []string) (fpcalcProcess, error) { return process, nil }
	if _, err := probe.run(context.Background(), nil); err == nil || !process.killed || !process.waited {
		t.Fatalf("timeout err=%v killed=%v waited=%v", err, process.killed, process.waited)
	}
	process = &fakeFPCalcProcess{stdout: strings.Repeat("x", maxFPCalcOutputBytes+1)}
	probe.timeout = time.Second
	probe.start = func(context.Context, string, []string) (fpcalcProcess, error) { return process, nil }
	if _, err := probe.run(context.Background(), nil); err == nil || !process.killed || !process.waited {
		t.Fatalf("stdout overflow err=%v killed=%v waited=%v", err, process.killed, process.waited)
	}
	process = &fakeFPCalcProcess{
		stdout: `{"duration":1,"fingerprint":"` + upstreamFingerprint + `"}`,
		stderr: strings.Repeat("e", maxFPCalcStderrBytes+1),
	}
	probe.start = func(context.Context, string, []string) (fpcalcProcess, error) { return process, nil }
	if _, err := probe.run(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "stderr exceeded") || !process.killed || !process.waited {
		t.Fatalf("stderr overflow err=%v killed=%v waited=%v", err, process.killed, process.waited)
	}
}

// TestFPCalcCancelsWhileWaitingAfterPipesClose exercises the second cancellation
// path: both output pipes reach EOF, so run enters the process wait, and the
// context is canceled there. run must still kill and reap exactly one child and
// report the cancellation.
func TestFPCalcCancelsWhileWaitingAfterPipesClose(t *testing.T) {
	probe, err := NewFPCalc(filepath.Join(t.TempDir(), "fpcalc"))
	if err != nil {
		t.Fatal(err)
	}
	process := &pipeClosingFPCalcProcess{
		stdout:      io.NopCloser(strings.NewReader(`{"duration":1,"fingerprint":"` + upstreamFingerprint + `"}`)),
		stderr:      io.NopCloser(strings.NewReader("")),
		waitStarted: make(chan struct{}),
		unblock:     make(chan struct{}),
	}
	probe.start = func(context.Context, string, []string) (fpcalcProcess, error) { return process, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := probe.run(ctx, nil)
		done <- err
	}()
	select {
	case <-process.waitStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("fpcalc never reached the wait after its pipes closed")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after cancellation")
	}
	if !process.killed || !process.waited {
		t.Fatalf("killed=%v waited=%v, want both", process.killed, process.waited)
	}
}

// pipeClosingFPCalcProcess supplies already-EOF pipes and blocks in wait until
// kill releases it, modelling a child that outlives its output.
type pipeClosingFPCalcProcess struct {
	stdout, stderr io.ReadCloser
	waitStarted    chan struct{}
	unblock        chan struct{}
	killed, waited bool
}

func (p *pipeClosingFPCalcProcess) stdoutPipe() (io.ReadCloser, error) { return p.stdout, nil }
func (p *pipeClosingFPCalcProcess) stderrPipe() (io.ReadCloser, error) { return p.stderr, nil }
func (p *pipeClosingFPCalcProcess) start() error                       { return nil }
func (p *pipeClosingFPCalcProcess) wait() error {
	p.waited = true
	close(p.waitStarted)
	<-p.unblock
	return nil
}
func (p *pipeClosingFPCalcProcess) kill() error {
	p.killed = true
	select {
	case <-p.unblock:
	default:
		close(p.unblock)
	}
	return nil
}

type fakeFPCalcProcess struct {
	stdout, stderr        string
	waitErr               error
	block, killed, waited bool
}

func (p *fakeFPCalcProcess) stdoutPipe() (io.ReadCloser, error) {
	return &fpcalcReader{Reader: strings.NewReader(p.stdout), block: p.block, closed: make(chan struct{})}, nil
}
func (p *fakeFPCalcProcess) stderrPipe() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(p.stderr)), nil
}
func (p *fakeFPCalcProcess) start() error { return nil }
func (p *fakeFPCalcProcess) wait() error  { p.waited = true; return p.waitErr }
func (p *fakeFPCalcProcess) kill() error  { p.killed = true; return nil }

type fpcalcReader struct {
	io.Reader
	block  bool
	closed chan struct{}
}

func (r *fpcalcReader) Read(p []byte) (int, error) {
	if r.block {
		<-r.closed
		return 0, io.ErrClosedPipe
	}
	return r.Reader.Read(p)
}
func (r *fpcalcReader) Close() error { close(r.closed); return nil }

func TestFPCalcRequiresAbsoluteExecutableAndInput(t *testing.T) {
	if _, err := NewFPCalc("fpcalc"); err == nil {
		t.Fatal("accepted relative executable")
	}
	probe, err := NewFPCalc(filepath.Join(t.TempDir(), "fpcalc"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Fingerprint(context.Background(), "client-url"); err == nil {
		t.Fatal("accepted relative source")
	}
}
