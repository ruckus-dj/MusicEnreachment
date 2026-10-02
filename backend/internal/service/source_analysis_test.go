package service_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// TestSourceAnalysisProbesOneReadOnlyFileAndPreparesTheApply pins the whole
// success contract: one probe of the exact resolved file, the fresh actual
// managed version, the preserved raw response and tags, and no write to the
// source, no scratch and no repository mutation.
func TestSourceAnalysisProbesOneReadOnlyFileAndPreparesTheApply(t *testing.T) {
	raw := sourceAnalysisRaw(t)
	fixture := newSourceAnalysisFixture(t, raw)
	beforeBytes, err := os.ReadFile(fixture.filePath)
	if err != nil {
		t.Fatalf("read the source before the analysis: %v", err)
	}
	beforeInfo, err := os.Lstat(fixture.filePath)
	if err != nil {
		t.Fatalf("stat the source before the analysis: %v", err)
	}

	apply, err := fixture.run()
	if err != nil {
		t.Fatalf("analyze the source location: %v", err)
	}
	if apply.OperationID != fixture.operationID || apply.RelativePath != fixture.relativePath {
		t.Fatalf("apply identity = %+v, want the pinned operation and path", apply)
	}
	if apply.SizeBytes != beforeInfo.Size() ||
		!apply.Mtime.Truncate(time.Microsecond).Equal(sourceAnalysisBaseMtime) {
		t.Fatalf("apply identity = size %d mtime %v, want the pinned file", apply.SizeBytes, apply.Mtime)
	}
	if apply.AnalysisPolicyVersion != persistence.SourceAnalysisPolicyVersion || apply.InspectedAt.IsZero() {
		t.Fatalf("apply policy/time = %d/%v, want the current policy and an inspection time", apply.AnalysisPolicyVersion, apply.InspectedAt)
	}
	if !bytes.Equal(apply.FFProbeJSON, raw) {
		t.Fatalf("apply raw JSON = %s, want the exact probe response", apply.FFProbeJSON)
	}
	if apply.FFProbeVersion != sourceAnalysisFFprobeVersion || apply.FFProbeVersion == sourceAnalysisRelease {
		t.Fatalf("apply ffprobe version = %q, want the fresh query result, not the catalog release", apply.FFProbeVersion)
	}
	var tags map[string][]string
	if err := json.Unmarshal(apply.ObservedTags, &tags); err != nil {
		t.Fatalf("decode observed tags: %v", err)
	}
	if !slices.Equal(tags["ARTIST"], []string{"Example"}) ||
		!slices.Equal(tags["ALBUM"], []string{"Example Album"}) ||
		!slices.Equal(tags["TITLE"], []string{"Track"}) {
		t.Fatalf("observed tags = %v, want the uppercase format-then-stream tags", tags)
	}
	if len(fixture.probe.probed) != 1 || fixture.probe.probed[0] != fixture.filePath {
		t.Fatalf("probed paths = %v, want exactly %q", fixture.probe.probed, fixture.filePath)
	}
	wantExecutable := filepath.Join(fixture.managedRoot, fixture.managedRel, "ffprobe")
	if fixture.probe.executable != wantExecutable {
		t.Fatalf("probe executable = %q, want the managed %q", fixture.probe.executable, wantExecutable)
	}
	assertSourceAnalysisVersionArgv(t, fixture.runner, fixture.managedRoot, fixture.managedRel)

	afterBytes, err := os.ReadFile(fixture.filePath)
	if err != nil {
		t.Fatalf("read the source after the analysis: %v", err)
	}
	afterInfo, err := os.Lstat(fixture.filePath)
	if err != nil {
		t.Fatalf("stat the source after the analysis: %v", err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) || afterInfo.Size() != beforeInfo.Size() ||
		!afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("the analysis wrote to the source file: size %d mtime %v", afterInfo.Size(), afterInfo.ModTime())
	}
	assertSourceAnalysisNoScratch(t, fixture.rootDir, fixture.relativePath)
	if fixture.repository.rootReads != 1 || fixture.repository.locationReads != 1 ||
		fixture.repository.installationReads != 1 {
		t.Fatalf("repository reads = %d/%d/%d, want one read each and no mutation",
			fixture.repository.rootReads, fixture.repository.locationReads, fixture.repository.installationReads)
	}
}

// TestSourceAnalysisDefaultProbeFactoryBuildsTheManagedFFProbe pins that the
// production default is the real bounded technical ffprobe over an absolute
// managed path, not a no-op seam.
func TestSourceAnalysisDefaultProbeFactoryBuildsTheManagedFFProbe(t *testing.T) {
	probe, err := service.NewManagedSourceTechnicalProbe(filepath.Join(t.TempDir(), "ffprobe"))
	if err != nil {
		t.Fatalf("build the production probe: %v", err)
	}
	if _, ok := probe.(*tools.FFProbe); !ok {
		t.Fatalf("production probe = %T, want *tools.FFProbe", probe)
	}
	if _, err := service.NewManagedSourceTechnicalProbe("ffprobe"); err == nil {
		t.Fatalf("a relative executable must be refused")
	}
}

// assertSourceAnalysisVersionArgv proves the real lifecycle version query ran
// with the exact `<absolute managed executable> -version` argv for every managed
// FFmpeg executable.
func assertSourceAnalysisVersionArgv(t *testing.T, runner *sourceAnalysisCommandRunner, managedRoot, managedRel string) {
	t.Helper()
	expected := tools.ExpectedExecutables(tools.PackageFFmpeg, sourceAnalysisGOOS)
	if len(runner.argv) != len(expected) {
		t.Fatalf("version queries = %v, want one per managed ffmpeg executable", runner.argv)
	}
	for index, name := range expected {
		want := filepath.Join(managedRoot, managedRel, name)
		if len(runner.argv[index]) != 2 || runner.argv[index][0] != want || runner.argv[index][1] != "-version" {
			t.Fatalf("version query %d = %v, want %q -version", index, runner.argv[index], want)
		}
	}
}

// assertSourceAnalysisNoScratch proves the analysis created no file below the
// source root: only the directory and the inventoried file exist.
func assertSourceAnalysisNoScratch(t *testing.T, rootDir, relativePath string) {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		if relative != "." {
			entries = append(entries, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the source root: %v", err)
	}
	want := []string{filepath.Dir(relativePath), relativePath}
	slices.Sort(entries)
	slices.Sort(want)
	if !slices.Equal(entries, want) {
		t.Fatalf("source root entries = %v, want %v", entries, want)
	}
}
