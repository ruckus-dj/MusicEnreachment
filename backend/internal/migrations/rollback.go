package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

// RollbackLastGroup rolls back the last group of embedded application
// migrations. Stop the application before running this command. Bun applies
// each .tx migration in its own transaction; for rollback we need one
// transaction encompassing every down file and its migration-history updates.
func RollbackLastGroup(ctx context.Context, db *bun.DB) (*migrate.MigrationGroup, error) {
	collection, err := Collection()
	if err != nil {
		return nil, err
	}
	known := make(map[string]string)
	for _, migration := range collection.Sorted() {
		known[migration.Name] = migration.String() + ".tx.down.sql"
	}
	var group *migrate.MigrationGroup
	// Down migrations may lock application tables before checking for data.
	// Read committed lets those checks see writes that commit while we wait.
	err = db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `LOCK TABLE bun_migrations IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return fmt.Errorf("lock application migration history: %w", err)
		}
		var applied migrate.MigrationSlice
		if err := tx.NewRaw(`SELECT id,name,group_id,migrated_at FROM bun_migrations
			WHERE group_id=(SELECT max(group_id) FROM bun_migrations) ORDER BY name`).Scan(ctx, &applied); err != nil {
			return fmt.Errorf("read last application migration group: %w", err)
		}
		group = applied.LastGroup()
		seen := make(map[string]bool, len(group.Migrations))
		for i := len(group.Migrations) - 1; i >= 0; i-- {
			migration := &group.Migrations[i]
			filename, exists := known[migration.Name]
			if !exists || seen[migration.Name] {
				return fmt.Errorf("invalid migration history for %q", migration.Name)
			}
			seen[migration.Name] = true
			if err := applyMigrationDown(ctx, tx, filename); err != nil {
				return fmt.Errorf("roll back migration %s: %w", migration.Name, err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM bun_migrations WHERE id=?`, migration.ID); err != nil {
				return fmt.Errorf("remove migration history for %s: %w", migration.Name, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return group, nil
}

func applyMigrationDown(ctx context.Context, tx bun.Tx, filename string) error {
	body, err := files.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read embedded down migration: %w", err)
	}
	var query strings.Builder
	exec := func() error {
		if strings.TrimSpace(query.String()) == "" {
			return nil
		}
		_, err := tx.ExecContext(ctx, query.String())
		query.Reset()
		return err
	}
	for line := range strings.SplitSeq(string(body), "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case line == "--bun:split":
			if err := exec(); err != nil {
				return err
			}
		case strings.HasPrefix(line, "--bun:"):
			return fmt.Errorf("unsupported migration directive %q", line)
		default:
			query.WriteString(line)
			query.WriteByte('\n')
		}
	}
	return exec()
}
