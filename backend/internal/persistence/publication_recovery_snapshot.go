package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationStartupRecord contains only the identities and classifications
// needed by the startup barrier. Frozen inputs and journal contents stay local
// to the repeatable-read loader.
type PublicationStartupRecord struct {
	OperationID         uuid.UUID
	Phase               string
	OperationState      string
	HasClaim            bool
	QueuedContractValid bool
	JobAvailable        bool
	EpochKnown          bool
	EpochMatches        bool
	CurrentKnown        bool
	CurrentPresent      bool
	CurrentGeneration   *uuid.UUID
}

type PublicationStartupSnapshot struct{ Records []PublicationStartupRecord }

// LoadPublicationStartupSnapshot observes attempts, their operations, claims,
// River jobs, and the current epoch in one read-only repeatable-read transaction.
func (repository *SetupManagerRepository) LoadPublicationStartupSnapshot(ctx context.Context) (PublicationStartupSnapshot, error) {
	var snapshot PublicationStartupSnapshot
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var attempts []TrackPublicationAttempt
		if err := tx.NewSelect().Model(&attempts).Scan(ctx); err != nil {
			return fmt.Errorf("read publication startup attempts: %w", err)
		}
		var currentEpoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton = true`).Scan(ctx, &currentEpoch); err != nil {
			return fmt.Errorf("read publication startup epoch: %w", err)
		}
		var allClaims []PublicationExecutionClaim
		if err := tx.NewSelect().Model(&allClaims).Scan(ctx); err != nil {
			return fmt.Errorf("read publication startup claims: %w", err)
		}
		claimsByOperation := make(map[uuid.UUID][]PublicationExecutionClaim, len(allClaims))
		for _, claim := range allClaims {
			claimsByOperation[claim.OperationID] = append(claimsByOperation[claim.OperationID], claim)
		}
		seenAttempts := make(map[uuid.UUID]bool, len(attempts))
		for _, attempt := range attempts {
			seenAttempts[attempt.OperationID] = true
			record := PublicationStartupRecord{OperationID: attempt.OperationID, Phase: attempt.Phase, EpochKnown: true, EpochMatches: currentEpoch == attempt.Epoch}
			var current TrackPublication
			currentErr := tx.NewSelect().Model(&current).Where("position_id = ?", attempt.PositionID).Scan(ctx)
			record.CurrentKnown = true
			if currentErr == nil {
				record.CurrentPresent = true
				record.CurrentGeneration = &current.Generation
			} else if !errors.Is(currentErr, sql.ErrNoRows) {
				return fmt.Errorf("read publication startup current publication: %w", currentErr)
			}
			var operation Operation
			if err := tx.NewSelect().Model(&operation).Where("id = ?", attempt.OperationID).Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					snapshot.Records = append(snapshot.Records, record)
					continue
				}
				return fmt.Errorf("read publication startup operation: %w", err)
			}
			record.OperationState = operation.State
			record.HasClaim = len(claimsByOperation[attempt.OperationID]) > 0
			if !record.HasClaim && attempt.Phase == "queued" && operation.State == "queued" && operation.Kind == "publish_track_v1" && operation.RiverJobID != nil {
				var job struct {
					Kind  string          `bun:"kind"`
					Args  json.RawMessage `bun:"args"`
					State string          `bun:"state"`
				}
				err := tx.NewRaw(`SELECT kind, args, state FROM river_job WHERE id = ?`, *operation.RiverJobID).Scan(ctx, &job)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						snapshot.Records = append(snapshot.Records, record)
						continue
					}
					return fmt.Errorf("read publication startup job: %w", err)
				}
				record.JobAvailable = job.State == "available" || job.State == "scheduled" || job.State == "retryable"
				if record.JobAvailable {
					var contractErr error
					record.QueuedContractValid, contractErr = validQueuedPublicationContract(ctx, tx, job.Kind, job.Args, operation, attempt, record)
					if contractErr != nil {
						return fmt.Errorf("read publication startup namespace contract: %w", contractErr)
					}
				}
			}
			snapshot.Records = append(snapshot.Records, record)
		}
		for operationID, claims := range claimsByOperation {
			if !seenAttempts[operationID] {
				snapshot.Records = append(snapshot.Records, PublicationStartupRecord{OperationID: operationID, HasClaim: len(claims) > 0})
			}
		}
		return nil
	})
	if err != nil {
		return PublicationStartupSnapshot{}, err
	}
	return snapshot, nil
}

func validQueuedPublicationContract(ctx context.Context, tx bun.Tx, jobKind string, jobArgs json.RawMessage, operation Operation, attempt TrackPublicationAttempt, record PublicationStartupRecord) (bool, error) {
	if jobKind != (publicationTrackArgs{}).Kind() {
		return false, nil
	}
	var args publicationTrackArgs
	decoder := json.NewDecoder(bytes.NewReader(jobArgs))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil || decoder.Decode(new(any)) != io.EOF || args.OperationID != attempt.OperationID {
		return false, nil
	}
	var manifest struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}
	decoder = json.NewDecoder(bytes.NewReader(attempt.NamespaceManifest))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF || manifest.Version != 1 || validatePublicationManifest(manifest.Nodes) != nil {
		return false, nil
	}
	inputs, inputErr := DecodePublicationInputs(attempt.FrozenInputs)
	if inputErr != nil || inputs.IntentKind != attempt.IntentKind || !validPublicationOperationSnapshot(operation.InputSnapshot, attempt) || !sameOptionalUUID(record.CurrentGeneration, attempt.ExpectedGeneration) || (inputs.IntentKind != "audio" && inputs.IntentKind != "rewrite") {
		return false, nil
	}
	return validPublicationNamespaceReservations(ctx, tx, attempt, manifest.Nodes)
}

// validPublicationNamespaceReservations verifies the complete frozen manifest
// against persisted claims without acquiring locks or changing namespace state.
func validPublicationNamespaceReservations(ctx context.Context, tx bun.Tx, attempt TrackPublicationAttempt, nodes []PublicationNamespaceNode) (bool, error) {
	var claims []PublicationNamespaceClaim
	if err := tx.NewSelect().Model(&claims).Where("epoch = ?", attempt.Epoch).Scan(ctx); err != nil {
		return false, err
	}
	claimsByKey := make(map[string][]PublicationNamespaceClaim, len(claims))
	for _, claim := range claims {
		claimsByKey[claim.CanonicalKey] = append(claimsByKey[claim.CanonicalKey], claim)
	}
	for _, node := range nodes {
		digest := sha256.Sum256([]byte(node.CanonicalKey))
		matching := claimsByKey[node.CanonicalKey]
		if len(matching) == 0 {
			return false, nil
		}
		var selected *PublicationNamespaceClaim
		for i := range matching {
			claim := &matching[i]
			if string(claim.KeyDigest) == string(digest[:]) && claim.NodeKind == node.NodeKind {
				if selected != nil {
					return false, nil
				}
				selected = claim
			}
		}
		if selected == nil || (node.NodeKind == "dir" && (selected.OwnerPositionID != nil || selected.DirectoryScopeKey == nil || *selected.DirectoryScopeKey != node.DirectoryScope)) ||
			(node.NodeKind == "file" && (selected.OwnerPositionID == nil || *selected.OwnerPositionID != attempt.PositionID || selected.DirectoryScopeKey != nil)) {
			return false, nil
		}
		for _, other := range claims {
			if other.CanonicalKey == node.CanonicalKey {
				if node.NodeKind == "dir" && other.NodeKind == "dir" && other.DirectoryScopeKey != nil && *other.DirectoryScopeKey == node.DirectoryScope {
					continue
				}
				if node.NodeKind == "file" && other.NodeKind == "file" && other.OwnerPositionID != nil && *other.OwnerPositionID == attempt.PositionID {
					continue
				}
				return false, nil
			}
			if publicationPrefixConflict(other.CanonicalKey, other.NodeKind, node.CanonicalKey, node.NodeKind) {
				return false, nil
			}
		}
		var reserved bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM publication_namespace_reservation WHERE namespace_claim_id = ? AND position_id = ? AND generation = ?)`, selected.ID, attempt.PositionID, attempt.CandidateGeneration).Scan(ctx, &reserved); err != nil {
			return false, err
		}
		if !reserved {
			return false, nil
		}
	}
	return true, nil
}

func validPublicationOperationSnapshot(raw json.RawMessage, attempt TrackPublicationAttempt) bool {
	var v struct {
		Version    int       `json:"version"`
		PositionID uuid.UUID `json:"position_id"`
		Epoch      uuid.UUID `json:"output_epoch"`
		Intent     string    `json:"intent_kind"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	return v.Version == 1 && v.PositionID == attempt.PositionID && v.Epoch == attempt.Epoch && v.Intent == attempt.IntentKind
}
