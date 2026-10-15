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

func TestLocalMatchingReceiptCurrentSchemaWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	groupID, requestID := uuid.New(), uuid.New()
	if _, err := database.NewRaw(`INSERT INTO incoming_group (id, manual, revision) VALUES (?, false, 'local-receipt-current')`, groupID).Exec(ctx); err != nil {
		t.Fatalf("insert receipt group: %v", err)
	}
	intentHash, previewDigest := strings.Repeat("a", 64), strings.Repeat("b", 64)
	if _, err := database.NewRaw(`INSERT INTO local_matching_receipt
		(request_id,group_id,intent_hash,preview_digest,response) VALUES (?,?,?,?,'{}'::jsonb)`,
		requestID, groupID, intentHash, previewDigest).Exec(ctx); err != nil {
		t.Fatalf("insert current local matching receipt: %v", err)
	}
	if !tableExists(t, database, "local_matching_receipt") {
		t.Fatal("current schema is missing local_matching_receipt")
	}
	var gotGroup uuid.UUID
	var gotIntentHash, gotPreviewDigest, gotResponse string
	if err := database.NewRaw(`SELECT group_id,intent_hash,preview_digest,response::text
		FROM local_matching_receipt WHERE request_id=?`, requestID).
		Scan(ctx, &gotGroup, &gotIntentHash, &gotPreviewDigest, &gotResponse); err != nil {
		t.Fatalf("read current local receipt: %v", err)
	}
	if gotGroup != groupID || gotIntentHash != intentHash || gotPreviewDigest != previewDigest || gotResponse != `{}` {
		t.Fatalf("stored receipt = %s/%s/%s/%s", gotGroup, gotIntentHash, gotPreviewDigest, gotResponse)
	}
}

func tableExists(t *testing.T, database *bun.DB, name string) bool {
	t.Helper()
	var table *string
	if err := database.NewRaw(`SELECT to_regclass(?)`, "public."+name).Scan(context.Background(), &table); err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return table != nil
}
