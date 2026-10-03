package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type borrowedFixture struct {
	file      *os.File
	borrowed  bool
	closed    bool
	started   chan struct{}
	startOnce sync.Once
}

func (f *borrowedFixture) Stat(context.Context) (os.FileInfo, error) { return f.file.Stat() }
func (f *borrowedFixture) Close() error {
	f.closed = true
	return f.file.Close()
}
func (f *borrowedFixture) Borrow(ctx context.Context, callback func(*os.File) error) error {
	if f.closed || f.borrowed {
		return os.ErrClosed
	}
	f.borrowed = true
	f.startOnce.Do(func() { close(f.started) })
	defer func() { f.borrowed = false }()
	if _, err := f.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return callback(f.file)
}

type fileProbeProcess struct {
	stdout io.ReadCloser
	stderr io.ReadCloser
	waitFn func() error
	killed bool
}

func (p *fileProbeProcess) stdoutPipe() (io.ReadCloser, error) { return p.stdout, nil }
func (p *fileProbeProcess) stderrPipe() (io.ReadCloser, error) { return p.stderr, nil }
func (p *fileProbeProcess) start() error                       { return nil }
func (p *fileProbeProcess) wait() error {
	if p.waitFn != nil {
		return p.waitFn()
	}
	return nil
}
func (p *fileProbeProcess) kill() error { p.killed = true; return nil }

func newBorrowedFixture(t *testing.T) *borrowedFixture {
	return newBorrowedFixtureWithContents(t, []byte("witness"))
}

func newBorrowedFixtureWithContents(t *testing.T, contents []byte) *borrowedFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "borrowed input")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return &borrowedFixture{file: file, started: make(chan struct{})}
}

func newFileProbe(t *testing.T) *FFProbe {
	t.Helper()
	probe, err := NewFFProbe(filepath.Join(t.TempDir(), "ffprobe"))
	if err != nil {
		t.Fatal(err)
	}
	return probe
}

func TestProbeTechnicalFileUsesBorrowedHandleAndFixedFDArguments(t *testing.T) {
	fixture := newBorrowedFixture(t)
	probe := newFileProbe(t)
	response := `{"format":{},"streams":[{"codec_type":"audio"}]}`
	waited := false
	probe.fileTech = func(_ context.Context, executable string, args []string, handle *os.File) (technicalProcess, error) {
		if !fixture.borrowed || handle != fixture.file {
			t.Fatal("child was not started inside the source file borrow")
		}
		if executable != probe.executable {
			t.Fatalf("executable = %q", executable)
		}
		want := technicalFileArguments()
		if !slices.Equal(args, want) {
			t.Fatalf("args = %q, want %q", args, want)
		}
		for _, arg := range args {
			if strings.Contains(arg, fixture.file.Name()) {
				t.Fatalf("source pathname leaked into argv: %q", args)
			}
		}
		return &fileProbeProcess{
			stdout: io.NopCloser(strings.NewReader(response)),
			stderr: io.NopCloser(strings.NewReader("")),
			waitFn: func() error { waited = true; return nil },
		}, nil
	}
	got, err := probe.ProbeTechnicalFile(context.Background(), fixture)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != response || !waited || fixture.borrowed {
		t.Fatalf("output=%s waited=%v borrowActive=%v", got, waited, fixture.borrowed)
	}
}

