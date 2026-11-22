package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// publicationMatchingRecheckMember is one durable publication receipt for a
// Local-to-MusicBrainz recheck. The row is an ownership witness: it binds one
// exact publication operation to the predecessor it may replace.
type publicationMatchingRecheckMember struct {
	RecheckID              uuid.UUID       `bun:"recheck_id"`
	OperationID            uuid.UUID       `bun:"operation_id"`
	OutputEpoch            uuid.UUID       `bun:"output_epoch"`
	PositionID             uuid.UUID       `bun:"position_id"`
	CandidateGeneration    uuid.UUID       `bun:"candidate_generation"`
	PredecessorPositionID  *uuid.UUID      `bun:"predecessor_position_id"`
	PredecessorGeneration  *uuid.UUID      `bun:"predecessor_generation"`
	PredecessorClaimID     *uuid.UUID      `bun:"predecessor_claim_id"`
	SamePath               bool            `bun:"same_path"`
	PredecessorSnapshot    json.RawMessage `bun:"predecessor_snapshot"`
	CommittedAt            *time.Time      `bun:"committed_at"`
	CandidatePayloadSHA256 *string         `bun:"candidate_payload_sha256"`
}

// removeMatchingRecheckPredecessorCurrentTx removes only the generation named
// by the publishing recheck. Selection and assignment retirement is a
// separate operation: the publication projection is not source authority.
func removeMatchingRecheckPredecessorCurrentTx(ctx context.Context, tx bun.Tx, current TrackPublication) error {
	if _, err := tx.NewRaw(`DELETE FROM position_source_authority
		WHERE position_id=? AND publication_epoch=? AND publication_generation=?`,
		current.PositionID, current.Epoch, current.Generation).Exec(ctx); err != nil {
		return fmt.Errorf("retire matching predecessor source authority: %w", err)
	}
	result, err := tx.NewRaw(`DELETE FROM track_publication WHERE position_id=? AND epoch=? AND generation=?`,
		current.PositionID, current.Epoch, current.Generation).Exec(ctx)
	if err != nil {
		return fmt.Errorf("retire matching predecessor current: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count matching predecessor current retirement: %w", err)
	}
	if count != 1 {
		return ErrPublicationAdmissionConflict
	}
	return nil
}

// transferMatchingRecheckFileClaimTx does not touch directory scope claims.
// It is called only by result settlement, never by preparation or native
// install. The destination reservation was written while the old owner was
// still CURRENT and survives this transaction.
func transferMatchingRecheckFileClaimTx(
	ctx context.Context, tx bun.Tx, member publicationMatchingRecheckMember, previous TrackPublication,
) error {
	if !member.SamePath || member.PredecessorClaimID == nil || member.PositionID == previous.PositionID ||
		member.OutputEpoch != previous.Epoch {
		return ErrPublicationAdmissionConflict
	}
	result, err := tx.NewRaw(`UPDATE publication_namespace_claim c SET owner_position_id=?
		WHERE c.id=? AND c.epoch=? AND c.owner_position_id=? AND c.node_kind='file' AND c.relative_path=?
		AND EXISTS (
			SELECT 1 FROM publication_namespace_reservation r
			WHERE r.namespace_claim_id=c.id AND r.position_id=? AND r.generation=?
		)`, member.PositionID, *member.PredecessorClaimID, previous.Epoch, previous.PositionID,
		previous.RelativePath, member.PositionID, member.CandidateGeneration).Exec(ctx)
	if err != nil {
		return fmt.Errorf("transfer matching predecessor file claim: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count matching predecessor file claim transfer: %w", err)
	}
	if count != 1 {
		return ErrPublicationAdmissionConflict
	}
	if _, err := tx.NewRaw(`DELETE FROM publication_namespace_reservation r
		USING publication_namespace_claim c
		WHERE c.id=r.namespace_claim_id AND c.epoch=? AND r.position_id=? AND r.generation=?
			AND NOT EXISTS (SELECT 1 FROM obsolete_publication_output_cleanup o
				WHERE o.epoch=c.epoch AND o.position_id=r.position_id AND o.generation=r.generation
				AND o.namespace_claim_id=r.namespace_claim_id)
			AND NOT EXISTS (SELECT 1 FROM publication_root_abandonment a
				WHERE a.epoch=c.epoch AND a.position_id=r.position_id AND c.id=ANY(a.namespace_claim_ids))`,
		previous.Epoch, previous.PositionID, previous.Generation).Exec(ctx); err != nil {
		return fmt.Errorf("release matching predecessor reservations: %w", err)
	}
	return nil
}

