package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// The safe reasons a failed source analysis reports. An operator reads one of
// them on the operation; none of them carries a path, an ffprobe diagnostic or
// any other internal detail.
const (
	analysisSafeInvalidInput = "The source analysis input is invalid. Start a new analysis."
	analysisSafeRootGone     = "The source root no longer exists."
	analysisSafeDisabled     = "The source root is disabled. Enable it before analyzing it again."
	analysisSafeNotReady     = "The source analysis requires a completed setup on a supported server platform."
	analysisSafeStale        = "The source file or its inventory changed since the analysis was queued. Start a new analysis."
	analysisSafeTool         = "The managed ffprobe is unavailable or failed verification. Repair the managed tools and retry the analysis."
	analysisSafeProbe        = "The source file could not be analyzed. The previous result is unchanged."
	analysisSafeApply        = "The verified analysis could not be saved. The previous result is unchanged."
	// analysisSafeInterrupted is the reason startup recovery records for an
	// analysis whose process stopped before its result was committed: the
	// previous result stays linked and the operation stays retryable.
	analysisSafeInterrupted = "The source analysis was interrupted. Retry the analysis."
)

// analysisWorkerRepository is the persistence contract of an analysis worker:
// the read-only analysis inputs, the atomic apply of the prepared result, the
// operation it reloads and the terminal failure that releases both holds.
type analysisWorkerRepository interface {
	service.SourceAnalysisRepository
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	ApplyAnalysisResult(context.Context, persistence.SourceAnalysisApply) (*persistence.Operation, error)
	FailSourceAnalysisOperation(context.Context, uuid.UUID, string, string) error
}

// analysisWorkerSettings is the runtime state an analysis reloads before it
// probes: the completed Setup and the managed tools directory the pinned
// installation is read from.
type analysisWorkerSettings interface {
	SetupCompleted(context.Context) (bool, error)
	GetToolsDirectory(context.Context) (string, bool, error)
}

// SourceAnalysisWorker runs one queued technical analysis of a source location.
// The River job carries only the durable operation ID; the worker reloads the
// immutable snapshot, the root and the location, so a job never carries an input
// that could contradict the stored record. It resolves the executable from the
// managed installation the snapshot pins, never from PATH, and never accepts a
// path from the request.
//
// A delivery of an operation a previous delivery already committed finishes
// without a fresh version query, probe or source read. A failure before the
// apply leaves the previous variant linked and releases both read holds in one
// transaction with the terminal state, so no failed analysis keeps a hold.
type SourceAnalysisWorker struct {
	river.WorkerDefaults[service.SourceAnalysisJobArgs]
	repository      analysisWorkerRepository
	operations      *service.Operations
	toolsDirectory  service.ToolsDirectoryReader
	runtimeSettings analysisWorkerSettings
	platform        settings.PlatformState
}

func NewSourceAnalysisWorker(repository analysisWorkerRepository, operations *service.Operations, toolsDirectory service.ToolsDirectoryReader, runtimeSettings analysisWorkerSettings, platform settings.PlatformState) *SourceAnalysisWorker {
	return &SourceAnalysisWorker{
		repository: repository, operations: operations, toolsDirectory: toolsDirectory,
		runtimeSettings: runtimeSettings, platform: platform,
	}
}

