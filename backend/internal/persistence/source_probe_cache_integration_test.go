//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestSourceProbeCacheKeepsVersionedDigestlessWinnersWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	digest := sha256.Sum256([]byte("versioned probe cache"))
	canonicalID := uuid.New()
	insertProbeCacheCanonical(t, ctx, database, canonicalID, digest[:], 128)
	winnerID := uuid.New()
	insertProbeCacheVariant(t, ctx, database, winnerID, 128, "7.1", 1)
	if _, err := database.ExecContext(ctx, `INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id) VALUES(?,?,?,?)`, digest[:], "7.1", 1, winnerID); err != nil {
		t.Fatalf("register initial canonical probe: %v", err)
	}

	// A later result for the same cache key cannot replace the immutable winner.
	laterID := uuid.New()
	insertProbeCacheVariant(t, ctx, database, laterID, 128, "7.1", 1)
	if _, err := database.ExecContext(ctx, `INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id) VALUES(?,?,?,?) ON CONFLICT(source_sha256,ffprobe_version_sha256,analysis_policy_version) DO NOTHING`, digest[:], "7.1", 1, laterID); err != nil {
		t.Fatalf("register later probe: %v", err)
	}
	cached, found, err := repository.LookupSourceProbe(ctx, digest, "7.1", 1)
	if err != nil || !found || cached.ID != winnerID {
		t.Fatalf("probe cache lookup = %+v, %v, %v; want immutable winner %s", cached, found, err, winnerID)
	}
	if _, found, err := repository.LookupSourceProbe(ctx, digest, "7.1", 2); err != nil || found {
		t.Fatalf("wrong-policy probe cache lookup = found %v, err %v; want miss", found, err)
	}
	if _, found, err := repository.LookupSourceProbe(ctx, digest, "7.2", 1); err != nil || found {
		t.Fatalf("wrong-version probe cache lookup = found %v, err %v; want miss", found, err)
	}
	if err := repository.CleanupSourceMediaVariants(ctx, []uuid.UUID{winnerID}); err != nil {
		t.Fatalf("cleanup cached winner: %v", err)
	}
	if _, err := repository.GetSourceMediaVariant(ctx, winnerID); err != nil {
		t.Fatalf("cached winner was deleted as an orphan: %v", err)
	}
}

func TestSourceProbeCacheSupportsLargeExactFFProbeBannerWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	version := "ffprobe " + strings.Repeat("x", 4096)
	digest := sha256.Sum256([]byte("large banner probe cache"))
	canonicalID := uuid.New()
	insertProbeCacheCanonical(t, ctx, database, canonicalID, digest[:], 128)
	resultID := uuid.New()
	insertProbeCacheVariant(t, ctx, database, resultID, 128, version, 1)
	if _, err := database.ExecContext(ctx, `INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id) VALUES(?,?,?,?)`, digest[:], version, 1, resultID); err != nil {
		t.Fatalf("register large ffprobe banner: %v", err)
	}
	cached, found, err := repository.LookupSourceProbe(ctx, digest, version, 1)
	if err != nil || !found || cached.ID != resultID || cached.FFProbeVersion == nil || *cached.FFProbeVersion != version {
		t.Fatalf("large-banner lookup = %+v, %v, %v; want exact banner result %s", cached, found, err, resultID)
	}
}

func TestSourceFingerprintCacheLookupWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	digest := sha256.Sum256([]byte("fingerprint cache lookup"))
	canonicalID := uuid.New()
	insertProbeCacheCanonical(t, ctx, database, canonicalID, digest[:], 128)
	winner := &persistence.SourceFingerprintResult{
		ID: uuid.New(), FPCalcVersion: "1.5.1", VersionBanner: "fpcalc 1.5.1",
		AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "111,222",
		ReportedDuration: 12.5, CalculatedAt: time.Now().UTC().Truncate(time.Microsecond),
		AppliedOperationID: uuid.New(), ParserContractVersion: 1,
	}
	if _, err := database.NewInsert().Model(winner).Exec(ctx); err != nil {
		t.Fatalf("insert fingerprint winner: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO media_fingerprint_cache(source_sha256,fpcalc_version,result_id) VALUES(?,?,?)`, digest[:], winner.FPCalcVersion, winner.ID); err != nil {
		t.Fatalf("register fingerprint cache: %v", err)
	}
	cached, found, err := repository.LookupSourceFingerprint(ctx, digest, winner.FPCalcVersion)
	if err != nil || !found || cached.ID != winner.ID {
		t.Fatalf("fingerprint cache lookup = %+v, %v, %v; want winner %s", cached, found, err, winner.ID)
	}
}

func insertProbeCacheCanonical(t *testing.T, ctx context.Context, database *bun.DB, id uuid.UUID, digest []byte, size int64) {
	t.Helper()
	operationID := uuid.New()
	_, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id)
		VALUES(?,?,?,?,?,?)`, id, size, digest, time.Now().UTC().Truncate(time.Microsecond), "SHA-256", operationID)
	if err != nil {
		t.Fatalf("insert canonical digest: %v", err)
	}
}

func insertProbeCacheVariant(t *testing.T, ctx context.Context, database *bun.DB, id uuid.UUID, size int64, version string, policy int) {
	t.Helper()
	probe := json.RawMessage(`{"format":{"format_name":"flac"}}`)
	tags := json.RawMessage(`{"ARTIST":["fixture"]}`)
	inspected := time.Now().UTC().Truncate(time.Microsecond)
	operationID := uuid.New()
	_, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count)
		VALUES(?,?,?,?,?::jsonb,?::jsonb,?,?,1)`, id, size, policy, version, probe, tags, inspected, operationID)
	if err != nil {
		t.Fatalf("insert probe cache variant: %v", err)
	}
}
