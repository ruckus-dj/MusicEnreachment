package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// The safe reasons a failed source scan reports. An operator reads one of them
// on the operation, and none of them carries a path, an ffprobe diagnostic or
// any other internal detail.
const (
	scanSafeInvalidInput = "The source scan input is invalid. Start a new scan."
	scanSafeRootGone     = "The source root no longer exists."
	scanSafeDisabled     = "The source root is disabled. Enable it before scanning it again."
	scanSafePath         = service.SourceScanDirectoryUnavailableReason
	scanSafeUnsupported  = "Windows UNC source roots are not supported yet. Configure a local drive path."
	scanSafeTool         = "The managed ffprobe is unavailable or failed verification. Repair the managed tools and retry the scan."
	scanSafeTraversal    = "The source directory could not be read completely. The previous inventory is unchanged."
	scanSafeApply        = "The verified scan could not be applied. The previous inventory is unchanged."
	// scanSafeInterrupted is the reason startup recovery records for a scan whose
	// process stopped before its generation was applied: the operation stays
	// retryable and the previous inventory is unchanged.
	scanSafeInterrupted = "The source scan was interrupted. Retry the scan."
)

// scanSucceededStage is the terminal stage of a scan whose generation is
// installed. The worker and startup recovery both report it, so a finished scan
// reads the same whether the process that applied it survived to say so.
const scanSucceededStage = "succeeded"

// scanWorkerRepository is the persistence contract of a scan worker: the
// traversal contract of the scan itself, plus the operation, the active tool
// installation and the atomic apply the worker drives after the traversal.
type scanWorkerRepository interface {
	service.SourceScanRepository
	scanDeliveryRepository
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	ApplySourceEnumeration(context.Context, persistence.SourceEnumerationApply) error
	MarkSourceRootUnavailable(context.Context, persistence.SourceScanUnavailable) error
}

type scanDeliveryRepository interface {
	StartSourceScanDelivery(context.Context, uuid.UUID, int, int64) error
	SetSourceScanDeliveryStage(context.Context, uuid.UUID, int, int64, string) error
	FinishSourceScanDelivery(context.Context, uuid.UUID, int, int64, string, string, string) error
}

// scanWorkerSettings stays in the constructor contract for composition
// compatibility. Enumeration does not read Setup or tool settings.
type scanWorkerSettings interface {
	SetupCompleted(context.Context) (bool, error)
	GetToolsDirectory(context.Context) (string, bool, error)
	ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error)
}

type scanSHA256SettingReader interface {
	GetSHA256Enabled(context.Context) (bool, error)
}

type sourceAnalysisPendingDispatcher interface {
	AdmitPending(context.Context, uuid.UUID) error
}

// SourceScanWorker runs one queued scan of a source root. The River job carries
// only the durable operation ID; the worker reloads the snapshot, the root, the
// platform and the managed tools, so a job never carries an input that could
// contradict the stored record. Every precondition is re-checked here, after
// the enqueue that created the operation, because the root can be edited between
// the two.
type SourceScanWorker struct {
	river.WorkerDefaults[service.ScanSourceJobArgs]
	repository scanWorkerRepository
	operations *service.Operations
	paths      service.SourceScanPathValidator
	settings   scanWorkerSettings
	platform   settings.PlatformState
	pending    sourceAnalysisPendingDispatcher
}

// SetPendingDispatcher installs the best-effort handoff that starts analysis
// work made pending by a newly applied scan generation.
func (worker *SourceScanWorker) SetPendingDispatcher(dispatcher sourceAnalysisPendingDispatcher) {
	worker.pending = dispatcher
}

func NewSourceScanWorker(repository scanWorkerRepository, operations *service.Operations, paths service.SourceScanPathValidator, runtimeSettings scanWorkerSettings, platform settings.PlatformState, _ *tools.Lifecycle) *SourceScanWorker {
	return &SourceScanWorker{
		repository: repository, operations: operations, paths: paths,
		settings: runtimeSettings, platform: platform,
	}
}

