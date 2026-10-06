package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type SourceAnalysisStep string

const (
	SourceAnalysisProbeStep       SourceAnalysisStep = "probe"
	SourceAnalysisFingerprintStep SourceAnalysisStep = "fingerprint"
)

type SourceAnalysisTarget uint8

const (
	SourceAnalysisTargetSHA256 SourceAnalysisTarget = 1 << iota
	SourceAnalysisTargetProbe
	SourceAnalysisTargetFingerprint
)

// SourceAnalysisPreparerHold acquires a persistence hold immediately before a
// pinned tool is used. The returned function releases it after execution.
type SourceAnalysisPreparerHold func(context.Context, SourceAnalysisStep) (func(), error)

type SourceAnalysisProbe interface {
	ProbeMediaFile(context.Context, sourcefs.RegularFile) ([]byte, error)
}

type SourceAnalysisFingerprinter interface {
	Fingerprint(context.Context, string) (tools.FPCalcResult, error)
	Version(context.Context) (tools.FPCalcVersion, error)
}

// SourceAnalysisCacheLookup resolves the independently reusable results for one
// content digest. Returned persistence models are preserved as provenance.
type SourceAnalysisCacheLookup interface {
	LookupSourceProbe(context.Context, [sha256.Size]byte, string, int) (*persistence.SourceMediaVariant, bool, error)
	LookupSourceFingerprint(context.Context, [sha256.Size]byte, string) (*persistence.SourceFingerprintResult, bool, error)
}

type SourceAnalysisOutcomeState string

const (
	SourceAnalysisNotRequested SourceAnalysisOutcomeState = "not_requested"
	SourceAnalysisSucceeded    SourceAnalysisOutcomeState = "succeeded"
	SourceAnalysisCacheHit     SourceAnalysisOutcomeState = "cache_hit"
	SourceAnalysisFailed       SourceAnalysisOutcomeState = "failed"
	SourceAnalysisDeferred     SourceAnalysisOutcomeState = "deferred"
)

type SourceAnalysisStepOutcome struct {
	State     SourceAnalysisOutcomeState
	SafeError string
}

type SourceAnalysisSHA256Outcome struct {
	SourceAnalysisStepOutcome
	Digest [sha256.Size]byte
}

type SourceAnalysisProbeOutcome struct {
	SourceAnalysisStepOutcome
	RawJSON          json.RawMessage
	AudioStreamCount int
	MatchEligible    bool
	Result           *persistence.SourceMediaVariant
}

type SourceAnalysisFingerprintOutcome struct {
	SourceAnalysisStepOutcome
	Result *persistence.SourceFingerprintResult
}

type SourceAnalysisPreparation struct {
	SHA256      SourceAnalysisSHA256Outcome
	Probe       SourceAnalysisProbeOutcome
	Fingerprint SourceAnalysisFingerprintOutcome
}

type SourceAnalysisPrepareRequest struct {
	File       sourcefs.RegularFile
	ServerPath string
	Targets    SourceAnalysisTarget
	// ExistingSHA256 is used for lookups only when SHA256 itself is not targeted.
	ExistingSHA256 *[sha256.Size]byte
	// BypassFingerprintCache is the explicit rerun override for a fingerprint target.
	BypassFingerprintCache bool
}

type SourceAnalysisPreparing interface {
	Prepare(context.Context, SourceAnalysisPrepareRequest) SourceAnalysisPreparation
}

type SourceAnalysisPreparerConfig struct {
	ProbeExecutable string
	// FFProbeVersion is the verified banner for the pinned ffprobe selection.
	FFProbeVersion       string
	AnalysisPolicy       int
	ProbeResult          persistence.SourceMediaVariant
	FPCalcExecutable     string
	FPCalcVersion        tools.FPCalcVersion
	FingerprintResult    persistence.SourceFingerprintResult
	ProbeFactory         func(string) (SourceAnalysisProbe, error)
	FingerprinterFactory func(string) (SourceAnalysisFingerprinter, error)
	Cache                SourceAnalysisCacheLookup
	Hold                 SourceAnalysisPreparerHold
}

