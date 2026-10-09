package settings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type ResetDirectoryRecord struct {
	Path     string `json:"path"`
	Phase    string `json:"phase"`
	Identity string `json:"identity,omitempty"`
}

// ResetFilesystem creates output directories and verifies durable OS identities
// before removing anything during recovery.
type ResetFilesystem struct {
	mu      sync.Mutex
	created map[string]os.FileInfo
}

// PlannedDirectories returns only missing directories that preparation may
// create. It walks upward to the closest existing ancestor without adopting it.
func (f *ResetFilesystem) PlannedDirectories(root string, directories []string) ([]string, error) {
	planned := make(map[string]struct{})
	for _, requested := range directories {
		if filepath.Clean(requested) != requested || !filepath.IsAbs(requested) || (requested != root && !isResetDescendant(root, requested)) {
			return nil, fmt.Errorf("logical output directory must be a normalized descendant of the output directory")
		}
		for path := requested; ; path = filepath.Dir(path) {
			info, err := os.Lstat(path)
			if err == nil {
				if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					return nil, fmt.Errorf("logical output path is not a directory")
				}
				if _, err := durableDirectoryIdentity(path, info); err != nil {
					return nil, fmt.Errorf("filesystem does not support durable output directory identities: %w", err)
				}
				break
			}
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("inspect logical output directory: %w", err)
			}
			if path == filepath.Dir(path) {
				return nil, fmt.Errorf("cannot create output filesystem root")
			}
			planned[path] = struct{}{}
		}
	}
	result := make([]string, 0, len(planned))
	for path := range planned {
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

func NewResetFilesystem() *ResetFilesystem {
	return &ResetFilesystem{created: make(map[string]os.FileInfo)}
}

// Prepare is retained for callers using the original path-only journal API.
func (f *ResetFilesystem) Prepare(ctx context.Context, root string, directories []string, record func(string) error) error {
	return f.prepare(ctx, root, directories, func(entry ResetDirectoryRecord) error {
		if entry.Phase == "intent" {
			return record(entry.Path)
		}
		return nil
	})
}

// PrepareDurable persists each intent before mkdir and the verified identity
// immediately afterward. Missing root ancestors are created one at a time; an
// existing ancestor is only inspected and is never adopted as owned.
func (f *ResetFilesystem) PrepareDurable(ctx context.Context, root string, directories []string, record func(ResetDirectoryRecord) error) error {
	return f.prepare(ctx, root, directories, record)
}

func (f *ResetFilesystem) prepare(ctx context.Context, root string, directories []string, record func(ResetDirectoryRecord) error) error {
	normalized, err := NormalizePath(root)
	if err != nil || normalized != root || !filepath.IsAbs(root) || record == nil {
		return fmt.Errorf("output directory and journal callback are required")
	}
	if _, err := f.PlannedDirectories(root, directories); err != nil {
		return err
	}
	for _, requested := range directories {
		if err := ctx.Err(); err != nil {
			return err
		}
		if filepath.Clean(requested) != requested || !filepath.IsAbs(requested) || (requested != root && !isResetDescendant(root, requested)) {
			return fmt.Errorf("logical output directory must be a normalized descendant of the output directory")
		}
		if err := f.createMissing(ctx, requested, record); err != nil {
			return err
		}
	}
	return nil
}

func (f *ResetFilesystem) createMissing(ctx context.Context, path string, record func(ResetDirectoryRecord) error) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("logical output path is not a directory")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("inspect logical output directory: %w", err)
	}
	parent := filepath.Dir(path)
	if parent == path {
		return fmt.Errorf("cannot create output filesystem root")
	}
	if err := f.createMissing(ctx, parent, record); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record(ResetDirectoryRecord{Path: path, Phase: "intent"}); err != nil {
		return fmt.Errorf("record logical output directory intent: %w", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		return fmt.Errorf("create logical output directory: %w", err)
	}
	info, err = os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("verify created logical output directory: %w", err)
	}
	identity, err := durableDirectoryIdentity(path, info)
	if err != nil {
		return fmt.Errorf("read created output directory identity: %w", err)
	}
	if err := record(ResetDirectoryRecord{Path: path, Phase: "created", Identity: identity}); err != nil {
		return fmt.Errorf("record created output directory identity: %w", err)
	}
	f.mu.Lock()
	f.created[path] = info
	f.mu.Unlock()
	return nil
}

