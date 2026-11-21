//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
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
		ObservedTags: json.RawMessage("{\"ARTIST\":[\"Punk-O-\u00cc\u00e0\u00ed\u00e8\u00ff 2\"]}"), Provenance: json.RawMessage(`{"reader":"promotion"}`), ObservedAt: stamp,
	})
	if err != nil {
		t.Fatalf("apply digestless metadata: %v", err)
	}
	if len(result.SourceSHA256) != 0 {
		t.Fatalf("digestless metadata has digest %x", result.SourceSHA256)
	}
	if len(result.TagEncoding) == 0 {
		t.Fatal("digestless metadata lost the detected encoding overlay")
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
	if promoted.ID != result.ID || !sameJSON(promoted.ObservedTags, result.ObservedTags) || promoted.WinningResultID != result.ID || !sameJSON(promoted.TagEncoding, result.TagEncoding) || len(promoted.TagEncoding) == 0 {
		t.Fatalf("promoted metadata = %+v; want digestless identity and payload %+v", promoted, result)
	}
	if err := db.NewRaw(`SELECT success_metadata_result_id FROM source_analysis_step WHERE work_id=? AND step='metadata'`, work.ID).Scan(ctx, &selectedID); err != nil || selectedID != result.ID {
		t.Fatalf("promoted selection = %s, %v; want %s", selectedID, err, result.ID)
	}

	groups := service.NewIncomingGroups(persistence.NewIncomingGroupingRepository(db))
	snapshot, err := groups.Snapshot(ctx)
	if err != nil || len(snapshot.Groups) != 1 {
		t.Fatalf("expected one visible group: %+v, %v", snapshot, err)
	}
	group := snapshot.Groups[0]
	corrections := persistence.NewIncomingTagEncodingRepository(db)
	preview, err := corrections.Preview(ctx, group.ID)
	if err != nil || len(preview.Members) != 1 || !preview.Members[0].Stored ||
		len(preview.Members[0].Report.Automatic.Entries) != 1 {
		t.Fatalf("expected saved automatic repair: %+v, %v", preview, err)
	}
	undo := persistence.IncomingTagEncodingConfirmation{
		Generation: preview.Generation, GroupRevision: preview.GroupRevision,
		Digest: preview.Digest, CorrectionRevision: preview.CorrectionRevision,
		Members: []persistence.IncomingTagEncodingCorrection{{
			VariantID: preview.Members[0].VariantID, MetadataResultID: selectedID, Undo: true,
		}},
	}
	updated, err := corrections.Confirm(ctx, group.ID, undo)
	if err != nil || len(updated.Members) != 1 || !updated.Members[0].Stored ||
		len(updated.Members[0].Report.Automatic.Entries) != 0 ||
		updated.CorrectionRevision == preview.CorrectionRevision {
		t.Fatalf("expected saved undo without a repair: %+v, %v", updated, err)
	}
	if _, err := corrections.Confirm(ctx, group.ID, undo); !errors.Is(err, persistence.ErrIncomingTagEncodingConflict) {
		t.Fatalf("stale undo must be rejected: %v", err)
	}
	var raw string
	if err := db.NewRaw(`SELECT observed_tags::text FROM media_metadata_result WHERE id=?`, selectedID).Scan(ctx, &raw); err != nil ||
		!sameJSON(json.RawMessage(raw), result.ObservedTags) {
		t.Fatalf("correction rewrote raw tags: %q, %v", raw, err)
	}
	unchanged, err := groups.Snapshot(ctx)
	if err != nil || len(unchanged.Groups) != 1 || unchanged.Groups[0].Revision != group.Revision {
		t.Fatalf("encoding repair changed grouping: %+v, %v", unchanged, err)
	}
}
