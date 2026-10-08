package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

const (
	analysisSafeInvalidInput = "The source analysis input is invalid. Start a new analysis."
	analysisSafeNotReady     = "The source analysis requires a completed setup on a supported server platform."
	analysisSafeInterrupted  = "The source analysis was interrupted. Pending work will be resumed."
)

type analysisWorkerRepository interface {
	service.SourceAnalysisRepository
	service.SourceAnalysisCacheLookup
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	StartNormalizedSourceAnalysisDelivery(context.Context, uuid.UUID, persistence.SourceAnalysisOperationDelivery, string, string) (*persistence.Operation, []persistence.SourceAnalysisToolSelection, string, error)
	ListNormalizedSourceAnalysisExecution(context.Context, uuid.UUID, int, int64) ([]persistence.SourceAnalysisExecution, error)
	GetNormalizedSourceAnalysisWork(context.Context, uuid.UUID) (*persistence.SourceAnalysisWork, *persistence.SourceLocation, error)
	ClaimSourceAnalysisStep(context.Context, persistence.SourceStepClaim) (int, error)
	ApplySourceSHA256(context.Context, persistence.SourceSHA256Apply) (*persistence.SourceMediaVariant, error)
	ApplySourceProbe(context.Context, persistence.SourceProbeApply) (*persistence.SourceMediaVariant, error)
	ApplySourceFingerprint(context.Context, persistence.SourceFingerprintApply) (*persistence.SourceFingerprintResult, error)
	ReuseSourceProbe(context.Context, persistence.SourceStepClaim, int, uuid.UUID, string, int) (*persistence.SourceMediaVariant, error)
	ReuseSourceFingerprint(context.Context, persistence.SourceStepClaim, int, string) (*persistence.SourceFingerprintResult, error)
	FailSourceAnalysisStep(context.Context, persistence.SourceStepFailure) error
	SettleNormalizedSourceAnalysisDelivery(context.Context, uuid.UUID, persistence.SourceAnalysisOperationDelivery, string, string, string) error
	RecoverNormalizedSourceAnalysisDelivery(context.Context, uuid.UUID, persistence.SourceAnalysisOperationDelivery, string) error
	CheckNormalizedSourceAnalysisToolHold(context.Context, uuid.UUID, uuid.UUID, int, int64, string) error
}

type analysisWorkerSettings interface {
	SetupCompleted(context.Context) (bool, error)
	GetToolsDirectory(context.Context) (string, bool, error)
}

type sourceFileConcurrencyReader interface {
	GetSourceFileConcurrency(context.Context) (int, error)
}

// pendingDispatcher admits the next queued batch for a source root after a
// normalized analysis settles. The pending service owns the root-exclusive
// admission; the worker only wakes it after a committed terminal settlement, so
// the release of the operation's holds never strands pending siblings.
type pendingDispatcher interface {
	AdmitPending(context.Context, uuid.UUID) (*persistence.Operation, error)
}

type SourceAnalysisWorker struct {
	river.WorkerDefaults[service.SourceAnalysisJobArgs]
	repository           analysisWorkerRepository
	operations           *service.Operations
	toolsDirectory       service.ToolsDirectoryReader
	runtimeSettings      analysisWorkerSettings
	platform             settings.PlatformState
	opener               sourcefs.Opener
	preparer             service.SourceAnalysisPreparing
	verifyFFProbeVersion func(context.Context, string) (string, error)
	pendingDispatcher    pendingDispatcher
	fileLimiter          *sourceFileLimiter
}

// sourceFileLimiter is shared by all River deliveries handled by this worker.
// The limit is read before each admission attempt, so runtime setting changes
// affect waiting files without imposing a process-wide hard-coded ceiling.
type sourceFileLimiter struct {
	mu      sync.Mutex
	active  int
	changed chan struct{}
}

