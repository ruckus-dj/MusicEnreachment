//go:build integration

package persistence_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	insertProbeCacheVariant(t, ctx, database, canonicalID, digest[:], 128, "7.1", 1)
	if _, err := database.ExecContext(ctx, `INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id) VALUES(?,?,?,?)`, digest[:], "7.1", 1, canonicalID); err != nil {
		t.Fatalf("register initial canonical probe: %v", err)
	}

	// A later ffprobe version must retain its own immutable result; it must not
	// replace the canonical row's earlier selected provenance.
	v2ID := uuid.New()
	insertProbeCacheVariant(t, ctx, database, v2ID, nil, 128, "7.2", 1)
	if _, err := database.ExecContext(ctx, `INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id) VALUES(?,?,?,?)`, digest[:], "7.2", 1, v2ID); err != nil {
		t.Fatalf("register later probe version: %v", err)
	}

	lookupDigest := [sha256.Size]byte(digest)
	if cached, found, err := repository.LookupSourceProbe(ctx, lookupDigest, "7.1", 1); err != nil || !found || cached.ID != canonicalID {
		t.Fatalf("original version 7.1 cache = %+v, %v, %v; want canonical result %s", cached, found, err, canonicalID)
	}
	for i := 0; i < 2; i++ {
		cached, found, err := repository.LookupSourceProbe(ctx, lookupDigest, "7.2", 1)
		if err != nil || !found || cached.ID != v2ID || len(cached.SourceSHA256) != 0 {
			t.Fatalf("version 7.2 lookup %d = %+v, %v, %v; want associated digestless result %s", i, cached, found, err, v2ID)
		}
	}
	if _, found, err := repository.LookupSourceProbe(ctx, lookupDigest, "7.2", 2); err != nil || found {
		t.Fatalf("wrong policy lookup = found %v, err %v; want cache miss", found, err)
	}
	if _, found, err := repository.LookupSourceProbe(ctx, lookupDigest, "7.3", 1); err != nil || found {
		t.Fatalf("wrong version lookup = found %v, err %v; want cache miss", found, err)
	}
	if err := repository.CleanupSourceMediaVariants(ctx, []uuid.UUID{v2ID}); err != nil {
		t.Fatalf("cleanup cache winner: %v", err)
	}
	if _, err := repository.GetSourceMediaVariant(ctx, v2ID); err != nil {
		t.Fatalf("cache winner was deleted as an orphan: %v", err)
	}

	// The association is constrained against actual result version, policy and
	// size; it cannot turn a mismatched immutable result into a cache hit.
	if _, err := database.ExecContext(ctx, `INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id) VALUES(?,?,?,?)`, digest[:], "wrong-version", 1, v2ID); err == nil {
		t.Fatal("accepted cache association with a mismatched ffprobe version")
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id) VALUES(?,?,?,?)`, digest[:], "7.2", 2, v2ID); err == nil {
		t.Fatal("accepted cache association with a mismatched policy")
	}
	otherDigest := sha256.Sum256([]byte("different size"))
	otherCanonicalID := uuid.New()
	insertProbeCacheVariant(t, ctx, database, otherCanonicalID, otherDigest[:], 129, "7.2", 1)
	if _, err := database.ExecContext(ctx, `INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id) VALUES(?,?,?,?)`, otherDigest[:], "7.2", 1, v2ID); err == nil {
		t.Fatal("accepted cache association with a mismatched canonical/result size")
	}
}

func TestSourceProbeCacheSupportsLargeExactFFProbeBannerWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)

	noise := make([]byte, 4096)
	if _, err := rand.Read(noise); err != nil {
		t.Fatalf("generate poorly-compressible ffprobe configuration: %v", err)
	}
	version := "ffprobe version 7.1\nconfiguration: --" + hex.EncodeToString(noise)
	if len(version) < 4096 {
		t.Fatalf("test banner length = %d, want at least 4 KiB", len(version))
	}
	digest := sha256.Sum256([]byte("large banner probe cache"))
	resultID := uuid.New()
	insertProbeCacheVariant(t, ctx, database, resultID, digest[:], 128, version, 1)
	if _, err := database.ExecContext(ctx, `INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id) VALUES(?,?,?,?)`, digest[:], version, 1, resultID); err != nil {
		t.Fatalf("register large ffprobe banner: %v", err)
	}
	cached, found, err := repository.LookupSourceProbe(ctx, digest, version, 1)
	if err != nil || !found || cached.ID != resultID || cached.FFProbeVersion == nil || *cached.FFProbeVersion != version {
		t.Fatalf("large-banner lookup = %+v, %v, %v; want exact banner result %s", cached, found, err, resultID)
	}
}

func insertProbeCacheVariant(t *testing.T, ctx context.Context, database *bun.DB, id uuid.UUID, digest []byte, size int64, version string, policy int) {
	t.Helper()
	probe := json.RawMessage(`{"format":{"format_name":"flac"}}`)
	tags := json.RawMessage(`{"ARTIST":["fixture"]}`)
	inspected := time.Now().UTC().Truncate(time.Microsecond)
	operationID := uuid.New()
	_, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count)
		VALUES(?,?,?,?,?,?,?,?,?::jsonb,?::jsonb,?,?,1)`, id, size, digest, nullableTime(len(digest) != 0, inspected), nullableText(len(digest) != 0, "SHA-256"), nullableUUID(len(digest) != 0, operationID), policy, version, probe, tags, inspected, operationID)
	if err != nil {
		t.Fatalf("insert probe cache variant: %v", err)
	}
}

func nullableTime(keep bool, value time.Time) any {
	if keep {
		return value
	}
	return nil
}

func nullableText(keep bool, value string) any {
	if keep {
		return value
	}
	return nil
}

func nullableUUID(keep bool, value uuid.UUID) any {
	if keep {
		return value
	}
	return nil
}
