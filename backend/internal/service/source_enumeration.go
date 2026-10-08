package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type SourceEnumerationScope = persistence.SourceEnumerationScope

// EnumerateSourceTree enumerates approved files and records unreadable scopes.
// Cancellation, namespace changes and visitor failures abort rather than yielding
// a partial result that could be mistaken for an authoritative inventory.
func EnumerateSourceTree(ctx context.Context, rootPath string, visit SourceWalkVisitor) ([]SourceEnumerationScope, error) {
	return enumerateSourceTree(ctx, sourcefs.NewOpener(), rootPath, visit)
}

func enumerateSourceTree(ctx context.Context, opener sourcefs.Opener, rootPath string, visit SourceWalkVisitor) ([]SourceEnumerationScope, error) {
	if !filepath.IsAbs(rootPath) {
		return nil, fmt.Errorf("enumerate source tree: root %q is not an absolute path", rootPath)
	}
	rootPath = filepath.Clean(rootPath)
	if err := sourcefs.ValidateRootPathSupport(rootPath); err != nil {
		return nil, fmt.Errorf("enumerate source tree: %w", err)
	}
	root, err := opener.OpenRoot(ctx, rootPath)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("open source root: %w", ctx.Err())
		}
		return []SourceEnumerationScope{{Kind: "root"}}, nil
	}
	defer func() { _ = root.Close() }()

	scopes := make([]SourceEnumerationScope, 0)
	var walk func(sourcefs.Directory, string, bool) error
	walk = func(directory sourcefs.Directory, relative string, isRoot bool) error {
		if !isRoot {
			defer func() { _ = directory.Close() }()
		}
		for {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("enumerate source tree: %w", err)
			}
			entries, readErr := directory.ReadDir(ctx, sourceWalkReadBatch)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return fmt.Errorf("enumerate source tree: %w", ctxErr)
			}
			if readErr != nil && (!errors.Is(readErr, io.EOF) || len(entries) == 0) {
				if errors.Is(readErr, io.EOF) && len(entries) == 0 {
					return nil
				}
				kind := "subtree"
				if isRoot {
					kind = "root"
				}
				scopes = append(scopes, SourceEnumerationScope{RelativePath: relative, Kind: kind})
				return nil
			}
			readFailed := readErr != nil && !errors.Is(readErr, io.EOF)
			if readFailed {
				kind := "subtree"
				if isRoot {
					kind = "root"
				}
				scopes = append(scopes, SourceEnumerationScope{RelativePath: relative, Kind: kind})
			}
			for _, item := range entries {
				if err := ctx.Err(); err != nil {
					return fmt.Errorf("enumerate source tree: %w", err)
				}
				child := item.Name
				if relative != "" {
					child = filepath.Join(relative, item.Name)
				}
				if item.Kind == sourcefs.KindExcluded {
					continue
				}
				if item.Kind == sourcefs.KindDir || item.Kind == sourcefs.KindUnknown {
					childDir, openErr := directory.OpenDir(ctx, item.Name)
					if openErr == nil {
						if item.Kind == sourcefs.KindUnknown {
							info, statErr := childDir.Stat(ctx)
							if statErr != nil {
								_ = childDir.Close()
								if ctx.Err() != nil {
									return fmt.Errorf("enumerate source tree: %w", ctx.Err())
								}
								scopes = append(scopes, SourceEnumerationScope{RelativePath: child, Kind: "subtree"})
								continue
							}
							if !info.IsDir() {
								_ = childDir.Close()
								continue
							}
						}
						if err := walk(childDir, child, false); err != nil {
							return err
						}
						continue
					}
					if errors.Is(openErr, sourcefs.ErrLink) {
						continue
					}
					if errors.Is(openErr, sourcefs.ErrNotDirectory) || errors.Is(openErr, sourcefs.ErrNotRegular) {
						if item.Kind == sourcefs.KindDir {
							continue
						}
						// An unknown entry may be a regular file. Fall through to the
						// approved-extension/open-regular path below.
					} else {
						if ctx.Err() != nil {
							return fmt.Errorf("enumerate source tree: %w", ctx.Err())
						}
						scopes = append(scopes, SourceEnumerationScope{RelativePath: child, Kind: "subtree"})
						continue
					}
				}
				if _, approved := sourceWalkExtensions[strings.ToLower(filepath.Ext(item.Name))]; !approved {
					continue
				}
				file, openErr := directory.OpenRegular(ctx, item.Name)
				if openErr != nil {
					if errors.Is(openErr, sourcefs.ErrLink) || errors.Is(openErr, sourcefs.ErrNotRegular) {
						continue
					}
					if ctx.Err() != nil {
						return fmt.Errorf("enumerate source tree: %w", ctx.Err())
					}
					scopes = append(scopes, SourceEnumerationScope{RelativePath: child, Kind: "file"})
					continue
				}
				info, statErr := file.Stat(ctx)
				closeErr := file.Close()
				if ctx.Err() != nil {
					return fmt.Errorf("enumerate source tree: %w", ctx.Err())
				}
				if statErr != nil {
					scopes = append(scopes, SourceEnumerationScope{RelativePath: child, Kind: "file"})
					continue
				}
				if closeErr != nil {
					return fmt.Errorf("close source file %q: %w", child, closeErr)
				}
				if !info.Mode().IsRegular() {
					continue
				}
				if err := visit(SourceWalkEntry{RelativePath: child, SizeBytes: info.Size(), Mtime: info.ModTime()}); err != nil {
					return fmt.Errorf("visit source file %q: %w", child, err)
				}
				if err := ctx.Err(); err != nil {
					return fmt.Errorf("enumerate source tree: %w", err)
				}
			}
			if readFailed || len(entries) < sourceWalkReadBatch {
				return nil
			}
		}
	}
	if err := walk(root, "", true); err != nil {
		return nil, err
	}
	if err := sourceScanConfirmRootNamespace(ctx, rootPath, root); err != nil {
		return nil, fmt.Errorf("confirm source root namespace: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("enumerate source tree: %w", err)
	}
	for i := range scopes {
		scopes[i].RelativePath = filepath.ToSlash(scopes[i].RelativePath)
	}
	return scopes, nil
}

// validEnumerationScope prevents a caller from broadening a reconciliation scope
// outside the pinned source namespace.
func validEnumerationScope(scope SourceEnumerationScope) bool {
	if scope.Kind != "root" && scope.Kind != "subtree" && scope.Kind != "file" {
		return false
	}
	if scope.Kind == "root" {
		return scope.RelativePath == ""
	}
	clean := filepath.Clean(scope.RelativePath)
	return clean != "." && !filepath.IsAbs(clean) && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator)) && !strings.Contains(scope.RelativePath, "\\") && fs.ValidPath(filepath.ToSlash(clean))
}