func (limiter *sourceFileLimiter) acquire(ctx context.Context, getLimit func(context.Context) (int, error)) (func(), error) {
	for {
		limit, err := getLimit(ctx)
		if err != nil {
			return nil, err
		}
		if limit < 1 {
			return nil, fmt.Errorf("source file concurrency must be positive")
		}
		limiter.mu.Lock()
		if limiter.changed == nil {
			limiter.changed = make(chan struct{})
		}
		if limiter.active < limit {
			limiter.active++
			limiter.mu.Unlock()
			return func() {
				limiter.mu.Lock()
				limiter.active--
				close(limiter.changed)
				limiter.changed = make(chan struct{})
				limiter.mu.Unlock()
			}, nil
		}
		changed := limiter.changed
		limiter.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func NewSourceAnalysisWorker(repository analysisWorkerRepository, operations *service.Operations, toolsDirectory service.ToolsDirectoryReader, runtimeSettings analysisWorkerSettings, platform settings.PlatformState) *SourceAnalysisWorker {
	return &SourceAnalysisWorker{repository: repository, operations: operations, toolsDirectory: toolsDirectory,
		runtimeSettings: runtimeSettings, platform: platform, opener: sourcefs.NewOpener(), fileLimiter: new(sourceFileLimiter)}
}

// WithPreparer makes the step engine replaceable in deterministic worker tests.
func (worker *SourceAnalysisWorker) WithPreparer(preparer service.SourceAnalysisPreparing) *SourceAnalysisWorker {
	worker.preparer = preparer
	return worker
}

// SetPendingDispatcher wires the root-scoped pending admission wake. It is set
// after composition because the pending service depends on the running River
// client.
func (worker *SourceAnalysisWorker) SetPendingDispatcher(dispatcher pendingDispatcher) {
	worker.pendingDispatcher = dispatcher
}

func (worker *SourceAnalysisWorker) Work(ctx context.Context, job *river.Job[service.SourceAnalysisJobArgs]) error {
	operation, err := worker.repository.GetOperation(ctx, job.Args.OperationID)
	if err != nil {
		return err
	}
	if operation.Kind != service.SourceAnalysisOperationKind {
		return fmt.Errorf("operation %s is not a source analysis", operation.ID)
	}
	if operation.State == "succeeded" || operation.State == "failed" {
		return nil
	}
	snapshot, err := persistence.ValidateSourceAnalysisOperationContract(operation)
	if operation.RiverJobID == nil || *operation.RiverJobID != job.ID {
		return nil // Stale delivery: it owns no current execution fence.
	}
	if err != nil {
		return worker.terminalFailure(ctx, operation, job.ID, service.SourceAnalysisStageQueued, analysisSafeInvalidInput)
	}
	delivery := persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: job.ID}
	if operation.State == "running" {
		rootID := operation.TargetSourceRootID
		if err := worker.repository.RecoverNormalizedSourceAnalysisDelivery(ctx, operation.ID, delivery, analysisSafeInterrupted); err != nil {
			if errors.Is(err, persistence.ErrSourceAnalysisStale) {
				return nil
			}
			return err
		}
		operation, err = worker.repository.GetOperation(ctx, operation.ID)
		if err != nil {
			return err
		}
		if operation.State != "failed" || operation.Stage != "recovered" {
			return nil
		}
		operation.TargetSourceRootID = rootID
		worker.operations.Notify(operation.ID)
		worker.wakePending(ctx, operation)
		return nil
	}
	if operation.State != "queued" {
		return worker.terminalFailure(ctx, operation, job.ID, service.SourceAnalysisStageQueued, analysisSafeInvalidInput)
	}
	if err := worker.ready(ctx); err != nil {
		slog.Warn("source analysis cannot start", "operation", operation.ID, "cause", err)
		return worker.terminalFailure(ctx, operation, job.ID, service.SourceAnalysisStageQueued, analysisSafeNotReady)
	}
	admittedOperation := operation
	operation, selections, processingMode, err := worker.repository.StartNormalizedSourceAnalysisDelivery(ctx, operation.ID, delivery, worker.platform.Platform.GOOS, worker.platform.Platform.GOARCH)
	if err != nil {
		if errors.Is(err, persistence.ErrSourceAnalysisStale) {
			// A delivery that still owns the operation fence can become stale
			// before it acquires its work/root holds (for example, the root was
			// disabled after admission). Recover that exact delivery instead of
			// leaving it queued forever. Recovery intentionally does not wake
			// pending admission: the same unchanged work would immediately loop.
			return worker.recoverStaleDelivery(ctx, admittedOperation, delivery)
		}
		return err
	}
	snapshot.Tools = selections
	snapshot.ToolsReadRequired = len(selections) != 0
	cacheOnly := false
	snapshot.CacheOnlyReuse = &cacheOnly
	if processingMode != "in_place" {
		return worker.terminalFailure(ctx, operation, job.ID, service.SourceAnalysisStageProbing, "Staged source analysis is unavailable until staged processing is supported.")
	}
	// A batch only owns the queued execution triples written at admission. It never
	// expands to newly-pending steps and never revisits source scans.
	executions, err := worker.repository.ListNormalizedSourceAnalysisExecution(ctx, operation.ID, operation.Attempt, job.ID)
	if err != nil {
		return err
	}
	if len(executions) == 0 {
		return worker.terminalFailure(ctx, operation, job.ID, service.SourceAnalysisStageQueued, analysisSafeInvalidInput)
	}
	var singleFailure *sourceAnalysisStepFailure
	groups := groupSourceAnalysisExecutions(executions)
	results := make(chan error, len(groups))
	for _, group := range groups {
		group := group
		go func() {
			getLimit := func(ctx context.Context) (int, error) {
				if reader, ok := worker.runtimeSettings.(sourceFileConcurrencyReader); ok {
					limit, readErr := reader.GetSourceFileConcurrency(ctx)
					if readErr != nil {
						return 0, fmt.Errorf("read source file concurrency: %w", readErr)
					}
					return limit, nil
				}
				return len(groups), nil
			}
			release, acquireErr := worker.fileLimiter.acquire(ctx, getLimit)
			if acquireErr != nil {
				results <- acquireErr
				return
			}
			defer release()
			results <- worker.runWorkGroup(ctx, operation, snapshot, job.ID, group)
		}()
	}
	for _, group := range groups {
		if err := <-results; err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			if errors.Is(err, persistence.ErrSourceAnalysisStale) {
				return worker.recoverStaleDelivery(ctx, operation, delivery)
			}
			var stepFailure *sourceAnalysisStepFailure
			if !errors.As(err, &stepFailure) {
				return err
			}
			if singleFailure == nil {
				singleFailure = stepFailure
			}
			slog.Warn("source analysis work delivery failed", "operation", operation.ID, "work", group[0].Work.ID, "cause", err)
		}
		worker.operations.Notify(operation.ID)
	}
	state, safeError := "succeeded", ""
	if singleFailure != nil {
		state, safeError = "failed", singleFailure.safeError
	}
	if err := worker.repository.SettleNormalizedSourceAnalysisDelivery(ctx, operation.ID, persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: job.ID}, state, service.SourceAnalysisStageApplying, safeError); err != nil {
		return err
	}
	worker.operations.Notify(operation.ID)
	worker.wakePending(ctx, operation)
	return nil
}

