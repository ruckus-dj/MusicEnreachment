package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type MatchingRecheckAbandonmentPreview struct {
	Revision  string                               `json:"revision"`
	Positions []MatchingRecheckAbandonmentPosition `json:"positions"`
}

type MatchingRecheckAbandonmentPosition struct {
	OperationID  uuid.UUID `json:"operation_id"`
	RelativePath string    `json:"relative_path"`
	Committed    bool      `json:"committed"`
}

type MatchingRecheckAbandonmentPredecessor struct {
	Current          TrackPublication
	NamespaceClaimID uuid.UUID
	AbsentTargetPath string
}

type matchingRecheckAbandonment struct {
	preview      MatchingRecheckAbandonmentPreview
	recheckID    uuid.UUID
	outputRoot   string
	predecessors []MatchingRecheckAbandonmentPredecessor
	pending      []publicationMatchingRecheckMember
}

func (repository *SetupManagerRepository) PreviewMatchingRecheckAbandonment(ctx context.Context, operationID uuid.UUID) (MatchingRecheckAbandonmentPreview, error) {
	var preview MatchingRecheckAbandonmentPreview
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		scope, err := readMatchingRecheckAbandonment(ctx, tx, operationID)
		preview = scope.preview
		return err
	})
	return preview, err
}

// AbandonMatchingRecheckPublications closes a failed recheck without native
// mutation or re-arming it. The caller holds the publication coordinator and
// must prove all unfinished predecessors still match their installed bytes.
func (repository *SetupManagerRepository) AbandonMatchingRecheckPublications(
	ctx context.Context, operationID uuid.UUID, revision string,
	verify func(context.Context, string, []MatchingRecheckAbandonmentPredecessor) error,
) error {
	if operationID == uuid.Nil || !validSHA256(revision) || verify == nil {
		return ErrPublicationAdmissionConflict
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		scope, err := readMatchingRecheckAbandonment(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if scope.preview.Revision != revision {
			return ErrPublicationAdmissionConflict
		}
		if err := verify(ctx, scope.outputRoot, scope.predecessors); err != nil {
			return fmt.Errorf("verify unchanged matching recheck output: %w", err)
		}
		if _, err := tx.NewRaw(`DELETE FROM matching_local_recheck WHERE id=? AND state='publishing'`,
			scope.recheckID).Exec(ctx); err != nil {
			return fmt.Errorf("close abandoned matching recheck: %w", err)
		}
		for _, member := range scope.pending {
			if err := retireMatchingRecheckAbandonedAttempt(ctx, tx, member); err != nil {
				return err
			}
		}
		return nil
	})
}

