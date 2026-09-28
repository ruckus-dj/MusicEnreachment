package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NormalizePath canonicalizes an existing or prospective local path before it
// is persisted or compared. Symlinks are resolved only for existing ancestors.
func NormalizePath(value string) (string, error) {
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("path must be absolute")
	}
	path := filepath.Clean(value)
	for existing := path; ; existing = filepath.Dir(existing) {
		if resolved, err := filepath.EvalSymlinks(existing); err == nil {
			relative, relativeErr := filepath.Rel(existing, path)
			if relativeErr == nil && relative != "." {
				return filepath.Join(resolved, relative), nil
			}
			return resolved, nil
		}
		if existing == filepath.Dir(existing) {
			break
		}
	}
	return path, nil
}

func PathsOverlap(first, second string) bool {
	first, err1 := NormalizePath(first)
	second, err2 := NormalizePath(second)
	if err1 != nil || err2 != nil {
		return false
	}
	return first == second || strings.HasPrefix(second, first+string(filepath.Separator)) || strings.HasPrefix(first, second+string(filepath.Separator))
}

func ProbeWritableEmpty(path string) error {
	normalized, err := NormalizePath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(normalized, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	entries, err := os.ReadDir(normalized)
	if err != nil {
		return fmt.Errorf("read directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("directory must be empty")
	}
	probe, err := os.CreateTemp(normalized, ".melotrove-write-probe-")
	if err != nil {
		return fmt.Errorf("directory is not writable: %w", err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove write probe: %w", err)
	}
	return nil
}