// SourceAnalysisPreparer prepares only explicitly requested steps. Cache reads
// happen before any runner starts so a hit suppresses the corresponding tool.
type SourceAnalysisPreparer struct {
	config SourceAnalysisPreparerConfig
}

func NewSourceAnalysisPreparer(config SourceAnalysisPreparerConfig) *SourceAnalysisPreparer {
	if config.ProbeFactory == nil {
		config.ProbeFactory = func(path string) (SourceAnalysisProbe, error) { return tools.NewFFProbe(path) }
	}
	if config.FingerprinterFactory == nil {
		config.FingerprinterFactory = func(path string) (SourceAnalysisFingerprinter, error) { return tools.NewFPCalc(path) }
	}
	return &SourceAnalysisPreparer{config: config}
}

func (preparer *SourceAnalysisPreparer) Prepare(ctx context.Context, request SourceAnalysisPrepareRequest) SourceAnalysisPreparation {
	result := SourceAnalysisPreparation{
		SHA256:      SourceAnalysisSHA256Outcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisNotRequested}},
		Probe:       SourceAnalysisProbeOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisNotRequested}},
		Fingerprint: SourceAnalysisFingerprintOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisNotRequested}},
	}
	if request.Targets&SourceAnalysisTargetSHA256 != 0 {
		digest, err := sourcefs.SHA256(ctx, request.File)
		if err != nil {
			result.SHA256 = failedSHA256(err)
		} else {
			result.SHA256 = SourceAnalysisSHA256Outcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisSucceeded}, Digest: digest}
		}
	}
	digest, hasDigest := preparationDigest(request, result.SHA256)

	probeCache := cacheProbeResult{}
	if request.Targets&SourceAnalysisTargetProbe != 0 && hasDigest && preparer.config.Cache != nil && preparer.config.FFProbeVersion != "" && preparer.config.AnalysisPolicy > 0 {
		cached, hit, err := preparer.config.Cache.LookupSourceProbe(ctx, digest, preparer.config.FFProbeVersion, preparer.config.AnalysisPolicy)
		if err != nil {
			probeCache.failed = &cacheFailure{message: safeProbeCacheError(err)}
		} else if hit {
			if cached == nil || cached.FFProbeVersion == nil || *cached.FFProbeVersion != preparer.config.FFProbeVersion || cached.AnalysisPolicyVersion == nil || *cached.AnalysisPolicyVersion != preparer.config.AnalysisPolicy {
				probeCache.failed = &cacheFailure{message: "source technical-result cache returned invalid provenance"}
			} else {
				probeCache.result = cached
			}
		}
	}
	fingerprintCache := cacheFingerprintResult{}
	if request.Targets&SourceAnalysisTargetFingerprint != 0 && hasDigest && preparer.config.Cache != nil && preparer.config.FPCalcVersion.Version != "" && !request.BypassFingerprintCache {
		cached, hit, err := preparer.config.Cache.LookupSourceFingerprint(ctx, digest, preparer.config.FPCalcVersion.Version)
		if err != nil {
			fingerprintCache.failed = &cacheFailure{message: safeFingerprintCacheError(err)}
		} else if hit {
			if cached == nil || cached.FPCalcVersion != preparer.config.FPCalcVersion.Version {
				fingerprintCache.failed = &cacheFailure{message: "source fingerprint cache returned invalid provenance"}
			} else {
				fingerprintCache.result = cached
			}
		}
	}

	type probeResult struct{ outcome SourceAnalysisProbeOutcome }
	type fingerprintResult struct {
		outcome SourceAnalysisFingerprintOutcome
	}
	var probeChannel chan probeResult
	var fingerprintChannel chan fingerprintResult
	if request.Targets&SourceAnalysisTargetProbe != 0 {
		probeChannel = make(chan probeResult, 1)
		go func() { probeChannel <- probeResult{preparer.runProbe(ctx, request.File, probeCache)} }()
	}
	if request.Targets&SourceAnalysisTargetFingerprint != 0 {
		fingerprintChannel = make(chan fingerprintResult, 1)
		go func() {
			fingerprintChannel <- fingerprintResult{preparer.runFingerprint(ctx, request.ServerPath, request.BypassFingerprintCache, fingerprintCache)}
		}()
	}
	if probeChannel != nil {
		result.Probe = (<-probeChannel).outcome
	}
	if fingerprintChannel != nil {
		result.Fingerprint = (<-fingerprintChannel).outcome
	}
	return result
}

