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
	RollbackToolsRootMove(context.Context, uuid.UUID, string, string) error
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
		if err := json.Unmarshal(operation.InputSnapshot, &completed); err != nil || completed.NewRoot == "" {
			return fmt.Errorf("invalid succeeded move snapshot")
		}
		if err := cleanupOldSourceRestoreStaging(completed, operation.ID); err != nil {
			return err
		}
		return tools.CleanupOperationStaging(completed.NewRoot, operation.ID)
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
	if exists && currentRoot == snapshot.OldRoot && operation.State == "running" && operation.Stage == "rolled_back" {
		if err := cleanupOldSourceRestoreStaging(snapshot, operation.ID); err != nil {
			return err
		}
		if err := tools.CleanupOperationStaging(snapshot.NewRoot, operation.ID); err != nil {
			return err
		}
		return worker.fail(ctx, operation, fmt.Errorf("tools root move rolled back"))
	}
	if exists && currentRoot == snapshot.NewRoot &&
		(operation.State == "running" && operation.Stage == "rollback_pending" || operation.State == "queued" && operation.Stage == "retry:rollback_pending") {
		staging, err := tools.EnsureOperationStaging(snapshot.NewRoot, operation.ID)
		if err != nil {
			return err
		}
		return worker.rollbackSwitched(ctx, operation, snapshot, staging, fmt.Errorf("tools root move rollback resumed"))
	}
	if exists && currentRoot == snapshot.NewRoot && operation.State == "queued" && operation.Stage == "retry:switched" {
		staging, err := tools.EnsureOperationStaging(snapshot.NewRoot, operation.ID)
		if err != nil {
			return err
		}
		return worker.rollbackSwitched(ctx, operation, snapshot, staging, fmt.Errorf("tools root move was interrupted after switching roots"))
	}
	if exists && currentRoot == snapshot.NewRoot && operation.State == "running" && strings.TrimPrefix(operation.Stage, "retry:") == "switched" {
		staging, err := tools.EnsureOperationStaging(snapshot.NewRoot, operation.ID)
		if err != nil {
			return err
		}
		if err := worker.verifyTargets(ctx, snapshot); err != nil {
			return worker.rollbackSwitched(ctx, operation, snapshot, staging, err)
		}
		if snapshot.RemoveOldFiles {
			if err := worker.removeOldSources(snapshot, staging); err != nil {
				return worker.rollbackSwitched(ctx, operation, snapshot, staging, err)
			}
		}
		if err := worker.repository.FinishToolsRootMove(ctx, operation.ID); err != nil {
			return err
		}
		worker.operations.Notify(operation.ID)
		if err := cleanupOldSourceRestoreStaging(snapshot, operation.ID); err != nil {
			return err
		}
		return tools.CleanupOperationStaging(snapshot.NewRoot, operation.ID)
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
	publication, err := loadMovePublication(staging, operation.ID, snapshot)
	if err != nil {
		return err
	}
	var ownedTargets []string
	if publication != nil {
		ownedTargets, err = ownedMoveTargets(snapshot, staging, publication)
		if err != nil {
			return err
		}
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
	for _, file := range snapshot.Files {
		payloadPath := filepath.Join(payloadRoot, file.RelativePath, file.Executable)
		if err := copyMovePayload(file, payloadPath, func(copied int64) {
			_ = worker.operations.Progress(ctx, operation.ID, "copy", bytesCopied+copied, &bytesTotal)
		}); err != nil {
			if ctx.Err() != nil {
				return err
			}
			return worker.rollbackTargets(ctx, operation, snapshot, staging, ownedTargets, err)
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
			return worker.rollbackTargets(ctx, operation, snapshot, staging, ownedTargets, err)
		}
		verified[file.InstallationID] = struct{}{}
	}
	if err := worker.operations.Running(ctx, operation.ID, "commit_targets"); err != nil {
		if ctx.Err() != nil {
			return err
		}
		return worker.rollbackTargets(ctx, operation, snapshot, staging, ownedTargets, err)
	}
	if publication == nil {
		publication, err = newMovePublication(staging, operation.ID, snapshot)
		if err != nil {
			return err
		}
	}
	ownedTargets, err = worker.commitTargets(ctx, snapshot, staging, publication)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		return worker.rollbackTargets(ctx, operation, snapshot, staging, ownedTargets, err)
	}
	if err := worker.verifyTargets(ctx, snapshot); err != nil {
		return worker.rollbackTargets(ctx, operation, snapshot, staging, ownedTargets, err)
	}
	if err := worker.operations.Running(ctx, operation.ID, "prepare_restore"); err != nil {
		return err
	}
	if err := recordMoveSources(snapshot, staging); err != nil {
		if ctx.Err() != nil {
			return err
		}
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
			return worker.rollbackSwitched(ctx, operation, snapshot, staging, err)
		}
	}
	if err := worker.repository.FinishToolsRootMove(ctx, operation.ID); err != nil {
		return err
	}
	worker.operations.Notify(operation.ID)
	if err := cleanupOldSourceRestoreStaging(snapshot, operation.ID); err != nil {
		return err
	}
	return tools.CleanupOperationStaging(snapshot.NewRoot, operation.ID)
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

