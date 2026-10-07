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
	"sync"

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
	scanSafeNotReady     = "The source scan requires a completed setup on a supported server platform."
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

// ffprobeExecutableName is the managed executable a scan probes files with. The
// name is matched without the platform suffix the managed package adds.
const ffprobeExecutableName = "ffprobe"

// scanWorkerRepository is the persistence contract of a scan worker: the
// traversal contract of the scan itself, plus the operation, the active tool
// installation and the atomic apply the worker drives after the traversal.
type scanWorkerRepository interface {
	service.SourceScanRepository
	scanDeliveryRepository
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
	ApplySourceScan(context.Context, persistence.SourceScanApply) error
	MarkSourceRootUnavailable(context.Context, persistence.SourceScanUnavailable) error
}

type scanDeliveryRepository interface {
	StartSourceScanDelivery(context.Context, uuid.UUID, int, int64) error
	SetSourceScanDeliveryStage(context.Context, uuid.UUID, int, int64, string) error
	FinishSourceScanDelivery(context.Context, uuid.UUID, int, int64, string, string, string) error
}

// scanWorkerSettings is the runtime state a scan reloads before it walks: the
// completed Setup, the managed tools directory and the active installations.
type scanWorkerSettings interface {
	SetupCompleted(context.Context) (bool, error)
	GetToolsDirectory(context.Context) (string, bool, error)
	ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error)
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
	lifecycle  *tools.Lifecycle
	newProbe   func(string) (service.SourceProbe, error)
	pending    sourceAnalysisPendingDispatcher
}

// SetPendingDispatcher installs the best-effort handoff that starts analysis
// work made pending by a newly applied scan generation.
func (worker *SourceScanWorker) SetPendingDispatcher(dispatcher sourceAnalysisPendingDispatcher) {
	worker.pending = dispatcher
}

