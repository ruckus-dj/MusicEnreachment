package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// ErrSourceRootBusy reports a path change, a disable or a deletion refused
// because a scan of the root is queued or running.
var ErrSourceRootBusy = errors.New("source root has an active scan")

// ErrSourceRootConfirmation reports a deletion whose confirmed configured path
// or location count does not describe the root as it is now.
var ErrSourceRootConfirmation = persistence.ErrSourceRootConfirmation

// ErrSourceRootInaccessible reports a configured source directory that cannot be
// used at all: it is gone, it is no longer a directory, or it cannot be opened
// and read. A managed-path overlap, a duplicate configured path and a failed
// database read are refusals too, but none of them proves the registered
// directory inaccessible and none carries this marker.
var ErrSourceRootInaccessible = errors.New("the source root directory is inaccessible")

// ErrUnsupportedSourceRoot identifies a source-root path syntax this build
// deliberately refuses before touching the filesystem.
var ErrUnsupportedSourceRoot = sourcefs.ErrUnsupportedNetworkRoot

// SourceRootRepository is the persistence contract of the source root service.
type SourceRootRepository interface {
	CreateSourceRoot(context.Context, *persistence.SourceRoot) error
	GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error)
	ListSourceRoots(context.Context) ([]persistence.SourceRoot, error)
	UpdateSourceRoot(context.Context, *persistence.SourceRoot) error
	DeleteSourceRoot(context.Context, uuid.UUID, string, int64) error
	CountSourceLocations(context.Context, uuid.UUID) (int64, error)
}

// ManagedPathsReader exposes the managed roots a source path must never overlap.
type ManagedPathsReader interface {
	GetToolsDirectory(context.Context) (string, bool, error)
	GetOutputDirectory(context.Context) (string, bool, error)
}

type sourceRootPendingAdmitter interface {
	AdmitPending(context.Context, uuid.UUID) (*persistence.Operation, error)
}

