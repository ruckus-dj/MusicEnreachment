package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// MatchingLocalRecheckPublicationCandidate is one incumbent library position of
// a waiting Local recheck that currently has a published CURRENT output. It is
// read-only preflight evidence for the bounded same-position republication
// preparation; it is never admission authority.
type MatchingLocalRecheckPublicationCandidate struct {
	PositionID   uuid.UUID
	ReleaseID    uuid.UUID
	Generation   uuid.UUID
	RelativePath string
}

// ReadMatchingLocalRecheckPublicationCandidates enumerates, at most once per
// incumbent position, the CURRENT publications a winning comparison would have
// to account for. The caller prepares one bounded publication or no-op proof per
// candidate; the winning transaction re-derives the authoritative coverage and
// rejects any mismatch.
func (repository *IncomingMatchCandidateRepository) ReadMatchingLocalRecheckPublicationCandidates(
	ctx context.Context, expected MatchingLocalRecheckTarget, decision MatchingLayoutDecision, providerSourceID uuid.UUID,
) ([]MatchingLocalRecheckPublicationCandidate, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("read Local recheck publication candidates: repository is unavailable")
	}
	if expected.ID == uuid.Nil || decision.ProviderReleaseID == nil || providerSourceID == uuid.Nil {
		return nil, ErrIncomingMatchConflict
	}
	candidates := make([]MatchingLocalRecheckPublicationCandidate, 0)
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var stored struct {
			State    string          `bun:"state"`
			Next     time.Time       `bun:"next_recheck_at"`
			Snapshot json.RawMessage `bun:"incumbent_snapshot"`
		}
		err := tx.NewRaw(`SELECT state,next_recheck_at,incumbent_snapshot
			FROM matching_local_recheck WHERE id=?`, expected.ID).Scan(ctx, &stored)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read Local recheck publication candidates: read obligation: %w", err)
		}
		if stored.State != "waiting" || !stored.Next.Equal(expected.Next) {
			return ErrIncomingMatchConflict
		}
		var incumbent matchingLocalRecheckSnapshot
		if err := json.Unmarshal(stored.Snapshot, &incumbent); err != nil || incumbent.Version != 1 ||
			incumbent.ProviderReleaseID == uuid.Nil || incumbent.LocalReleaseID == uuid.Nil || len(incumbent.Sources) == 0 {
			return errors.New("read Local recheck publication candidates: incumbent is invalid")
		}
		var epoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
			return fmt.Errorf("read Local recheck publication candidates: read output epoch: %w", err)
		}
		if epoch == uuid.Nil {
			return ErrIncomingMatchConflict
		}
		seen := make(map[uuid.UUID]bool, len(incumbent.Sources))
		tracks := make(map[uuid.UUID]uuid.UUID, len(decision.Assignments))
		for _, assignment := range decision.Assignments {
			tracks[assignment.FileMemberID] = assignment.ProviderTrackID
		}
		positions := make([]uuid.UUID, 0, len(incumbent.Sources))
		for _, source := range incumbent.Sources {
			if seen[source.ReleaseTrackID] {
				continue
			}
			seen[source.ReleaseTrackID] = true
			var samePosition bool
			if err := tx.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM release_track position
				JOIN provider_local_release rel ON rel.local_release_id=position.release_id
				JOIN provider p ON p.id=rel.provider_id AND p.code='musicbrainz'
				JOIN provider_local_track binding ON binding.provider_id=p.id AND binding.local_track_id=position.track_id
				JOIN provider_source_release_track track ON track.track_id=binding.provider_track_id AND track.provider_id=p.id
				WHERE track.provider_source_id=? AND position.id=? AND rel.provider_release_id=? AND track.track_id=? AND track.release_id=rel.provider_release_id
					AND track.present AND track.medium=position.medium AND track.position=position.position
			)`, providerSourceID, source.ReleaseTrackID, *decision.ProviderReleaseID, tracks[source.Capture.VariantID]).Scan(ctx, &samePosition); err != nil {
				return fmt.Errorf("read exact MusicBrainz same-position candidate: %w", err)
			}
			if !samePosition {
				continue
			}
			positions = append(positions, source.ReleaseTrackID)
		}
		sort.Slice(positions, func(i, j int) bool { return positions[i].String() < positions[j].String() })
		for _, position := range positions {
			var releaseID uuid.UUID
			if err := tx.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, position).Scan(ctx, &releaseID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrIncomingMatchConflict
				}
				return fmt.Errorf("read Local recheck publication candidates: read incumbent position: %w", err)
			}
			var current struct {
				Generation   uuid.UUID `bun:"generation"`
				RelativePath string    `bun:"relative_path"`
			}
			err := tx.NewRaw(`SELECT generation,relative_path FROM track_publication
				WHERE position_id=? AND epoch=?`, position, epoch).Scan(ctx, &current)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("read Local recheck publication candidates: read CURRENT: %w", err)
			}
			candidates = append(candidates, MatchingLocalRecheckPublicationCandidate{
				PositionID: position, ReleaseID: releaseID,
				Generation: current.Generation, RelativePath: current.RelativePath,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return candidates, nil
}

// ApplyMatchingLocalRecheck applies a strictly winning comparison calculated by
// the recovery worker. The accepted provider projection and exact incumbent are
// rechecked before materializing anything. Same-position CURRENT publications
// are admitted with their prepared provider-tag rewrites (or exact no-op proofs)
// in this one transaction; an unaccounted or cross-position CURRENT fails
// closed. No caller-named position is trusted: destinations are derived from the
// winning decision itself.
func (repository *IncomingMatchCandidateRepository) ApplyMatchingLocalRecheck(
	ctx context.Context, expected MatchingLocalRecheckTarget,
	evidence []MatchingProviderLayoutEvidence, decision MatchingLayoutDecision,
	plan MatchingRecheckPublicationPlan, client RiverInserter,
) error {
	if len(evidence) == 0 || decision.Status != MatchingLayoutDecisionSelected || decision.ProviderReleaseID == nil {
		return ErrIncomingMatchConflict
	}
	if err := plan.validate(); err != nil {
		return err
	}
	prepared := make([]matchingRecheckPreparedPublication, 0, len(plan.Publications))
	for _, admission := range plan.Publications {
		if admission.reconciliationGuard != nil || admission.firstAdmission != nil ||
			admission.sourceRepublishAdmission != nil || admission.validatedAudioUpgrade ||
			admission.validatedManualLyrics || admission.validatedManualCover ||
			admission.validatedAutomaticLyricsRetry {
			return fmt.Errorf("apply Local recheck publication: private capability is not allowed: %w", ErrIncomingMatchConflict)
		}
		input, err := admission.prepareProviderTagAdmission(client)
		if err != nil {
			return fmt.Errorf("apply Local recheck publication: %w", err)
		}
		prepared = append(prepared, matchingRecheckPreparedPublication{admission: admission, input: input})
	}
	manager := &SetupManagerRepository{db: repository.db}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockMatchingLocalRecheckEvidence(ctx, tx, evidence); err != nil {
			return err
		}
		target, incumbent, err := lockMatchingLocalRecheckTarget(ctx, tx, expected)
		if err != nil {
			return err
		}
		last := evidence[len(evidence)-1]
		keys, err := validateMatchingProviderLayoutEvidence(last)
		if err != nil {
			return err
		}
		snapshot, err := readMusicBrainzMatchingSnapshot(ctx, tx, last.Endpoint, last.ConfigurationIdentity, last.ReleaseKeys, keys, false)
		if err != nil {
			return err
		}
		ready := make([]uuid.UUID, 0, len(target.Assignments))
		for _, assignment := range target.Assignments {
			ready = append(ready, assignment.FileMemberID)
		}
		if err := decision.Validate(MatchingLayoutPolicyVersion, target.Threshold, ready, snapshot.Releases); err != nil {
			return fmt.Errorf("validate musicbrainz recovery decision: %w", err)
		}
		if err := lockMatchingRecheckManualAuthority(ctx, tx, incumbent); err != nil {
			return err
		}
		destinations, err := materializeMatchingRecheckPositions(ctx, tx, target, snapshot, last, decision)
		if err != nil {
			return fmt.Errorf("materialize matching recheck positions: %w", err)
		}
		positions := make([]uuid.UUID, 0, len(destinations)+len(incumbent.Sources))
		for _, position := range destinations {
			positions = append(positions, position)
		}
		for _, source := range incumbent.Sources {
			positions = append(positions, source.ReleaseTrackID)
		}
		// Serialize against publication admission even if neither position has
		// CURRENT yet. A source transfer must never bypass a new manual attempt.
		var epoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
			return err
		}
		blocked, err := matchingRecheckPublicationUsesPosition(ctx, tx, epoch, positions...)
		if err != nil {
			return err
		}
		if blocked {
			return ErrPublicationAdmissionConflict
		}
		var locked []uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id IN (?) ORDER BY id FOR UPDATE`, bun.List(positions)).Scan(ctx, &locked); err != nil {
			return err
		}
		if err := validateMatchingRecheckPublicationPlanTx(ctx, tx, incumbent, destinations, epoch, plan); err != nil {
			return fmt.Errorf("validate matching recheck publication plan: %w", err)
		}
		moves, err := readMatchingRecheckMovesTx(ctx, tx, incumbent, destinations, epoch)
		if err != nil {
			return fmt.Errorf("read matching recheck moves: %w", err)
		}
		movePositions := make(map[uuid.UUID]uuid.UUID, len(moves))
		for _, move := range moves {
			movePositions[move.previous.PositionID] = move.destination
		}
		transfer, err := transferMatchingLocalRecheckSourcesTx(ctx, tx, incumbent, destinations, last.ProviderID, last.ProviderSourceID, decision, movePositions)
		if err != nil {
			return fmt.Errorf("transfer matching recheck associations: %w", err)
		}
		// A transfer with an existing publication needs the owning publication
		// barrier; do not consume the veto or commit any partial transfer.
		if transfer.SelectionBarrier {
			return fmt.Errorf("matching recheck associations have unresolved publication selection: %w", ErrIncomingMatchConflict)
		}
		for _, move := range moves {
			if err := admitMatchingRecheckMoveTx(ctx, tx, manager, target.ID, move, plan.PrepareMove, client); err != nil {
				return err
			}
		}
		if len(prepared) > 0 || len(moves) > 0 {
			if err := admitMatchingRecheckPublicationsTx(ctx, tx, manager, target.ID, target.Next, epoch, prepared, client); err != nil {
				return err
			}
			return deleteLocalAutomaticAlbumGrantTx(ctx, tx, incumbent.LocalApplyOperationID)
		}
		if err := deleteLocalAutomaticAlbumGrantTx(ctx, tx, incumbent.LocalApplyOperationID); err != nil {
			return err
		}
		removed, err := tx.NewRaw(`DELETE FROM matching_local_recheck WHERE id=? AND state='waiting'`, target.ID).Exec(ctx)
		if err != nil {
			return err
		}
		rows, err := removed.RowsAffected()
		if err != nil {
			return fmt.Errorf("apply Local recheck: count settled obligation: %w", err)
		}
		if rows != 1 {
			return ErrIncomingMatchConflict
		}
		return nil
	})
}