// lockPublicationMatchingRecheckMemberTx reads the exact durable receipt that
// owns this publication operation. An ordinary publication has no receipt and
// returns nil. The caller already holds the standard publication result locks.
func lockPublicationMatchingRecheckMemberTx(ctx context.Context, tx bun.Tx, attempt *TrackPublicationAttempt) (*publicationMatchingRecheckMember, error) {
	if attempt == nil || attempt.OperationID == uuid.Nil {
		return nil, errors.New("read matching recheck publication receipt: attempt is missing")
	}
	members := make([]publicationMatchingRecheckMember, 0, 1)
	if err := tx.NewRaw(`SELECT recheck_id,operation_id,output_epoch,position_id,candidate_generation,
			predecessor_position_id,predecessor_generation,predecessor_claim_id,same_path,predecessor_snapshot,committed_at,candidate_payload_sha256
		FROM matching_local_recheck_publication WHERE operation_id=? FOR UPDATE`,
		attempt.OperationID).Scan(ctx, &members); err != nil {
		return nil, fmt.Errorf("read matching recheck publication receipt: %w", err)
	}
	switch len(members) {
	case 0:
		return nil, nil
	case 1:
		return &members[0], nil
	default:
		return nil, fmt.Errorf("read matching recheck publication receipt: operation has %d receipts: %w", len(members), ErrPublicationAdmissionConflict)
	}
}

func (member *publicationMatchingRecheckMember) validateSettlement(
	ctx context.Context, tx bun.Tx, attempt *TrackPublicationAttempt, input PublicationInputs,
) error {
	if member.OperationID != attempt.OperationID || member.OutputEpoch != attempt.Epoch ||
		member.PositionID != attempt.PositionID || member.CandidateGeneration != attempt.CandidateGeneration ||
		member.CommittedAt != nil {
		return fmt.Errorf("matching recheck publication receipt does not match the delivery: %w", ErrPublicationAdmissionConflict)
	}
	if !validProviderTagRewrite(input) {
		return errors.New("commit publication result: matching recheck receipt requires a provider-tag rewrite")
	}
	if member.PredecessorPositionID == nil || member.PredecessorGeneration == nil || member.PredecessorClaimID == nil ||
		*member.PredecessorPositionID == uuid.Nil || *member.PredecessorGeneration == uuid.Nil || *member.PredecessorClaimID == uuid.Nil ||
		attempt.ExpectedGeneration == nil || *member.PredecessorGeneration != *attempt.ExpectedGeneration {
		return fmt.Errorf("matching recheck publication receipt has no exact predecessor: %w", ErrPublicationAdmissionConflict)
	}
	if *member.PredecessorPositionID != member.PositionID {
		return fmt.Errorf("matching recheck cross-position publication result settlement is not authorized: %w", ErrPublicationAdmissionConflict)
	}
	if !member.SamePath {
		return fmt.Errorf("matching recheck publication result would install at a different path without an authorized install grant: %w", ErrPublicationAdmissionConflict)
	}
	var predecessor matchingRecheckPublicationPredecessorWitness
	if err := decodeStrictJSON(member.PredecessorSnapshot, &predecessor); err != nil ||
		predecessor.Version != 1 || predecessor.PositionID != member.PositionID ||
		predecessor.OutputEpoch != member.OutputEpoch || predecessor.Generation != *member.PredecessorGeneration ||
		predecessor.NamespaceClaimID != *member.PredecessorClaimID ||
		predecessor.RelativePath != input.ProviderTags.Current.RelativePath ||
		!reflect.DeepEqual(predecessor.Current, input.ProviderTags.Current) {
		return fmt.Errorf("matching recheck publication predecessor changed: %w", ErrPublicationAdmissionConflict)
	}
	var claim uuid.UUID
	if err := tx.NewRaw(`SELECT c.id FROM publication_namespace_claim c
		JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
		WHERE c.id=? AND c.epoch=? AND c.owner_position_id=? AND c.node_kind='file'
			AND c.relative_path=? AND r.position_id=? AND r.generation=?
		FOR UPDATE OF c`,
		predecessor.NamespaceClaimID, predecessor.OutputEpoch, predecessor.PositionID,
		predecessor.RelativePath, predecessor.PositionID, predecessor.Generation).Scan(ctx, &claim); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPublicationAdmissionConflict
		}
		return fmt.Errorf("lock matching recheck publication predecessor claim: %w", err)
	}
	return nil
}

