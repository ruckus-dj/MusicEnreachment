//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type publicationSourceCleanupFailFirstInserter struct {
	base      publicationSourceCleanupTestInserter
	calls     int
	failures  int
	successes int
	err       error
}

func (inserter *publicationSourceCleanupFailFirstInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := inserter.base.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return nil, err
	}
	inserter.calls++
	if inserter.calls == 1 {
		inserter.failures++
		return nil, inserter.err
	}
	inserter.successes++
	return result, nil
}

func insertCurrentCleanupRequest(t *testing.T, f publicationExecutionFixture) {
	t.Helper()
	if _, err := f.db.ExecContext(context.Background(), `INSERT INTO publication_source_cleanup_request(originating_operation_id,position_id,output_epoch,publication_generation) VALUES (?,?,?,?)`, f.guard.OperationID, f.guard.ExpectedPositionID, f.guard.Epoch, f.guard.CandidateGeneration); err != nil {
		t.Fatal(err)
	}
}
