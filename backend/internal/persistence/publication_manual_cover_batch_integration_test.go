//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"errors"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type coverBatchInserter struct {
	RiverInserter
	calls  int
	failAt int
}

func (inserter *coverBatchInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := inserter.RiverInserter.InsertTx(ctx, tx, args, opts)
	inserter.calls++
	if err == nil && inserter.calls == inserter.failAt {
		return nil, errors.New("injected failure after real River insert")
	}
	return result, err
}
