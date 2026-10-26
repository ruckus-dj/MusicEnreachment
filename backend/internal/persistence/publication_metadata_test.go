package persistence

import (
	"encoding/json"
	"testing"
)

func TestPublicationCommitMetadataRejectsUnknownAndNullFields(t *testing.T) {
	for _, raw := range []string{`{"version":1,"tags":{},"source_history":{}}`, `{"version":1,"tags":{"x":null}}`, `{"version":1,"tags":null}`, `null`} {
		var value PublicationCommitMetadata
		if err := strictMetadata(json.RawMessage(raw), &value); err == nil {
			t.Fatalf("accepted malformed metadata %s", raw)
		}
	}
}