func copyMovePayload(file service.MoveFileIdentity, destination string, progress func(int64)) error {
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

func (worker *MoveWorker) commitTargets(ctx context.Context, snapshot service.MoveSnapshot, staging string, publication *movePublication) ([]string, error) {
	fail := func(err error) ([]string, error) {
		ownedTargets, ownershipErr := ownedMoveTargets(snapshot, staging, publication)
		if ownershipErr != nil {
			return ownedTargets, fmt.Errorf("%v; inspect move publication ownership: %w", err, ownershipErr)
		}
		return ownedTargets, err
	}
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
		payload := filepath.Join(staging, "payload", file.RelativePath, file.Executable)
		if err := tools.VerifySHA256(payload, file.SHA256); err != nil {
			return fail(fmt.Errorf("verified move executable changed before publication: %w", err))
		}
		published, err := moveFileWasPublished(file, staging, index)
		if err != nil {
			return fail(err)
		}
		if published {
			if !publication.Files[index].Owned {
				publication.Files[index].Owned = true
				if err := saveMovePublication(staging, publication); err != nil {
					return fail(err)
				}
			}
			continue
		}
		backup := filepath.Join(staging, "target-backups", strconv.Itoa(index))
		if _, isConflict := confirmed[target]; isConflict {
			if _, backupErr := os.Lstat(backup); os.IsNotExist(backupErr) {
				if _, targetErr := os.Lstat(target); os.IsNotExist(targetErr) {
					return fail(fmt.Errorf("move conflict changed after confirmation"))
				} else if targetErr != nil {
					return fail(targetErr)
				}
				if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
					return fail(err)
				}
				if err := os.Rename(target, backup); err != nil {
					return fail(err)
				}
			} else if backupErr != nil {
				return fail(backupErr)
			} else if _, targetErr := os.Lstat(target); targetErr == nil {
				return fail(fmt.Errorf("another file appeared at confirmed move target"))
			} else if !os.IsNotExist(targetErr) {
				return fail(targetErr)
			}
		} else if _, targetErr := os.Lstat(target); targetErr == nil {
			return fail(fmt.Errorf("unconfirmed target appeared after move preflight"))
		} else if !os.IsNotExist(targetErr) {
			return fail(targetErr)
		}
		if err := os.Link(payload, target); err != nil {
			return fail(fmt.Errorf("publish move target without overwrite: %w", err))
		}
		publication.Files[index].Owned = true
		if err := saveMovePublication(staging, publication); err != nil {
			return fail(err)
		}
		if err := tools.VerifySHA256(target, file.SHA256); err != nil {
			return fail(err)
		}
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
	}
	return ownedMoveTargets(snapshot, staging, publication)
}

