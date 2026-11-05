//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type failingAfterRiverInsert struct {
	base          RiverInserter
	insertedJobID *int64
}

func (inserter failingAfterRiverInsert) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := inserter.base.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return result, err
	}
	if result == nil || result.Job == nil {
		return result, fmt.Errorf("River insertion returned no job")
	}
	if inserter.insertedJobID != nil {
		*inserter.insertedJobID = result.Job.ID
	}
	return result, fmt.Errorf("injected failure after River insertion")
}

// This exercises the persisted CURRENT witness and candidate-universe query
// used by V2 admission. In particular, admission must read the real setting
// columns, not merely accept a service-built input contract.
