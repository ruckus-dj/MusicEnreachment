//go:build integration

package persistence_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestSourceAnalysisArtifactAcquireAndReadyFencingWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newNormalizedWorkOperation(t, "/srv/artifact-fence", true,
		[]persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}})
	artifacts := persistence.NewSourceAnalysisArtifactRepository(fixture.database)
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_root SET processing_mode='staged' WHERE id=?`, fixture.root.ID); err != nil {
		t.Fatal(err)
	}
	operation := fixture.batchOperation(t)
	if err := fixture.admit(t, ctx, operation); err != nil {
		t.Fatalf("admit analysis operation: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `UPDATE operation SET state='running',stage='hashing',started_at=now() WHERE id=?`, operation.ID); err != nil {
		t.Fatal(err)
	}
	fence := persistence.SourceAnalysisArtifactFence{
		WorkID: fixture.work.ID, OperationID: operation.ID,
		OperationAttempt: operation.Attempt, JobID: *operation.RiverJobID,
	}
	insertWorkExecution(t, ctx, fixture, fence, "staged")
	claim := persistence.SourceStepClaim{WorkID: fence.WorkID, OperationID: fence.OperationID,
		OperationAttempt: fence.OperationAttempt, JobID: fence.JobID, Step: persistence.SourceStepSHA256}
	if _, err := fixture.repository.ClaimSourceAnalysisStep(ctx, claim); err != nil {
		t.Fatalf("claim analysis step: %v", err)
	}

	artifactID := uuid.New()
	artifact, err := artifacts.Acquire(ctx, artifactID, fence)
	if err != nil {
		t.Fatalf("acquire artifact: %v", err)
	}
	if !artifact.RequestedStepsKnown || len(artifact.RequestedSteps) != 1 || artifact.RequestedSteps[0] != string(persistence.SourceStepSHA256) {
		t.Fatalf("acquired artifact intent = %v (known %t), want known sha256", artifact.RequestedSteps, artifact.RequestedStepsKnown)
	}
	if _, err := artifacts.Acquire(ctx, artifactID, fence); err != nil {
		t.Fatalf("repeat artifact acquisition: %v", err)
	}
	var binding struct {
		RequestedSteps []string `bun:"requested_steps,array"`
	}
	if err := fixture.database.NewRaw(`SELECT requested_steps FROM source_analysis_work_artifact_binding WHERE work_id=?`, fence.WorkID).Scan(ctx, &binding); err != nil {
		t.Fatalf("read requested artifact steps: %v", err)
	}
	if len(binding.RequestedSteps) != 1 || binding.RequestedSteps[0] != string(persistence.SourceStepSHA256) {
		t.Fatalf("requested artifact steps = %v, want one sha256 entry", binding.RequestedSteps)
	}
	wantPath := "analysis/staging/" + strings.ToLower(fixture.root.ID.String()) + "/" +
		strings.ToLower(fixture.work.ID.String()) + "/" + strings.ToLower(artifactID.String())
	if artifact.RelativeOutputPath != wantPath || artifact.State != persistence.SourceAnalysisArtifactAcquiring || artifact.SourceSizeBytes != fixture.work.SizeBytes {
		t.Fatalf("acquired artifact = %+v, want canonical path %q and acquiring state", artifact, wantPath)
	}
	bound, err := artifacts.GetBinding(ctx, fixture.work.ID)
	if err != nil || bound == nil || bound.ID != artifactID {
		t.Fatalf("binding after acquisition = %+v, %v; want artifact %s", bound, err, artifactID)
	}

	// A registered path collision owned by another artifact ID must make the
	// insert fail; Acquire never adopts or overwrites that row.
	collisionID, occupyingID := uuid.New(), uuid.New()
	collisionPath := "analysis/staging/" + strings.ToLower(fixture.root.ID.String()) + "/" +
		strings.ToLower(fixture.work.ID.String()) + "/" + strings.ToLower(collisionID.String())
	if _, err := fixture.database.ExecContext(ctx, `INSERT INTO source_analysis_artifact
		(id,work_id,relative_output_path,source_size_bytes,source_mtime,owner_operation_id,owner_operation_attempt,owner_job_id,state)
		VALUES (?,?,?,?,?,?,?,?, 'acquiring')`, occupyingID, fixture.work.ID, collisionPath, fixture.work.SizeBytes,
		fixture.work.Mtime, operation.ID, operation.Attempt, *operation.RiverJobID); err != nil {
		t.Fatalf("insert path collision fixture: %v", err)
	}
	if _, err := artifacts.Acquire(ctx, collisionID, fence); err == nil {
		t.Fatal("acquire adopted an artifact path already registered to another artifact")
	}

	stale := fence
	stale.JobID++
	if _, err := artifacts.Acquire(ctx, uuid.New(), stale); err == nil {
		t.Fatal("stale delivery acquired artifact")
	}
	if _, err := artifacts.MarkReady(ctx, artifactID, stale, artifact.SourceSizeBytes); err == nil {
		t.Fatal("stale delivery marked artifact ready")
	}
	if _, err := artifacts.MarkReady(ctx, artifactID, fence, artifact.SourceSizeBytes-1); err == nil {
		t.Fatal("artifact became ready with a mismatched copied length")
	}
	ready, err := artifacts.MarkReady(ctx, artifactID, fence, artifact.SourceSizeBytes)
	if err != nil {
		t.Fatalf("mark exact-length artifact ready: %v", err)
	}
	if ready.State != persistence.SourceAnalysisArtifactReady {
		t.Fatalf("artifact state after ready = %q, want ready", ready.State)
	}
	readyAgain, err := artifacts.MarkReady(ctx, artifactID, fence, artifact.SourceSizeBytes)
	if err != nil || readyAgain.State != persistence.SourceAnalysisArtifactReady {
		t.Fatalf("idempotent ready = %+v, %v", readyAgain, err)
	}
	retained, err := artifacts.ListRetained(ctx, fixture.work.ID)
	if err != nil || len(retained) != 1 || retained[0].ID != artifactID {
		t.Fatalf("retained artifacts = %+v, %v; want the canonical ready artifact", retained, err)
	}
	borrowed, err := artifacts.BindRetained(ctx, artifactID, fence)
	if err != nil || borrowed == nil || borrowed.ID != artifactID || !borrowed.RequestedStepsKnown {
		t.Fatalf("idempotently bind retained artifact = %+v, %v", borrowed, err)
	}
	if err := artifacts.ForgetUncreated(ctx, artifactID, fence); err == nil {
		t.Fatal("ready artifact was forgotten as uncreated")
	}
}

func TestSourceAnalysisArtifactRejectsStaleWorkAndForgetsOnlyOwnedAcquiringRowWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newNormalizedWorkOperation(t, "/srv/artifact-stale", true,
		[]persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}})
	artifacts := persistence.NewSourceAnalysisArtifactRepository(fixture.database)
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_root SET processing_mode='staged' WHERE id=?`, fixture.root.ID); err != nil {
		t.Fatal(err)
	}
	operation := fixture.batchOperation(t)
	if err := fixture.admit(t, ctx, operation); err != nil {
		t.Fatalf("admit analysis operation: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `UPDATE operation SET state='running',stage='hashing',started_at=now() WHERE id=?`, operation.ID); err != nil {
		t.Fatal(err)
	}
	fence := persistence.SourceAnalysisArtifactFence{WorkID: fixture.work.ID, OperationID: operation.ID,
		OperationAttempt: operation.Attempt, JobID: *operation.RiverJobID}
	insertWorkExecution(t, ctx, fixture, fence, "staged")
	if _, err := fixture.repository.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
		WorkID: fence.WorkID, OperationID: fence.OperationID, OperationAttempt: fence.OperationAttempt,
		JobID: fence.JobID, Step: persistence.SourceStepSHA256,
	}); err != nil {
		t.Fatalf("claim analysis step: %v", err)
	}
	artifactID := uuid.New()
	if _, err := artifacts.Acquire(ctx, artifactID, fence); err != nil {
		t.Fatalf("acquire artifact: %v", err)
	}

	wrongOwner := fence
	wrongOwner.OperationAttempt++
	if err := artifacts.ForgetUncreated(ctx, artifactID, wrongOwner); err == nil {
		t.Fatal("different operation attempt forgot artifact")
	}
	if err := artifacts.ForgetUncreated(ctx, artifactID, fence); err != nil {
		t.Fatalf("forget exact uncreated artifact: %v", err)
	}
	var count int
	if err := fixture.database.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE id=?`, artifactID).Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("artifact rows after exact forget = %d, want 0", count)
	}

	changed := fixture.work.SizeBytes + 1
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_location SET size_bytes=? WHERE id=?`, changed, fixture.location.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := artifacts.Acquire(ctx, uuid.New(), fence); err == nil {
		t.Fatal("stale source stat acquired artifact")
	}
}

