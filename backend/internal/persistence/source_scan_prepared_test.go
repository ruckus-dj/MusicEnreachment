package persistence

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSourceScanPreparedAnalysisJSONRoundTrip(t *testing.T) {
	audioCount := 0 // A confirmed zero-audio probe is still a real probe result.
	probeVersion := "ffprobe-7.1"
	policy := 3
	algorithm := "sha256"
	operationID := uuid.New()
	prepared := SourceScanPreparedAnalysis{
		Version:                 1,
		HashRequested:           true,
		SHA256State:             SourcePreparedSucceeded,
		SHA256Variant:           &SourceMediaVariant{ID: uuid.New(), SourceSHA256: make([]byte, 32), SHA256CalculatedAt: timePointer(time.Unix(1, 0)), SHA256Algorithm: &algorithm, SHA256AppliedOperationID: &operationID},
		ProbeState:              SourcePreparedSucceeded,
		ProbeVariant:            &SourceMediaVariant{ID: uuid.New(), AnalysisPolicyVersion: &policy, FFProbeVersion: &probeVersion, FFProbeJSON: json.RawMessage(`{"format":{},"streams":[]}`), ObservedTags: json.RawMessage(`{}`), InspectedAt: timePointer(time.Unix(2, 0)), AppliedOperationID: &operationID, AudioStreamCount: &audioCount},
		FingerprintState:        SourcePreparedSucceeded,
		FingerprintResult:       &SourceFingerprintResult{ID: uuid.New(), FPCalcVersion: "1.5.1", VersionBanner: "fpcalc 1.5.1", AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "immutable-fingerprint", ReportedDuration: 0, CalculatedAt: time.Unix(3, 0), AppliedOperationID: operationID, ParserContractVersion: 1},
		FingerprintReused:       true,
		FingerprintCacheSHA256:  strings.Repeat("00", 32),
		FingerprintCacheVersion: "1.5.1",
		ReusedImmutableIDs:      []uuid.UUID{uuid.New()},
		OriginalLocationID:      uuidPointer(uuid.New()),
		OriginalMediaVariantID:  uuidPointer(uuid.New()),
		OriginalScanOperationID: uuidPointer(uuid.New()),
		SuccessorLocationID:     uuidPointer(uuid.New()),
	}

	encoded, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SourceScanPreparedAnalysis
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ProbeVariant.AudioStreamCount == nil || *decoded.ProbeVariant.AudioStreamCount != 0 {
		t.Fatalf("zero-audio probe result was not preserved: %#v", decoded.ProbeVariant)
	}
	if decoded.FingerprintResult == nil || decoded.FingerprintResult.ID != prepared.FingerprintResult.ID || decoded.FingerprintResult.AppliedOperationID != operationID {
		t.Fatalf("fingerprint result/provenance was not preserved: %#v", decoded.FingerprintResult)
	}
	if decoded.SHA256Variant == nil || decoded.SHA256Variant.ID != prepared.SHA256Variant.ID || decoded.OriginalScanOperationID == nil || *decoded.OriginalScanOperationID != *prepared.OriginalScanOperationID {
		t.Fatalf("SHA or original provenance was not preserved: %#v", decoded)
	}
}

func TestSourceScanPreparedAnalysisRejectsFabricatedOrPartialResults(t *testing.T) {
	cases := map[string]SourceScanPreparedAnalysis{
		"failed probe with fabricated zero audio": {Version: 1, SHA256State: SourcePreparedNotRequested, ProbeState: SourcePreparedFailed, ProbeVariant: &SourceMediaVariant{AudioStreamCount: intPointer(0)}, ProbeSafeError: "probe failed", FingerprintState: SourcePreparedNotRequested},
		"successful probe missing payload":        {Version: 1, SHA256State: SourcePreparedNotRequested, ProbeState: SourcePreparedSucceeded, ProbeVariant: &SourceMediaVariant{AudioStreamCount: intPointer(0)}, FingerprintState: SourcePreparedNotRequested},
		"failed fingerprint with result":          {Version: 1, SHA256State: SourcePreparedNotRequested, ProbeState: SourcePreparedNotRequested, FingerprintState: SourcePreparedFailed, FingerprintResult: &SourceFingerprintResult{ID: uuid.New()}, FingerprintSafeError: "failed"},
	}
	for name, prepared := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := json.Marshal(prepared); err == nil {
				t.Fatal("expected invalid prepared analysis to be rejected")
			}
		})
	}
}