// Rollback keeps the legacy process-local contract.
func (f *ResetFilesystem) Rollback(ctx context.Context, paths []string) error {
	var records []ResetDirectoryRecord
	for _, path := range paths {
		f.mu.Lock()
		info := f.created[path]
		f.mu.Unlock()
		if info != nil {
			identity, err := durableDirectoryIdentity(path, info)
			if err == nil {
				records = append(records, ResetDirectoryRecord{Path: path, Phase: "created", Identity: identity})
			}
		} else {
			records = append(records, ResetDirectoryRecord{Path: path, Phase: "intent"})
		}
	}
	return f.RollbackManifest(ctx, records)
}

// RollbackManifest removes only directories with a durable created identity,
// matching current identity, and no contents. An intent with no matching created
// record is ambiguous because the process may have crashed between mkdir and
// recording the result.
func (f *ResetFilesystem) RollbackManifest(ctx context.Context, records []ResetDirectoryRecord) error {
	var result error
	created := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record.Phase == "created" && record.Identity != "" {
			created[record.Path] = struct{}{}
		}
	}
	for i := len(records) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		r := records[i]
		if r.Phase == "intent" {
			if _, ok := created[r.Path]; ok {
				continue
			}
		}
		if r.Phase != "created" || r.Identity == "" {
			result = errors.Join(result, fmt.Errorf("cannot prove ownership of output directory %q", r.Path))
			continue
		}
		current, err := os.Lstat(r.Path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("inspect owned output directory: %w", err))
			continue
		}
		if !current.IsDir() || current.Mode()&os.ModeSymlink != 0 {
			result = errors.Join(result, fmt.Errorf("output directory ownership changed for %q", r.Path))
			continue
		}
		identity, err := durableDirectoryIdentity(r.Path, current)
		if err != nil || identity != r.Identity {
			result = errors.Join(result, fmt.Errorf("output directory ownership changed for %q", r.Path))
			continue
		}
		// Recheck immediately before removal. This narrows the rename/swap
		// window but is not an atomic defense against hostile concurrent actors.
		latest, err := os.Lstat(r.Path)
		if err != nil || !latest.IsDir() || latest.Mode()&os.ModeSymlink != 0 {
			result = errors.Join(result, fmt.Errorf("output directory ownership changed for %q", r.Path))
			continue
		}
		latestIdentity, err := durableDirectoryIdentity(r.Path, latest)
		if err != nil || latestIdentity != r.Identity {
			result = errors.Join(result, fmt.Errorf("output directory ownership changed for %q", r.Path))
			continue
		}
		entries, err := os.ReadDir(r.Path)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("inspect owned output directory: %w", err))
			continue
		}
		if len(entries) != 0 {
			result = errors.Join(result, fmt.Errorf("owned output directory is not empty: %q", r.Path))
			continue
		}
		if err := os.Remove(r.Path); err != nil {
			result = errors.Join(result, fmt.Errorf("remove owned output directory: %w", err))
			continue
		}
	}
	return result
}

// RollbackManifestForRoot rejects records outside the new root and the bounded,
// contiguous chain of missing ancestors between that root and its closest
// pre-existing parent. It does not claim atomic protection from hostile renames.
func (f *ResetFilesystem) RollbackManifestForRoot(ctx context.Context, root string, records []ResetDirectoryRecord) error {
	if filepath.Clean(root) != root || !filepath.IsAbs(root) {
		return fmt.Errorf("output reset root is not normalized")
	}
	ancestorRecords := make(map[string]struct{})
	for _, record := range records {
		path := record.Path
		if filepath.Clean(path) != path || !filepath.IsAbs(path) {
			return fmt.Errorf("output reset directory manifest contains a non-normalized path")
		}
		if path != root && !isResetDescendant(root, path) {
			if !isResetDescendant(path, root) {
				return fmt.Errorf("output reset directory manifest escapes the new output root")
			}
			ancestorRecords[path] = struct{}{}
		}
	}
	if len(ancestorRecords) > 64 {
		return fmt.Errorf("output reset directory manifest has an oversized ancestor chain")
	}
	for path := filepath.Dir(root); ; path = filepath.Dir(path) {
		if _, ok := ancestorRecords[path]; !ok {
			break
		}
		delete(ancestorRecords, path)
		if path == filepath.Dir(path) {
			break
		}
	}
	if len(ancestorRecords) != 0 {
		return fmt.Errorf("output reset directory manifest has an invalid ancestor chain")
	}
	return f.RollbackManifest(ctx, records)
}

func isResetDescendant(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