func (worker *SourceAnalysisWorker) recoverStaleDelivery(ctx context.Context, operation *persistence.Operation, delivery persistence.SourceAnalysisOperationDelivery) error {
	rootID := operation.TargetSourceRootID
	if err := worker.repository.RecoverNormalizedSourceAnalysisDelivery(ctx, operation.ID, delivery, analysisSafeInterrupted); err != nil {
		if errors.Is(err, persistence.ErrSourceAnalysisStale) {
			return nil
		}
		return err
	}
	recovered, err := worker.repository.GetOperation(ctx, operation.ID)
	if err != nil {
		return err
	}
	if recovered.State != "failed" || recovered.Stage != "recovered" {
		return nil
	}
	if rootID != nil {
		recovered.TargetSourceRootID = rootID
	}
	worker.operations.Notify(operation.ID)
	return nil
}

func (worker *SourceAnalysisWorker) runWorkGroup(ctx context.Context, operation *persistence.Operation, snapshot persistence.SourceAnalysisOperationSnapshot, jobID int64, executions []persistence.SourceAnalysisExecution) error {
	if len(executions) == 0 {
		return nil
	}
	workID := executions[0].Work.ID
	claims := make(map[persistence.SourceStepName]int, len(executions))
	var failures []error
	claim := func(execution persistence.SourceAnalysisExecution) error {
		step := persistence.SourceStepName(execution.Step.Step)
		attempt, err := worker.repository.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{WorkID: workID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: jobID, Step: step})
		if err != nil {
			return err
		}
		claims[step] = attempt
		return nil
	}
	byStep := make(map[persistence.SourceStepName]persistence.SourceAnalysisExecution, len(executions))
	for _, execution := range executions {
		byStep[persistence.SourceStepName(execution.Step.Step)] = execution
	}
	ordered := make([]persistence.SourceStepName, 0, len(executions))
	for _, step := range []persistence.SourceStepName{persistence.SourceStepSHA256, persistence.SourceStepProbe, persistence.SourceStepFingerprint} {
		if _, ok := byStep[step]; ok {
			ordered = append(ordered, step)
		}
	}
	var newDigest *[32]byte
	for _, step := range ordered {
		if err := claim(byStep[step]); err != nil {
			return err
		}
		if step == persistence.SourceStepSHA256 {
			prepared, work, file, root, err := worker.prepareWorkSteps(ctx, operation, snapshot, jobID, byStep[step], claims, []persistence.SourceStepName{step}, nil)
			if file != nil {
				defer func() { _ = file.Close() }()
			}
			if root != nil {
				defer func() { _ = root.Close() }()
			}
			if err != nil {
				var stepFailure *sourceAnalysisStepFailure
				if errors.As(err, &stepFailure) {
					failures = append(failures, err)
					continue
				}
				return err
			}
			digest, applyErr := worker.applyPreparedStep(ctx, operation, snapshot, jobID, byStep[step], claims[step], work, step, prepared)
			if applyErr != nil {
				failures = append(failures, applyErr)
			} else {
				newDigest = digest
			}
		}
	}
	// Claims for non-SHA steps are fenced independently before the shared prepare.
	for _, step := range []persistence.SourceStepName{persistence.SourceStepProbe, persistence.SourceStepFingerprint} {
		if _, ok := byStep[step]; ok {
			if _, exists := claims[step]; !exists {
				if err := claim(byStep[step]); err != nil {
					return err
				}
			}
		}
	}
	var existing *[32]byte
	if _, hasSHA := byStep[persistence.SourceStepSHA256]; hasSHA {
		existing = newDigest
	} else {
		existing = executions[0].ExistingSHA256
	}
	steps := make([]persistence.SourceStepName, 0, 2)
	for _, step := range []persistence.SourceStepName{persistence.SourceStepProbe, persistence.SourceStepFingerprint} {
		if _, ok := byStep[step]; ok {
			steps = append(steps, step)
		}
	}
	if len(steps) > 0 {
		prepared, work, file, root, err := worker.prepareWorkSteps(ctx, operation, snapshot, jobID, byStep[steps[0]], claims, steps, existing)
		if file != nil {
			defer func() { _ = file.Close() }()
		}
		if root != nil {
			defer func() { _ = root.Close() }()
		}
		if err != nil {
			var stepFailure *sourceAnalysisStepFailure
			if errors.As(err, &stepFailure) {
				failures = append(failures, err)
				for _, sibling := range steps[1:] {
					failures = append(failures, worker.failStep(ctx, operation, byStep[sibling], sibling, claims[sibling], jobID, stepFailure.safeError))
				}
				return errors.Join(failures...)
			}
			return err
		}
		for _, step := range steps {
			_, applyErr := worker.applyPreparedStep(ctx, operation, snapshot, jobID, byStep[step], claims[step], work, step, prepared)
			if applyErr != nil {
				failures = append(failures, applyErr)
			}
		}
	}
	return errors.Join(failures...)
}

