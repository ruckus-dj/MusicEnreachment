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
	"github.com/uptrace/bun"
)

func runApplySourceEnumerationPreservesUnchangedWorkAndProtectsUnreadableScope(t *testing.T, database *bun.DB) {
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
	retainedVariant := insertAssociationCleanupVariant(t, ctx, database, true)
	if _, err := database.ExecContext(ctx, `UPDATE source_location SET media_variant_id=? WHERE id=?`, retainedVariant, outside.ID); err != nil {
		t.Fatal(err)
	}
	trackID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'Incomplete enumeration association')`, trackID); err != nil {
		t.Fatal(err)
	}
	association := &persistence.TrackSource{ID: uuid.New(), TrackID: trackID, MediaVariantID: retainedVariant,
		Selected: true, Compatible: true, DecisionMethod: "manual", Metadata: json.RawMessage(`{}`)}
	if _, err := database.NewInsert().Model(association).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var originalWorkID string
	if err := database.NewRaw(`SELECT id FROM source_analysis_work WHERE location_id=?`, outside.ID).Scan(ctx, &originalWorkID); err != nil {
		t.Fatalf("read initial work: %v", err)
	}
	var initialSteps int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE work_id=? AND state='pending'`, originalWorkID).Scan(ctx, &initialSteps); err != nil {
		t.Fatalf("count initial pending steps: %v", err)
	}
	if initialSteps != 4 {
		t.Fatalf("initial pending steps = %d, want one per analysis step including metadata", initialSteps)
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
	if _, err := readLocationResult(ctx, database, root.ID, "album/removed.flac"); err != nil {
		t.Fatalf("incomplete enumeration removed location from unreadable subtree: %v", err)
	}
	if _, err := readLocationResult(ctx, database, root.ID, "gone.flac"); err != nil {
		t.Fatalf("incomplete enumeration removed an observed missing location: %v", err)
	}
	rootAfterPartial, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read root after incomplete enumeration: %v", err)
	}
	if rootAfterPartial.ScanGeneration != 1 || rootAfterPartial.LastAppliedOperationID == nil || *rootAfterPartial.LastAppliedOperationID != first.ID {
		t.Fatalf("incomplete enumeration changed published inventory identity: %+v", rootAfterPartial)
	}
	assertTrackSourceCount(t, ctx, database, retainedVariant, 1)
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
	if rootAfterRepeat.ScanGeneration != rootAfterFailure.ScanGeneration || rootAfterRepeat.LastAppliedOperationID == nil || *rootAfterRepeat.LastAppliedOperationID != first.ID {
		t.Fatalf("repeated root-scoped apply was not idempotent: before=%+v after=%+v", rootAfterFailure, rootAfterRepeat)
	}
	if _, err := readLocationResult(ctx, database, root.ID, "outside.flac"); err != nil {
		t.Fatalf("root-level failure removed the published location: %v", err)
	}
	assertTrackSourceCount(t, ctx, database, retainedVariant, 1)
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

