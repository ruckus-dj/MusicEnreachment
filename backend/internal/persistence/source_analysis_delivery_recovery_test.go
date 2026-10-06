package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestRecoverNormalizedSourceAnalysisDeliveryRejectsInvalidInputBeforeDatabaseAccess(t *testing.T) {
	validOperationID := uuid.New()
	validDelivery := SourceAnalysisOperationDelivery{Attempt: 1, JobID: 1}
	tests := []struct {
		name        string
		operationID uuid.UUID
		delivery    SourceAnalysisOperationDelivery
		safeError   string
	}{
		{name: "missing operation", operationID: uuid.Nil, delivery: validDelivery, safeError: "interrupted"},
		{name: "missing attempt", operationID: validOperationID, delivery: SourceAnalysisOperationDelivery{JobID: 1}, safeError: "interrupted"},
		{name: "missing job", operationID: validOperationID, delivery: SourceAnalysisOperationDelivery{Attempt: 1}, safeError: "interrupted"},
		{name: "empty safe error", operationID: validOperationID, delivery: validDelivery},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &SourceInventoryRepository{}
			err := repository.RecoverNormalizedSourceAnalysisDelivery(context.Background(), test.operationID, test.delivery, test.safeError)
			const want = "recover source analysis delivery: valid operation, delivery, and safe error are required"
			if err == nil || err.Error() != want {
				t.Fatalf("RecoverNormalizedSourceAnalysisDelivery() error = %v, want %q", err, want)
			}
		})
	}
}
