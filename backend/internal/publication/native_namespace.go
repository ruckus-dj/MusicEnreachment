package publication

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// NamespaceNode is the publication-layer form of a durable namespace claim.
// CanonicalKey is opaque and is not a filesystem path.
type NamespaceNode struct {
	CanonicalKey    string
	RelativePath    string
	NodeKind        string
	DirectoryScope  string
	OwnerPositionID *uuid.UUID
}

type NamespaceCandidate struct {
	RelativePath   string
	NodeKind       string
	DirectoryScope string
	PositionID     uuid.UUID
}

type NamespaceClaim struct {
	CanonicalKey    string
	RelativePath    string
	NodeKind        string
	DirectoryScope  string
	OwnerPositionID *uuid.UUID
}

// NativeComponentComparator reports native equivalence beneath parentRelative.
type NativeComponentComparator func(ctx context.Context, parentRelative, a, b string) (bool, error)

type namespaceEntry struct {
	path, key, kind, scope string
	owner                  *uuid.UUID
}

// PlanNativeNamespace plans claims and new candidates using only the caller's
// native filesystem-equivalence rules. It never normalizes rendered names.
func PlanNativeNamespace(ctx context.Context, candidates []NamespaceCandidate, claims []NamespaceClaim, compare NativeComponentComparator) ([]NamespaceNode, error) {
	if compare == nil {
		return nil, fmt.Errorf("plan native namespace: comparator is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries := make([]*namespaceEntry, 0, len(claims)+len(candidates)*2)
	keys := make(map[string]*namespaceEntry, len(claims))
	for _, c := range claims {
		if !validNamespacePath(c.RelativePath) || c.CanonicalKey == "" || (c.NodeKind != "file" && c.NodeKind != "directory") {
			return nil, fmt.Errorf("plan native namespace: invalid existing claim")
		}
		if (c.NodeKind == "file" && (c.OwnerPositionID == nil || *c.OwnerPositionID == uuid.Nil)) || (c.NodeKind == "directory" && c.OwnerPositionID != nil) {
			return nil, fmt.Errorf("plan native namespace: invalid existing claim owner")
		}
		if path.Dir(c.RelativePath) == "." && strings.Contains(c.CanonicalKey, "/") {
			return nil, fmt.Errorf("plan native namespace: root claim has a canonical parent")
		}
		if keys[c.CanonicalKey] != nil {
			return nil, fmt.Errorf("plan native namespace: duplicate canonical key")
		}
		e := &namespaceEntry{c.RelativePath, c.CanonicalKey, c.NodeKind, c.DirectoryScope, c.OwnerPositionID}
		keys[e.key] = e
		entries = append(entries, e)
	}
	requestedScopes := make(map[string]string)
	seenCandidates := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		if _, exists := seenCandidates[c.RelativePath]; exists {
			return nil, fmt.Errorf("plan native namespace: duplicate candidate path")
		}
		seenCandidates[c.RelativePath] = struct{}{}
		if c.NodeKind != "directory" {
			continue
		}
		if prior, ok := requestedScopes[c.RelativePath]; ok && prior != c.DirectoryScope {
			return nil, fmt.Errorf("plan native namespace: conflicting requested directory scopes")
		}
		requestedScopes[c.RelativePath] = c.DirectoryScope
	}
	for _, e := range entries {
		p := path.Dir(e.path)
		if p != "." {
			parent := keys[canonicalParent(e.key)]
			if parent == nil || parent.path != p || parent.kind != "directory" {
				return nil, fmt.Errorf("plan native namespace: inconsistent existing parent chain")
			}
		}
	}
	siblings := make(map[string][]*namespaceEntry)
	for _, e := range entries {
		siblings[canonicalParent(e.key)] = append(siblings[canonicalParent(e.key)], e)
	}
	ordered := append([]NamespaceCandidate(nil), candidates...)
	for _, c := range ordered {
		if !validNamespacePath(c.RelativePath) || (c.NodeKind != "file" && c.NodeKind != "directory") || c.PositionID == uuid.Nil {
			return nil, fmt.Errorf("plan native namespace: invalid candidate")
		}
		for p := path.Dir(c.RelativePath); p != "."; p = path.Dir(p) {
			if _, ok := requestedScopes[p]; !ok {
				return nil, fmt.Errorf("plan native namespace: candidate directory chain is incomplete at %q", p)
			}
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return strings.Count(ordered[i].RelativePath, "/") < strings.Count(ordered[j].RelativePath, "/")
	})
	planned := make(map[string]NamespaceNode, len(candidates))
	for _, c := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !validNamespacePath(c.RelativePath) || (c.NodeKind != "file" && c.NodeKind != "directory") || c.PositionID == uuid.Nil {
			return nil, fmt.Errorf("plan native namespace: invalid candidate")
		}
		parts := strings.Split(c.RelativePath, "/")
		parentPath, parentKey := "", ""
		for i, component := range parts {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			kind, scope := "directory", requestedScopes[strings.Join(parts[:i+1], "/")]
			if i == len(parts)-1 {
				kind = c.NodeKind
				if kind == "directory" {
					scope = c.DirectoryScope
				}
			}
			rel := component
			if parentPath != "" {
				rel = parentPath + "/" + component
			}
			var found *namespaceEntry
			siblingsForParent := siblings[parentKey]
			if kind == "directory" {
				var scoped *namespaceEntry
				for _, e := range siblingsForParent {
					if e.kind != "directory" {
						continue
					}
					if e.scope == scope {
						if scoped != nil && scoped.key != e.key {
							return nil, fmt.Errorf("plan native namespace: ambiguous existing directory scope %q", scope)
						}
						scoped = e
					}
				}
				if scoped != nil {
					parentRelative := parentPath
					if p := keys[parentKey]; p != nil {
						parentRelative = p.path
					}
					for _, e := range siblingsForParent {
						if e.key == scoped.key {
							continue
						}
						for _, requestedName := range []string{component, path.Base(scoped.path)} {
							eq, err := compare(ctx, parentRelative, path.Base(e.path), requestedName)
							if err != nil {
								return nil, fmt.Errorf("plan native namespace: compare scoped directory sibling: %w", err)
							}
							if err := ctx.Err(); err != nil {
								return nil, err
							}
							if eq {
								return nil, fmt.Errorf("plan native namespace: directory name collides with another scope")
							}
						}
					}
					found = scoped
				}
			}
			for _, e := range siblingsForParent {
				eq, err := compare(ctx, parentPath, path.Base(e.path), component)
				if err != nil {
					return nil, fmt.Errorf("plan native namespace: compare components: %w", err)
				}
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if eq {
					if found != nil && found.key != e.key {
						return nil, fmt.Errorf("plan native namespace: conflicting equivalent claims")
					}
					found = e
				}
			}
			if found != nil {
				if found.kind != kind {
					return nil, fmt.Errorf("plan native namespace: file/directory collision at %q", rel)
				}
				if kind == "directory" && found.scope != scope {
					return nil, fmt.Errorf("plan native namespace: directory scope conflict at %q", rel)
				}
				if kind == "file" && found.owner != nil && *found.owner != c.PositionID {
					return nil, fmt.Errorf("plan native namespace: file is owned by another position")
				}
				rel, parentKey = found.path, found.key
			} else {
				id := uuid.New()
				key := id.String()
				if parentKey != "" {
					key = parentKey + "/" + id.String()
				}
				var owner *uuid.UUID
				if kind == "file" {
					v := c.PositionID
					owner = &v
				}
				found = &namespaceEntry{rel, key, kind, scope, owner}
				entries = append(entries, found)
				keys[key] = found
				siblings[parentKey] = append(siblings[parentKey], found)
				parentKey = key
			}
			parentPath = rel
			if i == len(parts)-1 {
				planned[c.RelativePath] = NamespaceNode{found.key, found.path, found.kind, found.scope, found.owner}
			}
		}
	}
	result := make([]NamespaceNode, 0, len(candidates))
	for _, c := range candidates {
		result = append(result, planned[c.RelativePath])
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func validNamespacePath(s string) bool {
	if s == "" || strings.HasPrefix(s, "/") || strings.ContainsAny(s, "\\:\x00") {
		return false
	}
	for _, p := range strings.Split(s, "/") {
		if p == "" || p == "." || p == ".." {
			return false
		}
	}
	return true
}
func canonicalParent(key string) string {
	i := strings.LastIndexByte(key, '/')
	if i < 0 {
		return ""
	}
	return key[:i]
}