func TestSourceScanPreparedAnalysisAcceptsAllNotRequestedSteps(t *testing.T) {
	prepared := SourceScanPreparedAnalysis{Version: 1, SHA256State: SourcePreparedNotRequested, ProbeState: SourcePreparedNotRequested, FingerprintState: SourcePreparedNotRequested}
	if err := prepared.Validate(); err != nil {
		t.Fatalf("all-not-requested prepared analysis should be valid: %v", err)
	}
	prepared.HashRequested = true
	if err := prepared.Validate(); err != nil {
		t.Fatalf("an enabled policy may leave unchanged-file SHA not requested: %v", err)
	}
}

func TestSourceScanPreparedAnalysisAcceptsDeferredProbeAndFingerprintOnly(t *testing.T) {
	prepared := SourceScanPreparedAnalysis{
		Version: 1, HashRequested: true, SHA256State: SourcePreparedSucceeded,
		SHA256Variant: &SourceMediaVariant{ID: uuid.New(), SourceSHA256: make([]byte, 32), SHA256CalculatedAt: timePointer(time.Unix(1, 0)), SHA256Algorithm: stringPointer("sha256"), SHA256AppliedOperationID: uuidPointer(uuid.New())},
		ProbeState:    SourcePreparedDeferred, FingerprintState: SourcePreparedDeferred,
	}
	if err := prepared.Validate(); err != nil {
		t.Fatalf("deferred tool steps with successful SHA should be valid: %v", err)
	}
	prepared.SHA256State = SourcePreparedDeferred
	prepared.SHA256Variant = nil
	prepared.HashRequested = true
	if err := prepared.Validate(); err == nil {
		t.Fatal("SHA-256 must not be deferred")
	}
	prepared.SHA256State = SourcePreparedNotRequested
	prepared.ProbeSafeError = "fabricated failure"
	if err := prepared.Validate(); err == nil {
		t.Fatal("deferred probe must not carry a failure error")
	}
}

