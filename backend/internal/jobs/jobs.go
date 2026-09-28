package jobs

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
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

func StartWithWorkers(ctx context.Context, databaseURL string, database *sql.DB, register func(*river.Workers)) (*river.Client[*sql.Tx], *pgxpool.Pool, error) {
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
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
	})
	if err != nil {
		listenerPool.Close()
		return nil, nil, fmt.Errorf("create River client: %w", err)
	}
	if err := client.Start(ctx); err != nil {
		listenerPool.Close()
		return nil, nil, fmt.Errorf("start River: %w", err)
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
