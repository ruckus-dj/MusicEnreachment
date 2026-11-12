package persistence

import (
	"encoding/json"
	"testing"
)

func TestAcceptedReleaseIdentifierStrings(t *testing.T) {
	tests := []struct {
		name       string
		asin       json.RawMessage
		script     json.RawMessage
		wantASIN   *string
		wantScript *string
		wantErr    bool
	}{
		{name: "omitted identifiers"},
		{name: "null identifiers", asin: json.RawMessage(`null`), script: json.RawMessage(`null`)},
		{name: "empty identifiers", asin: json.RawMessage(`""`), script: json.RawMessage(`{"script":""}`)},
		{name: "accepted identifiers preserve exact text", asin: json.RawMessage(`" b-001 "`), script: json.RawMessage(`{"script":"Latn"}`), wantASIN: stringPointer(" b-001 "), wantScript: stringPointer("Latn")},
		{name: "malformed ASIN", asin: json.RawMessage(`[]`), wantErr: true},
		{name: "malformed text representation", script: json.RawMessage(`"Latn"`), wantErr: true},
		{name: "malformed nested script", script: json.RawMessage(`{"script":42}`), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			asin, asinErr := decodeNonemptyAcceptedString(test.asin)
			script, scriptErr := acceptedReleaseScript(test.script)
			if test.wantErr {
				if asinErr == nil && scriptErr == nil {
					t.Fatal("malformed accepted identifier was not rejected")
				}
				return
			}
			if asinErr != nil || scriptErr != nil {
				t.Fatalf("decode accepted identifiers: ASIN error=%v, script error=%v", asinErr, scriptErr)
			}
			if !equalStringPointer(asin, test.wantASIN) || !equalStringPointer(script, test.wantScript) {
				t.Fatalf("decoded ASIN/script = %v/%v, want %v/%v", asin, script, test.wantASIN, test.wantScript)
			}
		})
	}
}

func equalStringPointer(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
