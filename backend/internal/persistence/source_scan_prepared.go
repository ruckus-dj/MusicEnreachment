package persistence

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"

	"github.com/google/uuid"
)

// SourceScanPreparedAnalysis is private, per-candidate preparation data. It is
// published only after the complete traversal has confirmed the candidate's
// stat. Result pointers carry their original immutable identity and provenance.
type SourceScanPreparedAnalysis struct {
	Version       int                     `json:"version"`
	HashRequested bool                    `json:"hash_requested"` // Whether hashing is requested for this file, not global policy.
	SHA256State   SourcePreparedStepState `json:"sha256_state"`
	SHA256Variant *SourceMediaVariant     `json:"sha256_variant,omitempty"`
	// RetainedSHA256Variant is an existing selected digest only when candidate
	// identity was confirmed unchanged; it is never a hash backfill or new result.
	RetainedSHA256Variant   *SourceMediaVariant      `json:"retained_sha256_variant,omitempty"`
	ProbeState              SourcePreparedStepState  `json:"probe_state"`
	ProbeVariant            *SourceMediaVariant      `json:"probe_variant,omitempty"`
	FingerprintState        SourcePreparedStepState  `json:"fingerprint_state"`
	FingerprintResult       *SourceFingerprintResult `json:"fingerprint_result,omitempty"`
	FingerprintReused       bool                     `json:"fingerprint_reused"`
	FingerprintCacheSHA256  string                   `json:"fingerprint_cache_sha256,omitempty"`
	FingerprintCacheVersion string                   `json:"fingerprint_cache_version,omitempty"`
	SHA256SafeError         string                   `json:"sha256_safe_error,omitempty"`
	SHA256SkipReason        string                   `json:"sha256_skip_reason,omitempty"`
	ProbeSafeError          string                   `json:"probe_safe_error,omitempty"`
	ProbeSkipReason         string                   `json:"probe_skip_reason,omitempty"`
	FingerprintSafeError    string                   `json:"fingerprint_safe_error,omitempty"`
	FingerprintSkipReason   string                   `json:"fingerprint_skip_reason,omitempty"`
	ReusedImmutableIDs      []uuid.UUID              `json:"reused_immutable_ids,omitempty"`
	OriginalLocationID      *uuid.UUID               `json:"original_location_id,omitempty"`
	OriginalMediaVariantID  *uuid.UUID               `json:"original_media_variant_id,omitempty"`
	OriginalScanOperationID *uuid.UUID               `json:"original_scan_operation_id,omitempty"`
	SuccessorLocationID     *uuid.UUID               `json:"successor_location_id,omitempty"`
}

type SourcePreparedStepState string

const (
	SourcePreparedNotRequested SourcePreparedStepState = "not_requested"
	SourcePreparedSucceeded    SourcePreparedStepState = "succeeded"
	SourcePreparedFailed       SourcePreparedStepState = "failed"
	SourcePreparedSkipped      SourcePreparedStepState = "skipped"
	SourcePreparedDeferred     SourcePreparedStepState = "deferred"
)