// Work revalidates every precondition, walks the root, applies the verified
// snapshot in one transaction and marks the operation succeeded only after that
// commit. A delivery of an operation whose generation a previous delivery already
// applied finishes the operation without walking or applying it again. A failure
// before the traversal and a failed traversal or apply both leave the previous
// inventory untouched and fail the operation with a safe reason; the candidates
// of the failed attempt are dropped, so no partial snapshot survives it.
func (worker *SourceScanWorker) Work(ctx context.Context, job *river.Job[service.ScanSourceJobArgs]) error {
	operation, err := worker.repository.GetOperation(ctx, job.Args.OperationID)
	if err != nil {
		return err
	}
	if operation.Kind != service.SourceScanOperationKind {
		return fmt.Errorf("operation %s is not a source scan", operation.ID)
	}
	// A duplicate delivery of a job whose scan already reached a final state must
	// not walk the tree a second time.
	if operation.State == "succeeded" || operation.State == "failed" {
		return nil
	}
	if job.ID < 1 || operation.Attempt < 1 || operation.RiverJobID == nil || *operation.RiverJobID != job.ID {
		return fmt.Errorf("source scan delivery identity changed: %w", persistence.ErrSourceAnalysisStale)
	}
	delivery, ok := worker.repository.(scanDeliveryRepository)
	if !ok {
		return fmt.Errorf("source scan delivery fencing repository is unavailable")
	}
	if err := delivery.StartSourceScanDelivery(ctx, operation.ID, operation.Attempt, job.ID); err != nil {
		return err
	}
	snapshot, err := scanSourceSnapshot(operation)
	if err != nil {
		slog.Warn("source scan input is invalid", "operation", operation.ID.String(), "cause", err)
		return worker.fail(ctx, operation, service.SourceScanStageQueued, scanSafeInvalidInput)
	}
	root, err := worker.repository.GetSourceRoot(ctx, snapshot.SourceRootID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return worker.fail(ctx, operation, service.SourceScanStageQueued, scanSafeRootGone)
		}
		return err
	}
	// The apply of this operation committed before the process stopped. The
	// operation's durable root target is sufficient to finish the delivery.
	if root.LastAppliedOperationID != nil && *root.LastAppliedOperationID == operation.ID {
		return worker.finishApplied(ctx, operation.ID, root.ID)
	}
	if errors.Is(sourcefs.ValidateRootPathSupport(root.ConfiguredPath), service.ErrUnsupportedSourceRoot) {
		return worker.fail(ctx, operation, service.SourceScanStageQueued, scanSafeUnsupported)
	}
	if !root.Enabled {
		return worker.fail(ctx, operation, service.SourceScanStageQueued, scanSafeDisabled)
	}
	scan := service.NewSourceScan(worker.repository, nil, scanDeliveryStages{repository: worker.repository, attempt: operation.Attempt, jobID: job.ID})
	request := service.SourceScanRequest{
		OperationID: operation.ID, RootID: root.ID, ExpectedConfiguredPath: root.ConfiguredPath,
		ExpectedAttempt: operation.Attempt, ExpectedJobID: job.ID,
	}
	scopes, err := scan.EnumerateObserved(ctx, request)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		slog.Warn("source scan enumeration failed", "operation", operation.ID.String(), "cause", err)
		if errors.Is(err, service.ErrSourceRootInaccessible) {
			if markErr := worker.recordUnavailableRoot(ctx, operation); markErr != nil {
				return markErr
			}
			return worker.fail(ctx, operation, service.SourceScanStageTraversing, scanSafePath)
		}
		if errors.Is(err, service.ErrUnsupportedSourceRoot) {
			return worker.fail(ctx, operation, service.SourceScanStageTraversing, scanSafeUnsupported)
		}
		return worker.fail(ctx, operation, service.SourceScanStageTraversing, scanSafeTraversal)
	}
	shaSettings, ok := worker.settings.(scanSHA256SettingReader)
	if !ok {
		return fmt.Errorf("source scan SHA-256 setting reader is unavailable")
	}
	sha256Enabled, err := shaSettings.GetSHA256Enabled(ctx)
	if err != nil {
		return fmt.Errorf("read current SHA-256 setting for source enumeration: %w", err)
	}
	failureSafeError := ""
	if len(scopes) > 0 {
		failureSafeError = scanSafeTraversal
		for _, scope := range scopes {
			if scope.Kind == "root" {
				failureSafeError = scanSafePath
				break
			}
		}
	}
	if err := worker.repository.ApplySourceEnumeration(ctx, persistence.SourceEnumerationApply{
		OperationID: operation.ID, ExpectedConfiguredPath: root.ConfiguredPath,
		ExpectedAttempt: operation.Attempt, ExpectedJobID: job.ID, SHA256Enabled: sha256Enabled,
		Scopes: scopes, FailureSafeError: failureSafeError,
	}); err != nil {
		if ctx.Err() != nil {
			return err
		}
		slog.Warn("source enumeration apply failed", "operation", operation.ID.String(), "cause", err)
		return worker.fail(ctx, operation, service.SourceScanStageApplying, scanSafeApply)
	}
	return worker.finishApplied(ctx, operation.ID, root.ID)
}

