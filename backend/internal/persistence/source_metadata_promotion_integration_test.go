//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestDigestlessMetadataPromotesAfterSHAWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(db)
	client := openScanEnqueueRiver(t, db)
	digest := make([]byte, 32)
	digest[0] = 0xa1
	root := createInventoryRoot(t, ctx, repository, "/srv/metadata-promotion")
	location := insertAnalysisLocation(t, ctx, db, root.ID, "track.flac", 4096, probeMtime())
	establishInventory(t, ctx, db, root)
	work := normalizedWork(t, ctx, repository, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepMetadata, State: "pending"},
	)
	fixture := newAnalysisStepFixture(t, ctx, db, repository, client, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, false, nil)
	metadataAttempt := fixture.claim(persistence.SourceStepMetadata)
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	result, err := repository.ApplySourceMetadata(ctx, persistence.SourceMetadataApply{
		WorkID: work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: *fixture.operation.RiverJobID, StepAttempt: metadataAttempt,
		ObservedTags: json.RawMessage(`{"ARTIST":["digestless"]}`), Provenance: json.RawMessage(`{"reader":"promotion"}`), ObservedAt: stamp,
	})
	if err != nil {
		t.Fatalf("apply digestless metadata: %v", err)
	}
	if len(result.SourceSHA256) != 0 {
		t.Fatalf("digestless metadata has digest %x", result.SourceSHA256)
	}
	var selectedID uuid.UUID
	if err := db.NewRaw(`SELECT success_metadata_result_id FROM source_analysis_step WHERE work_id=? AND step='metadata'`, work.ID).Scan(ctx, &selectedID); err != nil || selectedID != result.ID {
		t.Fatalf("digestless selected ID = %s, %v; want %s", selectedID, err, result.ID)
	}
	shaAttempt := fixture.claim(persistence.SourceStepSHA256)
	if _, err := repository.ApplySourceSHA256(ctx, persistence.SourceSHA256Apply{
		WorkID: work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: *fixture.operation.RiverJobID, StepAttempt: shaAttempt, SHA256: digest, CalculatedAt: stamp, Algorithm: "SHA-256",
	}); err != nil {
		t.Fatalf("apply SHA-256 and promote metadata: %v", err)
	}
	var promoted persistence.SourceMetadataResult
	if err := db.NewRaw(`SELECT * FROM media_metadata_result WHERE source_sha256=?`, digest).Scan(ctx, &promoted); err != nil {
		t.Fatalf("read promoted metadata: %v", err)
	}
	if promoted.ID != result.ID || !sameJSON(promoted.ObservedTags, result.ObservedTags) || promoted.WinningResultID != result.ID {
		t.Fatalf("promoted metadata = %+v; want digestless identity and payload %+v", promoted, result)
	}
	if err := db.NewRaw(`SELECT success_metadata_result_id FROM source_analysis_step WHERE work_id=? AND step='metadata'`, work.ID).Scan(ctx, &selectedID); err != nil || selectedID != result.ID {
		t.Fatalf("promoted selection = %s, %v; want %s", selectedID, err, result.ID)
	}
}
