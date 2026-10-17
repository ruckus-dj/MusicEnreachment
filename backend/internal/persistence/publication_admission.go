package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

// PublicationNamespaceNode is a caller-planned namespace reservation. CanonicalKey
// must use the caller's probed filesystem-equivalence rules; no path policy is
// inferred here.
type PublicationNamespaceNode struct {
	CanonicalKey   string `json:"canonical_key"`
	RelativePath   string `json:"relative_path"`
	NodeKind       string `json:"node_kind"`
	DirectoryScope string `json:"directory_scope,omitempty"`
}

type PublicationAdmission struct {
	PositionID          uuid.UUID
	ExpectedEpoch       uuid.UUID
	ExpectedGeneration  *uuid.UUID
	CandidateGeneration uuid.UUID
	IntentKind          string
	ToolsReadRequired   bool
	FrozenInputs        json.RawMessage
	NamespaceManifest   []PublicationNamespaceNode
}

type publicationTrackArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (publicationTrackArgs) Kind() string { return "publish_track_v1" }

// AdmitPublication atomically inserts the source-free operation snapshot, attempt,
// namespace reservations, and its River delivery.
func (repository *SetupManagerRepository) AdmitPublication(ctx context.Context, request PublicationAdmission, client RiverInserter) (uuid.UUID, error) {
	if client == nil || request.PositionID == uuid.Nil || request.ExpectedEpoch == uuid.Nil || request.CandidateGeneration == uuid.Nil {
		return uuid.Nil, fmt.Errorf("admit publication: invalid identity or missing River client")
	}
	if request.IntentKind != "audio" && request.IntentKind != "rewrite" {
		return uuid.Nil, fmt.Errorf("admit publication: unsupported intent")
	}
	if !publicationJSONObject(request.FrozenInputs) {
		return uuid.Nil, fmt.Errorf("admit publication: frozen inputs must be a JSON object")
	}
	if err := validatePublicationManifest(request.NamespaceManifest); err != nil {
		return uuid.Nil, err
	}
	operationID := uuid.New()
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("admit publication: lock output gate: %w", err)
		}
		var epoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
			return fmt.Errorf("admit publication: lock output epoch: %w", err)
		}
		if epoch != request.ExpectedEpoch {
			return fmt.Errorf("admit publication: output epoch changed")
		}
		var position uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id=? FOR UPDATE`, request.PositionID).Scan(ctx, &position); err != nil {
			return fmt.Errorf("admit publication: lock position: %w", err)
		}
		var generation *uuid.UUID
		err := tx.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, request.PositionID).Scan(ctx, &generation)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("admit publication: read current generation: %w", err)
		}
		if !sameOptionalUUID(generation, request.ExpectedGeneration) {
			return fmt.Errorf("admit publication: current generation changed")
		}
		var active uuid.UUID
		if err := tx.NewRaw(`SELECT operation_id FROM track_publication_attempt WHERE position_id=?`, request.PositionID).Scan(ctx, &active); err == nil {
			return fmt.Errorf("admit publication: position already has an unfinished attempt")
		} else if err != sql.ErrNoRows {
			return fmt.Errorf("admit publication: check active attempt: %w", err)
		}
		if err := reservePublicationNamespace(ctx, tx, request); err != nil {
			return err
		}
		snapshot, _ := json.Marshal(map[string]any{"version": 1, "position_id": request.PositionID.String(), "output_epoch": epoch.String(), "intent_kind": request.IntentKind})
		result, err := client.InsertTx(ctx, tx.Tx, publicationTrackArgs{OperationID: operationID}, nil)
		if err != nil {
			return fmt.Errorf("admit publication: insert River job: %w", err)
		}
		if result == nil || result.Job == nil {
			return fmt.Errorf("admit publication: insert River job returned no job")
		}
		op := &Operation{ID: operationID, Kind: "publish_track_v1", State: "queued", Stage: "queued", InputSnapshot: snapshot, ToolsReadRequired: request.ToolsReadRequired, Attempt: 1, RiverJobID: &result.Job.ID}
		if err := repository.CreateOperationWith(ctx, tx, op); err != nil {
			return fmt.Errorf("admit publication: create operation: %w", err)
		}
		manifest, _ := json.Marshal(struct {
			Version int                        `json:"version"`
			Nodes   []PublicationNamespaceNode `json:"nodes"`
		}{Version: 1, Nodes: request.NamespaceManifest})
		attempt := &TrackPublicationAttempt{OperationID: operationID, PositionID: request.PositionID, Epoch: epoch, ExpectedGeneration: request.ExpectedGeneration, CandidateGeneration: request.CandidateGeneration, IntentKind: request.IntentKind, Phase: "queued", FrozenInputs: request.FrozenInputs, NamespaceManifest: manifest, MutationJournal: json.RawMessage("[]")}
		if _, err := tx.NewInsert().Model(attempt).Exec(ctx); err != nil {
			return fmt.Errorf("admit publication: create attempt: %w", err)
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return operationID, nil
}

func reservePublicationNamespace(ctx context.Context, tx bun.Tx, request PublicationAdmission) error {
	var existing []PublicationNamespaceClaim
	if err := tx.NewSelect().Model(&existing).Where("epoch=?", request.ExpectedEpoch).For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("admit publication: inspect namespace: %w", err)
	}
	manifest := append([]PublicationNamespaceNode(nil), request.NamespaceManifest...)
	sort.Slice(manifest, func(i, j int) bool { return manifest[i].CanonicalKey < manifest[j].CanonicalKey })
	for _, n := range manifest {
		digest := sha256.Sum256([]byte(n.CanonicalKey))
		var reusable *PublicationNamespaceClaim
		for _, old := range existing {
			if string(old.KeyDigest) == string(digest[:]) && old.CanonicalKey != n.CanonicalKey {
				return fmt.Errorf("admit publication: namespace digest collision")
			}
			if old.CanonicalKey == n.CanonicalKey {
				if n.NodeKind == "dir" && old.NodeKind == "dir" && old.DirectoryScopeKey != nil && *old.DirectoryScopeKey == n.DirectoryScope {
					reusable = &old
					continue
				}
				if n.NodeKind == "file" && old.NodeKind == "file" && old.OwnerPositionID != nil && *old.OwnerPositionID == request.PositionID {
					reusable = &old
					continue
				}
				return fmt.Errorf("admit publication: namespace conflict at %q", n.RelativePath)
			}
			if publicationPrefixConflict(old.CanonicalKey, old.NodeKind, n.CanonicalKey, n.NodeKind) {
				return fmt.Errorf("admit publication: file/directory namespace conflict")
			}
		}
		var owner *uuid.UUID
		var scope *string
		if n.NodeKind == "file" {
			owner = &request.PositionID
		} else {
			scope = &n.DirectoryScope
		}
		claim := reusable
		if claim == nil {
			claim = &PublicationNamespaceClaim{ID: uuid.New(), Epoch: request.ExpectedEpoch, KeyDigest: digest[:], CanonicalKey: n.CanonicalKey, RelativePath: n.RelativePath, NodeKind: n.NodeKind, OwnerPositionID: owner, DirectoryScopeKey: scope}
			if _, err := tx.NewInsert().Model(claim).Exec(ctx); err != nil {
				return fmt.Errorf("admit publication: reserve namespace: %w", err)
			}
			existing = append(existing, *claim)
		}
		reservation := &PublicationNamespaceReservation{NamespaceClaimID: claim.ID, PositionID: request.PositionID, Generation: request.CandidateGeneration}
		if _, err := tx.NewInsert().Model(reservation).Exec(ctx); err != nil {
			return fmt.Errorf("admit publication: reserve generation: %w", err)
		}
	}
	return nil
}

func validatePublicationManifest(nodes []PublicationNamespaceNode) error {
	if len(nodes) == 0 {
		return fmt.Errorf("admit publication: namespace manifest is required")
	}
	ordered := append([]PublicationNamespaceNode(nil), nodes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].CanonicalKey < ordered[j].CanonicalKey })
	seen := map[string]bool{}
	byKey := make(map[string]PublicationNamespaceNode, len(ordered))
	for _, n := range ordered {
		if !portableRelativePath(n.CanonicalKey) || !portableRelativePath(n.RelativePath) || (n.NodeKind != "file" && n.NodeKind != "dir") || seen[n.CanonicalKey] {
			return fmt.Errorf("admit publication: invalid or duplicate namespace entry")
		}
		if n.NodeKind == "dir" && n.DirectoryScope == "" {
			return fmt.Errorf("admit publication: directory scope is required")
		}
		seen[n.CanonicalKey] = true
		byKey[n.CanonicalKey] = n
	}
	for _, n := range ordered {
		for parent := parentPath(n.CanonicalKey); parent != ""; parent = parentPath(parent) {
			parentNode, ok := byKey[parent]
			if !ok || parentNode.NodeKind != "dir" {
				return fmt.Errorf("admit publication: parent directory %q missing from manifest", parent)
			}
		}
		canonicalParent, relativeParent := parentPath(n.CanonicalKey), parentPath(n.RelativePath)
		if canonicalParent != "" {
			parentNode := byKey[canonicalParent]
			if relativeParent == "" || parentNode.RelativePath != relativeParent {
				return fmt.Errorf("admit publication: namespace parent paths do not correspond")
			}
		} else if relativeParent != "" {
			return fmt.Errorf("admit publication: namespace parent paths do not correspond")
		}
	}
	return nil
}

func portableRelativePath(path string) bool {
	if path == "" || strings.ContainsAny(path, "\\:\x00") || strings.HasPrefix(path, "/") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func publicationPrefixConflict(a, aKind, b, bKind string) bool {
	return aKind == "file" && strings.HasPrefix(b, a+"/") || bKind == "file" && strings.HasPrefix(a, b+"/")
}
func parentPath(s string) string {
	i := strings.LastIndexByte(s, '/')
	if i < 0 {
		return ""
	}
	return s[:i]
}
func publicationJSONObject(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && value != nil
}

var _ river.JobArgs = publicationTrackArgs{}
