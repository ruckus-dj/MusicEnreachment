//go:build integration

package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestApplySourceEnumerationPreservesUnchangedWorkAndProtectsUnreadableScope(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/enumeration")
	mtime := probeMtime()
	first := newSourceScanOperation(t, ctx, database, root, "running")
	firstCandidates := []persistence.SourceScanCandidateInput{
		enumerationCandidate("album/removed.flac", 5, mtime),
		enumerationCandidate("gone.flac", 6, mtime),
		enumerationCandidate("outside.flac", 7, mtime),
	}
	if err := inventory.ReplaceSourceScanCandidates(ctx, first.ID, firstCandidates); err != nil {
		t.Fatalf("store initial enumeration: %v", err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(first, root, nil)); err != nil {
		t.Fatalf("apply initial enumeration: %v", err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(first, root, nil)); err != nil {
		t.Fatalf("repeat applied enumeration: %v", err)
	}
	outside := readLocation(t, ctx, database, root.ID, "outside.flac")
	var originalWorkID string
	if err := database.NewRaw(`SELECT id FROM source_analysis_work WHERE location_id=?`, outside.ID).Scan(ctx, &originalWorkID); err != nil {
		t.Fatalf("read initial work: %v", err)
	}
	var initialSteps int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE work_id=? AND state='pending'`, originalWorkID).Scan(ctx, &initialSteps); err != nil {
		t.Fatalf("count initial pending steps: %v", err)
	}
	if initialSteps != 3 {
		t.Fatalf("initial pending steps = %d, want one per analysis step", initialSteps)
	}
	if _, err := database.NewRaw(`UPDATE source_location SET probe_status='probe_error',safe_error='earlier failure' WHERE id=?`, outside.ID).Exec(ctx); err != nil {
		t.Fatalf("set prior analysis failure: %v", err)
	}
	setOperationState(t, ctx, database, first.ID, "succeeded")

	second := newSourceScanOperation(t, ctx, database, root, "running")
	secondCandidates := []persistence.SourceScanCandidateInput{
		enumerationCandidate("album/removed.flac", 5, mtime),
		enumerationCandidate("outside.flac", 7, mtime),
	}
	if err := inventory.ReplaceSourceScanCandidates(ctx, second.ID, secondCandidates); err != nil {
		t.Fatalf("store second enumeration: %v", err)
	}
	scopes := []persistence.SourceEnumerationScope{{Kind: "subtree", RelativePath: "album"}}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(second, root, scopes)); err != nil {
		t.Fatalf("apply second enumeration: %v", err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(second, root, scopes)); err != nil {
		t.Fatalf("repeat second enumeration apply: %v", err)
	}
	var partiallyApplied persistence.Operation
	if err := database.NewSelect().Model(&partiallyApplied).Where("id = ?", second.ID).Scan(ctx); err != nil {
		t.Fatalf("read operation outcome after unreadable-scope apply: %v", err)
	}
	if partiallyApplied.Stage != "traversing" || partiallyApplied.SafeError == nil || *partiallyApplied.SafeError == "" {
		t.Fatalf("unreadable-scope outcome was not persisted with the reconciliation: %+v", partiallyApplied)
	}
	unchanged := readLocation(t, ctx, database, root.ID, "outside.flac")
	if unchanged.ProbeStatus != "probe_error" || unchanged.SafeError == nil || *unchanged.SafeError != "earlier failure" {
		t.Fatalf("unchanged location lost prior failure state: %+v", unchanged)
	}
	var unchangedWorkID string
	if err := database.NewRaw(`SELECT id FROM source_analysis_work WHERE location_id=?`, unchanged.ID).Scan(ctx, &unchangedWorkID); err != nil {
		t.Fatalf("read unchanged work: %v", err)
	}
	if unchangedWorkID != originalWorkID {
		t.Fatalf("unchanged work identity = %s, want %s", unchangedWorkID, originalWorkID)
	}
	if _, err := readLocationResult(ctx, database, root.ID, "album/removed.flac"); err == nil {
		t.Fatal("location from an unreadable subtree was retained")
	}
	if _, err := readLocationResult(ctx, database, root.ID, "gone.flac"); err == nil {
		t.Fatal("missing location outside inaccessible scope was retained")
	}
	setOperationState(t, ctx, database, second.ID, "succeeded")
	rootBeforeFailure, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read root before unavailable observation: %v", err)
	}

	rootScoped := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, rootScoped.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("outside.flac", 8, mtime),
	}); err != nil {
		t.Fatalf("store empty root-scoped enumeration: %v", err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(rootScoped, root, []persistence.SourceEnumerationScope{{Kind: "root"}})); err != nil {
		t.Fatalf("apply root-scoped enumeration: %v", err)
	}
	rootAfterFailure, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read root after unavailable observation: %v", err)
	}
	if rootAfterFailure.Status != persistence.SourceRootStatusUnavailable || rootAfterFailure.SafeError == nil || *rootAfterFailure.SafeError != persistence.SourceEnumerationRootUnavailableReason {
		t.Fatalf("root failure status was not recorded safely: %+v", rootAfterFailure)
	}
	if rootAfterFailure.LastSuccessfulScanAt == nil || rootBeforeFailure.LastSuccessfulScanAt == nil || !rootAfterFailure.LastSuccessfulScanAt.Equal(*rootBeforeFailure.LastSuccessfulScanAt) {
		t.Fatalf("root failure changed last successful scan time: %+v", rootAfterFailure.LastSuccessfulScanAt)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(rootScoped, root, []persistence.SourceEnumerationScope{{Kind: "root"}})); err != nil {
		t.Fatalf("repeat root-scoped apply: %v", err)
	}
	rootAfterRepeat, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read root after repeated unavailable observation: %v", err)
	}
	if rootAfterRepeat.ScanGeneration != rootAfterFailure.ScanGeneration || rootAfterRepeat.LastAppliedOperationID == nil || *rootAfterRepeat.LastAppliedOperationID != rootScoped.ID {
		t.Fatalf("repeated root-scoped apply was not idempotent: before=%+v after=%+v", rootAfterFailure, rootAfterRepeat)
	}
	if _, err := readLocationResult(ctx, database, root.ID, "outside.flac"); err == nil {
		t.Fatal("root-level failure retained locations outside an unreadable scope")
	}
	setOperationState(t, ctx, database, rootScoped.ID, "succeeded")

	third := newSourceScanOperation(t, ctx, database, root, "running")
	changedMtime := mtime.Add(time.Second)
	if err := inventory.ReplaceSourceScanCandidates(ctx, third.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("outside.flac", 8, changedMtime),
	}); err != nil {
		t.Fatalf("store changed enumeration: %v", err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(third, root, nil)); err != nil {
		t.Fatalf("apply changed enumeration: %v", err)
	}
	changed := readLocation(t, ctx, database, root.ID, "outside.flac")
	if changed.ID == outside.ID {
		t.Fatalf("location recreated after root-scope prune reused identity %s", changed.ID)
	}
	if changed.ProbeStatus != persistence.SourceProbeStatusNotAnalyzed || changed.SafeError != nil || changed.MediaVariantID != nil {
		t.Fatalf("changed location retained stale analysis: %+v", changed)
	}
	var changedWorkID string
	if err := database.NewRaw(`SELECT id FROM source_analysis_work WHERE location_id=?`, changed.ID).Scan(ctx, &changedWorkID); err != nil {
		t.Fatalf("read replacement work: %v", err)
	}
	if changedWorkID == originalWorkID {
		t.Fatalf("changed file reused immutable work %s", changedWorkID)
	}
}

func TestDeleteSourceScanCandidatesForRootDeliveryRejectsMismatchedRootWithoutMutation(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	rootA := createInventoryRoot(t, ctx, inventory, "/srv/enumeration-root-a")
	rootB := createInventoryRoot(t, ctx, inventory, "/srv/enumeration-root-b")
	operation := newSourceScanOperation(t, ctx, database, rootA, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, operation.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("keep.flac", 1, probeMtime()),
	}); err != nil {
		t.Fatalf("store candidate: %v", err)
	}
	if err := inventory.DeleteSourceScanCandidatesForRootDelivery(ctx, operation.ID, rootB.ID, rootB.ConfiguredPath, operation.Attempt, *operation.RiverJobID); err == nil {
		t.Fatal("root mismatch was accepted")
	}
	var candidateCount int
	if err := database.NewRaw(`SELECT count(*) FROM source_scan_candidate WHERE operation_id=?`, operation.ID).Scan(ctx, &candidateCount); err != nil {
		t.Fatalf("count retained candidates: %v", err)
	}
	if candidateCount != 1 {
		t.Fatalf("candidate count after rejected mismatch = %d, want 1", candidateCount)
	}
}

func TestApplySourceEnumerationDoesNotRetireWorkWithOwnedArtifact(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/enumeration-artifact")
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
	var workID string
	if err := database.NewRaw(`SELECT id FROM source_analysis_work WHERE location_id=?`, location.ID).Scan(ctx, &workID); err != nil {
		t.Fatalf("read work identity: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO source_analysis_artifact
		(id,work_id,relative_output_path,source_size_bytes,source_mtime,owner_operation_id,owner_operation_attempt,owner_job_id,state)
		VALUES (gen_random_uuid(),?,'enumeration/owned-copy',?,?,?,?,?,'acquiring')`, workID, 5, mtime, first.ID, first.Attempt, *first.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("create owned artifact reference: %v", err)
	}
	setOperationState(t, ctx, database, first.ID, "succeeded")

	second := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, second.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("track.flac", 6, mtime.Add(time.Second)),
	}); err != nil {
		t.Fatalf("store changed enumeration: %v", err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(second, root, nil)); err == nil {
		t.Fatal("enumeration retired work referenced by an owned artifact")
	}
	stored := readLocation(t, ctx, database, root.ID, "track.flac")
	if stored.SizeBytes != 5 || !stored.Mtime.Equal(mtime) {
		t.Fatalf("failed apply partially changed location: %+v", stored)
	}
	var retainedWorkID string
	if err := database.NewRaw(`SELECT id FROM source_analysis_work WHERE location_id=?`, location.ID).Scan(ctx, &retainedWorkID); err != nil {
		t.Fatalf("read retained work: %v", err)
	}
	if retainedWorkID != workID {
		t.Fatalf("retained work identity = %s, want %s", retainedWorkID, workID)
	}
}

