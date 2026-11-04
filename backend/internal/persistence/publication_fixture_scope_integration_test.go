//go:build integration

package persistence

import (
	"crypto/sha256"
	"path"
	"strings"
)

// A fixture scope is a unique, safe relative path prefix (normally one
// scenario token). Pass the same scope through a fixture chain. Empty scope
// deliberately preserves the legacy fixture paths exactly.
func scopePublicationPath(value, scope string) string {
	if scope == "" {
		return value
	}
	return path.Join(scope, value)
}

func scopePublicationInputs(input *PublicationInputs, scope string) {
	if scope == "" || input == nil {
		return
	}
	if input.Audio != nil {
		input.Audio.RelativePath = scopePublicationPath(input.Audio.RelativePath, scope)
	}
	if input.Rewrite != nil {
		input.Rewrite.RelativePath = scopePublicationPath(input.Rewrite.RelativePath, scope)
	}
}

func scopePublicationManifest(nodes []PublicationNamespaceNode, scope string) []PublicationNamespaceNode {
	if scope == "" {
		return nodes
	}
	var ancestors []PublicationNamespaceNode
	parts := strings.Split(path.Clean(scope), "/")
	for i := range parts {
		ancestor := strings.Join(parts[:i+1], "/")
		ancestors = append(ancestors, PublicationNamespaceNode{
			CanonicalKey: ancestor, RelativePath: ancestor, NodeKind: "dir",
			DirectoryScope: scope + ":scope:" + ancestor,
		})
	}
	result := append(ancestors, nodes...)
	for i := len(ancestors); i < len(result); i++ {
		result[i].CanonicalKey = scopePublicationPath(result[i].CanonicalKey, scope)
		result[i].RelativePath = scopePublicationPath(result[i].RelativePath, scope)
		if result[i].DirectoryScope != "" {
			result[i].DirectoryScope = scope + ":" + result[i].DirectoryScope
		}
	}
	return result
}

func publicationNamespaceKeyDigest(key string) []byte {
	digest := sha256.Sum256([]byte(key))
	return digest[:]
}
