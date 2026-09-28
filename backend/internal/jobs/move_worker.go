package jobs

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type moveRepository interface {
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	ListInstallations(context.Context, string, string, string) ([]persistence.ToolInstallation, error)
	CommitToolsRootMove(context.Context, uuid.UUID, string, string) error
	FinishToolsRootMove(context.Context, uuid.UUID) error
}

type MoveWorker struct {
	repository moveRepository
	operations *service.Operations
	settings   moveSettings
	platform   tools.Platform
	lifecycle  *tools.Lifecycle
}

type moveSettings interface {
	GetToolsDirectory(context.Context) (string, bool, error)
	GetOutputDirectory(context.Context) (string, bool, error)
}

func NewMoveWorker(repository moveRepository, operations *service.Operations, runtimeSettings moveSettings, platform tools.Platform, lifecycle *tools.Lifecycle) *MoveWorker {
	if lifecycle == nil {
		lifecycle = tools.NewLifecycle(nil)
	}
	return &MoveWorker{repository: repository, operations: operations, settings: runtimeSettings, platform: platform, lifecycle: lifecycle}
}

func (worker *MoveWorker) Work(ctx context.Context, operation *persistence.Operation) error {
	if operation.State == "succeeded" {
		var completed service.MoveSnapshot
		if json.Unmarshal(operation.InputSnapshot, &completed) == nil && completed.NewRoot != "" {
			_ = tools.CleanupOperationStaging(completed.NewRoot, operation.ID)
		}
		return nil
	}
	if operation.State == "failed" {
		return nil
	}
	var snapshot service.MoveSnapshot
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
		return worker.fail(ctx, operation, err)
	}
	if snapshot.SchemaVersion != 1 || snapshot.OldRoot == "" || snapshot.NewRoot == "" {
		return worker.fail(ctx, operation, fmt.Errorf("invalid move snapshot"))
	}
	currentRoot, exists, err := worker.settings.GetToolsDirectory(ctx)
	if err != nil {
		return err
	}
	if exists && currentRoot == snapshot.NewRoot && operation.State == "running" && strings.TrimPrefix(operation.Stage, "retry:") == "switched" {
		staging, err := tools.EnsureOperationStaging(snapshot.NewRoot, operation.ID)
		if err != nil {
			return err
		}
		if err := worker.verifyTargets(ctx, snapshot); err != nil {
			return err
		}
		if snapshot.RemoveOldFiles {
			if err := worker.removeOldSources(snapshot, staging); err != nil {
				return err
			}
		}
		if err := tools.CleanupOperationStaging(snapshot.NewRoot, operation.ID); err != nil {
			return err
		}
		if err := worker.repository.FinishToolsRootMove(ctx, operation.ID); err != nil {
			return err
		}
		worker.operations.Notify(operation.ID)
		return nil
	}
	if !exists || currentRoot != snapshot.OldRoot {
		return worker.fail(ctx, operation, fmt.Errorf("current tools root does not match move snapshot"))
	}
	normalizedNewRoot, err := settings.NormalizePath(snapshot.NewRoot)
	if err != nil || normalizedNewRoot != snapshot.NewRoot || settings.PathsOverlap(snapshot.OldRoot, snapshot.NewRoot) {
		return worker.fail(ctx, operation, fmt.Errorf("move snapshot contains an invalid new tools root"))
	}
	outputRoot, hasOutput, err := worker.settings.GetOutputDirectory(ctx)
	if err != nil {
		return err
	}
	if hasOutput && settings.PathsOverlap(outputRoot, snapshot.NewRoot) {
		return worker.fail(ctx, operation, fmt.Errorf("new tools root overlaps output root"))
	}
	resumingTargetCommit := strings.Contains(operation.Stage, "commit_targets") || strings.Contains(operation.Stage, "switch")
	if strings.HasPrefix(operation.Stage, "retry:") {
		retryStage := strings.TrimPrefix(operation.Stage, "retry:")
		resumingTargetCommit = strings.Contains(retryStage, "commit_targets") || strings.Contains(retryStage, "switch")
	}
	for _, file := range snapshot.Files {
		if err := validateMoveFile(file, snapshot, worker.platform.GOOS); err != nil {
			return worker.fail(ctx, operation, err)
		}
	}
	installations, err := worker.repository.ListInstallations(ctx, "", worker.platform.GOOS, worker.platform.GOARCH)
	if err != nil {
		return err
	}
	expectedTargets := make(map[string]persistence.ToolInstallation)
	for _, installation := range installations {
		if installation.State != "ready" {
			continue
		}
		for _, name := range tools.ExpectedExecutables(tools.PackageKind(installation.PackageKind), worker.platform.GOOS) {
			path := filepath.Join(snapshot.NewRoot, installation.RelativePath, name)
			expectedTargets[path] = installation
		}
	}
	if len(expectedTargets) != len(snapshot.Files) {
		return worker.fail(ctx, operation, fmt.Errorf("move snapshot does not match managed installations"))
	}
	targets := make(map[string]struct{}, len(snapshot.Files))
	for _, file := range snapshot.Files {
		installation, ok := expectedTargets[filepath.Clean(file.TargetPath)]
		if !ok || installation.ID != file.InstallationID || installation.RelativePath != file.RelativePath ||
			installation.PackageKind != string(file.PackageKind) || installation.ReleaseIdentity != file.ReleaseIdentity {
			return worker.fail(ctx, operation, fmt.Errorf("move snapshot does not match managed installation identity"))
		}
		targets[filepath.Clean(file.TargetPath)] = struct{}{}
	}
	if len(targets) != len(expectedTargets) {
		return worker.fail(ctx, operation, fmt.Errorf("move snapshot contains duplicate target paths"))
	}
	for _, conflict := range snapshot.ConfirmedConflicts {
		if _, ok := targets[filepath.Clean(conflict)]; !ok {
			return worker.fail(ctx, operation, fmt.Errorf("move snapshot contains an unowned conflict path"))
		}
	}
	staging, err := tools.EnsureOperationStaging(snapshot.NewRoot, operation.ID)
	if err != nil {
		return worker.fail(ctx, operation, err)
	}
	if err := worker.restoreOldSources(ctx, staging, snapshot); err != nil {
		return err
	}
	if err := worker.operations.Running(ctx, operation.ID, "copy"); err != nil {
		return err
	}
	payloadRoot := filepath.Join(staging, "payload")
	if err := os.MkdirAll(payloadRoot, 0o700); err != nil {
		return worker.fail(ctx, operation, err)
	}
	var bytesCopied int64
	var bytesTotal int64
	for _, file := range snapshot.Files {
		bytesTotal += file.Size
	}
	for index, file := range snapshot.Files {
		payloadPath := filepath.Join(payloadRoot, file.RelativePath, file.Executable)
		if err := copyMovePayload(file, payloadPath, staging, index, func(copied int64) {
			_ = worker.operations.Progress(ctx, operation.ID, "copy", bytesCopied+copied, &bytesTotal)
		}); err != nil {
			if ctx.Err() != nil {
				return err
			}
			return worker.rollbackTargets(ctx, operation, snapshot, staging, nil, err)
		}
		bytesCopied += file.Size
		if err := worker.operations.Progress(ctx, operation.ID, "copy", bytesCopied, &bytesTotal); err != nil {
			return err
		}
	}
	if err := worker.operations.Running(ctx, operation.ID, "verify"); err != nil {
		return err
	}
	verified := make(map[uuid.UUID]struct{})
	for _, file := range snapshot.Files {
		if _, done := verified[file.InstallationID]; done {
			continue
		}
		if _, err := worker.lifecycle.VerifyInstallation(ctx, payloadRoot, file.RelativePath, file.PackageKind, file.ReleaseIdentity, worker.platform.GOOS); err != nil {
			return worker.rollbackTargets(ctx, operation, snapshot, staging, nil, err)
		}
		verified[file.InstallationID] = struct{}{}
	}
	if err := worker.operations.Running(ctx, operation.ID, "commit_targets"); err != nil {
		if ctx.Err() != nil {
			return err
		}
		return worker.rollbackTargets(ctx, operation, snapshot, staging, nil, err)
	}
	ownedTargets, err := worker.commitTargets(ctx, snapshot, staging, resumingTargetCommit)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		return worker.rollbackTargets(ctx, operation, snapshot, staging, ownedTargets, err)
	}
	if err := worker.verifyTargets(ctx, snapshot); err != nil {
		return worker.rollbackTargets(ctx, operation, snapshot, staging, ownedTargets, err)
	}
	if err := worker.operations.Running(ctx, operation.ID, "switch"); err != nil {
		if ctx.Err() != nil {
			return err
		}
		return worker.rollbackTargets(ctx, operation, snapshot, staging, ownedTargets, err)
	}
	if err := worker.repository.CommitToolsRootMove(ctx, operation.ID, snapshot.OldRoot, snapshot.NewRoot); err != nil {
		if ctx.Err() != nil {
			return err
		}
		return worker.rollbackTargets(ctx, operation, snapshot, staging, ownedTargets, err)
	}
	worker.operations.Notify(operation.ID)
	if snapshot.RemoveOldFiles {
		if err := worker.removeOldSources(snapshot, staging); err != nil {
			return err
		}
	}
	if err := tools.CleanupOperationStaging(snapshot.NewRoot, operation.ID); err != nil {
		return err
	}
	if err := worker.repository.FinishToolsRootMove(ctx, operation.ID); err != nil {
		return err
	}
	worker.operations.Notify(operation.ID)
	return nil
}

