package migrations

import (
	"context"
	"embed"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

//go:embed *.sql
var files embed.FS

func Collection() (*migrate.Migrations, error) {
	collection := migrate.NewMigrations()
	if err := collection.Discover(files); err != nil {
		return nil, fmt.Errorf("discover embedded migrations: %w", err)
	}
	return collection, nil
}

// Apply installs pending application migrations. It refuses schemas created
// by a different migration history rather than trying to install over them.
func Apply(ctx context.Context, db *bun.DB) error {
	collection, err := Collection()
	if err != nil {
		return err
	}
	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		return fmt.Errorf("initialize migrations: %w", err)
	}
	applied, err := migrator.AppliedMigrations(ctx)
	if err != nil {
		return fmt.Errorf("read application migration history: %w", err)
	}
	known := make(map[string]bool)
	for _, migration := range collection.Sorted() {
		known[migration.Name] = true
	}
	// Bun ignores applied versions that are missing from the collection.
	for _, migration := range applied {
		if !known[migration.Name] {
			return fmt.Errorf("unknown applied application migration %q: use a release that recognizes this migration, or configure a fresh database for a clean install", migration.Name)
		}
	}
	if _, err := migrator.Migrate(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
