//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"errors"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type obsoleteCleanupTestInserter struct{ failAfterInsert bool }

func (i obsoleteCleanupTestInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt) VALUES ($1,$2::jsonb,'available',0) RETURNING id`, args.Kind(), `{"operation_id":"`+args.(ObsoletePublicationOutputCleanupJobArgs).OperationID.String()+`"}`).Scan(&id)
	if err != nil {
		return nil, err
	}
	if i.failAfterInsert {
		return nil, errors.New("injected enqueue failure")
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: id}}, nil
}
