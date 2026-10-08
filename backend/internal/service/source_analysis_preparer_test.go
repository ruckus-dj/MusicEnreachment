package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type preparerFile struct {
	path      string
	borrowErr error
}

func (file preparerFile) Stat(context.Context) (fs.FileInfo, error) { return os.Stat(file.path) }
func (file preparerFile) Borrow(ctx context.Context, use func(*os.File) error) error {
	if file.borrowErr != nil {
		return file.borrowErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	handle, err := os.Open(file.path)
	if err != nil {
		return err
	}
	defer func() { _ = handle.Close() }()
	return use(handle)
}
func (preparerFile) Close() error { return nil }

type preparerProbe func(context.Context, sourcefs.RegularFile) ([]byte, error)

func (probe preparerProbe) ProbeMediaFile(ctx context.Context, file sourcefs.RegularFile) ([]byte, error) {
	return probe(ctx, file)
}

type preparerFingerprinter struct {
	versionCalls atomic.Int32
	fingerprint  func(context.Context, string) (tools.FPCalcResult, error)
	version      tools.FPCalcVersion
}

func (fingerprinter *preparerFingerprinter) Version(context.Context) (tools.FPCalcVersion, error) {
	fingerprinter.versionCalls.Add(1)
	return fingerprinter.version, nil
}
func (fingerprinter *preparerFingerprinter) Fingerprint(ctx context.Context, path string) (tools.FPCalcResult, error) {
	return fingerprinter.fingerprint(ctx, path)
}

type preparerCache struct {
	probeLookup       func(context.Context, [sha256.Size]byte, string, int) (*persistence.SourceMediaVariant, bool, error)
	fingerprintLookup func(context.Context, [sha256.Size]byte) (*persistence.SourceFingerprintResult, bool, error)
	probeCalls        atomic.Int32
	fingerprintCalls  atomic.Int32
	mu                sync.Mutex
	digest            [sha256.Size]byte
}

func (cache *preparerCache) LookupSourceProbe(ctx context.Context, digest [sha256.Size]byte, version string, policy int) (*persistence.SourceMediaVariant, bool, error) {
	cache.probeCalls.Add(1)
	cache.mu.Lock()
	cache.digest = digest
	cache.mu.Unlock()
	if cache.probeLookup == nil {
		return nil, false, nil
	}
	return cache.probeLookup(ctx, digest, version, policy)
}
func (cache *preparerCache) LookupSourceFingerprint(ctx context.Context, digest [sha256.Size]byte) (*persistence.SourceFingerprintResult, bool, error) {
	cache.fingerprintCalls.Add(1)
	cache.mu.Lock()
	cache.digest = digest
	cache.mu.Unlock()
	if cache.fingerprintLookup == nil {
		return nil, false, nil
	}
	return cache.fingerprintLookup(ctx, digest)
}

func (cache *preparerCache) lastDigest() [sha256.Size]byte {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.digest
}

func preparerTestFile(t *testing.T, contents string) preparerFile {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audio.flac")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return preparerFile{path: path}
}

func preparerConfig() SourceAnalysisPreparerConfig {
	return SourceAnalysisPreparerConfig{
		ProbeExecutable:  "/managed/ffprobe",
		FFProbeVersion:   "ffprobe 8.0 verified banner",
		AnalysisPolicy:   3,
		FPCalcExecutable: "/managed/fpcalc",
		FPCalcVersion:    tools.FPCalcVersion{Version: "1.2.3", Banner: "fpcalc version 1.2.3"},
		ProbeResult: persistence.SourceMediaVariant{
			ID: uuid.New(), SizeBytes: 5, ObservedTags: []byte(`{"ARTIST":["artist"]}`),
			InspectedAt: ptr(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)), AppliedOperationID: ptr(uuid.New()),
		},
		FingerprintResult: persistence.SourceFingerprintResult{
			ID: uuid.New(), AlgorithmNamespace: "chromaprint", AppliedOperationID: uuid.New(),
			CalculatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), ParserContractVersion: 4,
		},
	}
}