func validateMoveFile(file service.MoveFileIdentity, snapshot service.MoveSnapshot, goos string) error {
	relative, err := tools.ManagedRelativePath(file.PackageKind, file.ReleaseIdentity)
	if err != nil || relative != filepath.Clean(file.RelativePath) {
		return fmt.Errorf("move snapshot has an invalid managed relative path")
	}
	if filepath.Clean(file.SourcePath) != filepath.Join(snapshot.OldRoot, relative, file.Executable) ||
		filepath.Clean(file.TargetPath) != filepath.Join(snapshot.NewRoot, relative, file.Executable) {
		return fmt.Errorf("move snapshot contains a non-managed executable path")
	}
	expected := false
	for _, name := range tools.ExpectedExecutables(file.PackageKind, goos) {
		expected = expected || file.Executable == name
	}
	if !expected {
		return fmt.Errorf("move snapshot contains an unexpected executable")
	}
	if len(file.SHA256) != 64 || file.Size < 0 {
		return fmt.Errorf("move snapshot has invalid executable identity")
	}
	if _, err := hex.DecodeString(file.SHA256); err != nil {
		return fmt.Errorf("move snapshot has invalid executable identity")
	}
	return nil
}

func copyMovePayload(file service.MoveFileIdentity, destination, staging string, index int, progress func(int64)) error {
	if info, err := os.Lstat(destination); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("move staging executable has an unsupported file type")
		}
		if err := tools.VerifySHA256(destination, file.SHA256); err == nil {
			return nil
		}
		if err := os.Remove(destination); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	source := file.SourcePath
	if _, err := os.Stat(source); os.IsNotExist(err) {
		backup := filepath.Join(staging, "old-backups", strconv.Itoa(index))
		if _, backupErr := os.Stat(backup); backupErr != nil {
			return fmt.Errorf("move source and recovery copy are unavailable")
		}
		source = backup
	} else if err != nil {
		return err
	}
	if err := tools.HasSymlinkAncestors(filepath.Dir(source), source); err != nil {
		return err
	}
	digest, err := tools.SHA256File(source)
	if err != nil || digest != file.SHA256 {
		return fmt.Errorf("managed source changed after move preflight")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(&moveProgressWriter{writer: output, progress: progress}, input)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return closeErr
	}
	if written != file.Size {
		_ = os.Remove(destination)
		return fmt.Errorf("managed executable size changed during copy")
	}
	return tools.VerifySHA256(destination, file.SHA256)
}

