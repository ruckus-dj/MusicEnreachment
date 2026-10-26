package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"io"
	"strings"

	"github.com/uptrace/bun"
)

func validatePublicationRewriteNamespace(ctx context.Context, tx bun.Tx, attempt *TrackPublicationAttempt, relativePath string) error {
	var manifest struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}
	if decodeStrictJSON(attempt.NamespaceManifest, &manifest) != nil || manifest.Version != 1 || validatePublicationManifest(manifest.Nodes) != nil {
		return sql.ErrNoRows
	}
	var target *PublicationNamespaceNode
	for _, node := range manifest.Nodes {
		if node.NodeKind == "file" && node.RelativePath == relativePath {
			if target != nil {
				return sql.ErrNoRows
			}
			copy := node
			target = &copy
		}
	}
	if target == nil {
		return sql.ErrNoRows
	}
	return validatePublicationTargetNamespace(ctx, tx, attempt, target.CanonicalKey, relativePath)
}

func validatePublicationTargetNamespace(ctx context.Context, tx bun.Tx, attempt *TrackPublicationAttempt, targetCanonicalKey, targetRelativePath string) error {
	var manifest struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(attempt.NamespaceManifest)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF || manifest.Version != 1 || validatePublicationManifest(manifest.Nodes) != nil {
		return sql.ErrNoRows
	}
	var target *PublicationNamespaceNode
	for n := range manifest.Nodes {
		if manifest.Nodes[n].CanonicalKey == targetCanonicalKey && manifest.Nodes[n].RelativePath == targetRelativePath && manifest.Nodes[n].NodeKind == "file" {
			target = &manifest.Nodes[n]
		}
	}
	if target == nil {
		return sql.ErrNoRows
	}
	keys := []string{targetCanonicalKey}
	for parent := parentPath(targetCanonicalKey); parent != ""; parent = parentPath(parent) {
		keys = append(keys, parent)
	}
	for _, key := range keys {
		var node *PublicationNamespaceNode
		for _, n := range manifest.Nodes {
			if n.CanonicalKey == key {
				copy := n
				node = &copy
			}
		}
		if node == nil || (key != targetCanonicalKey && node.NodeKind != "dir") || (key == targetCanonicalKey && node.NodeKind != "file") {
			return sql.ErrNoRows
		}
		digest := sha256.Sum256([]byte(key))
		var claim PublicationNamespaceClaim
		err := tx.NewSelect().Model(&claim).Where("epoch=? AND key_digest=?", attempt.Epoch, digest[:]).For("UPDATE").Scan(ctx)
		if err != nil {
			return err
		}
		if claim.CanonicalKey != key || claim.RelativePath != node.RelativePath || claim.NodeKind != node.NodeKind ||
			(node.NodeKind == "file" && (claim.OwnerPositionID == nil || *claim.OwnerPositionID != attempt.PositionID)) ||
			(node.NodeKind == "dir" && claim.OwnerPositionID != nil) {
			return sql.ErrNoRows
		}
		if node.NodeKind == "dir" && (claim.DirectoryScopeKey == nil || *claim.DirectoryScopeKey != node.DirectoryScope) {
			return sql.ErrNoRows
		}
		if node.NodeKind == "file" && claim.DirectoryScopeKey != nil {
			return sql.ErrNoRows
		}
		var reserved bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM publication_namespace_reservation WHERE namespace_claim_id=? AND position_id=? AND generation=?)`, claim.ID, attempt.PositionID, attempt.CandidateGeneration).Scan(ctx, &reserved); err != nil {
			return err
		}
		if !reserved {
			return sql.ErrNoRows
		}
	}
	return nil
}