// finishApplied reloads the outcome committed atomically with the inventory.
// Unreadable scopes reconcile the readable portion of the tree but leave the
// operation failed; recovery must preserve that outcome rather than infer
// success from LastAppliedOperationID alone.
func (worker *SourceScanWorker) finishApplied(ctx context.Context, operationID, rootID uuid.UUID) error {
	operation, err := worker.repository.GetOperation(ctx, operationID)
	if err != nil {
		return err
	}
	if operation.SafeError != nil && *operation.SafeError != "" {
		return worker.fail(ctx, operation, service.SourceScanStageTraversing, *operation.SafeError)
	}
	return worker.finishAndDispatch(ctx, operation, rootID)
}

// finishAndDispatch commits the scan's terminal success before it wakes the
// pending analysis admission of its root. The succeeded operation releases the
// hold the scan kept on the root, so the pending service can admit the work the
// apply made pending instead of leaving it stranded behind a still-running
// operation. The wake is fenced by the success commit: a duplicate delivery
// whose Succeed fails has not committed the terminal state and never dispatches.
// A failed wake is logged and never returned, so a committed scan is not failed
// or rerun over a best-effort handoff.
func (worker *SourceScanWorker) finishAndDispatch(ctx context.Context, operation *persistence.Operation, rootID uuid.UUID) error {
	delivery, ok := worker.repository.(scanDeliveryRepository)
	if !ok {
		return fmt.Errorf("source scan delivery fencing repository is unavailable")
	}
	if err := delivery.FinishSourceScanDelivery(ctx, operation.ID, operation.Attempt, scanOperationJobID(operation), "succeeded", scanSucceededStage, ""); err != nil {
		return err
	}
	if worker.pending != nil {
		if err := worker.pending.AdmitPending(ctx, rootID); err != nil {
			slog.Warn("source analysis pending dispatch failed after scan apply", "operation", operation.ID.String(), "cause", err)
		}
	}
	return nil
}

// scanSourceSnapshot decodes the durable snapshot and confirms it describes the
// operation itself, so a job whose snapshot does not match its row fails instead
// of scanning a root the operation does not own.
func scanSourceSnapshot(operation *persistence.Operation) (service.ScanSourceSnapshot, error) {
	var snapshot service.ScanSourceSnapshot
	if operation.TargetSourceRootID == nil {
		return snapshot, fmt.Errorf("scan operation has no target source root")
	}
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
		return snapshot, fmt.Errorf("decode scan snapshot: %w", err)
	}
	if snapshot.SchemaVersion != service.SourceScanSnapshotVersion || snapshot.SourceRootID != *operation.TargetSourceRootID {
		return snapshot, fmt.Errorf("scan snapshot does not describe its operation")
	}
	return snapshot, nil
}

// fail drops the candidates of the failed attempt and records the safe reason on
// the operation. The cleanup ignores a cancellation that outlived the failure;
// the operation transition needs a live context, so a canceled scan leaves the
// operation for the River retry instead of marking it failed.
func (worker *SourceScanWorker) fail(ctx context.Context, operation *persistence.Operation, stage, safe string) error {
	delivery, ok := worker.repository.(scanDeliveryRepository)
	if !ok {
		return fmt.Errorf("source scan delivery fencing repository is unavailable")
	}
	return delivery.FinishSourceScanDelivery(context.WithoutCancel(ctx), operation.ID, operation.Attempt, scanOperationJobID(operation), "failed", stage, safe)
}

// recordUnavailableRoot records a root-level traversal observation without
// allowing a stale delivery to overwrite a newer successful scan.
func (worker *SourceScanWorker) recordUnavailableRoot(ctx context.Context, operation *persistence.Operation) error {
	if err := worker.repository.MarkSourceRootUnavailable(ctx, persistence.SourceScanUnavailable{
		OperationID: operation.ID, SafeError: scanSafePath,
		ExpectedAttempt: operation.Attempt, ExpectedJobID: scanOperationJobID(operation),
	}); err != nil {
		return fmt.Errorf("record the unavailable source root: %w", err)
	}
	return nil
}

func scanOperationJobID(operation *persistence.Operation) int64 {
	if operation == nil || operation.RiverJobID == nil {
		return 0
	}
	return *operation.RiverJobID
}

type scanDeliveryStages struct {
	repository scanDeliveryRepository
	attempt    int
	jobID      int64
}

func (stages scanDeliveryStages) Running(ctx context.Context, operationID uuid.UUID, stage string) error {
	return stages.repository.SetSourceScanDeliveryStage(ctx, operationID, stages.attempt, stages.jobID, stage)
}

var _ river.Worker[service.ScanSourceJobArgs] = (*SourceScanWorker)(nil)
