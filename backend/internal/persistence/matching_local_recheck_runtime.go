package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// MatchingLocalRecheckTarget is the incumbent for comparison, even after its
// incoming group has ceased to exist. Publication authority is not part of it.
type MatchingLocalRecheckTarget struct {
	ID          uuid.UUID
	Next        time.Time
	Local       MatchingRelease
	LocalDigest string
	Assignments []MatchingLayoutDecisionAssignment
	Captures    []IncomingGroupingCapture
	Threshold   float64
}

func (repository *IncomingMatchCandidateRepository) ListDueMatchingLocalRechecks(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit < 1 || limit > 16 {
		return nil, errors.New("local recheck limit must be between 1 and 16")
	}
	ids := make([]uuid.UUID, 0, limit)
	err := repository.db.NewRaw(`SELECT id FROM matching_local_recheck
		WHERE state='waiting' AND next_recheck_at<=now()
		ORDER BY next_recheck_at,id LIMIT ?`, limit).Scan(ctx, &ids)
	return ids, err
}

func (repository *IncomingMatchCandidateRepository) ReadDueMatchingLocalRecheck(ctx context.Context, id uuid.UUID) (*MatchingLocalRecheckTarget, error) {
	var target *MatchingLocalRecheckTarget
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var err error
		target, _, err = readMatchingLocalRecheckTarget(ctx, tx, id, false)
		return err
	})
	return target, err
}

// RetireSupersededMatchingLocalRecheck only closes a waiting obligation whose
// exact automatic assignment no longer exists or is now under manual authority.
// It never releases a publishing obligation or treats an unavailable root,
// changed analysis policy, or changed Local catalog as supersession.
func (repository *IncomingMatchCandidateRepository) RetireSupersededMatchingLocalRecheck(ctx context.Context, id uuid.UUID) (bool, error) {
	var retired bool
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return err
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return err
		}
		var snapshot string
		err := tx.NewRaw(`SELECT incumbent_snapshot::text FROM matching_local_recheck
			WHERE id=? AND state='waiting' AND next_recheck_at<=now() FOR UPDATE`, id).Scan(ctx, &snapshot)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read waiting Local recheck before retirement: %w", err)
		}
		var incumbent matchingLocalRecheckSnapshot
		if err := json.Unmarshal([]byte(snapshot), &incumbent); err != nil || incumbent.Version != 1 ||
			incumbent.LocalApplyOperationID == uuid.Nil || len(incumbent.Sources) == 0 {
			return errors.New("invalid Local recheck incumbent for retirement")
		}
		if err := matchingLocalRecheckValidateHeldSourcesTx(ctx, tx, id, incumbent); err != nil {
			return err
		}
		err = lockMatchingRecheckManualAuthority(ctx, tx, incumbent)
		switch {
		case errors.Is(err, ErrMatchingManualProtection):
			retired = true
		case err != nil:
			return err
		default:
			retired, err = matchingLocalRecheckAssignmentSuperseded(ctx, tx, incumbent)
			if err != nil {
				return err
			}
		}
		if !retired {
			return nil
		}
		if _, err := tx.NewRaw(`DELETE FROM matching_local_recheck WHERE id=? AND state='waiting'`, id).Exec(ctx); err != nil {
			return fmt.Errorf("retire superseded Local matching assignment: %w", err)
		}
		return nil
	})
	return retired && err == nil, err
}