func runApplySourceEnumerationReplacesCompletedInPlaceWork(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/enumeration-in-place-execution")
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
		t.Fatalf("read original work: %v", err)
	}
	completedAnalysisID := uuid.New()
	if _, err := database.NewRaw(`INSERT INTO operation
		(id, kind, state, stage, input_snapshot, attempt, river_job_id, created_at, updated_at, finished_at)
		VALUES (?, 'analyze_source', 'succeeded', 'complete', '{}', 1, 902, now(), now(), now())`, completedAnalysisID).Exec(ctx); err != nil {
		t.Fatalf("record completed source-analysis operation: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO source_analysis_work_execution
		(work_id, operation_id, operation_attempt, job_id, processing_mode)
		VALUES (?, ?, 1, 902, 'in_place')`, workID, completedAnalysisID).Exec(ctx); err != nil {
		t.Fatalf("record completed in-place execution: %v", err)
	}
	setOperationState(t, ctx, database, first.ID, "succeeded")

	second := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, second.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("track.flac", 6, mtime.Add(time.Second)),
	}); err != nil {
		t.Fatalf("store changed enumeration: %v", err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(second, root, nil)); err != nil {
		t.Fatalf("apply changed enumeration after completed execution: %v", err)
	}
	changed := readLocation(t, ctx, database, root.ID, "track.flac")
	if changed.ID != location.ID {
		t.Fatalf("changed file location identity = %s, want in-place location %s", changed.ID, location.ID)
	}
	if changed.SizeBytes != 6 || !changed.Mtime.Equal(mtime.Add(time.Second)) || changed.ProbeStatus != persistence.SourceProbeStatusNotAnalyzed {
		t.Fatalf("changed file was not successfully enumerated: %+v", changed)
	}
	var remaining int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE id=?`, workID).Scan(ctx, &remaining); err != nil {
		t.Fatalf("count retired work: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("old completed work rows = %d, want 0", remaining)
	}
}

func TestApplySourceEnumerationCleansAssociationsOnlyForVariantsWithoutLocationsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := testpostgres.OpenMigratedWithRiver(t)
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/enumeration-association-cleanup")
	mtime := probeMtime()
	first := newSourceScanOperation(t, ctx, database, root, "running")
	paths := []string{"sha-missing.flac", "digestless-missing.flac", "shared-missing.flac", "shared-survivor.flac"}
	candidates := make([]persistence.SourceScanCandidateInput, 0, len(paths))
	for _, path := range paths {
		candidates = append(candidates, enumerationCandidate(path, 5, mtime))
	}
	if err := inventory.ReplaceSourceScanCandidates(ctx, first.ID, candidates); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(first, root, nil)); err != nil {
		t.Fatal(err)
	}
	shaVariant := insertAssociationCleanupVariant(t, ctx, database, true)
	digestlessVariant := insertAssociationCleanupVariant(t, ctx, database, false)
	sharedVariant := insertAssociationCleanupVariant(t, ctx, database, true)
	variants := map[string]uuid.UUID{
		paths[0]: shaVariant, paths[1]: digestlessVariant,
		paths[2]: sharedVariant, paths[3]: sharedVariant,
	}
	for path, variantID := range variants {
		location := readLocation(t, ctx, database, root.ID, path)
		if _, err := database.ExecContext(ctx, `UPDATE source_location SET media_variant_id=? WHERE id=?`, variantID, location.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, variantID := range []uuid.UUID{shaVariant, digestlessVariant, sharedVariant} {
		trackID := uuid.New()
		if _, err := database.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'Enumeration association')`, trackID); err != nil {
			t.Fatal(err)
		}
		association := &persistence.TrackSource{ID: uuid.New(), TrackID: trackID, MediaVariantID: variantID,
			Selected: true, Compatible: true, DecisionMethod: "manual", Metadata: json.RawMessage(`{}`)}
		if _, err := database.NewInsert().Model(association).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	setOperationState(t, ctx, database, first.ID, "succeeded")

	second := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, second.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate(paths[3], 5, mtime),
	}); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(second, root, nil)); err != nil {
		t.Fatal(err)
	}
	assertTrackSourceCount(t, ctx, database, shaVariant, 0)
	assertTrackSourceCount(t, ctx, database, digestlessVariant, 0)
	assertTrackSourceCount(t, ctx, database, sharedVariant, 1)
	if _, err := readLocationResult(ctx, database, root.ID, paths[0]); err == nil {
		t.Fatal("genuinely missing location was retained")
	}
	if _, err := readLocationResult(ctx, database, root.ID, paths[1]); err == nil {
		t.Fatal("genuinely missing digestless location was retained")
	}
}

func TestApplySourceEnumerationChangedFileDoesNotInheritVariantOrCleanItsAssociationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := testpostgres.OpenMigratedWithRiver(t)
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/enumeration-changed-association")
	mtime := probeMtime()
	first := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, first.ID, []persistence.SourceScanCandidateInput{enumerationCandidate("changed.flac", 5, mtime)}); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(first, root, nil)); err != nil {
		t.Fatal(err)
	}
	location := readLocation(t, ctx, database, root.ID, "changed.flac")
	variantID := insertAssociationCleanupVariant(t, ctx, database, true)
	if _, err := database.ExecContext(ctx, `UPDATE source_location SET media_variant_id=? WHERE id=?`, variantID, location.ID); err != nil {
		t.Fatal(err)
	}
	trackID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'Changed source association')`, trackID); err != nil {
		t.Fatal(err)
	}
	association := &persistence.TrackSource{ID: uuid.New(), TrackID: trackID, MediaVariantID: variantID,
		Selected: true, Compatible: true, DecisionMethod: "manual", Metadata: json.RawMessage(`{}`)}
	if _, err := database.NewInsert().Model(association).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	setOperationState(t, ctx, database, first.ID, "succeeded")

	second := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, second.ID, []persistence.SourceScanCandidateInput{enumerationCandidate("changed.flac", 6, mtime.Add(time.Second))}); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(second, root, nil)); err != nil {
		t.Fatal(err)
	}
	changed := readLocation(t, ctx, database, root.ID, "changed.flac")
	if changed.ID != location.ID || changed.MediaVariantID != nil || changed.ProbeStatus != persistence.SourceProbeStatusNotAnalyzed {
		t.Fatalf("changed file retained old identity or analysis: %+v", changed)
	}
	assertTrackSourceCount(t, ctx, database, variantID, 1)
}

