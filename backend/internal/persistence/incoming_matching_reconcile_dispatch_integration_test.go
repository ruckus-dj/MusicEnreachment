//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"errors"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"sync"
)

type failAfterMatchingReconcileInsert struct{ persistence.RiverInserter }

func (client failAfterMatchingReconcileInsert) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := client.RiverInserter.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return result, err
	}
	return result, errors.New("injected dispatch failure")
}

type gatedMatchingReconcileInsert struct {
	persistence.RiverInserter
	inserted chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (client *gatedMatchingReconcileInsert) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := client.RiverInserter.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return result, err
	}
	client.once.Do(func() {
		close(client.inserted)
		<-client.release
	})
	return result, nil
}
