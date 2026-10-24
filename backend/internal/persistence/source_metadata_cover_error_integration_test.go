//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestCanonicalMetadataRetainsAndReplacesOptionalCoverDiagnosticPostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	digest := make([]byte, 32)
	digest[0] = 1
	message := "artwork image dimensions exceed the maximum"
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstID := uuid.New()
	first := SourceMetadataResult{ID: firstID, ObservedTags: json.RawMessage(`{"TITLE":["kept"]}`),
		Provenance: json.RawMessage(`{}`), CoverObserved: true, CoverError: &message,
		ObservedAt: now, WinningResultID: firstID, AppliedOperationID: uuid.New()}
	stored, err := upsertCanonicalMetadataResult(ctx, db, digest, first)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CoverError == nil || *stored.CoverError != message || len(stored.CoverData) != 0 || !stored.CoverObserved {
		t.Fatalf("optional cover diagnostic lost: %+v", stored)
	}
	second := first
	second.ID = uuid.New()
	second.WinningResultID = second.ID
	second.ObservedAt = now.Add(time.Second)
	second.CoverError = nil
	updated, err := upsertCanonicalMetadataResult(ctx, db, digest, second)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != firstID || updated.CoverError != nil || string(updated.ObservedTags) == "" {
		t.Fatalf("current successful observation retained obsolete diagnostic: %+v", updated)
	}
}
