//go:build integration

package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// TestSourceAnalysisWorkerRiverDispatchPostgreSQL drives the analysis worker
// through the real River dispatcher and real PostgreSQL: a delivery probes,
// applies and succeeds with both holds released; a duplicate delivery runs no
// second probe; a pinned installation that is gone fails the analysis with the
// previous variant untouched and both holds released; a successful retry after a
// transient tool failure replaces the variant; a fresh analysis that already
// replaced the variant makes an older failed retry a conflict; and an apply
// whose terminal update fails leaves the previous variant linked.
func TestSourceAnalysisWorkerRiverDispatchPostgreSQL(t *testing.T) {
	fixture := newAnalysisDispatchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Given an analyzed file, a successful delivery commits a variant and clears
	// both holds.
	first := fixture.start(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *first.RiverJobID)
	assertOperationStage(t, ctx, fixture.setup, first.ID, "succeeded", service.SourceAnalysisStageApplying)
	firstVariant := fixture.requireLinkedVariant(t, ctx)
	requireAnalysisHolds(t, fixture.readOperation(t, ctx, first.ID), nil, nil)
	if variants := fixture.countVariants(t, ctx); variants != 1 {
		t.Fatalf("media variants after the first analysis = %d, want 1", variants)
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

	// A missing managed ffprobe fails the queued analysis with a safe reason and
	// releases both holds; the previous variant stays linked.
	fixture.removeFFProbe(t)
	failed := fixture.start(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *failed.RiverJobID)
	assertOperationStage(t, ctx, fixture.setup, failed.ID, "failed", service.SourceAnalysisStageProbing)
	requireScanDispatchSafeError(t, fixture.readOperation(t, ctx, failed.ID), analysisSafeTool)
	requireAnalysisHolds(t, fixture.readOperation(t, ctx, failed.ID), nil, nil)
	if fixture.requireLinkedVariant(t, ctx) != firstVariant {
		t.Fatal("a failed analysis changed the linked variant")
	}
	if variants := fixture.countVariants(t, ctx); variants != 1 {
		t.Fatalf("media variants after the failure = %d, want the previous one alone", variants)
	}
	fixture.restoreFFProbe(t)

	// A fresh successful analysis of the unchanged file replaces the variant: the
	// old variant is unlinked, unheld and removed as an orphan.
	replacement := fixture.start(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *replacement.RiverJobID)
	assertOperationStage(t, ctx, fixture.setup, replacement.ID, "succeeded", service.SourceAnalysisStageApplying)
	replacementVariant := fixture.requireLinkedVariant(t, ctx)
	if replacementVariant == firstVariant {
		t.Fatal("the second analysis reused the first variant")
	}
	if variants := fixture.countVariants(t, ctx); variants != 1 {
		t.Fatalf("media variants after the replacement = %d, want the orphan removed", variants)
	}

	// Retrying the old failure now conflicts: its immutable snapshot still pins
	// the variant the location no longer carries. Nothing changes.
	jobsBeforeRetry := fixture.countJobs(t, ctx, failed.ID)
	if _, err := fixture.operations.Retry(ctx, failed.ID); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("retry after the variant was replaced = %v, want %v", err, persistence.ErrSourceAnalysisStale)
	}
	assertOperationStage(t, ctx, fixture.setup, failed.ID, "failed", service.SourceAnalysisStageProbing)
	requireAnalysisHolds(t, fixture.readOperation(t, ctx, failed.ID), nil, nil)
	if jobs := fixture.countJobs(t, ctx, failed.ID); jobs != jobsBeforeRetry {
		t.Fatalf("River jobs for the refused retry = %d, want the unchanged %d", jobs, jobsBeforeRetry)
	}

	// A retry that the tool made impossible and can now succeed replaces the
	// variant and clears both holds again.
	fixture.removeFFProbe(t)
	retryable := fixture.start(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *retryable.RiverJobID)
	requireScanDispatchSafeError(t, fixture.readOperation(t, ctx, retryable.ID), analysisSafeTool)
	fixture.restoreFFProbe(t)
	retried, err := fixture.operations.Retry(ctx, retryable.ID)
	if err != nil {
		t.Fatalf("retry the transient tool failure: %v", err)
	}
	awaitRiverCompletion(t, ctx, fixture.events, *retried.RiverJobID)
	assertOperationStage(t, ctx, fixture.setup, retryable.ID, "succeeded", service.SourceAnalysisStageApplying)
	finalVariant := fixture.requireLinkedVariant(t, ctx)
	if finalVariant == replacementVariant {
		t.Fatal("the retry reused the variant of the analysis it superseded")
	}
	requireAnalysisHolds(t, fixture.readOperation(t, ctx, retryable.ID), nil, nil)
	if variants := fixture.countVariants(t, ctx); variants != 1 {
		t.Fatalf("media variants after the retry = %d, want the superseded orphan removed", variants)
	}

	// A fresh analysis whose apply terminal update fails rolls the whole apply
	// back: the previous variant stays linked and both holds are released.
	installFailingAnalysisApplyTrigger(t, ctx, fixture.database)
	applyFailure := fixture.start(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *applyFailure.RiverJobID)
	assertOperationStage(t, ctx, fixture.setup, applyFailure.ID, "failed", service.SourceAnalysisStageApplying)
	requireScanDispatchSafeError(t, fixture.readOperation(t, ctx, applyFailure.ID), analysisSafeApply)
	requireAnalysisHolds(t, fixture.readOperation(t, ctx, applyFailure.ID), nil, nil)
	if fixture.requireLinkedVariant(t, ctx) != finalVariant {
		t.Fatal("a failed apply changed the linked variant")
	}
	if variants := fixture.countVariants(t, ctx); variants != 1 {
		t.Fatalf("media variants after the failed apply = %d, want the previous one alone", variants)
	}
	dropFailingAnalysisApplyTrigger(t, ctx, fixture.database)

	// A deleted pinned installation makes the failed analysis impossible to
	// retry; the refusal leaves the terminal state, its attempt and its holds. The
	// managed tool is removed before the job is queued, so the worker fails on the
	// missing installation instead of racing the delivery.
	fixture.removeFFProbe(t)
	deleted := fixture.start(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *deleted.RiverJobID)
	requireScanDispatchSafeError(t, fixture.readOperation(t, ctx, deleted.ID), analysisSafeTool)
	beforeAttempt := fixture.readOperation(t, ctx, deleted.ID).Attempt
	jobsBeforeDeleteRetry := fixture.countJobs(t, ctx, deleted.ID)
	if _, err := fixture.database.ExecContext(ctx, "DELETE FROM tool_installation WHERE id = ?", fixture.installationID); err != nil {
		t.Fatalf("delete the pinned installation: %v", err)
	}
	if _, err := fixture.operations.Retry(ctx, deleted.ID); !errors.Is(err, persistence.ErrSourceAnalysisInstallationUnusable) {
		t.Fatalf("retry with a deleted installation = %v, want %v", err, persistence.ErrSourceAnalysisInstallationUnusable)
	}
	assertOperationStage(t, ctx, fixture.setup, deleted.ID, "failed", service.SourceAnalysisStageProbing)
	after := fixture.readOperation(t, ctx, deleted.ID)
	requireAnalysisHolds(t, after, nil, nil)
	if after.Attempt != beforeAttempt {
		t.Fatalf("refused retry attempt = %d, want the unchanged %d", after.Attempt, beforeAttempt)
	}
	if jobs := fixture.countJobs(t, ctx, deleted.ID); jobs != jobsBeforeDeleteRetry {
		t.Fatalf("River jobs for the refused retry = %d, want the unchanged %d", jobs, jobsBeforeDeleteRetry)
	}
}
