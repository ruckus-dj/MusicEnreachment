package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// TestSourceAnalysisRefusesAnUnavailableManagedFFProbe covers the tool refusals:
// each fails as unavailable, probes nothing and prepares no result.
func TestSourceAnalysisRefusesAnUnavailableManagedFFProbe(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*sourceAnalysisFixture)
	}{
		{"missing installation", func(f *sourceAnalysisFixture) {
			f.repository.installationErr = errors.New("no such installation")
		}},
		{"missing pinned installation id", func(f *sourceAnalysisFixture) {
			f.snapshot.AnalysisInstallationID = uuid.New()
		}},
		{"installation not ready", func(f *sourceAnalysisFixture) {
			f.repository.installation.State = "preparing"
		}},
		{"installation of another platform", func(f *sourceAnalysisFixture) {
			f.repository.installation.PlatformGOOS = "darwin"
		}},
		{"invalid managed path", func(f *sourceAnalysisFixture) {
			f.repository.installation.RelativePath = "ffmpeg/9.9"
		}},
		{"managed tools directory missing", func(f *sourceAnalysisFixture) {
			f.analysis = newSourceAnalysisWithTools(*f, sourceAnalysisToolsFixture{})
		}},
		{"version query fails", func(f *sourceAnalysisFixture) {
			f.runner.err = errors.New("the executable is gone")
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
			testCase.mutate(&fixture)
			_, err := fixture.run()
			if !errors.Is(err, service.ErrSourceAnalysisToolUnavailable) {
				t.Fatalf("analysis error = %v, want %v", err, service.ErrSourceAnalysisToolUnavailable)
			}
			if len(fixture.probe.probed) != 0 {
				t.Fatalf("probed paths = %v, want no probe with an unavailable tool", fixture.probe.probed)
			}
		})
	}
}

func TestSourceAnalysisRefusesRootLocationMismatch(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*sourceAnalysisFixture)
	}{
		{"another root id", func(f *sourceAnalysisFixture) { f.snapshot.SourceRootID = uuid.New() }},
		{"another location id", func(f *sourceAnalysisFixture) { f.snapshot.SourceLocationID = uuid.New() }},
		{"location belongs to another root", func(f *sourceAnalysisFixture) { f.repository.location.SourceRootID = uuid.New() }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
			testCase.mutate(&fixture)
			apply, err := fixture.run()
			if err == nil {
				t.Fatal("mismatched root/location was accepted")
			}
			if apply.OperationID != uuid.Nil || apply.FFProbeJSON != nil || apply.ObservedTags != nil {
				t.Fatalf("mismatched root/location prepared a result: %+v", apply)
			}
			if len(fixture.probe.probed) != 0 {
				t.Fatalf("probed paths = %v, want no probe of a mismatched location", fixture.probe.probed)
			}
		})
	}
}

// TestSourceAnalysisRefusesAMissingManagedExecutable proves the fresh version
// query is the real check: a managed ffprobe removed after materialization is
// refused before any source file is probed.
func TestSourceAnalysisRefusesAMissingManagedExecutable(t *testing.T) {
	fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
	executables := tools.ExpectedExecutables(tools.PackageFFmpeg, sourceAnalysisGOOS)
	if err := os.Remove(filepath.Join(fixture.managedRoot, fixture.managedRel, executables[1])); err != nil {
		t.Fatalf("remove the managed ffprobe: %v", err)
	}
	if _, err := fixture.run(); !errors.Is(err, service.ErrSourceAnalysisToolUnavailable) {
		t.Fatalf("analysis error = %v, want %v", err, service.ErrSourceAnalysisToolUnavailable)
	}
	if len(fixture.probe.probed) != 0 {
		t.Fatalf("probed paths = %v, want no probe with a missing executable", fixture.probe.probed)
	}
}

// TestSourceAnalysisMalformedProbeKeepsThePreviousVariant proves a probe whose
// mandatory structure is broken fails the analysis and leaves the previous
// variant linked, because nothing is applied here.
func TestSourceAnalysisMalformedProbeKeepsThePreviousVariant(t *testing.T) {
	fixture := newSourceAnalysisFixture(t, []byte(`{"format":{},"streams":[]}`))
	if _, err := fixture.run(); !errors.Is(err, service.ErrSourceTechnicalMalformed) {
		t.Fatalf("analysis error = %v, want %v", err, service.ErrSourceTechnicalMalformed)
	}
	if fixture.repository.location.MediaVariantID == nil ||
		*fixture.repository.location.MediaVariantID != fixture.previousID {
		t.Fatalf("previous variant link = %v, want %s unchanged",
			fixture.repository.location.MediaVariantID, fixture.previousID)
	}
}

// TestSourceAnalysisHonorsCancellationAtTheProbe cancels exactly when the probe
// runs (the barrier) and requires the context error to surface, so the caller
// can retry the whole operation.
func TestSourceAnalysisHonorsCancellationAtTheProbe(t *testing.T) {
	fixture := newSourceAnalysisFixture(t, sourceAnalysisRaw(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.probe.onProbe = func(string) { cancel() }
	_, err := fixture.analysis.Run(ctx, service.SourceAnalysisRequest{
		OperationID: fixture.operationID, Snapshot: fixture.snapshot,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("analysis error = %v, want context.Canceled", err)
	}
}
