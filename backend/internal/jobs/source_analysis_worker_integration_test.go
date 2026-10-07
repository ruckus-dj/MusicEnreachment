//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

const analysisSafeProbe = "managed ffprobe is unavailable"

// TestSourceAnalysisWorkerRiverDispatchPostgreSQL drives the analysis worker
// through the real River dispatcher and real PostgreSQL: a delivery probes,
// applies and succeeds with its work and tool holds released; a duplicate delivery runs no
// second probe; a pinned installation that is gone fails the analysis with the
// previous variant untouched and both holds released; a successful retry after a
// transient tool failure replaces the variant; a fresh analysis that already
// replaced the variant makes an older failed retry a conflict; and an apply
// whose terminal update fails leaves the previous variant linked.
func TestSourceAnalysisWorkerRiverDispatchPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAnalysisDispatchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Given an analyzed file, a successful delivery commits a variant and clears
	// its work and managed-tool read holds.
	first := fixture.start(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *first.RiverJobID)
	assertOperationStage(t, ctx, fixture.setup, first.ID, "succeeded", service.SourceAnalysisStageApplying)
	firstVariant := fixture.requireLinkedVariant(t, ctx)
	requireLinkedCanonicalSHAIdentity(t, ctx, fixture, firstVariant)
	requireAnalysisHolds(t, ctx, fixture, first.ID, nil, nil)
	if variants := fixture.countVariants(t, ctx); variants != 1 {
		t.Fatalf("media variants after the first analysis = %d, want the canonical SHA/probe identity", variants)
	}
	probesAfterFirst := scanDispatchProbeCount(t, fixture.probeLog)

	// A duplicate delivery of the already succeeded operation is a no-op.
	awaitRiverCompletion(t, ctx, fixture.events, fixture.deliver(t, ctx, first.ID))
	assertOperationStage(t, ctx, fixture.setup, first.ID, "succeeded", service.SourceAnalysisStageApplying)
	if variants := fixture.countVariants(t, ctx); variants != 1 || fixture.requireLinkedVariant(t, ctx) != firstVariant {
		t.Fatal("a duplicate delivery rewrote the committed result")
	}
	if probes := scanDispatchProbeCount(t, fixture.probeLog); probes != probesAfterFirst {
		t.Fatalf("probes after the duplicate delivery = %d, want no second probe", probes)
	}

	// A missing managed ffprobe fails only the probe step; batch bookkeeping still
	// succeeds and the successful new SHA identity is committed independently.
	fixture.changeSourceBytes(t, ctx, "fresh bytes without a probe cache")
	fixture.removeFFProbe(t)
	failed := fixture.start(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *failed.RiverJobID)
	assertOperationStage(t, ctx, fixture.setup, failed.ID, "succeeded", service.SourceAnalysisStageApplying)
	requireAnalysisStepSafeError(t, ctx, fixture, failed.ID, persistence.SourceStepProbe, analysisSafeProbe)
	requireAnalysisHolds(t, ctx, fixture, failed.ID, nil, nil)
	failedVariant := fixture.requireLinkedVariant(t, ctx)
	var failedSHA uuid.UUID
	if err := fixture.database.NewRaw(`SELECT success_sha_variant_id FROM source_analysis_step WHERE work_id=? AND step='sha256'`, fixture.work.ID).Scan(ctx, &failedSHA); err != nil {
		t.Fatalf("read SHA identity after failed probe: %v", err)
	}
	if failedVariant != failedSHA || failedVariant == firstVariant {
		t.Fatalf("linked variant after failed probe = %s, want fresh SHA identity %s, not previous %s", failedVariant, failedSHA, firstVariant)
	}
	if variants := fixture.countVariants(t, ctx); variants != 2 {
		t.Fatalf("media variants after the failure = %d, want previous probe and fresh SHA identities", variants)
	}
	fixture.restoreFFProbe(t)

	// A fresh successful analysis of the unchanged file promotes the probe result
	// into the already canonical digest identity.
	replacement := fixture.start(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *replacement.RiverJobID)
	assertOperationStage(t, ctx, fixture.setup, replacement.ID, "succeeded", service.SourceAnalysisStageApplying)
	replacementVariant := fixture.requireLinkedVariant(t, ctx)
	requireLinkedCanonicalSHAIdentity(t, ctx, fixture, replacementVariant)
	if variants := fixture.countVariants(t, ctx); variants != 2 {
		t.Fatalf("media variants after the replacement = %d, want the canonical SHA/probe identity", variants)
	}

	// Batch operations are not retried as a whole after one step failed; the
	// failed step remains independently retryable.
	jobsBeforeRetry := fixture.countJobs(t, ctx, failed.ID)
	if _, err := fixture.operations.Retry(ctx, failed.ID); err == nil || err.Error() != "only failed operations can be retried" {
		t.Fatalf("retry of a succeeded batch = %v, want failed-operation refusal", err)
	}
	assertOperationStage(t, ctx, fixture.setup, failed.ID, "succeeded", service.SourceAnalysisStageApplying)
	requireAnalysisHolds(t, ctx, fixture, failed.ID, nil, nil)
	if jobs := fixture.countJobs(t, ctx, failed.ID); jobs != jobsBeforeRetry {
		t.Fatalf("River jobs for the refused retry = %d, want the unchanged %d", jobs, jobsBeforeRetry)
	}

	// A missing-tool batch still succeeds while retaining a failed probe step. Once
	// the managed executable is restored, retry that step through the step API.
	fixture.changeSourceBytes(t, ctx, "fresh bytes for explicit step retry")
	fixture.removeFFProbe(t)
	retryable := fixture.start(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *retryable.RiverJobID)
	assertOperationStage(t, ctx, fixture.setup, retryable.ID, "succeeded", service.SourceAnalysisStageApplying)
	requireAnalysisStepSafeError(t, ctx, fixture, retryable.ID, persistence.SourceStepProbe, analysisSafeProbe)
	fixture.restoreFFProbe(t)
	analysis := service.NewSourceAnalysisOperations(
		persistence.NewSourceAnalysisStartStore(fixture.database), fixture.registry, fixture.registry, fixture.platform, fixture.client,
	)
	retried, err := analysis.RetryStep(ctx, service.SourceAnalysisStepRequest{
		RootID: fixture.root.ID, LocationID: fixture.track.ID, Step: persistence.SourceStepProbe,
		ExpectedSizeBytes: fixture.track.SizeBytes, ExpectedMtime: fixture.track.Mtime,
	})
	if err != nil {
		t.Fatalf("retry the transient tool failure: %v", err)
	}
	awaitRiverCompletion(t, ctx, fixture.events, *retried.RiverJobID)
	assertOperationStage(t, ctx, fixture.setup, retried.ID, "succeeded", service.SourceAnalysisStageApplying)
	finalVariant := fixture.requireLinkedVariant(t, ctx)
	requireLinkedCanonicalSHAIdentity(t, ctx, fixture, finalVariant)
	requireAnalysisHolds(t, ctx, fixture, retried.ID, nil, nil)
	if variants := fixture.countVariants(t, ctx); variants != 3 {
		t.Fatalf("media variants after the retry = %d, want the canonical SHA/probe identity", variants)
	}

	// A terminal bookkeeping failure is recovered on redelivery. Previously
	// committed results remain linked and both holds are released.
	installFailingAnalysisApplyTrigger(t, ctx, fixture.database)
	applyFailure := fixture.start(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *applyFailure.RiverJobID)
	assertOperationStage(t, ctx, fixture.setup, applyFailure.ID, "failed", "recovered")
	if operation := fixture.readOperation(t, ctx, applyFailure.ID); operation.SafeError == nil || *operation.SafeError == "" {
		t.Fatal("a failed apply has no safe operation error")
	}
	requireAnalysisHolds(t, ctx, fixture, applyFailure.ID, nil, nil)
	if fixture.requireLinkedVariant(t, ctx) != finalVariant {
		t.Fatal("a failed apply changed the linked variant")
	}
	if variants := fixture.countVariants(t, ctx); variants != 3 {
		t.Fatalf("media variants after the failed apply = %d, want the canonical SHA/probe identity", variants)
	}
	dropFailingAnalysisApplyTrigger(t, ctx, fixture.database)

	// A deleted active installation makes the failed step impossible to retry; the
	// refusal leaves the succeeded batch state, its attempt and its holds. The
	// managed tool is removed before the job is queued, so the worker fails on the
	// missing installation instead of racing the delivery.
	fixture.changeSourceBytes(t, ctx, "fresh bytes for deleted-installation refusal")
	fixture.removeFFProbe(t)
	deleted := fixture.start(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *deleted.RiverJobID)
	assertOperationStage(t, ctx, fixture.setup, deleted.ID, "succeeded", service.SourceAnalysisStageApplying)
	requireAnalysisStepSafeError(t, ctx, fixture, deleted.ID, persistence.SourceStepProbe, analysisSafeProbe)
	beforeAttempt := fixture.readOperation(t, ctx, deleted.ID).Attempt
	jobsBeforeDeleteRetry := fixture.countJobs(t, ctx, deleted.ID)
	if _, err := fixture.database.ExecContext(ctx, "DELETE FROM tool_installation WHERE id = ?", fixture.installationID); err != nil {
		t.Fatalf("delete the pinned installation: %v", err)
	}
	if _, err := analysis.RetryStep(ctx, service.SourceAnalysisStepRequest{
		RootID: fixture.root.ID, LocationID: fixture.track.ID, Step: persistence.SourceStepProbe,
		ExpectedSizeBytes: fixture.track.SizeBytes, ExpectedMtime: fixture.track.Mtime,
	}); !errors.Is(err, service.ErrSourceAnalysisToolUnavailable) {
		t.Fatalf("retry with a deleted installation = %v, want %v", err, service.ErrSourceAnalysisToolUnavailable)
	}
	assertOperationStage(t, ctx, fixture.setup, deleted.ID, "succeeded", service.SourceAnalysisStageApplying)
	after := fixture.readOperation(t, ctx, deleted.ID)
	requireAnalysisHolds(t, ctx, fixture, deleted.ID, nil, nil)
	if after.Attempt != beforeAttempt {
		t.Fatalf("refused retry attempt = %d, want the unchanged %d", after.Attempt, beforeAttempt)
	}
	if jobs := fixture.countJobs(t, ctx, deleted.ID); jobs != jobsBeforeDeleteRetry {
		t.Fatalf("River jobs for the refused retry = %d, want the unchanged %d", jobs, jobsBeforeDeleteRetry)
	}
}

