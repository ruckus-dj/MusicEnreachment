//go:build managed_transport

package tools

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
)

var managedManifestPath = flag.String("managed-manifest", "", "managed FFmpeg transport test manifest")

type managedFixtureManifest struct {
	Platform    string            `json:"platform"`
	Source      string            `json:"source"`
	Release     string            `json:"release"`
	Artifacts   []json.RawMessage `json:"artifacts"`
	Versions    map[string]string `json:"versions"`
	FFmpegPath  string            `json:"ffmpeg_path"`
	FFprobePath string            `json:"ffprobe_path"`
	FixturePath string            `json:"fixture_path"`
}

func TestManagedFFProbeDescriptorTransport(t *testing.T) {
	manifest, err := loadManagedFixtureManifest(*managedManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := NewFFProbe(manifest.FFprobePath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := probe.CheckFileTransport(ctx); err != nil {
		t.Fatalf("check generated fd transport witness: %v", err)
	}

	// This is a trusted fixture baseline, deliberately probed by pathname. The
	// generated capability witness above is separate and never stages this source.
	pathTechnical, err := trustedPathTechnicalBaseline(ctx, probe, manifest.FixturePath)
	if err != nil {
		t.Fatalf("probe trusted fixture baseline: %v", err)
	}
	pathSeek, err := probe.probePacketSeekWitness(ctx, manifest.FixturePath)
	if err != nil {
		t.Fatalf("seek trusted fixture baseline: %v", err)
	}

	opened, err := openManagedFixture(ctx, manifest.FixturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	fileTechnical, err := probe.ProbeTechnicalFile(ctx, opened)
	if err != nil {
		t.Fatalf("probe fixture through borrowed descriptor: %v", err)
	}
	assertTechnicalEquivalent(t, pathTechnical, fileTechnical)
	pathQuick, err := trustedPathQuickBaseline(ctx, probe, manifest.FixturePath)
	if err != nil {
		t.Fatalf("probe trusted fixture quick baseline: %v", err)
	}
	fileQuick, err := probe.ProbeFile(ctx, opened)
	if err != nil {
		t.Fatalf("quick-probe fixture through borrowed descriptor: %v", err)
	}
	if fileQuick != pathQuick {
		t.Fatalf("quick probe differs from trusted pathname baseline: path=%v fd=%v", pathQuick, fileQuick)
	}
	evidence, err := probe.ProbeFileTransportCapabilities(ctx, opened)
	if err != nil {
		t.Fatalf("verify fd protocol and backward seek: %v", err)
	}
	if !containsString(evidence.Protocols, "fd") {
		t.Fatalf("managed ffprobe input protocols = %q, want fd", evidence.Protocols)
	}
	if !jsonEqual(pathSeek, evidence.Packets) {
		t.Fatalf("fd packet seek differs from trusted file baseline\npath: %s\nfd: %s", pathSeek, evidence.Packets)
	}
	witnessPath := filepath.Join(t.TempDir(), "seek-witness.wav")
	if err := writeSeekWitness(ctx, witnessPath); err != nil {
		t.Fatal(err)
	}
	assertPipeCannotSeek(t, manifest.FFprobePath, witnessPath)
	assertSecondaryReferencesBlocked(t, probe, manifest.FixturePath, pathQuick)

	t.Run("opened fixture survives pathname replacement", func(t *testing.T) {
		root := t.TempDir()
		fixturePath := filepath.Join(root, "witness.mp4")
		if err := copyManagedFixture(manifest.FixturePath, fixturePath); err != nil {
			t.Fatal(err)
		}
		baseline, err := probe.probePacketSeekWitness(ctx, fixturePath)
		if err != nil {
			t.Fatalf("probe pinned fixture baseline: %v", err)
		}
		file, err := openManagedFixture(ctx, fixturePath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		if err := os.Rename(fixturePath, filepath.Join(root, "original.mp4")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixturePath, []byte("replacement at the old pathname"), 0o600); err != nil {
			t.Fatal(err)
		}
		actual, err := probe.ProbePacketSeekFile(ctx, file)
		if err != nil {
			t.Fatalf("seek using opened fixture after pathname swap: %v", err)
		}
		if !jsonEqual(baseline, actual) {
			t.Fatalf("opened fixture no longer refers to pinned bytes\nbaseline: %s\nopened: %s", baseline, actual)
		}
	})
}

// trustedPathQuickBaseline is a test-only pathname control for the trusted
// managed fixture. Source probes themselves always use the borrowed fd path.
func trustedPathQuickBaseline(ctx context.Context, probe *FFProbe, path string) (bool, error) {
	if !filepath.IsAbs(path) {
		return false, fmt.Errorf("trusted fixture path must be absolute")
	}
	args := []string{"-v", "error", "-protocol_whitelist", "file", "-print_format", "json", "-show_entries", "stream=codec_type", path}
	output, err := probe.runTechnical(ctx, args, func(ctx context.Context, executable string, args []string) (technicalProcess, error) {
		return probe.technical(ctx, executable, args)
	})
	if err != nil {
		return false, err
	}
	return probeHasAudio(output)
}

func assertSecondaryReferencesBlocked(t *testing.T, probe *FFProbe, fixturePath string, expectedAudio bool) {
	t.Helper()
	if !expectedAudio {
		t.Fatal("managed fixture must expose audio to prove secondary-resource controls consumed valid media")
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	root := t.TempDir()
	secondary := filepath.Join(root, "secondary.bin")
	if err := copyManagedFixture(fixturePath, secondary); err != nil {
		t.Fatal(err)
	}
	localPlaylist := filepath.Join(root, "local.ffconcat")
	if err := os.WriteFile(localPlaylist, []byte("ffconcat version 1.0\nfile '"+secondary+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	localControl := assertConcatPathControl(t, ctx, probe, localPlaylist, "file")
	if localControl != expectedAudio {
		t.Fatalf("trusted local concat control audio=%v, fixture baseline=%v", localControl, expectedAudio)
	}
	localFile, err := openManagedFixture(ctx, localPlaylist)
	if err != nil {
		t.Fatal(err)
	}
	localHasAudio, localErr := probe.ProbeFile(ctx, localFile)
	_ = localFile.Close()
	if localErr == nil {
		t.Fatalf("fd-only local concat source was not rejected (audio=%v)", localHasAudio)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/secondary" {
			http.NotFound(w, r)
			return
		}
		input, err := os.Open(fixturePath)
		if err != nil {
			http.Error(w, "fixture unavailable", http.StatusInternalServerError)
			return
		}
		defer func() { _ = input.Close() }()
		_, _ = io.Copy(w, input)
	}))
	defer server.Close()
	httpPlaylist := filepath.Join(root, "http.ffconcat")
	playlist := fmt.Sprintf("ffconcat version 1.0\nfile '%s/secondary'\n", server.URL)
	if err := os.WriteFile(httpPlaylist, []byte(playlist), 0o600); err != nil {
		t.Fatal(err)
	}
	httpControl := assertConcatPathControl(t, ctx, probe, httpPlaylist, "file,http,tcp")
	if httpControl != expectedAudio {
		t.Fatalf("trusted HTTP concat control audio=%v, fixture baseline=%v", httpControl, expectedAudio)
	}
	if requests.Load() == 0 {
		t.Fatal("trusted whitelistfile/http concat control did not request its loopback secondary resource")
	}
	requests.Store(0)
	httpFile, err := openManagedFixture(ctx, httpPlaylist)
	if err != nil {
		t.Fatal(err)
	}
	httpHasAudio, httpErr := probe.ProbeFile(ctx, httpFile)
	_ = httpFile.Close()
	if httpErr == nil {
		t.Fatalf("fd-only HTTP concat source was not rejected (audio=%v)", httpHasAudio)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("fd-only probe made %d loopback secondary-resource requests", got)
	}
}

func assertConcatPathControl(t *testing.T, ctx context.Context, probe *FFProbe, playlist, whitelist string) bool {
	t.Helper()
	args := []string{"-v", "error", "-protocol_whitelist", whitelist, "-f", "concat", "-safe", "0", "-print_format", "json", "-show_entries", "stream=codec_type", playlist}
	output, err := probe.runTechnical(ctx, args, func(ctx context.Context, executable string, args []string) (technicalProcess, error) {
		return probe.technical(ctx, executable, args)
	})
	if err != nil {
		t.Fatalf("trusted concat whitelist control (%s) failed: %v", strings.Join(args, " "), err)
	}
	hasAudio, err := probeHasAudio(output)
	if err != nil {
		t.Fatalf("trusted concat whitelist control returned invalid stream response: %v", err)
	}
	return hasAudio
}

// trustedPathTechnicalBaseline is test-only equivalence evidence for the
// explicitly trusted managed fixture; source probes themselves accept loans.
func trustedPathTechnicalBaseline(ctx context.Context, probe *FFProbe, path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("trusted fixture path must be absolute")
	}
	output, err := probe.runTechnical(ctx, []string{"-v", "error", "-show_format", "-show_streams", "-of", "json", path}, func(ctx context.Context, executable string, args []string) (technicalProcess, error) {
		return probe.technical(ctx, executable, args)
	})
	if err != nil {
		return nil, err
	}
	if err := validateTechnicalResponse(output); err != nil {
		return nil, err
	}
	return output, nil
}

func loadManagedFixtureManifest(path string) (managedFixtureManifest, error) {
	if path == "" {
		return managedFixtureManifest{}, fmt.Errorf("-managed-manifest is required for managed_transport tests")
	}
	file, err := os.Open(path)
	if err != nil {
		return managedFixtureManifest{}, fmt.Errorf("open managed fixture manifest: %w", err)
	}
	defer func() { _ = file.Close() }()
	var manifest managedFixtureManifest
	if err := json.NewDecoder(file).Decode(&manifest); err != nil {
		return managedFixtureManifest{}, fmt.Errorf("decode managed fixture manifest: %w", err)
	}
	if manifest.Platform != runtime.GOOS+"/"+runtime.GOARCH || manifest.Release == "" || manifest.Source == "" || len(manifest.Artifacts) == 0 {
		return managedFixtureManifest{}, fmt.Errorf("managed fixture manifest has incomplete or incompatible platform/release evidence")
	}
	for label, path := range map[string]string{"ffmpeg": manifest.FFmpegPath, "ffprobe": manifest.FFprobePath, "fixture": manifest.FixturePath} {
		if !filepath.IsAbs(path) {
			return managedFixtureManifest{}, fmt.Errorf("managed %s path must be absolute", label)
		}
		if _, err := os.Stat(path); err != nil {
			return managedFixtureManifest{}, fmt.Errorf("managed %s is unavailable: %w", label, err)
		}
	}
	return manifest, nil
}

func openManagedFixture(ctx context.Context, path string) (sourcefs.RegularFile, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve managed fixture: %w", err)
	}
	root, err := sourcefs.NewOpener().OpenRoot(ctx, filepath.Dir(resolved))
	if err != nil {
		return nil, fmt.Errorf("open managed fixture directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	file, err := root.OpenRegular(ctx, filepath.Base(resolved))
	if err != nil {
		return nil, fmt.Errorf("open managed fixture: %w", err)
	}
	return file, nil
}

func assertTechnicalEquivalent(t *testing.T, expected, actual []byte) {
	t.Helper()
	var expectedValue, actualValue map[string]any
	if err := json.Unmarshal(expected, &expectedValue); err != nil {
		t.Fatalf("decode pathname technical response: %v", err)
	}
	if err := json.Unmarshal(actual, &actualValue); err != nil {
		t.Fatalf("decode descriptor technical response: %v", err)
	}
	for label, value := range map[string]map[string]any{"pathname": expectedValue, "descriptor": actualValue} {
		format, ok := value["format"].(map[string]any)
		if !ok {
			t.Fatalf("%s technical response has no format object", label)
		}
		delete(format, "filename")
	}
	if !reflect.DeepEqual(expectedValue, actualValue) {
		t.Fatalf("technical responses differ beyond format.filename\npath: %s\nfd: %s", expected, actual)
	}
}

func jsonEqual(first, second []byte) bool {
	var firstValue, secondValue any
	return json.Unmarshal(first, &firstValue) == nil && json.Unmarshal(second, &secondValue) == nil && reflect.DeepEqual(firstValue, secondValue)
}

func assertPipeCannotSeek(t *testing.T, ffprobe, fixture string) {
	t.Helper()
	input, err := os.Open(fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, ffprobe, "-v", "error", "-protocol_whitelist", "pipe", "-read_intervals", "8%+1,0%+1", "-show_packets", "-show_entries", "packet=pts_time,data_hash", "-show_data_hash", "sha256", "-of", "json", "pipe:0")
	command.Stdin = input
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("pipe negative control unexpectedly completed backward seek: %s", output)
	}
}

func copyManagedFixture(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := output.ReadFrom(input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}
