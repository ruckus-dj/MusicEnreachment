//go:build managed_transport

package tools

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
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
	pathTechnical, err := probe.ProbeTechnical(ctx, manifest.FixturePath)
	if err != nil {
		t.Fatalf("probe trusted fixture baseline: %v", err)
	}
	pathSeek, err := probe.ProbePacketSeek(ctx, manifest.FixturePath)
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

	t.Run("opened fixture survives pathname replacement", func(t *testing.T) {
		root := t.TempDir()
		fixturePath := filepath.Join(root, "witness.mp4")
		if err := copyManagedFixture(manifest.FixturePath, fixturePath); err != nil {
			t.Fatal(err)
		}
		baseline, err := probe.ProbePacketSeek(ctx, fixturePath)
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
