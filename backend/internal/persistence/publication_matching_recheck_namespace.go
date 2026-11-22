package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// An unsettled move owns its predecessor and the destination whose exact
// committed generation must still be present when the batch barrier opens.
// Call under the output epoch lock (or the exclusive admission gate).
func matchingRecheckPublicationUsesPosition(ctx context.Context, tx bun.IDB, epoch uuid.UUID, positions ...uuid.UUID) (bool, error) {
	if len(positions) == 0 {
		return false, nil
	}
	var used bool
	err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_local_recheck_publication
		WHERE output_epoch=? AND (
			(predecessor_position_id IN (?) AND (committed_at IS NULL OR NOT same_path))
			OR position_id IN (?)
		))`, epoch, bun.List(positions), bun.List(positions)).Scan(ctx, &used)
	if err != nil {
		return false, fmt.Errorf("read matching recheck publication position ownership: %w", err)
	}
	return used, nil
}

// matchingRecheckNamespaceAdoption is the exact predecessor output one
// cross-position matching-recheck member replaces at the same native path.
//
// The grant is deliberately narrow: it authorizes only the claim that still
// owns that exact native path, only while that claim has exactly one live
// consumer (the predecessor generation), and only for a destination position
// that has no CURRENT yet. A different destination path is not an adoption; it
// creates its own claim through the ordinary namespace planning path.
type matchingRecheckNamespaceAdoption struct {
	PredecessorPositionID uuid.UUID
	PredecessorGeneration uuid.UUID
	PredecessorClaimID    uuid.UUID
	CanonicalKey          string
	RelativePath          string
}

// matchingRecheckNamespaceConsumer names the destination reservation the
// adoption is allowed to add. It is nil while admission still has to insert the
// reservation and non-nil when an already admitted attempt is revalidated.
type matchingRecheckNamespaceConsumer struct {
	OperationID uuid.UUID
	PositionID  uuid.UUID
	Generation  uuid.UUID
}

// grantMatchingRecheckNamespaceAdoption adds the private cross-position
// namespace capability to one first-install admission. Generic admission has no
// exported way to set it: only the matching-recheck bridge, after proving the
// member and its predecessor from the frozen layout, may call this method.
func (request *PublicationAdmission) grantMatchingRecheckNamespaceAdoption(adoption matchingRecheckNamespaceAdoption) error {
	if request == nil {
		return errors.New("grant matching recheck namespace adoption: admission is missing")
	}
	if adoption.PredecessorPositionID == uuid.Nil || adoption.PredecessorGeneration == uuid.Nil ||
		adoption.PredecessorClaimID == uuid.Nil || !portableRelativePath(adoption.CanonicalKey) ||
		!portableRelativePath(adoption.RelativePath) {
		return fmt.Errorf("grant matching recheck namespace adoption: predecessor identity is incomplete: %w", ErrPublicationAdmissionConflict)
	}
	if request.PositionID == uuid.Nil || request.PositionID == adoption.PredecessorPositionID ||
		request.ExpectedEpoch == uuid.Nil || request.CandidateGeneration == uuid.Nil ||
		request.IntentKind != "audio" || request.ExpectedGeneration != nil {
		return fmt.Errorf("grant matching recheck namespace adoption: destination is not a first install: %w", ErrPublicationAdmissionConflict)
	}
	// A same-path adoption carries exactly one file target. The predecessor's
	// bytes can only replace that target, so no second file node may hide behind
	// the grant.
	files := 0
	var target PublicationNamespaceNode
	for _, node := range request.NamespaceManifest {
		if node.NodeKind != "file" {
			continue
		}
		files++
		target = node
	}
	if files != 1 || target.CanonicalKey != adoption.CanonicalKey || target.RelativePath != adoption.RelativePath {
		return fmt.Errorf("grant matching recheck namespace adoption: manifest target differs from the predecessor path: %w", ErrPublicationAdmissionConflict)
	}
	grant := adoption
	request.namespaceAdoption = &grant
	return nil
}

// requireMatchingRecheckNamespacePredecessor records the exact predecessor claim
// a different-path cross member consumes. It grants no adoption: the destination
// plans its own path, and the predecessor is only fenced against a competing
// consumer. Same-path members use grantMatchingRecheckNamespaceAdoption.
func (request *PublicationAdmission) requireMatchingRecheckNamespacePredecessor(predecessor matchingRecheckNamespaceAdoption) error {
	if request == nil {
		return errors.New("require matching recheck namespace predecessor: admission is missing")
	}
	if predecessor.PredecessorPositionID == uuid.Nil || predecessor.PredecessorGeneration == uuid.Nil ||
		predecessor.PredecessorClaimID == uuid.Nil || !portableRelativePath(predecessor.CanonicalKey) ||
		!portableRelativePath(predecessor.RelativePath) || request.PositionID == uuid.Nil ||
		request.PositionID == predecessor.PredecessorPositionID || request.ExpectedEpoch == uuid.Nil ||
		request.IntentKind != "audio" || request.namespaceAdoption != nil || request.namespacePredecessor != nil {
		return fmt.Errorf("require matching recheck namespace predecessor: invalid cross-position member: %w", ErrPublicationAdmissionConflict)
	}
	copy := predecessor
	request.namespacePredecessor = &copy
	return nil
}

// validatePublicationNamespaceAdoptionTx proves that one existing file claim is
// exactly the predecessor named by the adoption grant. The caller has already
// locked the epoch's claims and reused this claim for the destination node.
func validatePublicationNamespaceAdoptionTx(
	ctx context.Context, tx bun.Tx, request PublicationAdmission, claim PublicationNamespaceClaim, node PublicationNamespaceNode,
) error {
	grant := request.namespaceAdoption
	if grant == nil {
		return fmt.Errorf("admit publication: namespace adoption is not authorized: %w", ErrPublicationAdmissionConflict)
	}
	if request.ExpectedGeneration != nil || node.NodeKind != "file" ||
		node.CanonicalKey != grant.CanonicalKey || node.RelativePath != grant.RelativePath {
		return fmt.Errorf("admit publication: namespace adoption target differs from the grant: %w", ErrPublicationAdmissionConflict)
	}
	return validateMatchingRecheckNamespacePredecessorStateTx(ctx, tx, claim, *grant, &node, nil)
}

// validateMatchingRecheckNamespacePredecessorStateTx validates the durable
// predecessor side of a same-path adoption. It accepts exactly two reservation
// shapes: the predecessor reservation alone (before admission) or that
// reservation plus the named destination candidate (while the admitted attempt
// is revalidated). Any other consumer, cleanup authority, or recheck member
// prevents adoption instead of being overwritten.
func validateMatchingRecheckNamespacePredecessorStateTx(
	ctx context.Context, tx bun.Tx, claim PublicationNamespaceClaim,
	adoption matchingRecheckNamespaceAdoption, node *PublicationNamespaceNode, consumer *matchingRecheckNamespaceConsumer,
) error {
	digest := sha256.Sum256([]byte(claim.CanonicalKey))
	if claim.ID != adoption.PredecessorClaimID || claim.Epoch == uuid.Nil || claim.NodeKind != "file" ||
		claim.OwnerPositionID == nil || *claim.OwnerPositionID != adoption.PredecessorPositionID ||
		claim.DirectoryScopeKey != nil || string(claim.KeyDigest) != string(digest[:]) ||
		claim.RelativePath != adoption.RelativePath ||
		(adoption.CanonicalKey != "" && claim.CanonicalKey != adoption.CanonicalKey) ||
		(node != nil && (node.NodeKind != "file" || node.CanonicalKey != claim.CanonicalKey ||
			node.RelativePath != claim.RelativePath)) {
		return fmt.Errorf("admit publication: predecessor claim differs from the adoption grant: %w", ErrPublicationAdmissionConflict)
	}

	var cleanupOwned bool
	if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM obsolete_publication_output_cleanup WHERE namespace_claim_id=?
		)`, claim.ID).Scan(ctx, &cleanupOwned); err != nil {
		return fmt.Errorf("admit publication: inspect predecessor cleanup authority: %w", err)
	}
	if cleanupOwned {
		return fmt.Errorf("admit publication: predecessor namespace is owned by obsolete output cleanup: %w", ErrPublicationAdmissionConflict)
	}

	rows := make([]struct {
		PositionID uuid.UUID `bun:"position_id"`
		Generation uuid.UUID `bun:"generation"`
	}, 0, 2)
	if err := tx.NewRaw(`SELECT position_id,generation FROM publication_namespace_reservation
			WHERE namespace_claim_id=? ORDER BY position_id,generation`, claim.ID).Scan(ctx, &rows); err != nil {
		return fmt.Errorf("admit publication: read predecessor reservations: %w", err)
	}
	expected := 1
	if consumer != nil {
		expected = 2
	}
	if len(rows) != expected {
		return fmt.Errorf("admit publication: predecessor claim has another consumer: %w", ErrPublicationAdmissionConflict)
	}
	reserved := make(map[[2]uuid.UUID]bool, len(rows))
	for _, row := range rows {
		reserved[[2]uuid.UUID{row.PositionID, row.Generation}] = true
	}
	if !reserved[[2]uuid.UUID{adoption.PredecessorPositionID, adoption.PredecessorGeneration}] {
		return fmt.Errorf("admit publication: predecessor reservation is not live: %w", ErrPublicationAdmissionConflict)
	}
	if consumer != nil && !reserved[[2]uuid.UUID{consumer.PositionID, consumer.Generation}] {
		return fmt.Errorf("admit publication: destination reservation is missing: %w", ErrPublicationAdmissionConflict)
	}

	var current bool
	if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM track_publication
			WHERE position_id=? AND epoch=? AND generation=? AND relative_path=?)`,
		adoption.PredecessorPositionID, claim.Epoch, adoption.PredecessorGeneration,
		claim.RelativePath).Scan(ctx, &current); err != nil {
		return fmt.Errorf("admit publication: read predecessor CURRENT: %w", err)
	}
	if !current {
		return fmt.Errorf("admit publication: predecessor output is no longer CURRENT: %w", ErrPublicationAdmissionConflict)
	}

	// An open cross-member recheck already owns this predecessor claim, and a
	// committed different-path member has already moved the predecessor output
	// away from it. Both refuse a fresh same-path adoption.
	exclude := uuid.Nil
	if consumer != nil {
		exclude = consumer.OperationID
	}
	var competing bool
	if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM matching_local_recheck_publication
			WHERE predecessor_claim_id=? AND (committed_at IS NULL OR same_path IS FALSE)
				AND operation_id<>?)`, claim.ID, exclude).Scan(ctx, &competing); err != nil {
		return fmt.Errorf("admit publication: inspect matching recheck predecessor consumers: %w", err)
	}
	if competing {
		return fmt.Errorf("admit publication: predecessor claim already has another matching recheck consumer: %w", ErrPublicationAdmissionConflict)
	}
	return nil
}

