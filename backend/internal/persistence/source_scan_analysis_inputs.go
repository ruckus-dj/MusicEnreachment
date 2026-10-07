package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

// SourceScanToolHold describes the executable resolved while the tools-root
// reader gate and installation hold are both active.
type SourceScanToolHold struct {
	ToolsRoot          string
	ExecutablePath     string
	Installation       ToolInstallation
	ExecutableVersions map[string]string
}

// GetInstallation loads verified managed-tool metadata for the durable scan
// snapshot captured at start time.
func (repository *SourceInventoryRepository) GetInstallation(ctx context.Context, id uuid.UUID) (*ToolInstallation, error) {
	installation := new(ToolInstallation)
	if err := repository.db.NewSelect().Model(installation).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, fmt.Errorf("get source scan tool installation: %w", err)
	}
	return installation, nil
}

// AcquireSourceScanToolHold pins one selected installation for the duration of
// an actual cache-miss execution. A cache hit never calls this method.
func (repository *SourceInventoryRepository) AcquireSourceScanToolHold(
	ctx context.Context,
	operationID, rootID uuid.UUID,
	configuredPath string,
	operationAttempt int,
	jobID int64,
	selection SourceAnalysisToolSelection,
) (SourceScanToolHold, func() error, error) {
	if operationID == uuid.Nil || rootID == uuid.Nil || configuredPath == "" || operationAttempt < 1 || jobID < 1 ||
		selection.InstallationID == uuid.Nil || selection.PackageKind == "" || selection.RelativePath == "" || selection.Executable == "" || selection.Version == "" || selection.VersionBanner == "" {
		return SourceScanToolHold{}, nil, fmt.Errorf("acquire source scan tool hold: complete operation fence and pinned selection are required")
	}
	var heldTool SourceScanToolHold
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveReaders(ctx, tx); err != nil {
			return fmt.Errorf("lock tools-root reader gate: %w", err)
		}
		moving, err := activeToolsMoveExists(ctx, tx)
		if err != nil {
			return fmt.Errorf("check active tools-root move: %w", err)
		}
		if moving {
			return ErrToolsRootMoveActive
		}
		if err := lockPackageSelections(ctx, tx, []string{selection.PackageKind}); err != nil {
			return fmt.Errorf("lock selected package: %w", err)
		}
		var currentPath string
		if err := tx.NewRaw(`SELECT configured_path FROM source_root WHERE id=?`, rootID).Scan(ctx, &currentPath); err != nil {
			return fmt.Errorf("read scan source root: %w", err)
		}
		if currentPath != configuredPath {
			return fmt.Errorf("scan source root path changed")
		}
		if err := lockToolsRootsReaders(ctx, tx, []string{configuredPath}); err != nil {
			return fmt.Errorf("lock scan source root: %w", err)
		}
		var root SourceRoot
		if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR SHARE`, rootID).Scan(ctx, &root); err != nil {
			return fmt.Errorf("lock scan source root row: %w", err)
		}
		if !root.Enabled || root.ConfiguredPath != configuredPath {
			return fmt.Errorf("scan source root is no longer enabled at the pinned path")
		}
		if err := lockCompatibleInstallations(ctx, tx, []uuid.UUID{selection.InstallationID}); err != nil {
			return fmt.Errorf("lock pinned installation: %w", err)
		}
		var operation Operation
		if err := tx.NewRaw(`SELECT * FROM operation WHERE id=? FOR UPDATE`, operationID).Scan(ctx, &operation); err != nil {
			return fmt.Errorf("lock scan operation: %w", err)
		}
		if operation.Kind != "scan_source" || operation.State != "running" || operation.Attempt != operationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != jobID ||
			operation.TargetSourceRootID == nil || *operation.TargetSourceRootID != rootID {
			return fmt.Errorf("source scan tool hold fence is stale")
		}
		var snapshot struct {
			SchemaVersion  int                           `json:"schema_version"`
			SourceRootID   uuid.UUID                     `json:"source_root_id"`
			ConfiguredPath string                        `json:"configured_path"`
			Tools          []SourceAnalysisToolSelection `json:"tools"`
		}
		if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil || snapshot.SchemaVersion != SourceScanSnapshotVersion || snapshot.SourceRootID != rootID || snapshot.ConfiguredPath != configuredPath {
			return fmt.Errorf("source scan tool hold snapshot is invalid")
		}
		pinned := false
		for _, candidate := range snapshot.Tools {
			if candidate == selection {
				pinned = true
				break
			}
		}
		if !pinned {
			return fmt.Errorf("source scan tool selection is not pinned by the operation")
		}
		installation := new(ToolInstallation)
		if err := tx.NewRaw(`SELECT * FROM tool_installation WHERE id=?`, selection.InstallationID).Scan(ctx, installation); err != nil {
			return fmt.Errorf("read pinned installation: %w", err)
		}
		if installation.PackageKind != selection.PackageKind || installation.RelativePath != filepath.ToSlash(selection.RelativePath) || installation.State != "ready" || installation.VerifiedAt == nil {
			return fmt.Errorf("pinned installation no longer matches scan selection")
		}
		if !validInstallationRelativePath(installation) || !(tools.Platform{GOOS: installation.PlatformGOOS, GOARCH: installation.PlatformGOARCH}).Supported() {
			return fmt.Errorf("pinned installation path or platform is unsupported")
		}
		var toolsRoot string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='tools_directory'`).Scan(ctx, &toolsRoot); err != nil {
			return fmt.Errorf("read current tools directory: %w", err)
		}
		normalizedRoot, err := settings.NormalizePath(toolsRoot)
		if err != nil || normalizedRoot != filepath.Clean(toolsRoot) {
			return fmt.Errorf("current tools directory is not a normalized absolute path")
		}
		executableName := ""
		for _, candidate := range tools.ExpectedExecutables(tools.PackageKind(selection.PackageKind), installation.PlatformGOOS) {
			base := strings.TrimSuffix(candidate, filepath.Ext(candidate))
			if selection.Executable == candidate || selection.Executable == base {
				executableName = candidate
				break
			}
		}
		if executableName == "" {
			return fmt.Errorf("pinned executable is unsupported for its package")
		}
		executablePath := filepath.Join(normalizedRoot, filepath.FromSlash(installation.RelativePath), executableName)
		resolvedExecutable, err := filepath.EvalSymlinks(executablePath)
		if err != nil {
			return fmt.Errorf("resolve pinned managed executable: %w", err)
		}
		relativeExecutable, err := filepath.Rel(normalizedRoot, resolvedExecutable)
		if err != nil || relativeExecutable == ".." || strings.HasPrefix(relativeExecutable, ".."+string(filepath.Separator)) {
			return fmt.Errorf("pinned managed executable resolves outside the tools directory")
		}
		info, err := os.Lstat(executablePath)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("pinned managed executable is missing or not a regular file")
		}
		var versions map[string]string
		if json.Unmarshal(installation.ExecutableVersions, &versions) != nil || strings.TrimSpace(versions[executableName]) != selection.VersionBanner && strings.TrimSpace(versions[selection.Executable]) != selection.VersionBanner {
			return fmt.Errorf("pinned executable version no longer matches verified installation metadata")
		}
		heldTool = SourceScanToolHold{
			ToolsRoot:          normalizedRoot,
			ExecutablePath:     executablePath,
			Installation:       *installation,
			ExecutableVersions: versions,
		}
		var held bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation_tool_read_hold WHERE operation_id=? AND installation_id=?)`, operationID, selection.InstallationID).Scan(ctx, &held); err != nil {
			return fmt.Errorf("check existing scan tool hold: %w", err)
		}
		var anyHeld bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation_tool_read_hold WHERE operation_id=?)`, operationID).Scan(ctx, &anyHeld); err != nil {
			return fmt.Errorf("read normalized tool holds: %w", err)
		}
		if operation.ToolsReadRequired != anyHeld {
			return fmt.Errorf("scan operation tool-read flag does not match normalized holds")
		}
		if !held {
			if _, err := tx.NewRaw(`INSERT INTO operation_tool_read_hold(operation_id,installation_id) VALUES (?,?)`, operationID, selection.InstallationID).Exec(ctx); err != nil {
				return fmt.Errorf("persist scan tool hold: %w", err)
			}
		}
		if _, err := tx.NewRaw(`UPDATE operation SET tools_read_required=true WHERE id=? AND attempt=? AND river_job_id=?`, operationID, operationAttempt, jobID).Exec(ctx); err != nil {
			return fmt.Errorf("mark scan operation as reading tools: %w", err)
		}
		return nil
	})
	if err != nil {
		return SourceScanToolHold{}, nil, fmt.Errorf("acquire source scan tool hold: %w", err)
	}
	return heldTool, func() error {
		return repository.releaseSourceScanToolHold(context.WithoutCancel(ctx), operationID, selection.InstallationID, operationAttempt, jobID)
	}, nil
}