func TestSourceAnalysisPreparerPersistsCompleteTechnicalAnalysis(t *testing.T) {
	file := preparerTestFile(t, "audio")
	raw := []byte(`{"format":{"format_name":"matroska","tags":{"artist":"Container","title":"same","ARTIST":"Container"}},"streams":[{"index":2,"codec_type":"audio","tags":{"title":"same","album":"Second"}},{"index":0,"codec_type":"audio","tags":{"artist":"First","title":"same"}}]}`)
	config := preparerConfig()
	operationID := uuid.New()
	config.ProbeResult.AppliedOperationID = &operationID
	config.ProbeFactory = func(string) (SourceAnalysisProbe, error) {
		return preparerProbe(func(context.Context, sourcefs.RegularFile) ([]byte, error) { return raw, nil }), nil
	}
	fingerprinter := &preparerFingerprinter{version: config.FPCalcVersion, fingerprint: func(context.Context, string) (tools.FPCalcResult, error) {
		return tools.FPCalcResult{Fingerprint: "fingerprint"}, nil
	}}
	config.FingerprinterFactory = func(string) (SourceAnalysisFingerprinter, error) { return fingerprinter, nil }
	result := NewSourceAnalysisPreparer(config).Prepare(context.Background(), SourceAnalysisPrepareRequest{
		File: file, ServerPath: "/server/audio.flac", Targets: SourceAnalysisTargetProbe | SourceAnalysisTargetFingerprint,
	})
	if result.Probe.State != SourceAnalysisSucceeded || result.Fingerprint.State != SourceAnalysisSucceeded {
		t.Fatalf("independent outcomes = (%q, %q)", result.Probe.State, result.Fingerprint.State)
	}
	if result.Probe.AudioStreamCount != 2 || result.Probe.MatchEligible {
		t.Errorf("probe stream metadata = count %d, eligible %t", result.Probe.AudioStreamCount, result.Probe.MatchEligible)
	}
	if !bytes.Equal(result.Probe.RawJSON, raw) || !bytes.Equal(result.Probe.Result.FFProbeJSON, raw) {
		t.Error("raw ffprobe payload was not preserved")
	}
	if result.Probe.Result.AppliedOperationID == nil || *result.Probe.Result.AppliedOperationID != operationID {
		t.Errorf("operation provenance changed: %+v", result.Probe.Result.AppliedOperationID)
	}
	var tags map[string][]string
	if err := json.Unmarshal(result.Probe.Result.ObservedTags, &tags); err != nil {
		t.Fatalf("decode observed tags: %v", err)
	}
	want := map[string][]string{"ARTIST": {"Container", "First"}, "TITLE": {"same"}, "ALBUM": {"Second"}}
	if !bytes.Equal(mustPreparerJSON(t, tags), mustPreparerJSON(t, want)) {
		t.Errorf("observed tags = %v, want %v", tags, want)
	}
}

func TestSourceAnalysisPreparerAcceptsNoAudioAndFailsMalformedProbeLocally(t *testing.T) {
	for _, test := range []struct {
		name       string
		raw        string
		probeState SourceAnalysisOutcomeState
	}{
		{name: "valid zero audio streams", raw: `{"format":{"tags":{"artist":"Container"}},"streams":[{"codec_type":"video"}]}`, probeState: SourceAnalysisSucceeded},
		{name: "malformed format", raw: `{"format":null,"streams":[]}`, probeState: SourceAnalysisFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := preparerConfig()
			config.ProbeFactory = func(string) (SourceAnalysisProbe, error) {
				return preparerProbe(func(context.Context, sourcefs.RegularFile) ([]byte, error) { return []byte(test.raw), nil }), nil
			}
			fingerprinter := &preparerFingerprinter{version: config.FPCalcVersion, fingerprint: func(context.Context, string) (tools.FPCalcResult, error) {
				return tools.FPCalcResult{Fingerprint: "still succeeds"}, nil
			}}
			config.FingerprinterFactory = func(string) (SourceAnalysisFingerprinter, error) { return fingerprinter, nil }
			result := NewSourceAnalysisPreparer(config).Prepare(context.Background(), SourceAnalysisPrepareRequest{
				File: preparerTestFile(t, "audio"), ServerPath: "/server/audio.flac", Targets: SourceAnalysisTargetProbe | SourceAnalysisTargetFingerprint,
			})
			if result.Probe.State != test.probeState || result.Fingerprint.State != SourceAnalysisSucceeded {
				t.Fatalf("outcomes = (%q, %q), want (%q, %q)", result.Probe.State, result.Fingerprint.State, test.probeState, SourceAnalysisSucceeded)
			}
			if test.probeState == SourceAnalysisSucceeded {
				if result.Probe.AudioStreamCount != 0 || result.Probe.MatchEligible || string(result.Probe.Result.ObservedTags) != `{"ARTIST":["Container"]}` {
					t.Errorf("zero-audio result = %+v, observed tags %s", result.Probe, result.Probe.Result.ObservedTags)
				}
			} else if result.Probe.SafeError != "managed ffprobe returned invalid technical metadata" {
				t.Errorf("malformed probe safe error = %q", result.Probe.SafeError)
			}
		})
	}
}

func mustPreparerJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestSourceAnalysisPreparerDoesNotStartRunnersBeforeIndependentCacheLookups(t *testing.T) {
	file := preparerTestFile(t, "audio")
	cache := &preparerCache{}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	probeLookupStarted := make(chan struct{})
	fingerprintLookupStarted := make(chan struct{})
	releaseProbeLookup := make(chan struct{})
	releaseFingerprintLookup := make(chan struct{})
	cache.probeLookup = func(context.Context, [sha256.Size]byte, string, int) (*persistence.SourceMediaVariant, bool, error) {
		close(probeLookupStarted)
		<-releaseProbeLookup
		return nil, false, nil
	}
	cache.fingerprintLookup = func(context.Context, [sha256.Size]byte) (*persistence.SourceFingerprintResult, bool, error) {
		close(fingerprintLookupStarted)
		<-releaseFingerprintLookup
		return nil, false, nil
	}
	config := preparerConfig()
	config.Cache = cache
	config.ProbeFactory = func(string) (SourceAnalysisProbe, error) {
		return preparerProbe(func(context.Context, sourcefs.RegularFile) ([]byte, error) {
			if cache.probeCalls.Load() != 1 || cache.fingerprintCalls.Load() != 1 {
				t.Error("probe started before both cache lookups completed")
			}
			started <- struct{}{}
			<-release
			return []byte(`{"format":{},"streams":[{"codec_type":"audio"}]}`), nil
		}), nil
	}
	fingerprinter := &preparerFingerprinter{version: config.FPCalcVersion, fingerprint: func(context.Context, string) (tools.FPCalcResult, error) {
		if cache.probeCalls.Load() != 1 || cache.fingerprintCalls.Load() != 1 {
			t.Error("fingerprint started before both cache lookups completed")
		}
		started <- struct{}{}
		<-release
		return tools.FPCalcResult{Algorithm: 1, Fingerprint: "fresh"}, nil
	}}
	config.FingerprinterFactory = func(string) (SourceAnalysisFingerprinter, error) { return fingerprinter, nil }
	preparer := NewSourceAnalysisPreparer(config)
	done := make(chan SourceAnalysisPreparation, 1)
	go func() {
		done <- preparer.Prepare(context.Background(), SourceAnalysisPrepareRequest{
			File: file, ServerPath: "/server/audio.flac",
			Targets: SourceAnalysisTargetSHA256 | SourceAnalysisTargetProbe | SourceAnalysisTargetFingerprint,
		})
	}()
	select {
	case <-probeLookupStarted:
	case <-time.After(time.Second):
		t.Fatal("probe cache lookup did not start")
	}
	select {
	case <-started:
		t.Fatal("tool runner started while probe lookup was in progress")
	default:
	}
	close(releaseProbeLookup)
	select {
	case <-fingerprintLookupStarted:
	case <-time.After(time.Second):
		t.Fatal("fingerprint cache lookup did not start")
	}
	select {
	case <-started:
		t.Fatal("tool runner started while fingerprint lookup was in progress")
	default:
	}
	close(releaseFingerprintLookup)
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("probe and fingerprint did not both start")
		}
	}
	close(release)
	result := <-done
	if result.SHA256.State != SourceAnalysisSucceeded || result.Probe.State != SourceAnalysisSucceeded || result.Fingerprint.State != SourceAnalysisSucceeded {
		t.Fatalf("unexpected outcomes: %#v", result)
	}
	if !result.Probe.MatchEligible || result.Fingerprint.Result.FPCalcVersion != "1.2.3" {
		t.Fatalf("unexpected successful metadata: %#v", result)
	}
	if cache.lastDigest() != sha256.Sum256([]byte("audio")) {
		t.Fatalf("lookups received digest %x", cache.lastDigest())
	}
}

