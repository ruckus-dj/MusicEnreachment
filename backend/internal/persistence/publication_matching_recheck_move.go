package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// MatchingRecheckMoveSnapshot contains the authoritative, transaction-local
// inputs to native first-audio planning. It does not grant admission.
type MatchingRecheckMoveSnapshot struct {
	Context             *LibraryFirstPublicationContext
	Settings            map[string]string
	Installations       []ToolInstallation
	Predecessor         TrackPublication
	PredecessorClaim    PublicationNamespaceClaim
	SourceAssociationID uuid.UUID
	MediaVariantID      uuid.UUID
	LocationID          uuid.UUID
}

type PrepareMatchingRecheckMovedPublication func(context.Context, MatchingRecheckMoveSnapshot) (PublicationAdmission, error)

type matchingRecheckMove struct {
	previous    TrackPublication
	authority   PositionSourceAuthority
	destination uuid.UUID
	source      matchingLocalRecheckSnapshotSource
}

func readMatchingRecheckMovesTx(
	ctx context.Context, tx bun.Tx, incumbent matchingLocalRecheckSnapshot, destinations map[uuid.UUID]uuid.UUID, epoch uuid.UUID,
) ([]matchingRecheckMove, error) {
	moves := make([]matchingRecheckMove, 0)
	seen := make(map[uuid.UUID]uuid.UUID, len(incumbent.Sources))
	for _, source := range incumbent.Sources {
		destination := destinations[source.Capture.VariantID]
		if destination == uuid.Nil {
			return nil, ErrIncomingMatchConflict
		}
		if destination == source.ReleaseTrackID {
			continue
		}
		if prior, exists := seen[source.ReleaseTrackID]; exists {
			if prior != destination {
				return nil, ErrIncomingMatchConflict
			}
			continue
		}
		seen[source.ReleaseTrackID] = destination
		var blocked bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM track_publication_attempt WHERE position_id IN (?,?)
			UNION ALL SELECT 1 FROM track_publication WHERE position_id=?
		)`, source.ReleaseTrackID, destination, destination).Scan(ctx, &blocked); err != nil {
			return nil, fmt.Errorf("check cross-position matching publication: %w", err)
		}
		if blocked {
			return nil, ErrPublicationAdmissionConflict
		}
		move := matchingRecheckMove{destination: destination}
		err := tx.NewSelect().Model(&move.previous).Where("position_id=?", source.ReleaseTrackID).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			continue // No predecessor: first publication remains manual.
		}
		if err != nil {
			return nil, fmt.Errorf("lock matching move predecessor: %w", err)
		}
		if move.previous.Epoch != epoch {
			return nil, ErrPublicationAdmissionConflict
		}
		if err := tx.NewSelect().Model(&move.authority).Where("position_id=?", source.ReleaseTrackID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrPublicationAdmissionConflict
			}
			return nil, fmt.Errorf("lock matching move predecessor source: %w", err)
		}
		if move.authority.Mode != "automatic" || move.authority.PublicationEpoch != epoch ||
			move.authority.PublicationGeneration != move.previous.Generation {
			return nil, ErrPublicationAdmissionConflict
		}
		for _, candidate := range incumbent.Sources {
			if candidate.ReleaseTrackID == source.ReleaseTrackID && candidate.MediaVariantID == move.authority.MediaVariantID &&
				candidate.SourceLocationID == move.authority.SourceLocationID &&
				destinations[candidate.Capture.VariantID] == destination && candidate.TrackSourceInserted {
				move.source = candidate
				break
			}
		}
		if move.source.TrackSourceID == uuid.Nil {
			return nil, ErrPublicationAdmissionConflict
		}
		moves = append(moves, move)
	}
	sort.Slice(moves, func(i, j int) bool {
		return moves[i].previous.PositionID.String() < moves[j].previous.PositionID.String()
	})
	return moves, nil
}

func admitMatchingRecheckMoveTx(
	ctx context.Context, tx bun.Tx, manager *SetupManagerRepository, recheckID uuid.UUID, move matchingRecheckMove,
	prepare PrepareMatchingRecheckMovedPublication, client RiverInserter,
) error {
	if prepare == nil || client == nil {
		return ErrPublicationAdmissionConflict
	}
	previous := move.previous
	var claim PublicationNamespaceClaim
	if err := tx.NewRaw(`SELECT c.* FROM publication_namespace_claim c
		JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
		WHERE c.epoch=? AND c.owner_position_id=? AND c.node_kind='file' AND c.relative_path=?
		AND r.position_id=? AND r.generation=? FOR UPDATE OF c,r`,
		previous.Epoch, previous.PositionID, previous.RelativePath, previous.PositionID, previous.Generation).Scan(ctx, &claim); err != nil {
		return fmt.Errorf("lock matching move predecessor claim: %w", err)
	}
	var releaseID uuid.UUID
	if err := tx.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, move.destination).Scan(ctx, &releaseID); err != nil {
		return fmt.Errorf("read matching move destination release: %w", err)
	}
	var contextSnapshot *LibraryFirstPublicationContext
	if err := readLibraryFirstPublicationContextTx(ctx, tx, releaseID, move.destination, &contextSnapshot); err != nil {
		return fmt.Errorf("read matching move publication context: %w", err)
	}
	if contextSnapshot == nil || contextSnapshot.Library == nil || contextSnapshot.Library.ActiveEpoch != previous.Epoch ||
		contextSnapshot.Library.Current != nil || contextSnapshot.Library.ActiveAttempt != nil {
		return ErrPublicationAdmissionConflict
	}
	var settings []AppSetting
	if err := tx.NewSelect().Model(&settings).Scan(ctx); err != nil {
		return fmt.Errorf("read matching move publication settings: %w", err)
	}
	options := make(map[string]string, len(settings))
	for _, setting := range settings {
		options[setting.Name] = setting.Value
	}
	var installations []ToolInstallation
	if err := tx.NewSelect().Model(&installations).Where("package_kind='ffmpeg' AND state='ready'").Scan(ctx); err != nil {
		return fmt.Errorf("read matching move publication installations: %w", err)
	}
	adoption := matchingRecheckNamespaceAdoption{
		PredecessorPositionID: previous.PositionID, PredecessorGeneration: previous.Generation, PredecessorClaimID: claim.ID,
		CanonicalKey: claim.CanonicalKey, RelativePath: claim.RelativePath,
	}
	if err := validateMatchingRecheckNamespacePredecessorStateTx(ctx, tx, claim, adoption, nil, nil); err != nil {
		return err
	}
	var associationID uuid.UUID
	if err := tx.NewRaw(`SELECT s.id FROM track_source s JOIN release_track p ON p.track_id=s.track_id
		WHERE p.id=? AND s.media_variant_id=? AND s.decision_method='automatic' AND s.compatible IS TRUE FOR UPDATE OF s`,
		move.destination, move.source.MediaVariantID).Scan(ctx, &associationID); err != nil {
		return fmt.Errorf("lock matching move destination association: %w", err)
	}
	for _, association := range contextSnapshot.SourceAssociations {
		if association.ID != associationID && (association.Selected || association.DecisionMethod == "manual") {
			return ErrPublicationAdmissionConflict
		}
	}
	request, err := prepare(ctx, MatchingRecheckMoveSnapshot{
		Context: contextSnapshot, Settings: options, Installations: installations, Predecessor: previous, PredecessorClaim: claim,
		SourceAssociationID: associationID, MediaVariantID: move.source.MediaVariantID, LocationID: move.source.SourceLocationID,
	})
	if err != nil {
		return fmt.Errorf("plan matching move publication: %w", err)
	}
	if request.PositionID != move.destination || request.ExpectedReleaseID != releaseID || request.ExpectedEpoch != previous.Epoch ||
		request.ExpectedGeneration != nil || request.Source == nil || request.Source.MediaVariantID != move.source.MediaVariantID ||
		request.Source.LocationID != move.source.SourceLocationID || !request.ToolsReadRequired ||
		request.IntentKind != "audio" || request.firstAdmission != nil || request.reconciliationGuard != nil ||
		request.sourceRepublishAdmission != nil || request.namespaceAdoption != nil {
		return ErrPublicationAdmissionConflict
	}
	cover, err := readEffectiveReleaseCoverInput(ctx, tx, releaseID, contextSnapshot.EffectiveCover)
	if err != nil {
		return fmt.Errorf("read matching move effective cover: %w", err)
	}
	if err := request.requireMatchingRecheckCoverInput(cover); err != nil {
		return err
	}
	input, err := request.prepareAdmission(client)
	if err != nil {
		return err
	}
	if input.Version != PublicationInputsVersion || input.SourceAuthorityMode != "automatic" || input.Audio == nil {
		return ErrPublicationAdmissionConflict
	}
	selection := FirstPublicationSourceSelection{AssociationID: associationID, Associations: contextSnapshot.SourceAssociations}
	if _, _, err := applyTrackSourceSelection(ctx, tx, move.destination, *request.Source, *input.Audio, selection, "select matching move source"); err != nil {
		return err
	}
	samePath := false
	for _, node := range request.NamespaceManifest {
		if node.NodeKind == "file" && node.RelativePath == claim.RelativePath && node.CanonicalKey == claim.CanonicalKey {
			samePath = true
		}
	}
	if samePath {
		if err := request.grantMatchingRecheckNamespaceAdoption(adoption); err != nil {
			return err
		}
		member := &publicationMatchingRecheckMember{RecheckID: recheckID}
		canRetire, err := matchingRecheckPredecessorCanRetireTx(ctx, tx, member, previous)
		if err != nil {
			return err
		}
		if !canRetire {
			return ErrPublicationAdmissionConflict
		}
	} else if err := request.requireMatchingRecheckNamespacePredecessor(adoption); err != nil {
		return err
	}
	operationID, err := manager.admitPublicationTx(ctx, tx, request, input, uuid.New(), client)
	if err != nil {
		return err
	}
	witness, err := matchingRecheckMoveWitness(previous, claim.ID)
	if err != nil {
		return err
	}
	if _, err := tx.NewRaw(`INSERT INTO matching_local_recheck_publication
		(operation_id,recheck_id,output_epoch,position_id,candidate_generation,
		 predecessor_position_id,predecessor_generation,predecessor_claim_id,same_path,predecessor_snapshot)
		VALUES (?,?,?,?,?,?,?,?,?,?::jsonb)`, operationID, recheckID, previous.Epoch, request.PositionID,
		request.CandidateGeneration, previous.PositionID, previous.Generation, claim.ID, samePath, string(witness)).Exec(ctx); err != nil {
		return fmt.Errorf("record matching move publication membership: %w", err)
	}
	return nil
}

func matchingRecheckMoveWitness(current TrackPublication, claimID uuid.UUID) ([]byte, error) {
	var ownership PublicationCurrentOwnership
	var metadata PublicationCommitMetadata
	audio, ok := publicationAudioFactsFromObservation(current.ActualAudio)
	if !ok || decodePublicationCurrentContract(current.Ownership, &ownership) != nil ||
		!validInstalledPublicationOwnership(ownership) || strictMetadata(current.ActualMetadata, &metadata) != nil {
		return nil, ErrPublicationAdmissionConflict
	}
	assets, err := DecodePublicationAssetOutcomes(current.ActualAssets)
	if err != nil {
		return nil, err
	}
	return json.Marshal(matchingRecheckPublicationPredecessorWitness{
		Version: 1, PositionID: current.PositionID, OutputEpoch: current.Epoch, Generation: current.Generation,
		RelativePath: current.RelativePath, NamespaceClaimID: claimID,
		Current: PublicationCurrentWitness{Generation: current.Generation, RelativePath: current.RelativePath,
			Ownership: ownership.Identity, AudioFacts: audio, Assets: assets, Tags: metadata.Tags},
	})
}
