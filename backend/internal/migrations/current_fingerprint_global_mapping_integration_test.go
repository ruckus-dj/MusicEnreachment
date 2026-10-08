//go:build integration

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

func TestCurrentFingerprintMigrationMapsDigestlessReferencesAndStableTie(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, db, migrationsThrough(t, collection, "20261020000000"))

	digest := make([]byte, 32)
	digest[0] = 42
	shaID := insertFingerprintSHA(t, ctx, db, digest)
	stamp := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	canonicalID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	winnerID := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	loserID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	insertFingerprintResultWithID(t, ctx, db, canonicalID, stamp)
	insertFingerprintResultWithID(t, ctx, db, winnerID, stamp)
	insertFingerprintResultWithID(t, ctx, db, loserID, stamp)
	insertFingerprintWorkSelection(t, ctx, db, shaID, canonicalID)
	insertFingerprintWorkSelection(t, ctx, db, shaID, winnerID)
	insertFingerprintWorkSelection(t, ctx, db, shaID, loserID)
	insertDigestlessFingerprintWorkSelection(t, ctx, db, loserID)

	applyMigrationsOneAtATime(t, ctx, db, []*migrate.Migration{migrationNamed(t, collection, currentFingerprintMigration)})
	var winner uuid.UUID
	if err := db.NewRaw(`SELECT id FROM media_fingerprint_result WHERE source_sha256=?`, digest).Scan(ctx, &winner); err != nil || winner != winnerID {
		t.Fatalf("equal-time canonical winner = %s, %v; want greatest candidate UUID %s", winner, err, winnerID)
	}
	var digestlessWorkCount int
	if err := db.NewRaw(`SELECT count(*) FROM source_analysis_step fingerprint
		WHERE fingerprint.step='fingerprint' AND fingerprint.success_fingerprint_result_id=?
		AND NOT EXISTS (SELECT 1 FROM source_analysis_step sha WHERE sha.work_id=fingerprint.work_id AND sha.step='sha256')`, winner).Scan(ctx, &digestlessWorkCount); err != nil || digestlessWorkCount != 1 {
		t.Fatalf("digestless references remapped = %d, %v; want one", digestlessWorkCount, err)
	}
}

func insertFingerprintResultWithID(t *testing.T, ctx context.Context, db *bun.DB, id uuid.UUID, calculated time.Time) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO media_fingerprint_result(id,fpcalc_version,version_banner,algorithm_namespace,algorithm_id,fingerprint,reported_duration,calculated_at,applied_operation_id,parser_contract_version) VALUES(?,'fpcalc-test','fpcalc test','chromaprint',1,?,1,?,?,1)`, id, id.String(), calculated, uuid.New()); err != nil {
		t.Fatalf("insert fingerprint result %s: %v", id, err)
	}
}

func insertDigestlessFingerprintWorkSelection(t *testing.T, ctx context.Context, db *bun.DB, resultID uuid.UUID) {
	t.Helper()
	rootID, locationID, workID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path) VALUES(?,?,'test',1,'/inventory')`, rootID, "/source/"+rootID.String()); err != nil {
		t.Fatalf("insert digestless source root: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status) VALUES(?,?,?,1,?,1,'audio')`, locationID, rootID, uuid.NewString(), time.Now().UTC()); err != nil {
		t.Fatalf("insert digestless source location: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id) VALUES(?,?,?,?,?,?,1,?,true,?)`, workID, locationID, rootID, "/source", "/inventory", "digestless.flac", time.Now().UTC(), uuid.New()); err != nil {
		t.Fatalf("insert digestless work: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,success_fingerprint_result_id,success_reuse_origin) VALUES(?,'fingerprint','succeeded',?,'executed')`, workID, resultID); err != nil {
		t.Fatalf("insert digestless fingerprint reference: %v", err)
	}
}
