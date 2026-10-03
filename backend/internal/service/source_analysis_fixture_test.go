package service_test

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// The production types satisfy the analysis seams without a database or a real
// ffprobe.
var (
	_ service.InstallationVerifier = (*tools.Lifecycle)(nil)
	_ service.SourceTechnicalProbe = (*tools.FFProbe)(nil)
	_ service.ToolsDirectoryReader = (*settings.Registry)(nil)
)

const (
	sourceAnalysisGOOS    = "linux"
	sourceAnalysisGOARCH  = "amd64"
	sourceAnalysisRelease = "8.0"
	// The version outputs deliberately carry a patch version the catalog release
	// identity does not, so a test can tell the fresh query apart from the cache.
	sourceAnalysisFFmpegVersion  = "ffmpeg version 8.0.1 Copyright (c) 2000-2025 the FFmpeg developers"
	sourceAnalysisFFprobeVersion = "ffprobe version 8.0.1 Copyright (c) 2007-2025 the FFmpeg developers"
)

// sourceAnalysisBaseMtime is microsecond-aligned so the inventory identity can
// be compared while the real file carries a sub-microsecond offset.
var sourceAnalysisBaseMtime = time.Unix(1700000000, 0).UTC()

type sourceAnalysisRepositoryFixture struct {
	root              *persistence.SourceRoot
	location          *persistence.SourceLocation
	installation      *persistence.ToolInstallation
	rootErr           error
	locationErr       error
	installationErr   error
	rootReads         int
	locationReads     int
	installationReads int
}

func (f *sourceAnalysisRepositoryFixture) GetSourceRoot(_ context.Context, id uuid.UUID) (*persistence.SourceRoot, error) {
	f.rootReads++
	if f.rootErr != nil {
		return nil, f.rootErr
	}
	if id != f.root.ID {
		return nil, sql.ErrNoRows
	}
	return f.root, nil
}

func (f *sourceAnalysisRepositoryFixture) GetSourceLocation(_ context.Context, rootID, locationID uuid.UUID) (*persistence.SourceLocation, error) {
	f.locationReads++
	if f.locationErr != nil {
		return nil, f.locationErr
	}
	if locationID != f.location.ID || rootID != f.location.SourceRootID {
		return nil, persistence.ErrSourceLocationNotFound
	}
	return f.location, nil
}

func (f *sourceAnalysisRepositoryFixture) GetInstallation(_ context.Context, id uuid.UUID) (*persistence.ToolInstallation, error) {
	f.installationReads++
	if f.installationErr != nil {
		return nil, f.installationErr
	}
	if id != f.installation.ID {
		return nil, sql.ErrNoRows
	}
	return f.installation, nil
}

type sourceAnalysisToolsFixture struct {
	directory string
	exists    bool
	err       error
}

func (f sourceAnalysisToolsFixture) GetToolsDirectory(context.Context) (string, bool, error) {
	return f.directory, f.exists, f.err
}

// sourceAnalysisProbeFixture stands in for the managed ffprobe. beforeRead runs
// while the borrowed descriptor is active and immediately before it is read;
// onProbe runs after the read for mutations that remain in place afterward.
type sourceAnalysisProbeFixture struct {
	raw          []byte
	err          error
	executable   string
	probed       []string
	beforeRead   func(ctx context.Context, handle *os.File) (afterRead func())
	onProbe      func(absolutePath string)
	ignoreCancel bool
	path         string
	read         []byte
}

func (f *sourceAnalysisProbeFixture) CheckFileTransport(context.Context) error { return nil }

