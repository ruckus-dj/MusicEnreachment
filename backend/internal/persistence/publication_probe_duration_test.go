package persistence

import (
	"encoding/json"
	"testing"
)

func TestExactStoredMetadataDurationUsesExactFFProbeDuration(t *testing.T) {
	duration, err := exactStoredMetadataDuration(json.RawMessage(`{"format":{"duration":"183.456789"},"properties":{"length":183375000000}}`))
	if err != nil {
		t.Fatal(err)
	}
	if duration == nil || *duration != 183.456789 {
		t.Fatalf("duration = %v, want exact FFprobe duration 183.456789", duration)
	}
}

func TestExactStoredMetadataDurationTreatsMissingOrInvalidAsUnknown(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"format":{"duration":"not-a-number"}}`,
		`{"format":{"duration":"0"}}`,
		`{"format":{"duration":"NaN"}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			duration, err := exactStoredMetadataDuration(json.RawMessage(raw))
			if err != nil {
				t.Fatal(err)
			}
			if duration != nil {
				t.Fatalf("duration = %v, want unknown", *duration)
			}
		})
	}
}