// lockMatchingRecheckMovedCurrentTx re-reads an exact cross-position
// predecessor and its still-owned file claim. Result settlement already holds
// the output epoch, so no other publication can move this generation.
func lockMatchingRecheckMovedCurrentTx(
	ctx context.Context, tx bun.Tx, member *publicationMatchingRecheckMember,
) (TrackPublication, matchingRecheckPublicationPredecessorWitness, error) {
	var current TrackPublication
	var witness matchingRecheckPublicationPredecessorWitness
	if member.PredecessorPositionID == nil || member.PredecessorGeneration == nil || member.PredecessorClaimID == nil ||
		*member.PredecessorPositionID == member.PositionID ||
		decodeStrictJSON(member.PredecessorSnapshot, &witness) != nil || witness.Version != 1 ||
		witness.PositionID != *member.PredecessorPositionID || witness.Generation != *member.PredecessorGeneration ||
		witness.OutputEpoch != member.OutputEpoch || witness.NamespaceClaimID != *member.PredecessorClaimID ||
		witness.Current.RelativePath != witness.RelativePath || witness.Current.Generation != witness.Generation {
		return current, witness, ErrPublicationAdmissionConflict
	}
	if err := tx.NewSelect().Model(&current).Where("position_id=?", witness.PositionID).For("UPDATE").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return current, witness, ErrPublicationAdmissionConflict
		}
		return current, witness, fmt.Errorf("lock matching predecessor current: %w", err)
	}
	if !publicationCurrentMatchesWitness(current, witness.Current, witness.OutputEpoch) {
		return current, witness, ErrPublicationAdmissionConflict
	}
	var claim uuid.UUID
	if err := tx.NewRaw(`SELECT c.id FROM publication_namespace_claim c
		JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
		WHERE c.id=? AND c.epoch=? AND c.owner_position_id=? AND c.relative_path=?
		AND c.node_kind='file' AND r.position_id=? AND r.generation=?
		AND NOT EXISTS (SELECT 1 FROM track_publication_attempt a WHERE a.position_id=c.owner_position_id)
		AND NOT EXISTS (SELECT 1 FROM publication_root_abandonment a WHERE a.epoch=c.epoch AND c.id=ANY(a.namespace_claim_ids))
		AND NOT EXISTS (SELECT 1 FROM obsolete_publication_output_cleanup o WHERE o.namespace_claim_id=c.id)
		FOR UPDATE OF c,r`, witness.NamespaceClaimID, witness.OutputEpoch, witness.PositionID,
		witness.RelativePath, witness.PositionID, witness.Generation).Scan(ctx, &claim); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return current, witness, ErrPublicationAdmissionConflict
		}
		return current, witness, fmt.Errorf("lock matching predecessor file claim: %w", err)
	}
	return current, witness, nil
}