func materializeMatchingRecheckPositions(
	ctx context.Context, tx bun.Tx, target *MatchingLocalRecheckTarget, snapshot ProviderMatchingSnapshot, evidence MatchingProviderLayoutEvidence, decision MatchingLayoutDecision,
) (map[uuid.UUID]uuid.UUID, error) {
	evaluation, err := BuildFrozenMatchingLayoutEvaluation(&decision, FrozenMatchingReviewReleases(snapshot.Releases))
	if err != nil {
		return nil, err
	}
	persisted := persistedMatchingDecision{Assignments: evaluation.Assignments}
	persisted.SelectedRelease = &struct {
		ProviderReleaseID  uuid.UUID `json:"provider_release_id"`
		ProviderReleaseKey string    `json:"provider_release_key"`
	}{*decision.ProviderReleaseID, evaluation.SelectedRelease}
	group := IncomingMatchGroupEvaluation{}
	for _, assignment := range target.Assignments {
		group.Members = append(group.Members, IncomingMatchMemberFence{FileMemberID: assignment.FileMemberID})
	}
	fence := IncomingMatchProviderFence{
		ProviderID: evidence.ProviderID, ProviderSourceID: evidence.ProviderSourceID, ReleaseKeys: evidence.ReleaseKeys,
		AcceptedProjectionRevision: snapshot.AcceptedProjectionRevision,
	}
	destinations := make(map[uuid.UUID]uuid.UUID, len(group.Members))
	_, err = materializeMatchingDecision(ctx, tx, group, persisted, fence, MatchingLayoutPolicyVersion,
		func(member IncomingMatchMemberFence, releaseID, trackID uuid.UUID, medium, position int) error {
			var id uuid.UUID
			if err := tx.NewRaw(`SELECT id FROM release_track WHERE release_id=? AND track_id=? AND medium=? AND position=?`,
				releaseID, trackID, medium, position).Scan(ctx, &id); err != nil {
				return fmt.Errorf("read exact musicbrainz recovery destination: %w", err)
			}
			destinations[member.FileMemberID] = id
			return nil
		})
	return destinations, err
}

