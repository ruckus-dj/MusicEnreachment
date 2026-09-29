package jobs

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

var errInstallConflict = errors.New("installation target changed after preflight")

type installPublicationFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type installPublication struct {
	OperationID    uuid.UUID                `json:"operation_id"`
	InstallationID uuid.UUID                `json:"installation_id"`
	Root           string                   `json:"root"`
	PackageKind    tools.PackageKind        `json:"package_kind"`
	Release        string                   `json:"release"`
	Confirmed      []string                 `json:"confirmed"`
	Files          []installPublicationFile `json:"files"`
	Mode           string                   `json:"mode,omitempty"`
}

func installPublicationPath(staging string) string {
	return filepath.Join(staging, "publication.json")
}

func installCandidate(staging string, relative string, name string) string {
	return filepath.Join(staging, "candidate", relative, name)
}

func installTarget(root string, relative string, name string) string {
	return filepath.Join(root, relative, name)
}

func installBackup(staging string, name string) string {
	return filepath.Join(staging, "backups", name)
}

func sameInstallPaths(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func loadInstallPublication(staging string, operation *persistence.Operation, installation *persistence.ToolInstallation, snapshot service.InstallInputSnapshot, root, goos string) (*installPublication, error) {
	path := installPublicationPath(staging)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect installation publication: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return nil, fmt.Errorf("installation publication has an invalid file type or size")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read installation publication: %w", err)
	}
	var publication installPublication
	if err := json.Unmarshal(raw, &publication); err != nil {
		return nil, fmt.Errorf("decode installation publication: %w", err)
	}
	expected := tools.ExpectedExecutables(snapshot.PackageKind, goos)
	if publication.OperationID != operation.ID || publication.InstallationID != installation.ID ||
		publication.Root != root || publication.PackageKind != snapshot.PackageKind ||
		publication.Release != snapshot.ReleaseIdentity ||
		(publication.Mode != "" && publication.Mode != "rollback") || len(publication.Files) != len(expected) {
		return nil, fmt.Errorf("installation publication identity changed")
	}
	confirmed := append([]string(nil), snapshot.ConfirmedConflicts...)
	sort.Strings(confirmed)
	if !sameInstallPaths(publication.Confirmed, confirmed) {
		return nil, fmt.Errorf("installation publication conflicts changed")
	}
	for index, name := range expected {
		file := publication.Files[index]
		if file.Name != name || len(file.SHA256) != 64 {
			return nil, fmt.Errorf("installation publication executable identity changed")
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return nil, fmt.Errorf("installation publication executable digest is invalid")
		}
	}
	return &publication, nil
}

