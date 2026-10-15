//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestReturnAutomaticReceiptCurrentSchemaWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	groupID := uuid.New()
	requestID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO incoming_group (id, manual, revision)
		VALUES (?, false, 'current-schema-receipt-fixture')`, groupID); err != nil {
		t.Fatalf("insert incoming group: %v", err)
	}
	intentHash := strings.Repeat("a", 64)
	response := `{"group_id":"` + groupID.String() + `"}`
	if _, err := database.ExecContext(ctx, `INSERT INTO incoming_group_return_automatic_receipt
		(request_id, group_id, intent_hash, response) VALUES (?, ?, ?, ?::jsonb)`,
		requestID, groupID, intentHash, response); err != nil {
		t.Fatalf("insert return-to-automatic receipt: %v", err)
	}
	assertReturnAutomaticReceiptSchema(t, ctx, database, true)
	var rows int
	if err := database.NewRaw(`SELECT count(*) FROM incoming_group_return_automatic_receipt
		WHERE request_id = ? AND group_id = ? AND intent_hash = ? AND response = ?::jsonb`,
		requestID, groupID, intentHash, response).Scan(ctx, &rows); err != nil {
		t.Fatalf("read return-to-automatic receipt: %v", err)
	}
	if rows != 1 {
		t.Fatalf("return-to-automatic receipt count = %d, want 1", rows)
	}
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
