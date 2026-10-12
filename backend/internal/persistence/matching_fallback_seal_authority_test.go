package persistence

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestMatchingFallbackSealAuthorityCanonicalBytes(t *testing.T) {
	authority := MatchingFallbackSealAuthority{
		SchemaVersion:         matchingFallbackSealAuthoritySchemaVersion,
		Disposition:           "review_exhausted",
		RunID:                 uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
		WaitRevision:          9,
		Plan:                  json.RawMessage(`{"stages":[],"schema_version":1}`),
		FullEvaluationContext: json.RawMessage(`{"future_context":{"z":2,"a":1},"inventory":[{"future_release_field":"kept"}]}`),
		Digest:                "ignored-by-canonical-form",
	}
	canonical, err := canonicalMatchingFallbackSealAuthorityBytes(authority)
	if err != nil {
		t.Fatalf("canonicalize authority: %v", err)
	}

	var decoded MatchingFallbackSealAuthority
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		t.Fatalf("decode canonical authority: %v", err)
	}
	if decoded.Digest != "" || !matchingJSONEqual(decoded.Plan, authority.Plan) {
		t.Fatalf("canonical authority included digest or changed plan: %+v", decoded)
	}
	var context map[string]json.RawMessage
	if err := json.Unmarshal(decoded.FullEvaluationContext, &context); err != nil {
		t.Fatalf("decode full evaluation context: %v", err)
	}
	var futureContext map[string]json.RawMessage
	if err := json.Unmarshal(context["future_context"], &futureContext); err != nil || len(futureContext) != 2 {
		t.Fatalf("unknown context members were not retained: context=%s decoded=%v err=%v", decoded.FullEvaluationContext, futureContext, err)
	}

	ordered := authority
	ordered.Plan = json.RawMessage(`{"schema_version":1,"stages":[]}`)
	ordered.FullEvaluationContext = json.RawMessage(`{"inventory":[{"future_release_field":"kept"}],"future_context":{"a":1,"z":2}}`)
	ordered.Digest = "a-different-caller-digest"
	orderedCanonical, err := canonicalMatchingFallbackSealAuthorityBytes(ordered)
	if err != nil || !bytes.Equal(canonical, orderedCanonical) {
		t.Fatalf("canonical bytes depend on object order or caller digest: original=%s reordered=%s err=%v", canonical, orderedCanonical, err)
	}

	mutated := ordered
	mutated.FullEvaluationContext = json.RawMessage(`{"inventory":[{"future_release_field":"changed"}],"future_context":{"a":1,"z":2}}`)
	mutatedCanonical, err := canonicalMatchingFallbackSealAuthorityBytes(mutated)
	if err != nil || bytes.Equal(canonical, mutatedCanonical) {
		t.Fatalf("canonical bytes did not bind unknown nested inventory fields: original=%s mutated=%s err=%v", canonical, mutatedCanonical, err)
	}
}

func TestMatchingFallbackSearchReceiptSemanticDigest(t *testing.T) {
	legacy := MatchingFallbackSearchReceipt{
		PayloadDigestVersion: 0,
		Payload:              json.RawMessage(`{ "count": 1, "unknown": { "b": 2, "a": 1 }, "items": [1, 2] }`),
	}
	current := MatchingFallbackSearchReceipt{
		PayloadDigestVersion: 2,
		Payload:              json.RawMessage(`{"items":[1,2],"unknown":{"a":1,"b":2},"count":1}`),
	}
	legacyDigest, err := matchingFallbackSearchReceiptSemanticDigest(legacy)
	if err != nil {
		t.Fatalf("digest historical payload: %v", err)
	}
	currentDigest, err := matchingFallbackSearchReceiptSemanticDigest(current)
	if err != nil {
		t.Fatalf("digest current payload: %v", err)
	}
	if legacyDigest != currentDigest {
		t.Fatalf("semantically equal payloads differ: legacy=%s current=%s", legacyDigest, currentDigest)
	}

	mutated := legacy
	mutated.Payload = json.RawMessage(`{ "count": 1, "unknown": { "b": 2, "a": 1 }, "items": [2, 1] }`)
	mutatedDigest, err := matchingFallbackSearchReceiptSemanticDigest(mutated)
	if err != nil {
		t.Fatalf("digest payload with changed array order: %v", err)
	}
	if mutatedDigest == legacyDigest {
		t.Fatal("semantic digest ignored array order")
	}

	mutated = legacy
	mutated.Payload = json.RawMessage(`{ "count": 1, "unknown": { "b": 3, "a": 1 }, "items": [1, 2] }`)
	mutatedDigest, err = matchingFallbackSearchReceiptSemanticDigest(mutated)
	if err != nil {
		t.Fatalf("digest payload with changed unknown field: %v", err)
	}
	if mutatedDigest == legacyDigest {
		t.Fatal("semantic digest ignored unknown payload fields")
	}
}
