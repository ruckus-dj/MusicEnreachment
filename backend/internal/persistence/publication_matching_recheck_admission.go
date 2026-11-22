package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

// MatchingRecheckPublicationPreparation is the bounded, read-only preflight
// result for one already-published library position of a Local recheck. Exactly
// one field is set: Admission when the accepted provider projection changes the
// CURRENT tags, or NoOp when the projection is already present.
type MatchingRecheckPublicationPreparation struct {
	Admission *PublicationAdmission
	NoOp      *MatchingRecheckNoOpProof
}

// MatchingRecheckNoOpProof freezes the exact observation that a same-position
// CURRENT publication already carries the accepted provider tag projection. It
// is the only accepted substitute for a publication: the winning transaction
// re-reads CURRENT and the accepted provider authority and refuses the proof
// unless the same unchanged tag set is still provable without a filesystem or
// metadata read.
type MatchingRecheckNoOpProof struct {
	PositionID            uuid.UUID
	ExpectedReleaseID     uuid.UUID
	ExpectedEpoch         uuid.UUID
	Current               PublicationCurrentWitness
	ProviderSourceID      uuid.UUID
	Endpoint              string
	ConfigurationIdentity string
	AuthorityRevision     string
	Patch                 map[string][]string
	Notices               []string
	ProtectedKeys         []string
}

// MatchingRecheckPublicationPlan is the complete same-position republication
// preflight for one winning comparison. It is not an admission authority: the
// winning transaction derives the exact destinations and the required
// same-position CURRENT coverage itself and rejects any entry that does not
// match that derivation.
type MatchingRecheckPublicationPlan struct {
	Publications []PublicationAdmission
	NoOps        []MatchingRecheckNoOpProof
	PrepareMove  PrepareMatchingRecheckMovedPublication
}

func (plan MatchingRecheckPublicationPlan) validate() error {
	positions := make(map[uuid.UUID]bool, len(plan.Publications)+len(plan.NoOps))
	for _, admission := range plan.Publications {
		if admission.PositionID == uuid.Nil || admission.ExpectedReleaseID == uuid.Nil ||
			admission.ExpectedEpoch == uuid.Nil || admission.ExpectedGeneration == nil ||
			*admission.ExpectedGeneration == uuid.Nil || admission.CandidateGeneration == uuid.Nil ||
			admission.IntentKind != "rewrite" {
			return fmt.Errorf("apply Local recheck publication: prepared admission identity is incomplete: %w", ErrIncomingMatchConflict)
		}
		if positions[admission.PositionID] {
			return fmt.Errorf("apply Local recheck publication: prepared position is ambiguous: %w", ErrIncomingMatchConflict)
		}
		positions[admission.PositionID] = true
	}
	for _, proof := range plan.NoOps {
		if err := proof.validate(); err != nil {
			return err
		}
		if positions[proof.PositionID] {
			return fmt.Errorf("apply Local recheck publication: prepared position is ambiguous: %w", ErrIncomingMatchConflict)
		}
		positions[proof.PositionID] = true
	}
	return nil
}

func (proof MatchingRecheckNoOpProof) validate() error {
	if proof.PositionID == uuid.Nil || proof.ExpectedReleaseID == uuid.Nil || proof.ExpectedEpoch == uuid.Nil ||
		proof.ProviderSourceID == uuid.Nil {
		return fmt.Errorf("apply Local recheck publication: no-op identity is incomplete: %w", ErrIncomingMatchConflict)
	}
	if strings.TrimSpace(proof.Endpoint) == "" || strings.TrimSpace(proof.ConfigurationIdentity) == "" ||
		!validSHA256(proof.AuthorityRevision) || len(proof.Patch) == 0 || proof.ProtectedKeys == nil {
		return fmt.Errorf("apply Local recheck publication: no-op authority is incomplete: %w", ErrIncomingMatchConflict)
	}
	current := proof.Current
	if current.Generation == uuid.Nil || !frozenPublicationRelativePath(current.RelativePath) ||
		current.Ownership.SizeBytes == nil || current.Ownership.MTime == nil || !validSHA256(current.Ownership.SHA256) ||
		current.Tags == nil {
		return fmt.Errorf("apply Local recheck publication: no-op CURRENT witness is incomplete: %w", ErrIncomingMatchConflict)
	}
	if err := (PublicationRewriteInput{
		CurrentGeneration: current.Generation, RelativePath: current.RelativePath,
		Ownership: current.Ownership, AudioFacts: current.AudioFacts,
	}).Validate(); err != nil {
		return fmt.Errorf("apply Local recheck publication: invalid no-op CURRENT witness: %w", err)
	}
	if err := current.Assets.Validate(); err != nil {
		return fmt.Errorf("apply Local recheck publication: invalid no-op CURRENT assets: %w", err)
	}
	desired, err := publication.ApplyProviderTagPatch(current.Tags, proof.Patch, proof.ProtectedKeys)
	if err != nil {
		return fmt.Errorf("apply Local recheck publication: invalid no-op provider patch: %w", err)
	}
	if !reflect.DeepEqual(desired, current.Tags) {
		return fmt.Errorf("apply Local recheck publication: no-op proof does not prove unchanged tags: %w", ErrIncomingMatchConflict)
	}
	return nil
}

