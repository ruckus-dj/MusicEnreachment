package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	MarkInstallationReady(context.Context, uuid.UUID, json.RawMessage, time.Time) error
	MarkInstallationFailed(context.Context, uuid.UUID) error
	ActivateInstallationDuringSetup(context.Context, uuid.UUID, string, string, string, string) (bool, error)
}

type installCatalog interface {
	Resolve(context.Context, tools.PackageKind, tools.Platform, string) (tools.Release, error)
	Download(context.Context, tools.PackageKind, tools.Platform, string, string, io.Writer, func(int64)) (tools.Artifact, int64, error)
	Checksum(context.Context, tools.PackageKind, tools.Platform, string, string) (string, error)
}

type setupSettings interface {
	GetToolsDirectory(context.Context) (string, bool, error)
	SetupCompleted(context.Context) (bool, error)
}

type InstallationWorker struct {
	river.WorkerDefaults[service.OperationJobArgs]
	repository installRepository
	operations *service.Operations
	catalog    installCatalog
	settings   setupSettings
	platform   tools.Platform
	lifecycle  *tools.Lifecycle
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

func (worker *InstallationWorker) Work(ctx context.Context, job *river.Job[service.OperationJobArgs]) error {
	operationID := job.Args.OperationID
	operation, err := worker.repository.GetOperation(ctx, operationID)
	if err != nil {
		return err
	}
	if operation.State == "succeeded" || operation.State == "failed" {
		return nil
	}
	if operation.Kind != "install" || operation.TargetInstallationID == nil {
		return fmt.Errorf("operation is not an installation")
	}
	installation, err := worker.repository.GetInstallation(ctx, *operation.TargetInstallationID)
	if err != nil {
		return err
	}
	if installation.State == "failed" && operation.State == "running" {
		return worker.operations.Fail(ctx, operation.ID, operation.Stage, safeInstallationError(operation.Stage))
	}
	var snapshot service.InstallInputSnapshot
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
		return worker.fail(ctx, operation, installation, "resolve", err)
	}
	if snapshot.SchemaVersion != 1 || snapshot.PackageKind != tools.PackageKind(installation.PackageKind) ||
		snapshot.SourceName != installation.SourceName || snapshot.ReleaseIdentity != installation.ReleaseIdentity ||
		worker.platform.GOOS != installation.PlatformGOOS || worker.platform.GOARCH != installation.PlatformGOARCH {
		return worker.fail(ctx, operation, installation, "resolve", fmt.Errorf("installation snapshot does not match target"))
	}
	root, exists, err := worker.settings.GetToolsDirectory(ctx)
	if err != nil {
		return err
	}
	if !exists || root == "" {
		return worker.fail(ctx, operation, installation, "materialize", fmt.Errorf("tools directory is unavailable"))
	}

	if installation.State == "ready" {
		if _, err := worker.lifecycle.VerifyInstallation(ctx, root, installation.RelativePath, snapshot.PackageKind, snapshot.ReleaseIdentity, worker.platform.GOOS); err != nil {
			return worker.fail(ctx, operation, installation, "verify", err)
		}
		return worker.finish(ctx, operation, installation, root, snapshot)
	}
	if err := worker.operations.Running(ctx, operation.ID, "resolve"); err != nil {
		return err
	}
	release, err := worker.catalog.Resolve(ctx, snapshot.PackageKind, worker.platform, snapshot.ReleaseIdentity)
	if err != nil {
		return worker.fail(ctx, operation, installation, "resolve", err)
	}
	if !artifactIdentitiesMatch(snapshot.ArtifactIdentities, release.Artifacts) {
		return worker.fail(ctx, operation, installation, "resolve", fmt.Errorf("upstream artifact identity changed"))
	}

	staging, err := tools.ResetOperationStaging(root, operation.ID)
	if err != nil {
		return worker.fail(ctx, operation, installation, "download", err)
	}
	cleanupOnFailure := true
	defer func() {
		if cleanupOnFailure {
			_ = tools.CleanupOperationStaging(root, operation.ID)
		}
	}()
	archivePaths := make([]string, 0, len(snapshot.ArtifactIdentities))
	var downloadedBytes int64
	for index, identity := range snapshot.ArtifactIdentities {
		if err := worker.operations.Running(ctx, operation.ID, "download"); err != nil {
			return err
		}
		archivePath := filepath.Join(staging, fmt.Sprintf("artifact-%d", index))
		archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return worker.fail(ctx, operation, installation, "download", err)
		}
		lastProgress := int64(0)
		artifact, count, downloadErr := worker.catalog.Download(ctx, snapshot.PackageKind, worker.platform, snapshot.ReleaseIdentity, identity.Name, archive, func(current int64) {
			if current-lastProgress >= 1<<20 {
				lastProgress = current
				_ = worker.operations.Progress(ctx, operation.ID, "download", downloadedBytes+current, nil)
			}
		})
		closeErr := archive.Close()
		if downloadErr != nil {
			return worker.fail(ctx, operation, installation, "download", downloadErr)
		}
		if closeErr != nil {
			return worker.fail(ctx, operation, installation, "download", closeErr)
		}
		if artifact.Name != identity.Name {
			return worker.fail(ctx, operation, installation, "resolve", fmt.Errorf("resolved artifact identity changed"))
		}
		checksum, err := worker.catalog.Checksum(ctx, snapshot.PackageKind, worker.platform, snapshot.ReleaseIdentity, identity.Name)
		if err != nil {
			return worker.fail(ctx, operation, installation, "verify", err)
		}
		if identity.ChecksumSHA256 != "" && checksum != identity.ChecksumSHA256 {
			return worker.fail(ctx, operation, installation, "verify", fmt.Errorf("published checksum identity changed"))
		}
		if identity.ChecksumAvailable && checksum == "" {
			return worker.fail(ctx, operation, installation, "verify", fmt.Errorf("published checksum is unavailable"))
		}
		if err := tools.VerifySHA256(archivePath, checksum); err != nil {
			return worker.fail(ctx, operation, installation, "verify", err)
		}
		downloadedBytes += count
		_ = worker.operations.Progress(ctx, operation.ID, "download", downloadedBytes, nil)
		archivePaths = append(archivePaths, archivePath)
	}

	if err := worker.operations.Running(ctx, operation.ID, "extract"); err != nil {
		return err
	}
	extracted := filepath.Join(staging, "extracted")
	if err := os.Mkdir(extracted, 0o700); err != nil {
		return worker.fail(ctx, operation, installation, "extract", err)
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
			return worker.fail(ctx, operation, installation, "extract", err)
		}
	}

	if err := worker.operations.Running(ctx, operation.ID, "materialize"); err != nil {
		return err
	}
	knownPaths := []string(nil)
	if strings.Contains(operation.Stage, "materialize") {
		preflight, err := tools.PreflightTargets(root, snapshot.PackageKind, snapshot.ReleaseIdentity, worker.platform.GOOS, map[string]struct{}{})
		if err != nil {
			return worker.fail(ctx, operation, installation, "materialize", err)
		}
		knownPaths = preflight.Targets
	}
	_, versions, err := worker.lifecycle.Materialize(ctx, extracted, root, snapshot.PackageKind, snapshot.ReleaseIdentity, tools.MaterializeOptions{
		GOOS: worker.platform.GOOS, GOARCH: worker.platform.GOARCH,
		ManagedPaths: knownPaths, ConfirmedConflicts: snapshot.ConfirmedConflicts,
		Progress: func(copied int64) {
			_ = worker.operations.Progress(ctx, operation.ID, "materialize", downloadedBytes+copied, nil)
		},
	})
	if err != nil {
		return worker.fail(ctx, operation, installation, "materialize", err)
	}
	if err := worker.operations.Running(ctx, operation.ID, "files_materialized"); err != nil {
		return err
	}
	versionsJSON, err := json.Marshal(versions)
	if err != nil {
		return err
	}
	if err := worker.repository.MarkInstallationReady(ctx, installation.ID, versionsJSON, time.Now().UTC()); err != nil {
		return err
	}
	installation.State = "ready"
	if err := worker.finish(ctx, operation, installation, root, snapshot); err != nil {
		return err
	}
	cleanupOnFailure = false
	return nil
}

