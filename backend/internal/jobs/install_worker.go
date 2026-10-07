package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type installRepository interface {
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
	FinalizeInstallation(context.Context, uuid.UUID, int, int64, uuid.UUID, string, string, string, string, json.RawMessage, time.Time) (bool, error)
	FailInstallationDelivery(context.Context, uuid.UUID, int, int64, uuid.UUID, string, string) (bool, error)
}

type installCatalog interface {
	Resolve(context.Context, tools.PackageKind, tools.Platform, string) (tools.Release, error)
	Download(context.Context, tools.PackageKind, tools.Platform, string, string, io.Writer, func(int64)) (tools.Artifact, int64, error)
	Checksum(context.Context, tools.PackageKind, tools.Platform, string, string) (string, error)
}

type setupSettings interface {
	GetToolsDirectory(context.Context) (string, bool, error)
}

type InstallationWorker struct {
	river.WorkerDefaults[service.OperationJobArgs]
	repository installRepository
	operations *service.Operations
	catalog    installCatalog
	settings   setupSettings
	platform   tools.Platform
	lifecycle  *tools.Lifecycle
	moveWorker *MoveWorker
}

func NewInstallationWorker(repository installRepository, operations *service.Operations, catalog installCatalog, runtimeSettings setupSettings, platform tools.Platform, lifecycle *tools.Lifecycle) *InstallationWorker {
	if lifecycle == nil {
		lifecycle = tools.NewLifecycle(nil)
	}
	return &InstallationWorker{
		repository: repository, operations: operations, catalog: catalog,
		settings: runtimeSettings, platform: platform, lifecycle: lifecycle,
	}
}

func (worker *InstallationWorker) SetMoveWorker(moveWorker *MoveWorker) {
	worker.moveWorker = moveWorker
}