func TestSourceAnalysisPreparerDefersOnlyToolsRootMoveHoldFailures(t *testing.T) {
	file := preparerTestFile(t, "audio")
	for _, test := range []struct {
		name string
		err  error
		want SourceAnalysisOutcomeState
	}{
		{name: "tools root move", err: persistence.ErrToolsRootMoveActive, want: SourceAnalysisDeferred},
		{name: "other hold error", err: errors.New("hold unavailable"), want: SourceAnalysisFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := preparerConfig()
			config.Hold = func(context.Context, SourceAnalysisStep) (func(), error) { return nil, test.err }
			config.ProbeFactory = func(string) (SourceAnalysisProbe, error) {
				t.Fatal("probe factory ran despite refused hold")
				return nil, nil
			}
			config.FingerprinterFactory = func(string) (SourceAnalysisFingerprinter, error) {
				t.Fatal("fingerprint factory ran despite refused hold")
				return nil, nil
			}
			result := NewSourceAnalysisPreparer(config).Prepare(context.Background(), SourceAnalysisPrepareRequest{
				File: file, ServerPath: "/audio", Targets: SourceAnalysisTargetProbe | SourceAnalysisTargetFingerprint,
			})
			if result.Probe.State != test.want || result.Fingerprint.State != test.want {
				t.Fatalf("outcomes = (%q, %q), want %q", result.Probe.State, result.Fingerprint.State, test.want)
			}
			if test.want == SourceAnalysisDeferred && (result.Probe.SafeError != "" || result.Fingerprint.SafeError != "") {
				t.Fatalf("deferred outcomes carried safe errors: %+v", result)
			}
		})
	}
}

func TestSourceAnalysisPreparerHashFailureDoesNotUseSuppliedDigestOrBlockRunners(t *testing.T) {
	file := preparerFile{borrowErr: errors.New("private path read failure")}
	var probeCalls, fingerprintCalls atomic.Int32
	config := preparerConfig()
	cache := &preparerCache{}
	config.Cache = cache
	config.ProbeFactory = func(string) (SourceAnalysisProbe, error) {
		return preparerProbe(func(context.Context, sourcefs.RegularFile) ([]byte, error) {
			probeCalls.Add(1)
			return []byte(`{"format":{},"streams":[]}`), nil
		}), nil
	}
	fingerprinter := &preparerFingerprinter{version: config.FPCalcVersion, fingerprint: func(context.Context, string) (tools.FPCalcResult, error) {
		fingerprintCalls.Add(1)
		return tools.FPCalcResult{Fingerprint: "ok"}, nil
	}}
	config.FingerprinterFactory = func(string) (SourceAnalysisFingerprinter, error) { return fingerprinter, nil }
	preparer := NewSourceAnalysisPreparer(config)
	existing := sha256.Sum256([]byte("stale"))
	result := preparer.Prepare(context.Background(), SourceAnalysisPrepareRequest{
		File: file, ServerPath: "/server/no-audio.flac", ExistingSHA256: &existing,
		Targets: SourceAnalysisTargetSHA256 | SourceAnalysisTargetProbe | SourceAnalysisTargetFingerprint,
	})
	if result.SHA256.State != SourceAnalysisFailed || result.Probe.State != SourceAnalysisSucceeded || result.Fingerprint.State != SourceAnalysisSucceeded {
		t.Fatalf("hash failure blocked a sibling: %#v", result)
	}
	if probeCalls.Load() != 1 || fingerprintCalls.Load() != 1 || cache.probeCalls.Load() != 0 || cache.fingerprintCalls.Load() != 0 {
		t.Fatalf("unexpected calls: probe=%d fingerprint=%d cache=%d/%d", probeCalls.Load(), fingerprintCalls.Load(), cache.probeCalls.Load(), cache.fingerprintCalls.Load())
	}
}

