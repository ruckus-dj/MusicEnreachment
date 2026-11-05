//go:build integration

package service

import (
	"context"
	"database/sql"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type firstSourceSelectionFailingInserter struct {
	base     persistence.RiverInserter
	err      error
	inserted bool
}

func (inserter *firstSourceSelectionFailingInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := inserter.base.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return nil, err
	}
	inserter.inserted = result != nil && result.Job != nil && result.Job.ID > 0
	return nil, inserter.err
}
