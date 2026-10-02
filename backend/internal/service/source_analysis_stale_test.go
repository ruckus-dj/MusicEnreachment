package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// TestSourceAnalysisRefusesAStaleRootOrLocation proves every snapshot/state
// disagreement fails as stale before anything is probed: the root's enabled
// flag, inventory path and configured path, and the location's path, size,
// microsecond mtime, audio status and previous variant are all fenced.
func TestSourceAnalysisRefusesAStaleRootOrLocation(t *testing.T) {
	elsewhere := func(f *sourceAnalysisFixture) string { return filepath.Join(f.rootDir, "elsewhere") }
	cases := []struct {
		name   string
		mutate func(*sourceAnalysisFixture)
	}{
		{"root inventory points elsewhere", func(f *sourceAnalysisFixture) {
			other := elsewhere(f)
			f.repository.root.InventoryPath = &other
		}},
		{"root disabled", func(f *sourceAnalysisFixture) { f.repository.root.Enabled = false }},
		{"root inventory missing", func(f *sourceAnalysisFixture) { f.repository.root.InventoryPath = nil }},
		{"root configured path changed", func(f *sourceAnalysisFixture) { f.repository.root.ConfiguredPath = elsewhere(f) }},
		{"snapshot inventory path changed", func(f *sourceAnalysisFixture) { f.snapshot.InventoryPath = "/another/path" }},
		{"location relative path changed", func(f *sourceAnalysisFixture) {
			f.repository.location.RelativePath = filepath.Join("album", "other.flac")
		}},
		{"location size changed", func(f *sourceAnalysisFixture) { f.repository.location.SizeBytes++ }},
		{"location mtime changed", func(f *sourceAnalysisFixture) {
			f.repository.location.Mtime = f.repository.location.Mtime.Add(time.Microsecond)
		}},
		{"location is no longer audio", func(f *sourceAnalysisFixture) {
			f.repository.location.ProbeStatus = persistence.SourceProbeStatusNoAudio
		}},
		{"location previous variant changed", func(f *sourceAnalysisFixture) {
			other := uuid.New()
			f.repository.location.MediaVariantID = &other
		}},
		{"location previous variant removed", func(f *sourceAnalysisFixture) { f.repository.location.MediaVariantID = nil }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
			testCase.mutate(&fixture)
			_, err := fixture.run()
			if !errors.Is(err, persistence.ErrSourceAnalysisStale) {
				t.Fatalf("analysis error = %v, want %v", err, persistence.ErrSourceAnalysisStale)
			}
			if len(fixture.probe.probed) != 0 {
				t.Fatalf("probed paths = %v, want no probe of a stale location", fixture.probe.probed)
			}
		})
	}
}

// TestSourceAnalysisRefusesAMissingFileBeforeProbing covers an inventoried file
// that is gone: the analysis refuses before the probe and publishes nothing.
func TestSourceAnalysisRefusesAMissingFileBeforeProbing(t *testing.T) {
	fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
	if err := os.Remove(fixture.filePath); err != nil {
		t.Fatalf("remove the source file: %v", err)
	}
	if _, err := fixture.run(); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("analysis error = %v, want %v", err, persistence.ErrSourceAnalysisStale)
	}
	if len(fixture.probe.probed) != 0 {
		t.Fatalf("probed paths = %v, want no probe of a missing file", fixture.probe.probed)
	}
}

// TestSourceAnalysisRefusesAFileChangedDuringTheProbe covers a file that
// changes under the probe. The microsecond case proves the post-probe check uses
// the full filesystem precision, because the change stays inside one PostgreSQL
// microsecond and only the nanosecond differs.
func TestSourceAnalysisRefusesAFileChangedDuringTheProbe(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *sourceAnalysisFixture)
	}{
		{"different size", func(t *testing.T, f *sourceAnalysisFixture) {
			if err := os.WriteFile(f.filePath, []byte("different bytes entirely"), 0o644); err != nil {
				t.Errorf("rewrite the source file: %v", err)
			}
		}},
		{"removed", func(t *testing.T, f *sourceAnalysisFixture) {
			if err := os.Remove(f.filePath); err != nil {
				t.Errorf("remove the source file: %v", err)
			}
		}},
		{"same microsecond different nanosecond", func(t *testing.T, f *sourceAnalysisFixture) {
			changed := sourceAnalysisBaseMtime.Add(700 * time.Nanosecond)
			if !changed.Truncate(time.Microsecond).Equal(sourceAnalysisBaseMtime) {
				t.Fatalf("the test must stay inside one microsecond")
			}
			if err := os.Chtimes(f.filePath, changed, changed); err != nil {
				t.Errorf("re-time the source file: %v", err)
			}
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
			fixture.probe.onProbe = func(string) { testCase.change(t, &fixture) }
			if _, err := fixture.run(); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
				t.Fatalf("analysis error = %v, want %v", err, persistence.ErrSourceAnalysisStale)
			}
			if len(fixture.probe.probed) != 1 {
				t.Fatalf("probed paths = %v, want exactly one probe before the change was detected", fixture.probe.probed)
			}
		})
	}
}