func TestSourceAnalysisPreparerIndependentCacheHitsPreserveProvenanceAndSuppressExecutors(t *testing.T) {
	file := preparerTestFile(t, "audio")
	digest := sha256.Sum256([]byte("audio"))
	streamCount := 2
	probeResult := &persistence.SourceMediaVariant{
		ID: uuid.New(), FFProbeVersion: ptr("ffprobe 8.0 verified banner"), AnalysisPolicyVersion: ptr(3),
		FFProbeJSON:  []byte(`{"streams":[{"codec_type":"audio"},{"codec_type":"audio"}]}`),
		ObservedTags: []byte(`{"TITLE":["preserved"]}`), AudioStreamCount: &streamCount,
		InspectedAt: ptr(time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)), AppliedOperationID: ptr(uuid.New()),
	}
	fingerprintResult := &persistence.SourceFingerprintResult{
		ID: uuid.New(), SourceSHA256: digest[:], FPCalcVersion: "1.2.3", VersionBanner: "original banner", AlgorithmNamespace: "chromaprint",
		AlgorithmID: 7, Fingerprint: "original fingerprint", ReportedDuration: 99,
		CalculatedAt: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC), AppliedOperationID: uuid.New(), ParserContractVersion: 9,
	}
	cache := &preparerCache{
		probeLookup: func(_ context.Context, got [sha256.Size]byte, version string, policy int) (*persistence.SourceMediaVariant, bool, error) {
			if got != digest || version != "ffprobe 8.0 verified banner" || policy != 3 {
				t.Errorf("wrong probe key: digest=%x version=%q policy=%d", got, version, policy)
			}
			return probeResult, true, nil
		},
		fingerprintLookup: func(_ context.Context, got [sha256.Size]byte) (*persistence.SourceFingerprintResult, bool, error) {
			if got != digest {
				t.Errorf("wrong fingerprint key: digest=%x", got)
			}
			return fingerprintResult, true, nil
		},
	}
	config := preparerConfig()
	config.FPCalcVersion.Version = "9.9.9" // Cached tool version is provenance, not identity.
	config.Cache = cache
	var factories, holds atomic.Int32
	config.ProbeFactory = func(string) (SourceAnalysisProbe, error) { factories.Add(1); return nil, errors.New("must not build") }
	config.FingerprinterFactory = func(string) (SourceAnalysisFingerprinter, error) {
		factories.Add(1)
		return nil, errors.New("must not build")
	}
	config.Hold = func(context.Context, SourceAnalysisStep) (func(), error) { holds.Add(1); return nil, nil }
	preparer := NewSourceAnalysisPreparer(config)
	result := preparer.Prepare(context.Background(), SourceAnalysisPrepareRequest{
		File: file, ExistingSHA256: &digest, Targets: SourceAnalysisTargetProbe | SourceAnalysisTargetFingerprint,
	})
	if result.Probe.State != SourceAnalysisCacheHit || result.Probe.Result != probeResult || result.Probe.MatchEligible || result.Fingerprint.State != SourceAnalysisCacheHit || result.Fingerprint.Result != fingerprintResult {
		t.Fatalf("cache did not preserve results: %#v", result)
	}
	if factories.Load() != 0 || holds.Load() != 0 || cache.probeCalls.Load() != 1 || cache.fingerprintCalls.Load() != 1 {
		t.Fatalf("cache hit executed tools/holds: factories=%d holds=%d", factories.Load(), holds.Load())
	}
}

