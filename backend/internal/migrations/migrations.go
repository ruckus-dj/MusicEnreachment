package migrations

import (
	"embed"
	"fmt"

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