type matchingRecheckPreparedPublication struct {
	admission PublicationAdmission
	input     PublicationInputs
}

type matchingRecheckPublicationPredecessor struct {
	claimID  uuid.UUID
	snapshot json.RawMessage
}

// matchingRecheckPublicationPredecessorWitness freezes the exact CURRENT the
// receipt may replace. It is committed with the receipt, not inferred later.
type matchingRecheckPublicationPredecessorWitness struct {
	Version          int                       `json:"version"`
	PositionID       uuid.UUID                 `json:"position_id"`
	OutputEpoch      uuid.UUID                 `json:"output_epoch"`
	Generation       uuid.UUID                 `json:"generation"`
	RelativePath     string                    `json:"relative_path"`
	NamespaceClaimID uuid.UUID                 `json:"namespace_claim_id"`
	Current          PublicationCurrentWitness `json:"current"`
}

// admitMatchingRecheckPublicationsTx admits the prepared same-position
// republications inside the winning apply transaction. The recheck row is
// already locked by the caller; generic provider-tag admission, the predecessor
// witness and the receipt are created atomically with the source transfer.
func admitMatchingRecheckPublicationsTx(
	ctx context.Context,
	tx bun.Tx,
	manager *SetupManagerRepository,
	recheckID uuid.UUID,
	expectedNext time.Time,
	epoch uuid.UUID,
	publications []matchingRecheckPreparedPublication,
	client RiverInserter,
) error {
	if manager == nil || manager.db == nil || recheckID == uuid.Nil {
		return fmt.Errorf("admit matching recheck publications: admission scope is incomplete: %w", ErrIncomingMatchConflict)
	}
	for _, publication := range publications {
		operationID, err := manager.admitPublicationTx(ctx, tx, publication.admission, publication.input, uuid.New(), client)
		if err != nil {
			return fmt.Errorf("admit matching recheck publication: %w", err)
		}
		// Read the predecessor claim after the generic admission has taken the
		// epoch and namespace locks, so the bridge never introduces a
		// claim-before-epoch lock order.
		predecessor, err := lockMatchingRecheckPublicationPredecessorTx(ctx, tx, publication, epoch)
		if err != nil {
			return err
		}
		if err := insertMatchingRecheckPublicationReceiptTx(ctx, tx, recheckID, operationID, publication, epoch, predecessor); err != nil {
			return err
		}
	}
	updated, err := tx.NewRaw(`UPDATE matching_local_recheck SET state='publishing'
		WHERE id=? AND state='waiting' AND next_recheck_at=?`,
		recheckID, expectedNext).Exec(ctx)
	if err != nil {
		return fmt.Errorf("admit matching recheck publications: claim recheck: %w", err)
	}
	rows, err := updated.RowsAffected()
	if err != nil {
		return fmt.Errorf("admit matching recheck publications: count claimed recheck: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("admit matching recheck publications: recheck obligation changed: %w", ErrPublicationAdmissionConflict)
	}
	return nil
}

// validateMatchingRecheckPublicationPlanTx derives the authoritative
// same-position CURRENT coverage from the exact winning destinations and
// requires the caller's bounded preflight to account for every position exactly
// once. A position with CURRENT must be covered by one prepared publication or
// one exact no-op proof; a position without CURRENT must not be listed. Cross-
// position moves are handled separately and never consume a same-position plan
// entry.
func validateMatchingRecheckPublicationPlanTx(
	ctx context.Context,
	tx bun.Tx,
	incumbent matchingLocalRecheckSnapshot,
	destinations map[uuid.UUID]uuid.UUID,
	epoch uuid.UUID,
	plan MatchingRecheckPublicationPlan,
) error {
	incumbentPosition := make(map[uuid.UUID]uuid.UUID, len(incumbent.Sources))
	for _, source := range incumbent.Sources {
		member := source.Capture.VariantID
		if member == uuid.Nil || source.ReleaseTrackID == uuid.Nil {
			return fmt.Errorf("admit matching recheck publications: incumbent member identity is incomplete: %w", ErrIncomingMatchConflict)
		}
		if position, exists := incumbentPosition[member]; exists {
			if position != source.ReleaseTrackID {
				return fmt.Errorf("admit matching recheck publications: incumbent member has ambiguous positions: %w", ErrIncomingMatchConflict)
			}
			continue
		}
		incumbentPosition[member] = source.ReleaseTrackID
	}
	if len(destinations) != len(incumbentPosition) {
		return fmt.Errorf("admit matching recheck publications: derived destination set is incomplete: %w", ErrIncomingMatchConflict)
	}
	samePosition := make(map[uuid.UUID]bool, len(incumbentPosition))
	for member, position := range incumbentPosition {
		destination, exists := destinations[member]
		if !exists || destination == uuid.Nil {
			return fmt.Errorf("admit matching recheck publications: member has no derived destination: %w", ErrIncomingMatchConflict)
		}
		if destination == position {
			samePosition[position] = true
		}
	}

	admissions := make(map[uuid.UUID]PublicationAdmission, len(plan.Publications))
	for _, admission := range plan.Publications {
		if _, exists := admissions[admission.PositionID]; exists {
			return fmt.Errorf("admit matching recheck publications: prepared position is ambiguous: %w", ErrIncomingMatchConflict)
		}
		admissions[admission.PositionID] = admission
	}
	noOps := make(map[uuid.UUID]MatchingRecheckNoOpProof, len(plan.NoOps))
	for _, proof := range plan.NoOps {
		if _, exists := noOps[proof.PositionID]; exists {
			return fmt.Errorf("admit matching recheck publications: no-op position is ambiguous: %w", ErrIncomingMatchConflict)
		}
		if _, exists := admissions[proof.PositionID]; exists {
			return fmt.Errorf("admit matching recheck publications: position has several preparations: %w", ErrIncomingMatchConflict)
		}
		noOps[proof.PositionID] = proof
	}

	required := make(map[uuid.UUID]bool, len(samePosition))
	provenNoOp := make(map[uuid.UUID]bool, len(noOps))
	for position := range samePosition {
		var attempt bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM track_publication_attempt WHERE position_id=?)`,
			position).Scan(ctx, &attempt); err != nil {
			return fmt.Errorf("admit matching recheck publications: read active attempt: %w", err)
		}
		if attempt {
			return fmt.Errorf("admit matching recheck publications: same-position has an unfinished publication: %w", ErrPublicationAdmissionConflict)
		}
		var releaseID uuid.UUID
		if err := tx.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, position).Scan(ctx, &releaseID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("admit matching recheck publications: same-position is missing: %w", ErrIncomingMatchConflict)
			}
			return fmt.Errorf("admit matching recheck publications: read same-position release: %w", err)
		}
		var current TrackPublication
		err := tx.NewSelect().Model(&current).Where("position_id=?", position).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			if _, exists := admissions[position]; exists {
				return fmt.Errorf("admit matching recheck publications: position has no CURRENT to rewrite: %w", ErrPublicationAdmissionConflict)
			}
			if _, exists := noOps[position]; exists {
				return fmt.Errorf("admit matching recheck publications: no-op position has no CURRENT: %w", ErrPublicationAdmissionConflict)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("admit matching recheck publications: read CURRENT: %w", err)
		}
		if current.Epoch != epoch {
			return fmt.Errorf("admit matching recheck publications: CURRENT epoch changed: %w", ErrPublicationAdmissionConflict)
		}
		if admission, exists := admissions[position]; exists {
			if admission.ExpectedEpoch != epoch || admission.ExpectedReleaseID != releaseID ||
				admission.ExpectedGeneration == nil || *admission.ExpectedGeneration != current.Generation {
				return fmt.Errorf("admit matching recheck publications: prepared publication does not match CURRENT: %w", ErrPublicationAdmissionConflict)
			}
			required[position] = true
			continue
		}
		if proof, exists := noOps[position]; exists {
			if err := validateMatchingRecheckNoOpProofTx(ctx, tx, proof, current, releaseID, epoch); err != nil {
				return err
			}
			provenNoOp[position] = true
			continue
		}
		return fmt.Errorf("admit matching recheck publications: same-position CURRENT is not represented: %w", ErrPublicationAdmissionConflict)
	}
	for position := range admissions {
		if !samePosition[position] {
			return fmt.Errorf("admit matching recheck publications: publication position is not a same-position destination: %w", ErrPublicationAdmissionConflict)
		}
		if !required[position] {
			return fmt.Errorf("admit matching recheck publications: publication has no required CURRENT: %w", ErrPublicationAdmissionConflict)
		}
	}
	for position := range noOps {
		if !samePosition[position] {
			return fmt.Errorf("admit matching recheck publications: no-op position is not a same-position destination: %w", ErrPublicationAdmissionConflict)
		}
		if !provenNoOp[position] {
			return fmt.Errorf("admit matching recheck publications: no-op position has no CURRENT: %w", ErrPublicationAdmissionConflict)
		}
	}
	return nil
}

// validateMatchingRecheckNoOpProofTx re-proves a no-op against the locked CURRENT
// row and the accepted provider authority. Matching ordinals alone never prove
// it: the accepted projection must still produce exactly the recorded tag set
// and must change nothing.
func validateMatchingRecheckNoOpProofTx(
	ctx context.Context,
	tx bun.Tx,
	proof MatchingRecheckNoOpProof,
	current TrackPublication,
	releaseID, epoch uuid.UUID,
) error {
	if proof.PositionID != current.PositionID || releaseID == uuid.Nil ||
		proof.ExpectedReleaseID != releaseID || proof.ExpectedEpoch != epoch ||
		!publicationCurrentMatchesWitness(current, proof.Current, epoch) {
		return fmt.Errorf("admit matching recheck publications: no-op CURRENT changed: %w", ErrPublicationAdmissionConflict)
	}
	var snapshot *PublicationProviderTagContext
	if err := readPublicationProviderTagContextForPositionTx(ctx, tx, releaseID, proof.PositionID,
		proof.Endpoint, proof.ConfigurationIdentity, true, &snapshot); err != nil {
		return fmt.Errorf("admit matching recheck publications: read no-op accepted context: %w", err)
	}
	if snapshot == nil || snapshot.ReleaseOwner.ID != releaseID || snapshot.PositionOwner.Owner.ID != proof.PositionID ||
		snapshot.Source.ID != proof.ProviderSourceID || snapshot.Digest != proof.AuthorityRevision {
		return fmt.Errorf("admit matching recheck publications: no-op accepted authority changed: %w", ErrPublicationAdmissionConflict)
	}
	protection, err := BuildPublicationProviderTagProtection(snapshot)
	if err != nil {
		return fmt.Errorf("admit matching recheck publications: build no-op protection: %w", err)
	}
	projection, err := BuildPublicationProviderCanonicalTagProjection(*snapshot)
	if err != nil {
		return fmt.Errorf("admit matching recheck publications: build no-op projection: %w", err)
	}
	if !reflect.DeepEqual(projection.Tags, proof.Patch) || !sameProviderNotices(projection.Notices, proof.Notices) ||
		!reflect.DeepEqual(protection.Keys, proof.ProtectedKeys) {
		return fmt.Errorf("admit matching recheck publications: no-op accepted projection changed: %w", ErrPublicationAdmissionConflict)
	}
	desired, err := publication.ApplyProviderTagPatch(proof.Current.Tags, projection.Tags, protection.Keys)
	if err != nil {
		return fmt.Errorf("admit matching recheck publications: apply no-op provider patch: %w", err)
	}
	if !reflect.DeepEqual(desired, proof.Current.Tags) {
		return fmt.Errorf("admit matching recheck publications: no-op provider patch changes CURRENT tags: %w", ErrPublicationAdmissionConflict)
	}
	return nil
}

// lockMatchingRecheckPublicationPredecessorTx rechecks the exact predecessor
// generation, owning file claim and same-path target. Only a same-path install
// is authorized here; cross-position moves need their own grant.
func lockMatchingRecheckPublicationPredecessorTx(
	ctx context.Context, tx bun.Tx, publication matchingRecheckPreparedPublication, epoch uuid.UUID,
) (matchingRecheckPublicationPredecessor, error) {
	admission := publication.admission
	if admission.ExpectedGeneration == nil || *admission.ExpectedGeneration == uuid.Nil {
		return matchingRecheckPublicationPredecessor{}, fmt.Errorf("admit matching recheck publications: predecessor generation is missing: %w", ErrPublicationAdmissionConflict)
	}
	provider := publication.input.ProviderTags
	if provider == nil || publication.input.Rewrite == nil ||
		provider.Current.Generation != *admission.ExpectedGeneration ||
		publication.input.Rewrite.RelativePath != provider.Current.RelativePath {
		return matchingRecheckPublicationPredecessor{}, fmt.Errorf("admit matching recheck publications: predecessor CURRENT is not the target: %w", ErrPublicationAdmissionConflict)
	}
	rows := make([]struct {
		ID           uuid.UUID `bun:"id"`
		CanonicalKey string    `bun:"canonical_key"`
		RelativePath string    `bun:"relative_path"`
	}, 0, 2)
	if err := tx.NewRaw(`SELECT c.id,c.canonical_key,c.relative_path
		FROM publication_namespace_reservation r
		JOIN publication_namespace_claim c ON c.id=r.namespace_claim_id
		WHERE r.position_id=? AND r.generation=? AND c.epoch=? AND c.node_kind='file'
			AND c.owner_position_id=? AND c.relative_path=?
		ORDER BY c.id FOR UPDATE OF c`,
		admission.PositionID, *admission.ExpectedGeneration, epoch, admission.PositionID,
		provider.Current.RelativePath).Scan(ctx, &rows); err != nil {
		return matchingRecheckPublicationPredecessor{}, fmt.Errorf("admit matching recheck publications: read predecessor claim: %w", err)
	}
	if len(rows) != 1 {
		return matchingRecheckPublicationPredecessor{}, fmt.Errorf("admit matching recheck publications: predecessor claim is not unique: %w", ErrPublicationAdmissionConflict)
	}
	claim := rows[0]
	found := false
	for _, node := range admission.NamespaceManifest {
		if node.NodeKind != "file" || node.CanonicalKey != claim.CanonicalKey {
			continue
		}
		if node.RelativePath != claim.RelativePath {
			return matchingRecheckPublicationPredecessor{}, fmt.Errorf("admit matching recheck publications: namespace target changed: %w", ErrPublicationAdmissionConflict)
		}
		found = true
	}
	if !found {
		return matchingRecheckPublicationPredecessor{}, fmt.Errorf("admit matching recheck publications: namespace target is not the predecessor: %w", ErrPublicationAdmissionConflict)
	}
	snapshot, err := json.Marshal(matchingRecheckPublicationPredecessorWitness{
		Version:          1,
		PositionID:       admission.PositionID,
		OutputEpoch:      epoch,
		Generation:       *admission.ExpectedGeneration,
		RelativePath:     provider.Current.RelativePath,
		NamespaceClaimID: claim.ID,
		Current:          provider.Current,
	})
	if err != nil {
		return matchingRecheckPublicationPredecessor{}, fmt.Errorf("admit matching recheck publications: encode predecessor witness: %w", err)
	}
	return matchingRecheckPublicationPredecessor{claimID: claim.ID, snapshot: snapshot}, nil
}

func insertMatchingRecheckPublicationReceiptTx(
	ctx context.Context, tx bun.Tx, recheckID, operationID uuid.UUID,
	publication matchingRecheckPreparedPublication, epoch uuid.UUID,
	predecessor matchingRecheckPublicationPredecessor,
) error {
	if _, err := tx.NewRaw(`INSERT INTO matching_local_recheck_publication
		(recheck_id,operation_id,output_epoch,position_id,candidate_generation,
		 predecessor_position_id,predecessor_generation,predecessor_claim_id,same_path,predecessor_snapshot)
		VALUES (?,?,?,?,?,?,?,?,true,?::jsonb)`,
		recheckID, operationID, epoch, publication.admission.PositionID,
		publication.admission.CandidateGeneration, publication.admission.PositionID,
		*publication.admission.ExpectedGeneration, predecessor.claimID,
		string(predecessor.snapshot)).Exec(ctx); err != nil {
		return fmt.Errorf("admit matching recheck publications: create receipt: %w", err)
	}
	return nil
}
