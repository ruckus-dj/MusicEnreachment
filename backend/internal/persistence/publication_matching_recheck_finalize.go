package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

// matchingRecheckMovedOutputFinalizeLimit bounds one cleanup tick. Rechecks are
// independent complete-success barriers, so later ticks continue with the next
// deterministic set instead of retrying one large batch.
const matchingRecheckMovedOutputFinalizeLimit = 16

// matchingRecheckMovedOutput is one committed different-path publication and
// the predecessor CURRENT its receipt froze at admission. Retain records the
// protected case: the CURRENT stays and only the membership is closed.
type matchingRecheckMovedOutput struct {
	member             publicationMatchingRecheckMember
	current            TrackPublication
	retain             bool
	destination        TrackPublication
	destinationClaimID uuid.UUID
}

// matchingRecheckMovedCleanup is one moved receipt decided by complete success.
// A nil proof keeps the predecessor CURRENT and its associations; only the
// recheck receipt and source veto are released.
type matchingRecheckMovedCleanup struct {
	member  publicationMatchingRecheckMember
	current TrackPublication
	proof   *ObsoletePublicationOutputCleanupAdmission
}

// FinalizeMatchingRecheckMovedOutputs retires the predecessor publications of
// completed different-path Local-to-MusicBrainz rechecks. A moved receipt stays
// live after its own result commit because retirement is authorized only by the
// recheck's complete success barrier: every receipt must be committed before any
// predecessor CURRENT is touched.
//
// The caller supplies capture, which must prove the actual output bytes against
// the exact CURRENT passed to it and return native cleanup authority. The path
// argument is the exact predecessor target; the callback may ignore it and close
// over its configured output root. This method never reads settings or the
// output root itself, and it invokes capture before taking the publication
// locks, so no filesystem work runs inside the cross-position cleanup
// transaction.
//
// One transaction per recheck takes the shared output-admission gate, the source
// mutation gate and the output-epoch lock before re-reading the receipts, the
// destination CURRENTs, the predecessor CURRENTs and their file claims. A
// protected predecessor is a definitive no-retirement: its CURRENT and
// associations stay, while the receipt and source veto are closed. A destination
// that is no longer the committed publication, or a changed native output,
// keeps the predecessor CURRENT and leaves the obligation pending; a later tick
// may prove it again. Pending is not an error, so the periodic caller must not
// treat a skipped recheck as work failure.
func (repository *SetupManagerRepository) FinalizeMatchingRecheckMovedOutputs(
	ctx context.Context,
	limit int,
	capture func(context.Context, string, TrackPublication, uuid.UUID) (*ObsoletePublicationOutputCleanupAdmission, error),
) error {
	if repository == nil || repository.db == nil {
		return errors.New("finalize matching recheck moved outputs: repository is unavailable")
	}
	if capture == nil {
		return errors.New("finalize matching recheck moved outputs: native proof callback is required")
	}
	if limit < 1 || limit > matchingRecheckMovedOutputFinalizeLimit {
		return fmt.Errorf("finalize matching recheck moved outputs: limit must be between 1 and %d", matchingRecheckMovedOutputFinalizeLimit)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ids, err := repository.listFullyCommittedMatchingRecheckMoves(ctx, limit)
	if err != nil {
		return fmt.Errorf("list completed matching recheck moves: %w", err)
	}
	problems := make([]error, 0)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			problems = append(problems, err)
			break
		}
		if err := repository.finalizeMatchingRecheckMovedOutputs(ctx, id, capture); err != nil {
			problems = append(problems, fmt.Errorf("finalize matching recheck moved outputs %s: %w", id, err))
		}
	}
	return errors.Join(problems...)
}

func (repository *SetupManagerRepository) listFullyCommittedMatchingRecheckMoves(ctx context.Context, limit int) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, limit)
	err := repository.db.NewRaw(`SELECT recheck.id FROM matching_local_recheck recheck
		WHERE recheck.state='publishing'
			AND EXISTS (
				SELECT 1 FROM matching_local_recheck_publication publication
				WHERE publication.recheck_id=recheck.id AND NOT publication.same_path
					AND publication.committed_at IS NOT NULL
			)
			AND NOT EXISTS (
				SELECT 1 FROM matching_local_recheck_publication publication
				WHERE publication.recheck_id=recheck.id AND publication.committed_at IS NULL
			)
		ORDER BY recheck.id LIMIT ?`, limit).Scan(ctx, &ids)
	return ids, err
}