type cacheFailure struct{ message string }
type cacheProbeResult struct {
	result *persistence.SourceMediaVariant
	failed *cacheFailure
}
type cacheFingerprintResult struct {
	result *persistence.SourceFingerprintResult
	failed *cacheFailure
}

func (preparer *SourceAnalysisPreparer) runProbe(ctx context.Context, file sourcefs.RegularFile, cached cacheProbeResult) SourceAnalysisProbeOutcome {
	if cached.failed != nil {
		return SourceAnalysisProbeOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: cached.failed.message}}
	}
	if cached.result != nil {
		count := 0
		if cached.result.AudioStreamCount != nil {
			count = *cached.result.AudioStreamCount
		} else {
			var err error
			count, err = countAudioStreams(cached.result.FFProbeJSON)
			if err != nil {
				return SourceAnalysisProbeOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: "cached technical result has invalid audio-stream metadata"}}
			}
		}
		return SourceAnalysisProbeOutcome{
			SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisCacheHit},
			RawJSON:                   append(json.RawMessage(nil), cached.result.FFProbeJSON...), AudioStreamCount: count,
			MatchEligible: count == 1, Result: cached.result,
		}
	}
	if preparer.config.FFProbeVersion == "" || preparer.config.AnalysisPolicy < 1 {
		return SourceAnalysisProbeOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: "verified ffprobe provenance is unavailable"}}
	}
	release, err := preparer.acquireHold(ctx, SourceAnalysisProbeStep)
	if err != nil {
		if errors.Is(err, persistence.ErrToolsRootMoveActive) {
			return SourceAnalysisProbeOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisDeferred}}
		}
		return SourceAnalysisProbeOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: safeHoldError(err)}}
	}
	if release != nil {
		defer release()
	}
	probe, err := preparer.config.ProbeFactory(preparer.config.ProbeExecutable)
	if err != nil {
		return SourceAnalysisProbeOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: "managed ffprobe is unavailable"}}
	}
	raw, err := probe.ProbeMediaFile(ctx, file)
	if err != nil {
		return SourceAnalysisProbeOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: safeProbeError(err)}}
	}
	analysis, err := ParseSourceTechnicalAnalysis(raw)
	if err != nil {
		return SourceAnalysisProbeOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: "managed ffprobe returned invalid technical metadata"}}
	}
	count := len(analysis.Streams)
	observedTags, err := json.Marshal(analysis.Tags)
	if err != nil {
		return SourceAnalysisProbeOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: "managed ffprobe returned invalid technical metadata"}}
	}
	completed := preparer.config.ProbeResult
	version := preparer.config.FFProbeVersion
	completed.FFProbeVersion = &version
	policy := preparer.config.AnalysisPolicy
	completed.AnalysisPolicyVersion = &policy
	completed.FFProbeJSON = append(json.RawMessage(nil), raw...)
	completed.AudioStreamCount = &count
	completed.ObservedTags = observedTags
	inspectedAt := time.Now().UTC()
	completed.InspectedAt = &inspectedAt
	return SourceAnalysisProbeOutcome{
		SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisSucceeded},
		RawJSON:                   append(json.RawMessage(nil), raw...), AudioStreamCount: count, MatchEligible: count == 1,
		Result: &completed,
	}
}