func oldSourceRestorePaths(target string, token uuid.UUID) (string, string) {
	base := filepath.Join(filepath.Dir(target), ".melotrove-restore-"+token.String())
	return base + ".source", base + ".staging"
}

type moveRestoreIdentity struct {
	Volume  uint64 `json:"volume"`
	File    uint64 `json:"file"`
	Created int64  `json:"created"`
}

type moveRestoreRecord struct {
	Token    uuid.UUID            `json:"token"`
	SHA256   string               `json:"sha256"`
	Source   *moveRestoreIdentity `json:"source,omitempty"`
	Identity *moveRestoreIdentity `json:"identity,omitempty"`
	Ready    bool                 `json:"ready"`
}

func oldSourceRestoreJournal(staging string, index int) string {
	return filepath.Join(staging, fmt.Sprintf("old-source-restore-%d.json", index))
}

func recordMoveSources(snapshot service.MoveSnapshot, staging string) error {
	for index, file := range snapshot.Files {
		if err := tools.VerifySHA256(file.SourcePath, file.SHA256); err != nil {
			return err
		}
		identity, err := inspectMoveRestoreIdentity(file.SourcePath)
		if err != nil {
			return err
		}
		journal := oldSourceRestoreJournal(staging, index)
		record, err := loadOldSourceRestoreRecord(journal)
		if err != nil {
			return err
		}
		if record != nil {
			if record.Token == uuid.Nil || record.Source == nil || *record.Source != identity || record.SHA256 != file.SHA256 {
				return fmt.Errorf("managed source ownership changed before root switch")
			}
		} else {
			record = &moveRestoreRecord{Token: uuid.New(), Source: &identity, SHA256: file.SHA256}
			if err := saveOldSourceRestoreRecord(journal, record); err != nil {
				return err
			}
		}
		_, backup := oldSourceRestorePaths(file.SourcePath, record.Token)
		if _, err := prepareOldSourceRestore(file.SourcePath, backup, journal); err != nil {
			return err
		}
	}
	return nil
}

func loadOldSourceRestoreRecord(journal string) (*moveRestoreRecord, error) {
	info, err := os.Lstat(journal)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024 {
		return nil, fmt.Errorf("invalid old source restore journal")
	}
	raw, err := os.ReadFile(journal)
	if err != nil {
		return nil, err
	}
	var record moveRestoreRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func saveOldSourceRestoreRecord(journal string, record *moveRestoreRecord) error {
	file, err := os.CreateTemp(filepath.Dir(journal), ".old-source-restore-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	writeErr := json.NewEncoder(file).Encode(record)
	syncErr := file.Sync()
	if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), journal)
}

func inspectMoveRestoreIdentity(path string) (moveRestoreIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return moveRestoreIdentity{}, err
	}
	if !info.Mode().IsRegular() {
		return moveRestoreIdentity{}, fmt.Errorf("old source restore file is not regular")
	}
	file, err := os.Open(path)
	if err != nil {
		return moveRestoreIdentity{}, err
	}
	identity, _, inspectErr := moveRestoreFileIdentity(file)
	return identity, errors.Join(inspectErr, file.Close())
}