func lockMatchingRecheckManualAuthority(ctx context.Context, tx bun.Tx, incumbent matchingLocalRecheckSnapshot) error {
	ids := make([]uuid.UUID, 0, len(incumbent.Sources))
	variants := make([]uuid.UUID, 0, len(incumbent.Sources))
	for _, item := range incumbent.Sources {
		ids = append(ids, item.SourceLocationID)
		variants = append(variants, item.MediaVariantID)
	}
	var manual bool
	if err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM incoming_group_member_location loc
		JOIN incoming_group g ON g.id=loc.group_id
		LEFT JOIN incoming_group_matching_authority a ON a.group_id=g.id
		WHERE loc.location_id IN (?) AND (g.manual OR COALESCE(a.mode,'automatic')<>'automatic')
		UNION ALL SELECT 1 FROM track_source WHERE media_variant_id IN (?) AND decision_method='manual'
	)`, bun.List(ids), bun.List(variants)).Scan(ctx, &manual); err != nil {
		return err
	}
	if manual {
		return ErrMatchingManualProtection
	}
	return nil
}

func lockMatchingLocalRecheckEvidence(ctx context.Context, tx bun.Tx, evidence []MatchingProviderLayoutEvidence) error {
	if len(evidence) == 0 {
		return ErrIncomingMatchConflict
	}
	if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
		return err
	}
	if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
		return err
	}
	if err := lockToolsMoveGateShared(ctx, tx); err != nil {
		return err
	}
	for _, stage := range evidence {
		if stage.ProviderSourceID != evidence[0].ProviderSourceID {
			return ErrIncomingMatchConflict
		}
		if err := validateMatchingLocalRecheckProviderEvidenceTx(ctx, tx, stage); err != nil {
			return err
		}
	}
	return lockMatchingLocalCatalogEvidenceTx(ctx, tx)
}