func (preparer *SourceAnalysisPreparer) runFingerprint(ctx context.Context, serverPath string, bypassCache bool, cached cacheFingerprintResult) SourceAnalysisFingerprintOutcome {
	if cached.failed != nil {
		return SourceAnalysisFingerprintOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: cached.failed.message}}
	}
	if !bypassCache && cached.result != nil {
		return SourceAnalysisFingerprintOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisCacheHit}, Result: cached.result}
	}
	if serverPath == "" {
		return SourceAnalysisFingerprintOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: "server source path is unavailable for fingerprint analysis"}}
	}
	release, err := preparer.acquireHold(ctx, SourceAnalysisFingerprintStep)
	if err != nil {
		if errors.Is(err, persistence.ErrToolsRootMoveActive) {
			return SourceAnalysisFingerprintOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisDeferred}}
		}
		return SourceAnalysisFingerprintOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: safeHoldError(err)}}
	}
	if release != nil {
		defer release()
	}
	fingerprinter, err := preparer.config.FingerprinterFactory(preparer.config.FPCalcExecutable)
	if err != nil {
		return SourceAnalysisFingerprintOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: "managed fpcalc is unavailable"}}
	}
	actualVersion, err := fingerprinter.Version(ctx)
	if err != nil {
		return SourceAnalysisFingerprintOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: "managed fpcalc version could not be verified"}}
	}
	if preparer.config.FPCalcVersion.Version == "" || actualVersion.Version != preparer.config.FPCalcVersion.Version {
		return SourceAnalysisFingerprintOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: "managed fpcalc version no longer matches the selected installation"}}
	}
	calculated, err := fingerprinter.Fingerprint(ctx, serverPath)
	if err != nil {
		return SourceAnalysisFingerprintOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: safeFingerprintError(err)}}
	}
	completed := preparer.config.FingerprintResult
	completed.FPCalcVersion = actualVersion.Version
	completed.VersionBanner = actualVersion.Banner
	completed.AlgorithmID = int16(calculated.Algorithm)
	completed.Fingerprint = calculated.Fingerprint
	completed.ReportedDuration = calculated.Duration
	completed.CalculatedAt = time.Now().UTC()
	return SourceAnalysisFingerprintOutcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisSucceeded}, Result: &completed}
}

func (preparer *SourceAnalysisPreparer) acquireHold(ctx context.Context, step SourceAnalysisStep) (func(), error) {
	if preparer.config.Hold == nil {
		return nil, nil
	}
	return preparer.config.Hold(ctx, step)
}

func preparationDigest(request SourceAnalysisPrepareRequest, hashed SourceAnalysisSHA256Outcome) ([sha256.Size]byte, bool) {
	if hashed.State == SourceAnalysisSucceeded {
		return hashed.Digest, true
	}
	if request.Targets&SourceAnalysisTargetSHA256 != 0 {
		return [sha256.Size]byte{}, false
	}
	if request.ExistingSHA256 != nil {
		return *request.ExistingSHA256, true
	}
	return [sha256.Size]byte{}, false
}

func failedSHA256(err error) SourceAnalysisSHA256Outcome {
	message := "source content digest could not be read"
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		message = "source content digest calculation was canceled"
	}
	return SourceAnalysisSHA256Outcome{SourceAnalysisStepOutcome: SourceAnalysisStepOutcome{State: SourceAnalysisFailed, SafeError: message}}
}

func safeProbeCacheError(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "source technical-result lookup was canceled"
	}
	return "source technical-result cache could not be read"
}

func safeFingerprintCacheError(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "source fingerprint lookup was canceled"
	}
	return "source fingerprint cache could not be read"
}

func safeHoldError(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "source analysis was canceled before tool execution"
	}
	return "source analysis could not acquire its execution hold"
}

func safeProbeError(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "source technical analysis was canceled"
	}
	return "managed ffprobe could not analyze the source file"
}

func safeFingerprintError(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "source fingerprint analysis was canceled"
	}
	return "managed fpcalc could not analyze the source file"
}

func countAudioStreams(raw []byte) (int, error) {
	var response struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
	}
	if len(strings.TrimSpace(string(raw))) == 0 || json.Unmarshal(raw, &response) != nil || response.Streams == nil {
		return 0, errors.New("invalid technical probe output")
	}
	count := 0
	for _, stream := range response.Streams {
		if stream.CodecType == "audio" {
			count++
		}
	}
	return count, nil
}

var _ SourceAnalysisPreparing = (*SourceAnalysisPreparer)(nil)