func TestSourceScanPreparedAnalysisRequiresExplicitHashPolicyInJSON(t *testing.T) {
	for name, input := range map[string]string{
		"missing": `{"version":1,"sha256_state":"not_requested","probe_state":"not_requested","fingerprint_state":"not_requested"}`,
		"null":    `{"version":1,"hash_requested":null,"sha256_state":"not_requested","probe_state":"not_requested","fingerprint_state":"not_requested"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var prepared SourceScanPreparedAnalysis
			if err := json.Unmarshal([]byte(input), &prepared); err == nil {
				t.Fatal("missing or null per-file hash policy must be rejected")
			}
		})
	}
}

func TestSourceScanPreparedAnalysisRejectsInvalidProbeJSONShapes(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"null":           json.RawMessage(`null`),
		"string":         json.RawMessage(`"probe"`),
		"array":          json.RawMessage(`[]`),
		"missing stream": json.RawMessage(`{}`),
		"null streams":   json.RawMessage(`{"streams":null}`),
		"object streams": json.RawMessage(`{"streams":{}}`),
		"missing format": json.RawMessage(`{"streams":[]}`),
		"null format":    json.RawMessage(`{"format":null,"streams":[]}`),
		"array format":   json.RawMessage(`{"format":[],"streams":[]}`),
		"string format":  json.RawMessage(`{"format":"container","streams":[]}`),
	} {
		t.Run(name, func(t *testing.T) {
			prepared := validPreparedProbe(raw, json.RawMessage(`{}`))
			if err := prepared.Validate(); err == nil {
				t.Fatal("invalid probe payload shape must be rejected")
			}
		})
	}
	for name, tags := range map[string]json.RawMessage{"null": json.RawMessage(`null`), "array": json.RawMessage(`[]`), "string": json.RawMessage(`"tags"`)} {
		t.Run("tags "+name, func(t *testing.T) {
			prepared := validPreparedProbe(json.RawMessage(`{"format":{},"streams":[]}`), tags)
			if err := prepared.Validate(); err == nil {
				t.Fatal("observed tags must be a JSON object")
			}
		})
	}
}

func TestSourceScanPreparedAnalysisAllowsFingerprintOnProbeError(t *testing.T) {
	prepared := SourceScanPreparedAnalysis{
		Version: 1, HashRequested: true, SHA256State: SourcePreparedFailed, SHA256SafeError: "safe hash failure",
		ProbeState: SourcePreparedFailed, ProbeSafeError: "safe probe failure",
		FingerprintState:  SourcePreparedSucceeded,
		FingerprintResult: &SourceFingerprintResult{ID: uuid.New(), FPCalcVersion: "1", VersionBanner: "fpcalc 1", AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "result", CalculatedAt: time.Unix(1, 0), AppliedOperationID: uuid.New(), ParserContractVersion: 1},
	}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		t.Fatalf("fingerprint should be retained independently of probe failure: %v", err)
	}
	var decoded SourceScanPreparedAnalysis
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.FingerprintResult == nil || decoded.SHA256Variant != nil {
		t.Fatalf("successful fingerprint without a SHA result should round-trip: %#v, %v", decoded, err)
	}
}

func TestSourceScanPreparedAnalysisAllowsFingerprintWhenHashDisabledWithoutDigest(t *testing.T) {
	prepared := SourceScanPreparedAnalysis{
		Version: 1, SHA256State: SourcePreparedNotRequested, ProbeState: SourcePreparedNotRequested,
		FingerprintState:  SourcePreparedSucceeded,
		FingerprintResult: &SourceFingerprintResult{ID: uuid.New(), FPCalcVersion: "1", VersionBanner: "fpcalc 1", AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "result", CalculatedAt: time.Unix(1, 0), AppliedOperationID: uuid.New(), ParserContractVersion: 1},
	}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		t.Fatalf("disabled hashing must not block successful fingerprinting: %v", err)
	}
	var decoded SourceScanPreparedAnalysis
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.FingerprintResult == nil {
		t.Fatalf("fingerprint without SHA should round-trip when hashing is disabled: %#v, %v", decoded, err)
	}
}

func TestSourceScanPreparedAnalysisRequiresMatchingCurrentSHAForFingerprintCache(t *testing.T) {
	for name, prepared := range map[string]SourceScanPreparedAnalysis{
		"no current digest":        cachePrepared(nil, strings.Repeat("00", 32), "1"),
		"different digest":         cachePrepared(retainedSHA(make([]byte, 32)), strings.Repeat("11", 32), "1"),
		"different fpcalc version": cachePrepared(retainedSHA(make([]byte, 32)), strings.Repeat("00", 32), "2"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := prepared.Validate(); err == nil {
				t.Fatal("cache reuse without matching current digest and version must be rejected")
			}
		})
	}
	if err := cachePrepared(retainedSHA(make([]byte, 32)), strings.Repeat("00", 32), "1").Validate(); err != nil {
		t.Fatalf("unchanged retained SHA should support cache reuse without hash backfill: %v", err)
	}
}

func validPreparedProbe(probe, tags json.RawMessage) SourceScanPreparedAnalysis {
	version, operationID, policy, count := "ffprobe", uuid.New(), 1, 0
	return SourceScanPreparedAnalysis{Version: 1, SHA256State: SourcePreparedNotRequested, ProbeState: SourcePreparedSucceeded, ProbeVariant: &SourceMediaVariant{ID: uuid.New(), FFProbeVersion: &version, FFProbeJSON: probe, ObservedTags: tags, AnalysisPolicyVersion: &policy, InspectedAt: timePointer(time.Unix(1, 0)), AppliedOperationID: &operationID, AudioStreamCount: &count}, FingerprintState: SourcePreparedNotRequested}
}

func cachePrepared(current *SourceMediaVariant, digest, version string) SourceScanPreparedAnalysis {
	return SourceScanPreparedAnalysis{
		Version: 1, SHA256State: SourcePreparedNotRequested, ProbeState: SourcePreparedNotRequested,
		RetainedSHA256Variant: current, FingerprintState: SourcePreparedSucceeded, FingerprintReused: true,
		FingerprintCacheSHA256: digest, FingerprintCacheVersion: version,
		FingerprintResult: &SourceFingerprintResult{ID: uuid.New(), FPCalcVersion: "1", VersionBanner: "fpcalc 1", AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "result", CalculatedAt: time.Unix(1, 0), AppliedOperationID: uuid.New(), ParserContractVersion: 1},
	}
}

func retainedSHA(digest []byte) *SourceMediaVariant {
	return &SourceMediaVariant{ID: uuid.New(), SourceSHA256: digest, SHA256CalculatedAt: timePointer(time.Unix(1, 0)), SHA256Algorithm: stringPointer("sha256"), SHA256AppliedOperationID: uuidPointer(uuid.New())}
}

func timePointer(value time.Time) *time.Time { return &value }
func uuidPointer(value uuid.UUID) *uuid.UUID { return &value }
func intPointer(value int) *int              { return &value }
func stringPointer(value string) *string     { return &value }
