package persistence

import (
	"encoding/json"
	"strconv"
	"testing"
)

func TestValidateCanonicalReplacementPrefixSharedRawRejectsNonTenRecordPrefix(t *testing.T) {
	for _, length := range []int{9, 11} {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			entries := make([]json.RawMessage, length)
			for i := range entries {
				entries[i] = json.RawMessage(`{}`)
			}
			if _, ok := validateCanonicalReplacementPrefixSharedRaw(entries, PublicationDeliveryGuard{}, nil); ok {
				t.Fatal("shared replacement prefix accepted a non-canonical record count")
			}
		})
	}
}
