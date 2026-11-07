//go:build integration

package persistence_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	appsettings "github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

func runEnumerationRetainsArtifactWorkAsIrreversibleTombstone(t *testing.T, database *bun.DB) {
	bodyStarted := time.Now()
	defer func() { t.Logf("phase=scenario_body duration=%s", time.Since(bodyStarted)) }()
	baselineSetupStarted := time.Now()
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, t.TempDir())
	mtime := probeMtime()
	first := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, first.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("track.flac", 5, mtime),
	}); err != nil {
		t.Fatalf("store initial enumeration: %v", err)
	}
	t.Logf("phase=enumeration_baseline_setup duration=%s", time.Since(baselineSetupStarted))
	initialApplyStarted := time.Now()
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(first, root, nil)); err != nil {
		t.Fatalf("apply initial enumeration: %v", err)
	}
	t.Logf("phase=initial_apply duration=%s", time.Since(initialApplyStarted))
	artifactSeedStarted := time.Now()
	location := readLocation(t, ctx, database, root.ID, "track.flac")
	var workID uuid.UUID
	if err := database.NewRaw(`SELECT id FROM source_analysis_work WHERE current_location_id=?`, location.ID).Scan(ctx, &workID); err != nil {
		t.Fatalf("read original work: %v", err)
	}
	anchorID := uuid.New()
	if _, err := database.NewRaw(`INSERT INTO media_variant(id,size_bytes) VALUES (?,5)`, anchorID).Exec(ctx); err != nil {
		t.Fatalf("insert digestless work identity anchor: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, workID, anchorID).Exec(ctx); err != nil {
		t.Fatalf("insert work identity mapping: %v", err)
	}
	insertStagedAnalysisArtifact(t, ctx, database, workID, first.ID, first.Attempt, *first.RiverJobID, "retirement/owned-copy", 5, mtime)
	setOperationState(t, ctx, database, first.ID, "succeeded")

	t.Logf("phase=artifact_seed duration=%s", time.Since(artifactSeedStarted))
	changedSetupStarted := time.Now()
	second := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, second.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("track.flac", 6, mtime.Add(time.Second)),
	}); err != nil {
		t.Fatalf("store changed enumeration: %v", err)
	}
	t.Logf("phase=changed_setup duration=%s", time.Since(changedSetupStarted))
	changedApplyStarted := time.Now()
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(second, root, nil)); err != nil {
		t.Fatalf("apply changed enumeration: %v", err)
	}
	t.Logf("phase=changed_apply duration=%s", time.Since(changedApplyStarted))
	assertionsStarted := time.Now()
	var attached, artifacts, mappings int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE id=? AND current_location_id IS NULL`, workID).Scan(ctx, &attached); err != nil {
		t.Fatalf("read retired work: %v", err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE work_id=?`, workID).Scan(ctx, &artifacts); err != nil {
		t.Fatalf("read retained artifact: %v", err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM source_work_variant_identity WHERE work_id=?`, workID).Scan(ctx, &mappings); err != nil {
		t.Fatalf("read retired work identity: %v", err)
	}
	if attached != 1 || artifacts != 1 || mappings != 1 {
		t.Fatalf("invalidated work/artifact/identity counts = %d/%d/%d, want 1/1/1", attached, artifacts, mappings)
	}
	var invalidated int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE id=? AND invalidated_at IS NOT NULL`, workID).Scan(ctx, &invalidated); err != nil {
		t.Fatalf("read invalidated work marker: %v", err)
	}
	if invalidated != 1 {
		t.Fatalf("invalidated work marker rows = %d, want 1", invalidated)
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
	t.Logf("phase=assertions duration=%s", time.Since(assertionsStarted))
}

func runDeletingRootCleansArtifactsAndAnalysisState(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	rootDir := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(rootDir)
	if err != nil {
		t.Fatalf("canonicalize source root: %v", err)
	}
	root := createInventoryRoot(t, ctx, inventory, canonicalRoot)
	workDir := t.TempDir()
	outputDir := t.TempDir()
	workDir, err = filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("canonicalize work directory: %v", err)
	}
	outputDir, err = filepath.EvalSymlinks(outputDir)
	if err != nil {
		t.Fatalf("canonicalize output directory: %v", err)
	}
	settings := persistence.NewSettingsRepository(database)
	if err := settings.Set(ctx, "work_directory", workDir); err != nil {
		t.Fatalf("set work directory: %v", err)
	}
	if err := settings.Set(ctx, "output_directory", outputDir); err != nil {
		t.Fatalf("set output directory: %v", err)
	}
	outputPath := filepath.Join(outputDir, "host-output")
	if err := os.WriteFile(outputPath, []byte("host output"), 0o600); err != nil {
		t.Fatalf("write host output: %v", err)
	}
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
	artifactID := insertStagedAnalysisArtifact(t, ctx, database, retainedID, first.ID, first.Attempt, *first.RiverJobID, "", 5, mtime)
	artifactRelativePath := filepath.Join("analysis", "staging", root.ID.String(), retainedID.String(), artifactID.String())
	artifactPath := filepath.Join(workDir, artifactRelativePath)
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("create staged artifact directory: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("staged analysis copy"), 0o600); err != nil {
		t.Fatalf("write staged analysis copy: %v", err)
	}
	insertStagedAnalysisArtifact(t, ctx, database, unreadableID, first.ID, first.Attempt, *first.RiverJobID, "", 9, mtime)
	setOperationState(t, ctx, database, first.ID, "succeeded")
	second := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, second.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("retained.flac", 5, mtime),
		enumerationCandidate("unreadable.flac", 9, mtime),
	}); err != nil {
		t.Fatalf("store missing-file enumeration: %v", err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(second, root, nil)); err != nil {
		t.Fatalf("apply complete missing-file enumeration: %v", err)
	}
	var removedCount int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE id=?`, removedID).Scan(ctx, &removedCount); err != nil {
		t.Fatalf("read artifact-free work result: %v", err)
	}
	if removedCount != 1 {
		t.Fatalf("invalidated missing-file tombstone rows = %d, want 1", removedCount)
	}
	var invalidatedCount int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE id=? AND invalidated_at IS NOT NULL AND current_location_id IS NULL`, removedID).Scan(ctx, &invalidatedCount); err != nil {
		t.Fatalf("read missing-file invalidated tombstone: %v", err)
	}
	if invalidatedCount != 1 {
		t.Fatalf("missing-file invalidated tombstone rows = %d, want 1", invalidatedCount)
	}
	var unreadableRetained int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE id=? AND current_location_id=?`, unreadableID, unreadableLocation.ID).Scan(ctx, &unreadableRetained); err != nil {
		t.Fatalf("read retained work for a present file: %v", err)
	}
	if unreadableRetained != 1 {
		t.Fatalf("work for a present file = %d, want 1", unreadableRetained)
	}
	setOperationState(t, ctx, database, second.ID, "succeeded")
	var failedWork persistence.SourceAnalysisWork
	if err := database.NewSelect().Model(&failedWork).Where("id=?", retainedID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	failedOperation := normalizedOperation(t, root, retainedLocation, &failedWork, persistence.SourceAnalysisModeBatch, nil, nil, true, false, nil)
	if err := inventory.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, failedOperation, openScanEnqueueRiver(t, database), service.SourceAnalysisJobArgs{OperationID: failedOperation.ID}, nil); err != nil {
		t.Fatalf("admit analysis before root deletion: %v", err)
	}
	if err := inventory.SettleNormalizedSourceAnalysisOperation(ctx, failedOperation.ID, "failed", "analysis", "fixture analysis failure"); err != nil {
		t.Fatalf("settle failed analysis before root deletion: %v", err)
	}
	storedFailure, err := persistence.NewSetupManagerRepository(database).GetOperation(ctx, failedOperation.ID)
	if err != nil || storedFailure.TargetSourceRootID != nil || storedFailure.State != "failed" {
		t.Fatalf("failed analysis baseline with cleared selectors: %+v, %v", storedFailure, err)
	}
	roots := service.NewSourceRoots(inventory, appsettings.New(settings, nil))
	if err := roots.Delete(ctx, root.ID, root.ConfiguredPath, 2); err != nil {
		t.Fatalf("delete source root: %v", err)
	}
	if _, err := os.Stat(artifactPath); !os.IsNotExist(err) {
		t.Fatalf("staged artifact remains after root deletion (stat err = %v)", err)
	}
	if content, err := os.ReadFile(outputPath); err != nil || string(content) != "host output" {
		t.Fatalf("host output after root deletion = %q, err %v; want preserved", content, err)
	}
	var failedTaskCount int
	if err := database.NewRaw(`SELECT count(*) FROM operation WHERE id=?`, failedOperation.ID).Scan(ctx, &failedTaskCount); err != nil || failedTaskCount != 0 {
		t.Fatalf("failed analysis task after root deletion: count=%d err=%v", failedTaskCount, err)
	}
	var failedJobState string
	if err := database.NewRaw(`SELECT state FROM river_job WHERE id=?`, *failedOperation.RiverJobID).Scan(ctx, &failedJobState); err != nil || failedJobState != "cancelled" {
		t.Fatalf("failed analysis delivery after root deletion: state=%q err=%v", failedJobState, err)
	}
	var retainedCount, artifactCount, unreadableCount, unreadableArtifactCount int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE id=?`, retainedID).Scan(ctx, &retainedCount); err != nil {
		t.Fatalf("read root-deleted work: %v", err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE work_id=?`, retainedID).Scan(ctx, &artifactCount); err != nil {
		t.Fatalf("read root-deleted artifact: %v", err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work WHERE id=?`, unreadableID).Scan(ctx, &unreadableCount); err != nil {
		t.Fatalf("read root-deleted formerly unreadable work: %v", err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE work_id=?`, unreadableID).Scan(ctx, &unreadableArtifactCount); err != nil {
		t.Fatalf("read root-deleted formerly unreadable artifact: %v", err)
	}
	if retainedCount != 0 || artifactCount != 0 {
		t.Fatalf("root-deleted work/artifact counts = %d/%d, want 0/0", retainedCount, artifactCount)
	}
	if unreadableCount != 0 || unreadableArtifactCount != 0 {
		t.Fatalf("root-deleted second work/artifact counts = %d/%d, want 0/0", unreadableCount, unreadableArtifactCount)
	}
}

// insertStagedAnalysisArtifact stages the immutable execution identity that an
// artifact must reference, then inserts the owned artifact row. Artifacts can
// only originate from a staged execution, so every artifact fixture goes
// through this shared helper.
func insertStagedAnalysisArtifact(t *testing.T, ctx context.Context, database *bun.DB, workID, ownerOperationID uuid.UUID, ownerAttempt int, ownerJobID int64, relativePath string, sizeBytes int64, mtime time.Time) uuid.UUID {
	t.Helper()
	artifactID := uuid.New()
	if relativePath == "" {
		var rootID uuid.UUID
		if err := database.NewRaw(`SELECT source_root_id FROM source_analysis_work WHERE id=?`, workID).Scan(ctx, &rootID); err != nil {
			t.Fatalf("read source root for artifact %s: %v", workID, err)
		}
		relativePath = filepath.ToSlash(filepath.Join("analysis", "staging", rootID.String(), workID.String(), artifactID.String()))
	}
	if _, err := database.NewRaw(`INSERT INTO source_analysis_work_execution
		(work_id,operation_id,operation_attempt,job_id,processing_mode)
		VALUES (?,?,?,?,'staged')`, workID, ownerOperationID, ownerAttempt, ownerJobID).Exec(ctx); err != nil {
		t.Fatalf("stage source analysis execution for %q: %v", relativePath, err)
	}
	if _, err := database.NewRaw(`INSERT INTO source_analysis_artifact
		(id,work_id,relative_output_path,source_size_bytes,source_mtime,owner_operation_id,owner_operation_attempt,owner_job_id,state)
		VALUES (?,?,?,?,?,?,?,?,'acquiring')`, artifactID, workID, relativePath, sizeBytes, mtime, ownerOperationID, ownerAttempt, ownerJobID).Exec(ctx); err != nil {
		t.Fatalf("create retained artifact %q: %v", relativePath, err)
	}
	return artifactID
}
