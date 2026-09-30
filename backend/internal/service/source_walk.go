package service

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sourceWalkExtensions holds the 13 audio extensions a source inventory
// accepts. The walker compares an entry's extension against this set without
// case folding, while every stored path keeps the exact case of the file on
// disk.
var sourceWalkExtensions = map[string]struct{}{
	".flac": {}, ".wav": {}, ".aif": {}, ".aiff": {}, ".ape": {}, ".wv": {},
	".mp3": {}, ".m4a": {}, ".aac": {}, ".ogg": {}, ".opus": {}, ".wma": {}, ".mka": {},
}

// SourceWalkEntry is one approved regular file found below a source root. The
// absolute path is what a managed ffprobe is later executed against, and the
// relative path, size and mtime come from the file's own stat, never from a
// case-folded or otherwise rewritten name.
type SourceWalkEntry struct {
	AbsolutePath string
	RelativePath string
	SizeBytes    int64
	Mtime        time.Time
}

// SourceWalkVisitor receives every approved file once, in the order the walk
// finds it. Returning an error stops the walk and is returned to the caller.
type SourceWalkVisitor func(SourceWalkEntry) error

// WalkSourceTree walks the directory at the absolute rootPath and hands every
// approved regular file to visit. Only regular files whose extension is one of
// the 13 approved audio extensions are visited, and the comparison ignores the
// case of the extension only. Symlink entries, whether they point at a file or
// at a directory, are skipped without being stat'd, so the walk can neither
// leave the root nor loop through a link. No file is created, written, opened
// for writing or probed inside the tree.
//
// A nil error means the whole tree was read and every entry was delivered. An
// unreadable subtree, a canceled context or a visitor error fails the walk as a
// whole: the returned error makes the entries already delivered unusable, so
// the caller must discard them instead of treating the run as a partial success.
func WalkSourceTree(ctx context.Context, rootPath string, visit SourceWalkVisitor) error {
	if !filepath.IsAbs(rootPath) {
		return fmt.Errorf("walk source tree: root %q is not an absolute path", rootPath)
	}
	return walkSourceDirectory(ctx, filepath.Clean(rootPath), "", visit)
}

// walkSourceDirectory reads one directory below root and recurses into its
// subdirectories. relative is the exact path of the directory below root, and
// is empty for the root itself.
func walkSourceDirectory(ctx context.Context, root, relative string, visit SourceWalkVisitor) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("walk source tree: %w", err)
	}
	directory := root
	if relative != "" {
		directory = filepath.Join(root, relative)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read source directory %q: %w", directory, err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("walk source tree: %w", err)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("read source entry %q: %w", filepath.Join(relative, entry.Name()), err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		child := filepath.Join(relative, entry.Name())
		if info.IsDir() {
			if err := walkSourceDirectory(ctx, root, child, visit); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if _, approved := sourceWalkExtensions[strings.ToLower(filepath.Ext(entry.Name()))]; !approved {
			continue
		}
		err = visit(SourceWalkEntry{
			AbsolutePath: filepath.Join(root, child),
			RelativePath: child,
			SizeBytes:    info.Size(),
			Mtime:        info.ModTime(),
		})
		if err != nil {
			return fmt.Errorf("visit source file %q: %w", child, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("walk source tree: %w", err)
	}
	return nil
}
