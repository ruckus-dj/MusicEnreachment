//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestReadSourceLocationDetailIncludesSelectedAnalysisAndBatchHoldWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, repository, "/srv/location-detail-analysis")
	mtime := probeMtime()
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 4096, mtime)
	noProbeLocation := insertAnalysisLocation(t, ctx, database, root.ID, "album/other.flac", 2048, mtime)
	establishInventory(t, ctx, database, root)
	installation := insertAnalysisInstallation(t, ctx, database, "location-detail-analysis")
	activeFPCalc := readyFPCalc(t, "fpcalc-active", "1.6.0")
	activeFPCalcID := activeFPCalc.ID
	if _, err := database.NewInsert().Model(activeFPCalc).Exec(ctx); err != nil {
		t.Fatalf("insert active fpcalc installation: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value) VALUES('active_fpcalc_installation_id',?)`, activeFPCalcID.String()); err != nil {
		t.Fatalf("set active fpcalc installation: %v", err)
	}
	work := normalizedWork(t, ctx, repository, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepFingerprint, State: "pending"},
	)
	probeTool := normalizedToolSelection(t, ctx, database, installation, "ffprobe")
	fingerprintTool := normalizedToolSelection(t, ctx, database, activeFPCalcID, "fpcalc")
	operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, true, false,
		[]persistence.SourceAnalysisToolSelection{probeTool, fingerprintTool})
	admitAndRunNormalizedAnalysis(t, ctx, database, repository, openScanEnqueueRiver(t, database), operation)
	noProbeWork := detailWork(t, ctx, database, root, noProbeLocation)

	canonicalID := uuid.New()
	selectedProbeID := uuid.New()
	for _, row := range []struct {
		id    uuid.UUID
		size  int64
		probe string
		count int
	}{
		{id: canonicalID, size: location.SizeBytes, probe: "canonical", count: 2},
		{id: selectedProbeID, size: location.SizeBytes, probe: "selected", count: 1},
	} {
		if _, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count)
			VALUES(?,?,1,'7.1.2',?::jsonb,'{}'::jsonb,?,?,?)`, row.id, row.size,
			json.RawMessage(`{"format":{"format_name":"`+row.probe+`"}}`), time.Now().UTC(), operation.ID, row.count); err != nil {
			t.Fatalf("insert %s probe variant: %v", row.probe, err)
		}
	}
	noProbeCanonicalID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count)
		VALUES(?,?,1,'7.1.2','{"format":{"format_name":"no probe canonical"}}'::jsonb,'{}'::jsonb,?,?,2)`, noProbeCanonicalID, noProbeLocation.SizeBytes, time.Now().UTC(), operation.ID); err != nil {
		t.Fatalf("insert canonical result for current failed work: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_location SET media_variant_id=? WHERE id=?`, canonicalID, location.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_location SET media_variant_id=? WHERE id=?`, noProbeCanonicalID, noProbeLocation.ID); err != nil {
		t.Fatal(err)
	}
	fingerprintID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO media_fingerprint_result(id,fpcalc_version,version_banner,algorithm_namespace,algorithm_id,fingerprint,reported_duration,calculated_at,applied_operation_id,parser_contract_version,winning_result_id)
		VALUES(?,'1.5.1','fpcalc 1.5.1','chromaprint',1,'AQID',42,now(),?,1,?)`, fingerprintID, operation.ID, fingerprintID); err != nil {
		t.Fatal(err)
	}
	stepRows := []persistence.SourceAnalysisStep{
		{WorkID: work.ID, Step: string(persistence.SourceStepProbe), State: "failed", StepAttempt: 2, SafeError: stringPointer("probe rerun failed"), SuccessProbeVariantID: &selectedProbeID, SuccessReuseOrigin: stringPointer("executed")},
		{WorkID: work.ID, Step: string(persistence.SourceStepSHA256), State: "pending"},
		{WorkID: work.ID, Step: string(persistence.SourceStepFingerprint), State: "succeeded", SuccessFingerprintResultID: &fingerprintID, SuccessReuseOrigin: stringPointer("executed")},
		{WorkID: noProbeWork.ID, Step: string(persistence.SourceStepProbe), State: "failed", StepAttempt: 1, SafeError: stringPointer("probe unavailable")},
		{WorkID: noProbeWork.ID, Step: string(persistence.SourceStepFingerprint), State: "succeeded", SuccessFingerprintResultID: &fingerprintID, SuccessReuseOrigin: stringPointer("executed")},
	}
	for i := range stepRows {
		if stepRows[i].WorkID == work.ID {
			stepRows[i].ExecutionOperationID = nil
			stepRows[i].ExecutionOperationAttempt = nil
			stepRows[i].ExecutionJobID = nil
			if _, err := database.NewUpdate().Model(&stepRows[i]).
				Column("state", "step_attempt", "safe_error", "execution_operation_id", "execution_operation_attempt", "execution_job_id", "success_sha_variant_id", "success_probe_variant_id", "success_fingerprint_result_id", "success_reuse_origin").
				Where("work_id = ? AND step = ?", stepRows[i].WorkID, stepRows[i].Step).Exec(ctx); err != nil {
				t.Fatalf("update admitted analysis step %s: %v", stepRows[i].Step, err)
			}
		} else if _, err := database.NewInsert().Model(&stepRows[i]).Exec(ctx); err != nil {
			t.Fatalf("insert analysis step %s: %v", stepRows[i].Step, err)
		}
	}
	writer, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(ctx, `UPDATE source_root SET display_name='concurrent update' WHERE id=?`, root.ID); err != nil {
		_ = writer.Rollback()
		t.Fatal(err)
	}
	newActiveFPCalc := readyFPCalc(t, "fpcalc-next", "1.7.0")
	newActiveFPCalcID := newActiveFPCalc.ID
	if _, err := writer.ExecContext(ctx, `INSERT INTO tool_installation(id,package_kind,platform_goos,platform_goarch,source_name,release_identity,relative_path,state,executable_versions,artifact_identities,verified_at)
		VALUES(?, 'fpcalc', 'darwin', 'arm64', 'analysis-test', ?, ?, 'ready', ?::jsonb, '{}'::jsonb, ?)`, newActiveFPCalcID, newActiveFPCalc.ReleaseIdentity, newActiveFPCalc.RelativePath, newActiveFPCalc.ExecutableVersions, newActiveFPCalc.VerifiedAt); err != nil {
		_ = writer.Rollback()
		t.Fatalf("insert uncommitted active fpcalc installation: %v", err)
	}
	if _, err := writer.ExecContext(ctx, `UPDATE app_setting SET setting_value=? WHERE setting_name='active_fpcalc_installation_id'`, newActiveFPCalcID.String()); err != nil {
		_ = writer.Rollback()
		t.Fatalf("switch active fpcalc in concurrent writer: %v", err)
	}
	concurrentSnapshot, err := repository.ReadSourceLocationDetail(ctx, root.ID, location.ID)
	if err != nil {
		_ = writer.Rollback()
		t.Fatalf("read alongside uncommitted writer: %v", err)
	}
	if concurrentSnapshot.Root.DisplayName == "concurrent update" {
		_ = writer.Rollback()
		t.Fatal("repeatable-read snapshot observed another transaction's uncommitted root update")
	}
	if concurrentSnapshot.ActiveFPCalcInstallation == nil || concurrentSnapshot.ActiveFPCalcInstallation.ID != activeFPCalcID {
		_ = writer.Rollback()
		t.Fatalf("repeatable-read active fpcalc = %v, want committed installation %s", concurrentSnapshot.ActiveFPCalcInstallation, activeFPCalcID)
	}
	if err := writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.NewInsert().Model(newActiveFPCalc).Exec(ctx); err != nil {
		t.Fatalf("persist replacement active fpcalc installation: %v", err)
	}

	snapshot, err := repository.ReadSourceLocationDetail(ctx, root.ID, location.ID)
	if err != nil {
		t.Fatalf("read selected detail: %v", err)
	}
	if snapshot.Work == nil || snapshot.Work.ID != work.ID || len(snapshot.Steps) != 3 {
		t.Fatalf("work and ordered steps = %v, %v; want current work and all three steps", snapshot.Work, snapshot.Steps)
	}
	if got := []string{snapshot.Steps[0].Step, snapshot.Steps[1].Step, snapshot.Steps[2].Step}; got[0] != "sha256" || got[1] != "probe" || got[2] != "fingerprint" {
		t.Fatalf("step order = %v", got)
	}
	if snapshot.Steps[1].State != "failed" || snapshot.Steps[1].SuccessProbeVariantID == nil || *snapshot.Steps[1].SuccessProbeVariantID != selectedProbeID {
		t.Fatalf("failed rerun did not retain selected probe: %+v", snapshot.Steps[1])
	}
	if snapshot.SHAVariant != nil || snapshot.Fingerprint == nil || snapshot.Fingerprint.ID != fingerprintID {
		t.Fatalf("independent result selection = SHA %v fingerprint %v", snapshot.SHAVariant, snapshot.Fingerprint)
	}
	if !snapshot.MatchingEligible {
		t.Fatal("retained selected one-audio-stream probe plus successful fingerprint should be eligible")
	}
	if snapshot.Variant == nil || snapshot.Variant.ID != selectedProbeID {
		t.Fatalf("technical result = %v; want selected probe %s, not canonical location result %s", snapshot.Variant, selectedProbeID, canonicalID)
	}
	if snapshot.ActiveOperationID == nil || *snapshot.ActiveOperationID != operation.ID {
		t.Fatalf("active batch operation = %v, want %s", snapshot.ActiveOperationID, operation.ID)
	}
	if snapshot.ActiveFPCalcInstallation == nil || snapshot.ActiveFPCalcInstallation.ID != activeFPCalcID {
		t.Fatalf("active fpcalc installation = %v, want %s", snapshot.ActiveFPCalcInstallation, activeFPCalcID)
	}

	withoutProbe, err := repository.ReadSourceLocationDetail(ctx, root.ID, noProbeLocation.ID)
	if err != nil {
		t.Fatalf("read independent-fingerprint detail: %v", err)
	}
	if withoutProbe.Fingerprint == nil || withoutProbe.Steps[0].State != "failed" || withoutProbe.MatchingEligible {
		t.Fatalf("independent fingerprint without successful probe = %+v; want saved fingerprint but ineligible", withoutProbe)
	}
	if withoutProbe.ActiveOperationID != nil {
		t.Fatalf("batch operation for another work item appeared active here: %s", *withoutProbe.ActiveOperationID)
	}
	if withoutProbe.Variant != nil {
		t.Fatalf("current work without successful probe fell back to canonical variant %s", withoutProbe.Variant.ID)
	}

	if _, err := database.ExecContext(ctx, `UPDATE app_setting SET setting_value=? WHERE setting_name='active_fpcalc_installation_id'`, newActiveFPCalcID.String()); err != nil {
		t.Fatalf("commit active fpcalc switch: %v", err)
	}
	switchedSnapshot, err := repository.ReadSourceLocationDetail(ctx, root.ID, location.ID)
	if err != nil {
		t.Fatalf("read after active fpcalc switch: %v", err)
	}
	if switchedSnapshot.ActiveFPCalcInstallation == nil || switchedSnapshot.ActiveFPCalcInstallation.ID != newActiveFPCalcID {
		t.Fatalf("active fpcalc after switch = %v, want %s", switchedSnapshot.ActiveFPCalcInstallation, newActiveFPCalcID)
	}
}

func readyFPCalc(t *testing.T, releaseIdentity, version string) *persistence.ToolInstallation {
	t.Helper()
	relativePath, err := tools.ManagedRelativePath(tools.PackageFPCalc, releaseIdentity)
	if err != nil {
		t.Fatalf("managed fpcalc detail installation path: %v", err)
	}
	verifiedAt := time.Now().UTC()
	return &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: string(tools.PackageFPCalc), PlatformGOOS: "darwin", PlatformGOARCH: "arm64",
		SourceName: "analysis-test", ReleaseIdentity: releaseIdentity, RelativePath: relativePath,
		State: "ready", ExecutableVersions: verifiedExecutableVersionMetadata(t, tools.PackageFPCalc, "darwin", version),
		ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verifiedAt,
	}
}

func detailWork(t *testing.T, ctx context.Context, database *bun.DB, root *persistence.SourceRoot, location persistence.SourceLocation) *persistence.SourceAnalysisWork {
	t.Helper()
	work := &persistence.SourceAnalysisWork{
		ID: uuid.New(), LocationID: location.ID, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, InventoryPath: *root.InventoryPath,
		RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime,
		OriginScanOperationID: uuid.New(),
	}
	if _, err := database.NewInsert().Model(work).Exec(ctx); err != nil {
		t.Fatalf("insert source analysis work: %v", err)
	}
	return work
}
