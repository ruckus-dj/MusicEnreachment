package persistence

import (
	"encoding/json"

	"github.com/ruckus/MusicEnreachment/backend/internal/tagencoding"
)

// detectSourceTagEncoding builds the sparse repair overlay saved alongside one
// raw metadata capture. Detection is purely value-local and never reads the
// filesystem. A capture with no detected issue keeps a NULL overlay; the
// raw observed tags remain the source of truth and are never rewritten.
//
// Observed values that do not match the stored tag-map schema are ignored
// instead of failing metadata application, so a malformed synthetic capture
// cannot change analysis readiness.
func detectSourceTagEncoding(observed json.RawMessage) json.RawMessage {
	tags := decodeObservedTags(observed)
	if len(tags) == 0 {
		return nil
	}
	report := tagencoding.Analyze(tags)
	if len(report.Automatic.Entries) == 0 && len(report.Candidates) == 0 {
		return nil
	}
	encoded, err := json.Marshal(report)
	if err != nil || len(encoded) == 0 || string(encoded) == "null" {
		return nil
	}
	return encoded
}

func decodeObservedTags(observed json.RawMessage) map[string][]string {
	if len(observed) == 0 {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(observed, &raw); err != nil {
		return nil
	}
	tags := make(map[string][]string, len(raw))
	for key, value := range raw {
		var values []string
		if err := json.Unmarshal(value, &values); err != nil {
			continue
		}
		tags[key] = values
	}
	return tags
}