func (worker *InstallationWorker) Work(ctx context.Context, job *river.Job[service.OperationJobArgs]) error {
	operationID := job.Args.OperationID
	operation, err := worker.repository.GetOperation(ctx, operationID)
	if err != nil {
		return err
	}
	if operation.Kind == "move_tools_root" {
		if worker.moveWorker == nil {
			return fmt.Errorf("tools root move worker is unavailable")
		}
		return worker.moveWorker.Work(ctx, operation)
	}
	if operation.Kind != "install" {
		return fmt.Errorf("operation is not an installation")
	}
	unlock := lockInstallationExecution(operationID)
	defer unlock()
	// The operation may have been retried while this delivery waited for the
	// process-local filesystem fence. All later decisions use durable state.
	operation, err = worker.repository.GetOperation(ctx, operationID)
	if err != nil {
		return err
	}
	if operation.Kind == "install" && (operation.RiverJobID == nil || *operation.RiverJobID != job.ID) {
		// A delivery from before a retry must not run against the newer attempt.
		return nil
	}
	if operation.State == "succeeded" || operation.State == "failed" {
		return worker.cleanupTerminalInstallation(ctx, operation)
	}
	if operation.Kind != "install" || operation.TargetInstallationID == nil {
		return fmt.Errorf("operation is not an installation")
	}
	installation, err := worker.repository.GetInstallation(ctx, *operation.TargetInstallationID)
	if err != nil {
		return err
	}
	var snapshot service.InstallInputSnapshot
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
		return worker.fail(ctx, operation, installation, "resolve", err)
	}
	if !validInstallSnapshot(snapshot, installation, worker.platform) {
		return worker.fail(ctx, operation, installation, "resolve", fmt.Errorf("installation snapshot does not match target"))
	}
	if _, err := validatedInstallToolsRoot(snapshot); err != nil {
		return worker.fail(ctx, operation, installation, "resolve", err)
	}
	root, exists, err := worker.settings.GetToolsDirectory(ctx)
	if err != nil {
		return err
	}
	if !exists || root == "" {
		return worker.failWithStaging(ctx, operation, installation, "materialize", fmt.Errorf("tools directory is unavailable"), snapshot.ToolsRoot)
	}
	if root != snapshot.ToolsRoot {
		return worker.failWithStaging(ctx, operation, installation, "materialize", fmt.Errorf("tools directory changed since installation was queued"), snapshot.ToolsRoot)
	}

	if installation.State == "ready" {
		versions, err := worker.lifecycle.VerifyInstallation(ctx, root, installation.RelativePath, snapshot.PackageKind, snapshot.ReleaseIdentity, worker.platform.GOOS)
		if err != nil {
			return worker.failWithStaging(ctx, operation, installation, "verify", err, snapshot.ToolsRoot)
		}
		installation.ExecutableVersions, err = json.Marshal(versions)
		if err != nil {
			return err
		}
		verifiedAt := time.Now().UTC()
		installation.VerifiedAt = &verifiedAt
		return worker.finish(ctx, operation, installation, root, snapshot)
	}
	staging, err := tools.EnsureOperationStaging(root, operation.ID)
	if err != nil {
		return err
	}
	publication, err := loadInstallPublication(staging, operation, installation, snapshot, root, worker.platform.GOOS)
	if err != nil {
		return err
	}
	if publication != nil {
		if operation.State == "queued" {
			if err := worker.retireQueuedInstallStaging(ctx, operation, installation, root, staging, publication); err != nil {
				return err
			}
			staging, err = tools.EnsureOperationStaging(root, operation.ID)
			if err != nil {
				return err
			}
		} else {
			return worker.resumeInstallPublication(ctx, operation, installation, snapshot, root, staging, publication)
		}
	}
	if operation.State == "queued" {
		if err := refuseUnjournaledInstallBackups(staging); err != nil {
			return err
		}
		if err := tools.CleanupOperationStaging(root, operation.ID); err != nil {
			return err
		}
	}
	if installation.State == "failed" && operation.State == "running" {
		return worker.fail(ctx, operation, installation, operation.Stage, fmt.Errorf("installation target is failed"))
	}
	if err := worker.operations.RunningForDelivery(ctx, operation, "resolve"); err != nil {
		return err
	}
	release, err := worker.catalog.Resolve(ctx, snapshot.PackageKind, worker.platform, snapshot.ReleaseIdentity)
	if err != nil {
		return worker.failWithStaging(ctx, operation, installation, "resolve", err, snapshot.ToolsRoot)
	}
	if !artifactIdentitiesMatch(snapshot.ArtifactIdentities, release.Artifacts) {
		return worker.failWithStaging(ctx, operation, installation, "resolve", fmt.Errorf("upstream artifact identity changed"), snapshot.ToolsRoot)
	}

	staging, err = tools.ResetOperationStaging(root, operation.ID)
	if err != nil {
		return worker.failWithStaging(ctx, operation, installation, "download", err, snapshot.ToolsRoot)
	}
	archivePaths := make([]string, 0, len(snapshot.ArtifactIdentities))
	var downloadedBytes int64
	for index, identity := range snapshot.ArtifactIdentities {
		if err := worker.operations.RunningForDelivery(ctx, operation, "download"); err != nil {
			return err
		}
		archivePath := filepath.Join(staging, fmt.Sprintf("artifact-%d", index))
		archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return worker.failWithStaging(ctx, operation, installation, "download", err, snapshot.ToolsRoot)
		}
		lastProgress := int64(0)
		artifact, count, downloadErr := worker.catalog.Download(ctx, snapshot.PackageKind, worker.platform, snapshot.ReleaseIdentity, identity.Name, archive, func(current int64) {
			if current-lastProgress >= 1<<20 {
				lastProgress = current
				_ = worker.operations.ProgressForDelivery(ctx, operation, "download", downloadedBytes+current, nil)
			}
		})
		closeErr := archive.Close()
		if downloadErr != nil {
			return worker.failWithStaging(ctx, operation, installation, "download", downloadErr, snapshot.ToolsRoot)
		}
		if closeErr != nil {
			return worker.failWithStaging(ctx, operation, installation, "download", closeErr, snapshot.ToolsRoot)
		}
		if artifact.Name != identity.Name {
			return worker.failWithStaging(ctx, operation, installation, "resolve", fmt.Errorf("resolved artifact identity changed"), snapshot.ToolsRoot)
		}
		checksum, err := worker.catalog.Checksum(ctx, snapshot.PackageKind, worker.platform, snapshot.ReleaseIdentity, identity.Name)
		if err != nil {
			return worker.failWithStaging(ctx, operation, installation, "verify", err, snapshot.ToolsRoot)
		}
		if identity.ChecksumSHA256 != "" && checksum != identity.ChecksumSHA256 {
			return worker.failWithStaging(ctx, operation, installation, "verify", fmt.Errorf("published checksum identity changed"), snapshot.ToolsRoot)
		}
		if identity.ChecksumAvailable && checksum == "" {
			return worker.failWithStaging(ctx, operation, installation, "verify", fmt.Errorf("published checksum is unavailable"), snapshot.ToolsRoot)
		}
		if err := tools.VerifySHA256(archivePath, checksum); err != nil {
			return worker.failWithStaging(ctx, operation, installation, "verify", err, snapshot.ToolsRoot)
		}
		downloadedBytes += count
		_ = worker.operations.ProgressForDelivery(ctx, operation, "download", downloadedBytes, nil)
		archivePaths = append(archivePaths, archivePath)
	}

	if err := worker.operations.RunningForDelivery(ctx, operation, "extract"); err != nil {
		return err
	}
	extracted := filepath.Join(staging, "extracted")
	if err := os.Mkdir(extracted, 0o700); err != nil {
		return worker.failWithStaging(ctx, operation, installation, "extract", err, snapshot.ToolsRoot)
	}
	for index, archivePath := range archivePaths {
		name := snapshot.ArtifactIdentities[index].Name
		switch {
		case strings.HasSuffix(name, ".zip"):
			err = tools.ExtractZip(archivePath, extracted)
		case strings.HasSuffix(name, ".tar.xz"):
			err = tools.ExtractTarXz(archivePath, extracted)
		case strings.HasSuffix(name, ".tar.gz"):
			err = tools.ExtractTarGzip(archivePath, extracted)
		default:
			err = fmt.Errorf("unsupported archive format")
		}
		if err != nil {
			return worker.failWithStaging(ctx, operation, installation, "extract", err, snapshot.ToolsRoot)
		}
	}

	if err := worker.operations.RunningForDelivery(ctx, operation, "materialize"); err != nil {
		return err
	}
	candidateRoot := filepath.Join(staging, "candidate")
	_, _, err = worker.lifecycle.Materialize(ctx, extracted, candidateRoot, snapshot.PackageKind, snapshot.ReleaseIdentity, tools.MaterializeOptions{
		GOOS: worker.platform.GOOS, GOARCH: worker.platform.GOARCH,
		Progress: func(copied int64) {
			_ = worker.operations.ProgressForDelivery(ctx, operation, "materialize", downloadedBytes+copied, nil)
		},
	})
	if err != nil {
		return worker.failWithStaging(ctx, operation, installation, "materialize", err, snapshot.ToolsRoot)
	}
	publication, err = prepareInstallPublication(staging, operation, installation, snapshot, root, worker.platform.GOOS)
	if err != nil {
		return worker.failWithStaging(ctx, operation, installation, "materialize", err, snapshot.ToolsRoot)
	}
	return worker.resumeInstallPublication(ctx, operation, installation, snapshot, root, staging, publication)
}