func TestSourceAnalysisPreparerTargetMasksAndFingerprintCacheBypass(t *testing.T) {
	file := preparerTestFile(t, "audio")
	config := preparerConfig()
	cache := &preparerCache{}
	config.Cache = cache
	var probeCalls, fingerprintCalls atomic.Int32
	config.ProbeFactory = func(string) (SourceAnalysisProbe, error) {
		probeCalls.Add(1)
		return preparerProbe(func(context.Context, sourcefs.RegularFile) ([]byte, error) {
			return []byte(`{"format":{},"streams":[]}`), nil
		}), nil
	}
	fingerprinter := &preparerFingerprinter{version: config.FPCalcVersion, fingerprint: func(context.Context, string) (tools.FPCalcResult, error) {
		fingerprintCalls.Add(1)
		return tools.FPCalcResult{Fingerprint: "fresh"}, nil
	}}
	config.FingerprinterFactory = func(string) (SourceAnalysisFingerprinter, error) { return fingerprinter, nil }
	preparer := NewSourceAnalysisPreparer(config)
	shaOnly := preparer.Prepare(context.Background(), SourceAnalysisPrepareRequest{File: file, Targets: SourceAnalysisTargetSHA256})
	if shaOnly.SHA256.State != SourceAnalysisSucceeded || shaOnly.Probe.State != SourceAnalysisNotRequested || shaOnly.Fingerprint.State != SourceAnalysisNotRequested {
		t.Fatalf("SHA-only mask was not respected: %#v", shaOnly)
	}
	probeOnly := preparer.Prepare(context.Background(), SourceAnalysisPrepareRequest{File: file, Targets: SourceAnalysisTargetProbe})
	if probeOnly.Probe.State != SourceAnalysisSucceeded || probeOnly.SHA256.State != SourceAnalysisNotRequested || probeOnly.Fingerprint.State != SourceAnalysisNotRequested || probeCalls.Load() != 1 || fingerprintCalls.Load() != 0 {
		t.Fatalf("probe-only mask was not respected: %#v", probeOnly)
	}
	existing := sha256.Sum256([]byte("previous"))
	fingerprintOnly := preparer.Prepare(context.Background(), SourceAnalysisPrepareRequest{
		File: file, ServerPath: "/server/audio.flac", ExistingSHA256: &existing,
		Targets: SourceAnalysisTargetFingerprint, BypassFingerprintCache: true,
	})
	if fingerprintOnly.Fingerprint.State != SourceAnalysisSucceeded || fingerprintOnly.SHA256.State != SourceAnalysisNotRequested || fingerprintOnly.Probe.State != SourceAnalysisNotRequested || probeCalls.Load() != 1 || fingerprintCalls.Load() != 1 {
		t.Fatalf("fingerprint-only mask was not respected: %#v", fingerprintOnly)
	}
	if cache.probeCalls.Load() != 0 || cache.fingerprintCalls.Load() != 0 {
		t.Fatalf("unexpected cache calls: %d/%d", cache.probeCalls.Load(), cache.fingerprintCalls.Load())
	}
}

