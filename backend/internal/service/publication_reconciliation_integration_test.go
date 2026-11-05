//go:build integration

package service

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// reconciliationIntegrationInserter persists test deliveries in River's real
// table while retaining the returned row identities for assertions.
type reconciliationIntegrationInserter struct{}

func (reconciliationIntegrationInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	queue := "default"
	if opts != nil && opts.Queue != "" {
		queue = opts.Queue
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt,queue) VALUES ($1,$2,'available',0,$3) RETURNING id`, args.Kind(), string(encoded), queue).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: id}}, nil
}
