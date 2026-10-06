package persistence

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type sourceAnalysisAdmissionTestInserter struct {
	called bool
}

func (inserter *sourceAnalysisAdmissionTestInserter) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	inserter.called = true
	return nil, nil
}

func TestCreateNormalizedSourceAnalysisOperationAndEnqueuePreflight(t *testing.T) {
	t.Run("requires River client", func(t *testing.T) {
		repository := new(SourceInventoryRepository)
		if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(context.Background(), nil, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "River client is required") {
			t.Fatalf("expected missing-client preflight error, got %v", err)
		}
	})

	t.Run("rejects invalid snapshot before database access", func(t *testing.T) {
		repository := new(SourceInventoryRepository)
		inserter := new(sourceAnalysisAdmissionTestInserter)
		operation := &Operation{Kind: analysisSourceOperationKind, SourceAnalysisMode: SourceAnalysisModeSingleStep, InputSnapshot: []byte("{")}
		err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(context.Background(), operation, inserter, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "decode source analysis operation snapshot") {
			t.Fatalf("expected invalid-snapshot preflight error, got %v", err)
		}
		if inserter.called {
			t.Fatal("River job insert ran before snapshot validation")
		}
	})
}