// validateMatchingRecheckNamespacePredecessorAvailabilityTx locks and validates
// the predecessor claim of a different-path member. It proves the claim is not
// already consumed by an open cross member or a committed different-path move.
func validateMatchingRecheckNamespacePredecessorAvailabilityTx(
	ctx context.Context, tx bun.Tx, request PublicationAdmission,
) error {
	predecessor := request.namespacePredecessor
	if predecessor == nil || request.ExpectedEpoch == uuid.Nil {
		return fmt.Errorf("admit publication: matching recheck predecessor is missing: %w", ErrPublicationAdmissionConflict)
	}
	var claim PublicationNamespaceClaim
	if err := tx.NewSelect().Model(&claim).Where("id=? AND epoch=?", predecessor.PredecessorClaimID, request.ExpectedEpoch).For("UPDATE").Scan(ctx, &claim); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("admit publication: predecessor claim is missing: %w", ErrPublicationAdmissionConflict)
		}
		return fmt.Errorf("admit publication: lock predecessor claim: %w", err)
	}
	return validateMatchingRecheckNamespacePredecessorStateTx(ctx, tx, claim, *predecessor, nil, nil)
}

// publicationMatchingRecheckCrossPositionNamespaceAdoption is the pre-lock check
// that a replacement guard belongs to a same-path cross-position member. The
// caller must take the source mutation gate before the output epoch when this is
// true, because the mounted predecessor CURRENT and its source authority are
// about to be retired.
func publicationMatchingRecheckCrossPositionNamespaceAdoption(ctx context.Context, tx bun.Tx, operationID uuid.UUID) (bool, error) {
	if operationID == uuid.Nil {
		return false, nil
	}
	var cross bool
	if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM matching_local_recheck_publication
			WHERE operation_id=? AND same_path IS TRUE AND predecessor_position_id IS NOT NULL
				AND predecessor_position_id<>position_id)`, operationID).Scan(ctx, &cross); err != nil {
		return false, fmt.Errorf("read matching recheck cross-position membership: %w", err)
	}
	return cross, nil
}

// validateMatchingRecheckNamespaceReplacementTx authorizes replacing the
// predecessor's native file at the same path. The predecessor CURRENT must still
// be retirable by this exact member before any mutation: manual or foreign
// source authority fails closed rather than being overwritten by a recheck.
func validateMatchingRecheckNamespaceReplacementTx(
	ctx context.Context, tx bun.Tx, attempt *TrackPublicationAttempt, member *publicationMatchingRecheckMember, relativePath string,
) error {
	if attempt == nil || member == nil || member.OperationID != attempt.OperationID ||
		member.OutputEpoch != attempt.Epoch || member.PositionID != attempt.PositionID ||
		member.CandidateGeneration != attempt.CandidateGeneration || member.CommittedAt != nil ||
		!member.SamePath || member.PredecessorPositionID == nil || member.PredecessorGeneration == nil ||
		member.PredecessorClaimID == nil || *member.PredecessorPositionID == member.PositionID ||
		attempt.ExpectedGeneration != nil {
		return fmt.Errorf("check publication target replacement: cross-position namespace receipt is not an install: %w", ErrPublicationAdmissionConflict)
	}
	previous, witness, err := lockMatchingRecheckMovedCurrentTx(ctx, tx, member)
	if err != nil {
		return fmt.Errorf("check publication target replacement: lock predecessor CURRENT: %w", err)
	}
	canRetire, err := matchingRecheckPredecessorCanRetireTx(ctx, tx, member, previous)
	if err != nil {
		return fmt.Errorf("check publication target replacement: prove predecessor retirement: %w", err)
	}
	if !canRetire {
		return fmt.Errorf("check publication target replacement: predecessor CURRENT cannot be retired: %w", ErrPublicationAdmissionConflict)
	}
	adoption := matchingRecheckNamespaceAdoption{
		PredecessorPositionID: *member.PredecessorPositionID,
		PredecessorGeneration: *member.PredecessorGeneration,
		PredecessorClaimID:    *member.PredecessorClaimID,
		RelativePath:          witness.RelativePath,
	}
	if previous.Generation != adoption.PredecessorGeneration || previous.PositionID != adoption.PredecessorPositionID ||
		previous.RelativePath != adoption.RelativePath || witness.Version != 1 ||
		witness.PositionID != adoption.PredecessorPositionID || witness.OutputEpoch != member.OutputEpoch ||
		witness.Generation != adoption.PredecessorGeneration || witness.NamespaceClaimID != adoption.PredecessorClaimID ||
		witness.RelativePath != relativePath || witness.Current.Generation != adoption.PredecessorGeneration ||
		witness.Current.RelativePath != adoption.RelativePath {
		return fmt.Errorf("check publication target replacement: predecessor witness differs from the adoption: %w", ErrPublicationAdmissionConflict)
	}
	var claim PublicationNamespaceClaim
	if err := tx.NewSelect().Model(&claim).Where("id=? AND epoch=?", adoption.PredecessorClaimID, attempt.Epoch).For("UPDATE").Scan(ctx, &claim); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check publication target replacement: predecessor claim is missing: %w", ErrPublicationAdmissionConflict)
		}
		return fmt.Errorf("check publication target replacement: lock predecessor claim: %w", err)
	}
	consumer := &matchingRecheckNamespaceConsumer{
		OperationID: attempt.OperationID,
		PositionID:  attempt.PositionID,
		Generation:  attempt.CandidateGeneration,
	}
	if err := validateMatchingRecheckNamespacePredecessorStateTx(ctx, tx, claim, adoption, nil, consumer); err != nil {
		return err
	}
	return nil
}

// readMatchingRecheckNamespaceMember is the lock-free counterpart used while a
// rewrite namespace is only being verified. It cannot introduce a
// receipt-before-claim lock order into preparation.
func readMatchingRecheckNamespaceMember(
	ctx context.Context, tx bun.Tx, operationID uuid.UUID,
) (*publicationMatchingRecheckMember, error) {
	if operationID == uuid.Nil {
		return nil, nil
	}
	members := make([]publicationMatchingRecheckMember, 0, 1)
	if err := tx.NewRaw(`SELECT recheck_id,operation_id,output_epoch,position_id,candidate_generation,
			predecessor_position_id,predecessor_generation,predecessor_claim_id,same_path,predecessor_snapshot,committed_at
		FROM matching_local_recheck_publication WHERE operation_id=?`, operationID).Scan(ctx, &members); err != nil {
		return nil, fmt.Errorf("read matching recheck namespace member: %w", err)
	}
	switch len(members) {
	case 0:
		return nil, nil
	case 1:
		return &members[0], nil
	default:
		return nil, fmt.Errorf("read matching recheck namespace member: operation has %d receipts: %w", len(members), ErrPublicationAdmissionConflict)
	}
}

// publicationNamespaceAdoptionProvesClaim recognizes the already admitted
// same-path adoption while a rewrite namespace is revalidated. A cross-position
// install has no ExpectedGeneration, so this can never widen an ordinary
// same-position rewrite.
func publicationNamespaceAdoptionProvesClaim(
	ctx context.Context, tx bun.Tx, attempt *TrackPublicationAttempt, claim PublicationNamespaceClaim,
) (bool, error) {
	if attempt == nil || attempt.OperationID == uuid.Nil || attempt.ExpectedGeneration != nil {
		return false, nil
	}
	member, err := readMatchingRecheckNamespaceMember(ctx, tx, attempt.OperationID)
	if err != nil {
		return false, err
	}
	adoption, ok := matchingRecheckNamespaceAdoptionFromMember(member)
	if !ok || adoption.PredecessorClaimID != claim.ID || claim.OwnerPositionID == nil ||
		*claim.OwnerPositionID != adoption.PredecessorPositionID {
		return false, nil
	}
	consumer := matchingRecheckNamespaceConsumer{
		OperationID: attempt.OperationID,
		PositionID:  attempt.PositionID,
		Generation:  attempt.CandidateGeneration,
	}
	if err := validateMatchingRecheckNamespacePredecessorStateTx(ctx, tx, claim, adoption, nil, &consumer); err != nil {
		return false, err
	}
	return true, nil
}

// matchingRecheckNamespaceAdoptionFromMember projects the durable receipt into
// the adoption identity. The frozen witness is the receipt's own evidence, so
// the projection cannot be widened by a caller-supplied path.
func matchingRecheckNamespaceAdoptionFromMember(member *publicationMatchingRecheckMember) (matchingRecheckNamespaceAdoption, bool) {
	if member == nil || !member.SamePath || member.PredecessorPositionID == nil ||
		member.PredecessorGeneration == nil || member.PredecessorClaimID == nil ||
		*member.PredecessorPositionID == uuid.Nil || *member.PredecessorGeneration == uuid.Nil ||
		*member.PredecessorClaimID == uuid.Nil || *member.PredecessorPositionID == member.PositionID {
		return matchingRecheckNamespaceAdoption{}, false
	}
	var witness matchingRecheckPublicationPredecessorWitness
	if decodeStrictJSON(member.PredecessorSnapshot, &witness) != nil || witness.Version != 1 ||
		witness.PositionID != *member.PredecessorPositionID || witness.OutputEpoch != member.OutputEpoch ||
		witness.Generation != *member.PredecessorGeneration || witness.NamespaceClaimID != *member.PredecessorClaimID ||
		witness.RelativePath == "" || witness.Current.Generation != witness.Generation ||
		witness.Current.RelativePath != witness.RelativePath {
		return matchingRecheckNamespaceAdoption{}, false
	}
	return matchingRecheckNamespaceAdoption{
		PredecessorPositionID: *member.PredecessorPositionID,
		PredecessorGeneration: *member.PredecessorGeneration,
		PredecessorClaimID:    *member.PredecessorClaimID,
		RelativePath:          witness.RelativePath,
	}, true
}