func prepareOldSourceRestore(target, temp, journal string) (*moveRestoreRecord, error) {
	record, err := loadOldSourceRestoreRecord(journal)
	if err != nil {
		return nil, err
	}
	if record == nil || record.Source == nil || record.Token == uuid.Nil {
		return nil, fmt.Errorf("old source restoration preparation has no source proof")
	}
	original, backup := oldSourceRestorePaths(target, record.Token)
	if temp != backup {
		return nil, fmt.Errorf("old source restoration witness path changed")
	}
	sourceIdentity, err := inspectMoveRestoreIdentity(target)
	if err != nil {
		return nil, err
	}
	if sourceIdentity != *record.Source {
		return nil, fmt.Errorf("old source changed during restoration preparation")
	}
	// This witness keeps the original inode alive through source unlink and
	// redelivery. Preparation never changes the still-active managed source.
	if err := os.Link(target, original); err != nil && !os.IsExist(err) {
		return nil, err
	}
	originalIdentity, err := inspectMoveRestoreIdentity(original)
	if err != nil {
		return nil, err
	}
	if originalIdentity != *record.Source {
		return nil, fmt.Errorf("unknown original source witness")
	}
	flags := os.O_RDWR
	if record.Identity == nil {
		flags |= os.O_CREATE | os.O_EXCL
	} else {
		identity, err := inspectMoveRestoreIdentity(backup)
		if err != nil {
			return nil, err
		}
		if identity != *record.Identity {
			return nil, fmt.Errorf("unknown old source recovery copy")
		}
		if err := tools.VerifySHA256(backup, record.SHA256); err == nil {
			record.Ready = true
			return record, saveOldSourceRestoreRecord(journal, record)
		}
	}
	output, err := os.OpenFile(backup, flags, 0o700)
	if err != nil {
		return nil, err
	}
	defer func() { _ = output.Close() }()
	identity, links, err := moveRestoreFileIdentity(output)
	if err != nil {
		return nil, err
	}
	if record.Identity != nil && identity != *record.Identity {
		return nil, fmt.Errorf("old source recovery copy changed")
	}
	if links != 1 {
		return nil, fmt.Errorf("old source recovery copy has unknown hardlink aliases")
	}
	// A crash before this journal write leaves only an unproven sibling file.
	// The old root and source are intact, so redelivery can fail terminally.
	record.Identity, record.Ready = &identity, false
	if err := saveOldSourceRestoreRecord(journal, record); err != nil {
		return nil, err
	}
	input, err := os.Open(target)
	if err != nil {
		return nil, err
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("move recovery source is not regular")
	}
	if err := output.Truncate(0); err != nil {
		return nil, err
	}
	if _, err := io.Copy(output, input); err != nil {
		return nil, err
	}
	if err := output.Chmod(info.Mode().Perm()); err != nil {
		return nil, err
	}
	if err := output.Sync(); err != nil {
		return nil, err
	}
	if err := output.Close(); err != nil {
		return nil, err
	}
	if err := tools.VerifySHA256(backup, record.SHA256); err != nil {
		return nil, err
	}
	record.Ready = true
	return record, saveOldSourceRestoreRecord(journal, record)
}

