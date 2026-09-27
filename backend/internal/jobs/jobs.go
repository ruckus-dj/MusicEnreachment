package jobs

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func Start(ctx context.Context, databaseURL string) (*river.Client[pgx.Tx], *pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("create River pool: %w", err)
	}
	driver := riverpgxv5.New(pool)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("create River migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("apply River migrations: %w", err)
	}
	client, err := river.NewClient(driver, &river.Config{Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}}})
	if err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("create River client: %w", err)
	}
	if err := client.Start(ctx); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("start River: %w", err)
	}
	return client, pool, nil
}
