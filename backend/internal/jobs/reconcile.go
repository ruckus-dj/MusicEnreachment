package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type interruptedOperationRepository interface {
	ListOperations(context.Context, ...string) ([]persistence.Operation, error)
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
	FinalizeInstallation(context.Context, uuid.UUID, int, int64, uuid.UUID, string, string, string, string, json.RawMessage, time.Time) (bool, error)
	FailInstallationDelivery(context.Context, uuid.UUID, int, int64, uuid.UUID, string, string) (bool, error)
}

type orphanedInstallationRecovery interface {
	FailOrphanedInstallationRecovery(context.Context, uuid.UUID, int, uuid.UUID, string, string) (bool, error)
}

type undeliveredInstallationRecovery interface {
	FailUndeliveredInstallationRecovery(context.Context, uuid.UUID, int, json.RawMessage, string, string) (bool, error)
}

type interruptedOperationSettings interface {
	GetToolsDirectory(context.Context) (string, bool, error)
	SetupCompleted(context.Context) (bool, error)
}

var errInvalidInstallPublicationEvidence = errors.New("invalid installation publication evidence")

// analysisSafeInterrupted is the retryable reason startup recovery records for a
// normalized analysis whose process stopped before its result committed. The
// current recovery fails the orphaned delivery and releases both read holds in
// the same transaction, so the operation can be retried without a partially
// published variant.

type riverJobLiveness func(context.Context, *int64) (bool, error)

// interruptedSourceScanRecovery is the scan-specific half of startup recovery:
// it reports whether the generation of an interrupted scan was already applied
// to its root and drops the private candidates of one that was not. The
// repository the composition root passes implements it; the recovery of an
// install or a tools-root move never asks for it.
type interruptedSourceScanRecovery interface {
	RecoverInterruptedSourceScan(context.Context, uuid.UUID) (bool, error)
}

// interruptedSourceAnalysisRecovery is the analysis-specific half of startup
// recovery: the successful apply commits the variant, the location link and the
// succeeded state in one transaction, so an analysis still queued or running
// when its delivery is gone never published a result. Recovery fails it with the
// caller's safe reason while releasing both read holds in the same transaction.
type interruptedSourceAnalysisRecovery interface {
	RecoverInterruptedSourceAnalysis(context.Context, uuid.UUID, string) error
}

type interruptedSourceAnalysisArtifactCleanupRecovery interface {
	RecoverInterruptedSourceAnalysisArtifactCleanupAtStartup(context.Context, uuid.UUID, int, int64) error
}

