package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type semanticNamespaceNode struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
}

// finalizeFirstPublicationNamespace performs native resolution from one
// coherent context snapshot. The pure preview builder remains filesystem-free.
func finalizeFirstPublicationNamespace(ctx context.Context, snapshot *persistence.LibraryFirstPublicationContext, outputRoot, template string, preview *FirstPublicationPreview) ([]persistence.PublicationNamespaceNode, error) {
	if snapshot == nil || preview == nil || len(preview.PathComponents) == 0 {
		return nil, fmt.Errorf("first publication namespace inputs are incomplete")
	}
	candidates, err := firstPublicationNamespaceCandidates(snapshot, template, preview.PathComponents, preview.ReleaseID, preview.PositionID)
	if err != nil {
		return nil, err
	}
	manifest, err := planFirstPublicationNamespace(ctx, outputRoot, preview.PositionID, candidates, snapshot.NamespaceClaims)
	if err != nil {
		return nil, err
	}
	preview.ResolvedPathComponents = resolvedFileComponents(manifest)
	if len(preview.ResolvedPathComponents) == 0 {
		return nil, fmt.Errorf("first publication namespace has no file node")
	}
	if err := applyNamespaceDigest(preview, manifest); err != nil {
		return nil, err
	}
	preview.NativeStatus = "native namespace resolved"
	return manifest, nil
}

// finalizeFirstPublicationCurrentComparisonNamespace resolves the rendered
// target without first-publication retry substitution. Only the exact active
// CURRENT file claim is exempted from collision checks.
func finalizeFirstPublicationCurrentComparisonNamespace(ctx context.Context, snapshot *persistence.LibraryFirstPublicationContext, outputRoot, template string, preview *FirstPublicationPreview) ([]persistence.PublicationNamespaceNode, error) {
	if snapshot == nil || snapshot.Library == nil || snapshot.Library.Current == nil || preview == nil || len(preview.PathComponents) == 0 {
		return nil, fmt.Errorf("first publication CURRENT comparison namespace inputs are incomplete")
	}
	candidates, err := firstPublicationNamespaceCandidates(snapshot, template, preview.PathComponents, preview.ReleaseID, preview.PositionID)
	if err != nil {
		return nil, err
	}
	current := snapshot.Library.Current
	var currentClaimID *uuid.UUID
	for _, node := range snapshot.Library.NamespaceManifest {
		if node.PositionID != nil && *node.PositionID == preview.PositionID && node.Generation != nil && *node.Generation == current.Record.Generation &&
			node.Claim.Epoch == current.Record.Epoch && node.Claim.NodeKind == "file" && node.Claim.OwnerPositionID != nil && *node.Claim.OwnerPositionID == preview.PositionID &&
			node.Claim.RelativePath == current.Record.RelativePath {
			id := node.Claim.ID
			currentClaimID = &id
			break
		}
	}
	manifest, err := planCurrentComparisonNamespace(ctx, outputRoot, preview.PositionID, candidates, snapshot.NamespaceClaims, current.Record.RelativePath, currentClaimID)
	if err != nil {
		return nil, err
	}
	preview.ResolvedPathComponents = resolvedFileComponents(manifest)
	if len(preview.ResolvedPathComponents) == 0 {
		return nil, fmt.Errorf("first publication CURRENT comparison namespace has no file node")
	}
	if err := applyNamespaceDigest(preview, manifest); err != nil {
		return nil, err
	}
	preview.NativeStatus = "native namespace resolved"
	return manifest, nil
}

func applyNamespaceDigest(preview *FirstPublicationPreview, manifest []persistence.PublicationNamespaceNode) error {
	semantic := make([]semanticNamespaceNode, 0, len(manifest))
	for _, node := range manifest {
		kind := node.NodeKind
		if kind == "dir" {
			kind = "directory"
		}
		semantic = append(semantic, semanticNamespaceNode{Path: node.RelativePath, Kind: kind, Scope: node.DirectoryScope})
	}
	sort.Slice(semantic, func(i, j int) bool {
		if semantic[i].Path != semantic[j].Path {
			return semantic[i].Path < semantic[j].Path
		}
		if semantic[i].Kind != semantic[j].Kind {
			return semantic[i].Kind < semantic[j].Kind
		}
		return semantic[i].Scope < semantic[j].Scope
	})
	encoded, err := json.Marshal(semantic)
	if err != nil {
		return fmt.Errorf("encode semantic publication namespace: %w", err)
	}
	digest := sha256.Sum256(append([]byte(preview.ContextDigest+":"), encoded...))
	preview.ContextDigest = hex.EncodeToString(digest[:])
	return nil
}

func resolvedFileComponents(manifest []persistence.PublicationNamespaceNode) []string {
	for _, node := range manifest {
		if node.NodeKind == "file" {
			return strings.Split(node.RelativePath, "/")
		}
	}
	return nil
}
