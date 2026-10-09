package jobs

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// BootstrapArgs keeps River startable before application jobs are introduced.
// No bootstrap jobs are scheduled automatically.
type BootstrapArgs struct{}

func (BootstrapArgs) Kind() string { return "bootstrap" }

type bootstrapWorker struct {
	river.WorkerDefaults[BootstrapArgs]
}

func (*bootstrapWorker) Work(context.Context, *river.Job[BootstrapArgs]) error { return nil }

func Start(ctx context.Context, databaseURL string, database *sql.DB) (*river.Client[*sql.Tx], *pgxpool.Pool, error) {
	return StartWithWorkers(ctx, databaseURL, database, nil)
}

func StartWithWorkers(ctx context.Context, databaseURL string, database *sql.DB, register func(*river.Workers), periodicJobs ...*river.PeriodicJob) (*river.Client[*sql.Tx], *pgxpool.Pool, error) {
	client, listenerPool, err := PrepareWithWorkers(ctx, databaseURL, database, register, periodicJobs...)
	if err != nil {
		return nil, nil, err
	}
	if err := client.Start(ctx); err != nil {
		listenerPool.Close()
		return nil, nil, fmt.Errorf("start River: %w", err)
	}
	return client, listenerPool, nil
}

// PrepareWithWorkers constructs an unstarted client so recovery can enqueue
// durable work before any worker begins consuming it.
func PrepareWithWorkers(ctx context.Context, databaseURL string, database *sql.DB, register func(*river.Workers), periodicJobs ...*river.PeriodicJob) (*river.Client[*sql.Tx], *pgxpool.Pool, error) {
	return prepareWithWorkers(ctx, databaseURL, database, register, 1, periodicJobs...)
}

// PrepareWithWorkersAndAnalysisConcurrency configures River's source-analysis
// queue from the runtime setting. A shared in-worker limiter remains the live
// authority if that setting changes before process restart.
func PrepareWithWorkersAndAnalysisConcurrency(ctx context.Context, databaseURL string, database *sql.DB, register func(*river.Workers), maxWorkers int, periodicJobs ...*river.PeriodicJob) (*river.Client[*sql.Tx], *pgxpool.Pool, error) {
	if maxWorkers < 1 {
		return nil, nil, fmt.Errorf("source file concurrency must be positive")
	}
	return prepareWithWorkers(ctx, databaseURL, database, register, maxWorkers, periodicJobs...)
}

// StartAnalysisConsumer starts an additional River client consuming only the
// source-analysis queue. Each client has an independent River worker limit;
// callers share the same durable worker registration and admission authority.
func StartAnalysisConsumer(ctx context.Context, databaseURL string, database *sql.DB, register func(*river.Workers), maxWorkers int) (*river.Client[*sql.Tx], *pgxpool.Pool, error) {
	return StartAnalysisConsumerWithLifetime(ctx, ctx, databaseURL, database, register, maxWorkers)
}

// StartAnalysisConsumerWithLifetime bounds setup by startCtx while keeping a
// successfully started client's lifetime attached to lifetimeCtx.
func StartAnalysisConsumerWithLifetime(startCtx, lifetimeCtx context.Context, databaseURL string, database *sql.DB, register func(*river.Workers), maxWorkers int) (*river.Client[*sql.Tx], *pgxpool.Pool, error) {
	if maxWorkers < 1 || maxWorkers > 10000 {
		return nil, nil, fmt.Errorf("source analysis consumer capacity must be between 1 and 10000")
	}
	if database == nil {
		return nil, nil, fmt.Errorf("start River analysis consumer: database pool is required")
	}
	listenerConfig, err := newListenerConfig(databaseURL)
	if err != nil {
		return nil, nil, err
	}
	listenerPool, err := pgxpool.NewWithConfig(startCtx, listenerConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("create River analysis listener pool: %w", err)
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &bootstrapWorker{})
	if register != nil {
		register(workers)
	}
	driver := riverdatabasesql.NewWithPgxListener(database, listenerPool)
	client, err := river.NewClient(driver, &river.Config{
		Workers: workers,
		Queues:  map[string]river.QueueConfig{service.SourceAnalysisQueue: {MaxWorkers: maxWorkers}},
	})
	if err != nil {
		listenerPool.Close()
		return nil, nil, fmt.Errorf("create River analysis consumer: %w", err)
	}
	if err := startCtx.Err(); err != nil {
		listenerPool.Close()
		return nil, nil, fmt.Errorf("start River analysis consumer: %w", err)
	}
	if err := client.Start(lifetimeCtx); err != nil {
		listenerPool.Close()
		return nil, nil, fmt.Errorf("start River analysis consumer: %w", err)
	}
	return client, listenerPool, nil
}

// AnalysisQueueDemand returns the number of active and available analysis jobs.
// River's cursor is followed to exhaustion; the page size is an I/O chunk, not
// a cap on visible demand.
func AnalysisQueueDemand(ctx context.Context, client *river.Client[*sql.Tx]) (int, error) {
	if client == nil {
		return 0, fmt.Errorf("analysis queue client is required")
	}
	demand := 0
	for _, state := range []rivertype.JobState{rivertype.JobStateRunning, rivertype.JobStateAvailable} {
		var cursor *river.JobListCursor
		for {
			params := river.NewJobListParams().Queues(service.SourceAnalysisQueue).States(state).First(10000)
			if cursor != nil {
				params.After(cursor)
			}
			page, err := client.JobList(ctx, params)
			if err != nil {
				return 0, fmt.Errorf("list source analysis queue demand: %w", err)
			}
			demand += len(page.Jobs)
			if page.LastCursor == nil || len(page.Jobs) == 0 {
				break
			}
			cursor = page.LastCursor
		}
	}
	return demand, nil
}

func prepareWithWorkers(ctx context.Context, databaseURL string, database *sql.DB, register func(*river.Workers), analysisWorkers int, periodicJobs ...*river.PeriodicJob) (*river.Client[*sql.Tx], *pgxpool.Pool, error) {
	if database == nil {
		return nil, nil, fmt.Errorf("start River: database pool is required")
	}
	listenerConfig, err := newListenerConfig(databaseURL)
	if err != nil {
		return nil, nil, err
	}
	listenerPool, err := pgxpool.NewWithConfig(ctx, listenerConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("create River listener pool: %w", err)
	}
	driver := riverdatabasesql.NewWithPgxListener(database, listenerPool)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		listenerPool.Close()
		return nil, nil, fmt.Errorf("create River migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		listenerPool.Close()
		return nil, nil, fmt.Errorf("apply River migrations: %w", err)
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &bootstrapWorker{})
	if register != nil {
		register(workers)
	}
	client, err := river.NewClient(driver, &river.Config{
		Workers: workers,
		Queues: map[string]river.QueueConfig{
			river.QueueDefault:          {MaxWorkers: 1},
			service.SourceAnalysisQueue: {MaxWorkers: analysisWorkers},
		},
		PeriodicJobs: periodicJobs,
	})
	if err != nil {
		listenerPool.Close()
		return nil, nil, fmt.Errorf("create River client: %w", err)
	}
	return client, listenerPool, nil
}

func newListenerConfig(databaseURL string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse River listener configuration: %w", err)
	}
	config.MinConns = 0
	config.MaxConns = 1
	return config, nil
}