func matchingLocalRecheckAssignmentSuperseded(ctx context.Context, tx bun.Tx, incumbent matchingLocalRecheckSnapshot) (bool, error) {
	locations := make([]uuid.UUID, 0, len(incumbent.Sources))
	for _, source := range incumbent.Sources {
		var assigned bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM track_source source JOIN release_track position ON position.track_id=source.track_id
			WHERE source.id=? AND source.track_id=? AND source.media_variant_id=? AND position.id=?
		)`, source.TrackSourceID, source.LocalTrackID, source.MediaVariantID, source.ReleaseTrackID).Scan(ctx, &assigned); err != nil {
			return false, fmt.Errorf("check Local recheck incumbent association: %w", err)
		}
		if !assigned {
			return true, nil
		}
		locations = append(locations, source.SourceLocationID)
	}
	loaded, err := loadIncomingGroupingCapturesForLocations(ctx, tx, locations)
	if err != nil {
		return false, err
	}
	frozen := make(map[uuid.UUID][]byte, len(incumbent.Sources))
	for _, source := range incumbent.Sources {
		frozen[source.SourceLocationID] = source.Capture.SourceSHA256
	}
	for _, capture := range loaded {
		old := frozen[capture.LocationID]
		if len(old) == 32 && len(capture.SourceSHA256) == 32 && !bytes.Equal(old, capture.SourceSHA256) {
			return true, nil
		}
	}
	return false, nil
}

func readMatchingLocalRecheckTarget(ctx context.Context, tx bun.Tx, id uuid.UUID, lock bool) (*MatchingLocalRecheckTarget, matchingLocalRecheckSnapshot, error) {
	var incumbent matchingLocalRecheckSnapshot
	var row struct {
		Snapshot  json.RawMessage `bun:"incumbent_snapshot"`
		Operation uuid.UUID       `bun:"local_apply_operation_id"`
		Next      time.Time       `bun:"next_recheck_at"`
	}
	err := tx.NewRaw(`SELECT incumbent_snapshot,local_apply_operation_id,next_recheck_at
		FROM matching_local_recheck WHERE id=? AND state='waiting' AND next_recheck_at<=now()`, id).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, incumbent, nil
	}
	if err != nil {
		return nil, incumbent, err
	}
	if json.Unmarshal(row.Snapshot, &incumbent) != nil || incumbent.Version != 1 ||
		incumbent.LocalApplyOperationID != row.Operation || incumbent.PolicyVersion != MatchingLayoutPolicyVersion ||
		incumbent.ProviderReleaseID == uuid.Nil || incumbent.LocalReleaseID == uuid.Nil || len(incumbent.Sources) == 0 {
		return nil, incumbent, errors.New("local matching recheck incumbent is invalid")
	}
	local, err := readMatchingLocalSelectionSnapshotTx(ctx, tx, []uuid.UUID{incumbent.ProviderReleaseID})
	if err != nil {
		return nil, incumbent, err
	}
	if len(local.Releases) != 1 || local.Releases[0].ID != incumbent.ProviderReleaseID {
		return nil, incumbent, ErrIncomingMatchConflict
	}
	thresholdText, err := readMatchingLayoutPairThreshold(ctx, tx, lock)
	if err != nil {
		return nil, incumbent, err
	}
	threshold, err := parseMatchingLayoutPairThreshold(thresholdText)
	if err != nil {
		return nil, incumbent, err
	}
	target := &MatchingLocalRecheckTarget{
		ID: id, Next: row.Next, Local: local.Releases[0], LocalDigest: local.Digest,
		Threshold: threshold, Captures: make([]IncomingGroupingCapture, 0, len(incumbent.Sources)),
	}
	seen := make(map[uuid.UUID]uuid.UUID)
	for _, source := range incumbent.Sources {
		capture := source.Capture
		capture.TagEncoding = source.TagEncoding
		if !jsonObject(capture.TagEncoding) {
			capture.TagEncoding = nil
		}
		identity, ok := matchingLocalRecheckCaptureIdentityOf(capture)
		if !ok || identity.WorkID != source.WorkID || identity.RootID != source.SourceRootID ||
			identity.LocationID != source.SourceLocationID || identity.MediaVariantID != source.MediaVariantID {
			return nil, incumbent, errors.New("local matching recheck capture identity is invalid")
		}
		target.Captures = append(target.Captures, capture)
		if track := seen[capture.VariantID]; track != uuid.Nil {
			if track != source.ProviderTrackID {
				return nil, incumbent, errors.New("local matching recheck has conflicting incumbent positions")
			}
			continue
		}
		seen[capture.VariantID] = source.ProviderTrackID
		target.Assignments = append(target.Assignments, MatchingLayoutDecisionAssignment{
			FileMemberID: capture.VariantID, ProviderTrackID: source.ProviderTrackID,
		})
	}
	return target, incumbent, nil
}

// PostponeMatchingLocalRecheck leaves the exact source veto in place while
// MusicBrainz is unavailable or its result is indeterminate. An old delivery
// cannot postpone a newer obligation.
func (repository *IncomingMatchCandidateRepository) PostponeMatchingLocalRecheck(ctx context.Context, target MatchingLocalRecheckTarget, next time.Time) error {
	_, err := repository.db.NewRaw(`UPDATE matching_local_recheck SET next_recheck_at=?
		WHERE id=? AND next_recheck_at=? AND state='waiting' AND next_recheck_at<?`,
		next, target.ID, target.Next, next).Exec(ctx)
	return err
}

// RetainMatchingLocalAssignment settles a proved non-winning comparison.
// Provider, Local catalog and source facts are fenced before releasing the
// automatic cleanup veto.
func (repository *IncomingMatchCandidateRepository) RetainMatchingLocalAssignment(
	ctx context.Context, expected MatchingLocalRecheckTarget, provider []MatchingProviderLayoutEvidence,
) error {
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockMatchingLocalRecheckEvidence(ctx, tx, provider); err != nil {
			return err
		}
		_, incumbent, err := lockMatchingLocalRecheckTarget(ctx, tx, expected)
		if err != nil {
			return err
		}
		if err := matchingLocalRecheckValidateHeldSourcesTx(ctx, tx, expected.ID, incumbent); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM matching_local_recheck WHERE id=? AND state='waiting'`, expected.ID).Exec(ctx); err != nil {
			return fmt.Errorf("release Local matching recheck veto: %w", err)
		}
		return nil
	})
}

