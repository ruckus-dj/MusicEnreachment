package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
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
}

type riverJobLiveness func(context.Context, *int64) (bool, error)

// ReconcileInterruptedOperations marks queued/running operations whose River
// delivery is not live as retryable failures and removes their private staging.
// The caller runs this before starting River workers, so persisted running jobs
// belong to a previous process and are not mistaken for live work.
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
		if operation.Kind == "install" && operationStageAfterRetries(operation.Stage) == "files_materialized" {
			completed, err := reconcileMaterializedInstallation(ctx, repository, operations, operation, runtimeSettings, lifecycle)
			if err != nil {
				return fmt.Errorf("recover published installation %s: %w", operation.ID, err)
			}
			if completed {
				continue
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
			if err := repository.MarkInstallationFailed(ctx, *operation.TargetInstallationID); err != nil {
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

func reconcileMaterializedInstallation(ctx context.Context, repository interruptedOperationRepository, operations *service.Operations, operation *persistence.Operation, runtimeSettings interruptedOperationSettings, lifecycle *tools.Lifecycle) (bool, error) {
	if operation.TargetInstallationID == nil {
		return false, fmt.Errorf("materialized install operation has no target installation")
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
		return false, fmt.Errorf("tools directory is not configured")
	}
	var snapshot service.InstallInputSnapshot
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
		return false, fmt.Errorf("decode installation snapshot: %w", err)
	}
	if snapshot.SchemaVersion != 1 || snapshot.PackageKind != tools.PackageKind(installation.PackageKind) ||
		snapshot.SourceName != installation.SourceName || snapshot.ReleaseIdentity != installation.ReleaseIdentity {
		return false, fmt.Errorf("installation snapshot does not match target")
	}
	staging := filepath.Join(root, ".staging", operation.ID.String())
	publication, err := loadInstallPublication(staging, operation, installation, snapshot, root, installation.PlatformGOOS)
	if err != nil {
		return false, err
	}
	if installation.State != "ready" && publication == nil {
		return false, fmt.Errorf("materialized installation ownership journal is missing")
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
