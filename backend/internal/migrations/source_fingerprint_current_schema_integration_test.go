//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const currentFingerprintMigration = "20261021000000"

func TestCurrentFingerprintMigrationCollapsesSelectionsAndRollsBackWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, db, migrationsThrough(t, collection, "20261020000000"))

	digest := make([]byte, 32)
	digest[0] = 7
	shaID := insertFingerprintSHA(t, ctx, db, digest)
	stamp := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	old := insertCurrentFingerprintResult(t, ctx, db, stamp, "old")
	new := insertCurrentFingerprintResult(t, ctx, db, stamp.Add(time.Hour), "new")
	uncachedNewest := insertCurrentFingerprintResult(t, ctx, db, stamp.Add(2*time.Hour), "uncached")
	unknown := insertCurrentFingerprintResult(t, ctx, db, stamp.Add(3*time.Hour), "unknown")
	insertFingerprintWorkSelection(t, ctx, db, shaID, old)
	insertFingerprintWorkSelection(t, ctx, db, shaID, new)
	insertFingerprintWorkSelection(t, ctx, db, shaID, uncachedNewest)
	insertFingerprintWorkSelectionWithoutSHA(t, ctx, db, unknown)
	if _, err := db.ExecContext(ctx, `INSERT INTO media_fingerprint_cache(source_sha256,fpcalc_version,result_id) VALUES(?,?,?)`, digest, "fpcalc-old", old); err != nil {
		t.Fatalf("insert old cache row: %v", err)
	}
	applyMigrationsOneAtATime(t, ctx, db, []*migrate.Migration{migrationNamed(t, collection, currentFingerprintMigration)})

	var count int
	if err := db.NewRaw(`SELECT count(*) FROM media_fingerprint_result WHERE source_sha256=?`, digest).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("canonical result count = %d, %v; want one", count, err)
	}
	var winner uuid.UUID
	if err := db.NewRaw(`SELECT id FROM media_fingerprint_result WHERE source_sha256=?`, digest).Scan(ctx, &winner); err != nil || winner != uncachedNewest {
		t.Fatalf("canonical winner = %s, %v; want latest selected result %s", winner, err, uncachedNewest)
	}
	var steps int
	if err := db.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE step='fingerprint' AND success_fingerprint_result_id=?`, winner).Scan(ctx, &steps); err != nil || steps != 3 {
		t.Fatalf("remapped fingerprint steps = %d, %v; want three", steps, err)
	}
	var preserved bool
	if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_fingerprint_result WHERE id=?)`, unknown).Scan(ctx, &preserved); err != nil || !preserved {
		t.Fatalf("unknown-SHA result preserved = %v, %v", preserved, err)
	}
	var cacheTable string
	if err := db.NewRaw(`SELECT to_regclass('public.media_fingerprint_cache')::text`).Scan(ctx, &cacheTable); err != nil || cacheTable != "" {
		t.Fatalf("cache table after upgrade = %q, %v; want absent", cacheTable, err)
	}

	rollbackMigration(t, ctx, db, migrationNamed(t, collection, currentFingerprintMigration))
	// The down migration rebuilds the legacy cache from the surviving known-SHA
	// winner. The pre-migration loser row was deleted during upgrade, so only the
	// canonical result is restored, under its own fpcalc version.
	var restoredWinner uuid.UUID
	if err := db.NewRaw(`SELECT result_id FROM media_fingerprint_cache WHERE source_sha256=? AND fpcalc_version='fpcalc-uncached'`, digest).Scan(ctx, &restoredWinner); err != nil || restoredWinner != winner {
		t.Fatalf("restored cache winner = %s, %v; want surviving canonical result %s", restoredWinner, err, winner)
	}
	var legacyRestored int
	if err := db.NewRaw(`SELECT count(*) FROM media_fingerprint_cache WHERE source_sha256=? AND fpcalc_version='fpcalc-old'`, digest).Scan(ctx, &legacyRestored); err != nil || legacyRestored != 0 {
		t.Fatalf("pre-migration cache row restored = %d, %v; want none", legacyRestored, err)
	}
	var unknownRestored int
	if err := db.NewRaw(`SELECT count(*) FROM media_fingerprint_cache WHERE result_id=?`, unknown).Scan(ctx, &unknownRestored); err != nil || unknownRestored != 0 {
		t.Fatalf("unknown-SHA result restored into cache = %d, %v; want none", unknownRestored, err)
	}
	var survived int
	if err := db.NewRaw(`SELECT count(*) FROM media_fingerprint_result WHERE id IN (?,?,?)`, winner, unknown, old).Scan(ctx, &survived); err != nil || survived != 2 {
		t.Fatalf("rollback surviving canonical/unknown results = %d, %v; want two", survived, err)
	}
}

