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

const localMatchingReceiptsMigration = "20261214000000"

func TestLocalMatchingReceiptsMigrationRollbackWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	t.Cleanup(func() { testpostgres.Reset(t, database) })
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	migration := migrationNamed(t, collection, localMatchingReceiptsMigration)

	t.Run("empty receipt table rolls back and reapplies", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, localMatchingReceiptsMigration))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

		rollbackMigration(t, ctx, database, migration)
		if tableExists(t, database, "local_matching_receipt") {
			t.Fatal("receipt table remains after clean rollback")
		}
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
		if !tableExists(t, database, "local_matching_receipt") {
			t.Fatal("receipt table missing after reapplying migration")
		}
	})

	t.Run("populated receipts refuse destructive rollback", func(t *testing.T) {
		testpostgres.Reset(t, database)
		applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, localMatchingReceiptsMigration))
		applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
		groupID, requestID := uuid.New(), uuid.New()
		releaseID, sourceID := uuid.New(), uuid.New()
		if _, err := database.NewRaw(`INSERT INTO local_release (id,title) VALUES (?, 'pre-existing catalog')`, releaseID).Exec(ctx); err != nil {
			t.Fatalf("insert pre-existing local release: %v", err)
		}
		if _, err := database.NewRaw(`INSERT INTO source_root (id,configured_path,display_name) VALUES (?, '/local-matching-rollback', 'pre-existing source')`, sourceID).Exec(ctx); err != nil {
			t.Fatalf("insert pre-existing source root: %v", err)
		}
		intentHash, previewDigest := strings.Repeat("a", 64), strings.Repeat("b", 64)
		if _, err := database.NewRaw(`INSERT INTO local_matching_receipt
			(request_id,group_id,intent_hash,preview_digest,response)
			VALUES (?,?,?,?,'{}'::jsonb)`, requestID, groupID, intentHash, previewDigest).Exec(ctx); err != nil {
			t.Fatalf("insert receipt fixture: %v", err)
		}

		err := rollbackLocalMatchingReceiptsMigration(ctx, database, collection)
		if err == nil || !strings.Contains(err.Error(), "local matching receipts contain confirmation data") {
			t.Fatalf("rollback with receipt error = %v, want destructive rollback guard", err)
		}
		if !tableExists(t, database, "local_matching_receipt") {
			t.Fatal("blocked rollback removed receipt table")
		}
		var gotGroup uuid.UUID
		var gotIntentHash, gotPreviewDigest, gotResponse string
		if err := database.NewRaw(`SELECT group_id,intent_hash,preview_digest,response::text
			FROM local_matching_receipt WHERE request_id=?`, requestID).
			Scan(ctx, &gotGroup, &gotIntentHash, &gotPreviewDigest, &gotResponse); err != nil {
			t.Fatalf("read retained receipt: %v", err)
		}
		if gotGroup != groupID || gotIntentHash != intentHash || gotPreviewDigest != previewDigest || gotResponse != `{}` {
			t.Fatalf("blocked rollback changed receipt: group=%s hashes=%s/%s response=%s", gotGroup, gotIntentHash, gotPreviewDigest, gotResponse)
		}
		var retainedRelease, retainedSource int
		if err := database.NewRaw(`SELECT (SELECT count(*) FROM local_release WHERE id=?),
			(SELECT count(*) FROM source_root WHERE id=?)`, releaseID, sourceID).Scan(ctx, &retainedRelease, &retainedSource); err != nil {
			t.Fatalf("read retained catalog/source data: %v", err)
		}
		if retainedRelease != 1 || retainedSource != 1 {
			t.Fatalf("rollback changed pre-existing data: local release=%d source root=%d", retainedRelease, retainedSource)
		}
	})
}

func rollbackLocalMatchingReceiptsMigration(ctx context.Context, database *bun.DB, collection *migrate.Migrations) error {
	migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		return err
	}
	_, err := migrator.Rollback(ctx)
	return err
}

func tableExists(t *testing.T, database *bun.DB, name string) bool {
	t.Helper()
	var table *string
	if err := database.NewRaw(`SELECT to_regclass(?)`, "public."+name).Scan(context.Background(), &table); err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return table != nil
}
