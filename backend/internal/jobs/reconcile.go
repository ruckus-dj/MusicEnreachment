package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	MarkInstallationReady(context.Context, uuid.UUID, json.RawMessage, time.Time) error
	MarkInstallationFailed(context.Context, uuid.UUID) error
	ActivateInstallationDuringSetup(context.Context, uuid.UUID, string, string, string, string) (bool, error)
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
		operation := &active[index]
		live := false
		if operation.RiverJobID != nil {
			live, err = isLive(ctx, operation.RiverJobID)
			if err != nil {
				return fmt.Errorf("check River job for operation %s: %w", operation.ID, err)
			}
		}
		if live {
			continue
		}
		if operation.Kind == service.SourceScanOperationKind {
			if err := recoverInterruptedSourceScan(ctx, repository, operations, operation); err != nil {
				return err
			}
			continue
		}
		if operation.Kind == service.SourceAnalysisOperationKind {
			if err := recoverInterruptedSourceAnalysis(ctx, repository, operation); err != nil {
				return err
			}
			continue
		}
		if operation.Kind == "install" {
			var snapshot service.InstallInputSnapshot
			if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil || snapshot.SchemaVersion != 2 || snapshot.ToolsRoot == "" {
				if operation.TargetInstallationID != nil {
					if err := markPreparingInstallationFailed(ctx, repository, *operation.TargetInstallationID); err != nil {
						return fmt.Errorf("mark legacy installation failed: %w", err)
					}
				}
				if err := operations.Fail(ctx, operation.ID, operation.Stage, "The installation snapshot predates tools-directory pinning and cannot be safely recovered. Start a new installation."); err != nil {
					return fmt.Errorf("fail installation without a pinned tools directory: %w", err)
				}
				continue
			}
			currentRoot, exists, err := runtimeSettings.GetToolsDirectory(ctx)
			if err != nil {
				return fmt.Errorf("read tools directory while recovering installation %s: %w", operation.ID, err)
			}
			if !exists || currentRoot != snapshot.ToolsRoot {
				if operation.TargetInstallationID != nil {
					if err := markPreparingInstallationFailed(ctx, repository, *operation.TargetInstallationID); err != nil {
						return fmt.Errorf("mark stale-root installation failed: %w", err)
					}
				}
				if err := operations.Fail(ctx, operation.ID, operation.Stage, "The tools directory changed since this installation was queued. Start a new installation."); err != nil {
					return fmt.Errorf("fail installation with a stale tools directory: %w", err)
				}
				continue
			}
			publicationExists, err := hasInstallPublicationEvidence(ctx, operation, runtimeSettings)
			if errors.Is(err, errInvalidInstallPublicationEvidence) {
				if err := failInvalidInstallPublication(ctx, repository, operations, operation); err != nil {
					return fmt.Errorf("fail operation with invalid publication evidence: %w", err)
				}
				continue
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
					continue
				}
				if err != nil {
					return fmt.Errorf("recover published installation %s: %w", operation.ID, err)
				}
				if completed {
					continue
				}
			}
		}

		preserveMoveStaging := operation.Kind == "move_tools_root" && moveNeedsRollbackOnRetry(operation.Stage)
		root, err := interruptedOperationStagingRoot(ctx, operation, runtimeSettings)
		if err != nil {
			return fmt.Errorf("resolve staging root for operation %s: %w", operation.ID, err)
		}
		if root != "" && !preserveMoveStaging {
			if err := tools.CleanupOperationStaging(root, operation.ID); err != nil {
				return fmt.Errorf("clean interrupted operation %s staging: %w", operation.ID, err)
			}
		}
		if operation.TargetInstallationID != nil {
			if err := markPreparingInstallationFailed(ctx, repository, *operation.TargetInstallationID); err != nil {
				return fmt.Errorf("mark interrupted installation failed: %w", err)
			}
		}
		safeError := "The operation was interrupted. Retry the operation."
		if operation.Kind == "move_tools_root" && moveWasSwitched(operation.Stage) {
			safeError = "The tools directory move was interrupted after switching roots. Retry the move to restore the previous tools directory."
		}
		if err := operations.Fail(ctx, operation.ID, operation.Stage, safeError); err != nil {
			return fmt.Errorf("mark interrupted operation failed: %w", err)
		}
	}
	return nil
}

func markPreparingInstallationFailed(ctx context.Context, repository interruptedOperationRepository, id uuid.UUID) error {
	installation, err := repository.GetInstallation(ctx, id)
	if err != nil {
		return err
	}
	if installation.State != "preparing" {
		return nil
	}
	return repository.MarkInstallationFailed(ctx, id)
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
	if operation.TargetInstallationID != nil {
		installation, err := repository.GetInstallation(ctx, *operation.TargetInstallationID)
		if err != nil {
			return err
		}
		if installation.State == "preparing" {
			if err := repository.MarkInstallationFailed(ctx, installation.ID); err != nil {
				return err
			}
		}
	}
	return operations.Fail(ctx, operation.ID, operation.Stage,
		"The interrupted tool operation has invalid publication evidence. Resolve the installation before retrying the operation.")
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
	if installation.State != "ready" {
		versionsJSON, err := json.Marshal(versions)
		if err != nil {
			return false, err
		}
		if err := repository.MarkInstallationReady(ctx, installation.ID, versionsJSON, time.Now().UTC()); err != nil {
			return false, err
		}
	}
	setupComplete, err := runtimeSettings.SetupCompleted(ctx)
	if err != nil {
		return false, err
	}
	if !setupComplete {
		activeSetting := settings.ActiveFFmpegInstallationKey
		if snapshot.PackageKind == tools.PackageFPCalc {
			activeSetting = settings.ActiveFPCalcInstallationKey
		}
		if _, err := repository.ActivateInstallationDuringSetup(ctx, installation.ID, string(snapshot.PackageKind), installation.PlatformGOOS, installation.PlatformGOARCH, activeSetting); err != nil {
			return false, err
		}
	}
	if err := tools.CleanupOperationStaging(root, operation.ID); err != nil {
		return false, err
	}
	if err := operations.Succeed(ctx, operation.ID, "succeeded"); err != nil {
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
