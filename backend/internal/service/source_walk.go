package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
)

// sourceWalkExtensions holds the exact 13 accepted audio extensions. Extension
// matching is case-insensitive; relative paths retain the spelling on disk.
var sourceWalkExtensions = map[string]struct{}{
	".flac": {}, ".wav": {}, ".aif": {}, ".aiff": {}, ".ape": {}, ".wv": {},
	".mp3": {}, ".m4a": {}, ".aac": {}, ".ogg": {}, ".opus": {}, ".wma": {}, ".mka": {},
}

// SourceWalkEntry is one approved regular file. privateInfo is the identity
// witness from the safely opened descriptor; it must not escape this package.
type SourceWalkEntry struct {
	RelativePath string
	SizeBytes    int64
	Mtime        time.Time
	privateInfo  fs.FileInfo
}

type SourceWalkVisitor func(SourceWalkEntry) error
type sourceWalkFileVisitor func(SourceWalkEntry, sourcefs.RegularFile) error

const sourceWalkReadBatch = 64

// WalkSourceTree safely walks an absolute source path through one pinned root.
// File handles are loaned only for the synchronous visitor call and immediately
// closed. The walk retains at most one 64-entry directory batch per depth, so
// directory-handle usage is O(depth); file handles close immediately after each
// visitor. Scan uses walkSourceRoot to share the same pinned root with confirms.
func WalkSourceTree(ctx context.Context, rootPath string, visit SourceWalkVisitor) error {
	if !filepath.IsAbs(rootPath) {
		return fmt.Errorf("walk source tree: root %q is not an absolute path", rootPath)
	}
	root, err := sourcefs.NewOpener().OpenRoot(ctx, filepath.Clean(rootPath))
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("open source root: %w", ctx.Err())
		}
		return fmt.Errorf("open source root: %w: %w", ErrSourceRootInaccessible, err)
	}
	defer func() { _ = root.Close() }()
	return walkSourceRoot(ctx, root, func(entry SourceWalkEntry, _ sourcefs.RegularFile) error {
		return visit(entry)
	})
}

func walkSourceRoot(ctx context.Context, root sourcefs.Directory, visit sourceWalkFileVisitor) error {
	return walkSourceDirectory(ctx, root, "", true, visit)
}

func walkSourceDirectory(ctx context.Context, directory sourcefs.Directory, relative string, isRoot bool, visit sourceWalkFileVisitor) error {
	if !isRoot {
		defer func() { _ = directory.Close() }()
	}
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("walk source tree: %w", err)
		}
		entries, err := directory.ReadDir(ctx, sourceWalkReadBatch)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("walk source tree: %w", ctxErr)
		}
		if err != nil && (!errors.Is(err, io.EOF) || len(entries) == 0) {
			if errors.Is(err, io.EOF) && len(entries) == 0 {
				return nil
			}
			if isRoot && ctx.Err() == nil {
				return fmt.Errorf("read source directory %q: %w: %w", relative, ErrSourceRootInaccessible, err)
			}
			return fmt.Errorf("read source directory %q: %w", relative, err)
		}
		for _, item := range entries {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("walk source tree: %w", err)
			}
			child := item.Name
			if relative != "" {
				child = filepath.Join(relative, item.Name)
			}
			if item.Kind == sourcefs.KindExcluded {
				continue
			}
			if item.Kind == sourcefs.KindDir || item.Kind == sourcefs.KindUnknown {
				childDir, dirErr := directory.OpenDir(ctx, item.Name)
				if dirErr == nil {
					if item.Kind == sourcefs.KindUnknown {
						info, statErr := childDir.Stat(ctx)
						if statErr != nil || !info.IsDir() {
							_ = childDir.Close()
							if statErr != nil {
								return fmt.Errorf("stat source directory %q: %w", child, statErr)
							}
							return fmt.Errorf("source entry %q changed type", child)
						}
					}
					if err := walkSourceDirectory(ctx, childDir, child, false, visit); err != nil {
						return err
					}
					continue
				}
				if errors.Is(dirErr, sourcefs.ErrLink) && item.Kind == sourcefs.KindUnknown {
					continue
				}
				if errors.Is(dirErr, sourcefs.ErrLink) || item.Kind == sourcefs.KindDir {
					return fmt.Errorf("open source directory %q: %w", child, dirErr)
				}
				if !errors.Is(dirErr, sourcefs.ErrNotDirectory) && !errors.Is(dirErr, sourcefs.ErrNotRegular) {
					return fmt.Errorf("open source directory %q: %w", child, dirErr)
				}
			}
			if _, approved := sourceWalkExtensions[strings.ToLower(filepath.Ext(item.Name))]; !approved {
				continue
			}
			file, fileErr := directory.OpenRegular(ctx, item.Name)
			if fileErr != nil {
				if errors.Is(fileErr, sourcefs.ErrLink) || errors.Is(fileErr, sourcefs.ErrNotRegular) {
					// Existing link/special entries are ignored; a selected regular
					// entry replaced by one fails because it had KindRegular.
					if item.Kind == sourcefs.KindRegular {
						return fmt.Errorf("open source file %q: %w", child, fileErr)
					}
					continue
				}
				return fmt.Errorf("open source file %q: %w", child, fileErr)
			}
			info, statErr := file.Stat(ctx)
			if statErr != nil {
				_ = file.Close()
				return fmt.Errorf("stat source file %q: %w", child, statErr)
			}
			if !info.Mode().IsRegular() {
				_ = file.Close()
				return fmt.Errorf("source entry %q is not a regular file", child)
			}
			entry := SourceWalkEntry{RelativePath: child, SizeBytes: info.Size(), Mtime: info.ModTime(), privateInfo: info}
			visitErr := visit(entry, file)
			closeErr := file.Close()
			if visitErr != nil {
				return fmt.Errorf("visit source file %q: %w", child, visitErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close source file %q: %w", child, closeErr)
			}
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("walk source tree: %w", err)
			}
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("walk source tree: %w", err)
		}
		if len(entries) < sourceWalkReadBatch {
			return nil
		}
	}
}
