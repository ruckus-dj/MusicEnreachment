// Command migrate rolls back the last group of application migrations.
// Stop the application before running it. River owns its own schema lifecycle.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"log"
	"os"

	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/migrate"
)

func main() {
	rollback := flag.Bool("rollback", false, "roll back the last application migration group")
	flag.Parse()
	if err := run(context.Background(), *rollback, os.Getenv("DATABASE_URL")); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, rollback bool, databaseURL string) error {
	if !rollback {
		return errors.New("specify -rollback; forward migrations run at application startup")
	}
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL))), pgdialect.New())
	defer func() { _ = db.Close() }()
	collection, err := migrations.Collection()
	if err != nil {
		return err
	}
	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		return err
	}
	group, err := migrator.Rollback(ctx)
	if err != nil {
		return err
	}
	log.Printf("rolled back application migration group: %s", group)
	return nil
}