func TestSourceAnalysisArtifactAcquireReplacesOnlyOrphanedSelectionWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newNormalizedWorkOperation(t, "/srv/artifact-replacement", true,
		[]persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}})
	artifacts := persistence.NewSourceAnalysisArtifactRepository(fixture.database)
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_root SET processing_mode='staged' WHERE id=?`, fixture.root.ID); err != nil {
		t.Fatal(err)
	}
	operation := fixture.batchOperation(t)
	if err := fixture.admit(t, ctx, operation); err != nil {
		t.Fatalf("admit analysis operation: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `UPDATE operation SET state='running',stage='hashing',started_at=now() WHERE id=?`, operation.ID); err != nil {
		t.Fatal(err)
	}
	first := persistence.SourceAnalysisArtifactFence{WorkID: fixture.work.ID, OperationID: operation.ID,
		OperationAttempt: operation.Attempt, JobID: *operation.RiverJobID}
	insertWorkExecution(t, ctx, fixture, first, "staged")
	if _, err := fixture.repository.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
		WorkID: first.WorkID, OperationID: first.OperationID, OperationAttempt: first.OperationAttempt,
		JobID: first.JobID, Step: persistence.SourceStepSHA256,
	}); err != nil {
		t.Fatalf("claim first delivery: %v", err)
	}
	oldArtifactID := uuid.New()
	if _, err := artifacts.Acquire(ctx, oldArtifactID, first); err != nil {
		t.Fatalf("acquire first artifact: %v", err)
	}

	second := first
	second.OperationAttempt++
	second.JobID++
	setupTx, err := fixture.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin replacement delivery fixture: %v", err)
	}
	defer setupTx.Rollback()
	if _, err := setupTx.ExecContext(ctx, `UPDATE source_analysis_step SET state='pending',execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL WHERE work_id=? AND step='sha256'`, first.WorkID); err != nil {
		t.Fatalf("clear old step delivery fence: %v", err)
	}
	if _, err := setupTx.ExecContext(ctx, `UPDATE operation SET attempt=?,river_job_id=? WHERE id=?`,
		second.OperationAttempt, second.JobID, operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := setupTx.ExecContext(ctx, `INSERT INTO source_analysis_work_execution
		(work_id,operation_id,operation_attempt,job_id,processing_mode) VALUES (?,?,?,?, 'staged')`, second.WorkID, second.OperationID, second.OperationAttempt, second.JobID); err != nil {
		t.Fatalf("insert new delivery execution: %v", err)
	}
	if _, err := setupTx.ExecContext(ctx, `UPDATE source_analysis_step SET state='queued',execution_operation_id=?,execution_operation_attempt=?,execution_job_id=?,step_attempt=0 WHERE work_id=? AND step='sha256'`,
		second.OperationID, second.OperationAttempt, second.JobID, second.WorkID); err != nil {
		t.Fatal(err)
	}
	if err := setupTx.Commit(); err != nil {
		t.Fatalf("commit replacement delivery fixture: %v", err)
	}
	newArtifactID := uuid.New()
	if _, err := artifacts.Acquire(ctx, newArtifactID, second); err != nil {
		t.Fatalf("replace terminal creator's stale selection: %v", err)
	}
	if err := artifacts.ForgetUncreated(ctx, oldArtifactID, first); err == nil {
		t.Fatal("former creator forgot an artifact after a newer delivery adopted the work")
	}
	if err := artifacts.InvalidateBinding(ctx, oldArtifactID, second); err == nil {
		t.Fatal("invalidation for the former selection removed the new binding")
	}
	assertArtifactBinding(t, ctx, fixture, newArtifactID)

	if _, err := artifacts.Acquire(ctx, uuid.New(), second); err == nil {
		t.Fatal("acquire stole a binding with a live creator/borrower")
	}
	if err := artifacts.InvalidateBinding(ctx, newArtifactID, second); err != nil {
		t.Fatalf("invalidate exact current binding: %v", err)
	}
	replacementID := uuid.New()
	if _, err := artifacts.Acquire(ctx, replacementID, second); err != nil {
		t.Fatalf("acquire after missing selection was invalidated: %v", err)
	}
	assertArtifactBinding(t, ctx, fixture, replacementID)
	var retained int
	if err := fixture.database.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE id=?`, oldArtifactID).Scan(ctx, &retained); err != nil {
		t.Fatal(err)
	}
	if retained != 1 {
		t.Fatalf("old artifact registry rows = %d, want 1 (selection replacement must not delete artifact bytes/record)", retained)
	}
}

