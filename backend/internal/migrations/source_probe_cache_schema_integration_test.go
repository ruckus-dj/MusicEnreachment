//go:build integration

package migrations_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const (
	probeCacheMigration = "20261014000000"
	probeCacheBaseline  = "20261008120000"
)

func TestSourceProbeCacheSchemaBackfillWinnerAndGuardsWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, database, migrationsThrough(t, collection, probeCacheBaseline))

	var randomBanner [4096]byte
	if _, err := rand.Read(randomBanner[:]); err != nil {
		t.Fatal(err)
	}
	banner := base64.RawStdEncoding.EncodeToString(randomBanner[:])
	digest := sha256.Sum256([]byte("canonical source bytes"))
	canonical := insertProbeCacheSchemaVariant(t, ctx, database, digest[:], 8192, banner, 3)
	duplicate := insertProbeCacheSchemaVariant(t, ctx, database, nil, 8192, banner, 3)
	applyProbeCacheMigration(t, ctx, database, collection)

	var winner uuid.UUID
	var storedBanner string
	var storedDigest []byte
	wantBannerDigest := sha256.Sum256([]byte(banner))
	if err := database.NewRaw(`SELECT result_id, ffprobe_version, ffprobe_version_sha256 FROM media_probe_cache WHERE source_sha256=? AND analysis_policy_version=3`, digest[:]).Scan(ctx, &winner, &storedBanner, &storedDigest); err != nil {
		t.Fatalf("read backfilled probe cache entry: %v", err)
	}
	if winner != canonical || storedBanner != banner || len(storedBanner) < 4096 || len(storedDigest) != sha256.Size || string(storedDigest) != string(wantBannerDigest[:]) {
		t.Fatalf("backfilled cache winner/banner/digest = %s/%d/%x, want %s/large/SHA-256(banner)", winner, len(storedBanner), storedDigest, canonical)
	}

	// A duplicate primary key is ignored without replacing the first winner.
	if _, err := database.ExecContext(ctx, `INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id) VALUES(?,?,?,?) ON CONFLICT(source_sha256,ffprobe_version_sha256,analysis_policy_version) DO NOTHING`, digest[:], banner, 3, duplicate); err != nil {
		t.Fatalf("insert duplicate probe-cache association: %v", err)
	}
	assertProbeCacheWinner(t, ctx, database, digest[:], 3, canonical)

	for _, fixture := range []struct {
		name         string
		size         int64
		resultBanner string
		resultPolicy int
		cachePolicy  int
	}{
		{name: "size", size: 8191, resultBanner: banner, resultPolicy: 3, cachePolicy: 3},
		{name: "version", size: 8192, resultBanner: banner + "-different", resultPolicy: 3, cachePolicy: 3},
		{name: "policy", size: 8192, resultBanner: banner, resultPolicy: 3, cachePolicy: 4},
	} {
		t.Run(fixture.name+"_mismatch_rejected", func(t *testing.T) {
			wrong := insertProbeCacheSchemaVariant(t, ctx, database, nil, fixture.size, fixture.resultBanner, fixture.resultPolicy)
			_, err := database.ExecContext(ctx, `INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id) VALUES(?,?,?,?)`, digest[:], banner, fixture.cachePolicy, wrong)
			if err == nil || !strings.Contains(err.Error(), "probe cache association does not match") {
				t.Fatalf("mismatched probe association error = %v, want size/version/policy guard", err)
			}
		})
	}
}

func TestSourceProbeCacheRollbackRefusesNoncanonicalWinnerWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, database, migrationsThrough(t, collection, probeCacheBaseline))
	digest := sha256.Sum256([]byte("rollback canonical source"))
	canonical := insertProbeCacheSchemaVariant(t, ctx, database, digest[:], 128, "", 0)
	winner := insertProbeCacheSchemaVariant(t, ctx, database, nil, 128, "ffprobe test banner", 1)
	applyProbeCacheMigration(t, ctx, database, collection)
	if _, err := database.ExecContext(ctx, `INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id) VALUES(?,?,?,?)`, digest[:], "ffprobe test banner", 1, winner); err != nil {
		t.Fatalf("insert noncanonical cached winner: %v", err)
	}
	if winner == canonical {
		t.Fatal("rollback fixture winner unexpectedly points at the digest-bearing row")
	}
	assertProbeCacheWinner(t, ctx, database, digest[:], 1, winner)

	rollbackCollection := migrate.NewMigrations()
	rollbackCollection.Add(*migrationNamed(t, collection, probeCacheMigration))
	migrator := migrate.NewMigrator(database, rollbackCollection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize source probe cache rollback: %v", err)
	}
	if _, err := migrator.Rollback(ctx); err == nil {
		t.Fatal("rollback with a noncanonical cached winner succeeded")
	}
	var tableName string
	if err := database.NewRaw(`SELECT to_regclass('public.media_probe_cache')::text`).Scan(ctx, &tableName); err != nil || tableName == "" {
		t.Fatalf("probe cache table after refused rollback = %q, %v", tableName, err)
	}
	assertProbeCacheWinner(t, ctx, database, digest[:], 1, winner)
}

func migrationsThrough(t *testing.T, collection *migrate.Migrations, last string) []*migrate.Migration {
	t.Helper()
	var selected []*migrate.Migration
	found := false
	for _, migration := range collection.Sorted() {
		if migration.Name > last {
			break
		}
		selected = append(selected, &migration)
		found = found || migration.Name == last
	}
	if !found {
		t.Fatalf("baseline migration %s is missing", last)
	}
	return selected
}

func applyProbeCacheMigration(t *testing.T, ctx context.Context, database *bun.DB, collection *migrate.Migrations) {
	t.Helper()
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migrationNamed(t, collection, probeCacheMigration)})
}

func insertProbeCacheSchemaVariant(t *testing.T, ctx context.Context, database *bun.DB, digest []byte, size int64, version string, policy int) uuid.UUID {
	t.Helper()
	id, operationID := uuid.New(), uuid.New()
	if len(digest) == 0 {
		_, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES(?,?,?, ?,?::jsonb,'{}'::jsonb,?,?,1)`, id, size, policy, version, `{"streams":[{"codec_type":"audio"}]}`, time.Now().UTC(), operationID)
		if err != nil {
			t.Fatalf("insert digestless probe result: %v", err)
		}
		return id
	}
	if version == "" {
		_, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id) VALUES(?,?,?,?,?,?)`, id, size, digest, time.Now().UTC(), "SHA-256", operationID)
		if err != nil {
			t.Fatalf("insert canonical SHA-only result: %v", err)
		}
		return id
	}
	_, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES(?,?,?,?,?,?,?,?,?::jsonb,'{}'::jsonb,?,?,1)`, id, size, digest, time.Now().UTC(), "SHA-256", operationID, policy, version, `{"streams":[{"codec_type":"audio"}]}`, time.Now().UTC(), operationID)
	if err != nil {
		t.Fatalf("insert canonical probe result: %v", err)
	}
	return id
}

func assertProbeCacheWinner(t *testing.T, ctx context.Context, database *bun.DB, digest []byte, policy int, want uuid.UUID) {
	t.Helper()
	var got uuid.UUID
	if err := database.NewRaw(`SELECT result_id FROM media_probe_cache WHERE source_sha256=? AND analysis_policy_version=?`, digest, policy).Scan(ctx, &got); err != nil {
		t.Fatalf("read probe-cache winner: %v", err)
	}
	if got != want {
		t.Fatalf("probe-cache winner = %s, want first winner %s", got, want)
	}
}