func (repository *SetupManagerRepository) finalizeMatchingRecheckMovedOutputs(
	ctx context.Context,
	recheckID uuid.UUID,
	capture func(context.Context, string, TrackPublication, uuid.UUID) (*ObsoletePublicationOutputCleanupAdmission, error),
) error {
	outputs, err := repository.readMatchingRecheckMovedOutputs(ctx, recheckID)
	if err != nil {
		return fmt.Errorf("read committed moved outputs: %w", err)
	}
	if len(outputs) == 0 {
		return nil
	}
	if repository.riverInserter == nil {
		return errors.New("finalize matching recheck moved outputs: River inserter is unavailable")
	}
	proofs := make([]*ObsoletePublicationOutputCleanupAdmission, len(outputs))
	for index, output := range outputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if output.retain {
			// Protection was already proved in the preflight. The locked
			// transaction re-checks it; a flip to retireable without a proof is
			// only pending for this tick.
			continue
		}
		// The callback must re-prove the CURRENT bytes natively and return only
		// frozen authority for this exact predecessor target.
		proof, err := capture(ctx, output.current.RelativePath, output.current, *output.member.PredecessorClaimID)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// A missing, drifted or otherwise unprovable output is pending, not a
			// cleanup decision: keep CURRENT and try again on a later tick.
			return nil
		}
		if proof == nil {
			return nil
		}
		destinationProof, err := capture(ctx, output.destination.RelativePath, output.destination, output.destinationClaimID)
		if err != nil || destinationProof == nil {
			return ctx.Err()
		}
		proofs[index] = proof
	}
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		return repository.finalizeMatchingRecheckMovedOutputsTx(ctx, tx, recheckID, outputs, proofs)
	})
	if errors.Is(err, ErrPublicationAdmissionConflict) {
		// Drift keeps the predecessor CURRENT and the obligation pending.
		return nil
	}
	return err
}

