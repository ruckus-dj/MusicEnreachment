package jobs

import (
	"context"
	"database/sql"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type genreCatalogInsertCapture struct {
	args persistence.GenreCatalogRefreshArgs
	opts *river.InsertOpts
}

func (capture *genreCatalogInsertCapture) InsertTx(_ context.Context, _ *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if typed, ok := args.(persistence.GenreCatalogRefreshArgs); ok {
		capture.args = typed
	}
	capture.opts = opts
	return nil, nil
}

func TestGenreCatalogQueueEnqueuerRoutesRefreshToDedicatedQueue(t *testing.T) {
	capture := &genreCatalogInsertCapture{}
	enqueuer := NewGenreCatalogQueueEnqueuer(capture)
	if _, err := enqueuer.InsertTx(context.Background(), nil, persistence.GenreCatalogRefreshArgs{}, &river.InsertOpts{MaxAttempts: 9}); err != nil {
		t.Fatalf("InsertTx() error = %v", err)
	}
	if capture.opts == nil || capture.opts.Queue != GenreCatalogQueue || capture.opts.MaxAttempts != 9 {
		t.Fatalf("insert options = %+v, want queue %q and MaxAttempts 9", capture.opts, GenreCatalogQueue)
	}
}

func TestGenreCatalogDispatchTickUsesDedicatedQueue(t *testing.T) {
	if got := (GenreCatalogRefreshDispatchArgs{}).InsertOpts().Queue; got != GenreCatalogQueue {
		t.Fatalf("dispatch tick queue = %q, want %q", got, GenreCatalogQueue)
	}
}

func TestGenreCatalogWorkerHasNoArbitraryTimeout(t *testing.T) {
	if got := (*GenreCatalogWorker)(nil).Timeout(nil); got != -1 {
		t.Fatalf("genre catalog worker timeout = %s, want no deadline (-1)", got)
	}
}