func TestCurrentFingerprintMigrationRefusesResultSharedAcrossDigests(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, db, migrationsThrough(t, collection, "20261020000000"))

	firstDigest := make([]byte, 32)
	firstDigest[0] = 11
	secondDigest := make([]byte, 32)
	secondDigest[0] = 12
	insertFingerprintSHA(t, ctx, db, firstDigest)
	insertFingerprintSHA(t, ctx, db, secondDigest)
	shared := insertCurrentFingerprintResult(t, ctx, db, time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), "shared")
	for _, digest := range [][]byte{firstDigest, secondDigest} {
		if _, err := db.ExecContext(ctx, `INSERT INTO media_fingerprint_cache(source_sha256,fpcalc_version,result_id) VALUES(?,?,?)`, digest, "fpcalc-shared", shared); err != nil {
			t.Fatalf("insert shared-digest legacy cache row: %v", err)
		}
	}

	migration := migrationNamed(t, collection, currentFingerprintMigration)
	single := migrate.NewMigrations()
	single.Add(*migration)
	migrator := migrate.NewMigrator(db, single, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize migrator: %v", err)
	}
	_, err := migrator.Migrate(ctx)
	if err == nil {
		t.Fatal("migration with one result bound to two distinct digests succeeded")
	}
	if !strings.Contains(err.Error(), "multiple source digests") {
		t.Fatalf("migration error = %v; want it to name multiple source digests", err)
	}

	var cacheTable string
	if err := db.NewRaw(`SELECT to_regclass('public.media_fingerprint_cache')::text`).Scan(ctx, &cacheTable); err != nil || cacheTable == "" {
		t.Fatalf("legacy cache table after refused migration = %q, %v; want it intact", cacheTable, err)
	}
	var cacheRows int
	if err := db.NewRaw(`SELECT count(*) FROM media_fingerprint_cache WHERE result_id=?`, shared).Scan(ctx, &cacheRows); err != nil || cacheRows != 2 {
		t.Fatalf("legacy cache rows after refused migration = %d, %v; want two", cacheRows, err)
	}
	var resultRows int
	if err := db.NewRaw(`SELECT count(*) FROM media_fingerprint_result WHERE id=?`, shared).Scan(ctx, &resultRows); err != nil || resultRows != 1 {
		t.Fatalf("legacy result rows after refused migration = %d, %v; want one", resultRows, err)
	}
	if columnExists(t, db, "media_fingerprint_result", "source_sha256") {
		t.Fatal("refused migration added media_fingerprint_result.source_sha256 despite rollback")
	}
}

func insertFingerprintSHA(t *testing.T, ctx context.Context, db *bun.DB, digest []byte) uuid.UUID {
	t.Helper()
	id, operation := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id) VALUES(?,1,?,?,?,?)`, id, digest, time.Now().UTC(), "SHA-256", operation); err != nil {
		t.Fatalf("insert fingerprint SHA: %v", err)
	}
	return id
}

func insertCurrentFingerprintResult(t *testing.T, ctx context.Context, db *bun.DB, calculated time.Time, version string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO media_fingerprint_result(id,fpcalc_version,version_banner,algorithm_namespace,algorithm_id,fingerprint,reported_duration,calculated_at,applied_operation_id,parser_contract_version) VALUES(?,?,?,'chromaprint',1,?,1,?,?,1)`, id, "fpcalc-"+version, "fpcalc test", version, calculated, uuid.New()); err != nil {
		t.Fatalf("insert fingerprint result: %v", err)
	}
	return id
}

func insertFingerprintWorkSelection(t *testing.T, ctx context.Context, db *bun.DB, shaID, resultID uuid.UUID) {
	insertFingerprintWorkSelectionWithOptionalSHA(t, ctx, db, &shaID, resultID)
}

func insertFingerprintWorkSelectionWithoutSHA(t *testing.T, ctx context.Context, db *bun.DB, resultID uuid.UUID) {
	insertFingerprintWorkSelectionWithOptionalSHA(t, ctx, db, nil, resultID)
}

func insertFingerprintWorkSelectionWithOptionalSHA(t *testing.T, ctx context.Context, db *bun.DB, shaID *uuid.UUID, resultID uuid.UUID) {
	t.Helper()
	rootID, locationID, workID, operationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path) VALUES(?,?,'test',1,'/inventory')`, rootID, "/source/"+rootID.String()); err != nil {
		t.Fatalf("insert fingerprint source root: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status) VALUES(?,?,?,1,?,1,'audio')`, locationID, rootID, uuid.NewString(), time.Now().UTC()); err != nil {
		t.Fatalf("insert fingerprint source location: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id) VALUES(?,?,?,?,?,?,1,?,true,?)`, workID, locationID, rootID, "/source", "/inventory", "track.flac", time.Now().UTC(), operationID); err != nil {
		t.Fatalf("insert fingerprint work: %v", err)
	}
	if shaID != nil {
		if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,success_sha_variant_id,success_reuse_origin) VALUES(?,'sha256','succeeded',?,'executed')`, workID, *shaID); err != nil {
			t.Fatalf("insert SHA step: %v", err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,success_fingerprint_result_id,success_reuse_origin) VALUES(?,'fingerprint','succeeded',?, 'executed')`, workID, resultID); err != nil {
		t.Fatalf("insert fingerprint step: %v", err)
	}
}