// ReconcileInterruptedOperations marks queued/running operations whose River
// delivery is not live as retryable failures and removes their private staging.
// An interrupted scan is resolved against its root first: a generation the root
// already records as applied is finished as succeeded without being applied
// again, and a scan that never applied drops its private candidates and stays
// retryable, while both leave the previous inventory untouched. The caller runs
// this before starting River workers, so persisted running jobs belong to a
// previous process and are not mistaken for live work.
func ReconcileInterruptedOperations(ctx context.Context, repository interruptedOperationRepository, operations *service.Operations, isLive riverJobLiveness, runtimeSettings interruptedOperationSettings, lifecycles ...*tools.Lifecycle) error {
	lifecycle := tools.NewLifecycle(nil)
	if len(lifecycles) > 0 && lifecycles[0] != nil {
		lifecycle = lifecycles[0]
	}
	active, err := repository.ListOperations(ctx, "queued", "running")
	if err != nil {
		return fmt.Errorf("list active operations for recovery: %w", err)
	}
	for index := range active {
		if err := func() (recoveryErr error) {
			operation := &active[index]
			var unlock func()
			if operation.Kind == "install" {
				unlock = lockInstallationExecution(operation.ID)
				defer func() {
					if unlock != nil {
						unlock()
					}
				}()
				operation, err = repository.GetOperation(ctx, operation.ID)
				if err != nil {
					return fmt.Errorf("reload installation %s for recovery: %w", active[index].ID, err)
				}
			}
			live := false
			if operation.RiverJobID != nil {
				live, err = isLive(ctx, operation.RiverJobID)
				if err != nil {
					return fmt.Errorf("check River job for operation %s: %w", operation.ID, err)
				}
			}
			if live {
				return nil
			}
			if (operation.Kind == "install" || operation.Kind == "move_tools_root") && operation.RiverJobID != nil {
				recoveryOperation := *operation
				preserveMoveStaging := operation.Kind == "move_tools_root" && moveNeedsRollbackOnRetry(operation.Stage)
				defer func() {
					if recoveryErr != nil || preserveMoveStaging {
						if claims, ok := repository.(interface {
							AbandonToolsExecutionDelivery(context.Context, uuid.UUID, int, int64, string) error
						}); ok {
							recoveryErr = errors.Join(recoveryErr, claims.AbandonToolsExecutionDelivery(ctx, recoveryOperation.ID, recoveryOperation.Attempt, *recoveryOperation.RiverJobID, recoveryOperation.Kind))
						}
					} else {
						recoveryErr = operations.ReleaseToolsExecutionDelivery(ctx, &recoveryOperation, *recoveryOperation.RiverJobID)
					}
				}()
			}
			if operation.Kind == service.SourceScanOperationKind {
				if err := recoverInterruptedSourceScan(ctx, repository, operations, operation); err != nil {
					return err
				}
				return nil
			}
			if operation.Kind == service.SourceAnalysisOperationKind {
				if err := recoverInterruptedSourceAnalysis(ctx, repository, operation); err != nil {
					return err
				}
				return nil
			}
			if operation.Kind == persistence.SourceAnalysisArtifactCleanupOperationKind {
				recovery, ok := repository.(interruptedSourceAnalysisArtifactCleanupRecovery)
				if !ok || operation.RiverJobID == nil {
					return fmt.Errorf("recover source analysis artifact cleanup: exact-fence persistence recovery is unavailable")
				}
				if err := recovery.RecoverInterruptedSourceAnalysisArtifactCleanupAtStartup(ctx, operation.ID, operation.Attempt, *operation.RiverJobID); err != nil {
					return fmt.Errorf("recover source analysis artifact cleanup %s: %w", operation.ID, err)
				}
				return nil
			}
			if operation.Kind == "install" {
				var snapshot service.InstallInputSnapshot
				if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil || snapshot.SchemaVersion != 2 || snapshot.ToolsRoot == "" {
					_, err := failInterruptedInstallation(ctx, repository, operations, operation, "The installation snapshot predates tools-directory pinning and cannot be safely recovered. Start a new installation.")
					if err != nil {
						return fmt.Errorf("fail installation without a pinned tools directory: %w", err)
					}
					return nil
				}
				currentRoot, exists, err := runtimeSettings.GetToolsDirectory(ctx)
				if err != nil {
					return fmt.Errorf("read tools directory while recovering installation %s: %w", operation.ID, err)
				}
				if !exists || currentRoot != snapshot.ToolsRoot {
					if _, err := failInterruptedInstallation(ctx, repository, operations, operation, "The tools directory changed since this installation was queued. Start a new installation."); err != nil {
						return fmt.Errorf("fail installation with a stale tools directory: %w", err)
					}
					return nil
				}
				publicationExists, err := hasInstallPublicationEvidence(ctx, operation, runtimeSettings)
				if errors.Is(err, errInvalidInstallPublicationEvidence) {
					if err := failInvalidInstallPublication(ctx, repository, operations, operation); err != nil {
						return fmt.Errorf("fail operation with invalid publication evidence: %w", err)
					}
					return nil
				}
				if err != nil {
					return fmt.Errorf("inspect installation publication for %s: %w", operation.ID, err)
				}
				if publicationExists || operationStageAfterRetries(operation.Stage) == "files_materialized" {
					completed, err := reconcileMaterializedInstallation(ctx, repository, operations, operation, runtimeSettings, lifecycle)
					if errors.Is(err, errInvalidInstallPublicationEvidence) {
						if err := failInvalidInstallPublication(ctx, repository, operations, operation); err != nil {
							return fmt.Errorf("fail operation with invalid publication evidence: %w", err)
						}
						return nil
					}
					if err != nil {
						return fmt.Errorf("recover published installation %s: %w", operation.ID, err)
					}
					if completed {
						return nil
					}
				}
				if !publicationExists && operationStageAfterRetries(operation.Stage) != "files_materialized" {
					staging := filepath.Join(snapshot.ToolsRoot, ".staging", operation.ID.String())
					if err := refuseUnjournaledInstallBackups(staging); err != nil {
						return fmt.Errorf("refuse unsafe installation staging for %s: %w", operation.ID, err)
					}
					if err := tools.CleanupOperationStaging(snapshot.ToolsRoot, operation.ID); err != nil {
						return fmt.Errorf("clean interrupted installation %s staging: %w", operation.ID, err)
					}
				}
			}
			preserveMoveStaging := operation.Kind == "move_tools_root" && moveNeedsRollbackOnRetry(operation.Stage)
			root, err := interruptedOperationStagingRoot(ctx, operation, runtimeSettings)
			if err != nil {
				return fmt.Errorf("resolve staging root for operation %s: %w", operation.ID, err)
			}
			if root != "" && !preserveMoveStaging && operation.Kind != "install" {
				if err := tools.CleanupOperationStaging(root, operation.ID); err != nil {
					return fmt.Errorf("clean interrupted operation %s staging: %w", operation.ID, err)
				}
			}
			safeError := "The operation was interrupted. Retry the operation."
			if operation.Kind == "move_tools_root" && moveWasSwitched(operation.Stage) {
				safeError = "The tools directory move was interrupted after switching roots. Retry the move to restore the previous tools directory."
			}
			if operation.Kind == "install" {
				if _, err := failInterruptedInstallation(ctx, repository, operations, operation, safeError); err != nil {
					return fmt.Errorf("mark interrupted installation failed: %w", err)
				}
				return nil
			}
			if err := operations.Fail(ctx, operation.ID, operation.Stage, safeError); err != nil {
				return fmt.Errorf("mark interrupted operation failed: %w", err)
			}
			return nil
		}(); err != nil {
			return err
		}
	}
	if err := cleanupTerminalInstallations(ctx, repository, operations, runtimeSettings); err != nil {
		return err
	}
	if err := cleanupTerminalMoves(ctx, repository, operations); err != nil {
		return err
	}
	return nil
}