// TestSourceAnalysisRefusesSymlinkReplacement covers a file or a directory
// swapped for a symlink, before and during the probe: the replacement is never
// read as the inventoried file.
func TestSourceAnalysisRefusesSymlinkReplacement(t *testing.T) {
	t.Run("file replaced before the probe", func(t *testing.T) {
		fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
		target := filepath.Join(t.TempDir(), "target.flac")
		if err := os.WriteFile(target, []byte("source bytes are only ever read"), 0o644); err != nil {
			t.Fatalf("write the symlink target: %v", err)
		}
		if err := os.Remove(fixture.filePath); err != nil {
			t.Fatalf("remove the source file: %v", err)
		}
		if err := os.Symlink(target, fixture.filePath); err != nil {
			t.Fatalf("replace the source file with a symlink: %v", err)
		}
		if _, err := fixture.run(); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
			t.Fatalf("analysis error = %v, want %v", err, persistence.ErrSourceAnalysisStale)
		}
		if len(fixture.probe.probed) != 0 {
			t.Fatalf("probed paths = %v, want no probe through a symlink", fixture.probe.probed)
		}
	})
	t.Run("file replaced during the probe", func(t *testing.T) {
		fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
		target := filepath.Join(t.TempDir(), "target.flac")
		if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
			t.Fatalf("write the symlink target: %v", err)
		}
		fixture.probe.onProbe = func(string) {
			if err := os.Remove(fixture.filePath); err != nil {
				t.Errorf("remove the source file: %v", err)
				return
			}
			if err := os.Symlink(target, fixture.filePath); err != nil {
				t.Errorf("replace the source file with a symlink: %v", err)
			}
		}
		if _, err := fixture.run(); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
			t.Fatalf("analysis error = %v, want %v", err, persistence.ErrSourceAnalysisStale)
		}
	})
	t.Run("directory replaced during the probe", func(t *testing.T) {
		fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
		outside := t.TempDir()
		album := filepath.Join(outside, "album")
		if err := os.MkdirAll(album, 0o755); err != nil {
			t.Fatalf("create the outside directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(album, "track.flac"), []byte("outside bytes"), 0o644); err != nil {
			t.Fatalf("write the outside file: %v", err)
		}
		fixture.probe.onProbe = func(string) {
			within := filepath.Join(fixture.rootDir, "album")
			if err := os.RemoveAll(within); err != nil {
				t.Errorf("remove the source directory: %v", err)
				return
			}
			if err := os.Symlink(album, within); err != nil {
				t.Errorf("replace the source directory with a symlink: %v", err)
			}
		}
		if _, err := fixture.run(); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
			t.Fatalf("analysis error = %v, want %v", err, persistence.ErrSourceAnalysisStale)
		}
	})
}

// TestSourceAnalysisRefusesAnEscapingPath covers a snapshot relative path that
// leaves the root or is absolute, whether by traversal or by construction.
func TestSourceAnalysisRefusesAnEscapingPath(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"traversal", filepath.Join("..", "escaped.flac")},
		{"deeper traversal", filepath.Join("album", "..", "..", "escaped.flac")},
		{"absolute", filepath.Join(string(filepath.Separator), "etc", "passwd")},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
			fixture.repository.location.RelativePath = testCase.path
			fixture.snapshot.RelativePath = testCase.path
			if _, err := fixture.run(); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
				t.Fatalf("analysis error = %v, want %v", err, persistence.ErrSourceAnalysisStale)
			}
			if len(fixture.probe.probed) != 0 {
				t.Fatalf("probed paths = %v, want no probe outside the root", fixture.probe.probed)
			}
		})
	}
}