func readMatchingRecheckAbandonment(ctx context.Context, tx bun.Tx, operationID uuid.UUID) (matchingRecheckAbandonment, error) {
	var scope matchingRecheckAbandonment
	if err := lockOutputAdmissionGateExclusive(ctx, tx); err != nil {
		return scope, err
	}
	var epoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
		return scope, fmt.Errorf("lock matching recheck abandonment epoch: %w", err)
	}
	if err := tx.NewRaw(`SELECT recheck.id FROM matching_local_recheck recheck
		JOIN matching_local_recheck_publication member ON member.recheck_id=recheck.id
		WHERE member.operation_id=? AND member.committed_at IS NULL AND recheck.state='publishing' FOR UPDATE OF recheck`,
		operationID).Scan(ctx, &scope.recheckID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return scope, ErrPublicationAdmissionConflict
		}
		return scope, fmt.Errorf("lock matching recheck to abandon: %w", err)
	}
	_, outputRoot, _, err := readRuntimeRoots(ctx, tx)
	if err != nil {
		return scope, err
	}
	scope.outputRoot = outputRoot
	var members []publicationMatchingRecheckMember
	if err := tx.NewRaw(`SELECT * FROM matching_local_recheck_publication WHERE recheck_id=? ORDER BY operation_id FOR UPDATE`,
		scope.recheckID).Scan(ctx, &members); err != nil {
		return scope, fmt.Errorf("lock matching recheck abandonment members: %w", err)
	}
	var deliveries []matchingRecheckAbandonmentDelivery
	hasCommitted := false
	for _, member := range members {
		if member.OutputEpoch != epoch || member.PredecessorPositionID == nil || member.PredecessorGeneration == nil || member.PredecessorClaimID == nil {
			return scope, ErrPublicationAdmissionConflict
		}
		var witness matchingRecheckPublicationPredecessorWitness
		if err := decodeStrictJSON(member.PredecessorSnapshot, &witness); err != nil ||
			witness.Version != 1 || witness.PositionID != *member.PredecessorPositionID || witness.OutputEpoch != epoch ||
			witness.Generation != *member.PredecessorGeneration || witness.NamespaceClaimID != *member.PredecessorClaimID ||
			witness.RelativePath != witness.Current.RelativePath || witness.Current.Generation != witness.Generation {
			return scope, ErrPublicationAdmissionConflict
		}
		scope.preview.Positions = append(scope.preview.Positions, MatchingRecheckAbandonmentPosition{
			OperationID: member.OperationID, RelativePath: witness.RelativePath, Committed: member.CommittedAt != nil,
		})
		if member.CommittedAt != nil {
			hasCommitted = true
			continue
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", witness.PositionID).For("UPDATE").Scan(ctx); err != nil {
			return scope, fmt.Errorf("lock matching recheck abandonment predecessor: %w", err)
		}
		if !publicationCurrentMatchesWitness(current, witness.Current, epoch) {
			return scope, ErrPublicationAdmissionConflict
		}
		var claim uuid.UUID
		if err := tx.NewRaw(`SELECT c.id FROM publication_namespace_claim c JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
			WHERE c.id=? AND c.epoch=? AND c.node_kind='file' AND c.owner_position_id=? AND c.relative_path=?
			AND r.position_id=? AND r.generation=? FOR UPDATE OF c,r`, witness.NamespaceClaimID, epoch, witness.PositionID,
			witness.RelativePath, witness.PositionID, witness.Generation).Scan(ctx, &claim); err != nil {
			return scope, fmt.Errorf("lock matching recheck abandonment predecessor claim: %w", err)
		}
		delivery, err := lockMatchingRecheckAbandonmentDelivery(ctx, tx, member)
		if err != nil {
			return scope, err
		}
		pred := MatchingRecheckAbandonmentPredecessor{Current: current, NamespaceClaimID: claim}
		if !member.SamePath {
			var manifest struct {
				Version int                        `json:"version"`
				Nodes   []PublicationNamespaceNode `json:"nodes"`
			}
			if decodeStrictJSON(delivery.Attempt.NamespaceManifest, &manifest) != nil ||
				manifest.Version != 1 || validatePublicationManifest(manifest.Nodes) != nil {
				return scope, ErrPublicationAdmissionConflict
			}
			for _, node := range manifest.Nodes {
				if node.NodeKind == "file" {
					if pred.AbsentTargetPath != "" || node.RelativePath == witness.RelativePath {
						return scope, ErrPublicationAdmissionConflict
					}
					pred.AbsentTargetPath = node.RelativePath
				}
			}
			if pred.AbsentTargetPath == "" {
				return scope, ErrPublicationAdmissionConflict
			}
		}
		deliveries = append(deliveries, delivery)
		scope.pending = append(scope.pending, member)
		scope.predecessors = append(scope.predecessors, pred)
	}
	if !hasCommitted || len(scope.pending) == 0 {
		return scope, ErrPublicationAdmissionConflict
	}
	encoded, err := json.Marshal(struct {
		Root       string
		Members    []publicationMatchingRecheckMember
		Deliveries []matchingRecheckAbandonmentDelivery
	}{outputRoot, members, deliveries})
	if err != nil {
		return scope, fmt.Errorf("encode matching recheck abandonment revision: %w", err)
	}
	hash := sha256.Sum256(encoded)
	scope.preview.Revision = hex.EncodeToString(hash[:])
	return scope, nil
}

type matchingRecheckAbandonmentDelivery struct {
	Attempt          TrackPublicationAttempt
	OperationAttempt int
	JobID            int64
	Claims           []PublicationExecutionClaim
}