// Validate enforces that stored prepared results are truthful: failed steps
// cannot carry fabricated values, and each successful step has its full result.
func (prepared SourceScanPreparedAnalysis) Validate() error {
	if prepared.Version != 1 {
		return fmt.Errorf("unsupported source scan prepared analysis version %d", prepared.Version)
	}
	if prepared.HashRequested {
		if err := validatePreparedSHA(prepared.SHA256State, prepared.SHA256Variant, prepared.SHA256SafeError, prepared.SHA256SkipReason); err != nil {
			return fmt.Errorf("invalid prepared SHA-256 state: %w", err)
		}
	} else if prepared.SHA256State != SourcePreparedNotRequested || prepared.SHA256Variant != nil || prepared.SHA256SafeError != "" || prepared.SHA256SkipReason != "" {
		return fmt.Errorf("SHA-256 must be not requested when hashing is disabled")
	}
	if prepared.RetainedSHA256Variant != nil {
		if err := validateCompleteSHA256Variant(prepared.RetainedSHA256Variant); err != nil {
			return fmt.Errorf("invalid retained SHA-256 identity: %w", err)
		}
		if prepared.SHA256State == SourcePreparedSucceeded {
			return fmt.Errorf("new and retained SHA-256 identities cannot both be selected")
		}
	}
	if err := validatePreparedProbe(prepared.ProbeState, prepared.ProbeVariant, prepared.ProbeSafeError, prepared.ProbeSkipReason); err != nil {
		return fmt.Errorf("invalid prepared probe state: %w", err)
	}
	if err := validatePreparedFingerprint(prepared); err != nil {
		return fmt.Errorf("invalid prepared fingerprint state: %w", err)
	}
	if prepared.FingerprintReused && prepared.FingerprintState != SourcePreparedSucceeded {
		return fmt.Errorf("reused fingerprint must be successful")
	}
	if prepared.FingerprintReused && (prepared.FingerprintCacheSHA256 == "" || prepared.FingerprintCacheVersion == "" || prepared.FingerprintResult.FPCalcVersion != prepared.FingerprintCacheVersion) {
		return fmt.Errorf("reused fingerprint requires matching digest and fpcalc version provenance")
	}
	if prepared.FingerprintReused {
		currentSHA := prepared.SHA256Variant
		if currentSHA == nil {
			currentSHA = prepared.RetainedSHA256Variant
		}
		if currentSHA == nil || prepared.FingerprintCacheSHA256 != hex.EncodeToString(currentSHA.SourceSHA256) {
			return fmt.Errorf("reused fingerprint cache key must match the current SHA-256 identity")
		}
	}
	if !prepared.FingerprintReused && (prepared.FingerprintCacheSHA256 != "" || prepared.FingerprintCacheVersion != "") {
		return fmt.Errorf("fingerprint cache key is only valid for a reused result")
	}
	if len(prepared.ReusedImmutableIDs) > 0 {
		seen := make(map[uuid.UUID]struct{}, len(prepared.ReusedImmutableIDs))
		for _, id := range prepared.ReusedImmutableIDs {
			if id == uuid.Nil {
				return fmt.Errorf("reused immutable IDs must be nonzero")
			}
			if _, exists := seen[id]; exists {
				return fmt.Errorf("reused immutable IDs must be unique")
			}
			seen[id] = struct{}{}
		}
	}
	return nil
}

func validatePreparedSHA(state SourcePreparedStepState, variant *SourceMediaVariant, safeError, skipReason string) error {
	switch state {
	case SourcePreparedSucceeded:
		if safeError != "" || skipReason != "" {
			return fmt.Errorf("successful SHA-256 cannot carry an error or skip reason")
		}
		if err := validateCompleteSHA256Variant(variant); err != nil {
			return fmt.Errorf("successful SHA-256 requires its complete immutable variant: %w", err)
		}
	case SourcePreparedFailed:
		if variant != nil || safeError == "" || skipReason != "" {
			return fmt.Errorf("failed SHA-256 requires only a safe error")
		}
	case SourcePreparedSkipped:
		if variant != nil || skipReason == "" || safeError != "" {
			return fmt.Errorf("skipped SHA-256 requires only a skip reason")
		}
	case SourcePreparedDeferred:
		return fmt.Errorf("SHA-256 cannot be deferred")
	case SourcePreparedNotRequested:
		if variant != nil || safeError != "" || skipReason != "" {
			return fmt.Errorf("not-requested SHA-256 cannot carry a result")
		}
	default:
		return fmt.Errorf("unknown step state %q", state)
	}
	return nil
}

func validateCompleteSHA256Variant(variant *SourceMediaVariant) error {
	if variant == nil || variant.ID == uuid.Nil || len(variant.SourceSHA256) != 32 || variant.SHA256CalculatedAt == nil || variant.SHA256CalculatedAt.IsZero() || variant.SHA256Algorithm == nil || *variant.SHA256Algorithm == "" || variant.SHA256AppliedOperationID == nil || *variant.SHA256AppliedOperationID == uuid.Nil {
		return fmt.Errorf("complete immutable SHA-256 provenance is required")
	}
	return nil
}