func (worker *SourceAnalysisWorker) prepareWorkSteps(ctx context.Context, operation *persistence.Operation, snapshot persistence.SourceAnalysisOperationSnapshot, jobID int64, execution persistence.SourceAnalysisExecution, claims map[persistence.SourceStepName]int, steps []persistence.SourceStepName, existing *[32]byte) (service.SourceAnalysisPreparation, *persistence.SourceAnalysisWork, sourcefs.RegularFile, sourcefs.Directory, error) {
	step := persistence.SourceStepName(execution.Step.Step)
	work, location, err := worker.repository.GetNormalizedSourceAnalysisWork(ctx, execution.Work.ID)
	if err != nil {
		return service.SourceAnalysisPreparation{}, nil, nil, nil, worker.failStep(ctx, operation, execution, step, claims[step], jobID, "The source file is no longer available for analysis.")
	}
	root, err := worker.repository.GetSourceRoot(ctx, work.SourceRootID)
	if err != nil || root == nil || !root.Enabled || root.Stale() || root.ConfiguredPath != work.ConfiguredPath || root.InventoryPath == nil || *root.InventoryPath != work.InventoryPath {
		return service.SourceAnalysisPreparation{}, nil, nil, nil, worker.failStep(ctx, operation, execution, step, claims[step], jobID, "The source inventory changed. Start a new analysis.")
	}
	if location.RelativePath != work.RelativePath || location.SizeBytes != work.SizeBytes || analysisMtime(location.Mtime) != analysisMtime(work.Mtime) {
		return service.SourceAnalysisPreparation{}, nil, nil, nil, worker.failStep(ctx, operation, execution, step, claims[step], jobID, "The source file changed. Start a new analysis.")
	}
	rootDir, err := worker.opener.OpenRoot(ctx, work.InventoryPath)
	if err != nil {
		return service.SourceAnalysisPreparation{}, nil, nil, nil, worker.failStep(ctx, operation, execution, step, claims[step], jobID, "The source directory is unavailable. The previous result is unchanged.")
	}
	file, err := sourcefs.OpenRegularAt(ctx, rootDir, work.RelativePath)
	if err != nil {
		_ = rootDir.Close()
		return service.SourceAnalysisPreparation{}, nil, nil, nil, worker.failStep(ctx, operation, execution, step, claims[step], jobID, "The source file is unavailable. The previous result is unchanged.")
	}
	info, err := file.Stat(ctx)
	if err != nil || info.Size() != work.SizeBytes || analysisMtime(info.ModTime()) != analysisMtime(work.Mtime) {
		_ = file.Close()
		_ = rootDir.Close()
		return service.SourceAnalysisPreparation{}, nil, nil, nil, worker.failStep(ctx, operation, execution, step, claims[step], jobID, "The source file changed. Start a new analysis.")
	}
	if err := worker.verifyAbsoluteSourceStillCurrent(ctx, work, file, rootDir); err != nil {
		_ = file.Close()
		_ = rootDir.Close()
		return service.SourceAnalysisPreparation{}, nil, nil, nil, worker.failStep(ctx, operation, execution, step, claims[step], jobID, "The source file changed. Start a new analysis.")
	}
	targets := service.SourceAnalysisTarget(0)
	for _, targetStep := range steps {
		targets |= analysisTargets(targetStep)
	}
	request := service.SourceAnalysisPrepareRequest{File: file, Targets: targets, ExistingSHA256: existing}
	if snapshot.Mode == persistence.SourceAnalysisModeSingleStep && operation.RerunTarget && snapshot.TargetStep != nil && *snapshot.TargetStep == string(persistence.SourceStepFingerprint) {
		request.BypassFingerprintCache = true
	}
	config, err := worker.preparerConfig(ctx, snapshot, work, operation.ID, operation.Attempt, jobID, steps...)
	if err != nil {
		_ = file.Close()
		_ = rootDir.Close()
		return service.SourceAnalysisPreparation{}, nil, nil, nil, worker.failStep(ctx, operation, execution, step, claims[step], jobID, "The selected managed analysis tool is unavailable.")
	}
	preparer := worker.preparer
	if preparer == nil {
		preparer = service.NewSourceAnalysisPreparer(config)
	}
	if targets&service.SourceAnalysisTargetFingerprint != 0 {
		request.ServerPath = filepath.Join(work.InventoryPath, filepath.FromSlash(work.RelativePath))
	}
	prepared := preparer.Prepare(ctx, request)
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		_ = rootDir.Close()
		return service.SourceAnalysisPreparation{}, nil, nil, nil, err
	}
	if err := worker.verifyAbsoluteSourceStillCurrent(ctx, work, file, rootDir); err != nil {
		_ = file.Close()
		_ = rootDir.Close()
		return service.SourceAnalysisPreparation{}, nil, nil, nil, worker.failStep(ctx, operation, execution, step, claims[step], jobID, "The source file changed. Start a new analysis.")
	}
	return prepared, work, file, rootDir, nil
}