func (worker *InstallationWorker) finish(ctx context.Context, operation *persistence.Operation, installation *persistence.ToolInstallation, root string, snapshot service.InstallInputSnapshot) error {
	completed, err := worker.settings.SetupCompleted(ctx)
	if err != nil {
		return err
	}
	if !completed {
		activeSetting := settings.ActiveFFmpegInstallationKey
		if snapshot.PackageKind == tools.PackageFPCalc {
			activeSetting = settings.ActiveFPCalcInstallationKey
		}
		if _, err := worker.repository.ActivateInstallationDuringSetup(ctx, installation.ID, string(snapshot.PackageKind), worker.platform.GOOS, worker.platform.GOARCH, activeSetting); err != nil {
			return err
		}
	}
	if err := tools.CleanupOperationStaging(root, operation.ID); err != nil {
		return err
	}
	if err := worker.operations.Succeed(ctx, operation.ID, "succeeded"); err != nil {
		return err
	}
	return nil
}

func (worker *InstallationWorker) fail(ctx context.Context, operation *persistence.Operation, installation *persistence.ToolInstallation, stage string, cause error) error {
	if ctx.Err() != nil {
		return cause
	}
	if installation.State == "preparing" {
		if err := worker.repository.MarkInstallationFailed(ctx, installation.ID); err != nil {
			return fmt.Errorf("%v; mark installation failed: %w", cause, err)
		}
	}
	if err := worker.operations.Fail(ctx, operation.ID, stage, safeInstallationError(stage)); err != nil {
		return fmt.Errorf("%v; mark operation failed: %w", cause, err)
	}
	return nil
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