func cleanupTerminalMoves(ctx context.Context, repository interruptedOperationRepository, operations *service.Operations) error {
	terminal, err := repository.ListOperations(ctx, "succeeded", "failed")
	if err != nil {
		return fmt.Errorf("list terminal tools moves for cleanup: %w", err)
	}
	for index := range terminal {
		listed := &terminal[index]
		if listed.Kind != "move_tools_root" {
			continue
		}
		unlock := lockInstallationExecution(listed.ID)
		operation, err := repository.GetOperation(ctx, listed.ID)
		if err != nil {
			unlock()
			return fmt.Errorf("reload terminal tools move %s: %w", listed.ID, err)
		}
		if operation.State != "succeeded" && operation.State != "failed" {
			unlock()
			continue
		}
		var snapshot service.MoveSnapshot
		if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil || snapshot.NewRoot == "" {
			unlock()
			return fmt.Errorf("invalid succeeded move snapshot for cleanup")
		}
		if operation.State == "failed" && moveNeedsRollbackOnRetry(operation.Stage) {
			if operation.RiverJobID != nil {
				if claims, ok := repository.(interface {
					AbandonToolsExecutionDelivery(context.Context, uuid.UUID, int, int64, string) error
				}); ok {
					if err := claims.AbandonToolsExecutionDelivery(ctx, operation.ID, operation.Attempt, *operation.RiverJobID, operation.Kind); err != nil {
						unlock()
						return fmt.Errorf("retain unresolved failed move %s for retry: %w", operation.ID, err)
					}
				}
			}
			unlock()
			continue
		}
		if err := cleanupOldSourceRestoreStaging(snapshot, operation.ID); err != nil {
			if operation.RiverJobID != nil {
				if claims, ok := repository.(interface {
					AbandonToolsExecutionDelivery(context.Context, uuid.UUID, int, int64, string) error
				}); ok {
					err = errors.Join(err, claims.AbandonToolsExecutionDelivery(ctx, operation.ID, operation.Attempt, *operation.RiverJobID, operation.Kind))
				}
			}
			unlock()
			return fmt.Errorf("clean succeeded move restore staging %s: %w", operation.ID, err)
		}
		if err := tools.CleanupOperationStaging(snapshot.NewRoot, operation.ID); err != nil {
			if operation.RiverJobID != nil {
				if claims, ok := repository.(interface {
					AbandonToolsExecutionDelivery(context.Context, uuid.UUID, int, int64, string) error
				}); ok {
					err = errors.Join(err, claims.AbandonToolsExecutionDelivery(ctx, operation.ID, operation.Attempt, *operation.RiverJobID, operation.Kind))
				}
			}
			unlock()
			return fmt.Errorf("clean succeeded move staging %s: %w", operation.ID, err)
		}
		if operation.RiverJobID != nil {
			if err := operations.ReleaseToolsExecutionDelivery(ctx, operation, *operation.RiverJobID); err != nil {
				unlock()
				return fmt.Errorf("release recovered succeeded move %s: %w", operation.ID, err)
			}
		}
		unlock()
	}
	return nil
}