func (worker *SourceAnalysisWorker) applyPreparedStep(ctx context.Context, operation *persistence.Operation, snapshot persistence.SourceAnalysisOperationSnapshot, jobID int64, execution persistence.SourceAnalysisExecution, attempt int, work *persistence.SourceAnalysisWork, step persistence.SourceStepName, prepared service.SourceAnalysisPreparation) (*[32]byte, error) {
	fence := func() persistence.SourceStepFailure {
		return persistence.SourceStepFailure{WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: jobID, StepAttempt: attempt, Step: step}
	}
	var err error
	switch step {
	case persistence.SourceStepSHA256:
		if prepared.SHA256.State != service.SourceAnalysisSucceeded {
			failure := fence()
			failure.SafeError = safeStepError(prepared.SHA256.SafeError, "The source digest could not be calculated.")
			return nil, worker.persistStepFailure(ctx, failure)
		}
		applied, applyErr := worker.repository.ApplySourceSHA256(ctx, persistence.SourceSHA256Apply{WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: jobID, StepAttempt: attempt, SHA256: prepared.SHA256.Digest[:], CalculatedAt: time.Now().UTC(), Algorithm: "sha256"})
		err = applyErr
		if err == nil && applied != nil && len(applied.SourceSHA256) == 32 {
			var digest [32]byte
			copy(digest[:], applied.SourceSHA256)
			return &digest, nil
		}
		if err == nil {
			err = fmt.Errorf("applied source digest is unavailable")
		}
	case persistence.SourceStepProbe:
		if prepared.Probe.State == service.SourceAnalysisFailed || prepared.Probe.Result == nil || prepared.Probe.Result.AnalysisPolicyVersion == nil || prepared.Probe.Result.FFProbeVersion == nil || prepared.Probe.Result.InspectedAt == nil {
			failure := fence()
			failure.SafeError = safeStepError(prepared.Probe.SafeError, "The technical analysis could not be completed.")
			if snapshot.CacheOnlyReuse != nil && *snapshot.CacheOnlyReuse {
				failure.SafeError = "No cached technical analysis is available for the selected version."
			}
			return nil, worker.persistStepFailure(ctx, failure)
		}
		result := prepared.Probe.Result
		if prepared.Probe.State == service.SourceAnalysisCacheHit {
			_, err = worker.repository.ReuseSourceProbe(ctx, persistence.SourceStepClaim{WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: jobID, Step: step}, attempt, result.ID, *result.FFProbeVersion, *result.AnalysisPolicyVersion)
			return nil, err
		}
		_, err = worker.repository.ApplySourceProbe(ctx, persistence.SourceProbeApply{WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: jobID, StepAttempt: attempt, SizeBytes: work.SizeBytes, AnalysisPolicy: *result.AnalysisPolicyVersion, FFProbeVersion: *result.FFProbeVersion, FFProbeJSON: result.FFProbeJSON, ObservedTags: result.ObservedTags, InspectedAt: *result.InspectedAt, AudioStreamCount: prepared.Probe.AudioStreamCount})
	case persistence.SourceStepFingerprint:
		if prepared.Fingerprint.State == service.SourceAnalysisFailed || prepared.Fingerprint.Result == nil {
			failure := fence()
			failure.SafeError = safeStepError(prepared.Fingerprint.SafeError, "The fingerprint could not be calculated.")
			if snapshot.CacheOnlyReuse != nil && *snapshot.CacheOnlyReuse {
				failure.SafeError = "No cached fingerprint is available for the selected version."
			}
			return nil, worker.persistStepFailure(ctx, failure)
		}
		if prepared.Fingerprint.State == service.SourceAnalysisCacheHit {
			_, err = worker.repository.ReuseSourceFingerprint(ctx, persistence.SourceStepClaim{WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: jobID, Step: step}, attempt, prepared.Fingerprint.Result.FPCalcVersion)
			return nil, err
		}
		_, err = worker.repository.ApplySourceFingerprint(ctx, persistence.SourceFingerprintApply{WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: jobID, StepAttempt: attempt, Result: *prepared.Fingerprint.Result})
	default:
		return nil, fmt.Errorf("unsupported source analysis step %q", step)
	}
	if err != nil {
		if errors.Is(err, persistence.ErrSourceAnalysisStale) {
			return nil, err
		}
		failure := fence()
		failure.SafeError = "The analysis result could not be saved. The previous result is unchanged."
		return nil, worker.persistStepFailure(ctx, failure)
	}
	return nil, nil
}

