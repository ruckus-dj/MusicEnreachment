//go:build integration

package migrations_test

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestSourceMetadataMediaFactsSchemaWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	insert := func(coverObserved bool, data []byte, mime string, digest []byte, size any) error {
		t.Helper()
		id := uuid.New()
		_, err := db.ExecContext(ctx, `INSERT INTO media_metadata_result
			(id,observed_tags,provenance,cover_observed,cover_data,cover_mime,cover_sha256,cover_size_bytes,observed_at,winning_result_id,applied_operation_id)
			VALUES(?,'{}','{}',?,?,?,?,?,now(),?,?)`, id, coverObserved, data, nullableText(mime), digest, size, id, id)
		return err
	}
	if err := insert(false, nil, "", nil, nil); err != nil {
		t.Fatalf("insert legacy unknown metadata facts: %v", err)
	}
	if err := insert(true, nil, "", nil, nil); err != nil {
		t.Fatalf("insert observed metadata without a cover: %v", err)
	}
	data := []byte("cover bytes")
	digest := sha256.Sum256(data)
	if err := insert(true, data, "image/jpeg", digest[:], len(data)); err != nil {
		t.Fatalf("insert complete observed cover facts: %v", err)
	}
	if err := insert(true, data, "", digest[:], len(data)); err == nil {
		t.Fatal("partial cover facts inserted; want strict all-null/all-present rejection")
	}
	if err := insert(false, data, "image/jpeg", digest[:], len(data)); err == nil {
		t.Fatal("cover facts without a cover observation inserted")
	}
	if err := insert(true, data, "image/jpeg", digest[:], len(data)+1); err == nil {
		t.Fatal("inconsistent cover byte size inserted")
	}
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