func validatePreparedProbe(state SourcePreparedStepState, variant *SourceMediaVariant, safeError, skipReason string) error {
	switch state {
	case SourcePreparedSucceeded:
		if safeError != "" || skipReason != "" || variant == nil || variant.ID == uuid.Nil || variant.FFProbeVersion == nil || *variant.FFProbeVersion == "" || !validProbePayload(variant.FFProbeJSON) || !validJSONObject(variant.ObservedTags) || variant.AnalysisPolicyVersion == nil || *variant.AnalysisPolicyVersion < 1 || variant.InspectedAt == nil || variant.InspectedAt.IsZero() || variant.AppliedOperationID == nil || *variant.AppliedOperationID == uuid.Nil || variant.AudioStreamCount == nil || *variant.AudioStreamCount < 0 {
			return fmt.Errorf("successful probe requires a complete probe result")
		}
	case SourcePreparedFailed:
		if variant != nil || safeError == "" || skipReason != "" {
			return fmt.Errorf("failed probe requires only a safe error")
		}
	case SourcePreparedSkipped:
		if variant != nil || skipReason == "" || safeError != "" {
			return fmt.Errorf("skipped probe requires only a skip reason")
		}
	case SourcePreparedDeferred:
		if variant != nil || safeError != "" || skipReason != "" {
			return fmt.Errorf("deferred probe cannot carry a result, error, or skip reason")
		}
	case SourcePreparedNotRequested:
		if variant != nil || safeError != "" || skipReason != "" {
			return fmt.Errorf("not-requested probe cannot carry a result")
		}
	default:
		return fmt.Errorf("unknown step state %q", state)
	}
	return nil
}

func validProbePayload(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &object) != nil || object == nil {
		return false
	}
	if !validJSONObject(object["format"]) {
		return false
	}
	streams, ok := object["streams"]
	trimmedStreams := bytes.TrimSpace(streams)
	if !ok || len(trimmedStreams) == 0 || trimmedStreams[0] != '[' {
		return false
	}
	var decoded []json.RawMessage
	return json.Unmarshal(streams, &decoded) == nil && decoded != nil
}

func validJSONObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &object) == nil && object != nil
}

func validatePreparedFingerprint(prepared SourceScanPreparedAnalysis) error {
	result := prepared.FingerprintResult
	switch prepared.FingerprintState {
	case SourcePreparedSucceeded:
		if prepared.FingerprintSafeError != "" || prepared.FingerprintSkipReason != "" || result == nil || result.ID == uuid.Nil || result.FPCalcVersion == "" || result.VersionBanner == "" || result.AlgorithmNamespace == "" || result.AlgorithmID < 0 || result.AlgorithmID > 255 || result.Fingerprint == "" || result.ReportedDuration < 0 || math.IsNaN(result.ReportedDuration) || math.IsInf(result.ReportedDuration, 0) || result.CalculatedAt.IsZero() || result.AppliedOperationID == uuid.Nil || result.ParserContractVersion < 1 {
			return fmt.Errorf("successful fingerprint requires complete immutable provenance")
		}
	case SourcePreparedFailed:
		if result != nil || prepared.FingerprintSafeError == "" || prepared.FingerprintSkipReason != "" {
			return fmt.Errorf("failed fingerprint requires only a safe error")
		}
	case SourcePreparedSkipped:
		if result != nil || prepared.FingerprintSkipReason == "" || prepared.FingerprintSafeError != "" {
			return fmt.Errorf("skipped fingerprint requires only a skip reason")
		}
	case SourcePreparedDeferred:
		if result != nil || prepared.FingerprintSafeError != "" || prepared.FingerprintSkipReason != "" {
			return fmt.Errorf("deferred fingerprint cannot carry a result, error, or skip reason")
		}
	case SourcePreparedNotRequested:
		if result != nil || prepared.FingerprintSafeError != "" || prepared.FingerprintSkipReason != "" {
			return fmt.Errorf("not-requested fingerprint cannot carry a result")
		}
	default:
		return fmt.Errorf("unknown step state %q", prepared.FingerprintState)
	}
	return nil
}

// MarshalJSON ensures invalid or partial analysis cannot enter the private
// JSONB transport through the normal encoding path.
func (prepared SourceScanPreparedAnalysis) MarshalJSON() ([]byte, error) {
	type wire SourceScanPreparedAnalysis
	if err := prepared.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(wire(prepared))
}

// UnmarshalJSON rejects corrupt or semantically incomplete JSONB data.
func (prepared *SourceScanPreparedAnalysis) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	requested, present := fields["hash_requested"]
	if !present || bytes.Equal(bytes.TrimSpace(requested), []byte("null")) {
		return fmt.Errorf("hash_requested policy is required")
	}
	var hashRequested bool
	if err := json.Unmarshal(requested, &hashRequested); err != nil {
		return fmt.Errorf("decode hash_requested policy: %w", err)
	}
	type wire SourceScanPreparedAnalysis
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	decoded.HashRequested = hashRequested
	result := SourceScanPreparedAnalysis(decoded)
	if err := result.Validate(); err != nil {
		return err
	}
	*prepared = result
	return nil
}