type moveProgressWriter struct {
	writer   io.Writer
	progress func(int64)
	count    int64
}

func (writer *moveProgressWriter) Write(data []byte) (int, error) {
	n, err := writer.writer.Write(data)
	writer.count += int64(n)
	if writer.progress != nil && n > 0 {
		writer.progress(writer.count)
	}
	return n, err
}

func (worker *MoveWorker) commitTargets(ctx context.Context, snapshot service.MoveSnapshot, staging string, resumingTargetCommit bool) ([]string, error) {
	ownedTargets := make([]string, 0, len(snapshot.Files))
	fail := func(err error) ([]string, error) { return ownedTargets, err }
	confirmed := make(map[string]struct{}, len(snapshot.ConfirmedConflicts))
	for _, path := range snapshot.ConfirmedConflicts {
		confirmed[filepath.Clean(path)] = struct{}{}
	}
	for index, file := range snapshot.Files {
		target := filepath.Clean(file.TargetPath)
		if err := tools.HasSymlinkAncestors(snapshot.NewRoot, filepath.Dir(target)); err != nil {
			return fail(err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fail(err)
		}
		digest, hashErr := tools.SHA256File(target)
		if hashErr == nil && digest == file.SHA256 {
			if _, isConflict := confirmed[target]; isConflict {
				if err := worker.ensureTargetBackup(target, filepath.Join(staging, "target-backups", strconv.Itoa(index))); err != nil {
					return fail(err)
				}
			} else if !resumingTargetCommit {
				return fail(fmt.Errorf("move target appeared after preflight"))
			} else {
				ownedTargets = append(ownedTargets, target)
			}
			continue
		}
		info, statErr := os.Lstat(target)
		if statErr == nil {
			if _, isConflict := confirmed[target]; !isConflict {
				return fail(fmt.Errorf("unconfirmed target appeared after move preflight"))
			}
			if err := worker.ensureTargetBackup(target, filepath.Join(staging, "target-backups", strconv.Itoa(index))); err != nil {
				return fail(err)
			}
		} else if !os.IsNotExist(statErr) {
			return fail(statErr)
		} else if _, isConflict := confirmed[target]; isConflict {
			backup := filepath.Join(staging, "target-backups", strconv.Itoa(index))
			if _, backupErr := os.Lstat(backup); backupErr != nil {
				return fail(fmt.Errorf("move conflict changed after confirmation"))
			}
		}
		if info != nil && !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return fail(fmt.Errorf("move target has an unsupported file type"))
		}
		payload := filepath.Join(staging, "payload", file.RelativePath, file.Executable)
		temporary, err := os.CreateTemp(filepath.Dir(target), ".melotrove-move-")
		if err != nil {
			return fail(err)
		}
		tempPath := temporary.Name()
		if err := temporary.Close(); err != nil {
			_ = os.Remove(tempPath)
			return fail(err)
		}
		if err := os.Remove(tempPath); err != nil {
			return fail(err)
		}
		if err := copyFile(payload, tempPath); err != nil {
			return fail(err)
		}
		if _, err := os.Lstat(target); err == nil {
			if err := os.Remove(target); err != nil {
				_ = os.Remove(tempPath)
				return fail(err)
			}
		} else if !os.IsNotExist(err) {
			_ = os.Remove(tempPath)
			return fail(err)
		}
		if err := os.Rename(tempPath, target); err != nil {
			_ = os.Remove(tempPath)
			return fail(err)
		}
		ownedTargets = append(ownedTargets, target)
		if err := tools.VerifySHA256(target, file.SHA256); err != nil {
			return fail(err)
		}
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
	}
	return ownedTargets, nil
}