func requireLinkedCanonicalSHAIdentity(t *testing.T, ctx context.Context, fixture analysisDispatchFixture, linked uuid.UUID) {
	t.Helper()
	var canonical uuid.UUID
	if err := fixture.database.NewRaw(`SELECT success_sha_variant_id FROM source_analysis_step
		WHERE work_id = ? AND step = 'sha256'`, fixture.work.ID).Scan(ctx, &canonical); err != nil {
		t.Fatalf("read canonical SHA identity: %v", err)
	}
	if canonical != linked {
		t.Fatalf("linked variant identity = %s, want canonical SHA identity %s", linked, canonical)
	}
}

func requireAnalysisStepSafeError(t *testing.T, ctx context.Context, fixture analysisDispatchFixture, operationID uuid.UUID, step persistence.SourceStepName, want string) {
	t.Helper()
	operation := fixture.readOperation(t, ctx, operationID)
	var snapshot persistence.SourceAnalysisOperationSnapshot
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
		t.Fatalf("decode analysis snapshot: %v", err)
	}
	for _, workID := range snapshot.WorkIDs {
		var state string
		var safeError *string
		err := fixture.database.NewRaw("SELECT state, safe_error FROM source_analysis_step WHERE work_id = ? AND step = ?", workID, string(step)).Scan(ctx, &state, &safeError)
		if err != nil {
			t.Fatalf("read %s step result: %v", step, err)
		}
		if state == "failed" {
			if safeError == nil || *safeError != want {
				got := "<nil>"
				if safeError != nil {
					got = *safeError
				}
				t.Fatalf("%s step safe error = %q, want %q", step, got, want)
			}
			return
		}
	}
	t.Fatalf("operation %s has no failed %s step", operationID, step)
}
