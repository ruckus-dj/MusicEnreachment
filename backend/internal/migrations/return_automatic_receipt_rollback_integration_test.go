//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const returnAutomaticReceiptMigration = "20261210000000"

func TestReturnAutomaticReceiptMigrationRollbackWithPostgreSQL(t *testing.T) {
	t.Parallel()

	database := testpostgres.Open(t)
	ctx := context.Background()
	collection := mustMigrations(t)
	migration := migrationNamed(t, collection, returnAutomaticReceiptMigration)

	t.Run("empty receipt table rolls back and reapplies", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, returnAutomaticReceiptMigration))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

		rollbackMigration(t, ctx, database, migration)
		assertReturnAutomaticReceiptSchema(t, ctx, database, false)

		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
		assertReturnAutomaticReceiptSchema(t, ctx, database, true)
	})

	t.Run("receipt prevents rollback without changing schema or data", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, returnAutomaticReceiptMigration))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

		groupID := uuid.New()
		requestID := uuid.New()
		if _, err := database.ExecContext(ctx, `INSERT INTO incoming_group (id, manual, revision)
			VALUES (?, false, 'rollback-guard-fixture')`, groupID); err != nil {
			t.Fatalf("insert incoming group: %v", err)
		}
		intentHash := strings.Repeat("a", 64)
		response := `{"group_id":"` + groupID.String() + `"}`
		if _, err := database.ExecContext(ctx, `INSERT INTO incoming_group_return_automatic_receipt
			(request_id, group_id, intent_hash, response) VALUES (?, ?, ?, ?::jsonb)`,
			requestID, groupID, intentHash, response); err != nil {
			t.Fatalf("insert return-to-automatic receipt: %v", err)
		}

		migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
		if _, err := migrator.Rollback(ctx); err == nil {
			t.Fatal("rollback with a stored return-to-automatic receipt succeeded")
		} else if !strings.Contains(err.Error(), "cannot roll back return-to-automatic receipts while rows remain") {
			t.Fatalf("rollback error = %q, want receipt guard", err)
		}

		assertReturnAutomaticReceiptSchema(t, ctx, database, true)
		var rows int
		if err := database.NewRaw(`SELECT count(*) FROM incoming_group_return_automatic_receipt
			WHERE request_id = ? AND group_id = ? AND intent_hash = ? AND response = ?::jsonb`,
			requestID, groupID, intentHash, response).Scan(ctx, &rows); err != nil {
			t.Fatalf("read return-to-automatic receipt after refused rollback: %v", err)
		}
		if rows != 1 {
			t.Fatalf("preserved return-to-automatic receipt count = %d, want 1", rows)
		}
	})
}

func assertReturnAutomaticReceiptSchema(t *testing.T, ctx context.Context, database *bun.DB, wantPresent bool) {
	t.Helper()
	var table *string
	if err := database.NewRaw(`SELECT to_regclass('public.incoming_group_return_automatic_receipt')`).Scan(ctx, &table); err != nil {
		t.Fatalf("check return-to-automatic receipt table: %v", err)
	}
	var index *string
	if err := database.NewRaw(`SELECT to_regclass('public.incoming_group_return_automatic_receipt_group_idx')`).Scan(ctx, &index); err != nil {
		t.Fatalf("check return-to-automatic receipt index: %v", err)
	}
	if (table != nil) != wantPresent || (index != nil) != wantPresent {
		t.Fatalf("return-to-automatic receipt schema present = (table: %t, index: %t), want %t", table != nil, index != nil, wantPresent)
	}
}
