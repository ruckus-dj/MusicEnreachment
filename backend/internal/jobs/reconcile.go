package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type interruptedOperationRepository interface {
	ListOperations(context.Context, ...string) ([]persistence.Operation, error)
	MarkInstallationFailed(context.Context, uuid.UUID) error
}

type interruptedOperationSettings interface {
	GetToolsDirectory(context.Context) (string, bool, error)
}

type riverJobLiveness func(context.Context, *int64) (bool, error)

// ReconcileInterruptedOperations marks queued/running operations whose River
// delivery is not live as retryable failures and removes their private staging.
// The caller runs this before starting River workers, so persisted running jobs
// belong to a previous process and are not mistaken for live work.
func ReconcileInterruptedOperations(ctx context.Context, repository interruptedOperationRepository, operations *service.Operations, isLive riverJobLiveness, runtimeSettings interruptedOperationSettings) error {
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

		root, err := interruptedOperationStagingRoot(ctx, operation, runtimeSettings)
		if err != nil {
			return fmt.Errorf("resolve staging root for operation %s: %w", operation.ID, err)
		}
		if root != "" {
			if err := tools.CleanupOperationStaging(root, operation.ID); err != nil {
				return fmt.Errorf("clean interrupted operation %s staging: %w", operation.ID, err)
			}
		}
		if operation.TargetInstallationID != nil {
			if err := repository.MarkInstallationFailed(ctx, *operation.TargetInstallationID); err != nil {
				return fmt.Errorf("mark interrupted installation failed: %w", err)
			}
		}
		if err := operations.Fail(ctx, operation.ID, operation.Stage, "The operation was interrupted. Retry the operation."); err != nil {
			return fmt.Errorf("mark interrupted operation failed: %w", err)
		}
	}
	return nil
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
