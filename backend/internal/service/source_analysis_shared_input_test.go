package service

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
)

// SHA calculation is an independent step: its failure must not prevent the
// requested probe and fingerprint runners from being attempted.
func TestSourceAnalysisPreparerSHAFailureDoesNotSuppressSiblingRunners(t *testing.T) {
	file := preparerFile{path: t.TempDir() + "/missing-audio"}
	var probes, fingerprints atomic.Int32
	config := preparerConfig()
	config.ProbeFactory = func(string) (SourceAnalysisProbe, error) {
		return preparerProbe(func(context.Context, sourcefs.RegularFile) ([]byte, error) {
			probes.Add(1)
			return []byte(`{"format":{},"streams":[]}`), nil
		}), nil
	}
	config.FingerprinterFactory = func(string) (SourceAnalysisFingerprinter, error) {
		return &preparerFingerprinter{version: config.FPCalcVersion, fingerprint: func(context.Context, string) (tools.FPCalcResult, error) {
			fingerprints.Add(1)
			return tools.FPCalcResult{Fingerprint: "fresh"}, nil
		}}, nil
	}
	result := NewSourceAnalysisPreparer(config).Prepare(context.Background(), SourceAnalysisPrepareRequest{
		File: file, ServerPath: "/server/audio.flac",
		Targets: SourceAnalysisTargetSHA256 | SourceAnalysisTargetProbe | SourceAnalysisTargetFingerprint,
	})
	if result.SHA256.State != SourceAnalysisFailed {
		t.Fatalf("SHA state = %q, want failed", result.SHA256.State)
	}
	if probes.Load() != 1 || fingerprints.Load() != 1 {
		t.Fatalf("sibling runners: probe=%d fingerprint=%d, want one each", probes.Load(), fingerprints.Load())
	}
	if result.Probe.State != SourceAnalysisSucceeded || result.Fingerprint.State != SourceAnalysisSucceeded {
		t.Fatalf("sibling outcomes were suppressed: probe=%#v fingerprint=%#v", result.Probe, result.Fingerprint)
	}
}
