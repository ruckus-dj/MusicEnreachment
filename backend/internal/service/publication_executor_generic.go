package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func openPublicationTargetParent(ctx context.Context, root sourcefs.OutputDirectory, relative string, nodes []persistence.PublicationNamespaceNode) (sourcefs.OutputDirectory, error) {
	if relative == "" || path.IsAbs(relative) || path.Clean(relative) != relative || strings.ContainsAny(relative, "\\:\x00") {
		return nil, sourcefs.ErrInvalidPath
	}
	for _, component := range strings.Split(relative, "/") {
		if component == "" || component == "." || component == ".." {
			return nil, sourcefs.ErrInvalidPath
		}
	}
	parent := path.Dir(relative)
	if parent == "." {
		return root, nil
	}
	current := root
	owned := false
	ancestors := splitPublicationPath(parent)
	for index, part := range ancestors {
		relativeAncestor := path.Join(ancestors[:index+1]...)
		approved := 0
		for _, node := range nodes {
			if node.RelativePath == relativeAncestor && node.NodeKind == "dir" && node.DirectoryScope != "" {
				approved++
			}
		}
		if approved != 1 {
			err := fmt.Errorf("publication target parent %q is not uniquely approved by the frozen namespace", relativeAncestor)
			if owned {
				err = errors.Join(err, current.Close())
			}
			return nil, err
		}
		traversal, ok := current.(sourcefs.OutputDirectoryTraversal)
		if !ok {
			err := errors.New("publication target parent traversal capability is unavailable")
			if owned {
				err = errors.Join(err, current.Close())
			}
			return nil, err
		}
		next, err := traversal.OpenExistingDir(ctx, part)
		if errors.Is(err, os.ErrNotExist) {
			next, err = current.CreateDirExclusive(ctx, part)
			// Another authorized executor may have created the namespace directory
			// after our read-only lookup. Reopen it without following links.
			if errors.Is(err, os.ErrExist) {
				next, err = traversal.OpenExistingDir(ctx, part)
			}
		}
		if err != nil {
			if owned {
				err = errors.Join(err, current.Close())
			}
			return nil, fmt.Errorf("open approved publication target parent %q: %w", part, err)
		}
		if owned {
			if err := current.Close(); err != nil {
				return nil, errors.Join(err, next.Close())
			}
		}
		current, owned = next, true
	}
	return current, nil
}

func splitPublicationPath(value string) []string {
	parts := make([]string, 0)
	for value != "" && value != "." && value != "/" {
		part, parent := path.Base(value), path.Dir(value)
		parts = append([]string{part}, parts...)
		value = parent
	}
	return parts
}
