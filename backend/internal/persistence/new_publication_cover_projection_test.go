package persistence

import (
	"testing"

	"github.com/google/uuid"
)

func TestPublicationCoverEmbeddingProjectionVersionedOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		version int
		outcome string
		warning string
		valid   bool
	}{
		{name: "legacy rejects preparation failure", version: PublicationCoverEmbeddingProjectionVersion, outcome: "preparation_failed", warning: "decode_failed"},
		{name: "legacy rejects interrupted absence", version: PublicationCoverEmbeddingProjectionVersion, outcome: "absent_after_interruption", warning: "cover_absent_after_interruption"},
		{name: "derivative accepts preparation failure", version: PublicationCoverEmbeddingDerivativeProjectionVersion, outcome: "preparation_failed", warning: "decode_failed", valid: true},
		{name: "derivative accepts interrupted absence", version: PublicationCoverEmbeddingDerivativeProjectionVersion, outcome: "absent_after_interruption", warning: "cover_absent_after_interruption", valid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projection := PublicationCoverEmbeddingProjection{
				Version: test.version, Generation: uuid.New(), ReceiptOperationID: uuid.New(),
				ReceiptReleaseID: uuid.New(), ReceiptRevision: 1, ReceiptMode: "automatic",
				RequestedGeneration: uuid.New(), RequestedSHA256: testAssetDigest("requested cover"),
				Outcome: test.outcome, WarningCode: test.warning,
			}
			err := projection.Validate()
			if (err == nil) != test.valid {
				t.Fatalf("projection validation error = %v, valid = %t", err, test.valid)
			}
		})
	}
}
