package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// planFirstPublicationNamespace plans against the caller's complete snapshot of
// namespace claims without creating publication directories in the output root.
func planFirstPublicationNamespace(ctx context.Context, outputRoot string, positionID uuid.UUID, candidates []publication.NamespaceCandidate, claims []persistence.PublicationNamespaceClaim) (_ []persistence.PublicationNamespaceNode, resultErr error) {
	root, err := sourcefs.NewOutputOpener().OpenRoot(ctx, outputRoot)
	if err != nil {
		return nil, fmt.Errorf("open publication output root: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	if err := root.Verify(ctx); err != nil {
		return nil, fmt.Errorf("verify publication output root: %w", err)
	}

	// Keep every opened parent pinned until all comparisons and final verification
	// complete. Missing path components are intentionally not created.
	directories := map[string]sourcefs.OutputDirectory{"": root}
	identities := map[string]sourcefs.DirectoryIdentity{}
	if id, err := root.Identity(ctx); err != nil {
		return nil, err
	} else {
		identities[""] = id
	}
	defer func() {
		for relative, directory := range directories {
			if relative != "" {
				resultErr = errors.Join(resultErr, directory.Close())
			}
		}
	}()

	var openParent func(string) (sourcefs.OutputDirectory, error)
	openParent = func(relative string) (sourcefs.OutputDirectory, error) {
		if directory := directories[relative]; directory != nil {
			return directory, nil
		}
		parent := path.Dir(relative)
		if parent == "." {
			parent = ""
		}
		base := path.Base(relative)
		parentDir, err := openParent(parent)
		if err != nil {
			return nil, err
		}
		traversal, ok := parentDir.(sourcefs.OutputDirectoryTraversal)
		if !ok {
			return nil, sourcefs.ErrUnsupported
		}
		child, err := traversal.OpenExistingDir(ctx, base)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if err != nil {
			return nil, err
		}
		id, err := child.Identity(ctx)
		if err != nil {
			return nil, errors.Join(err, child.Close())
		}
		directories[relative], identities[relative] = child, id
		return child, nil
	}

	compare := func(ctx context.Context, parentRelative, a, b string) (bool, error) {
		parent, err := openParent(parentRelative)
		if errors.Is(err, fs.ErrNotExist) {
			// A missing directory inherits the semantics of the nearest existing
			// parent when it is created beneath that pinned directory.
			for p := parentRelative; p != ""; p = path.Dir(p) {
				if p == "." {
					p = ""
				}
				parent, err = openParent(p)
				if err == nil {
					break
				}
				if !errors.Is(err, fs.ErrNotExist) {
					return false, err
				}
			}
		}
		if err != nil {
			return false, err
		}
		return sourcefs.NativeCompareComponents(ctx, parent, a, b)
	}

	// Probe every supplied candidate component against itself so unrepresentable
	// native names are rejected even when there are no conflicting siblings.
	for _, candidate := range candidates {
		if candidate.PositionID != positionID {
			return nil, errors.New("plan first publication namespace: candidate position mismatch")
		}
		parts := strings.Split(candidate.RelativePath, "/")
		for i, component := range parts {
			parent := strings.Join(parts[:i], "/")
			if _, err := compare(ctx, parent, component, component); err != nil {
				return nil, fmt.Errorf("probe publication namespace component %q: %w", component, err)
			}
		}
	}

	nativeClaims := make([]publication.NamespaceClaim, 0, len(claims))
	for _, claim := range claims {
		kind := claim.NodeKind
		if kind == "dir" {
			kind = "directory"
		}
		scope := ""
		if claim.DirectoryScopeKey != nil {
			scope = *claim.DirectoryScopeKey
		}
		nativeClaims = append(nativeClaims, publication.NamespaceClaim{CanonicalKey: claim.CanonicalKey, RelativePath: claim.RelativePath, NodeKind: kind, DirectoryScope: scope, OwnerPositionID: claim.OwnerPositionID})
	}
	planned, err := publication.PlanNativeNamespace(ctx, candidates, nativeClaims, compare)
	if err != nil {
		return nil, err
	}
	for relative, id := range identities {
		if relative == "" {
			continue
		}
		parent := path.Dir(relative)
		if parent == "." {
			parent = ""
		}
		if err := directories[parent].VerifyChild(ctx, path.Base(relative), id); err != nil {
			return nil, fmt.Errorf("verify publication namespace parent %q: %w", relative, err)
		}
	}
	if err := root.Verify(ctx); err != nil {
		return nil, fmt.Errorf("verify publication output root: %w", err)
	}
	result := make([]persistence.PublicationNamespaceNode, len(planned))
	for i, node := range planned {
		kind := node.NodeKind
		if kind == "directory" {
			kind = "dir"
		}
		result[i] = persistence.PublicationNamespaceNode{CanonicalKey: node.CanonicalKey, RelativePath: node.RelativePath, NodeKind: kind, DirectoryScope: node.DirectoryScope}
	}
	return result, nil
}