func TestQuickAndTechnicalProbeReadBorrowedBytesAcrossOutwardSymlinkSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory rename and symlink replacement are not portable to Windows")
	}
	for _, test := range []struct {
		name  string
		args  []string
		probe func(*FFProbe, context.Context, *borrowedFixture) ([]byte, error)
	}{
		{
			name: "quick",
			args: quickFileArguments(),
			probe: func(probe *FFProbe, ctx context.Context, file *borrowedFixture) ([]byte, error) {
				got, err := probe.ProbeFile(ctx, file)
				if err != nil {
					return nil, err
				}
				return []byte(fmt.Sprintf(`{"streams":[{"codec_type":%q}]}`, map[bool]string{true: "audio", false: "video"}[got])), nil
			},
		},
		{
			name: "technical",
			args: technicalFileArguments(),
			probe: func(probe *FFProbe, ctx context.Context, file *borrowedFixture) ([]byte, error) {
				return probe.ProbeTechnicalFile(ctx, file)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBorrowedFixtureWithContents(t, []byte("tag=original;type=audio"))
			probe := newFileProbe(t)
			probe.fileTech = func(_ context.Context, executable string, args []string, handle *os.File) (technicalProcess, error) {
				if executable != probe.executable || !slices.Equal(args, test.args) {
					t.Fatalf("child executable/args = %q %q", executable, args)
				}
				marker, err := readBorrowedAfterOutwardSymlinkSwap(t, fixture, handle)
				if err != nil {
					return nil, err
				}
				tag, streamType := parseProbeMarker(t, marker)
				response := fmt.Sprintf(`{"format":{"tags":{"title":%q}},"streams":[{"codec_type":%q}]}`, tag, streamType)
				if test.name == "quick" {
					response = fmt.Sprintf(`{"streams":[{"codec_type":%q}]}`, streamType)
				}
				return &fileProbeProcess{stdout: io.NopCloser(strings.NewReader(response)), stderr: io.NopCloser(strings.NewReader(""))}, nil
			}
			output, err := test.probe(probe, context.Background(), fixture)
			if err != nil {
				t.Fatal(err)
			}
			if test.name == "quick" {
				if !bytes.Contains(output, []byte(`"audio"`)) {
					t.Fatalf("quick probe did not report audio from original borrowed bytes: %s", output)
				}
			} else if !bytes.Contains(output, []byte(`"title":"original"`)) || !bytes.Contains(output, []byte(`"codec_type":"audio"`)) {
				t.Fatalf("technical probe did not reflect original borrowed bytes: %s", output)
			}
		})
	}
}

func readBorrowedAfterOutwardSymlinkSwap(t *testing.T, fixture *borrowedFixture, handle *os.File) ([]byte, error) {
	t.Helper()
	root := filepath.Dir(handle.Name())
	name := filepath.Base(handle.Name())
	backup := root + ".pinned"
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, name), []byte("tag=outward;type=video"), 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(root, backup); err != nil {
		return nil, err
	}
	if err := os.Symlink(outside, root); err != nil {
		_ = os.Rename(backup, root)
		return nil, err
	}
	defer func() {
		if err := os.Remove(root); err != nil {
			t.Errorf("remove outward symlink: %v", err)
		}
		if err := os.Rename(backup, root); err != nil {
			t.Errorf("restore original source ancestor: %v", err)
		}
	}()
	if !fixture.borrowed || fixture.file != handle {
		return nil, errors.New("child did not receive the active borrowed handle")
	}
	if _, err := handle.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(handle)
}

func parseProbeMarker(t *testing.T, marker []byte) (string, string) {
	t.Helper()
	parts := strings.Split(string(marker), ";type=")
	if len(parts) != 2 {
		t.Fatalf("invalid probe marker %q", marker)
	}
	tag := strings.TrimPrefix(parts[0], "tag=")
	streamType := parts[1]
	return tag, streamType
}

func TestProbeTechnicalFileBoundsOutputAndWaitsBeforeBorrowReturns(t *testing.T) {
	fixture := newBorrowedFixture(t)
	probe := newFileProbe(t)
	process := &fileProbeProcess{
		stdout: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), maxProbeOutputBytes+1))),
		stderr: io.NopCloser(strings.NewReader("")),
	}
	probe.fileTech = func(context.Context, string, []string, *os.File) (technicalProcess, error) { return process, nil }
	if _, err := probe.ProbeTechnicalFile(context.Background(), fixture); err == nil {
		t.Fatal("oversized output was accepted")
	}
	if !process.killed || fixture.borrowed {
		t.Fatalf("killed=%v borrowActive=%v", process.killed, fixture.borrowed)
	}
}

func TestProbeTechnicalFileCancellationStopsAndWaitsInsideBorrow(t *testing.T) {
	fixture := newBorrowedFixture(t)
	probe := newFileProbe(t)
	probe.timeout = time.Second
	reader := &blockingFileReader{closed: make(chan struct{})}
	process := &fileProbeProcess{stdout: reader, stderr: io.NopCloser(strings.NewReader(""))}
	waited := false
	process.waitFn = func() error { waited = true; return nil }
	probe.fileTech = func(context.Context, string, []string, *os.File) (technicalProcess, error) { return process, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := probe.ProbeTechnicalFile(ctx, fixture); done <- err }()
	select {
	case <-fixture.started:
	case <-time.After(time.Second):
		t.Fatal("probe did not borrow source")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled probe did not return")
	}
	if !process.killed || !waited || fixture.borrowed {
		t.Fatalf("killed=%v waited=%v borrowActive=%v", process.killed, waited, fixture.borrowed)
	}
}