// Work revalidates the analysis preconditions, probes the pinned file once and
// commits the prepared result and the terminal state in the apply's single
// transaction. The commit returns the succeeded operation, and only then is the
// wake-up notification sent, so a subscriber that reacts can already read the
// committed result. A failure preserves the previous variant exactly as it was
// and fails the operation with a safe reason while releasing both read holds.
func (worker *SourceAnalysisWorker) Work(ctx context.Context, job *river.Job[service.SourceAnalysisJobArgs]) error {
	operation, err := worker.repository.GetOperation(ctx, job.Args.OperationID)
	if err != nil {
		return err
	}
	if operation.Kind != service.SourceAnalysisOperationKind {
		return fmt.Errorf("operation %s is not a source analysis", operation.ID)
	}
	// A duplicate delivery of an operation that already reached a final state
	// must not run a fresh version query, probe or source read.
	if operation.State == "succeeded" || operation.State == "failed" {
		return nil
	}
	snapshot, err := persistence.DecodeSourceAnalysisSnapshot(operation.InputSnapshot)
	if err != nil || operation.TargetSourceRootID == nil || *operation.TargetSourceRootID != snapshot.SourceRootID ||
		operation.TargetSourceLocationID == nil || *operation.TargetSourceLocationID != snapshot.SourceLocationID {
		slog.Warn("source analysis input is invalid", "operation", operation.ID.String(), "cause", err)
		return worker.fail(ctx, operation, service.SourceAnalysisStageQueued, analysisSafeInvalidInput)
	}
	root, err := worker.repository.GetSourceRoot(ctx, snapshot.SourceRootID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return worker.fail(ctx, operation, service.SourceAnalysisStageQueued, analysisSafeRootGone)
		}
		return err
	}
	if !root.Enabled {
		return worker.fail(ctx, operation, service.SourceAnalysisStageQueued, analysisSafeDisabled)
	}
	if root.Stale() || root.ConfiguredPath != snapshot.ConfiguredPath ||
		root.InventoryPath == nil || *root.InventoryPath != snapshot.InventoryPath {
		return worker.fail(ctx, operation, service.SourceAnalysisStageQueued, analysisSafeStale)
	}
	if err := worker.ready(ctx); err != nil {
		slog.Warn("source analysis cannot start", "operation", operation.ID.String(), "cause", err)
		return worker.fail(ctx, operation, service.SourceAnalysisStageQueued, analysisSafeNotReady)
	}
	if err := worker.operations.Running(ctx, operation.ID, service.SourceAnalysisStageProbing); err != nil {
		return err
	}
	analysis := service.NewSourceAnalysis(worker.repository, worker.toolsDirectory, worker.platform.Platform)
	apply, err := analysis.Run(ctx, service.SourceAnalysisRequest{OperationID: operation.ID, Snapshot: snapshot})
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		slog.Warn("source analysis failed", "operation", operation.ID.String(), "cause", err)
		return worker.fail(ctx, operation, service.SourceAnalysisStageProbing, analysisRunSafe(err))
	}
	if err := worker.operations.Running(ctx, operation.ID, service.SourceAnalysisStageApplying); err != nil {
		return err
	}
	committed, err := worker.repository.ApplyAnalysisResult(ctx, apply)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		slog.Warn("source analysis apply failed", "operation", operation.ID.String(), "cause", err)
		if errors.Is(err, persistence.ErrSourceAnalysisStale) {
			return worker.fail(ctx, operation, service.SourceAnalysisStageApplying, analysisSafeStale)
		}
		return worker.fail(ctx, operation, service.SourceAnalysisStageApplying, analysisSafeApply)
	}
	// The result, the location link and the terminal state are durable now; the
	// notification is a wake-up and follows the commit.
	worker.operations.Notify(committed.ID)
	return nil
}

// ready reports whether the instance can probe a source at all: an unusable
// platform has no managed tools to read a source with, and an unfinished Setup
// has not verified them yet.
func (worker *SourceAnalysisWorker) ready(ctx context.Context) error {
	if worker.platform.Diagnostic || !worker.platform.Platform.Supported() {
		return fmt.Errorf("the instance platform is not usable: %s", worker.platform.Reason)
	}
	completed, err := worker.runtimeSettings.SetupCompleted(ctx)
	if err != nil {
		return fmt.Errorf("read setup completion: %w", err)
	}
	if !completed {
		return fmt.Errorf("setup is not complete")
	}
	return nil
}

// fail releases both read holds and records the safe reason in one transaction
// and, only after that commit, sends the same wake-up a successful commit sends,
// so a client waiting on the operation's final notification is woken even when
// the analysis failed. The wake carries no data: the subscriber re-reads the
// committed operation and sees the failed state with both holds released. A
// cancellation that outlived the failure leaves the operation to the River
// retry instead of recording a terminal state from a dead context.
func (worker *SourceAnalysisWorker) fail(ctx context.Context, operation *persistence.Operation, stage, safe string) error {
	if err := worker.repository.FailSourceAnalysisOperation(ctx, operation.ID, stage, safe); err != nil {
		return err
	}
	worker.operations.Notify(operation.ID)
	return nil
}

// analysisRunSafe classifies a failed analysis into the safe reason an operator
// reads, without leaking a path, an ffprobe diagnostic or a tool error.
func analysisRunSafe(err error) string {
	switch {
	case errors.Is(err, service.ErrSourceAnalysisToolUnavailable):
		return analysisSafeTool
	case errors.Is(err, persistence.ErrSourceAnalysisStale):
		return analysisSafeStale
	default:
		return analysisSafeProbe
	}
}

var _ river.Worker[service.SourceAnalysisJobArgs] = (*SourceAnalysisWorker)(nil)
