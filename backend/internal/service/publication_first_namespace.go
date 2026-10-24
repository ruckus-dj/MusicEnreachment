package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// planFirstPublicationNamespace plans against the caller's complete snapshot of
// namespace claims without creating publication directories in the output root.
func planFirstPublicationNamespace(ctx context.Context, outputRoot string, positionID uuid.UUID, candidates []publication.NamespaceCandidate, claims []persistence.PublicationNamespaceClaim) (_ []persistence.PublicationNamespaceNode, resultErr error) {
	return planPublicationNamespace(ctx, outputRoot, positionID, candidates, claims, false)
}

// planExactTargetPublicationNamespace reserves the requested file names exactly,
// without reusing an older filename or choosing a collision suffix.
func planExactTargetPublicationNamespace(ctx context.Context, outputRoot string, positionID uuid.UUID, candidates []publication.NamespaceCandidate, claims []persistence.PublicationNamespaceClaim) (_ []persistence.PublicationNamespaceNode, resultErr error) {
	return planPublicationNamespace(ctx, outputRoot, positionID, candidates, claims, true)
}

func planPublicationNamespace(ctx context.Context, outputRoot string, positionID uuid.UUID, candidates []publication.NamespaceCandidate, claims []persistence.PublicationNamespaceClaim, exactTarget bool) (_ []persistence.PublicationNamespaceNode, resultErr error) {
	candidates = append([]publication.NamespaceCandidate(nil), candidates...)
	root, err := sourcefs.NewOutputOpener().OpenRoot(ctx, outputRoot)
	if err != nil {
		return nil, fmt.Errorf("open publication output root: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	if err := root.Verify(ctx); err != nil {
		return nil, fmt.Errorf("verify publication output root: %w", err)
	}
	// The output reset contract provisions analysis as the technical-data area.
	// Native comparison probes belong there, never beside release artifacts.
	traversal, ok := root.(sourcefs.OutputDirectoryTraversal)
	if !ok {
		return nil, sourcefs.ErrUnsupported
	}
	technicalParent, err := traversal.OpenExistingDir(ctx, "analysis")
	if errors.Is(err, fs.ErrNotExist) {
		technicalParent, err = root.OpenOrCreateDir(ctx, "analysis")
	}
	if err != nil {
		return nil, fmt.Errorf("open publication technical-data area: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, technicalParent.Close()) }()

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
		return sourcefs.NativeCompareComponentsIn(ctx, parent, technicalParent, a, b)
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

	// Resolve the directory portion first so sibling discovery runs under the
	// actual, scope-pinned directory representative (not merely rendered text).
	directoryCandidates := make([]publication.NamespaceCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.NodeKind == "directory" {
			directoryCandidates = append(directoryCandidates, candidate)
		}
	}
	directoryNodes, err := publication.PlanNativeNamespace(ctx, directoryCandidates, nativeClaims, compare)
	if err != nil {
		return nil, err
	}
	directoryByCandidate := make(map[string]publication.NamespaceNode, len(directoryNodes))
	for i, candidate := range directoryCandidates {
		directoryByCandidate[candidate.RelativePath] = directoryNodes[i]
	}

	// A position suffix is stable across retries and durable because the selected
	// path is recorded in the admission manifest/namespace claim. Increase the
	// UUID prefix only when the shorter spelling is occupied.
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.NodeKind != "file" {
			continue
		}
		parentCandidate := path.Dir(candidate.RelativePath)
		if parentCandidate == "." {
			parentCandidate = ""
		}
		parentPath, parentKey := parentCandidate, ""
		if parentCandidate != "" {
			node, ok := directoryByCandidate[parentCandidate]
			if !ok {
				return nil, fmt.Errorf("plan first publication namespace: missing planned parent %q", parentCandidate)
			}
			parentPath, parentKey = node.RelativePath, node.CanonicalKey
		}
		parent, openErr := openParent(parentPath)
		var diskEntries []sourcefs.Entry
		if openErr == nil {
			reader, ok := parent.(sourcefs.OutputDirectoryReader)
			if !ok {
				return nil, sourcefs.ErrUnsupported
			}
			diskEntries, err = reader.ReadDir(ctx)
			if err != nil {
				return nil, fmt.Errorf("read publication namespace siblings: %w", err)
			}
		} else if !errors.Is(openErr, fs.ErrNotExist) {
			return nil, openErr
		}
		base := path.Base(candidate.RelativePath)
		// First publication keeps a durable selected component across retries.
		// Audio upgrades instead retain their explicitly requested (possibly new
		// extension) name.
		if !exactTarget {
			for _, claim := range nativeClaims {
				if claim.NodeKind == "file" && claim.OwnerPositionID != nil && *claim.OwnerPositionID == positionID && canonicalNamespaceParent(claim.CanonicalKey) == parentKey {
					candidate.RelativePath = path.Join(parentCandidate, path.Base(claim.RelativePath))
					base = path.Base(candidate.RelativePath)
					break
				}
			}
		}
		ownClaim := false
		for _, claim := range nativeClaims {
			if claim.NodeKind == "file" && claim.OwnerPositionID != nil && *claim.OwnerPositionID == positionID && canonicalNamespaceParent(claim.CanonicalKey) == parentKey {
				if !exactTarget || path.Base(claim.RelativePath) == base {
					ownClaim = true
					break
				}
			}
		}
		if ownClaim && !exactTarget {
			continue
		}
		occupied := func(name string) (bool, error) {
			for _, entry := range diskEntries {
				equal, compareErr := compareExistingNamespaceComponent(ctx, parent, technicalParent, entry.Name, name)
				if compareErr != nil {
					return false, compareErr
				}
				if equal {
					if exactTarget && ownClaim && entry.Name == base && name == base {
						continue
					}
					return true, nil
				}
			}
			for _, claim := range nativeClaims {
				if canonicalNamespaceParent(claim.CanonicalKey) != parentKey {
					continue
				}
				if claim.NodeKind == "file" && claim.OwnerPositionID != nil && *claim.OwnerPositionID == positionID && (!exactTarget || path.Base(claim.RelativePath) == name) {
					continue
				}
				claimName := path.Base(claim.RelativePath)
				if !safeNamespaceProbeComponent(claimName) {
					continue
				}
				equal, compareErr := compare(ctx, parentPath, claimName, name)
				if compareErr != nil {
					return false, compareErr
				}
				if equal {
					return true, nil
				}
			}
			return false, nil
		}
		collision, err := occupied(base)
		if err != nil {
			return nil, err
		}
		if !collision {
			continue
		}
		if exactTarget {
			return nil, fmt.Errorf("plan exact publication namespace: requested target %q is occupied", candidate.RelativePath)
		}
		ext := path.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		id := strings.ReplaceAll(positionID.String(), "-", "")
		selected := ""
		for width := 8; width <= len(id); width += 4 {
			if width > len(id) {
				width = len(id)
			}
			name := stem + " [" + id[:width] + "]" + ext
			if !validFirstPublicationNamespaceComponent(name) {
				return nil, fmt.Errorf("plan first publication namespace: suffixed filename is not portable")
			}
			collision, err = occupied(name)
			if err != nil {
				return nil, err
			}
			if !collision {
				selected = name
				break
			}
		}
		if selected == "" {
			return nil, fmt.Errorf("plan first publication namespace: exhausted stable position suffixes")
		}
		candidate.RelativePath = path.Join(parentCandidate, selected)
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

func canonicalNamespaceParent(key string) string {
	if index := strings.LastIndexByte(key, '/'); index >= 0 {
		return key[:index]
	}
	return ""
}

func compareExistingNamespaceComponent(ctx context.Context, parent, technicalParent sourcefs.OutputDirectory, existing, candidate string) (bool, error) {
	// Names that cannot be represented by the portable probe syntax cannot be
	// equivalent to a portable candidate on supported filesystems (notably Unix
	// permits colon, backslash, and arbitrary byte sequences in directory entries).
	if !safeNamespaceProbeComponent(existing) {
		return false, nil
	}
	return sourcefs.NativeCompareComponentsIn(ctx, parent, technicalParent, existing, candidate)
}

func safeNamespaceProbeComponent(name string) bool {
	return utf8.ValidString(name) && name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\:\x00")
}
