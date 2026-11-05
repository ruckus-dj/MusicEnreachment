//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"errors"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type failingSecondHydrationInsert struct {
	persistence.RiverInserter
	inserts int
}

func (inserter *failingSecondHydrationInsert) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	inserter.inserts++
	if inserter.inserts == 2 {
		return nil, errors.New("injected second hydration enqueue failure")
	}
	return inserter.RiverInserter.InsertTx(ctx, tx, args, options)
}
