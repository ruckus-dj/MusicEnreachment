package persistence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRejectManualOnlyMatchingEvaluation(t *testing.T) {
	tests := []struct {
		name      string
		evidence  string
		wantError string
	}{
		{
			name:     "automatic evaluation remains applicable",
			evidence: `{"evaluation_decision":{}}`,
		},
		{
			name:      "manual review authority cannot be automatically applied",
			evidence:  `{"evaluation_decision":{},"manual_authority":{"version":1,"execution_mode":"manual_review","selected_release_mbid":"00000000-0000-4000-8000-000000000001"}}`,
			wantError: "manual-review evaluations cannot be applied automatically",
		},
		{
			name:      "unknown manual authority fields fail closed",
			evidence:  `{"manual_authority":{"version":1,"execution_mode":"manual_review","selected_release_mbid":"00000000-0000-4000-8000-000000000001","unexpected":true}}`,
			wantError: "manual authority is invalid",
		},
		{
			name:      "malformed manual authority fails closed",
			evidence:  `{"manual_authority":{"version":1,"execution_mode":"automatic","selected_release_mbid":"00000000-0000-4000-8000-000000000001"}}`,
			wantError: "manual authority is invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := rejectManualOnlyMatchingEvaluation(json.RawMessage(test.evidence))
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}