func cleanupTerminalInstallations(ctx context.Context, repository interruptedOperationRepository, operations *service.Operations, runtimeSettings interruptedOperationSettings) error {
	terminal, err := repository.ListOperations(ctx, "succeeded", "failed")
	if err != nil {
		return fmt.Errorf("list terminal installations for cleanup: %w", err)
	}
	for index := range terminal {
		listed := &terminal[index]
		if listed.Kind != "install" {
			continue
		}
		unlock := lockInstallationExecution(listed.ID)
		operation, err := repository.GetOperation(ctx, listed.ID)
		if err != nil {
			unlock()
			return fmt.Errorf("reload terminal installation %s: %w", listed.ID, err)
		}
		if operation.State != "succeeded" && operation.State != "failed" {
			unlock()
			continue
		}
		worker := &InstallationWorker{repository: repository, platform: tools.Platform{}}
		// Installation metadata determines platform identity during terminal
		// cleanup; no executable is verified or activated here.
		if operation.TargetInstallationID != nil {
			installation, getErr := repository.GetInstallation(ctx, *operation.TargetInstallationID)
			if getErr != nil {
				unlock()
				return fmt.Errorf("load terminal installation target %s: %w", operation.ID, getErr)
			}
			worker.platform = tools.Platform{GOOS: installation.PlatformGOOS, GOARCH: installation.PlatformGOARCH}
		}
		getRoot, exists, rootErr := runtimeSettings.GetToolsDirectory(ctx)
		if rootErr != nil {
			unlock()
			return fmt.Errorf("read tools directory for terminal installation %s: %w", operation.ID, rootErr)
		}
		var snapshot service.InstallInputSnapshot
		if exists && getRoot != "" && json.Unmarshal(operation.InputSnapshot, &snapshot) == nil && snapshot.ToolsRoot == getRoot {
			if cleanupErr := worker.cleanupTerminalInstallation(ctx, operation); cleanupErr != nil {
				unlock()
				if errors.Is(cleanupErr, errInvalidInstallPublicationEvidence) {
					slog.WarnContext(ctx, "terminal installation staging was preserved because its publication evidence is invalid",
						"operation", operation.ID)
					continue
				}
				return fmt.Errorf("clean terminal installation %s: %w", operation.ID, cleanupErr)
			}
			if operation.RiverJobID != nil {
				if err := operations.ReleaseToolsExecutionDelivery(ctx, operation, *operation.RiverJobID); err != nil {
					unlock()
					return fmt.Errorf("release recovered terminal installation %s: %w", operation.ID, err)
				}
			}
		}
		unlock()
	}
	return nil
}

func failInterruptedInstallation(ctx context.Context, repository interruptedOperationRepository, operations *service.Operations, operation *persistence.Operation, safe string) (bool, error) {
	if operation.TargetInstallationID == nil {
		if operation.RiverJobID != nil {
			return false, fmt.Errorf("installation delivery identity is unavailable")
		}
		recovery, ok := repository.(undeliveredInstallationRecovery)
		if !ok {
			return false, fmt.Errorf("undelivered installation recovery is unavailable")
		}
		changed, err := recovery.FailUndeliveredInstallationRecovery(ctx, operation.ID, operation.Attempt, operation.InputSnapshot, operation.Stage, safe)
		if err != nil {
			return false, err
		}
		if changed {
			operations.Notify(operation.ID)
		}
		return changed, nil
	}
	var changed bool
	var err error
	if operation.RiverJobID == nil {
		recovery, ok := repository.(orphanedInstallationRecovery)
		if !ok {
			return false, fmt.Errorf("installation recovery identity is unavailable")
		}
		changed, err = recovery.FailOrphanedInstallationRecovery(ctx, operation.ID, operation.Attempt, *operation.TargetInstallationID, operation.Stage, safe)
	} else {
		changed, err = repository.FailInstallationDelivery(ctx, operation.ID, operation.Attempt, *operation.RiverJobID, *operation.TargetInstallationID, operation.Stage, safe)
	}
	if err != nil {
		return false, err
	}
	if changed {
		operations.Notify(operation.ID)
	}
	return changed, nil
}