func restoreMoveSource(file service.MoveFileIdentity, staging string, index int) error {
	record, err := loadOldSourceRestoreRecord(oldSourceRestoreJournal(staging, index))
	if err != nil {
		return err
	}
	if record == nil || !record.Ready || record.Source == nil || record.Identity == nil || record.Token == uuid.Nil || record.SHA256 != file.SHA256 {
		return fmt.Errorf("old source restoration witnesses were not prepared before switch")
	}
	sourceIdentity, err := inspectMoveRestoreIdentity(file.SourcePath)
	sourceExists := err == nil
	if sourceExists {
		if sourceIdentity != *record.Source && sourceIdentity != *record.Identity {
			return fmt.Errorf("unknown old source blocks restoration")
		}
		if err := tools.VerifySHA256(file.SourcePath, file.SHA256); err == nil {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	original, backup := oldSourceRestorePaths(file.SourcePath, record.Token)
	recovery := ""
	for _, candidate := range []struct {
		path     string
		identity moveRestoreIdentity
	}{{backup, *record.Identity}, {original, *record.Source}} {
		identity, err := inspectMoveRestoreIdentity(candidate.path)
		if err != nil || identity != candidate.identity {
			continue
		}
		if err := tools.VerifySHA256(candidate.path, file.SHA256); err == nil {
			recovery = candidate.path
			break
		}
	}
	if recovery == "" {
		return fmt.Errorf("no owned old source restoration witness remains valid")
	}
	if sourceExists {
		current, err := inspectMoveRestoreIdentity(file.SourcePath)
		if err != nil {
			return err
		}
		if current != sourceIdentity {
			return fmt.Errorf("old source changed before restoration")
		}
		if err := os.Remove(file.SourcePath); err != nil {
			return err
		}
	}
	// No allocation, truncation, copy, or journal publication after switch.
	// This same-volume link is atomic and cannot overwrite an operator file.
	return os.Link(recovery, file.SourcePath)
}

func cleanupOldSourceRestoreStaging(snapshot service.MoveSnapshot, operationID uuid.UUID) error {
	staging := filepath.Join(snapshot.NewRoot, ".staging", operationID.String())
	for index, file := range snapshot.Files {
		record, err := loadOldSourceRestoreRecord(oldSourceRestoreJournal(staging, index))
		if err != nil {
			return err
		}
		if record == nil || record.Token == uuid.Nil {
			continue
		}
		original, backup := oldSourceRestorePaths(file.SourcePath, record.Token)
		for _, witness := range []struct {
			path     string
			identity *moveRestoreIdentity
		}{{original, record.Source}, {backup, record.Identity}} {
			if witness.identity == nil {
				continue
			}
			info, err := os.Lstat(witness.path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			identity, err := inspectMoveRestoreIdentity(witness.path)
			if err != nil {
				return err
			}
			if identity != *witness.identity {
				continue
			}
			if err := os.Remove(witness.path); err != nil {
				return err
			}
		}
	}
	return nil
}

func moveConflictRestoreTemporary(target string, operationID uuid.UUID, index int) string {
	return filepath.Join(filepath.Dir(target), fmt.Sprintf(".melotrove-restore-%s-%d", operationID, index))
}

func restoreMoveConflictBackup(backup, target string, operationID uuid.UUID, index int) error {
	temp := moveConflictRestoreTemporary(target, operationID, index)
	if err := os.Link(backup, temp); err != nil {
		if !os.IsExist(err) {
			return err
		}
		backupInfo, backupErr := os.Lstat(backup)
		if backupErr != nil {
			return backupErr
		}
		tempInfo, tempErr := os.Lstat(temp)
		if tempErr != nil {
			return tempErr
		}
		if !os.SameFile(backupInfo, tempInfo) {
			return fmt.Errorf("unknown restore temporary blocks restoration")
		}
	}
	if err := os.Rename(temp, target); err != nil {
		if cleanupErr := os.Remove(temp); cleanupErr != nil && !os.IsNotExist(cleanupErr) {
			return errors.Join(err, cleanupErr)
		}
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
		record, err := loadOldSourceRestoreRecord(oldSourceRestoreJournal(staging, index))
		if err != nil {
			return err
		}
		if record == nil || !record.Ready || record.Source == nil || record.Identity == nil || record.Token == uuid.Nil {
			return fmt.Errorf("old source restoration witnesses were not prepared before cleanup")
		}
		original, backup := oldSourceRestorePaths(file.SourcePath, record.Token)
		originalIdentity, err := inspectMoveRestoreIdentity(original)
		if err != nil {
			return err
		}
		backupIdentity, err := inspectMoveRestoreIdentity(backup)
		if err != nil {
			return err
		}
		if originalIdentity != *record.Source || backupIdentity != *record.Identity {
			return fmt.Errorf("old source restoration witness ownership changed")
		}
		if err := tools.VerifySHA256(backup, file.SHA256); err != nil {
			return err
		}
		sourceIdentity, err := inspectMoveRestoreIdentity(file.SourcePath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if sourceIdentity != originalIdentity {
			return fmt.Errorf("unknown old managed source blocks cleanup")
		}
		if err := tools.VerifySHA256(file.SourcePath, file.SHA256); err != nil {
			return err
		}
		if err := os.Remove(file.SourcePath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (worker *MoveWorker) rollbackSwitched(ctx context.Context, operation *persistence.Operation, snapshot service.MoveSnapshot, staging string, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if operation.Stage != "rollback_pending" {
		if err := worker.operations.Running(ctx, operation.ID, "rollback_pending"); err != nil {
			return err
		}
	}
	for index, file := range snapshot.Files {
		if err := restoreMoveSource(file, staging, index); err != nil {
			return err
		}
	}
	publication, err := loadMovePublication(staging, operation.ID, snapshot)
	if err != nil {
		return err
	}
	ownedTargets := []string(nil)
	if publication != nil {
		ownedTargets, err = ownedMoveTargets(snapshot, staging, publication)
		if err != nil {
			return err
		}
	}
	if err := restoreMoveTargets(snapshot, staging, operation.ID, ownedTargets); err != nil {
		return err
	}
	if err := worker.repository.RollbackToolsRootMove(ctx, operation.ID, snapshot.OldRoot, snapshot.NewRoot); err != nil {
		return err
	}
	worker.operations.Notify(operation.ID)
	if err := cleanupOldSourceRestoreStaging(snapshot, operation.ID); err != nil {
		return err
	}
	if err := tools.CleanupOperationStaging(snapshot.NewRoot, operation.ID); err != nil {
		return err
	}
	return worker.fail(ctx, operation, cause)
}

func restoreMoveTargets(snapshot service.MoveSnapshot, staging string, operationID uuid.UUID, ownedTargets []string) error {
	var rollbackErrors []error
	owned := make(map[string]struct{}, len(ownedTargets))
	for _, path := range ownedTargets {
		owned[filepath.Clean(path)] = struct{}{}
	}
	for index := len(snapshot.Files) - 1; index >= 0; index-- {
		file := snapshot.Files[index]
		backup := filepath.Join(staging, "target-backups", strconv.Itoa(index))
		if _, err := os.Lstat(backup); err == nil {
			published, publishErr := moveFileWasPublished(file, staging, index)
			if publishErr != nil {
				rollbackErrors = append(rollbackErrors, publishErr)
				continue
			}
			targetInfo, targetErr := os.Lstat(file.TargetPath)
			if targetErr == nil && !published {
				backupInfo, backupErr := os.Lstat(backup)
				if backupErr != nil {
					rollbackErrors = append(rollbackErrors, backupErr)
					continue
				}
				if !os.SameFile(targetInfo, backupInfo) {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("unknown target blocks restoration of confirmed move conflict"))
					continue
				}
				continue
			}
			if targetErr != nil && !os.IsNotExist(targetErr) {
				rollbackErrors = append(rollbackErrors, targetErr)
				continue
			}
			if published {
				if err := os.Remove(file.TargetPath); err != nil {
					rollbackErrors = append(rollbackErrors, err)
					continue
				}
			}
			if err := restoreMoveConflictBackup(backup, file.TargetPath, operationID, index); err != nil {
				rollbackErrors = append(rollbackErrors, err)
			}
			continue
		}
		if _, wasOwned := owned[filepath.Clean(file.TargetPath)]; wasOwned {
			published, err := moveFileWasPublished(file, staging, index)
			if err != nil {
				rollbackErrors = append(rollbackErrors, err)
				continue
			}
			if published {
				if err := os.Remove(file.TargetPath); err != nil {
					rollbackErrors = append(rollbackErrors, err)
				}
			}
		}
	}
	if len(rollbackErrors) > 0 {
		return fmt.Errorf("move target rollback incomplete: %w", errors.Join(rollbackErrors...))
	}
	return nil
}

func (worker *MoveWorker) rollbackTargets(ctx context.Context, operation *persistence.Operation, snapshot service.MoveSnapshot, staging string, ownedTargets []string, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := restoreMoveTargets(snapshot, staging, operation.ID, ownedTargets); err != nil {
		return err
	}
	if err := cleanupOldSourceRestoreStaging(snapshot, operation.ID); err != nil {
		return fmt.Errorf("move rollback cleanup failed: %w", err)
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