func runDeleteSourceScanCandidatesForRootDeliveryRejectsMismatchedRootWithoutMutation(t *testing.T, database *bun.DB) {
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

func runApplySourceEnumerationRetiresWorkWithOwnedArtifactAsTombstone(t *testing.T, database *bun.DB) {
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
	var workID uuid.UUID
	if err := database.NewRaw(`SELECT id FROM source_analysis_work WHERE current_location_id=?`, location.ID).Scan(ctx, &workID); err != nil {
		t.Fatalf("read work identity: %v", err)
	}
	insertStagedAnalysisArtifact(t, ctx, database, workID, first.ID, first.Attempt, *first.RiverJobID, "enumeration/owned-copy", 5, mtime)
	setOperationState(t, ctx, database, first.ID, "succeeded")

	second := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, second.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("track.flac", 6, mtime.Add(time.Second)),
	}); err != nil {
		t.Fatalf("store changed enumeration: %v", err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(second, root, nil)); err != nil {
		t.Fatalf("apply changed enumeration with owned artifact: %v", err)
	}
	var tombstoned int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE id=? AND current_location_id IS NULL`, workID).Scan(ctx, &tombstoned); err != nil {
		t.Fatalf("read retired work: %v", err)
	}
	if tombstoned != 1 {
		t.Fatalf("retired work tombstone count = %d, want 1", tombstoned)
	}
	var retainedArtifact int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_artifact
		WHERE work_id=? AND relative_output_path='enumeration/owned-copy'
		AND owner_operation_id=? AND owner_operation_attempt=? AND owner_job_id=?`,
		workID, first.ID, first.Attempt, *first.RiverJobID).Scan(ctx, &retainedArtifact); err != nil {
		t.Fatalf("read retained artifact: %v", err)
	}
	if retainedArtifact != 1 {
		t.Fatalf("retained artifact count = %d, want 1", retainedArtifact)
	}
	var retainedExecution int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work_execution
		WHERE work_id=? AND operation_id=? AND operation_attempt=? AND job_id=? AND processing_mode='staged'`,
		workID, first.ID, first.Attempt, *first.RiverJobID).Scan(ctx, &retainedExecution); err != nil {
		t.Fatalf("read retained execution: %v", err)
	}
	if retainedExecution != 1 {
		t.Fatalf("retained execution count = %d, want 1", retainedExecution)
	}
	current := readLocation(t, ctx, database, root.ID, "track.flac")
	var currentWorkID uuid.UUID
	if err := database.NewRaw(`SELECT id FROM source_analysis_work WHERE current_location_id=?`, current.ID).Scan(ctx, &currentWorkID); err != nil {
		t.Fatalf("read current work: %v", err)
	}
	if currentWorkID == workID {
		t.Fatalf("changed location reused retired work %s", currentWorkID)
	}
}

func runApplySourceEnumerationRejectsStaleDeliveryWithoutMutation(t *testing.T, database *bun.DB) {
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

func runApplySourceEnumerationInvalidatesExactCleanupMarkerPostgreSQL(t *testing.T, database *bun.DB) {
	ctx, inventory, root, mtime := newEnumerationCleanupMarkerFixture(t, database, "cleanup-invalidates")
	accepted := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, accepted.ID, []persistence.SourceScanCandidateInput{enumerationCandidate("album/track.flac", 5, mtime)}); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(accepted, root, nil)); err != nil {
		t.Fatalf("apply accepted unchanged-stat enumeration: %v", err)
	}
	var remaining int
	if err := database.NewRaw(`SELECT count(*) FROM source_cleanup_completion WHERE source_root_id=?`, root.ID).Scan(ctx, &remaining); err != nil || remaining != 0 {
		t.Fatalf("cleanup markers after accepted enumeration=%d err=%v; want invalidation even at same stats", remaining, err)
	}
}

func runApplySourceEnumerationStaleDeliveryPreservesCleanupMarkerPostgreSQL(t *testing.T, database *bun.DB) {
	ctx, inventory, root, mtime := newEnumerationCleanupMarkerFixture(t, database, "cleanup-stale-delivery")
	operation := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, operation.ID, []persistence.SourceScanCandidateInput{enumerationCandidate("album/track.flac", 5, mtime)}); err != nil {
		t.Fatal(err)
	}
	stale := enumerationApply(operation, root, nil)
	stale.ExpectedJobID++
	if err := inventory.ApplySourceEnumeration(ctx, stale); err == nil {
		t.Fatal("stale enumeration delivery was accepted")
	}
	assertEnumerationCleanupMarker(t, ctx, database, root.ID, true)
}

func runApplySourceEnumerationUnreadableScopePreservesScopedCleanupMarkerPostgreSQL(t *testing.T, database *bun.DB) {
	ctx, inventory, root, mtime := newEnumerationCleanupMarkerFixture(t, database, "cleanup-unreadable-scope")
	oldLocation := readLocation(t, ctx, database, root.ID, "album/track.flac")
	operation := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, operation.ID, []persistence.SourceScanCandidateInput{enumerationCandidate("album/track.flac", 5, mtime)}); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(operation, root, []persistence.SourceEnumerationScope{{Kind: "subtree", RelativePath: "album"}})); err != nil {
		t.Fatalf("apply enumeration with unreadable scope: %v", err)
	}
	assertEnumerationCleanupMarker(t, ctx, database, root.ID, true)
	if retained := readLocation(t, ctx, database, root.ID, "album/track.flac"); retained.ID != oldLocation.ID {
		t.Fatalf("incomplete enumeration replaced the published location: before=%s after=%s", oldLocation.ID, retained.ID)
	}
	rootAfterIncomplete, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rootAfterIncomplete.ScanGeneration != 1 {
		t.Fatalf("incomplete enumeration advanced generation to %d, want 1", rootAfterIncomplete.ScanGeneration)
	}
	setOperationState(t, ctx, database, operation.ID, "succeeded")

	accepted := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, accepted.ID, []persistence.SourceScanCandidateInput{enumerationCandidate("album/track.flac", 5, mtime)}); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(accepted, root, nil)); err != nil {
		t.Fatalf("apply accepted re-observation: %v", err)
	}
	newLocation := readLocation(t, ctx, database, root.ID, "album/track.flac")
	if newLocation.ID != oldLocation.ID {
		t.Fatalf("complete re-observation replaced retained location identity %s with %s", oldLocation.ID, newLocation.ID)
	}
	assertEnumerationCleanupMarker(t, ctx, database, root.ID, false)
}

func runApplySourceEnumerationDeferredCommitFailurePreservesCleanupMarkerPostgreSQL(t *testing.T, database *bun.DB) {
	ctx, inventory, root, mtime := newEnumerationCleanupMarkerFixture(t, database, "cleanup-deferred-commit")
	if _, err := database.ExecContext(ctx, `CREATE FUNCTION reject_cleanup_marker_delete_commit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected cleanup marker delete commit failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	defer database.ExecContext(ctx, `DROP FUNCTION reject_cleanup_marker_delete_commit() CASCADE`)
	if _, err := database.ExecContext(ctx, `CREATE CONSTRAINT TRIGGER reject_cleanup_marker_delete_commit
		AFTER DELETE ON source_cleanup_completion DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW EXECUTE FUNCTION reject_cleanup_marker_delete_commit()`); err != nil {
		t.Fatal(err)
	}
	operation := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, operation.ID, []persistence.SourceScanCandidateInput{enumerationCandidate("album/track.flac", 5, mtime)}); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(operation, root, nil)); err == nil {
		t.Fatal("enumeration with deferred marker-delete failure committed")
	}
	assertEnumerationCleanupMarker(t, ctx, database, root.ID, true)
}