func (worker *MoveWorker) ensureTargetBackup(target, backup string) error {
	if _, err := os.Lstat(backup); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(target)
		if err != nil {
			return err
		}
		return os.Symlink(link, backup)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("move conflict has an unsupported file type")
	}
	return copyFile(target, backup)
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("move copy source is not a regular file")
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return closeErr
	}
	return nil
}

func restoreTargetBackup(backup, target string) error {
	info, err := os.Lstat(backup)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".melotrove-restore-")
	if err != nil {
		return err
	}
	temp := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Remove(temp); err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(backup)
		if err != nil {
			return err
		}
		if err := os.Symlink(link, temp); err != nil {
			return err
		}
	} else if info.Mode().IsRegular() {
		if err := copyFile(backup, temp); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("move backup has an unsupported file type")
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func (worker *MoveWorker) verifyTargets(ctx context.Context, snapshot service.MoveSnapshot) error {
	verified := make(map[uuid.UUID]struct{})
	for _, file := range snapshot.Files {
		if _, done := verified[file.InstallationID]; done {
			continue
		}
		if _, err := worker.lifecycle.VerifyInstallation(ctx, snapshot.NewRoot, file.RelativePath, file.PackageKind, file.ReleaseIdentity, worker.platform.GOOS); err != nil {
			return err
		}
		verified[file.InstallationID] = struct{}{}
	}
	return nil
}

func (worker *MoveWorker) removeOldSources(snapshot service.MoveSnapshot, staging string) error {
	for index, file := range snapshot.Files {
		backup := filepath.Join(staging, "old-backups", strconv.Itoa(index))
		if _, err := os.Lstat(backup); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
				return err
			}
			sourceInfo, err := os.Lstat(file.SourcePath)
			if err != nil {
				return err
			}
			if !sourceInfo.Mode().IsRegular() {
				return fmt.Errorf("old managed executable changed file type")
			}
			sourceDigest, err := tools.SHA256File(file.SourcePath)
			if err != nil || sourceDigest != file.SHA256 {
				return fmt.Errorf("old managed executable changed after move preflight")
			}
			if err := copyFile(file.SourcePath, backup); err != nil {
				return err
			}
			if err := tools.VerifySHA256(backup, file.SHA256); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if err := tools.VerifySHA256(backup, file.SHA256); err != nil {
			return fmt.Errorf("old managed recovery copy is invalid: %w", err)
		}
		sourceInfo, err := os.Lstat(file.SourcePath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !sourceInfo.Mode().IsRegular() {
			return fmt.Errorf("old managed executable changed file type")
		}
		sourceDigest, err := tools.SHA256File(file.SourcePath)
		if err != nil || sourceDigest != file.SHA256 {
			return fmt.Errorf("old managed executable changed after move preflight")
		}
		if err := os.Remove(file.SourcePath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (worker *MoveWorker) restoreOldSources(ctx context.Context, staging string, snapshot service.MoveSnapshot) error {
	if !snapshot.RemoveOldFiles {
		return nil
	}
	for index, file := range snapshot.Files {
		backup := filepath.Join(staging, "old-backups", strconv.Itoa(index))
		backupInfo, err := os.Lstat(backup)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if backupInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("move recovery backup is a symlink")
		}
		if err := tools.VerifySHA256(backup, file.SHA256); err != nil {
			return fmt.Errorf("move recovery backup is invalid: %w", err)
		}
		if _, err := os.Lstat(file.SourcePath); err == nil {
			digest, digestErr := tools.SHA256File(file.SourcePath)
			if digestErr != nil {
				return digestErr
			}
			if digest == file.SHA256 {
				continue
			}
			return fmt.Errorf("old managed source changed during move recovery")
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(file.SourcePath), 0o755); err != nil {
			return err
		}
		if err := copyFile(backup, file.SourcePath); err != nil {
			return err
		}
		if err := tools.VerifySHA256(file.SourcePath, file.SHA256); err != nil {
			_ = os.Remove(file.SourcePath)
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

func (worker *MoveWorker) rollbackTargets(ctx context.Context, operation *persistence.Operation, snapshot service.MoveSnapshot, staging string, ownedTargets []string, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var rollbackErrors []error
	owned := make(map[string]struct{}, len(ownedTargets))
	for _, path := range ownedTargets {
		owned[filepath.Clean(path)] = struct{}{}
	}
	for index := len(snapshot.Files) - 1; index >= 0; index-- {
		file := snapshot.Files[index]
		backup := filepath.Join(staging, "target-backups", strconv.Itoa(index))
		if _, err := os.Lstat(backup); err == nil {
			if err := restoreTargetBackup(backup, file.TargetPath); err != nil {
				rollbackErrors = append(rollbackErrors, err)
			}
			continue
		}
		if _, wasOwned := owned[filepath.Clean(file.TargetPath)]; wasOwned {
			digest, err := tools.SHA256File(file.TargetPath)
			if err != nil {
				if !os.IsNotExist(err) {
					rollbackErrors = append(rollbackErrors, err)
				}
				continue
			}
			if digest != file.SHA256 {
				continue
			}
			if err := os.Remove(file.TargetPath); err != nil {
				rollbackErrors = append(rollbackErrors, err)
			}
		}
	}
	if snapshot.RemoveOldFiles {
		if err := worker.restoreOldSources(context.Background(), staging, snapshot); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	if len(rollbackErrors) > 0 {
		return fmt.Errorf("move rollback incomplete: %w", errors.Join(rollbackErrors...))
	}
	if err := tools.CleanupOperationStaging(snapshot.NewRoot, operation.ID); err != nil {
		return fmt.Errorf("move rollback cleanup failed: %w", err)
	}
	return worker.fail(ctx, operation, cause)
}

func (worker *MoveWorker) fail(ctx context.Context, operation *persistence.Operation, cause error) error {
	if ctx.Err() != nil {
		return cause
	}
	if err := worker.operations.Fail(ctx, operation.ID, "move", "The tools directory move failed. The current tools directory is unchanged."); err != nil {
		return fmt.Errorf("%v; mark move failed: %w", cause, err)
	}
	return nil
}
