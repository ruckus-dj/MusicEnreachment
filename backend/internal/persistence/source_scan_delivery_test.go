package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestFinishSourceScanDeliveryRejectsInvalidTerminalStateBeforeDatabaseAccess(t *testing.T) {
	var repository *SourceInventoryRepository
	err := repository.FinishSourceScanDelivery(context.Background(), uuid.New(), 1, 1, "queued", "", "")
	if err == nil || err.Error() != "finish source scan delivery: invalid terminal state" {
		t.Fatalf("finish with a nonterminal state = %v, want invalid terminal state", err)
	}
}

func TestFinishSourceScanDeliveryRequiresSafeErrorBeforeDatabaseAccess(t *testing.T) {
	var repository *SourceInventoryRepository
	err := repository.FinishSourceScanDelivery(context.Background(), uuid.New(), 1, 1, "failed", "", "")
	if err == nil || err.Error() != "finish source scan delivery: a safe error is required" {
		t.Fatalf("finish a failed delivery without a safe error = %v, want safe error required", err)
	}
}