func (f *sourceAnalysisProbeFixture) ProbeTechnicalFile(ctx context.Context, file sourcefs.RegularFile) ([]byte, error) {
	f.probed = append(f.probed, f.path)
	if f.err != nil {
		return nil, f.err
	}
	if err := file.Borrow(ctx, func(handle *os.File) error {
		var afterRead func()
		if f.beforeRead != nil {
			afterRead = f.beforeRead(ctx, handle)
		}
		var err error
		f.read, err = io.ReadAll(handle)
		if afterRead != nil {
			afterRead()
		}
		if err != nil {
			return err
		}
		// Preserve post-read mutations for tests that verify service checks detect
		// changes that remain in place after the descriptor loan returns.
		if f.onProbe != nil {
			f.onProbe(f.path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil && !f.ignoreCancel {
		return nil, err
	}
	return f.raw, nil
}

// sourceAnalysisCommandRunner runs the real tools.Lifecycle version query
// without spawning a process, so the analysis test exercises the actual
// lifecycle path (argv and version matching) against a fake subprocess.
type sourceAnalysisCommandRunner struct {
	argv    [][]string
	outputs map[string]string
	err     error
}

func (r *sourceAnalysisCommandRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	r.argv = append(r.argv, append([]string{executable}, args...))
	if r.err != nil {
		return nil, r.err
	}
	name := strings.TrimSuffix(filepath.Base(executable), filepath.Ext(executable))
	return []byte(r.outputs[name]), nil
}

type sourceAnalysisFixture struct {
	analysis     *service.SourceAnalysis
	repository   *sourceAnalysisRepositoryFixture
	probe        *sourceAnalysisProbeFixture
	runner       *sourceAnalysisCommandRunner
	rootDir      string
	filePath     string
	relativePath string
	managedRoot  string
	managedRel   string
	snapshot     persistence.SourceAnalysisSnapshot
	operationID  uuid.UUID
	previousID   uuid.UUID
}

func newSourceAnalysisFixture(t *testing.T, raw []byte) sourceAnalysisFixture {
	t.Helper()
	operationID, rootID, locationID, installationID, previousID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()

	rootDir := t.TempDir()
	relativePath := filepath.Join("album", "track.flac")
	filePath := filepath.Join(rootDir, relativePath)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("create the source directory: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("source bytes are only ever read"), 0o644); err != nil {
		t.Fatalf("write the source file: %v", err)
	}
	actualMtime := sourceAnalysisBaseMtime.Add(500 * time.Nanosecond)
	if err := os.Chtimes(filePath, actualMtime, actualMtime); err != nil {
		t.Fatalf("set the source mtime: %v", err)
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		t.Fatalf("stat the source file: %v", err)
	}

	managedRoot := t.TempDir()
	managedRel := filepath.Join(string(tools.PackageFFmpeg), sourceAnalysisRelease)
	for _, name := range tools.ExpectedExecutables(tools.PackageFFmpeg, sourceAnalysisGOOS) {
		path := filepath.Join(managedRoot, managedRel, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create the managed directory: %v", err)
		}
		if err := os.WriteFile(path, []byte("managed executable"), 0o755); err != nil {
			t.Fatalf("write the managed executable: %v", err)
		}
	}
	runner := &sourceAnalysisCommandRunner{outputs: map[string]string{
		"ffmpeg": sourceAnalysisFFmpegVersion, "ffprobe": sourceAnalysisFFprobeVersion,
	}}
	probe := &sourceAnalysisProbeFixture{raw: raw}
	probe.path = filePath
	configured, err := filepath.EvalSymlinks(rootDir)
	if err != nil {
		t.Fatalf("resolve source root path: %v", err)
	}
	repository := &sourceAnalysisRepositoryFixture{
		root: &persistence.SourceRoot{ID: rootID, ConfiguredPath: configured, InventoryPath: &configured, Enabled: true, Status: persistence.SourceRootStatusAvailable},
		location: &persistence.SourceLocation{
			ID: locationID, SourceRootID: rootID, RelativePath: relativePath,
			SizeBytes: info.Size(), Mtime: sourceAnalysisBaseMtime,
			ProbeStatus: persistence.SourceProbeStatusAudio, MediaVariantID: &previousID,
		},
		installation: &persistence.ToolInstallation{
			ID: installationID, PackageKind: string(tools.PackageFFmpeg),
			PlatformGOOS: sourceAnalysisGOOS, PlatformGOARCH: sourceAnalysisGOARCH,
			ReleaseIdentity: sourceAnalysisRelease, RelativePath: managedRel, State: "ready",
		},
	}
	snapshot := persistence.SourceAnalysisSnapshot{
		SchemaVersion: persistence.SourceAnalysisSnapshotVersion,
		SourceRootID:  rootID, SourceLocationID: locationID,
		ConfiguredPath: configured, InventoryPath: configured,
		RelativePath: relativePath, SizeBytes: info.Size(), Mtime: sourceAnalysisBaseMtime,
		PreviousVariantID:      &previousID,
		AnalysisPolicyVersion:  persistence.SourceAnalysisPolicyVersion,
		AnalysisInstallationID: installationID,
	}
	analysis := newSourceAnalysisWithTools(
		sourceAnalysisFixture{repository: repository, probe: probe, runner: runner},
		sourceAnalysisToolsFixture{directory: managedRoot, exists: true},
	)
	return sourceAnalysisFixture{
		analysis: analysis, repository: repository, probe: probe, runner: runner,
		rootDir: rootDir, filePath: filePath, relativePath: relativePath,
		managedRoot: managedRoot, managedRel: managedRel,
		snapshot: snapshot, operationID: operationID, previousID: previousID,
	}
}

func sourceAnalysisRaw(t *testing.T) []byte {
	t.Helper()
	return []byte(`{"format":{"format_name":"flac","duration":"12.5","bit_rate":"900000","tags":{"ARTIST":"Example","album":"Example Album"}},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","sample_rate":"44100","channels":2,"tags":{"TITLE":"Track"}}]}`)
}

func (fixture sourceAnalysisFixture) run() (persistence.SourceAnalysisApply, error) {
	return fixture.runContext(context.Background())
}

func (fixture sourceAnalysisFixture) runContext(ctx context.Context) (persistence.SourceAnalysisApply, error) {
	return fixture.analysis.Run(ctx, service.SourceAnalysisRequest{
		OperationID: fixture.operationID, Snapshot: fixture.snapshot,
	})
}

// newSourceAnalysisWithTools rebuilds the analysis over a substitute managed
// tools directory, keeping the same repository, runner and probe. It lets a
// test exercise a tools directory that is missing or unavailable.
func newSourceAnalysisWithTools(fixture sourceAnalysisFixture, toolsDirectory sourceAnalysisToolsFixture) *service.SourceAnalysis {
	probe := fixture.probe
	return service.NewSourceAnalysis(fixture.repository, toolsDirectory,
		settings.Platform{GOOS: sourceAnalysisGOOS, GOARCH: sourceAnalysisGOARCH},
		service.WithSourceAnalysisVerifier(tools.NewLifecycle(fixture.runner)),
		service.WithSourceTechnicalProbeFactory(func(executable string) (service.SourceTechnicalProbe, error) {
			probe.executable = executable
			return probe, nil
		}),
	)
}