func TestSourceAnalysisPreparerCacheHitsAreIndependent(t *testing.T) {
	for _, test := range []struct {
		name           string
		probeHit       bool
		fingerprintHit bool
	}{
		{name: "probe hit leaves fingerprint missing", probeHit: true},
		{name: "fingerprint hit leaves probe missing", fingerprintHit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := preparerTestFile(t, "audio")
			digest := sha256.Sum256([]byte("existing"))
			streams := 1
			probeResult := &persistence.SourceMediaVariant{
				AudioStreamCount: &streams, FFProbeVersion: ptr("ffprobe 8.0 verified banner"), AnalysisPolicyVersion: ptr(3),
				FFProbeJSON: []byte(`{"streams":[{"codec_type":"audio"}]}`),
			}
			fingerprintResult := &persistence.SourceFingerprintResult{SourceSHA256: digest[:], FPCalcVersion: "1.2.3", VersionBanner: "fpcalc cached", AlgorithmNamespace: "chromaprint", Fingerprint: "cached"}
			cache := &preparerCache{
				probeLookup: func(context.Context, [sha256.Size]byte, string, int) (*persistence.SourceMediaVariant, bool, error) {
					return probeResult, test.probeHit, nil
				},
				fingerprintLookup: func(context.Context, [sha256.Size]byte) (*persistence.SourceFingerprintResult, bool, error) {
					return fingerprintResult, test.fingerprintHit, nil
				},
			}
			var probeRuns, fingerprintRuns atomic.Int32
			config := preparerConfig()
			config.Cache = cache
			config.ProbeFactory = func(string) (SourceAnalysisProbe, error) {
				return preparerProbe(func(context.Context, sourcefs.RegularFile) ([]byte, error) {
					probeRuns.Add(1)
					return []byte(`{"format":{},"streams":[]}`), nil
				}), nil
			}
			fp := &preparerFingerprinter{version: config.FPCalcVersion, fingerprint: func(context.Context, string) (tools.FPCalcResult, error) {
				fingerprintRuns.Add(1)
				return tools.FPCalcResult{Fingerprint: "fresh"}, nil
			}}
			config.FingerprinterFactory = func(string) (SourceAnalysisFingerprinter, error) { return fp, nil }
			result := NewSourceAnalysisPreparer(config).Prepare(context.Background(), SourceAnalysisPrepareRequest{
				File: file, ExistingSHA256: &digest, ServerPath: "/server/audio.flac",
				Targets: SourceAnalysisTargetProbe | SourceAnalysisTargetFingerprint,
			})
			if (result.Probe.State == SourceAnalysisCacheHit) != test.probeHit || (result.Fingerprint.State == SourceAnalysisCacheHit) != test.fingerprintHit {
				t.Fatalf("independent outcomes: %#v", result)
			}
			wantProbeRuns, wantFingerprintRuns := int32(1), int32(1)
			if test.probeHit {
				wantProbeRuns = 0
			}
			if test.fingerprintHit {
				wantFingerprintRuns = 0
			}
			if probeRuns.Load() != wantProbeRuns || fingerprintRuns.Load() != wantFingerprintRuns {
				t.Fatalf("independent runners: probe=%d fingerprint=%d", probeRuns.Load(), fingerprintRuns.Load())
			}
		})
	}
}

func TestSourceAnalysisPreparerCacheLookupFailureIsStepLocal(t *testing.T) {
	file := preparerTestFile(t, "audio")
	digest := sha256.Sum256([]byte("existing"))
	cache := &preparerCache{
		probeLookup: func(context.Context, [sha256.Size]byte, string, int) (*persistence.SourceMediaVariant, bool, error) {
			return nil, false, errors.New("private database detail")
		},
	}
	var probeRuns, fingerprintRuns atomic.Int32
	config := preparerConfig()
	config.Cache = cache
	config.ProbeFactory = func(string) (SourceAnalysisProbe, error) {
		probeRuns.Add(1)
		return preparerProbe(func(context.Context, sourcefs.RegularFile) ([]byte, error) {
			return []byte(`{"format":{},"streams":[]}`), nil
		}), nil
	}
	fingerprinter := &preparerFingerprinter{version: config.FPCalcVersion, fingerprint: func(context.Context, string) (tools.FPCalcResult, error) {
		fingerprintRuns.Add(1)
		return tools.FPCalcResult{Fingerprint: "fresh"}, nil
	}}
	config.FingerprinterFactory = func(string) (SourceAnalysisFingerprinter, error) { return fingerprinter, nil }
	result := NewSourceAnalysisPreparer(config).Prepare(context.Background(), SourceAnalysisPrepareRequest{
		File: file, ServerPath: "/server/audio.flac", ExistingSHA256: &digest,
		Targets: SourceAnalysisTargetProbe | SourceAnalysisTargetFingerprint,
	})
	if result.Probe.State != SourceAnalysisFailed || result.Probe.SafeError != "source technical-result cache could not be read" || result.Fingerprint.State != SourceAnalysisSucceeded {
		t.Fatalf("cache read failure escaped its step: %#v", result)
	}
	if probeRuns.Load() != 0 || fingerprintRuns.Load() != 1 {
		t.Fatalf("wrong tool execution after cache failure: probe=%d fingerprint=%d", probeRuns.Load(), fingerprintRuns.Load())
	}
}