// recoverInterruptedSourceScan finishes one orphaned scan. A generation that the
// durable root already records as applied is marked succeeded without applying
// it again and without touching its locations; a scan that never applied is
// failed with a retryable reason after its private candidates were dropped. The
// previous inventory is untouched by either outcome.
func recoverInterruptedSourceScan(ctx context.Context, repository interruptedOperationRepository, operations *service.Operations, operation *persistence.Operation) error {
	recovery, ok := repository.(interruptedSourceScanRecovery)
	if !ok {
		return fmt.Errorf("recover source scan %s: the repository does not support source scan recovery", operation.ID)
	}
	applied, err := recovery.RecoverInterruptedSourceScan(ctx, operation.ID)
	if err != nil {
		return fmt.Errorf("recover source scan %s: %w", operation.ID, err)
	}
	if applied {
		return operations.Succeed(ctx, operation.ID, scanSucceededStage)
	}
	return operations.Fail(ctx, operation.ID, operation.Stage, scanSafeInterrupted)
}

// recoverInterruptedSourceAnalysis resolves one orphaned analysis. The terminal
// failure and the release of both read holds happen inside the persistence
// method, so recovery cannot leave a failed analysis holding a variant or an
// installation; an operation that already succeeded is not in the reconcile set
// and is left untouched.
func recoverInterruptedSourceAnalysis(ctx context.Context, repository interruptedOperationRepository, operation *persistence.Operation) error {
	recovery, ok := repository.(interruptedSourceAnalysisRecovery)
	if !ok {
		return fmt.Errorf("recover source analysis %s: the repository does not support source analysis recovery", operation.ID)
	}
	if err := recovery.RecoverInterruptedSourceAnalysis(ctx, operation.ID, analysisSafeInterrupted); err != nil {
		return fmt.Errorf("recover source analysis %s: %w", operation.ID, err)
	}
	return nil
}

func operationStageAfterRetries(stage string) string {
	for strings.HasPrefix(stage, "retry:") {
		stage = strings.TrimPrefix(stage, "retry:")
	}
	return stage
}

func moveNeedsRollbackOnRetry(stage string) bool {
	switch operationStageAfterRetries(stage) {
	case "commit_targets", "prepare_restore", "switch", "switched", "rollback_pending":
		return true
	default:
		return false
	}
}

func moveWasSwitched(stage string) bool {
	switch operationStageAfterRetries(stage) {
	case "switched", "rollback_pending":
		return true
	default:
		return false
	}
}

func moveNeedsPreSwitchRollbackOnRetry(stage string) bool {
	switch operationStageAfterRetries(stage) {
	case "commit_targets", "prepare_restore", "switch":
		return true
	default:
		return false
	}
}

func hasInstallPublicationEvidence(ctx context.Context, operation *persistence.Operation, runtimeSettings interruptedOperationSettings) (bool, error) {
	root, exists, err := runtimeSettings.GetToolsDirectory(ctx)
	if err != nil || !exists || root == "" {
		return false, err
	}
	path := filepath.Join(root, ".staging", operation.ID.String(), "publication.json")
	_, err = os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: %w", errInvalidInstallPublicationEvidence, err)
	}
	return true, nil
}

func failInvalidInstallPublication(ctx context.Context, repository interruptedOperationRepository, operations *service.Operations, operation *persistence.Operation) error {
	_, err := failInterruptedInstallation(ctx, repository, operations, operation,
		"The interrupted tool operation has invalid publication evidence. Resolve the installation before retrying the operation.")
	return err
}

