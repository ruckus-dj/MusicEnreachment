//go:build integration

package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestEnumerationRetainsArtifactWorkAsIrreversibleTombstone(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/retained-work-change")
	mtime := probeMtime()
	first := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, first.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("track.flac", 5, mtime),
	}); err != nil {
		t.Fatalf("store initial enumeration: %v", err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(first, root, nil)); err != nil {
		t.Fatalf("apply initial enumeration: %v", err)
	}
	location := readLocation(t, ctx, database, root.ID, "track.flac")
	var workID uuid.UUID
	if err := database.NewRaw(`SELECT id FROM source_analysis_work WHERE current_location_id=?`, location.ID).Scan(ctx, &workID); err != nil {
		t.Fatalf("read original work: %v", err)
	}
	insertStagedAnalysisArtifact(t, ctx, database, workID, first.ID, first.Attempt, *first.RiverJobID, "retirement/owned-copy", 5, mtime)
	setOperationState(t, ctx, database, first.ID, "succeeded")

	second := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, second.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("track.flac", 6, mtime.Add(time.Second)),
	}); err != nil {
		t.Fatalf("store changed enumeration: %v", err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(second, root, nil)); err != nil {
		t.Fatalf("apply changed enumeration: %v", err)
	}
	var attached, artifacts int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE id=? AND current_location_id IS NULL`, workID).Scan(ctx, &attached); err != nil {
		t.Fatalf("read retired work: %v", err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE work_id=?`, workID).Scan(ctx, &artifacts); err != nil {
		t.Fatalf("read retained artifact: %v", err)
	}
	if attached != 1 || artifacts != 1 {
		t.Fatalf("retired work/artifact counts = %d/%d, want 1/1", attached, artifacts)
	}
	if _, err := database.NewRaw(`UPDATE source_analysis_work SET current_location_id=location_id WHERE id=?`, workID).Exec(ctx); err == nil {
		t.Fatal("retired work was reattached")
	}
	current := readLocation(t, ctx, database, root.ID, "track.flac")
	var replacementCount int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE current_location_id=?`, current.ID).Scan(ctx, &replacementCount); err != nil {
		t.Fatalf("read replacement work: %v", err)
	}
	if replacementCount != 1 || current.SizeBytes != 6 {
		t.Fatalf("replacement work count/size = %d/%d, want 1/6", replacementCount, current.SizeBytes)
	}
}

func TestDeletingRootRetainsArtifactWorkAndMissingArtifactFreeWorkIsDeleted(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/retained-work-delete")
	mtime := probeMtime()
	first := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, first.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("retained.flac", 5, mtime), enumerationCandidate("removed.flac", 7, mtime),
		enumerationCandidate("unreadable.flac", 9, mtime),
	}); err != nil {
		t.Fatalf("store initial enumeration: %v", err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(first, root, nil)); err != nil {
		t.Fatalf("apply initial enumeration: %v", err)
	}
	retainedLocation := readLocation(t, ctx, database, root.ID, "retained.flac")
	removedLocation := readLocation(t, ctx, database, root.ID, "removed.flac")
	var retainedID, removedID, unreadableID uuid.UUID
	if err := database.NewRaw(`SELECT id FROM source_analysis_work WHERE current_location_id=?`, retainedLocation.ID).Scan(ctx, &retainedID); err != nil {
		t.Fatalf("read artifact work: %v", err)
	}
	if err := database.NewRaw(`SELECT id FROM source_analysis_work WHERE current_location_id=?`, removedLocation.ID).Scan(ctx, &removedID); err != nil {
		t.Fatalf("read artifact-free work: %v", err)
	}
	unreadableLocation := readLocation(t, ctx, database, root.ID, "unreadable.flac")
	if err := database.NewRaw(`SELECT id FROM source_analysis_work WHERE current_location_id=?`, unreadableLocation.ID).Scan(ctx, &unreadableID); err != nil {
		t.Fatalf("read unreadable work: %v", err)
	}
	insertStagedAnalysisArtifact(t, ctx, database, retainedID, first.ID, first.Attempt, *first.RiverJobID, "retirement/root-copy", 5, mtime)
	insertStagedAnalysisArtifact(t, ctx, database, unreadableID, first.ID, first.Attempt, *first.RiverJobID, "retirement/unreadable-copy", 9, mtime)
	setOperationState(t, ctx, database, first.ID, "succeeded")
	second := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, second.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("retained.flac", 5, mtime),
	}); err != nil {
		t.Fatalf("store missing-file enumeration: %v", err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(second, root, []persistence.SourceEnumerationScope{{Kind: "file", RelativePath: "unreadable.flac"}})); err != nil {
		t.Fatalf("apply missing-file enumeration: %v", err)
	}
	var removedCount int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE id=?`, removedID).Scan(ctx, &removedCount); err != nil {
		t.Fatalf("read artifact-free work result: %v", err)
	}
	if removedCount != 0 {
		t.Fatalf("artifact-free missing work remains %d times", removedCount)
	}
	var unreadableRetained int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE id=? AND current_location_id IS NULL`, unreadableID).Scan(ctx, &unreadableRetained); err != nil {
		t.Fatalf("read unreadable-scope tombstone: %v", err)
	}
	if unreadableRetained != 1 {
		t.Fatalf("unreadable-scope work tombstone count = %d, want 1", unreadableRetained)
	}
	setOperationState(t, ctx, database, second.ID, "succeeded")
	if err := inventory.DeleteSourceRoot(ctx, root.ID, root.ConfiguredPath, 1); err != nil {
		t.Fatalf("delete source root: %v", err)
	}
	var retainedCount, artifactCount int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE id=? AND current_location_id IS NULL`, retainedID).Scan(ctx, &retainedCount); err != nil {
		t.Fatalf("read root-deleted work: %v", err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE work_id=?`, retainedID).Scan(ctx, &artifactCount); err != nil {
		t.Fatalf("read root-deleted artifact: %v", err)
	}
	if retainedCount != 1 || artifactCount != 1 {
		t.Fatalf("root-deleted work/artifact counts = %d/%d, want 1/1", retainedCount, artifactCount)
	}
}

// insertStagedAnalysisArtifact stages the immutable execution identity that an
// artifact must reference, then inserts the owned artifact row. Artifacts can
// only originate from a staged execution, so every artifact fixture goes
// through this shared helper.
func insertStagedAnalysisArtifact(t *testing.T, ctx context.Context, database *bun.DB, workID, ownerOperationID uuid.UUID, ownerAttempt int, ownerJobID int64, relativePath string, sizeBytes int64, mtime time.Time) {
	t.Helper()
	if _, err := database.NewRaw(`INSERT INTO source_analysis_work_execution
		(work_id,operation_id,operation_attempt,job_id,processing_mode)
		VALUES (?,?,?,?,'staged')`, workID, ownerOperationID, ownerAttempt, ownerJobID).Exec(ctx); err != nil {
		t.Fatalf("stage source analysis execution for %q: %v", relativePath, err)
	}
	if _, err := database.NewRaw(`INSERT INTO source_analysis_artifact
		(id,work_id,relative_output_path,source_size_bytes,source_mtime,owner_operation_id,owner_operation_attempt,owner_job_id,state)
		VALUES (gen_random_uuid(),?,?,?,?,?,?,?,'acquiring')`, workID, relativePath, sizeBytes, mtime, ownerOperationID, ownerAttempt, ownerJobID).Exec(ctx); err != nil {
		t.Fatalf("create retained artifact %q: %v", relativePath, err)
	}
}
