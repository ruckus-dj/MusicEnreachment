package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type genreCatalogRefresher interface {
	RefreshDelivery(context.Context, uuid.UUID, int64) error
}

// GenreCatalogQueueEnqueuer routes only catalog-delivery inserts to their
// isolated River queue while preserving repository-supplied insertion opts.
type GenreCatalogQueueEnqueuer struct {
	inner persistence.GenreCatalogJobEnqueuer
}

func NewGenreCatalogQueueEnqueuer(inner persistence.GenreCatalogJobEnqueuer) *GenreCatalogQueueEnqueuer {
	return &GenreCatalogQueueEnqueuer{inner: inner}
}

func (enqueuer *GenreCatalogQueueEnqueuer) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if enqueuer == nil || enqueuer.inner == nil {
		return nil, fmt.Errorf("genre catalog River enqueuer is unavailable")
	}
	queueOpts := river.InsertOpts{Queue: GenreCatalogQueue}
	if opts != nil {
		queueOpts = *opts
		queueOpts.Queue = GenreCatalogQueue
	}
	return enqueuer.inner.InsertTx(ctx, tx, args, &queueOpts)
}

const GenreCatalogQueue = "genre-catalog"

// GenreCatalogRefreshDispatchArgs is a periodic maintenance tick, not a refresh
// delivery.
type GenreCatalogRefreshDispatchArgs struct{}

func (GenreCatalogRefreshDispatchArgs) Kind() string { return "genre_catalog_refresh_dispatch_v1" }
func (GenreCatalogRefreshDispatchArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: GenreCatalogQueue}
}

type genreCatalogDispatcher interface {
	DispatchDueRefresh(context.Context) error
}

type GenreCatalogRefreshDispatchWorker struct {
	river.WorkerDefaults[GenreCatalogRefreshDispatchArgs]
	dispatcher genreCatalogDispatcher
}

func NewGenreCatalogRefreshDispatchWorker(dispatcher genreCatalogDispatcher) *GenreCatalogRefreshDispatchWorker {
	return &GenreCatalogRefreshDispatchWorker{dispatcher: dispatcher}
}

func (worker *GenreCatalogRefreshDispatchWorker) Work(ctx context.Context, job *river.Job[GenreCatalogRefreshDispatchArgs]) error {
	if worker == nil || worker.dispatcher == nil || job == nil {
		return fmt.Errorf("genre catalog refresh dispatcher is unavailable")
	}
	return worker.dispatcher.DispatchDueRefresh(ctx)
}

var _ river.Worker[GenreCatalogRefreshDispatchArgs] = (*GenreCatalogRefreshDispatchWorker)(nil)

type GenreCatalogWorker struct {
	river.WorkerDefaults[persistence.GenreCatalogRefreshArgs]
	refresher genreCatalogRefresher
}

func NewGenreCatalogWorker(refresher genreCatalogRefresher) *GenreCatalogWorker {
	return &GenreCatalogWorker{refresher: refresher}
}

// Timeout disables River's default job deadline. The configured provider delay
// can make a complete catalog refresh longer than a minute; River shutdown and
// rescue cancellation still propagate through the work context.
func (*GenreCatalogWorker) Timeout(*river.Job[persistence.GenreCatalogRefreshArgs]) time.Duration {
	return -1
}

func (worker *GenreCatalogWorker) Work(ctx context.Context, job *river.Job[persistence.GenreCatalogRefreshArgs]) error {
	if worker == nil || worker.refresher == nil || job == nil {
		return fmt.Errorf("genre catalog worker is unavailable")
	}
	if job.Args.OperationID == uuid.Nil {
		return fmt.Errorf("genre catalog operation ID is unavailable")
	}
	return worker.refresher.RefreshDelivery(ctx, job.Args.OperationID, job.ID)
}

var _ river.Worker[persistence.GenreCatalogRefreshArgs] = (*GenreCatalogWorker)(nil)

func NewGenreCatalogRefreshPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return GenreCatalogRefreshDispatchArgs{}, nil
	}, &river.PeriodicJobOpts{ID: "musicbrainz-genre-catalog-refresh-dispatch"})
}