func (worker *InstallationWorker) finish(ctx context.Context, operation *persistence.Operation, installation *persistence.ToolInstallation, root string, snapshot service.InstallInputSnapshot) error {
	activeSetting := settings.ActiveFFmpegInstallationKey
	if snapshot.PackageKind == tools.PackageFPCalc {
		activeSetting = settings.ActiveFPCalcInstallationKey
	}
	verifiedAt := time.Now().UTC()
	if installation.VerifiedAt != nil {
		verifiedAt = *installation.VerifiedAt
	}
	if operation.RiverJobID == nil {
		return fmt.Errorf("finalize installation: River job identity is unavailable")
	}
	finalized, err := worker.repository.FinalizeInstallation(ctx, operation.ID, operation.Attempt, *operation.RiverJobID,
		installation.ID, string(snapshot.PackageKind), worker.platform.GOOS, worker.platform.GOARCH,
		activeSetting, installation.ExecutableVersions, verifiedAt)
	if err != nil {
		return err
	}
	if !finalized {
		// Staging is shared by retries; a stale delivery cannot safely remove it.
		return nil
	}
	worker.operations.Notify(operation.ID)
	if err := tools.CleanupOperationStaging(root, operation.ID); err != nil {
		return err
	}
	return nil
}

func (worker *InstallationWorker) fail(ctx context.Context, operation *persistence.Operation, installation *persistence.ToolInstallation, stage string, cause error) error {
	if ctx.Err() != nil {
		return cause
	}
	slog.Warn("tool operation failed", "operation", operation.ID.String(), "stage", stage, "cause", cause)
	if operation.RiverJobID == nil || operation.TargetInstallationID == nil || *operation.TargetInstallationID != installation.ID {
		return fmt.Errorf("fail installation delivery: immutable delivery target is unavailable")
	}
	changed, err := worker.repository.FailInstallationDelivery(ctx, operation.ID, operation.Attempt, *operation.RiverJobID, installation.ID, stage, safeInstallationError(stage))
	if err != nil {
		return fmt.Errorf("%v; mark installation delivery failed: %w", cause, err)
	}
	if changed {
		worker.operations.Notify(operation.ID)
	}
	return nil
}