// readMatchingRecheckMovedOutputs freezes the capture inputs without holding
// publication locks. The transaction re-reads every one of these facts under
// the gates; a stale preflight can only make the finalizer skip.
func (repository *SetupManagerRepository) readMatchingRecheckMovedOutputs(ctx context.Context, recheckID uuid.UUID) ([]matchingRecheckMovedOutput, error) {
	var outputs []matchingRecheckMovedOutput
	skip := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if recheckID == uuid.Nil {
			return ErrPublicationAdmissionConflict
		}
		var state string
		err := tx.NewRaw(`SELECT state FROM matching_local_recheck WHERE id=?`, recheckID).Scan(ctx, &state)
		if errors.Is(err, sql.ErrNoRows) {
			skip = true
			return nil
		}
		if err != nil {
			return err
		}
		if state != "publishing" {
			skip = true
			return nil
		}
		var pending bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM matching_local_recheck_publication WHERE recheck_id=? AND committed_at IS NULL
		)`, recheckID).Scan(ctx, &pending); err != nil {
			return err
		}
		if pending {
			skip = true
			return nil
		}
		members := make([]publicationMatchingRecheckMember, 0, 4)
		if err := tx.NewRaw(`SELECT recheck_id,operation_id,output_epoch,position_id,candidate_generation,
				predecessor_position_id,predecessor_generation,predecessor_claim_id,same_path,predecessor_snapshot,committed_at
			FROM matching_local_recheck_publication
			WHERE recheck_id=? AND NOT same_path AND committed_at IS NOT NULL
			ORDER BY operation_id`, recheckID).Scan(ctx, &members); err != nil {
			return err
		}
		if len(members) == 0 {
			skip = true
			return nil
		}
		for index := range members {
			member := members[index]
			if member.PredecessorPositionID == nil {
				skip = true
				return nil
			}
			var current TrackPublication
			err := tx.NewSelect().Model(&current).Where("position_id=?", *member.PredecessorPositionID).Scan(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				skip = true
				return nil
			}
			if err != nil {
				return err
			}
			if !matchingRecheckMovedMemberMatchesCurrent(member, current) {
				skip = true
				return nil
			}
			// Decide protection before requiring native proof: a protected
			// predecessor is a definitive retention, so an unprovable output must
			// not keep its membership pending forever. The locked transaction
			// repeats this decision under the gates.
			retire, err := matchingRecheckPredecessorCanRetireTx(ctx, tx, &member, current)
			if err != nil {
				if errors.Is(err, ErrPublicationAdmissionConflict) {
					skip = true
					return nil
				}
				return err
			}
			output := matchingRecheckMovedOutput{member: member, current: current, retain: !retire}
			if retire {
				err := tx.NewSelect().Model(&output.destination).Where("position_id=? AND epoch=? AND generation=?",
					member.PositionID, member.OutputEpoch, member.CandidateGeneration).Scan(ctx)
				if errors.Is(err, sql.ErrNoRows) {
					skip = true
					return nil
				}
				if err != nil {
					return err
				}
				output.destinationClaimID, err = matchingRecheckMovedDestinationClaim(ctx, tx, output.destination)
				if errors.Is(err, sql.ErrNoRows) {
					skip = true
					return nil
				}
				if err != nil {
					return err
				}
			}
			outputs = append(outputs, output)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if skip {
		return nil, nil
	}
	return outputs, nil
}

// matchingRecheckMovedMemberMatchesCurrent recognizes only the exact
// cross-position receipt and the predecessor witness it froze at admission.
func matchingRecheckMovedMemberMatchesCurrent(member publicationMatchingRecheckMember, current TrackPublication) bool {
	if member.SamePath || member.CommittedAt == nil || member.PredecessorPositionID == nil ||
		member.PredecessorGeneration == nil || member.PredecessorClaimID == nil ||
		member.PositionID == uuid.Nil || member.PositionID == *member.PredecessorPositionID {
		return false
	}
	var witness matchingRecheckPublicationPredecessorWitness
	if decodeStrictJSON(member.PredecessorSnapshot, &witness) != nil || witness.Version != 1 ||
		witness.PositionID != *member.PredecessorPositionID || witness.Generation != *member.PredecessorGeneration ||
		witness.OutputEpoch != member.OutputEpoch || witness.NamespaceClaimID != *member.PredecessorClaimID ||
		witness.Current.RelativePath != witness.RelativePath || witness.Current.Generation != witness.Generation {
		return false
	}
	return current.PositionID == witness.PositionID && current.Epoch == witness.OutputEpoch &&
		current.Generation == witness.Generation && current.RelativePath == witness.RelativePath &&
		publicationCurrentMatchesWitness(current, witness.Current, witness.OutputEpoch)
}

func (repository *SetupManagerRepository) finalizeMatchingRecheckMovedOutputsTx(
	ctx context.Context,
	tx bun.Tx,
	recheckID uuid.UUID,
	expected []matchingRecheckMovedOutput,
	proofs []*ObsoletePublicationOutputCleanupAdmission,
) error {
	if len(expected) == 0 || len(expected) != len(proofs) {
		return ErrPublicationAdmissionConflict
	}
	proofByOperation := make(map[uuid.UUID]*ObsoletePublicationOutputCleanupAdmission, len(expected))
	expectedByOperation := make(map[uuid.UUID]matchingRecheckMovedOutput, len(expected))
	for index, output := range expected {
		if output.member.OperationID == uuid.Nil {
			return ErrPublicationAdmissionConflict
		}
		if _, exists := proofByOperation[output.member.OperationID]; exists {
			return ErrPublicationAdmissionConflict
		}
		proofByOperation[output.member.OperationID] = proofs[index]
		expectedByOperation[output.member.OperationID] = output
	}

	// Lock order: shared output admission, source mutation gate, output epoch.
	// The retired predecessor output still belongs to the current output epoch.
	if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
		return fmt.Errorf("finalize matching recheck moved outputs: lock output admission gate: %w", err)
	}
	if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
		return fmt.Errorf("finalize matching recheck moved outputs: lock source mutation gate: %w", err)
	}
	var epoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
		return fmt.Errorf("finalize matching recheck moved outputs: lock output epoch: %w", err)
	}

	var state string
	err := tx.NewRaw(`SELECT state FROM matching_local_recheck WHERE id=? FOR UPDATE`, recheckID).Scan(ctx, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPublicationAdmissionConflict
	}
	if err != nil {
		return fmt.Errorf("finalize matching recheck moved outputs: lock recheck: %w", err)
	}
	if state != "publishing" {
		return ErrPublicationAdmissionConflict
	}
	var pending bool
	if err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM matching_local_recheck_publication WHERE recheck_id=? AND committed_at IS NULL
	)`, recheckID).Scan(ctx, &pending); err != nil {
		return fmt.Errorf("finalize matching recheck moved outputs: read receipts: %w", err)
	}
	if pending {
		return ErrPublicationAdmissionConflict
	}
	members := make([]publicationMatchingRecheckMember, 0, len(expected))
	if err := tx.NewRaw(`SELECT recheck_id,operation_id,output_epoch,position_id,candidate_generation,
			predecessor_position_id,predecessor_generation,predecessor_claim_id,same_path,predecessor_snapshot,committed_at
		FROM matching_local_recheck_publication
		WHERE recheck_id=? AND NOT same_path
		ORDER BY operation_id FOR UPDATE`, recheckID).Scan(ctx, &members); err != nil {
		return fmt.Errorf("finalize matching recheck moved outputs: lock receipts: %w", err)
	}
	if len(members) != len(expected) {
		return ErrPublicationAdmissionConflict
	}

	plan := make([]matchingRecheckMovedCleanup, 0, len(members))
	for index := range members {
		member := &members[index]
		proof, exists := proofByOperation[member.OperationID]
		if !exists || member.CommittedAt == nil || member.SamePath || member.OutputEpoch != epoch {
			return ErrPublicationAdmissionConflict
		}
		current, witness, err := lockMatchingRecheckMovedCurrentTx(ctx, tx, member)
		if err != nil {
			return err
		}
		retire, err := matchingRecheckPredecessorCanRetireTx(ctx, tx, member, current)
		if err != nil {
			return err
		}
		if !retire {
			// Definitive no-retirement on complete success.
			plan = append(plan, matchingRecheckMovedCleanup{member: *member, current: current})
			continue
		}
		if proof == nil {
			// Protection changed since the preflight proved it safe, so this tick
			// has no native evidence. Keep CURRENT and retry with proof next tick.
			return ErrPublicationAdmissionConflict
		}
		if err := validateMatchingRecheckMovedOutputProof(member, current, witness, proof); err != nil {
			return err
		}
		if err := lockMatchingRecheckMovedDestinationTx(ctx, tx, member, current, expectedByOperation[member.OperationID]); err != nil {
			return err
		}
		plan = append(plan, matchingRecheckMovedCleanup{member: *member, current: current, proof: proof})
	}

	// Every receipt of the recheck passed the complete-success proof. A proved
	// receipt retires its exact predecessor publication, owned associations and
	// output cleanup authority; a protected receipt keeps CURRENT untouched.
	for _, cleanup := range plan {
		if cleanup.proof == nil {
			continue
		}
		if err := removeMatchingRecheckPredecessorCurrentTx(ctx, tx, cleanup.current); err != nil {
			return err
		}
		// The association helper is authorized only after
		// matchingRecheckPredecessorCanRetireTx attested the exact owned sources,
		// no manual authority, no shared track and no live shared recheck.
		if err := retireMatchingRecheckMovedAssociationTx(ctx, tx, &cleanup.member, cleanup.current); err != nil {
			return err
		}
		if _, err := repository.AdmitObsoletePublicationOutputCleanupTx(ctx, tx, *cleanup.proof, repository.riverInserter,
			func(id uuid.UUID) river.JobArgs { return ObsoletePublicationOutputCleanupJobArgs{OperationID: id} }, nil); err != nil {
			return fmt.Errorf("finalize matching recheck moved outputs: %w", err)
		}
	}
	// All moved receipts are consumed by this decision; the existing close path
	// then releases the recheck and its source veto exactly once, for both
	// retired and protected predecessors.
	if _, err := tx.NewRaw(`DELETE FROM matching_local_recheck_publication WHERE recheck_id=? AND NOT same_path`, recheckID).Exec(ctx); err != nil {
		return fmt.Errorf("finalize matching recheck moved outputs: consume moved receipts: %w", err)
	}
	if err := closeMatchingRecheckAfterPublicationReceiptsTx(ctx, tx, recheckID); err != nil {
		return err
	}
	return nil
}

