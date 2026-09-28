// Package testpostgres provides PostgreSQL integration-test access through Testcontainers.
package testpostgres

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/migrate"
)

func Open(t *testing.T) *bun.DB {
	t.Helper()
	ctx := context.Background()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		container, err := postgres.Run(ctx, "postgres:17",
			postgres.WithDatabase("melotrove_test"),
			postgres.WithUsername("postgres"),
			postgres.WithPassword("postgres"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(30*time.Second),
			),
		)
		if err != nil {
			t.Fatalf("start PostgreSQL Testcontainer: %v", err)
		}
		t.Cleanup(func() {
			if err := container.Terminate(context.Background()); err != nil {
				t.Errorf("terminate PostgreSQL Testcontainer: %v", err)
			}
		})
		databaseURL, err = container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatalf("get PostgreSQL Testcontainer connection string: %v", err)
		}
	}
	database := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL))), pgdialect.New())
	if err := database.PingContext(context.Background()); err != nil {
		_ = database.Close()
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	connection, err := database.DB.Conn(context.Background())
	if err != nil {
		_ = database.Close()
		t.Fatalf("acquire PostgreSQL integration lock connection: %v", err)
	}
	if _, err := connection.ExecContext(context.Background(), "SELECT pg_advisory_lock(90828001)"); err != nil {
		_ = connection.Close()
		_ = database.Close()
		t.Fatalf("lock PostgreSQL integration database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = connection.ExecContext(context.Background(), "SELECT pg_advisory_unlock(90828001)")
		_ = connection.Close()
	})
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// ResetAndMigrate replaces the public schema in the isolated Testcontainer or explicit test database.
func ResetAndMigrate(t *testing.T, database *bun.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
		t.Fatalf("reset PostgreSQL integration schema: %v", err)
	}
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize migrations: %v", err)
	}
	if _, err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
}
