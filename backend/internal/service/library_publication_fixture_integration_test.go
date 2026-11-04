//go:build integration

package service

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type libraryPublicationTestInserter struct{}

func (libraryPublicationTestInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state) VALUES ($1,$2::jsonb,'available') RETURNING id`, args.Kind(), string(raw)).Scan(&id); err != nil {
		return nil, err
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: id}}, nil
}
