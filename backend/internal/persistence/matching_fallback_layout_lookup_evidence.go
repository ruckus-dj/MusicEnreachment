package persistence

import (
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

// DecodeFallbackLayoutLookup validates one accepted automatic fallback lookup
// receipt as the exact singleton provider projection captured by
// captureAutomaticFallbackLookupReceipt. It performs no database or provider
// I/O and returns the hydrated release together with the layout proof a history
// validator can bind.
//
// The embedded AcceptedProjectionRevision is validated as a witness and copied
// verbatim; this decoder must never recompute it. An incomplete release remains
// decodable, leaving the layout solver's own completeness check to report the
// indeterminate outcome.
func DecodeFallbackLayoutLookup(receipt MatchingFallbackLookupReceipt, scope MatchingFallbackProviderSource, releaseKey string) (MatchingRelease, MatchingProviderLayoutEvidence, error) {
	snapshot, evidence, err := decodeFallbackLayoutLookup(receipt, scope, releaseKey)
	if err != nil {
		return MatchingRelease{}, MatchingProviderLayoutEvidence{}, err
	}
	return snapshot.Releases[0], evidence, nil
}

func decodeFallbackLayoutLookup(receipt MatchingFallbackLookupReceipt, scope MatchingFallbackProviderSource, releaseKey string) (ProviderMatchingSnapshot, MatchingProviderLayoutEvidence, error) {
	var snapshot ProviderMatchingSnapshot
	var evidence MatchingProviderLayoutEvidence
	key, err := uuid.Parse(releaseKey)
	if err != nil || key == uuid.Nil || key.String() != releaseKey {
		return snapshot, evidence, errors.New("fallback layout lookup requires a canonical release key")
	}
	if receipt.ProviderSourceID == uuid.Nil || receipt.ConfigurationIdentity == "" ||
		receipt.CacheKey != "release-lookup:"+releaseKey ||
		scope.ID == uuid.Nil || scope.ProviderID == uuid.Nil || scope.Endpoint == "" || scope.ConfigurationIdentity == "" ||
		receipt.ProviderSourceID != scope.ID || receipt.ConfigurationIdentity != scope.ConfigurationIdentity {
		return snapshot, evidence, errors.New("fallback layout lookup provider scope is invalid")
	}
	canonicalPayload, err := canonicalMatchingJSON(receipt.Payload)
	if err != nil || !jsonObject(canonicalPayload) {
		return snapshot, evidence, errors.New("fallback layout lookup payload is invalid")
	}
	canonicalProjection, err := canonicalMatchingJSON(receipt.AcceptedProjection)
	if err != nil {
		return snapshot, evidence, errors.New("fallback layout lookup accepted projection is invalid")
	}
	payloadDigest := sha256.Sum256(canonicalPayload)
	projectionDigest := sha256.Sum256(canonicalProjection)
	if !equalDigest(mustDecodeSHA256(receipt.PayloadSHA256), payloadDigest[:]) ||
		!equalDigest(mustDecodeSHA256(receipt.ProjectionSHA256), projectionDigest[:]) {
		return snapshot, evidence, errors.New("fallback layout lookup receipt digest changed")
	}
	if err := json.Unmarshal(canonicalProjection, &snapshot); err != nil {
		return snapshot, evidence, errors.New("fallback layout lookup accepted projection is invalid")
	}
	if len(snapshot.Releases) != 1 || len(snapshot.Responses) != 1 {
		return snapshot, evidence, errors.New("fallback layout lookup accepted projection is not a singleton")
	}
	if snapshot.Source.ID != receipt.ProviderSourceID || snapshot.Source.ConfigurationIdentity != receipt.ConfigurationIdentity ||
		snapshot.Source.ID != scope.ID || snapshot.Source.ProviderID != scope.ProviderID ||
		snapshot.Source.Endpoint != scope.Endpoint || snapshot.Source.ConfigurationIdentity != scope.ConfigurationIdentity ||
		snapshot.Source.Namespace == "" {
		return snapshot, evidence, errors.New("fallback layout lookup source scope changed")
	}
	release := snapshot.Releases[0]
	response := snapshot.Responses[receipt.CacheKey]
	if release.ID == uuid.Nil || release.ProviderKey != releaseKey || response == nil || response.ProviderSourceID != receipt.ProviderSourceID ||
		response.ProviderID != scope.ProviderID || response.CacheKey != receipt.CacheKey ||
		!equalOptionalString(response.Revision, receipt.CacheRevision) || !matchingJSONEqual(response.Payload, canonicalPayload) {
		return snapshot, evidence, errors.New("fallback layout lookup response changed")
	}
	if len(mustDecodeSHA256(snapshot.AcceptedProjectionRevision)) != sha256.Size {
		return snapshot, evidence, errors.New("fallback layout lookup accepted projection revision is invalid")
	}
	evidence = MatchingProviderLayoutEvidence{
		SchemaVersion:              MatchingProviderLayoutEvidenceVersion,
		ProviderSourceID:           snapshot.Source.ID,
		ProviderID:                 snapshot.Source.ProviderID,
		Namespace:                  snapshot.Source.Namespace,
		Endpoint:                   snapshot.Source.Endpoint,
		ConfigurationIdentity:      snapshot.Source.ConfigurationIdentity,
		ReleaseKeys:                []string{releaseKey},
		HydrationKeys:              []string{receipt.CacheKey},
		AcceptedProjectionRevision: snapshot.AcceptedProjectionRevision,
	}
	return snapshot, evidence, nil
}
