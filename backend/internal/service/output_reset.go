package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type outputResetRepository interface {
	RunOutputReset(
		context.Context,
		string,
		string,
		func(context.Context, func(string) error) error,
		func(context.Context, persistence.OutputResetJournal) error,
	) (uuid.UUID, error)
	RecoverOutputReset(context.Context, func(context.Context, persistence.OutputResetJournal) error) error
}

type durableOutputResetRepository interface {
	RunOutputResetRequest(context.Context, persistence.OutputResetRequest) (uuid.UUID, error)
}

// OutputReset coordinates filesystem preparation with the persistence journal.
// The supplied logical directory names are passed explicitly so adding or
// changing output areas remains a product-level choice rather than an implicit
// service default.
type OutputReset struct {
	repository   outputResetRepository
	filesystem   *settings.ResetFilesystem
	logicalAreas []string
	registry     *settings.Registry
}

func NewOutputReset(repository outputResetRepository, filesystem *settings.ResetFilesystem, logicalAreas []string) *OutputReset {
	if filesystem == nil {
		filesystem = settings.NewResetFilesystem()
	}
	return &OutputReset{repository: repository, filesystem: filesystem, logicalAreas: append([]string(nil), logicalAreas...)}
}

// Reset changes the output root through the durable reset coordinator. Candidate
// normalization is intentionally non-mutating; all filesystem creation starts
// only after the repository has persisted its preparing journal.
func (s *OutputReset) Reset(ctx context.Context, expectedCurrent, candidate string) (uuid.UUID, error) {
	if s.repository == nil {
		return uuid.Nil, fmt.Errorf("output reset repository is required")
	}
	oldRoot := expectedCurrent
	if expectedCurrent != "" {
		normalizedOldRoot, err := settings.NormalizePath(expectedCurrent)
		if err != nil {
			return uuid.Nil, fmt.Errorf("normalize current output directory: %w", err)
		}
		oldRoot = normalizedOldRoot
	}
	newRoot, err := settings.NormalizePath(candidate)
	if err != nil {
		return uuid.Nil, fmt.Errorf("normalize candidate output directory: %w", err)
	}
	if oldRoot != expectedCurrent || newRoot != candidate {
		return uuid.Nil, fmt.Errorf("output directories must be normalized paths")
	}
	if oldRoot == newRoot {
		return uuid.Nil, nil
	}
	directories := append([]string{newRoot}, s.areaPaths(newRoot)...)
	plannedDirectories, err := s.filesystem.PlannedDirectories(newRoot, directories)
	if err != nil {
		return uuid.Nil, fmt.Errorf("preflight output directory creation: %w", err)
	}
	prepareDurable := func(ctx context.Context, record func(persistence.OutputResetDirectory) error) error {
		return s.filesystem.PrepareDurable(ctx, newRoot, directories, func(entry settings.ResetDirectoryRecord) error {
			return record(persistence.OutputResetDirectory{Path: entry.Path, Phase: entry.Phase, Identity: entry.Identity})
		})
	}
	finish := func(context.Context, persistence.OutputResetJournal) error {
		return nil
	}
	var token uuid.UUID
	if repository, ok := s.repository.(durableOutputResetRepository); ok {
		token, err = repository.RunOutputResetRequest(ctx, persistence.OutputResetRequest{ExpectedOldRoot: oldRoot, NewRoot: newRoot, AllowedDirectories: plannedDirectories, Prepare: prepareDurable, Finish: finish})
	} else {
		token, err = s.repository.RunOutputReset(ctx, oldRoot, newRoot, func(ctx context.Context, record func(string) error) error {
			return s.filesystem.Prepare(ctx, newRoot, directories, record)
		}, finish)
	}
	if err != nil {
		if recoveryErr := s.RecoverOutputReset(ctx); recoveryErr != nil {
			return token, fmt.Errorf("output reset failed: %w; recovery failed: %v", err, recoveryErr)
		}
		return token, err
	}
	return token, nil
}

