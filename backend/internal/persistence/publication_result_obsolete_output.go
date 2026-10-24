package persistence

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationObsoleteOutputCandidate binds the locked CURRENT publication to
// the exact namespace claim needed to prepare cleanup before filesystem install.
type PublicationObsoleteOutputCandidate struct {
	Publication      TrackPublication
	NamespaceClaimID uuid.UUID
}

// ReadPublicationObsoleteOutputCandidate reads old CURRENT and its file claim
// while verifying the delivery guard. It performs no filesystem I/O.
func (repository *SetupManagerRepository) ReadPublicationObsoleteOutputCandidate(ctx context.Context, guard PublicationDeliveryGuard, nextRelativePath string) (*PublicationObsoleteOutputCandidate, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return nil, err
	}
	if !frozenPublicationRelativePath(nextRelativePath) {
		return nil, errors.New("read publication obsolete output candidate: invalid next path")
	}
	var candidate *PublicationObsoleteOutputCandidate
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, _, claim, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "read publication obsolete output candidate")
		if err != nil || !live || claim.State != "executing" || attempt.ExpectedGeneration == nil {
			return err
		}
		if nextRelativePath == "" {
			return nil
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
			return fmt.Errorf("read publication obsolete output candidate: lock current publication: %w", err)
		}
		if current.Generation != *attempt.ExpectedGeneration || current.RelativePath == nextRelativePath {
			return nil
		}
		var namespaceClaim PublicationNamespaceClaim
		if err := tx.NewSelect().Model(&namespaceClaim).Where("epoch=? AND relative_path=? AND node_kind='file' AND owner_position_id=?", current.Epoch, current.RelativePath, current.PositionID).Scan(ctx); err != nil {
			return fmt.Errorf("read publication obsolete output candidate: read file claim: %w", err)
		}
		var reserved bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM publication_namespace_reservation WHERE namespace_claim_id=? AND position_id=? AND generation=?)`, namespaceClaim.ID, current.PositionID, current.Generation).Scan(ctx, &reserved); err != nil {
			return fmt.Errorf("read publication obsolete output candidate: check reservation: %w", err)
		}
		if !reserved {
			return nil
		}
		candidate = &PublicationObsoleteOutputCandidate{Publication: current, NamespaceClaimID: namespaceClaim.ID}
		return nil
	})
	return candidate, err
}

func validateObsoletePublicationOutputCleanup(proof *ObsoletePublicationOutputCleanupAdmission, previous *TrackPublication, attempt *TrackPublicationAttempt, nextRelativePath string) error {
	if previous == nil || previous.RelativePath == nextRelativePath {
		if proof != nil {
			return errors.New("commit publication result: unexpected obsolete output cleanup proof")
		}
		return nil
	}
	if proof == nil {
		return errors.New("commit publication result: superseded output cleanup proof is required")
	}
	var ownership PublicationCurrentOwnership
	if err := decodeStrictJSON(previous.Ownership, &ownership); err != nil || !validInstalledPublicationOwnership(ownership) {
		return errors.New("commit publication result: current output ownership is invalid")
	}
	identity := ownership.Identity
	digest, err := hex.DecodeString(identity.SHA256)
	if err != nil || identity.SizeBytes == nil || identity.MTime == nil ||
		proof.Epoch != previous.Epoch || proof.PositionID != previous.PositionID || proof.Generation != previous.Generation ||
		proof.RelativePath != previous.RelativePath || proof.FileSize != *identity.SizeBytes || proof.ModifiedAt.UnixMicro() != *identity.MTime ||
		proof.NamespaceClaimID == uuid.Nil || len(digest) != len(proof.SHA256) || !equalBytes(digest, proof.SHA256) {
		return errors.New("commit publication result: obsolete output cleanup proof does not match CURRENT")
	}
	if attempt == nil || proof.PositionID != attempt.PositionID || proof.Generation == attempt.CandidateGeneration {
		return errors.New("commit publication result: obsolete output cleanup proof has invalid generation")
	}
	return nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