// SourceRoot is the operator-facing state of one registered source root. The
// stale flag and the location count are derived here, so no caller compares
// inventory_path with configured_path itself or counts rows.
type SourceRoot struct {
	ID                   uuid.UUID
	DisplayName          string
	ConfiguredPath       string
	Enabled              bool
	Status               string
	SafeError            *string
	InventoryPath        *string
	Stale                bool
	ScanGeneration       int64
	LastSuccessfulScanAt *time.Time
	LocationCount        int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// SourceRootEdit carries the fields an edit may change. A nil field keeps the
// stored value, and Enabled is a pointer because false is a value an operator
// sets, not an omission.
type SourceRootEdit struct {
	DisplayName    *string
	ConfiguredPath *string
	Enabled        *bool
}

// SourceRoots manages registered source directories. It only records and
// validates paths: a source directory belongs to the operator, MeloTrove reads
// it, and nothing is ever created or probed inside it.
type SourceRoots struct {
	repository   SourceRootRepository
	managedPaths ManagedPathsReader
	pending      sourceRootPendingAdmitter
}

func NewSourceRoots(repository SourceRootRepository, managedPaths ManagedPathsReader) *SourceRoots {
	return &SourceRoots{repository: repository, managedPaths: managedPaths}
}

// SetPendingDispatcher wires best-effort pending analysis admission after an
// enabled root is committed.
func (s *SourceRoots) SetPendingDispatcher(dispatcher sourceRootPendingAdmitter) {
	s.pending = dispatcher
}

// Create registers a new enabled root. The display name must not be empty and
// the path must pass ValidateSourcePath. Nothing is written to the directory.
func (s *SourceRoots) Create(ctx context.Context, displayName, configuredPath string) (SourceRoot, error) {
	name, err := sourceRootDisplayName(displayName)
	if err != nil {
		return SourceRoot{}, err
	}
	path, err := s.ValidateSourcePath(ctx, configuredPath, nil)
	if err != nil {
		return SourceRoot{}, fmt.Errorf("source root path: %w", err)
	}
	root := &persistence.SourceRoot{DisplayName: name, ConfiguredPath: path, Enabled: true}
	if err := s.repository.CreateSourceRoot(ctx, root); err != nil {
		return SourceRoot{}, fmt.Errorf("create source root: %w", err)
	}
	return s.Get(ctx, root.ID)
}

func (s *SourceRoots) Get(ctx context.Context, id uuid.UUID) (SourceRoot, error) {
	root, err := s.repository.GetSourceRoot(ctx, id)
	if err != nil {
		return SourceRoot{}, fmt.Errorf("read source root: %w", err)
	}
	return s.view(ctx, root)
}

func (s *SourceRoots) List(ctx context.Context) ([]SourceRoot, error) {
	roots, err := s.repository.ListSourceRoots(ctx)
	if err != nil {
		return nil, fmt.Errorf("list source roots: %w", err)
	}
	views := make([]SourceRoot, 0, len(roots))
	for index := range roots {
		view, err := s.view(ctx, &roots[index])
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

// Edit changes the display name, the configured path or the enabled flag. A path
// is re-validated before it is stored, and a changed path keeps the previous
// inventory, which the returned view reports as stale until a successful scan of
// the new path replaces it. A path or enabled change is refused with
// ErrSourceRootBusy while a scan of the root is active; a name change is not.
func (s *SourceRoots) Edit(ctx context.Context, id uuid.UUID, edit SourceRootEdit) (SourceRoot, error) {
	current, err := s.repository.GetSourceRoot(ctx, id)
	if err != nil {
		return SourceRoot{}, fmt.Errorf("read source root: %w", err)
	}
	updated := *current
	if edit.DisplayName != nil {
		name, err := sourceRootDisplayName(*edit.DisplayName)
		if err != nil {
			return SourceRoot{}, err
		}
		updated.DisplayName = name
	}
	if edit.ConfiguredPath != nil && *edit.ConfiguredPath != current.ConfiguredPath {
		path, err := s.ValidateSourcePath(ctx, *edit.ConfiguredPath, &current.ID)
		if err != nil {
			return SourceRoot{}, fmt.Errorf("source root path: %w", err)
		}
		updated.ConfiguredPath = path
	}
	if edit.Enabled != nil {
		updated.Enabled = *edit.Enabled
	}
	if err := s.repository.UpdateSourceRoot(ctx, &updated); err != nil {
		if errors.Is(err, persistence.ErrSourceRootActiveScan) {
			return SourceRoot{}, fmt.Errorf("edit source root: %w", ErrSourceRootBusy)
		}
		return SourceRoot{}, fmt.Errorf("edit source root: %w", err)
	}
	if !current.Enabled && updated.Enabled && s.pending != nil {
		if _, err := s.pending.AdmitPending(context.WithoutCancel(ctx), id); err != nil {
			slog.WarnContext(ctx, "pending source analysis admission failed after source root enabled", "root", id, "cause", err)
		}
	}
	return s.Get(ctx, id)
}

// Delete removes a root and its inventory. The operator confirms the exact
// configured path and the number of locations the deletion discards; the count
// is read again here, so a stale confirmation is refused. Only database rows are
// removed: no source file and nothing in the managed output directory is ever
// touched. A deletion is refused with ErrSourceRootBusy while a scan of the root
// is active.
func (s *SourceRoots) Delete(ctx context.Context, id uuid.UUID, confirmedPath string, confirmedLocations int64) error {
	current, err := s.repository.GetSourceRoot(ctx, id)
	if err != nil {
		return fmt.Errorf("read source root: %w", err)
	}
	if confirmedPath != current.ConfiguredPath {
		return fmt.Errorf("delete source root: confirmed path %q is not the configured path %q: %w",
			confirmedPath, current.ConfiguredPath, ErrSourceRootConfirmation)
	}
	count, err := s.repository.CountSourceLocations(ctx, id)
	if err != nil {
		return fmt.Errorf("count source locations: %w", err)
	}
	if count != confirmedLocations {
		return fmt.Errorf("delete source root: confirmed %d locations but the inventory holds %d: %w",
			confirmedLocations, count, ErrSourceRootConfirmation)
	}
	if err := s.repository.DeleteSourceRoot(ctx, id, confirmedPath, confirmedLocations); err != nil {
		if errors.Is(err, persistence.ErrSourceRootActiveScan) {
			return fmt.Errorf("delete source root: %w", ErrSourceRootBusy)
		}
		if errors.Is(err, persistence.ErrSourceRootConfirmation) {
			return fmt.Errorf("delete source root: %w", ErrSourceRootConfirmation)
		}
		return fmt.Errorf("delete source root: %w", err)
	}
	return nil
}

// ValidateSourcePath normalizes a configured source path and verifies that it is
// an existing readable server directory which overlaps neither the managed tools
// root nor the managed output root, and that no other registered root already
// carries it. excludeRootID skips one root, which is the root itself when an edit
// or a scan start re-validates its own path. The directory is never created, and
// neither a write probe nor any other file is ever put inside it.
func (s *SourceRoots) ValidateSourcePath(ctx context.Context, configuredPath string, excludeRootID *uuid.UUID) (string, error) {
	if err := sourcefs.ValidateRootPathSupport(configuredPath); err != nil {
		return "", err
	}
	path, err := settings.NormalizePath(configuredPath)
	if err != nil {
		return "", fmt.Errorf("source directory: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("source directory must exist and be readable: %w: %w", ErrSourceRootInaccessible, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("source directory is not a directory: %w", ErrSourceRootInaccessible)
	}
	directory, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("source directory is not readable: %w: %w", ErrSourceRootInaccessible, err)
	}
	defer func() { _ = directory.Close() }()
	if _, err := directory.ReadDir(1); err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("source directory is not readable: %w: %w", ErrSourceRootInaccessible, err)
	}
	if err := s.checkManagedOverlap(ctx, path); err != nil {
		return "", err
	}
	roots, err := s.repository.ListSourceRoots(ctx)
	if err != nil {
		return "", fmt.Errorf("list source roots: %w", err)
	}
	for index := range roots {
		if excludeRootID != nil && roots[index].ID == *excludeRootID {
			continue
		}
		if roots[index].ConfiguredPath == path {
			return "", fmt.Errorf("a source root with the configured path %q already exists", path)
		}
	}
	return path, nil
}

// checkManagedOverlap refuses a source path that intersects the managed tools or
// output root in either direction. Both sides are normalized, so a symlink into
// a managed root is caught by the path it really points at. Two source roots may
// overlap each other: only an exact duplicate is refused.
func (s *SourceRoots) checkManagedOverlap(ctx context.Context, path string) error {
	toolsRoot, hasTools, err := s.managedPaths.GetToolsDirectory(ctx)
	if err != nil {
		return fmt.Errorf("read managed tools directory: %w", err)
	}
	if hasTools && toolsRoot != "" {
		overlap, err := settings.SourcePathsOverlap(path, toolsRoot)
		if err != nil {
			return fmt.Errorf("check managed tools directory overlap: %w", err)
		}
		if overlap {
			return fmt.Errorf("source directory overlaps the managed tools directory")
		}
	}
	outputRoot, hasOutput, err := s.managedPaths.GetOutputDirectory(ctx)
	if err != nil {
		return fmt.Errorf("read managed output directory: %w", err)
	}
	if hasOutput && outputRoot != "" {
		overlap, err := settings.SourcePathsOverlap(path, outputRoot)
		if err != nil {
			return fmt.Errorf("check managed output directory overlap: %w", err)
		}
		if overlap {
			return fmt.Errorf("source directory overlaps the managed output directory")
		}
	}
	return nil
}

func (s *SourceRoots) view(ctx context.Context, root *persistence.SourceRoot) (SourceRoot, error) {
	count, err := s.repository.CountSourceLocations(ctx, root.ID)
	if err != nil {
		return SourceRoot{}, fmt.Errorf("count source locations: %w", err)
	}
	return SourceRoot{
		ID: root.ID, DisplayName: root.DisplayName, ConfiguredPath: root.ConfiguredPath,
		Enabled: root.Enabled, Status: root.Status, SafeError: root.SafeError,
		InventoryPath: root.InventoryPath, Stale: root.Stale(),
		ScanGeneration: root.ScanGeneration, LastSuccessfulScanAt: root.LastSuccessfulScanAt,
		LocationCount: count, CreatedAt: root.CreatedAt, UpdatedAt: root.UpdatedAt,
	}, nil
}

func sourceRootDisplayName(displayName string) (string, error) {
	name := strings.TrimSpace(displayName)
	if name == "" {
		return "", fmt.Errorf("source root display name must not be empty")
	}
	return name, nil
}