// verifyAbsoluteSourceStillCurrent checks both the pinned inventory handle and
// the configured absolute namespace. The former protects traversal; the latter
// detects replacement of the root path while a long-running preparer is active.
func (worker *SourceAnalysisWorker) verifyAbsoluteSourceStillCurrent(ctx context.Context, work *persistence.SourceAnalysisWork, original sourcefs.RegularFile, pinnedRoot sourcefs.Directory) error {
	if err := worker.verifySourceStillCurrent(ctx, work, original, pinnedRoot); err != nil {
		return err
	}
	freshRoot, err := worker.opener.OpenRoot(ctx, work.InventoryPath)
	if err != nil {
		return fmt.Errorf("reopen absolute inventory root: %w", err)
	}
	defer func() { _ = freshRoot.Close() }()
	rootInfo, rootErr := pinnedRoot.Stat(ctx)
	freshRootInfo, freshRootErr := freshRoot.Stat(ctx)
	if rootErr != nil || freshRootErr != nil || !os.SameFile(rootInfo, freshRootInfo) {
		return fmt.Errorf("absolute inventory root no longer names the analyzed directory")
	}
	freshFile, err := sourcefs.OpenRegularAt(ctx, freshRoot, work.RelativePath)
	if err != nil {
		return fmt.Errorf("reopen absolute source path: %w", err)
	}
	defer func() { _ = freshFile.Close() }()
	return original.Borrow(ctx, func(first *os.File) error {
		return freshFile.Borrow(ctx, func(second *os.File) error {
			firstInfo, firstErr := first.Stat()
			secondInfo, secondErr := second.Stat()
			if firstErr != nil || secondErr != nil || !os.SameFile(firstInfo, secondInfo) {
				return fmt.Errorf("absolute source path no longer names the analyzed file")
			}
			return nil
		})
	})
}