// validateMatchingRecheckMovedOutputProof requires the native evidence returned
// by capture to describe the exact predecessor CURRENT and claim, including the
// frozen owned bytes.
func validateMatchingRecheckMovedOutputProof(
	member *publicationMatchingRecheckMember,
	current TrackPublication,
	witness matchingRecheckPublicationPredecessorWitness,
	proof *ObsoletePublicationOutputCleanupAdmission,
) error {
	if member.SamePath || member.PredecessorPositionID == nil || member.PredecessorGeneration == nil ||
		member.PredecessorClaimID == nil || proof == nil {
		return ErrPublicationAdmissionConflict
	}
	if current.PositionID != *member.PredecessorPositionID || current.Epoch != member.OutputEpoch ||
		current.Generation != *member.PredecessorGeneration || current.RelativePath != witness.RelativePath {
		return ErrPublicationAdmissionConflict
	}
	ownership := witness.Current.Ownership
	if ownership.SizeBytes == nil || *ownership.SizeBytes < 0 || ownership.MTime == nil {
		return ErrPublicationAdmissionConflict
	}
	digest, err := hex.DecodeString(ownership.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return ErrPublicationAdmissionConflict
	}
	if proof.Epoch != current.Epoch || proof.PositionID != current.PositionID ||
		proof.Generation != current.Generation || proof.NamespaceClaimID != *member.PredecessorClaimID ||
		proof.RelativePath != current.RelativePath || !filepath.IsAbs(proof.NativePath) ||
		proof.FileSize != *ownership.SizeBytes || proof.ModifiedAt.UnixMicro() != *ownership.MTime ||
		!bytes.Equal(proof.SHA256, digest) ||
		!validCleanupDirectoryIdentity(proof.OutputRootIdentity) ||
		!validCleanupDirectoryIdentity(proof.ParentIdentity) ||
		proof.OutputRootIdentity.Platform != proof.ParentIdentity.Platform {
		return ErrPublicationAdmissionConflict
	}
	return nil
}

