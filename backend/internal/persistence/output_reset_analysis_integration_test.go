//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func sameSnapshotIntent(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		return false
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func TestOutputResetCancelsQueuedNormalizedAnalysisAndRetainsSuccesses(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	client := openScanEnqueueRiver(t, database)
	manager := persistence.NewSetupManagerRepository(database)
	if err := persistence.NewSettingsRepository(database).Set(ctx, "output_directory", "/music/reset-analysis-old"); err != nil {
		t.Fatal(err)
	}
	repository := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, repository, "/srv/reset-analysis")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "track.flac", 2048, probeMtime())
	secondLocation := insertAnalysisLocation(t, ctx, database, root.ID, "track-2.flac", 4096, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepFingerprint, State: "pending"},
	)
	secondWork := normalizedWork(t, ctx, repository, root, secondLocation, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepFingerprint, State: "pending"},
	)
	tools := []persistence.SourceAnalysisToolSelection{
		insertVerifiedAnalysisTool(t, ctx, database, "ffmpeg", "ffprobe", "7.1.2", "ffprobe version 7.1.2"),
		insertVerifiedAnalysisTool(t, ctx, database, "fpcalc", "fpcalc", "1.5.1", "fpcalc version 1.5.1"),
	}

	// Batch admissions pin one work item each. Both remain queued: the test
	// records one work item's successful results directly without claiming either
	// delivery, because claiming requires the operation to be running.
	firstOperation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, true, false, tools)
	secondOperation := normalizedOperation(t, root, secondLocation, secondWork, persistence.SourceAnalysisModeBatch, nil, nil, true, false, tools)
	for _, operation := range []*persistence.Operation{firstOperation, secondOperation} {
		if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client,
			service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
			t.Fatalf("admit normalized queued analysis %s: %v", operation.ID, err)
		}
		if operation.RiverJobID == nil {
			t.Fatalf("admitted operation %s has no River job", operation.ID)
		}
		assertOperationHoldCounts(t, ctx, database, operation.ID, 1, 2)
	}
	operationSnapshots := map[uuid.UUID]json.RawMessage{
		firstOperation.ID:  firstOperation.InputSnapshot,
		secondOperation.ID: secondOperation.InputSnapshot,
	}
	stepSnapshots := make(map[string]json.RawMessage)
	for _, selectedWork := range []*persistence.SourceAnalysisWork{work, secondWork} {
		var steps []persistence.SourceAnalysisStep
		if err := database.NewSelect().Model(&steps).Where("work_id = ?", selectedWork.ID).Scan(ctx); err != nil {
			t.Fatalf("read admitted step snapshots: %v", err)
		}
		for _, step := range steps {
			stepSnapshots[selectedWork.ID.String()+":"+step.Step] = step.InputSnapshot
		}
	}

	// Model one work group committing successful SHA/probe/fingerprint results
	// while the second work group's steps remain queued. Preserve the admitted
	// execution triples until reset, and use the real admitted operation ID as
	// the result provenance.
	digest := make([]byte, 32)
	digest[0] = 0xa5
	shaVariant := uuid.New()
	probeVariant := uuid.New()
	fingerprintID := uuid.New()
	inspected := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := database.ExecContext(ctx, `INSERT INTO media_variant
		(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id,
		 ffprobe_version,ffprobe_json,analysis_policy_version,observed_tags,inspected_at,applied_operation_id,audio_stream_count)
		VALUES(?,?,?,now(),'SHA-256',?,?,?::jsonb,?,?,?, ?,1)`, shaVariant, location.SizeBytes, digest, firstOperation.ID,
		"7.1.2", `{"format":{"format_name":"flac"}}`, persistence.SourceAnalysisPolicyVersion, `{}`, inspected, firstOperation.ID); err != nil {
		t.Fatalf("insert successful SHA/probe variant: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO media_variant
		(id,size_bytes,ffprobe_version,ffprobe_json,analysis_policy_version,observed_tags,inspected_at,applied_operation_id,audio_stream_count)
		VALUES(?,?,?,?::jsonb,?,?,?, ?,1)`, probeVariant, location.SizeBytes, "7.1.2", `{"format":{"format_name":"flac"}}`,
		persistence.SourceAnalysisPolicyVersion, `{}`, inspected, firstOperation.ID); err != nil {
		t.Fatalf("insert successful probe result: %v", err)
	}
	fingerprint := &persistence.SourceFingerprintResult{ID: fingerprintID, FPCalcVersion: "1.5.1", VersionBanner: "fpcalc version 1.5.1",
		AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "123,456", ReportedDuration: 2,
		CalculatedAt: inspected, AppliedOperationID: firstOperation.ID, ParserContractVersion: 1, SourceSHA256: digest}
	if _, err := database.NewInsert().Model(fingerprint).Exec(ctx); err != nil {
		t.Fatalf("insert successful fingerprint: %v", err)
	}
	for _, selection := range []struct {
		step   string
		column string
		id     uuid.UUID
	}{{"sha256", "success_sha_variant_id", shaVariant}, {"probe", "success_probe_variant_id", probeVariant}, {"fingerprint", "success_fingerprint_result_id", fingerprintID}} {
		if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET state='succeeded',`+selection.column+`=?,success_reuse_origin='executed',last_operation_id=? WHERE work_id=? AND step=?`, selection.id, firstOperation.ID, work.ID, selection.step); err != nil {
			t.Fatalf("preserve successful %s selection: %v", selection.step, err)
		}
	}

	if _, err := manager.RunOutputReset(ctx, "/music/reset-analysis-old", "/music/reset-analysis-new",
		func(context.Context, func(string) error) error { return nil },
		func(context.Context, persistence.OutputResetJournal) error { return nil }); err != nil {
		t.Fatalf("run output reset: %v", err)
	}
	for _, operation := range []*persistence.Operation{firstOperation, secondOperation} {
		stored, err := manager.GetOperation(ctx, operation.ID)
		if err != nil || stored.State != "failed" || stored.SourceAnalysisMode != persistence.SourceAnalysisModeBatch ||
			stored.TargetSourceRootID != nil || stored.TargetSourceLocationID != nil || stored.TargetWorkID != nil || stored.TargetStep != nil ||
			stored.ToolsReadRequired || stored.RerunTarget || stored.SafeError == nil || *stored.SafeError == "" {
			t.Fatalf("reset operation %s = %+v, %v; want a legal failed normalized terminal operation", operation.ID, stored, err)
		}
		if _, err := persistence.ValidateSourceAnalysisOperationContract(stored); err != nil {
			t.Fatalf("validate reset operation %s terminal contract: %v", operation.ID, err)
		}
		if !sameSnapshotIntent(stored.InputSnapshot, operationSnapshots[operation.ID]) {
			t.Fatalf("reset changed durable operation snapshot for %s: got %s, want %s", operation.ID, stored.InputSnapshot, operationSnapshots[operation.ID])
		}
		assertOperationHoldCounts(t, ctx, database, operation.ID, 0, 0)
		var riverState string
		if err := database.NewRaw(`SELECT state FROM river_job WHERE id=?`, *operation.RiverJobID).Scan(ctx, &riverState); err != nil || riverState != "cancelled" {
			t.Fatalf("River job state for %s after reset = %q, %v; want cancelled", operation.ID, riverState, err)
		}
	}

	var executions, inputs int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE execution_operation_id IS NOT NULL OR execution_operation_attempt IS NOT NULL OR execution_job_id IS NOT NULL`).Scan(ctx, &executions); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE input_snapshot IS NOT NULL`).Scan(ctx, &inputs); err != nil {
		t.Fatal(err)
	}
	if executions != 0 || inputs != len(stepSnapshots) {
		t.Fatalf("reset retained execution triples=%d or input snapshots=%d; want 0 triples and %d durable snapshots", executions, inputs, len(stepSnapshots))
	}
	var succeeded []persistence.SourceAnalysisStep
	if err := database.NewSelect().Model(&succeeded).Where("work_id=?", work.ID).Order("step").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(succeeded) != 3 {
		t.Fatalf("successful work steps after reset = %d; want 3", len(succeeded))
	}
	for _, step := range succeeded {
		if step.State != "succeeded" {
			t.Errorf("successful step after reset = %+v; want succeeded fence retained", step)
		}
	}
	var interrupted []persistence.SourceAnalysisStep
	if err := database.NewSelect().Model(&interrupted).Where("work_id=?", secondWork.ID).Order("step").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(interrupted) != 3 {
		t.Fatalf("interrupted work steps after reset = %d; want 3", len(interrupted))
	}
	for _, step := range interrupted {
		if step.State != "failed" || step.SafeError == nil || *step.SafeError == "" ||
			step.ExecutionOperationID != nil || step.ExecutionOperationAttempt != nil || step.ExecutionJobID != nil ||
			!sameSnapshotIntent(step.InputSnapshot, stepSnapshots[secondWork.ID.String()+":"+step.Step]) {
			t.Errorf("interrupted step after reset = %+v; want safe failed step with ownership cleared and durable input retained", step)
		}
	}
	for _, original := range []*persistence.SourceAnalysisWork{work, secondWork} {
		retainedWork, _, err := repository.GetNormalizedSourceAnalysisWork(ctx, original.ID)
		if err != nil || retainedWork == nil || retainedWork.OriginScanOperationID != original.OriginScanOperationID {
			t.Fatalf("work origin after reset = %+v, %v; want origin %s", retainedWork, err, original.OriginScanOperationID)
		}
	}
	for _, selection := range []struct {
		step   string
		column string
		want   uuid.UUID
	}{{"sha256", "success_sha_variant_id", shaVariant}, {"probe", "success_probe_variant_id", probeVariant}, {"fingerprint", "success_fingerprint_result_id", fingerprintID}} {
		var retained uuid.UUID
		var origin string
		var lastOperation uuid.UUID
		if err := database.NewRaw(`SELECT `+selection.column+`,success_reuse_origin,last_operation_id FROM source_analysis_step WHERE work_id=? AND step=?`, work.ID, selection.step).Scan(ctx, &retained, &origin, &lastOperation); err != nil {
			t.Fatal(err)
		}
		if retained != selection.want || origin != "executed" || lastOperation != firstOperation.ID {
			t.Errorf("successful %s fence after reset = %s origin=%q last-operation=%s; want %s executed by %s", selection.step, retained, origin, lastOperation, selection.want, firstOperation.ID)
		}
	}
	var variants, fingerprints int
	if err := database.NewRaw(`SELECT count(*) FROM media_variant WHERE id IN (?,?)`, shaVariant, probeVariant).Scan(ctx, &variants); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM media_fingerprint_result WHERE id=?`, fingerprintID).Scan(ctx, &fingerprints); err != nil {
		t.Fatal(err)
	}
	if variants != 2 || fingerprints != 1 {
		t.Fatalf("reset removed success refs: variants=%d fingerprints=%d", variants, fingerprints)
	}

	// Reset leaves no pending work for this root: pending listing must not
	// auto-dispatch or convert the failed second work item back to pending.
	pending, err := repository.ListPendingSourceAnalysisWork(ctx, root.ID)
	if err != nil {
		t.Fatalf("list pending work after reset: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending work after reset = %d; want none", len(pending))
	}
	// A fresh, correctly fenced operation must still be rejected specifically
	// because the steps are failed, not because its job delivery identity differs.
	reattempt := normalizedOperation(t, root, secondLocation, secondWork, persistence.SourceAnalysisModeBatch, nil, nil, true, false, tools)
	err = repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, reattempt, client,
		service.SourceAnalysisJobArgs{OperationID: reattempt.ID}, nil)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "pending") {
		t.Fatalf("readmit failed steps error = %v; want pending-state rejection", err)
	}
}