func (worker *InstallationWorker) failWithStaging(ctx context.Context, operation *persistence.Operation, installation *persistence.ToolInstallation, stage string, cause error, root string) error {
	if ctx.Err() != nil {
		return cause
	}
	if operation.RiverJobID == nil || operation.TargetInstallationID == nil || *operation.TargetInstallationID != installation.ID {
		return fmt.Errorf("fail installation delivery: immutable delivery target is unavailable")
	}
	changed, err := worker.repository.FailInstallationDelivery(ctx, operation.ID, operation.Attempt, *operation.RiverJobID, installation.ID, stage, safeInstallationError(stage))
	if err != nil {
		return fmt.Errorf("%v; mark installation delivery failed: %w", cause, err)
	}
	if changed {
		worker.operations.Notify(operation.ID)
		if root != "" {
			if err := tools.CleanupOperationStaging(root, operation.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (worker *InstallationWorker) cleanupTerminalInstallation(ctx context.Context, operation *persistence.Operation) error {
	if operation.TargetInstallationID == nil {
		return nil
	}
	installation, err := worker.repository.GetInstallation(ctx, *operation.TargetInstallationID)
	if err != nil {
		return nil
	}
	var snapshot service.InstallInputSnapshot
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil || !validInstallSnapshot(snapshot, installation, worker.platform) {
		return nil
	}
	root, err := validatedInstallToolsRoot(snapshot)
	if err != nil {
		return nil
	}
	staging := filepath.Join(root, ".staging", operation.ID.String())
	publication, err := loadInstallPublication(staging, operation, installation, snapshot, root, worker.platform.GOOS)
	if err != nil {
		return err
	}
	if publication != nil {
		if operation.State == "succeeded" {
			return tools.CleanupOperationStaging(root, operation.ID)
		}
		if err := worker.rollbackInstallFiles(ctx, root, staging, publication); err != nil {
			return err
		}
		if err := os.Remove(installPublicationPath(staging)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := refuseUnjournaledInstallBackups(staging); err != nil {
		return err
	}
	// A failed operation without a publication journal can own only private
	// archives/candidate files. Backups without their journal are never inferred.
	return tools.CleanupOperationStaging(root, operation.ID)
}

func (worker *InstallationWorker) retireQueuedInstallStaging(ctx context.Context, operation *persistence.Operation, installation *persistence.ToolInstallation, root, staging string, publication *installPublication) error {
	publication.Mode = "rollback"
	if err := saveInstallPublication(staging, publication); err != nil {
		return err
	}
	if err := worker.rollbackInstallFiles(ctx, root, staging, publication); err != nil {
		return err
	}
	if err := os.Remove(installPublicationPath(staging)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return tools.CleanupOperationStaging(root, operation.ID)
}

func validInstallSnapshot(snapshot service.InstallInputSnapshot, installation *persistence.ToolInstallation, platform tools.Platform) bool {
	return snapshot.SchemaVersion == 2 && snapshot.ToolsRoot != "" && snapshot.PackageKind == tools.PackageKind(installation.PackageKind) &&
		snapshot.SourceName == installation.SourceName && snapshot.ReleaseIdentity == installation.ReleaseIdentity &&
		platform.GOOS == installation.PlatformGOOS && platform.GOARCH == installation.PlatformGOARCH
}

func validatedInstallToolsRoot(snapshot service.InstallInputSnapshot) (string, error) {
	if _, err := settings.NormalizePath(snapshot.ToolsRoot); err != nil {
		return "", fmt.Errorf("installation snapshot has an invalid tools root: %w", err)
	}
	return snapshot.ToolsRoot, nil
}

func safeInstallationError(stage string) string {
	stage = strings.TrimPrefix(stage, "retry:")
	switch stage {
	case "resolve":
		return "The selected tool release is no longer available."
	case "download":
		return "The tool download failed. Retry the operation."
	case "verify":
		return "The downloaded tool package failed verification."
	case "extract":
		return "The downloaded tool package could not be safely extracted."
	case "materialize":
		return "The verified tools could not be installed in the tools directory."
	default:
		return "The tool installation failed. Retry the operation."
	}
}

func artifactIdentitiesMatch(expected []service.InstallArtifactIdentity, actual []tools.Artifact) bool {
	if len(expected) != len(actual) {
		return false
	}
	for _, identity := range expected {
		found := false
		for _, artifact := range actual {
			if artifact.Name == identity.Name {
				if identity.ChecksumSHA256 != "" && identity.ChecksumSHA256 != artifact.ChecksumSHA256 {
					return false
				}
				if identity.ChecksumAvailable && artifact.ChecksumSHA256 == "" && artifact.ChecksumURL == "" {
					return false
				}
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

var _ river.Worker[service.OperationJobArgs] = (*InstallationWorker)(nil)