func (repository *SourceInventoryRepository) releaseSourceScanToolHold(ctx context.Context, operationID, installationID uuid.UUID, attempt int, jobID int64) error {
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var held bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation_tool_read_hold WHERE operation_id=? AND installation_id=?)`, operationID, installationID).Scan(ctx, &held); err != nil {
			return fmt.Errorf("check scan tool hold before release: %w", err)
		}
		if !held {
			return nil
		}
		var operation Operation
		if err := tx.NewRaw(`SELECT * FROM operation WHERE id=? FOR UPDATE`, operationID).Scan(ctx, &operation); err != nil {
			return fmt.Errorf("lock scan operation for hold release: %w", err)
		}
		if operation.State != "running" || operation.Attempt != attempt || operation.RiverJobID == nil || *operation.RiverJobID != jobID {
			return fmt.Errorf("scan tool hold release fence is stale")
		}
		if _, err := tx.NewRaw(`DELETE FROM operation_tool_read_hold WHERE operation_id=? AND installation_id=?`, operationID, installationID).Exec(ctx); err != nil {
			return fmt.Errorf("release scan tool hold: %w", err)
		}
		var anyHeld bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation_tool_read_hold WHERE operation_id=?)`, operationID).Scan(ctx, &anyHeld); err != nil {
			return fmt.Errorf("recheck scan tool holds: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE operation SET tools_read_required=? WHERE id=? AND attempt=? AND river_job_id=?`, anyHeld, operationID, attempt, jobID).Exec(ctx); err != nil {
			return fmt.Errorf("update scan tool-read requirement: %w", err)
		}
		return nil
	})
}