func groupSourceAnalysisExecutions(executions []persistence.SourceAnalysisExecution) [][]persistence.SourceAnalysisExecution {
	byWork := make(map[uuid.UUID][]persistence.SourceAnalysisExecution)
	for _, execution := range executions {
		byWork[execution.Work.ID] = append(byWork[execution.Work.ID], execution)
	}
	ids := make([]uuid.UUID, 0, len(byWork))
	for id := range byWork {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	groups := make([][]persistence.SourceAnalysisExecution, 0, len(ids))
	for _, id := range ids {
		group := byWork[id]
		sort.Slice(group, func(i, j int) bool {
			return stepOrder(persistence.SourceStepName(group[i].Step.Step)) < stepOrder(persistence.SourceStepName(group[j].Step.Step))
		})
		groups = append(groups, group)
	}
	return groups
}

func stepOrder(step persistence.SourceStepName) int {
	switch step {
	case persistence.SourceStepSHA256:
		return 0
	case persistence.SourceStepProbe:
		return 1
	case persistence.SourceStepFingerprint:
		return 2
	default:
		return 3
	}
}

func (worker *SourceAnalysisWorker) preparerConfig(ctx context.Context, snapshot persistence.SourceAnalysisOperationSnapshot, work *persistence.SourceAnalysisWork, operationID uuid.UUID, operationAttempt int, jobID int64, steps ...persistence.SourceStepName) (service.SourceAnalysisPreparerConfig, error) {
	config := service.SourceAnalysisPreparerConfig{Cache: worker.repository, AnalysisPolicy: persistence.SourceAnalysisPolicyVersion}
	needsProbe, needsFingerprint := false, false
	for _, step := range steps {
		needsProbe = needsProbe || step == persistence.SourceStepProbe
		needsFingerprint = needsFingerprint || step == persistence.SourceStepFingerprint
	}
	if !needsProbe && !needsFingerprint {
		return config, nil
	}
	if snapshot.CacheOnlyReuse != nil && *snapshot.CacheOnlyReuse {
		if needsProbe {
			config.FFProbeVersion = snapshot.CacheOnlyFFProbeVersion
			config.ProbeFactory = func(string) (service.SourceAnalysisProbe, error) {
				return nil, fmt.Errorf("cache-only reuse cannot execute ffprobe")
			}
		}
		if needsFingerprint {
			config.FPCalcVersion.Version = snapshot.CacheOnlyFPCalcVersion
			config.FingerprinterFactory = func(string) (service.SourceAnalysisFingerprinter, error) {
				return nil, fmt.Errorf("cache-only reuse cannot execute fpcalc")
			}
		}
		return config, nil
	}
	root, ok, err := worker.toolsDirectory.GetToolsDirectory(ctx)
	if err != nil || !ok || root == "" {
		return config, fmt.Errorf("managed tools root unavailable")
	}
	selected := make(map[persistence.SourceStepName]*persistence.SourceAnalysisToolSelection)
	for index := range snapshot.Tools {
		tool := &snapshot.Tools[index]
		if tool.Executable == "ffprobe" {
			selected[persistence.SourceStepProbe] = tool
		}
		if tool.Executable == "fpcalc" {
			selected[persistence.SourceStepFingerprint] = tool
		}
	}
	config.Hold = func(ctx context.Context, requested service.SourceAnalysisStep) (func(), error) {
		step := persistence.SourceStepName(requested)
		selection := selected[step]
		if selection == nil {
			return nil, fmt.Errorf("pinned tool selection is unavailable")
		}
		return func() {}, worker.repository.CheckNormalizedSourceAnalysisToolHold(ctx, operationID, selection.InstallationID, operationAttempt, jobID, string(step))
	}
	if needsProbe {
		selection := selected[persistence.SourceStepProbe]
		config.ProbeFactory = func(string) (service.SourceAnalysisProbe, error) {
			return nil, fmt.Errorf("pinned ffprobe selection is unavailable")
		}
		if selection == nil {
			if !needsFingerprint {
				return config, fmt.Errorf("pinned ffprobe selection is unavailable")
			}
		} else {
			config.FFProbeVersion = selection.Version
			base := filepath.Join(root, filepath.FromSlash(selection.RelativePath))
			executable := filepath.Join(base, selection.Executable)
			if worker.platform.Platform.GOOS == "windows" {
				executable += ".exe"
			}
			if filepath.IsAbs(executable) && pathInside(root, executable) {
				config.ProbeExecutable = executable
				config.ProbeFactory = func(path string) (service.SourceAnalysisProbe, error) {
					verify := worker.verifyFFProbeVersion
					if verify == nil {
						verify = func(ctx context.Context, executable string) (string, error) {
							output, err := (tools.SystemRunner{}).Run(ctx, executable, "-version")
							return strings.TrimSpace(string(output)), err
						}
					}
					banner, err := verify(ctx, path)
					if err != nil || !pinnedFFProbeVersionMatches(banner, selection.Version) {
						return nil, fmt.Errorf("managed ffprobe version does not match pinned selection")
					}
					return tools.NewFFProbe(path)
				}
			} else if !needsFingerprint {
				return config, fmt.Errorf("pinned ffprobe path is invalid")
			}
		}
		config.ProbeResult = persistence.SourceMediaVariant{ID: uuid.New(), SizeBytes: work.SizeBytes, AppliedOperationID: uuidPointer(operationID), ObservedTags: []byte(`{}`)}
	}
	if needsFingerprint {
		selection := selected[persistence.SourceStepFingerprint]
		config.FingerprinterFactory = func(string) (service.SourceAnalysisFingerprinter, error) {
			return nil, fmt.Errorf("pinned fpcalc selection is unavailable")
		}
		if selection == nil {
			if !needsProbe {
				return config, fmt.Errorf("pinned fpcalc selection is unavailable")
			}
		} else {
			// Preserve the selected version for a cache lookup; bad runner metadata must
			// only fail this step, and a cache hit does not need a tool invocation.
			config.FPCalcVersion.Version = selection.Version
			base := filepath.Join(root, filepath.FromSlash(selection.RelativePath))
			executable := filepath.Join(base, selection.Executable)
			if worker.platform.Platform.GOOS == "windows" {
				executable += ".exe"
			}
			version, parseErr := tools.ParseFPCalcVersion(selection.VersionBanner)
			if filepath.IsAbs(executable) && pathInside(root, executable) && parseErr == nil && version.Version == selection.Version {
				config.FPCalcExecutable, config.FPCalcVersion = executable, version
				config.FingerprinterFactory = func(path string) (service.SourceAnalysisFingerprinter, error) { return tools.NewFPCalc(path) }
				config.FingerprintResult = persistence.SourceFingerprintResult{ID: uuid.New(), VersionBanner: selection.VersionBanner, FPCalcVersion: selection.Version, AlgorithmNamespace: "chromaprint", ParserContractVersion: 1, AppliedOperationID: operationID}
			} else if !needsProbe {
				return config, fmt.Errorf("pinned fpcalc metadata is invalid")
			}
		}
	}
	return config, nil
}

func pinnedFFProbeVersionMatches(banner, version string) bool {
	if version == "" {
		return false
	}
	pattern := regexp.MustCompile(`(?:^|[^0-9A-Za-z.+-])` + regexp.QuoteMeta(version) + `(?:$|[^0-9A-Za-z.+-])`)
	return pattern.MatchString(banner)
}

func (worker *SourceAnalysisWorker) failStep(ctx context.Context, operation *persistence.Operation, execution persistence.SourceAnalysisExecution, step persistence.SourceStepName, attempt int, jobID int64, safe string) error {
	return worker.persistStepFailure(ctx, persistence.SourceStepFailure{WorkID: execution.Work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: jobID, StepAttempt: attempt, Step: step, SafeError: safe})
}

type sourceAnalysisStepFailure struct{ safeError string }

func (failure *sourceAnalysisStepFailure) Error() string { return failure.safeError }

func (worker *SourceAnalysisWorker) persistStepFailure(ctx context.Context, failure persistence.SourceStepFailure) error {
	if err := worker.repository.FailSourceAnalysisStep(ctx, failure); err != nil {
		return err
	}
	return &sourceAnalysisStepFailure{safeError: failure.SafeError}
}

func analysisMtime(value time.Time) time.Time { return value.UTC().Truncate(time.Microsecond) }

func (worker *SourceAnalysisWorker) verifySourceStillCurrent(ctx context.Context, work *persistence.SourceAnalysisWork, original sourcefs.RegularFile, root sourcefs.Directory) error {
	info, err := original.Stat(ctx)
	if err != nil || info.Size() != work.SizeBytes || analysisMtime(info.ModTime()) != analysisMtime(work.Mtime) {
		return fmt.Errorf("source descriptor changed")
	}
	reopened, err := sourcefs.OpenRegularAt(ctx, root, work.RelativePath)
	if err != nil {
		return fmt.Errorf("reopen source path: %w", err)
	}
	defer func() { _ = reopened.Close() }()
	return original.Borrow(ctx, func(first *os.File) error {
		return reopened.Borrow(ctx, func(second *os.File) error {
			firstInfo, firstErr := first.Stat()
			secondInfo, secondErr := second.Stat()
			if firstErr != nil || secondErr != nil || !os.SameFile(firstInfo, secondInfo) {
				return fmt.Errorf("source path no longer names the analyzed file")
			}
			return nil
		})
	})
}

func (worker *SourceAnalysisWorker) terminalFailure(ctx context.Context, operation *persistence.Operation, jobID int64, stage, safe string) error {
	if err := worker.repository.SettleNormalizedSourceAnalysisDelivery(ctx, operation.ID, persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: jobID}, "failed", stage, safe); err != nil {
		return err
	}
	worker.operations.Notify(operation.ID)
	worker.wakePending(ctx, operation)
	return nil
}

