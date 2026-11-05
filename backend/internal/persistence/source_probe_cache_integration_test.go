//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

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
