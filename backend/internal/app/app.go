package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/static"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/migrate"
)

type Config struct {
	DatabaseURL string
}

func Run(ctx context.Context, config Config) error {
	if config.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(config.DatabaseURL)))
	defer func() { _ = sqldb.Close() }()
	if err := sqldb.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	db := bun.NewDB(sqldb, pgdialect.New())
	defer func() { _ = db.Close() }()
	if err := applyMigrations(ctx, db); err != nil {
		return err
	}

	riverClient, riverPool, err := jobs.Start(ctx, config.DatabaseURL)
	if err != nil {
		return err
	}
	defer riverPool.Close()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = riverClient.Stop(shutdown)
	}()

	router := chi.NewRouter()
	router.Mount("/api", api.Handler())
	router.Handle("/*", static.Handler())
	server := &http.Server{Addr: ":8080", Handler: router}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	go func() {
		<-stop
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}

func applyMigrations(ctx context.Context, db *bun.DB) error {
	collection, err := migrations.Collection()
	if err != nil {
		return err
	}
	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		return fmt.Errorf("initialize migrations: %w", err)
	}
	if _, err := migrator.Migrate(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
