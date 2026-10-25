package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func validatePublicationProviderTagAuthorityTx(ctx context.Context, tx bun.Tx, releaseID, positionID uuid.UUID, input PublicationInputs) error {
	if !validProviderTagRewrite(input) {
		return fmt.Errorf("validate provider-tag authority: invalid frozen contract: %w", ErrPublicationAdmissionConflict)
	}
	provider := input.ProviderTags
	var snapshot *PublicationProviderTagContext
	if err := readPublicationProviderTagContextForPositionTx(ctx, tx, releaseID, positionID, provider.Endpoint, provider.ConfigurationIdentity, true, &snapshot); err != nil {
		return fmt.Errorf("validate provider-tag authority: read accepted context: %w", err)
	}
	protection, err := BuildPublicationProviderTagProtection(snapshot)
	if err != nil {
		return fmt.Errorf("validate provider-tag authority: read protection: %w", err)
	}
	patch, err := BuildPublicationProviderCanonicalTagPatch(*snapshot)
	if err != nil {
		return fmt.Errorf("validate provider-tag authority: project accepted fields: %w", err)
	}
	if snapshot.Source.ID != provider.ProviderSourceID || snapshot.Digest != provider.AuthorityRevision ||
		!reflect.DeepEqual(patch, provider.Patch) || !reflect.DeepEqual(protection.Keys, provider.ProtectedKeys) {
		return fmt.Errorf("validate provider-tag authority: accepted evidence or protection changed: %w", ErrPublicationAdmissionConflict)
	}
	return nil
}

// lockProviderTagDeliveryAuthority acquires the provider/owner lock prefix before
// normal publication-domain locks. The unlocked selector grants no delivery
// authority: callers subsequently re-read and validate the complete claim.
func lockProviderTagDeliveryAuthority(ctx context.Context, tx bun.Tx, guard PublicationDeliveryGuard) error {
	var selector struct {
		Frozen    string    `bun:"frozen"`
		ReleaseID uuid.UUID `bun:"release_id,type:uuid"`
	}
	err := tx.NewRaw(`SELECT attempt.frozen_inputs::text AS frozen, position.release_id
		FROM track_publication_attempt attempt
		JOIN release_track position ON position.id=attempt.position_id
		WHERE attempt.operation_id=? AND attempt.position_id=? AND attempt.epoch=? AND attempt.candidate_generation=?`,
		guard.OperationID, guard.ExpectedPositionID, guard.Epoch, guard.CandidateGeneration).Scan(ctx, &selector)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read provider-tag delivery selector: %w", err)
	}
	input, err := DecodePublicationInputs([]byte(selector.Frozen))
	if err != nil {
		return fmt.Errorf("read provider-tag delivery contract: %w", err)
	}
	if input.ProviderTags == nil {
		return nil
	}
	return validatePublicationProviderTagAuthorityTx(ctx, tx, selector.ReleaseID, guard.ExpectedPositionID, input)
}