// lockMatchingRecheckMovedDestinationTx requires the committed publication that
// moved the member to remain the destination CURRENT. If it was replaced or
// removed, retiring the old output could discard the only remaining copy.
func lockMatchingRecheckMovedDestinationTx(
	ctx context.Context, tx bun.Tx, member *publicationMatchingRecheckMember, predecessor TrackPublication, checked matchingRecheckMovedOutput,
) error {
	var destination TrackPublication
	err := tx.NewSelect().Model(&destination).Where("position_id=?", member.PositionID).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPublicationAdmissionConflict
	}
	if err != nil {
		return fmt.Errorf("lock matching recheck moved destination: %w", err)
	}
	if destination.PositionID != member.PositionID || destination.Epoch != member.OutputEpoch ||
		destination.Generation != member.CandidateGeneration || destination.RelativePath == "" ||
		destination.RelativePath == predecessor.RelativePath || destination.RelativePath != checked.destination.RelativePath ||
		!sameJSONValue(destination.Ownership, checked.destination.Ownership) {
		return ErrPublicationAdmissionConflict
	}
	claimID, err := matchingRecheckMovedDestinationClaim(ctx, tx, destination)
	if errors.Is(err, sql.ErrNoRows) || err == nil && claimID != checked.destinationClaimID {
		return ErrPublicationAdmissionConflict
	}
	return err
}

func matchingRecheckMovedDestinationClaim(ctx context.Context, tx bun.Tx, destination TrackPublication) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.NewRaw(`SELECT c.id FROM publication_namespace_claim c
		JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
		WHERE c.owner_position_id=? AND c.epoch=? AND c.relative_path=? AND c.node_kind='file'
			AND r.position_id=? AND r.generation=?`, destination.PositionID, destination.Epoch,
		destination.RelativePath, destination.PositionID, destination.Generation).Scan(ctx, &id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("read matching recheck destination file claim: %w", err)
	}
	return id, err
}