// ResetRuntime atomically changes the output root and all supplied runtime
// values through the durable output journal. Candidate creation and filesystem
// semantics probing happen only after the preparing journal is persisted.
func (s *OutputReset) ResetRuntime(ctx context.Context, expectedTools, expectedOutput, candidate string, update settings.RuntimeUpdate) (uuid.UUID, error) {
	if s.repository == nil {
		return uuid.Nil, fmt.Errorf("output reset repository is required")
	}
	newRoot, err := settings.NormalizePath(candidate)
	if err != nil {
		return uuid.Nil, fmt.Errorf("normalize candidate output directory: %w", err)
	}
	if newRoot != candidate {
		return uuid.Nil, fmt.Errorf("output directory must be a normalized path")
	}
	if expectedOutput == newRoot {
		// A same-root save is a semantics refresh, not a reset. Existing output is
		// intentionally allowed to be populated.
		semantics, err := settings.ProbeOutputDirectory(newRoot, false)
		if err != nil {
			return uuid.Nil, fmt.Errorf("output directory semantics: %w", err)
		}
		update.OutputCaseSensitive = &semantics.CaseSensitive
		update.OutputUnicodeNormalization = &semantics.UnicodeNormalization
		update.ExpectedToolsDirectory = &expectedTools
		update.ExpectedOutputDirectory = &expectedOutput
		return uuid.Nil, s.registry.UpdateRuntime(ctx, update)
	}
	update.OutputDirectory = &newRoot
	update.ExpectedToolsDirectory, update.ExpectedOutputDirectory = &expectedTools, &expectedOutput
	var token uuid.UUID
	err = s.registryCoordinate(ctx, expectedTools, expectedOutput, update, func(runtime map[string]string) error {
		directories := append([]string{newRoot}, s.areaPaths(newRoot)...)
		planned, err := s.filesystem.PlannedDirectories(newRoot, directories)
		if err != nil {
			return fmt.Errorf("preflight output directory creation: %w", err)
		}
		var semantics settings.FilesystemSemantics
		prepare := func(ctx context.Context, record func(persistence.OutputResetDirectory) error) error {
			if err := s.filesystem.PrepareDurable(ctx, newRoot, directories, func(entry settings.ResetDirectoryRecord) error {
				return record(persistence.OutputResetDirectory{Path: entry.Path, Phase: entry.Phase, Identity: entry.Identity})
			}); err != nil {
				return err
			}
			semantics, err = settings.ProbeOutputDirectory(newRoot, false)
			return err
		}
		finish := func(context.Context, persistence.OutputResetJournal) error { return nil }
		runtime[settings.OutputDirectoryKey] = newRoot
		request := persistence.OutputResetRequest{
			ExpectedToolsRoot: expectedTools, ExpectedOldRoot: expectedOutput, NewRoot: newRoot,
			AllowedDirectories: planned, RuntimeValues: runtime, Prepare: prepare, Finish: finish,
			RuntimeValuesAfterPrepare: func(context.Context) (map[string]string, error) {
				return map[string]string{
					settings.OutputCaseSensitiveKey:        strconv.FormatBool(semantics.CaseSensitive),
					settings.OutputUnicodeNormalizationKey: semantics.UnicodeNormalization,
				}, nil
			},
		}
		token, err = s.runRuntimeOutputReset(ctx, request)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if recoveryErr := s.RecoverOutputReset(ctx); recoveryErr != nil {
			return token, fmt.Errorf("output reset failed: %w; recovery failed: %v", err, recoveryErr)
		}
		return token, err
	}
	return token, nil
}

// These narrow indirections keep settings' serialization lock around the whole
// durable reset without exposing persistence details to the settings package.
var coordinateRuntimeReset = func(ctx context.Context, registry *settings.Registry, tools, output string, update settings.RuntimeUpdate, run func(map[string]string) error) error {
	return registry.CoordinateRuntimeReset(ctx, tools, output, update, func(_, _ string, values map[string]string) error { return run(values) })
}

func (s *OutputReset) registryCoordinate(ctx context.Context, tools, output string, update settings.RuntimeUpdate, run func(map[string]string) error) error {
	return coordinateRuntimeReset(ctx, s.registry, tools, output, update, run)
}

func (s *OutputReset) runRuntimeOutputReset(ctx context.Context, request persistence.OutputResetRequest) (uuid.UUID, error) {
	repository, ok := s.repository.(durableOutputResetRepository)
	if !ok {
		return uuid.Nil, fmt.Errorf("durable output reset repository is required")
	}
	return repository.RunOutputResetRequest(ctx, request)
}

// RecoverOutputReset resolves the filesystem half of the durable reset. A
// committed journal is finalized without touching any path. A preparing journal
// can only be rolled back when this process still has the creation identities;
// otherwise recovery fails closed for operator action.
func (s *OutputReset) RecoverOutputReset(ctx context.Context) error {
	if s.repository == nil {
		return fmt.Errorf("output reset repository is required")
	}
	return s.repository.RecoverOutputReset(ctx, func(ctx context.Context, journal persistence.OutputResetJournal) error {
		if journal.State == "committed" {
			return nil
		}
		var manifest []settings.ResetDirectoryRecord
		if len(journal.DirectoryManifest) == 0 || json.Unmarshal(journal.DirectoryManifest, &manifest) != nil {
			return fmt.Errorf("output reset directory manifest is invalid")
		}
		return s.filesystem.RollbackManifestForRoot(ctx, journal.NewRoot, manifest)
	})
}

func (s *OutputReset) areaPaths(root string) []string {
	paths := make([]string, 0, len(s.logicalAreas))
	for _, area := range s.logicalAreas {
		paths = append(paths, filepath.Join(root, area))
	}
	return paths
}