func assertArtifactBinding(t *testing.T, ctx context.Context, fixture normalizedAnalysisFixture, want uuid.UUID) {
	t.Helper()
	var got uuid.UUID
	if err := fixture.database.NewRaw(`SELECT artifact_id FROM source_analysis_work_artifact_binding WHERE work_id=?`, fixture.work.ID).Scan(ctx, &got); err != nil {
		t.Fatalf("read artifact binding: %v", err)
	}
	if got != want {
		t.Fatalf("bound artifact = %s, want %s", got, want)
	}
}

func TestSourceAnalysisExecutionModeIsImmutableAcrossRootModeChangesAndRetryWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newNormalizedWorkOperation(t, "/srv/artifact-execution-mode", true,
		[]persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}})
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_root SET processing_mode='staged' WHERE id=?`, fixture.root.ID); err != nil {
		t.Fatal(err)
	}
	operation := fixture.batchOperation(t)
	if err := fixture.admit(t, ctx, operation); err != nil {
		t.Fatalf("admit analysis operation: %v", err)
	}
	started, _, mode, err := fixture.repository.StartNormalizedSourceAnalysisDelivery(ctx, operation.ID,
		persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID}, "", "")
	if err != nil {
		t.Fatalf("start staged delivery: %v", err)
	}
	if mode != "staged" || started.State != "running" {
		t.Fatalf("started delivery = mode %q state %q, want staged/running", mode, started.State)
	}
	fence := persistence.SourceAnalysisArtifactFence{WorkID: fixture.work.ID, OperationID: operation.ID,
		OperationAttempt: operation.Attempt, JobID: *operation.RiverJobID}
	claim := persistence.SourceStepClaim{WorkID: fence.WorkID, OperationID: fence.OperationID,
		OperationAttempt: fence.OperationAttempt, JobID: fence.JobID, Step: persistence.SourceStepSHA256}
	if _, err := fixture.repository.ClaimSourceAnalysisStep(ctx, claim); err != nil {
		t.Fatalf("claim staged SHA step: %v", err)
	}
	artifacts := persistence.NewSourceAnalysisArtifactRepository(fixture.database)
	artifactID := uuid.New()
	artifact, err := artifacts.Acquire(ctx, artifactID, fence)
	if err != nil {
		t.Fatalf("acquire artifact for staged execution: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_root SET processing_mode='in_place' WHERE id=?`, fixture.root.ID); err != nil {
		t.Fatal(err)
	}
	execution, err := fixture.repository.GetSourceAnalysisWorkExecution(ctx, fence)
	if err != nil || execution.ProcessingMode != "staged" {
		t.Fatalf("recorded execution after root mode change = %+v, %v; want staged", execution, err)
	}
	if _, err := artifacts.MarkReady(ctx, artifactID, fence, artifact.SourceSizeBytes); err != nil {
		t.Fatalf("mark staged artifact ready after root mode change: %v", err)
	}

	// A retry has a new attempt/job identity and records the then-current mode;
	// the retained artifact remains owned by its original immutable execution.
	retryFence := fence
	retryFence.OperationAttempt++
	retryFence.JobID++
	insertWorkExecution(t, ctx, fixture, retryFence, "in_place")
	retryExecution, err := fixture.repository.GetSourceAnalysisWorkExecution(ctx, retryFence)
	if err != nil || retryExecution.ProcessingMode != "in_place" {
		t.Fatalf("retry execution = %+v, %v; want in_place", retryExecution, err)
	}
	var stored persistence.SourceAnalysisArtifact
	if err := fixture.database.NewSelect().Model(&stored).Where("id = ?", artifactID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if stored.OwnerOperationAttempt != fence.OperationAttempt || stored.OwnerJobID != fence.JobID || stored.OwnerOperationID != fence.OperationID {
		t.Fatalf("retained artifact origin changed after retry: %+v", stored)
	}
}

