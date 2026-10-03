package service_test

import (
	"context"
	"database/sql"
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
		{"root row missing", func(f *sourceAnalysisFixture) { f.repository.rootErr = sql.ErrNoRows }},
		{"location row missing", func(f *sourceAnalysisFixture) {
			f.repository.locationErr = persistence.ErrSourceLocationNotFound
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

func TestSourceAnalysisUsesTheBorrowedFileDuringTransientSymlinkSwaps(t *testing.T) {
	for _, swap := range []string{"file", "ancestor"} {
		t.Run(swap, func(t *testing.T) {
			fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
			originalBytes, err := os.ReadFile(fixture.filePath)
			if err != nil {
				t.Fatalf("read original source: %v", err)
			}
			originalInfo, err := os.Stat(fixture.filePath)
			if err != nil {
				t.Fatalf("stat original source: %v", err)
			}
			externalDir := t.TempDir()
			externalAlbum := filepath.Join(externalDir, "album")
			if err := os.Mkdir(externalAlbum, 0o755); err != nil {
				t.Fatalf("create external album: %v", err)
			}
			externalPath := filepath.Join(externalAlbum, "track.flac")
			externalBytes := make([]byte, len(originalBytes))
			copy(externalBytes, "EXTERNAL")
			for i := len("EXTERNAL"); i < len(externalBytes); i++ {
				externalBytes[i] = '!'
			}
			if err := os.WriteFile(externalPath, externalBytes, 0o644); err != nil {
				t.Fatalf("write external source: %v", err)
			}
			if err := os.Chtimes(externalPath, originalInfo.ModTime(), originalInfo.ModTime()); err != nil {
				t.Fatalf("set external source mtime: %v", err)
			}

			fixture.probe.beforeRead = func(_ context.Context, _ *os.File) func() {
				var restore func()
				switch swap {
				case "file":
					held := fixture.filePath + ".held"
					if err := os.Rename(fixture.filePath, held); err != nil {
						t.Errorf("hold original file: %v", err)
						return nil
					}
					if err := os.Symlink(externalPath, fixture.filePath); err != nil {
						_ = os.Rename(held, fixture.filePath)
						t.Errorf("swap file path to external symlink: %v", err)
						return nil
					}
					restore = func() {
						if err := os.Remove(fixture.filePath); err != nil {
							t.Errorf("remove temporary file symlink: %v", err)
							return
						}
						if err := os.Rename(held, fixture.filePath); err != nil {
							t.Errorf("restore original file: %v", err)
							return
						}
					}
				case "ancestor":
					held := filepath.Join(fixture.rootDir, "album.held")
					if err := os.Rename(filepath.Dir(fixture.filePath), held); err != nil {
						t.Errorf("hold original ancestor: %v", err)
						return nil
					}
					if err := os.Symlink(externalAlbum, filepath.Dir(fixture.filePath)); err != nil {
						_ = os.Rename(held, filepath.Dir(fixture.filePath))
						t.Errorf("swap ancestor to external symlink: %v", err)
						return nil
					}
					restore = func() {
						if err := os.Remove(filepath.Dir(fixture.filePath)); err != nil {
							t.Errorf("remove temporary ancestor symlink: %v", err)
							return
						}
						if err := os.Rename(held, filepath.Dir(fixture.filePath)); err != nil {
							t.Errorf("restore original ancestor: %v", err)
							return
						}
					}
				}
				if restore == nil {
					return nil
				}
				visibleBytes, err := os.ReadFile(fixture.filePath)
				if err != nil {
					t.Errorf("read source path during borrowed-file read: %v", err)
				} else if string(visibleBytes) != string(externalBytes) {
					t.Errorf("source path during borrowed-file read = %q, want external marker %q", visibleBytes, externalBytes)
				}
				return restore
			}
			fixture.probe.onProbe = func(string) {
				if string(fixture.probe.read) == string(originalBytes) {
					fixture.probe.raw = sourceAnalysisRaw(t)
				} else {
					fixture.probe.raw = []byte(`{"format":{"format_name":"flac","tags":{"ARTIST":"EXTERNAL"}},"streams":[{"codec_type":"audio","codec_name":"flac"}]}`)
				}
			}

			apply, err := fixture.run()
			if err != nil {
				t.Fatalf("analyze source with transient %s swap: %v", swap, err)
			}
			if got := string(fixture.probe.read); got != string(originalBytes) {
				t.Fatalf("borrowed bytes = %q, want original %q", got, originalBytes)
			}
			if got := string(apply.ObservedTags); got != `{"ALBUM":["Example Album"],"ARTIST":["Example"],"TITLE":["Track"]}` {
				t.Fatalf("observed tags = %s, want tags from original borrowed bytes", got)
			}
		})
	}
}

func TestSourceAnalysisRefusesSameIdentityMetadataNamespaceReplacement(t *testing.T) {
	fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
	fixture.probe.onProbe = func(string) {
		info, err := os.Stat(fixture.filePath)
		if err != nil {
			t.Errorf("stat source before replacement: %v", err)
			return
		}
		if err := os.Remove(fixture.filePath); err != nil {
			t.Errorf("remove source before replacement: %v", err)
			return
		}
		replacement := make([]byte, info.Size())
		copy(replacement, "replacement marker")
		for i := len("replacement marker"); i < len(replacement); i++ {
			replacement[i] = '!'
		}
		if err := os.WriteFile(fixture.filePath, replacement, 0o644); err != nil {
			t.Errorf("write same-size replacement: %v", err)
			return
		}
		if err := os.Chtimes(fixture.filePath, info.ModTime(), info.ModTime()); err != nil {
			t.Errorf("match replacement mtime: %v", err)
		}
	}
	if _, err := fixture.run(); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("analysis error = %v, want stale namespace replacement", err)
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

func TestSourceAnalysisCancellationDuringPostProbeCheckIsNotStale(t *testing.T) {
	fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.probe.ignoreCancel = true
	fixture.probe.onProbe = func(string) { cancel() }
	if _, err := fixture.runContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("analysis error = %v, want context.Canceled", err)
	} else if errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("canceled post-probe check was classified stale: %v", err)
	}
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