func reconcileMaterializedInstallation(ctx context.Context, repository interruptedOperationRepository, operations *service.Operations, operation *persistence.Operation, runtimeSettings interruptedOperationSettings, lifecycle *tools.Lifecycle) (bool, error) {
	filesMaterialized := operationStageAfterRetries(operation.Stage) == "files_materialized"
	if operation.TargetInstallationID == nil {
		if !filesMaterialized {
			return false, nil
		}
		return false, fmt.Errorf("%w: materialized install operation has no target installation", errInvalidInstallPublicationEvidence)
	}
	installation, err := repository.GetInstallation(ctx, *operation.TargetInstallationID)
	if err != nil {
		return false, err
	}
	root, exists, err := runtimeSettings.GetToolsDirectory(ctx)
	if err != nil {
		return false, err
	}
	if !exists || root == "" {
		if !filesMaterialized {
			return false, nil
		}
		return false, fmt.Errorf("tools directory is not configured")
	}
	var snapshot service.InstallInputSnapshot
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
		return false, fmt.Errorf("%w: decode installation snapshot: %w", errInvalidInstallPublicationEvidence, err)
	}
	if snapshot.SchemaVersion != 2 || snapshot.ToolsRoot == "" || snapshot.ToolsRoot != root || snapshot.PackageKind != tools.PackageKind(installation.PackageKind) ||
		snapshot.SourceName != installation.SourceName || snapshot.ReleaseIdentity != installation.ReleaseIdentity {
		return false, fmt.Errorf("%w: installation snapshot does not match target", errInvalidInstallPublicationEvidence)
	}
	staging := filepath.Join(root, ".staging", operation.ID.String())
	publication, err := loadInstallPublication(staging, operation, installation, snapshot, root, installation.PlatformGOOS)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errInvalidInstallPublicationEvidence, err)
	}
	if operation.State == "queued" && publication != nil {
		worker := &InstallationWorker{repository: repository, platform: tools.Platform{GOOS: installation.PlatformGOOS, GOARCH: installation.PlatformGOARCH}}
		if err := worker.retireQueuedInstallStaging(ctx, operation, installation, root, staging, publication); err != nil {
			return false, err
		}
		staging, err = tools.EnsureOperationStaging(root, operation.ID)
		if err != nil {
			return false, err
		}
		publication = nil
	}
	if publication == nil && !filesMaterialized {
		return false, nil
	}
	if installation.State != "ready" && publication == nil {
		return false, fmt.Errorf("%w: materialized installation ownership journal is missing", errInvalidInstallPublicationEvidence)
	}
	worker := &InstallationWorker{
		repository: repository, operations: operations, platform: tools.Platform{
			GOOS: installation.PlatformGOOS, GOARCH: installation.PlatformGOARCH,
		}, lifecycle: lifecycle,
	}
	if publication != nil {
		if publication.Mode == "rollback" {
			return true, worker.rollbackInstallPublication(ctx, operation, installation, root, staging, publication)
		}
		if err := worker.publishInstallFiles(ctx, staging, publication); err != nil {
			if ctx.Err() != nil || !errors.Is(err, errInstallConflict) {
				return false, err
			}
			publication.Mode = "rollback"
			if err := saveInstallPublication(staging, publication); err != nil {
				return false, err
			}
			return true, worker.rollbackInstallPublication(ctx, operation, installation, root, staging, publication)
		}
	}
	versions, err := lifecycle.VerifyInstallation(ctx, root, installation.RelativePath, tools.PackageKind(installation.PackageKind), installation.ReleaseIdentity, installation.PlatformGOOS)
	if err != nil {
		if ctx.Err() != nil || publication == nil {
			return false, err
		}
		publication.Mode = "rollback"
		if err := saveInstallPublication(staging, publication); err != nil {
			return false, err
		}
		return true, worker.rollbackInstallPublication(ctx, operation, installation, root, staging, publication)
	}
	versionsJSON, err := json.Marshal(versions)
	if err != nil {
		return false, err
	}
	installation.ExecutableVersions = versionsJSON
	verifiedAt := time.Now().UTC()
	if installation.VerifiedAt != nil {
		verifiedAt = *installation.VerifiedAt
	}
	activeSetting := settings.ActiveFFmpegInstallationKey
	if snapshot.PackageKind == tools.PackageFPCalc {
		activeSetting = settings.ActiveFPCalcInstallationKey
	}
	if operation.RiverJobID == nil {
		return false, fmt.Errorf("installation recovery: River job identity is unavailable")
	}
	finalized, err := repository.FinalizeInstallation(ctx, operation.ID, operation.Attempt, *operation.RiverJobID,
		installation.ID, string(snapshot.PackageKind), installation.PlatformGOOS, installation.PlatformGOARCH,
		activeSetting, versionsJSON, verifiedAt)
	if err != nil {
		return false, err
	}
	if !finalized {
		// A concurrent retry now owns the operation's shared staging directory.
		return true, nil
	}
	operations.Notify(operation.ID)
	if err := tools.CleanupOperationStaging(root, operation.ID); err != nil {
		return false, err
	}
	return true, nil
}

func interruptedOperationStagingRoot(ctx context.Context, operation *persistence.Operation, runtimeSettings interruptedOperationSettings) (string, error) {
	if operation.Kind == "move_tools_root" {
		var snapshot struct {
			NewRoot string `json:"new_root"`
		}
		if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
			return "", fmt.Errorf("decode tools-root move snapshot: %w", err)
		}
		if snapshot.NewRoot == "" {
			return "", fmt.Errorf("tools-root move snapshot has no target root")
		}
		return snapshot.NewRoot, nil
	}
	root, exists, err := runtimeSettings.GetToolsDirectory(ctx)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	return root, nil
}