func NewSourceScanWorker(repository scanWorkerRepository, operations *service.Operations, paths service.SourceScanPathValidator, runtimeSettings scanWorkerSettings, platform settings.PlatformState, lifecycle *tools.Lifecycle) *SourceScanWorker {
	if lifecycle == nil {
		lifecycle = tools.NewLifecycle(nil)
	}
	return &SourceScanWorker{
		repository: repository, operations: operations, paths: paths,
		settings: runtimeSettings, platform: platform, lifecycle: lifecycle,
		newProbe: func(executable string) (service.SourceProbe, error) { return tools.NewFFProbe(executable) },
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
	// The apply of this operation committed before the process stopped: the root
	// records it as the generation it last applied, so the inventory is already
	// installed and the operation only needs to be finished, whatever state the
	// root is in now. Walking or applying again would re-probe the tree and
	// advance the generation of a snapshot that is already published.
	if root.LastAppliedOperationID != nil && *root.LastAppliedOperationID == operation.ID {
		return worker.finishAndDispatch(ctx, operation, root.ID)
	}
	if root.ConfiguredPath != snapshot.ConfiguredPath || root.ScanGeneration != snapshot.ScanGeneration {
		return worker.fail(ctx, operation, service.SourceScanStageQueued, scanSafePath)
	}
	if errors.Is(sourcefs.ValidateRootPathSupport(root.ConfiguredPath), service.ErrUnsupportedSourceRoot) {
		return worker.fail(ctx, operation, service.SourceScanStageQueued, scanSafeUnsupported)
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
		if errors.Is(err, service.ErrUnsupportedSourceRoot) {
			return worker.fail(ctx, operation, service.SourceScanStageQueued, scanSafeUnsupported)
		}
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
	analysis := worker.scanAnalysis(ctx, operation, snapshot, job.ID)
	scan := service.NewSourceScan(worker.repository, scanTransportDeferredProbe{}, scanDeliveryStages{repository: worker.repository, attempt: operation.Attempt, jobID: job.ID}, service.WithSourceScanAnalysis(analysis))
	scanErr := scan.Run(ctx, service.SourceScanRequest{
		OperationID: operation.ID, RootID: root.ID, ExpectedConfiguredPath: snapshot.ConfiguredPath,
		ExpectedAttempt: operation.Attempt, ExpectedJobID: job.ID,
		AnalysisTargets: scanAnalysisTargets(snapshot),
	})
	if releaseErr := analysis.ReleaseError(); releaseErr != nil {
		return fmt.Errorf("release source scan tool holds: %w", releaseErr)
	}
	if err := scanErr; err != nil {
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
		if errors.Is(err, service.ErrUnsupportedSourceRoot) {
			return worker.fail(ctx, operation, service.SourceScanStageTraversing, scanSafeUnsupported)
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
		ExpectedAttempt: operation.Attempt, ExpectedJobID: job.ID, SHA256Enabled: snapshot.SHA256Enabled,
	}); err != nil {
		if ctx.Err() != nil {
			return err
		}
		slog.Warn("source scan apply failed", "operation", operation.ID.String(), "cause", err)
		return worker.fail(ctx, operation, service.SourceScanStageApplying, scanSafeApply)
	}
	return worker.finishAndDispatch(ctx, operation, root.ID)
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
	if snapshot.SchemaVersion != service.SourceScanSnapshotVersion ||
		snapshot.SourceRootID != *operation.TargetSourceRootID || snapshot.ConfiguredPath == "" || snapshot.ScanGeneration < 0 ||
		snapshot.SHA256Enabled == nil || snapshot.Tools == nil {
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
	delivery, ok := worker.repository.(scanDeliveryRepository)
	if !ok {
		return fmt.Errorf("source scan delivery fencing repository is unavailable")
	}
	return delivery.FinishSourceScanDelivery(context.WithoutCancel(ctx), operation.ID, operation.Attempt, scanOperationJobID(operation), "failed", stage, safe)
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

// recordUnavailableRoot marks the scan's root unavailable before the operation
// is failed. The repository refuses the write when a successful scan of the root
// superseded this attempt, so a late failure never overwrites a newer success;
// that refusal is a no-op, not an error.
func (worker *SourceScanWorker) recordUnavailableRoot(ctx context.Context, operation *persistence.Operation) error {
	if err := worker.repository.MarkSourceRootUnavailable(ctx, persistence.SourceScanUnavailable{
		OperationID: operation.ID, SafeError: service.SourceScanDirectoryUnavailableReason,
		ExpectedAttempt: operation.Attempt, ExpectedJobID: scanOperationJobID(operation),
	}); err != nil {
		return fmt.Errorf("record the unavailable source root: %w", err)
	}
	return nil
}

type scanTransportDeferredProbe struct{}

func (scanTransportDeferredProbe) CheckFileTransport(context.Context) error { return nil }

func scanToolSelection(snapshot service.ScanSourceSnapshot, packageKind, executable string) (persistence.SourceAnalysisToolSelection, bool) {
	for _, selection := range snapshot.Tools {
		if selection.PackageKind == packageKind && selection.Executable == executable {
			return selection, true
		}
	}
	return persistence.SourceAnalysisToolSelection{}, false
}

func scanAnalysisTargets(snapshot service.ScanSourceSnapshot) service.SourceAnalysisTarget {
	targets := service.SourceAnalysisTargetProbe | service.SourceAnalysisTargetFingerprint
	if snapshot.SHA256Enabled != nil && *snapshot.SHA256Enabled {
		targets |= service.SourceAnalysisTargetSHA256
	}
	return targets
}

type scanAnalysisPreparer struct {
	config      service.SourceAnalysisPreparerConfig
	operationID uuid.UUID
	mu          sync.Mutex
	releaseErr  error
}

type scanHeldToolPath struct {
	mu   sync.Mutex
	path string
}

func (path *scanHeldToolPath) set(value string) {
	path.mu.Lock()
	path.path = value
	path.mu.Unlock()
}

func (path *scanHeldToolPath) get() (string, error) {
	path.mu.Lock()
	defer path.mu.Unlock()
	if path.path == "" {
		return "", fmt.Errorf("source scan tool hold did not provide an executable path")
	}
	return path.path, nil
}

func (preparer *scanAnalysisPreparer) Prepare(ctx context.Context, request service.SourceAnalysisPrepareRequest) service.SourceAnalysisPreparation {
	config := preparer.config
	config.ProbeResult.ID = uuid.New()
	config.ProbeResult.AppliedOperationID = &preparer.operationID
	config.FingerprintResult.ID = uuid.New()
	config.FingerprintResult.AppliedOperationID = preparer.operationID
	if info, err := request.File.Stat(ctx); err == nil {
		config.ProbeResult.SizeBytes = info.Size()
	}
	return service.NewSourceAnalysisPreparer(config).Prepare(ctx, request)
}

func (preparer *scanAnalysisPreparer) ReleaseError() error {
	preparer.mu.Lock()
	defer preparer.mu.Unlock()
	return preparer.releaseErr
}

func (worker *SourceScanWorker) scanAnalysis(ctx context.Context, operation *persistence.Operation, snapshot service.ScanSourceSnapshot, jobID int64) *scanAnalysisPreparer {
	probe, hasProbe := scanToolSelection(snapshot, string(tools.PackageFFmpeg), ffprobeExecutableName)
	fpcalc, hasFPCalc := scanToolSelection(snapshot, string(tools.PackageFPCalc), "fpcalc")
	probePath := &scanHeldToolPath{}
	fpcalcPath := &scanHeldToolPath{}
	config := service.SourceAnalysisPreparerConfig{
		AnalysisPolicy: persistence.SourceAnalysisPolicyVersion,
		ProbeResult:    persistence.SourceMediaVariant{SizeBytes: 0},
		FPCalcVersion:  tools.FPCalcVersion{},
		FingerprintResult: persistence.SourceFingerprintResult{
			FPCalcVersion: "", VersionBanner: "", AlgorithmNamespace: "chromaprint", ParserContractVersion: 1,
		},
	}
	if hasProbe {
		if _, ok := scanExpectedExecutable(tools.PackageFFmpeg, probe.Executable, worker.platform.Platform.GOOS); !ok {
			hasProbe = false
		}
		config.FFProbeVersion = probe.VersionBanner
	}
	if hasFPCalc {
		if _, ok := scanExpectedExecutable(tools.PackageFPCalc, fpcalc.Executable, worker.platform.Platform.GOOS); !ok {
			hasFPCalc = false
		}
		config.FPCalcVersion = tools.FPCalcVersion{Version: fpcalc.Version, Banner: fpcalc.VersionBanner}
		config.FingerprintResult.FPCalcVersion = fpcalc.Version
		config.FingerprintResult.VersionBanner = fpcalc.VersionBanner
	}
	if cache, ok := worker.repository.(service.SourceAnalysisCacheLookup); ok {
		config.Cache = cache
	}
	config.ProbeFactory = func(string) (service.SourceAnalysisProbe, error) {
		path, err := probePath.get()
		if err != nil {
			return nil, err
		}
		return tools.NewFFProbe(path)
	}
	config.FingerprinterFactory = func(string) (service.SourceAnalysisFingerprinter, error) {
		path, err := fpcalcPath.get()
		if err != nil {
			return nil, err
		}
		return tools.NewFPCalc(path)
	}
	preparer := &scanAnalysisPreparer{config: config, operationID: operation.ID}
	config.Hold = func(ctx context.Context, step service.SourceAnalysisStep) (func(), error) {
		selection, selected := probe, hasProbe
		targetPath := probePath
		if step == service.SourceAnalysisFingerprintStep {
			selection, selected = fpcalc, hasFPCalc
			targetPath = fpcalcPath
		}
		if !selected {
			return nil, fmt.Errorf("the pinned tool selection is unavailable")
		}
		holder, ok := worker.repository.(interface {
			AcquireSourceScanToolHold(context.Context, uuid.UUID, uuid.UUID, string, int, int64, persistence.SourceAnalysisToolSelection) (persistence.SourceScanToolHold, func() error, error)
		})
		if !ok {
			return nil, fmt.Errorf("source scan tool hold repository is unavailable")
		}
		held, release, err := holder.AcquireSourceScanToolHold(ctx, operation.ID, snapshot.SourceRootID, snapshot.ConfiguredPath, operation.Attempt, jobID, selection)
		if err != nil {
			return nil, err
		}
		releaseAfterSetupFailure := func(cause error) (func(), error) {
			if releaseErr := release(); releaseErr != nil {
				preparer.mu.Lock()
				preparer.releaseErr = errors.Join(preparer.releaseErr, releaseErr)
				preparer.mu.Unlock()
				cause = errors.Join(cause, releaseErr)
			}
			return nil, cause
		}
		if held.Installation.PlatformGOOS != worker.platform.Platform.GOOS || held.Installation.PlatformGOARCH != worker.platform.Platform.GOARCH {
			return releaseAfterSetupFailure(fmt.Errorf("pinned managed tool does not match the worker platform"))
		}
		if step == service.SourceAnalysisProbeStep {
			versions, verifyErr := worker.lifecycle.VerifyInstallation(ctx, held.ToolsRoot, held.Installation.RelativePath, tools.PackageFFmpeg, held.Installation.ReleaseIdentity, worker.platform.Platform.GOOS)
			actual := versions[filepath.Base(held.ExecutablePath)]
			if verifyErr != nil || strings.TrimSpace(actual) != selection.VersionBanner || !pinnedFFProbeVersionMatches(actual, selection.Version) {
				if verifyErr != nil {
					return releaseAfterSetupFailure(fmt.Errorf("verify pinned managed ffprobe while held: %w", verifyErr))
				}
				return releaseAfterSetupFailure(fmt.Errorf("managed ffprobe does not match pinned version metadata"))
			}
			transportProbe, probeErr := worker.newProbe(held.ExecutablePath)
			if probeErr != nil {
				return releaseAfterSetupFailure(fmt.Errorf("create the pinned managed ffprobe: %w", probeErr))
			}
			if err := transportProbe.CheckFileTransport(ctx); err != nil {
				return releaseAfterSetupFailure(fmt.Errorf("check pinned managed ffprobe transport: %w", err))
			}
		}
		targetPath.set(held.ExecutablePath)
		return func() {
			if err := release(); err != nil {
				preparer.mu.Lock()
				preparer.releaseErr = errors.Join(preparer.releaseErr, err)
				preparer.mu.Unlock()
			}
		}, nil
	}
	preparer.config = config
	return preparer
}

func scanExpectedExecutable(kind tools.PackageKind, executable, goos string) (string, bool) {
	for _, name := range tools.ExpectedExecutables(kind, goos) {
		base := name
		if extension := filepath.Ext(name); extension != "" {
			base = name[:len(name)-len(extension)]
		}
		if name == executable || base == executable {
			return name, true
		}
	}
	return "", false
}

var _ river.Worker[service.ScanSourceJobArgs] = (*SourceScanWorker)(nil)