func TestSourceAnalysisPreparerSiblingFailureIsIndependentAndVersionIsPinned(t *testing.T) {
	file := preparerTestFile(t, "audio")
	config := preparerConfig()
	config.ProbeFactory = func(string) (SourceAnalysisProbe, error) {
		return preparerProbe(func(context.Context, sourcefs.RegularFile) ([]byte, error) {
			return nil, errors.New("private ffprobe error")
		}), nil
	}
	var fingerprintCalls atomic.Int32
	fingerprinter := &preparerFingerprinter{version: tools.FPCalcVersion{Version: "9.9.9"}, fingerprint: func(context.Context, string) (tools.FPCalcResult, error) {
		fingerprintCalls.Add(1)
		return tools.FPCalcResult{}, errors.New("unexpected fingerprint run")
	}}
	config.FingerprinterFactory = func(string) (SourceAnalysisFingerprinter, error) { return fingerprinter, nil }
	result := NewSourceAnalysisPreparer(config).Prepare(context.Background(), SourceAnalysisPrepareRequest{
		File: file, ServerPath: "/server/file", Targets: SourceAnalysisTargetProbe | SourceAnalysisTargetFingerprint,
	})
	if result.Probe.State != SourceAnalysisFailed || result.Probe.SafeError != "managed ffprobe could not analyze the source file" || result.Fingerprint.State != SourceAnalysisFailed || result.Fingerprint.SafeError != "managed fpcalc version no longer matches the selected installation" {
		t.Fatalf("unexpected failure outcomes: %#v", result)
	}
	if fingerprintCalls.Load() != 0 {
		t.Fatal("fingerprint ran after selected-version mismatch")
	}
}

func TestSourceAnalysisPreparerCancellationJoinsRunnersAndReleasesHolds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probeStarted := make(chan struct{})
	fingerprintStarted := make(chan struct{})
	probeRelease := make(chan struct{})
	fingerprintRelease := make(chan struct{})
	probeFinished := make(chan struct{})
	fingerprintFinished := make(chan struct{})
	var holdReleases atomic.Int32
	config := preparerConfig()
	config.ProbeFactory = func(string) (SourceAnalysisProbe, error) {
		return preparerProbe(func(ctx context.Context, _ sourcefs.RegularFile) ([]byte, error) {
			close(probeStarted)
			<-ctx.Done()
			<-probeRelease
			close(probeFinished)
			return nil, ctx.Err()
		}), nil
	}
	fingerprinter := &preparerFingerprinter{version: config.FPCalcVersion, fingerprint: func(ctx context.Context, _ string) (tools.FPCalcResult, error) {
		close(fingerprintStarted)
		<-ctx.Done()
		<-fingerprintRelease
		close(fingerprintFinished)
		return tools.FPCalcResult{}, ctx.Err()
	}}
	config.FingerprinterFactory = func(string) (SourceAnalysisFingerprinter, error) { return fingerprinter, nil }
	config.Hold = func(_ context.Context, _ SourceAnalysisStep) (func(), error) {
		return func() { holdReleases.Add(1) }, nil
	}
	preparer := NewSourceAnalysisPreparer(config)
	done := make(chan struct{})
	go func() {
		preparer.Prepare(ctx, SourceAnalysisPrepareRequest{ServerPath: "/server/audio.flac", Targets: SourceAnalysisTargetProbe | SourceAnalysisTargetFingerprint})
		close(done)
	}()
	select {
	case <-probeStarted:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	select {
	case <-fingerprintStarted:
	case <-time.After(time.Second):
		t.Fatal("fingerprint did not start")
	}
	cancel()
	assertNotClosed := func(ch <-chan struct{}, message string) {
		t.Helper()
		select {
		case <-ch:
			t.Fatal(message)
		case <-time.After(20 * time.Millisecond):
		}
	}
	assertNotClosed(done, "Prepare returned before either runner was released")
	close(probeRelease)
	select {
	case <-probeFinished:
	case <-time.After(time.Second):
		t.Fatal("probe did not finish after release")
	}
	assertNotClosed(done, "Prepare returned while fingerprint runner was still blocked")
	close(fingerprintRelease)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Prepare did not join both runners")
	}
	select {
	case <-fingerprintFinished:
	default:
		t.Fatal("fingerprint runner did not finish")
	}
	if holdReleases.Load() != 2 {
		t.Fatalf("released %d holds; want both", holdReleases.Load())
	}
}

func ptr[T any](value T) *T { return &value }