func newEnumerationCleanupMarkerFixture(t *testing.T, database *bun.DB, suffix string) (context.Context, *persistence.SourceInventoryRepository, *persistence.SourceRoot, time.Time) {
	t.Helper()
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/enumeration-"+suffix)
	mtime := probeMtime()
	seed := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, seed.ID, []persistence.SourceScanCandidateInput{enumerationCandidate("album/track.flac", 5, mtime)}); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(seed, root, nil)); err != nil {
		t.Fatalf("apply baseline enumeration: %v", err)
	}
	setOperationState(t, ctx, database, seed.ID, "succeeded")
	var workID, locationID uuid.UUID
	if err := database.NewRaw(`SELECT w.id,l.id FROM source_analysis_work w JOIN source_location l ON l.id=w.current_location_id WHERE w.source_root_id=? AND w.relative_path='album/track.flac'`, root.ID).Scan(ctx, &workID, &locationID); err != nil {
		t.Fatalf("read baseline work/location: %v", err)
	}
	variantID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes) VALUES (?,5)`, variantID); err != nil {
		t.Fatalf("seed valid media variant: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_location SET media_variant_id=? WHERE id=?`, variantID, locationID); err != nil {
		t.Fatalf("bind variant to location: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, workID, variantID); err != nil {
		t.Fatalf("bind variant to work: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_cleanup_completion
		(work_id,source_root_id,source_location_id,media_variant_id,configured_path,inventory_path,relative_path,size_bytes,mtime,kind)
		SELECT w.id,w.source_root_id,l.id,l.media_variant_id,r.configured_path,r.inventory_path,w.relative_path,w.size_bytes,w.mtime,'native_removed'
		FROM source_analysis_work w JOIN source_location l ON l.id=w.current_location_id
		JOIN source_root r ON r.id=w.source_root_id WHERE w.source_root_id=?`, root.ID); err != nil {
		t.Fatalf("seed exact cleanup marker: %v", err)
	}
	assertEnumerationCleanupMarker(t, ctx, database, root.ID, true)
	return ctx, inventory, root, mtime
}

func assertEnumerationCleanupMarker(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID, want bool) {
	t.Helper()
	var count int
	if err := database.NewRaw(`SELECT count(*) FROM source_cleanup_completion WHERE source_root_id=?`, rootID).Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	wantCount := 0
	if want {
		wantCount = 1
	}
	if count != wantCount {
		t.Fatalf("cleanup completion markers=%d; want %d", count, wantCount)
	}
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