func lockMatchingRecheckAbandonmentDelivery(ctx context.Context, tx bun.Tx, member publicationMatchingRecheckMember) (matchingRecheckAbandonmentDelivery, error) {
	var delivery matchingRecheckAbandonmentDelivery
	var operation Operation
	if err := tx.NewSelect().Model(&delivery.Attempt).Where("operation_id=?", member.OperationID).For("UPDATE").Scan(ctx); err != nil {
		return delivery, fmt.Errorf("lock abandoned matching recheck attempt: %w", err)
	}
	if err := tx.NewSelect().Model(&operation).Where("id=?", member.OperationID).For("UPDATE").Scan(ctx); err != nil {
		return delivery, fmt.Errorf("lock abandoned matching recheck operation: %w", err)
	}
	attempt := delivery.Attempt
	if attempt.PositionID != member.PositionID || attempt.Epoch != member.OutputEpoch ||
		attempt.CandidateGeneration != member.CandidateGeneration || operation.Kind != "publish_track_v1" ||
		operation.State != "failed" || operation.FinishedAt == nil || operation.RiverJobID == nil {
		return delivery, ErrPublicationAdmissionConflict
	}
	var jobs []struct {
		ID          int64      `bun:"id"`
		Attempt     int        `bun:"attempt"`
		State       string     `bun:"state"`
		Matches     bool       `bun:"matches"`
		FinalizedAt *time.Time `bun:"finalized_at"`
	}
	if err := tx.NewRaw(`SELECT id,attempt,state,finalized_at,
			(kind='publish_track_v1' AND args->>'operation_id'=?) IS TRUE AS matches FROM river_job
		WHERE id=? OR (kind='publish_track_v1' AND args->>'operation_id'=?) ORDER BY id FOR UPDATE`,
		member.OperationID.String(), *operation.RiverJobID, member.OperationID.String()).Scan(ctx, &jobs); err != nil {
		return delivery, fmt.Errorf("lock abandoned matching recheck jobs: %w", err)
	}
	var jobAttempt int
	var foundJob bool
	for _, job := range jobs {
		if !job.Matches || job.FinalizedAt == nil || (job.State != "completed" && job.State != "cancelled" && job.State != "discarded") {
			return delivery, ErrPublicationAdmissionConflict
		}
		if job.ID == *operation.RiverJobID {
			foundJob = true
			jobAttempt = job.Attempt
		}
	}
	delivery.JobID = *operation.RiverJobID
	if err := tx.NewSelect().Model(&delivery.Claims).Where("operation_id=?", member.OperationID).
		Order("application_attempt").For("UPDATE").Scan(ctx); err != nil {
		return delivery, fmt.Errorf("lock abandoned matching recheck claims: %w", err)
	}
	quiescent := false
	for _, claim := range delivery.Claims {
		if claim.State != "quiescent" || claim.Epoch != attempt.Epoch {
			return delivery, ErrPublicationAdmissionConflict
		}
		if claim.ApplicationAttempt == operation.Attempt && claim.RiverJobID == delivery.JobID &&
			(claim.RiverAttempt == jobAttempt || !foundJob && claim.RiverAttempt > 0) {
			quiescent = true
		}
	}
	if !quiescent && (len(delivery.Claims) != 0 || attempt.Phase != "queued" || operation.StartedAt != nil || jobAttempt != 0) {
		return delivery, ErrPublicationAdmissionConflict
	}
	// A pruned job cannot be redelivered. The failed operation plus its exact
	// returned claim (or provably untouched queued attempt) still prove native
	// quiescence; a present matching job must have been proven terminal above.
	delivery.OperationAttempt = operation.Attempt
	return delivery, nil
}

func retireMatchingRecheckAbandonedAttempt(ctx context.Context, tx bun.Tx, member publicationMatchingRecheckMember) error {
	var fileClaim uuid.UUID
	if !member.SamePath {
		err := tx.NewRaw(`SELECT c.id FROM publication_namespace_claim c JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
			WHERE c.epoch=? AND c.owner_position_id=? AND c.node_kind='file' AND r.position_id=? AND r.generation=? FOR UPDATE OF c`,
			member.OutputEpoch, member.PositionID, member.PositionID, member.CandidateGeneration).Scan(ctx, &fileClaim)
		if err != nil {
			return fmt.Errorf("lock abandoned matching destination claim: %w", err)
		}
	}
	if _, err := tx.NewRaw(`DELETE FROM publication_namespace_reservation r USING publication_namespace_claim c
		WHERE c.id=r.namespace_claim_id AND c.epoch=? AND r.position_id=? AND r.generation=?`,
		member.OutputEpoch, member.PositionID, member.CandidateGeneration).Exec(ctx); err != nil {
		return fmt.Errorf("release abandoned matching recheck candidate reservations: %w", err)
	}
	if fileClaim != uuid.Nil {
		if _, err := tx.NewRaw(`DELETE FROM publication_namespace_claim c WHERE c.id=?
			AND NOT EXISTS (SELECT 1 FROM publication_namespace_reservation r WHERE r.namespace_claim_id=c.id)
			AND NOT EXISTS (SELECT 1 FROM obsolete_publication_output_cleanup o WHERE o.namespace_claim_id=c.id)
			AND NOT EXISTS (SELECT 1 FROM publication_root_abandonment a WHERE a.epoch=c.epoch AND c.id=ANY(a.namespace_claim_ids))`,
			fileClaim).Exec(ctx); err != nil {
			return fmt.Errorf("release unused matching destination claim: %w", err)
		}
	}
	for _, query := range []string{
		`DELETE FROM publication_source_hold WHERE operation_id=?`,
		`DELETE FROM operation_tool_read_hold WHERE operation_id=?`,
		`DELETE FROM publication_execution_claim WHERE operation_id=?`,
		`DELETE FROM track_publication_attempt WHERE operation_id=?`,
		`DELETE FROM operation WHERE id=? AND state='failed'`,
	} {
		if _, err := tx.NewRaw(query, member.OperationID).Exec(ctx); err != nil {
			return fmt.Errorf("retire abandoned matching recheck attempt: %w", err)
		}
	}
	return nil
}