func TestApplySourceEnumerationRejectsStaleDeliveryWithoutMutation(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/enumeration-fence")
	operation := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, operation.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("track.flac", 5, probeMtime()),
	}); err != nil {
		t.Fatalf("store candidate: %v", err)
	}
	apply := enumerationApply(operation, root, nil)
	apply.ExpectedJobID++
	if err := inventory.ApplySourceEnumeration(ctx, apply); err == nil {
		t.Fatal("stale delivery was accepted")
	}
	stored, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read unchanged root: %v", err)
	}
	if stored.ScanGeneration != 0 || stored.InventoryPath != nil {
		t.Fatalf("stale delivery mutated root inventory: %+v", stored)
	}
	assertCandidateCount(t, ctx, database, operation.ID, 1)
}

func enumerationCandidate(path string, size int64, mtime time.Time) persistence.SourceScanCandidateInput {
	return persistence.SourceScanCandidateInput{RelativePath: path, SizeBytes: size, Mtime: mtime, ProbeStatus: persistence.SourceProbeStatusNotAnalyzed}
}

func enumerationApply(operation *persistence.Operation, root *persistence.SourceRoot, scopes []persistence.SourceEnumerationScope) persistence.SourceEnumerationApply {
	return persistence.SourceEnumerationApply{
		OperationID: operation.ID, ExpectedConfiguredPath: root.ConfiguredPath, ExpectedAttempt: operation.Attempt,
		ExpectedJobID: *operation.RiverJobID, SHA256Enabled: true, Scopes: scopes,
		FailureSafeError: "The source directory could not be read completely. The previous inventory is unchanged.",
	}
}
