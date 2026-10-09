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
	db := testpostgres.OpenMigrated(t)
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

func TestPromoteDigestlessMetadataLosingToCanonicalRewiresAndDeletes(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(db)
	client := openScanEnqueueRiver(t, db)
	digest := make([]byte, 32)
	digest[0] = 0xa2
	root := createInventoryRoot(t, ctx, repository, "/srv/metadata-collision")
	location := insertAnalysisLocation(t, ctx, db, root.ID, "track.flac", 4096, probeMtime())
	establishInventory(t, ctx, db, root)
	work := normalizedWork(t, ctx, repository, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepMetadata, State: "pending"},
	)
	fixture := newAnalysisStepFixture(t, ctx, db, repository, client, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, false, nil)
	oldTime := time.Now().UTC().Truncate(time.Microsecond)
	newerTime := oldTime.Add(time.Second)
	metadataAttempt := fixture.claim(persistence.SourceStepMetadata)
	loser, err := repository.ApplySourceMetadata(ctx, persistence.SourceMetadataApply{
		WorkID: work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: *fixture.operation.RiverJobID, StepAttempt: metadataAttempt,
		ObservedTags: json.RawMessage(`{"ARTIST":["older"]}`), Provenance: json.RawMessage(`{"reader":"loser"}`), ObservedAt: oldTime,
	})
	if err != nil {
		t.Fatalf("apply digestless metadata: %v", err)
	}

	otherRoot := createInventoryRoot(t, ctx, repository, "/srv/metadata-canonical")
	otherLocation := insertAnalysisLocation(t, ctx, db, otherRoot.ID, "other.flac", 4096, probeMtime())
	establishInventory(t, ctx, db, otherRoot)
	otherWork := normalizedWork(t, ctx, repository, otherRoot, otherLocation, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepMetadata, State: "pending"},
	)
	other := newAnalysisStepFixture(t, ctx, db, repository, client, otherRoot, otherLocation, otherWork, persistence.SourceAnalysisModeBatch, nil, nil, false, nil)
	otherSHA := other.claim(persistence.SourceStepSHA256)
	if _, err := repository.ApplySourceSHA256(ctx, persistence.SourceSHA256Apply{
		WorkID: otherWork.ID, OperationID: other.operation.ID, OperationAttempt: other.operation.Attempt,
		JobID: *other.operation.RiverJobID, StepAttempt: otherSHA, SHA256: digest, CalculatedAt: newerTime, Algorithm: "SHA-256",
	}); err != nil {
		t.Fatalf("apply canonical SHA-256: %v", err)
	}
	otherMetadata := other.claim(persistence.SourceStepMetadata)
	winner, err := repository.ApplySourceMetadata(ctx, persistence.SourceMetadataApply{
		WorkID: otherWork.ID, OperationID: other.operation.ID, OperationAttempt: other.operation.Attempt,
		JobID: *other.operation.RiverJobID, StepAttempt: otherMetadata,
		ObservedTags: json.RawMessage(`{"ARTIST":["canonical"]}`), Provenance: json.RawMessage(`{"reader":"winner"}`), ObservedAt: newerTime,
	})
	if err != nil {
		t.Fatalf("apply canonical metadata: %v", err)
	}
	if winner.ID == loser.ID {
		t.Fatal("digestless candidate unexpectedly became canonical identity")
	}

	shaAttempt := fixture.claim(persistence.SourceStepSHA256)
	if _, err := repository.ApplySourceSHA256(ctx, persistence.SourceSHA256Apply{
		WorkID: work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: *fixture.operation.RiverJobID, StepAttempt: shaAttempt, SHA256: digest, CalculatedAt: newerTime, Algorithm: "SHA-256",
	}); err != nil {
		t.Fatalf("promote colliding metadata: %v", err)
	}
	var count int
	if err := db.NewRaw(`SELECT count(*) FROM media_metadata_result WHERE id=?`, loser.ID).Scan(ctx, &count); err != nil || count != 0 {
		t.Fatalf("losing digestless row count = %d, %v; want deleted", count, err)
	}
	var winnerID, loserSelection, otherSelection uuid.UUID
	if err := db.NewRaw(`SELECT id FROM media_metadata_result WHERE source_sha256=?`, digest).Scan(ctx, &winnerID); err != nil {
		t.Fatalf("read canonical metadata ID: %v", err)
	}
	for _, selection := range []struct {
		workID uuid.UUID
		out    *uuid.UUID
	}{{work.ID, &loserSelection}, {otherWork.ID, &otherSelection}} {
		if err := db.NewRaw(`SELECT success_metadata_result_id FROM source_analysis_step WHERE work_id=? AND step='metadata'`, selection.workID).Scan(ctx, selection.out); err != nil {
			t.Fatalf("read metadata selection: %v", err)
		}
	}
	if loserSelection != winnerID || otherSelection != winnerID {
		t.Fatalf("rewired selections = %s and %s; want canonical %s", loserSelection, otherSelection, winnerID)
	}
	var stored persistence.SourceMetadataResult
	if err := db.NewRaw(`SELECT * FROM media_metadata_result WHERE id=?`, winnerID).Scan(ctx, &stored); err != nil {
		t.Fatalf("read canonical metadata payload: %v", err)
	}
	if !sameJSON(stored.ObservedTags, json.RawMessage(`{"ARTIST":["canonical"]}`)) || stored.WinningResultID != winner.WinningResultID {
		t.Fatalf("canonical payload changed during losing promotion: %+v", stored)
	}
}