func saveInstallPublication(staging string, publication *installPublication) error {
	raw, err := json.Marshal(publication)
	if err != nil {
		return fmt.Errorf("encode installation publication: %w", err)
	}
	file, err := os.CreateTemp(staging, ".publication-")
	if err != nil {
		return fmt.Errorf("create installation publication: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return fmt.Errorf("write installation publication: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync installation publication: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close installation publication: %w", err)
	}
	if err := os.Rename(file.Name(), installPublicationPath(staging)); err != nil {
		return fmt.Errorf("publish installation journal: %w", err)
	}
	return nil
}

func prepareInstallPublication(staging string, operation *persistence.Operation, installation *persistence.ToolInstallation, snapshot service.InstallInputSnapshot, root, goos string) (*installPublication, error) {
	preflight, err := tools.PreflightTargets(root, snapshot.PackageKind, snapshot.ReleaseIdentity, goos, map[string]struct{}{})
	if err != nil {
		return nil, err
	}
	confirmed := append([]string(nil), snapshot.ConfirmedConflicts...)
	sort.Strings(confirmed)
	sort.Strings(preflight.Conflicts)
	if !sameInstallPaths(preflight.Conflicts, confirmed) {
		return nil, errInstallConflict
	}
	relative, err := tools.ManagedRelativePath(snapshot.PackageKind, snapshot.ReleaseIdentity)
	if err != nil {
		return nil, err
	}
	publication := &installPublication{
		OperationID: operation.ID, InstallationID: installation.ID,
		Root: root, PackageKind: snapshot.PackageKind, Release: snapshot.ReleaseIdentity,
		Confirmed: confirmed, Files: make([]installPublicationFile, 0, len(preflight.Targets)),
	}
	for _, name := range tools.ExpectedExecutables(snapshot.PackageKind, goos) {
		candidate := installCandidate(staging, relative, name)
		digest, err := tools.SHA256File(candidate)
		if err != nil {
			return nil, fmt.Errorf("hash verified executable: %w", err)
		}
		target := installTarget(root, relative, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, fmt.Errorf("prepare executable destination: %w", err)
		}
		publication.Files = append(publication.Files, installPublicationFile{Name: name, SHA256: digest})
	}
	if err := saveInstallPublication(staging, publication); err != nil {
		return nil, err
	}
	return publication, nil
}

func (worker *InstallationWorker) resumeInstallPublication(ctx context.Context, operation *persistence.Operation, installation *persistence.ToolInstallation, snapshot service.InstallInputSnapshot, root, staging string, publication *installPublication) error {
	if publication.Mode == "rollback" {
		return worker.rollbackInstallPublication(ctx, operation, installation, root, staging, publication)
	}
	if err := worker.publishInstallFiles(ctx, staging, publication); err != nil {
		if ctx.Err() != nil || !errors.Is(err, errInstallConflict) {
			return err
		}
		publication.Mode = "rollback"
		if err := saveInstallPublication(staging, publication); err != nil {
			return err
		}
		return worker.rollbackInstallPublication(ctx, operation, installation, root, staging, publication)
	}
	versions, err := worker.lifecycle.VerifyInstallation(ctx, root, installation.RelativePath, snapshot.PackageKind, snapshot.ReleaseIdentity, worker.platform.GOOS)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		publication.Mode = "rollback"
		if journalErr := saveInstallPublication(staging, publication); journalErr != nil {
			return journalErr
		}
		return worker.rollbackInstallPublication(ctx, operation, installation, root, staging, publication)
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
	return worker.finish(ctx, operation, installation, root, snapshot)
}

func (worker *InstallationWorker) publishInstallFiles(ctx context.Context, staging string, publication *installPublication) error {
	relative, err := tools.ManagedRelativePath(publication.PackageKind, publication.Release)
	if err != nil {
		return err
	}
	confirmed := make(map[string]struct{}, len(publication.Confirmed))
	for _, path := range publication.Confirmed {
		confirmed[path] = struct{}{}
	}
	for _, file := range publication.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		candidate := installCandidate(staging, relative, file.Name)
		if err := tools.VerifySHA256(candidate, file.SHA256); err != nil {
			return fmt.Errorf("verified executable changed during publication: %w", err)
		}
		target := installTarget(publication.Root, relative, file.Name)
		backup := installBackup(staging, file.Name)
		_, isConfirmed := confirmed[target]
		targetInfo, targetErr := os.Lstat(target)
		if targetErr == nil {
			candidateInfo, err := os.Lstat(candidate)
			if err != nil {
				return err
			}
			if targetInfo.Mode().IsRegular() && os.SameFile(targetInfo, candidateInfo) {
				if isConfirmed {
					if _, err := os.Lstat(backup); err != nil {
						return fmt.Errorf("%w: confirmed original backup is missing", errInstallConflict)
					}
				}
				continue
			}
		} else if !os.IsNotExist(targetErr) {
			return targetErr
		}
		if isConfirmed {
			if _, err := os.Lstat(backup); os.IsNotExist(err) {
				if targetErr != nil {
					return fmt.Errorf("%w: confirmed original disappeared", errInstallConflict)
				}
				if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
					return err
				}
				if err := os.Rename(target, backup); err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else if targetErr == nil {
				return fmt.Errorf("%w: another file appeared at confirmed target", errInstallConflict)
			}
		} else if targetErr == nil {
			return fmt.Errorf("%w: unknown executable exists", errInstallConflict)
		}
		if err := os.Link(candidate, target); err != nil {
			if os.IsExist(err) {
				return fmt.Errorf("%w: executable appeared during publication", errInstallConflict)
			}
			return fmt.Errorf("%w: cannot link verified executable: %v", errInstallConflict, err)
		}
	}
	return nil
}

func (worker *InstallationWorker) rollbackInstallPublication(ctx context.Context, operation *persistence.Operation, installation *persistence.ToolInstallation, root, staging string, publication *installPublication) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	relative, err := tools.ManagedRelativePath(publication.PackageKind, publication.Release)
	if err != nil {
		return err
	}
	for index := len(publication.Files) - 1; index >= 0; index-- {
		file := publication.Files[index]
		candidate := installCandidate(staging, relative, file.Name)
		target := installTarget(root, relative, file.Name)
		backup := installBackup(staging, file.Name)
		candidateInfo, err := os.Lstat(candidate)
		if err != nil {
			return fmt.Errorf("read publication ownership witness: %w", err)
		}
		if targetInfo, err := os.Lstat(target); err == nil {
			if targetInfo.Mode().IsRegular() && os.SameFile(candidateInfo, targetInfo) {
				if err := os.Remove(target); err != nil {
					return err
				}
			} else if _, backupErr := os.Lstat(backup); backupErr == nil {
				return fmt.Errorf("unknown executable blocks restoration of confirmed original")
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if _, err := os.Lstat(backup); err == nil {
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				return fmt.Errorf("unknown executable blocks restoration of confirmed original")
			}
			if err := os.Rename(backup, target); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if installation.State == "preparing" {
		if err := worker.repository.MarkInstallationFailed(ctx, installation.ID); err != nil {
			return err
		}
		installation.State = "failed"
	}
	if err := worker.operations.Fail(ctx, operation.ID, "materialize", safeInstallationError("materialize")); err != nil {
		return err
	}
	return tools.CleanupOperationStaging(root, operation.ID)
}
