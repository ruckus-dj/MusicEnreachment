package persistence

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestSettleNormalizedSourceAnalysisOperationRejectsInvalidTerminalState pins
// the preflight of the normalized settlement boundary. The repository carries
// no database, so any input that reached RunInTx would panic instead of
// returning. Every invalid terminal state, stage, or safe error must therefore
// be refused before any write is attempted.
func TestSettleNormalizedSourceAnalysisOperationRejectsInvalidTerminalState(t *testing.T) {
	repository := new(SourceInventoryRepository) // nil db: database access would panic
	ctx := context.Background()
	operationID := uuid.New()

	cases := []struct {
		name      string
		operation uuid.UUID
		state     string
		stage     string
		safeError string
	}{
		{name: "nil operation id", operation: uuid.Nil, state: "succeeded", stage: "done"},
		{name: "empty state", operation: operationID, state: "", stage: "done"},
		{name: "non-terminal state", operation: operationID, state: "running", stage: "done"},
		{name: "unknown state", operation: operationID, state: "canceled", stage: "done"},
		{name: "missing stage", operation: operationID, state: "succeeded", stage: ""},
		{name: "failed without safe error", operation: operationID, state: "failed", stage: "done"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := repository.SettleNormalizedSourceAnalysisOperation(ctx, test.operation, test.state, test.stage, test.safeError)
			if err == nil || !strings.Contains(err.Error(), "valid terminal state") {
				t.Fatalf("expected terminal-state preflight error, got %v", err)
			}
		})
	}
}

// TestSettleNormalizedSourceAnalysisDeliveryRejectsInvalidDelivery pins the
// River delivery fence of the same boundary. An invalid attempt or job identity
// must be refused before the transaction, so the nil-db repository proves no
// write is attempted for a stale or malformed delivery.
func TestSettleNormalizedSourceAnalysisDeliveryRejectsInvalidDelivery(t *testing.T) {
	repository := new(SourceInventoryRepository) // nil db: database access would panic
	ctx := context.Background()
	operationID := uuid.New()

	cases := []struct {
		name     string
		delivery SourceAnalysisOperationDelivery
	}{
		{name: "zero attempt and job", delivery: SourceAnalysisOperationDelivery{}},
		{name: "zero attempt", delivery: SourceAnalysisOperationDelivery{JobID: 7}},
		{name: "zero job", delivery: SourceAnalysisOperationDelivery{Attempt: 1}},
		{name: "negative identity", delivery: SourceAnalysisOperationDelivery{Attempt: -1, JobID: -1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := repository.SettleNormalizedSourceAnalysisDelivery(ctx, operationID, test.delivery, "succeeded", "done", "")
			if err == nil || !strings.Contains(err.Error(), "valid attempt and job identity") {
				t.Fatalf("expected delivery preflight error, got %v", err)
			}
		})
	}
}