func lockMatchingLocalRecheckTarget(ctx context.Context, tx bun.Tx, expected MatchingLocalRecheckTarget) (*MatchingLocalRecheckTarget, matchingLocalRecheckSnapshot, error) {
	var incumbent matchingLocalRecheckSnapshot
	var next time.Time
	err := tx.NewRaw(`SELECT next_recheck_at FROM matching_local_recheck
		WHERE id=? AND state='waiting' AND next_recheck_at<=now() FOR UPDATE`, expected.ID).Scan(ctx, &next)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !next.Equal(expected.Next) {
		return nil, incumbent, ErrIncomingMatchConflict
	}
	if err != nil {
		return nil, incumbent, err
	}
	current, incumbent, err := readMatchingLocalRecheckTarget(ctx, tx, expected.ID, true)
	if err != nil {
		return nil, incumbent, err
	}
	if current == nil || current.LocalDigest != expected.LocalDigest || current.Threshold != expected.Threshold {
		return nil, incumbent, ErrIncomingMatchConflict
	}
	if !slices.Equal(current.Assignments, expected.Assignments) ||
		!slices.EqualFunc(current.Captures, expected.Captures, matchingLocalRecheckCaptureEqual) {
		return nil, incumbent, ErrIncomingMatchConflict
	}
	locations := make([]uuid.UUID, 0, len(current.Captures))
	wanted := make(map[uuid.UUID]IncomingGroupingCapture, len(current.Captures))
	for _, capture := range current.Captures {
		locations = append(locations, capture.LocationID)
		wanted[capture.LocationID] = capture
	}
	loaded, err := loadIncomingGroupingCapturesForLocations(ctx, tx, locations)
	if err != nil {
		return nil, incumbent, err
	}
	if len(loaded) != len(wanted) {
		return nil, incumbent, ErrIncomingMatchConflict
	}
	for _, capture := range loaded {
		if !matchingLocalRecheckCaptureEqual(capture, wanted[capture.LocationID]) {
			return nil, incumbent, fmt.Errorf("revalidate Local matching recheck capture: %w", ErrIncomingMatchConflict)
		}
	}
	return current, incumbent, nil
}
