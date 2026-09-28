package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type MoveRepository interface {
	ListInstallations(context.Context, string, string, string) ([]persistence.ToolInstallation, error)
	CreateOperationAndEnqueue(context.Context, *persistence.Operation, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) error
}

type MoveFileIdentity struct {
	InstallationID  uuid.UUID         `json:"installation_id"`
	PackageKind     tools.PackageKind `json:"package_kind"`
	ReleaseIdentity string            `json:"release_identity"`
	RelativePath    string            `json:"relative_path"`
	Executable      string            `json:"executable"`
	SourcePath      string            `json:"source_path"`
	TargetPath      string            `json:"target_path"`
	Size            int64             `json:"size"`
	SHA256          string            `json:"sha256"`
}

type MoveSnapshot struct {
	SchemaVersion      int                `json:"schema_version"`
	OldRoot            string             `json:"old_root"`
	NewRoot            string             `json:"new_root"`
	Files              []MoveFileIdentity `json:"files"`
	ConfirmedConflicts []string           `json:"confirmed_conflicts,omitempty"`
	RemoveOldFiles     bool               `json:"remove_old_files"`
}

type MovePreflight struct {
	Snapshot    MoveSnapshot
	Conflicts   []string
	Fingerprint string
}

type MoveTools struct {
	repository MoveRepository
	settings   *settings.Registry
	platform   tools.Platform
	river      persistence.RiverInserter
}

func NewMoveTools(repository MoveRepository, registry *settings.Registry, platform tools.Platform, riverClient persistence.RiverInserter) *MoveTools {
	return &MoveTools{repository: repository, settings: registry, platform: platform, river: riverClient}
}

func (service *MoveTools) Preflight(ctx context.Context, newRoot string, removeOldFiles bool) (MovePreflight, error) {
	if !service.platform.Supported() {
		return MovePreflight{}, fmt.Errorf("unsupported instance platform")
	}
	oldRoot, hasOldRoot, err := service.settings.GetToolsDirectory(ctx)
	if err != nil {
		return MovePreflight{}, fmt.Errorf("read current tools directory: %w", err)
	}
	if !hasOldRoot || oldRoot == "" {
		return MovePreflight{}, fmt.Errorf("current tools directory is not configured")
	}
	newRoot, err = settings.NormalizePath(newRoot)
	if err != nil {
		return MovePreflight{}, fmt.Errorf("new tools directory: %w", err)
	}
	if settings.PathsOverlap(oldRoot, newRoot) {
		return MovePreflight{}, fmt.Errorf("new tools directory overlaps current tools directory")
	}
	outputRoot, hasOutput, err := service.settings.GetOutputDirectory(ctx)
	if err != nil {
		return MovePreflight{}, fmt.Errorf("read output directory: %w", err)
	}
	if hasOutput && settings.PathsOverlap(outputRoot, newRoot) {
		return MovePreflight{}, fmt.Errorf("new tools directory overlaps output directory")
	}
	if err := settings.ProbeWritable(newRoot); err != nil {
		return MovePreflight{}, fmt.Errorf("new tools directory: %w", err)
	}
	installations, err := service.repository.ListInstallations(ctx, "", service.platform.GOOS, service.platform.GOARCH)
	if err != nil {
		return MovePreflight{}, fmt.Errorf("list managed installations: %w", err)
	}
	snapshot := MoveSnapshot{
		SchemaVersion: 1, OldRoot: oldRoot, NewRoot: newRoot,
		Files: make([]MoveFileIdentity, 0), RemoveOldFiles: removeOldFiles,
	}
	conflicts := make([]string, 0)
	for _, installation := range installations {
		if installation.State != "ready" {
			continue
		}
		kind := tools.PackageKind(installation.PackageKind)
		relative, err := tools.ManagedRelativePath(kind, installation.ReleaseIdentity)
		if err != nil || filepath.Clean(installation.RelativePath) != relative {
			return MovePreflight{}, fmt.Errorf("managed installation has an invalid relative path")
		}
		for _, executable := range tools.ExpectedExecutables(kind, service.platform.GOOS) {
			source := filepath.Join(oldRoot, relative, executable)
			target := filepath.Join(newRoot, relative, executable)
			info, err := os.Lstat(source)
			if err != nil {
				return MovePreflight{}, fmt.Errorf("inspect managed source executable: %w", err)
			}
			if !info.Mode().IsRegular() {
				return MovePreflight{}, fmt.Errorf("managed source executable has an unsupported file type")
			}
			if err := tools.HasSymlinkAncestors(oldRoot, filepath.Dir(source)); err != nil {
				return MovePreflight{}, err
			}
			if err := tools.HasSymlinkAncestors(newRoot, filepath.Dir(target)); err != nil {
				return MovePreflight{}, err
			}
			if targetInfo, err := os.Lstat(target); err == nil {
				if !targetInfo.Mode().IsRegular() && targetInfo.Mode()&os.ModeSymlink == 0 {
					return MovePreflight{}, fmt.Errorf("move target has an unsupported file type")
				}
				conflicts = append(conflicts, target)
			} else if !os.IsNotExist(err) {
				return MovePreflight{}, fmt.Errorf("inspect move target: %w", err)
			}
			digest, err := tools.SHA256File(source)
			if err != nil {
				return MovePreflight{}, fmt.Errorf("hash managed source executable: %w", err)
			}
			snapshot.Files = append(snapshot.Files, MoveFileIdentity{
				InstallationID: installation.ID,
				PackageKind:    kind, ReleaseIdentity: installation.ReleaseIdentity,
				RelativePath: relative, Executable: executable,
				SourcePath: source, TargetPath: target, Size: info.Size(), SHA256: digest,
			})
		}
	}
	sort.Strings(conflicts)
	fingerprintData, err := json.Marshal(snapshot)
	if err != nil {
		return MovePreflight{}, fmt.Errorf("encode move preflight: %w", err)
	}
	fingerprint := sha256.Sum256(fingerprintData)
	return MovePreflight{Snapshot: snapshot, Conflicts: conflicts, Fingerprint: hex.EncodeToString(fingerprint[:])}, nil
}

func (service *MoveTools) Start(ctx context.Context, preflight MovePreflight, confirmedConflicts []string) (*persistence.Operation, error) {
	current, err := service.Preflight(ctx, preflight.Snapshot.NewRoot, preflight.Snapshot.RemoveOldFiles)
	if err != nil {
		return nil, err
	}
	if current.Fingerprint != preflight.Fingerprint {
		return nil, fmt.Errorf("move preflight is stale")
	}
	confirmed := append([]string(nil), confirmedConflicts...)
	sort.Strings(confirmed)
	if !samePaths(current.Conflicts, confirmed) {
		return nil, fmt.Errorf("move conflict confirmation does not match current targets")
	}
	current.Snapshot.ConfirmedConflicts = confirmed
	snapshot, err := json.Marshal(current.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("encode move snapshot: %w", err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued",
		InputSnapshot: snapshot,
	}
	if service.river == nil {
		return nil, fmt.Errorf("river client is required for tools directory move")
	}
	if err := service.repository.CreateOperationAndEnqueue(ctx, operation, service.river, OperationJobArgs{OperationID: operation.ID}, nil); err != nil {
		return nil, fmt.Errorf("start tools directory move: %w", err)
	}
	return operation, nil
}

func samePaths(first, second []string) bool {
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