func TestProbeTechnicalFileTimeoutStopsAndWaitsInsideBorrow(t *testing.T) {
	fixture := newBorrowedFixture(t)
	probe := newFileProbe(t)
	probe.timeout = 20 * time.Millisecond
	process := &fileProbeProcess{stdout: &blockingFileReader{closed: make(chan struct{})}, stderr: io.NopCloser(strings.NewReader(""))}
	waited := false
	process.waitFn = func() error { waited = true; return nil }
	probe.fileTech = func(context.Context, string, []string, *os.File) (technicalProcess, error) { return process, nil }
	_, err := probe.ProbeTechnicalFile(context.Background(), fixture)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if !process.killed || !waited || fixture.borrowed {
		t.Fatalf("killed=%v waited=%v borrowActive=%v", process.killed, waited, fixture.borrowed)
	}
}

type blockingFileReader struct{ closed chan struct{} }

func (r *blockingFileReader) Read([]byte) (int, error) { <-r.closed; return 0, io.EOF }
func (r *blockingFileReader) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

func TestFileTransportEvidenceRequiresFDAndSeekPackets(t *testing.T) {
	if got := parseInputProtocols([]byte("Input:\n  file\n  fd\nOutput:\n  file\n")); !slices.Equal(got, []string{"file", "fd"}) {
		t.Fatalf("protocols = %q", got)
	}
	if err := validatePacketSeek([]byte(`{"packets":[{"pts_time":"8.0","data_hash":"SHA256:a"},{"pts_time":"0.0","data_hash":"SHA256:b"}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := validatePacketSeek([]byte(`{"packets":[]}`)); err == nil {
		t.Fatal("empty seek witness accepted")
	}
}

func TestProbeFileTransportCapabilitiesRunsProtocolAndBackwardSeek(t *testing.T) {
	fixture := newBorrowedFixture(t)
	probe := newFileProbe(t)
	responses := []string{
		"Supported file protocols:\nInput:\n  file\n  fd\nOutput:\n  file\n",
		`{"packets":[{"pts_time":"8.0","data_hash":"SHA256:first"},{"pts_time":"0.0","data_hash":"SHA256:second"}]}`,
	}
	calls := 0
	probe.fileTech = func(_ context.Context, _ string, args []string, handle *os.File) (technicalProcess, error) {
		if !fixture.borrowed || handle != fixture.file {
			t.Fatal("capability child ran outside file borrow")
		}
		want := [][]string{{"-protocols"}, packetSeekArguments()}
		if calls >= len(want) || !slices.Equal(args, want[calls]) {
			t.Fatalf("call %d args = %q", calls, args)
		}
		response := responses[calls]
		calls++
		return &fileProbeProcess{stdout: io.NopCloser(strings.NewReader(response)), stderr: io.NopCloser(strings.NewReader(""))}, nil
	}
	evidence, err := probe.ProbeFileTransportCapabilities(context.Background(), fixture)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !slices.Equal(evidence.Protocols, []string{"file", "fd"}) || len(evidence.Packets) == 0 {
		t.Fatalf("calls=%d evidence=%+v", calls, evidence)
	}
}

func TestCheckFileTransportUsesAndCleansGeneratedWitness(t *testing.T) {
	probe := newFileProbe(t)
	packets := `{"packets":[{"pts_time":"8.000000","data_hash":"SHA256:first"},{"pts_time":"0.000000","data_hash":"SHA256:second"}]}`
	var witnessPath string
	calls := 0
	probe.fileTech = func(_ context.Context, _ string, args []string, handle *os.File) (technicalProcess, error) {
		calls++
		if calls == 1 {
			if !slices.Equal(args, []string{"-protocols"}) {
				t.Fatalf("protocol args = %q", args)
			}
		} else if !slices.Equal(args, packetSeekArguments()) {
			t.Fatalf("descriptor seek args = %q", args)
		}
		witnessPath = handle.Name()
		if filepath.Dir(witnessPath) == "." || !strings.HasPrefix(filepath.Base(witnessPath), "seek-witness") {
			t.Fatalf("unexpected witness path %q", witnessPath)
		}
		response := "Input:\n  fd\nOutput:\n  file\n"
		if calls == 2 {
			response = packets
		}
		return &fileProbeProcess{stdout: io.NopCloser(strings.NewReader(response)), stderr: io.NopCloser(strings.NewReader(""))}, nil
	}
	probe.technical = func(_ context.Context, _ string, args []string) (technicalProcess, error) {
		if !filepath.IsAbs(args[len(args)-1]) || args[len(args)-1] != witnessPath {
			t.Fatalf("pathname baseline args = %q, witness = %q", args, witnessPath)
		}
		return &fileProbeProcess{stdout: io.NopCloser(strings.NewReader(packets)), stderr: io.NopCloser(strings.NewReader(""))}, nil
	}
	if err := probe.CheckFileTransport(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("descriptor invocations = %d, want 2", calls)
	}
	if _, err := os.Stat(witnessPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("witness remains after check: stat error = %v", err)
	}
}

func TestCheckFileTransportCancellationAndCapabilityFailures(t *testing.T) {
	t.Run("cancel before witness creation", func(t *testing.T) {
		probe := newFileProbe(t)
		called := false
		probe.fileTech = func(context.Context, string, []string, *os.File) (technicalProcess, error) {
			called = true
			return nil, errors.New("unexpected invocation")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := probe.CheckFileTransport(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want canceled", err)
		}
		if called {
			t.Fatal("canceled capability check invoked ffprobe")
		}
	})
	t.Run("cancel during protocol query and clean witness", func(t *testing.T) {
		probe := newFileProbe(t)
		ctx, cancel := context.WithCancel(context.Background())
		var witnessPath string
		probe.fileTech = func(_ context.Context, _ string, _ []string, handle *os.File) (technicalProcess, error) {
			witnessPath = handle.Name()
			cancel()
			return &fileProbeProcess{stdout: &blockingFileReader{closed: make(chan struct{})}, stderr: io.NopCloser(strings.NewReader(""))}, nil
		}
		if err := probe.CheckFileTransport(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want canceled", err)
		}
		if _, err := os.Stat(witnessPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("witness remains after cancellation: stat error = %v", err)
		}
	})

	for _, test := range []struct {
		name      string
		protocols string
		packets   string
		want      string
	}{
		{name: "missing fd", protocols: "Input:\n  file\nOutput:\n  file\n", want: "does not advertise the fd"},
		{name: "failed backward seek", protocols: "Input:\n  fd\nOutput:\n", packets: `{"packets":[{"pts_time":"0","data_hash":"SHA256:a"},{"pts_time":"1","data_hash":"SHA256:b"}]}`, want: "backward-seek order"},
	} {
		t.Run(test.name, func(t *testing.T) {
			probe := newFileProbe(t)
			calls := 0
			probe.fileTech = func(context.Context, string, []string, *os.File) (technicalProcess, error) {
				calls++
				response := test.protocols
				if calls == 2 {
					response = test.packets
				}
				return &fileProbeProcess{stdout: io.NopCloser(strings.NewReader(response)), stderr: io.NopCloser(strings.NewReader(""))}, nil
			}
			if err := probe.CheckFileTransport(context.Background()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want classification containing %q", err, test.want)
			}
		})
	}
}

func TestProbePacketSeekUsesPathOnlyForExplicitBaseline(t *testing.T) {
	probe := newFileProbe(t)
	path := filepath.Join(t.TempDir(), "baseline media.mp4")
	response := `{"packets":[{"pts_time":"8.0","data_hash":"SHA256:first"},{"pts_time":"0.0","data_hash":"SHA256:second"}]}`
	probe.technical = func(_ context.Context, _ string, args []string) (technicalProcess, error) {
		if want := packetSeekPathArguments(path); !slices.Equal(args, want) {
			t.Fatalf("args = %q, want %q", args, want)
		}
		if args[3] != "file" {
			t.Fatalf("trusted fixture baseline protocol whitelist = %q, want file", args[3])
		}
		return &fileProbeProcess{stdout: io.NopCloser(strings.NewReader(response)), stderr: io.NopCloser(strings.NewReader(""))}, nil
	}
	if _, err := probe.probePacketSeekWitness(context.Background(), path); err != nil {
		t.Fatal(err)
	}
}