// wakePending admits the next pending batch for the operation's root after its
// terminal settlement committed. Admission is best-effort infrastructure: a
// downstream error is logged as a safe warning and never returned, so a
// committed terminal operation stays terminal and River does not rerun this
// delivery. A stale delivery never reaches here because its settlement fails.
func (worker *SourceAnalysisWorker) wakePending(ctx context.Context, operation *persistence.Operation) {
	if worker.pendingDispatcher == nil || operation == nil || operation.TargetSourceRootID == nil {
		return
	}
	if _, err := worker.pendingDispatcher.AdmitPending(context.WithoutCancel(ctx), *operation.TargetSourceRootID); err != nil {
		slog.WarnContext(ctx, "source analysis pending admission failed", "operation", operation.ID, "root", *operation.TargetSourceRootID)
	}
}

func (worker *SourceAnalysisWorker) ready(ctx context.Context) error {
	if worker.platform.Diagnostic || !worker.platform.Platform.Supported() {
		return fmt.Errorf("the instance platform is not usable")
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

func analysisTargets(step persistence.SourceStepName) service.SourceAnalysisTarget {
	switch step {
	case persistence.SourceStepSHA256:
		return service.SourceAnalysisTargetSHA256
	case persistence.SourceStepProbe:
		return service.SourceAnalysisTargetProbe
	case persistence.SourceStepFingerprint:
		return service.SourceAnalysisTargetFingerprint
	default:
		return 0
	}
}

func safeStepError(message, fallback string) string {
	if strings.TrimSpace(message) == "" {
		return fallback
	}
	return message
}

func uuidPointer(value uuid.UUID) *uuid.UUID { return &value }

func pathInside(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

var _ river.Worker[service.SourceAnalysisJobArgs] = (*SourceAnalysisWorker)(nil)
