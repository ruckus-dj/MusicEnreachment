package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// sourceAnalysisResolveFile resolves the exact relative path below a registered
// root to the absolute file a managed ffprobe reads. The path must stay inside
// the root, no component below the root may be a symlink and the final entry
// must be a regular file. The returned FileInfo is the Lstat reading of that
// final regular file; a later read compares against it with the full filesystem
// precision.
//
// The root itself is the operator's registered directory and is trusted; every
// component below it is inspected with Lstat, so a file or a directory replaced
// by a symlink after the inventory cannot be read as the inventoried file.
func sourceAnalysisResolveFile(root, relative string) (string, os.FileInfo, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", nil, fmt.Errorf("the source path %q is not a relative path below its root: %w", relative, persistence.ErrSourceAnalysisStale)
	}
	absolute := filepath.Join(root, relative)
	within, err := filepath.Rel(root, absolute)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) || filepath.IsAbs(within) {
		return "", nil, fmt.Errorf("the source path %q escapes its root: %w", relative, persistence.ErrSourceAnalysisStale)
	}
	current := root
	parts := strings.Split(within, string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return "", nil, fmt.Errorf("inspect the source path %q: %w: %w", relative, persistence.ErrSourceAnalysisStale, err)
			}
			return "", nil, fmt.Errorf("inspect the source path %q: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", nil, fmt.Errorf("the source path %q is or traverses a symbolic link: %w", relative, persistence.ErrSourceAnalysisStale)
		}
		if index == len(parts)-1 {
			if !info.Mode().IsRegular() {
				return "", nil, fmt.Errorf("the source path %q is not a regular file: %w", relative, persistence.ErrSourceAnalysisStale)
			}
			return current, info, nil
		}
		if !info.IsDir() {
			return "", nil, fmt.Errorf("the source path %q traverses a non-directory: %w", relative, persistence.ErrSourceAnalysisStale)
		}
	}
	return "", nil, fmt.Errorf("the source path %q has no file: %w", relative, persistence.ErrSourceAnalysisStale)
}
