package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/static"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/migrate"
)

type Config struct {
	DatabaseURL string
	Logger      *slog.Logger
}

func Run(ctx context.Context, config Config) error {
	if config.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	sqldb, err := sql.Open("pgx", config.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open PostgreSQL: %w", err)
	}
	if err := sqldb.PingContext(ctx); err != nil {
		_ = sqldb.Close()
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	db := bun.NewDB(sqldb, pgdialect.New())
	defer func() { _ = db.Close() }()
	if err := applyMigrations(ctx, db); err != nil {
		return err
	}
	logger.InfoContext(ctx, "application migrations complete")

	riverClient, riverListenerPool, err := jobs.Start(ctx, config.DatabaseURL, sqldb)
	if err != nil {
		return err
	}
	defer riverListenerPool.Close()
	logger.InfoContext(ctx, "River started")
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = riverClient.Stop(shutdown)
	}()

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(api.RequestIDHeader)
	router.Use(api.RequestLogger(logger))
	router.Use(api.RecoverPanics(logger))
	router.Get("/health/live", api.Liveness)
	router.Get("/health/ready", api.Readiness(sqldb))
	router.Mount("/api", api.Handler())
	router.Handle("/*", static.Handler())
	server := &http.Server{
		Addr:              ":8080",
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverDone := make(chan struct{})
	shutdownResult := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			shutdownResult <- server.Shutdown(shutdown)
		case <-serverDone:
			shutdownResult <- nil
		}
	}()

	logger.InfoContext(ctx, "HTTP server starting", "address", server.Addr)
	serveErr := server.ListenAndServe()
	close(serverDone)
	if shutdownErr := <-shutdownResult; shutdownErr != nil {
		logger.Warn("HTTP server did not finish graceful shutdown", "error", shutdownErr)
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", serveErr)
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
