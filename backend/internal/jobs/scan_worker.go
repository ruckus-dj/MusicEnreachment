package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
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
	scanSafeNotReady     = "The source scan requires a completed setup on a supported server platform."
	scanSafePath         = service.SourceScanDirectoryUnavailableReason
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

// ffprobeExecutableName is the managed executable a scan probes files with. The
// name is matched without the platform suffix the managed package adds.
const ffprobeExecutableName = "ffprobe"

// scanWorkerRepository is the persistence contract of a scan worker: the
// traversal contract of the scan itself, plus the operation, the active tool
// installation and the atomic apply the worker drives after the traversal.
type scanWorkerRepository interface {
	service.SourceScanRepository
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
	ApplySourceScan(context.Context, persistence.SourceScanApply) error
	MarkSourceRootUnavailable(context.Context, persistence.SourceScanUnavailable) error
}

// scanWorkerSettings is the runtime state a scan reloads before it walks: the
// completed Setup, the managed tools directory and the active installations.
type scanWorkerSettings interface {
	SetupCompleted(context.Context) (bool, error)
	GetToolsDirectory(context.Context) (string, bool, error)
	ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error)
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
	lifecycle  *tools.Lifecycle
}

func NewSourceScanWorker(repository scanWorkerRepository, operations *service.Operations, paths service.SourceScanPathValidator, runtimeSettings scanWorkerSettings, platform settings.PlatformState, lifecycle *tools.Lifecycle) *SourceScanWorker {
	if lifecycle == nil {
		lifecycle = tools.NewLifecycle(nil)
	}
	return &SourceScanWorker{
		repository: repository, operations: operations, paths: paths,
		settings: runtimeSettings, platform: platform, lifecycle: lifecycle,
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
	// The apply of this operation committed before the process stopped: the root
	// records it as the generation it last applied, so the inventory is already
	// installed and the operation only needs to be finished, whatever state the
	// root is in now. Walking or applying again would re-probe the tree and
	// advance the generation of a snapshot that is already published.
	if root.LastAppliedOperationID != nil && *root.LastAppliedOperationID == operation.ID {
		return worker.operations.Succeed(ctx, operation.ID, scanSucceededStage)
	}
	if !root.Enabled {
		return worker.fail(ctx, operation, service.SourceScanStageQueued, scanSafeDisabled)
	}
	if err := worker.ready(ctx); err != nil {
		slog.Warn("source scan cannot start", "operation", operation.ID.String(), "cause", err)
		return worker.fail(ctx, operation, service.SourceScanStageQueued, scanSafeNotReady)
	}
	// The root is re-validated against the path the snapshot carries: a path the
	// operator changed after the enqueue must not publish files of the new path
	// as the inventory of the old one.
	path, err := worker.paths.ValidateSourcePath(ctx, root.ConfiguredPath, &root.ID)
	if err != nil {
		slog.Warn("source scan path failed revalidation", "operation", operation.ID.String(), "cause", err)
		// A managed-path overlap, a duplicate configured path and a failed
		// database read say nothing about the directory, so only a proven
		// inaccessible root is recorded before the operation fails.
		if errors.Is(err, service.ErrSourceRootInaccessible) {
			if markErr := worker.recordUnavailableRoot(ctx, operation); markErr != nil {
				return markErr
			}
		}
		return worker.fail(ctx, operation, service.SourceScanStageQueued, scanSafePath)
	}
	if path != snapshot.ConfiguredPath {
		slog.Warn("source scan path changed after the enqueue", "operation", operation.ID.String())
		return worker.fail(ctx, operation, service.SourceScanStageQueued, scanSafePath)
	}
	probe, err := worker.managedProbe(ctx)
	if err != nil {
		slog.Warn("source scan has no working managed ffprobe", "operation", operation.ID.String(), "cause", err)
		return worker.fail(ctx, operation, service.SourceScanStageQueued, scanSafeTool)
	}
	scan := service.NewSourceScan(worker.repository, probe, worker.operations)
	if err := scan.Run(ctx, service.SourceScanRequest{OperationID: operation.ID, RootID: root.ID}); err != nil {
		if ctx.Err() != nil {
			return err
		}
		slog.Warn("source scan traversal failed", "operation", operation.ID.String(), "cause", err)
		// A traversal that failed because the root directory itself cannot be
		// read proves the root inaccessible. A subtree that could not be read, a
		// file that changed under the scan and a canceled scan carry no marker
		// and leave the root exactly as it was.
		if errors.Is(err, service.ErrSourceRootInaccessible) {
			if markErr := worker.recordUnavailableRoot(ctx, operation); markErr != nil {
				return markErr
			}
		}
		return worker.fail(ctx, operation, service.SourceScanStageTraversing, scanSafeTraversal)
	}
	// The apply is one transaction that installs the verified generation. It is
	// deliberately not atomic with the operation transition: after this commit
	// the root carries last_applied_operation_id while the operation is still
	// running, and startup recovery reads that window to finish the operation
	// without re-applying the generation.
	if err := worker.repository.ApplySourceScan(ctx, persistence.SourceScanApply{
		OperationID: operation.ID, ExpectedConfiguredPath: snapshot.ConfiguredPath,
	}); err != nil {
		if ctx.Err() != nil {
			return err
		}
		slog.Warn("source scan apply failed", "operation", operation.ID.String(), "cause", err)
		return worker.fail(ctx, operation, service.SourceScanStageApplying, scanSafeApply)
	}
	return worker.operations.Succeed(ctx, operation.ID, scanSucceededStage)
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
	if snapshot.SchemaVersion != service.SourceScanSnapshotVersion ||
		snapshot.SourceRootID != *operation.TargetSourceRootID || snapshot.ConfiguredPath == "" {
		return snapshot, fmt.Errorf("scan snapshot does not describe its operation")
	}
	return snapshot, nil
}

// ready reports whether the instance can run a scan at all: an unusable platform
// has no managed tools to read a source with, and an unfinished Setup has not
// verified them yet.
func (worker *SourceScanWorker) ready(ctx context.Context) error {
	if worker.platform.Diagnostic || !worker.platform.Platform.Supported() {
		return fmt.Errorf("the instance platform is not usable: %s", worker.platform.Reason)
	}
	completed, err := worker.settings.SetupCompleted(ctx)
	if err != nil {
		return fmt.Errorf("read setup completion: %w", err)
	}
	if !completed {
		return fmt.Errorf("setup is not complete")
	}
	return nil
}

// fail drops the candidates of the failed attempt and records the safe reason on
// the operation. The cleanup ignores a cancellation that outlived the failure;
// the operation transition needs a live context, so a canceled scan leaves the
// operation for the River retry instead of marking it failed.
func (worker *SourceScanWorker) fail(ctx context.Context, operation *persistence.Operation, stage, safe string) error {
	if err := worker.repository.DeleteSourceScanCandidates(context.WithoutCancel(ctx), operation.ID); err != nil {
		return fmt.Errorf("drop the candidates of the failed scan: %w", err)
	}
	return worker.operations.Fail(ctx, operation.ID, stage, safe)
}

// recordUnavailableRoot marks the scan's root unavailable before the operation
// is failed. The repository refuses the write when a successful scan of the root
// superseded this attempt, so a late failure never overwrites a newer success;
// that refusal is a no-op, not an error.
func (worker *SourceScanWorker) recordUnavailableRoot(ctx context.Context, operation *persistence.Operation) error {
	if err := worker.repository.MarkSourceRootUnavailable(ctx, persistence.SourceScanUnavailable{
		OperationID: operation.ID, SafeError: service.SourceScanDirectoryUnavailableReason,
	}); err != nil {
		return fmt.Errorf("record the unavailable source root: %w", err)
	}
	return nil
}

// managedProbe resolves the active managed ffprobe a scan probes files with and
// proves it still runs. ffprobe is never resolved from PATH, and a tool that is
// missing, not the active installation of this platform, or failing its version
// query is an error: the whole scan fails with one safe reason instead of every
// file becoming a probe_error.
func (worker *SourceScanWorker) managedProbe(ctx context.Context) (service.SourceProbe, error) {
	runtime, err := worker.settings.ReadRuntimeSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the runtime settings: %w", err)
	}
	if runtime.ActiveFFmpegInstallation == "" {
		return nil, fmt.Errorf("no active managed ffmpeg installation is configured")
	}
	installationID, err := uuid.Parse(runtime.ActiveFFmpegInstallation)
	if err != nil {
		return nil, fmt.Errorf("the active managed ffmpeg installation is invalid")
	}
	installation, err := worker.repository.GetInstallation(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("read the active managed ffmpeg installation: %w", err)
	}
	if installation.PackageKind != string(tools.PackageFFmpeg) || installation.State != "ready" || installation.VerifiedAt == nil ||
		installation.PlatformGOOS != worker.platform.Platform.GOOS || installation.PlatformGOARCH != worker.platform.Platform.GOARCH {
		return nil, fmt.Errorf("the active managed ffmpeg installation is not ready for this platform")
	}
	relative, err := tools.ManagedRelativePath(tools.PackageFFmpeg, installation.ReleaseIdentity)
	if err != nil || filepath.Clean(installation.RelativePath) != relative {
		return nil, fmt.Errorf("the active managed ffmpeg installation has an invalid path")
	}
	root, exists, err := worker.settings.GetToolsDirectory(ctx)
	if err != nil {
		return nil, err
	}
	if !exists || root == "" {
		return nil, fmt.Errorf("the managed tools directory is unavailable")
	}
	// The installation was verified when it was materialized; running the version
	// query again proves the executable the scan is about to run still works, so a
	// tool that was removed or broken later fails the scan up front.
	if _, err := worker.lifecycle.VerifyInstallation(ctx, root, relative, tools.PackageFFmpeg, installation.ReleaseIdentity, worker.platform.Platform.GOOS); err != nil {
		return nil, fmt.Errorf("verify the managed ffmpeg installation: %w", err)
	}
	executable, err := managedFFProbeExecutable(root, relative, worker.platform.Platform.GOOS)
	if err != nil {
		return nil, err
	}
	probe, err := tools.NewFFProbe(executable)
	if err != nil {
		return nil, err
	}
	return probe, nil
}

// managedFFProbeExecutable names the ffprobe of a managed ffmpeg package,
// including the executable suffix the platform adds.
func managedFFProbeExecutable(root, relative, goos string) (string, error) {
	for _, name := range tools.ExpectedExecutables(tools.PackageFFmpeg, goos) {
		if strings.TrimSuffix(name, filepath.Ext(name)) == ffprobeExecutableName {
			return filepath.Join(root, relative, name), nil
		}
	}
	return "", fmt.Errorf("a managed ffmpeg package has no %s executable", ffprobeExecutableName)
}

var _ river.Worker[service.ScanSourceJobArgs] = (*SourceScanWorker)(nil)