func validateMatchingRecheckMovedResult(
	attempt *TrackPublicationAttempt, input PublicationInputs, member *publicationMatchingRecheckMember,
	previous TrackPublication, witness matchingRecheckPublicationPredecessorWitness, observation PublicationStageObservation,
) error {
	if member.OperationID != attempt.OperationID || member.OutputEpoch != attempt.Epoch || member.PositionID != attempt.PositionID ||
		member.CandidateGeneration != attempt.CandidateGeneration || member.CommittedAt != nil ||
		attempt.ExpectedGeneration != nil || attempt.IntentKind != "audio" || input.Version != PublicationInputsVersion ||
		input.SourceAuthorityMode != "automatic" || input.Audio == nil ||
		member.SamePath != (previous.RelativePath == observation.RelativePath) {
		return ErrPublicationAdmissionConflict
	}
	if !member.SamePath && (member.CandidatePayloadSHA256 == nil || *member.CandidatePayloadSHA256 != observation.PayloadSHA256) {
		return fmt.Errorf("matching move has no exact prepared destination proof: %w", ErrPublicationAdmissionConflict)
	}
	return (MatchingRecheckPublicationMoveInput{Current: witness.Current}).ValidateQuality(observation)
}

// matchingRecheckPredecessorCanRetireTx recognizes only the exact automatic
// Local association owned by this recheck. An existing association, shared
// track, manual authority or draft is not erased to make room for a move.
// The caller holds the source mutation gate.
func matchingRecheckPredecessorCanRetireTx(
	ctx context.Context, tx bun.Tx, member *publicationMatchingRecheckMember, previous TrackPublication,
) (bool, error) {
	var snapshot string
	if err := tx.NewRaw(`SELECT incumbent_snapshot::text FROM matching_local_recheck
		WHERE id=? AND state IN ('waiting','publishing')`, member.RecheckID).Scan(ctx, &snapshot); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrPublicationAdmissionConflict
		}
		return false, fmt.Errorf("read predecessor matching incumbent: %w", err)
	}
	var incumbent matchingLocalRecheckSnapshot
	if err := decodeStrictJSON([]byte(snapshot), &incumbent); err != nil || incumbent.Version != 1 || len(incumbent.Sources) == 0 {
		return false, ErrPublicationAdmissionConflict
	}
	if err := lockMatchingRecheckManualAuthority(ctx, tx, incumbent); err != nil {
		if errors.Is(err, ErrMatchingManualProtection) {
			return false, nil
		}
		return false, err
	}
	allowed := make([]uuid.UUID, 0, len(incumbent.Sources))
	for _, source := range incumbent.Sources {
		if source.ReleaseTrackID == previous.PositionID && source.TrackSourceInserted {
			allowed = append(allowed, source.TrackSourceID)
		}
	}
	if len(allowed) == 0 {
		return false, nil
	}
	for _, id := range allowed {
		shared, err := matchingLocalRecheckTransferAssociationSharedTx(ctx, tx, member.RecheckID, id)
		if err != nil || shared {
			return false, err
		}
	}
	var protected bool
	if err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM position_source_authority WHERE position_id=? AND mode='manual'
		UNION ALL SELECT 1 FROM release_track position
			JOIN track_source source ON source.track_id=position.track_id
			WHERE position.id=? AND (source.id NOT IN (?) OR source.decision_method<>'automatic' OR source.policy_version IS DISTINCT FROM ?)
		UNION ALL SELECT 1 FROM release_track position JOIN release_track shared ON shared.track_id=position.track_id
			WHERE position.id=? AND shared.id<>position.id
	)`, previous.PositionID, previous.PositionID, bun.List(allowed), MatchingLayoutPolicyVersion, previous.PositionID).Scan(ctx, &protected); err != nil {
		return false, fmt.Errorf("check matching predecessor protection: %w", err)
	}
	return !protected, nil
}

func retireMatchingRecheckMovedAssociationTx(ctx context.Context, tx bun.Tx, member *publicationMatchingRecheckMember, previous TrackPublication) error {
	if _, err := tx.NewRaw(`DELETE FROM track_source source USING matching_local_recheck recheck, release_track position
		WHERE recheck.id=? AND recheck.state='publishing' AND position.id=? AND position.track_id=source.track_id
		AND source.decision_method='automatic' AND source.policy_version=?
		AND EXISTS (SELECT 1 FROM jsonb_array_elements(recheck.incumbent_snapshot->'sources') entry
			WHERE entry->>'release_track_id'=position.id::text AND entry->>'track_source_id'=source.id::text
			AND entry->>'media_variant_id'=source.media_variant_id::text AND entry->'track_source_inserted'='true'::jsonb)`,
		member.RecheckID, previous.PositionID, MatchingLayoutPolicyVersion).Exec(ctx); err != nil {
		return fmt.Errorf("retire moved automatic matching association: %w", err)
	}
	return nil
}

// settlePublicationMatchingRecheckResultTx marks the exact receipt committed and
// closes the recheck only once every publication receipt has committed. Closing
// the recheck releases the durable source veto in the same transaction.
func settlePublicationMatchingRecheckResultTx(ctx context.Context, tx bun.Tx, member *publicationMatchingRecheckMember) error {
	result, err := tx.NewRaw(`UPDATE matching_local_recheck_publication SET committed_at=now()
		WHERE recheck_id=? AND operation_id=? AND output_epoch=? AND position_id=? AND candidate_generation=?
			AND predecessor_position_id=? AND predecessor_generation=? AND predecessor_claim_id=?
			AND same_path=? AND committed_at IS NULL`,
		member.RecheckID, member.OperationID, member.OutputEpoch, member.PositionID, member.CandidateGeneration,
		*member.PredecessorPositionID, *member.PredecessorGeneration, *member.PredecessorClaimID, member.SamePath).Exec(ctx)
	if err != nil {
		return fmt.Errorf("commit publication result: mark matching recheck receipt: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("commit publication result: count matching recheck receipt: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("commit publication result: matching recheck receipt changed: %w", ErrPublicationAdmissionConflict)
	}
	return closeMatchingRecheckAfterPublicationReceiptsTx(ctx, tx, member.RecheckID)
}

// closeMatchingRecheckAfterPublicationReceiptsTx serializes receipt completion
// on the recheck row so concurrent settlements cannot both leave the recheck
// open, and refuses to release the source veto while any receipt is pending.
func closeMatchingRecheckAfterPublicationReceiptsTx(ctx context.Context, tx bun.Tx, recheckID uuid.UUID) error {
	var state string
	if err := tx.NewRaw(`SELECT state FROM matching_local_recheck WHERE id=? FOR UPDATE`, recheckID).Scan(ctx, &state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("commit publication result: matching recheck is missing: %w", ErrPublicationAdmissionConflict)
		}
		return fmt.Errorf("commit publication result: lock matching recheck: %w", err)
	}
	if state != "publishing" {
		return fmt.Errorf("commit publication result: matching recheck is not publishing: %w", ErrPublicationAdmissionConflict)
	}
	var pending bool
	if err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM matching_local_recheck_publication WHERE recheck_id=? AND (committed_at IS NULL OR NOT same_path)
	)`, recheckID).Scan(ctx, &pending); err != nil {
		return fmt.Errorf("commit publication result: read matching recheck receipts: %w", err)
	}
	if pending {
		return nil
	}
	if _, err := tx.NewRaw(`DELETE FROM matching_local_recheck WHERE id=? AND state='publishing'`, recheckID).Exec(ctx); err != nil {
		return fmt.Errorf("commit publication result: close matching recheck: %w", err)
	}
	return nil
}