func TestSourceAnalysisArtifactSettlementKeepsCumulativeCopyForSingleStepRetryWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newNormalizedWorkOperation(t, "/srv/artifact-single-step-settlement", true,
		[]persistence.SourceAnalysisStepInput{
			{Step: persistence.SourceStepSHA256, State: "failed", SafeError: sourceAnalysisArtifactString("prior SHA failure")},
			{Step: persistence.SourceStepProbe, State: "failed", SafeError: sourceAnalysisArtifactString("prior probe failure")},
			{Step: persistence.SourceStepFingerprint, State: "failed", SafeError: sourceAnalysisArtifactString("prior fingerprint failure")},
		})
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_root SET processing_mode='staged' WHERE id=?`, fixture.root.ID); err != nil {
		t.Fatal(err)
	}
	target := fixture.work.ID
	step := string(persistence.SourceStepSHA256)
	operation := normalizedOperation(t, fixture.root, fixture.location, fixture.work,
		persistence.SourceAnalysisModeSingleStep, &target, &step, true, false, nil)
	if err := fixture.admit(t, ctx, operation); err != nil {
		t.Fatalf("admit SHA retry: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `UPDATE operation SET state='running',stage='hashing' WHERE id=?`, operation.ID); err != nil {
		t.Fatal(err)
	}
	fence := persistence.SourceAnalysisArtifactFence{WorkID: fixture.work.ID, OperationID: operation.ID,
		OperationAttempt: operation.Attempt, JobID: *operation.RiverJobID}
	insertWorkExecution(t, ctx, fixture, fence, "staged")
	if _, err := fixture.repository.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
		WorkID: fence.WorkID, OperationID: fence.OperationID, OperationAttempt: fence.OperationAttempt,
		JobID: fence.JobID, Step: persistence.SourceStepSHA256,
	}); err != nil {
		t.Fatalf("claim SHA retry: %v", err)
	}
	artifacts := persistence.NewSourceAnalysisArtifactRepository(fixture.database)
	artifactID := uuid.New()
	artifact, err := artifacts.Acquire(ctx, artifactID, fence)
	if err != nil {
		t.Fatalf("acquire staged artifact: %v", err)
	}
	if _, err := artifacts.MarkReady(ctx, artifactID, fence, artifact.SourceSizeBytes); err != nil {
		t.Fatalf("mark staged artifact ready: %v", err)
	}
	// Prior batch deliveries accumulated all step obligations for this shared copy.
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_analysis_work_artifact_binding SET requested_steps=ARRAY['sha256','probe','fingerprint'] WHERE work_id=?`, fixture.work.ID); err != nil {
		t.Fatal(err)
	}
	digest := make([]byte, 32)
	digest[0] = 1
	resultID := uuid.New()
	if _, err := fixture.database.ExecContext(ctx, `INSERT INTO media_variant
		(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id)
		VALUES (?,?,?,?,?,?)`, resultID, fixture.work.SizeBytes, digest, time.Now().UTC(), "SHA-256", operation.ID); err != nil {
		t.Fatalf("insert successful SHA result: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_analysis_step SET state='succeeded',success_sha_variant_id=?,success_reuse_origin='executed',execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL WHERE work_id=? AND step='sha256'`, resultID, fixture.work.ID); err != nil {
		t.Fatal(err)
	}
	delivery := persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: fence.JobID}
	if err := fixture.repository.SettleNormalizedSourceAnalysisDelivery(ctx, operation.ID, delivery, "succeeded", "completed", ""); err != nil {
		t.Fatalf("settle successful single-step retry: %v", err)
	}
	var stored persistence.SourceAnalysisArtifact
	if err := fixture.database.NewSelect().Model(&stored).Where("id=?", artifactID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if stored.State != persistence.SourceAnalysisArtifactReady {
		t.Fatalf("shared artifact state = %q, want retained ready copy", stored.State)
	}
	var binding struct {
		BorrowerOperationID *uuid.UUID `bun:"borrower_operation_id,type:uuid,nullzero"`
		RequestedSteps      []string   `bun:"requested_steps,array"`
	}
	if err := fixture.database.NewRaw(`SELECT borrower_operation_id,requested_steps FROM source_analysis_work_artifact_binding WHERE work_id=?`, fixture.work.ID).Scan(ctx, &binding); err != nil {
		t.Fatal(err)
	}
	if binding.BorrowerOperationID != nil || strings.Join(binding.RequestedSteps, ",") != "sha256,probe,fingerprint" {
		t.Fatalf("settled artifact binding = borrower %v requested %v; want released borrower and preserved cumulative intent", binding.BorrowerOperationID, binding.RequestedSteps)
	}
	var probe, fingerprint persistence.SourceAnalysisStep
	if err := fixture.database.NewSelect().Model(&probe).Where("work_id=? AND step='probe'", fixture.work.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.NewSelect().Model(&fingerprint).Where("work_id=? AND step='fingerprint'", fixture.work.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if probe.State != "failed" || fingerprint.State != "failed" {
		t.Fatalf("single-step settlement changed failed siblings: probe=%q fingerprint=%q", probe.State, fingerprint.State)
	}
	probeResultID := uuid.New()
	if _, err := fixture.database.ExecContext(ctx, `INSERT INTO media_variant
		(id,size_bytes,ffprobe_version,ffprobe_json,analysis_policy_version,observed_tags,inspected_at,applied_operation_id,audio_stream_count)
		VALUES (?,?,?,?,?,?,?, ?,1)`, probeResultID, fixture.work.SizeBytes, "ffprobe version 7.1.2", `{"format":{"format_name":"flac"}}`, persistence.SourceAnalysisPolicyVersion, `{}`, time.Now().UTC(), operation.ID); err != nil {
		t.Fatalf("insert successful probe result: %v", err)
	}
	fingerprintResultID := uuid.New()
	if _, err := fixture.database.ExecContext(ctx, `INSERT INTO media_fingerprint_result
		(id,winning_result_id,fpcalc_version,version_banner,algorithm_namespace,algorithm_id,fingerprint,reported_duration,calculated_at,applied_operation_id,parser_contract_version)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, fingerprintResultID, fingerprintResultID, "1.5.1", "fpcalc version 1.5.1", "chromaprint", 1, "AQAA", 1.0, time.Now().UTC(), operation.ID, 1); err != nil {
		t.Fatalf("insert successful fingerprint result: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_analysis_step SET state='succeeded',success_probe_variant_id=CASE WHEN step='probe' THEN ? ELSE success_probe_variant_id END,success_fingerprint_result_id=CASE WHEN step='fingerprint' THEN ? ELSE success_fingerprint_result_id END,success_reuse_origin='executed',safe_error=NULL WHERE work_id=? AND step IN ('probe','fingerprint')`, probeResultID, fingerprintResultID, fixture.work.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.NewSelect().Model(&stored).Where("id=?", artifactID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if stored.State != persistence.SourceAnalysisArtifactReady {
		t.Fatalf("completing all accumulated step requests automatically changed artifact to %q", stored.State)
	}
}

func TestSourceAnalysisArtifactRetainedBindingsAccumulateCanonicalRequestedStepsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newNormalizedWorkOperation(t, "/srv/artifact-requested-step-union", true,
		[]persistence.SourceAnalysisStepInput{
			{Step: persistence.SourceStepSHA256, State: "failed", SafeError: sourceAnalysisArtifactString("retry SHA")},
			{Step: persistence.SourceStepProbe, State: "failed", SafeError: sourceAnalysisArtifactString("retry probe")},
			{Step: persistence.SourceStepFingerprint, State: "failed", SafeError: sourceAnalysisArtifactString("retry fingerprint")},
		})
	ffmpegInstallationID := insertAnalysisInstallation(t, ctx, fixture.database, "artifact-requested-step-union")
	fpcalcInstallation := insertAnalysisFPCalcFixture(t, ctx, fixture.database, "artifact-requested-step-union-fpcalc", "1.5.1")
	probeTool := normalizedToolSelection(t, ctx, fixture.database, ffmpegInstallationID, "ffprobe")
	fingerprintTool := normalizedToolSelection(t, ctx, fixture.database, fpcalcInstallation.ID, "fpcalc")
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_root SET processing_mode='staged' WHERE id=?`, fixture.root.ID); err != nil {
		t.Fatal(err)
	}
	artifacts := persistence.NewSourceAnalysisArtifactRepository(fixture.database)
	var artifactID uuid.UUID
	var creatorOperationID uuid.UUID
	for index, step := range []persistence.SourceStepName{
		persistence.SourceStepFingerprint,
		persistence.SourceStepProbe,
		persistence.SourceStepSHA256,
	} {
		target := fixture.work.ID
		stepValue := string(step)
		var selectedTools []persistence.SourceAnalysisToolSelection
		switch step {
		case persistence.SourceStepProbe:
			selectedTools = []persistence.SourceAnalysisToolSelection{probeTool}
		case persistence.SourceStepFingerprint:
			selectedTools = []persistence.SourceAnalysisToolSelection{fingerprintTool}
		}
		operation := normalizedOperation(t, fixture.root, fixture.location, fixture.work,
			persistence.SourceAnalysisModeSingleStep, &target, &stepValue, true, false, selectedTools)
		if err := fixture.admit(t, ctx, operation); err != nil {
			t.Fatalf("admit %s retry: %v", step, err)
		}
		if _, err := fixture.database.ExecContext(ctx, `UPDATE operation SET state='running',stage='analysis',started_at=now() WHERE id=?`, operation.ID); err != nil {
			t.Fatal(err)
		}
		fence := persistence.SourceAnalysisArtifactFence{WorkID: fixture.work.ID, OperationID: operation.ID,
			OperationAttempt: operation.Attempt, JobID: *operation.RiverJobID}
		insertWorkExecution(t, ctx, fixture, fence, "staged")
		if _, err := fixture.repository.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
			WorkID: fence.WorkID, OperationID: fence.OperationID, OperationAttempt: fence.OperationAttempt,
			JobID: fence.JobID, Step: step,
		}); err != nil {
			t.Fatalf("claim %s retry: %v", step, err)
		}

		if index == 0 {
			artifactID = uuid.New()
			creatorOperationID = operation.ID
			artifact, err := artifacts.Acquire(ctx, artifactID, fence)
			if err != nil {
				t.Fatalf("acquire staged artifact for %s: %v", step, err)
			}
			if _, err := artifacts.MarkReady(ctx, artifactID, fence, artifact.SourceSizeBytes); err != nil {
				t.Fatalf("mark artifact ready: %v", err)
			}
		} else {
			binding, err := artifacts.GetBinding(ctx, fixture.work.ID)
			if err != nil || binding == nil || binding.ID != artifactID {
				t.Fatalf("retained binding before %s = %+v, %v", step, binding, err)
			}
			retained, err := artifacts.ListRetained(ctx, fixture.work.ID)
			if err != nil || len(retained) != 1 || retained[0].ID != artifactID {
				t.Fatalf("retained artifacts before %s = %+v, %v", step, retained, err)
			}
			bound, err := artifacts.BindRetained(ctx, artifactID, fence)
			if err != nil || bound == nil || bound.ID != artifactID || !bound.RequestedStepsKnown {
				t.Fatalf("bind retained artifact for %s = %+v, %v", step, bound, err)
			}
		}
		if err := fixture.repository.SettleNormalizedSourceAnalysisDelivery(ctx, operation.ID,
			persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: fence.JobID},
			"failed", "retryable", "Fixture retry remains incomplete."); err != nil {
			t.Fatalf("settle %s retry: %v", step, err)
		}
	}

	var stored persistence.SourceAnalysisArtifact
	if err := fixture.database.NewSelect().Model(&stored).Where("id=?", artifactID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"sha256", "probe", "fingerprint"}
	if !stored.RequestedStepsKnown || strings.Join(stored.RequestedSteps, ",") != strings.Join(want, ",") {
		t.Fatalf("retained artifact steps = %v (known %t), want canonical %v", stored.RequestedSteps, stored.RequestedStepsKnown, want)
	}
	if stored.OwnerOperationID != creatorOperationID || stored.State != persistence.SourceAnalysisArtifactReady {
		t.Fatalf("retained artifact ownership/state = %s/%s; want original owner %s and ready state", stored.OwnerOperationID, stored.State, creatorOperationID)
	}
	var binding struct {
		RequestedSteps []string `bun:"requested_steps,array"`
	}
	if err := fixture.database.NewRaw(`SELECT requested_steps FROM source_analysis_work_artifact_binding WHERE work_id=?`, fixture.work.ID).Scan(ctx, &binding); err != nil {
		t.Fatal(err)
	}
	if strings.Join(binding.RequestedSteps, ",") != strings.Join(want, ",") {
		t.Fatalf("retained binding steps = %v, want %v", binding.RequestedSteps, want)
	}
}

func sourceAnalysisArtifactString(value string) *string { return &value }

func insertWorkExecution(t *testing.T, ctx context.Context, fixture normalizedAnalysisFixture, fence persistence.SourceAnalysisArtifactFence, mode string) {
	t.Helper()
	if _, err := fixture.database.ExecContext(ctx, `INSERT INTO source_analysis_work_execution
		(work_id,operation_id,operation_attempt,job_id,processing_mode) VALUES (?,?,?,?,?)`,
		fence.WorkID, fence.OperationID, fence.OperationAttempt, fence.JobID, mode); err != nil {
		t.Fatalf("insert execution registry fixture: %v", err)
	}
}
