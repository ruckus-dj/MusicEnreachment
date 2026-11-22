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

// MatchingLocalRecheckTransferRetention explains why an incumbent automatic
// association was deliberately left in place. A retained association is never a
// transfer failure; settlement or publication decides what happens next.
type MatchingLocalRecheckTransferRetention string

const (
	// MatchingLocalRecheckRetainedReused marks an association that predates the
	// incumbent automatic layout. The layout reused it; the recheck does not own
	// the row and must not delete it.
	MatchingLocalRecheckRetainedReused MatchingLocalRecheckTransferRetention = "reused"
	// MatchingLocalRecheckRetainedForeign marks an automatic row whose policy is
	// not the incumbent whole-layout policy. It is a foreign decision.
	MatchingLocalRecheckRetainedForeign MatchingLocalRecheckTransferRetention = "foreign"
	// MatchingLocalRecheckRetainedSamePosition marks an association whose exact
	// library position already carries a proven MusicBrainz binding. The Local
	// association and the MusicBrainz links coexist.
	MatchingLocalRecheckRetainedSamePosition MatchingLocalRecheckTransferRetention = "same_position"
	// MatchingLocalRecheckRetainedSharedTrack marks a destination that resolves
	// to the incumbent local track itself. The association row is shared and is
	// not removed.
	MatchingLocalRecheckRetainedSharedTrack MatchingLocalRecheckTransferRetention = "shared_track"
	// MatchingLocalRecheckRetainedSelected marks an owned association that
	// publication currently selected. Only the existing selection authority may
	// move selection; the transfer never selects a destination row itself.
	MatchingLocalRecheckRetainedSelected MatchingLocalRecheckTransferRetention = "selected_publication_barrier"
	// MatchingLocalRecheckRetainedHeld marks an owned association whose media
	// variant is held by a publication operation.
	MatchingLocalRecheckRetainedHeld MatchingLocalRecheckTransferRetention = "publication_hold"
	// MatchingLocalRecheckRetainedSharedRecheck marks an owned association that
	// another live recheck also witnesses. Deleting it would invalidate that
	// evidence, so it is kept.
	MatchingLocalRecheckRetainedSharedRecheck MatchingLocalRecheckTransferRetention = "shared_recheck"
)

// MatchingLocalRecheckTransferMusicBrainzBinding is the exact established
// MusicBrainz binding proved for a destination library position.
type MatchingLocalRecheckTransferMusicBrainzBinding struct {
	ProviderID        uuid.UUID `json:"provider_id"`
	ProviderReleaseID uuid.UUID `json:"provider_release_id"`
	ProviderTrackID   uuid.UUID `json:"provider_track_id"`
	LocalReleaseID    uuid.UUID `json:"local_release_id"`
	LocalTrackID      uuid.UUID `json:"local_track_id"`
	Method            string    `json:"method"`
	PolicyVersion     *string   `json:"policy_version,omitempty"`
	Medium            int       `json:"medium"`
	Position          int       `json:"position"`
}

// MatchingLocalRecheckTransferAssociation is the exact ownership and outcome
// witness for one unique incumbent track_source row. Several file members can
// share one row; FileMemberIDs records every member it covers.
type MatchingLocalRecheckTransferAssociation struct {
	TrackSourceID uuid.UUID `json:"track_source_id"`
	// ProviderTrackID and LocalTrackID are the incumbent Local provider track
	// and local track recorded in the immutable incumbent snapshot.
	ProviderTrackID uuid.UUID   `json:"provider_track_id"`
	LocalTrackID    uuid.UUID   `json:"local_track_id"`
	ReleaseTrackID  uuid.UUID   `json:"release_track_id"`
	MediaVariantID  uuid.UUID   `json:"media_variant_id"`
	FileMemberIDs   []uuid.UUID `json:"file_member_ids"`

	// Inserted reports whether the incumbent layout inserted the row. A reused
	// row is never owned by this recheck.
	Inserted bool `json:"inserted"`
	Selected bool `json:"selected"`
	Owned    bool `json:"owned"`
	Shared   bool `json:"shared"`

	SamePosition bool                                  `json:"same_position"`
	Removed      bool                                  `json:"removed"`
	Retained     bool                                  `json:"retained"`
	Retention    MatchingLocalRecheckTransferRetention `json:"retention,omitempty"`

	DestinationReleaseTrackID uuid.UUID `json:"destination_release_track_id,omitempty"`
	DestinationLocalTrackID   uuid.UUID `json:"destination_local_track_id,omitempty"`
	DestinationTrackSourceID  uuid.UUID `json:"destination_track_source_id,omitempty"`
	DestinationCreated        bool      `json:"destination_created"`

	DestinationBinding MatchingLocalRecheckTransferMusicBrainzBinding `json:"destination_binding"`
}

// MatchingLocalRecheckTransferResult is the deterministic settlement input for
// one transfer pass. SelectionBarrier is set when an owned association is
// currently selected; the caller must route it through publication selection
// authority instead of selecting a destination row.
type MatchingLocalRecheckTransferResult struct {
	RecheckID                  uuid.UUID                                 `json:"recheck_id"`
	IncumbentProviderReleaseID uuid.UUID                                 `json:"incumbent_provider_release_id"`
	IncumbentLocalReleaseID    uuid.UUID                                 `json:"incumbent_local_release_id"`
	Associations               []MatchingLocalRecheckTransferAssociation `json:"associations"`
	SelectionBarrier           bool                                      `json:"selection_barrier"`
}

type matchingLocalRecheckHeldSource struct {
	WorkID         uuid.UUID `bun:"work_id"`
	LocationID     uuid.UUID `bun:"source_location_id"`
	RootID         uuid.UUID `bun:"source_root_id"`
	MediaVariantID uuid.UUID `bun:"media_variant_id"`
	UnlinkTarget   string    `bun:"unlink_target"`
}

type matchingLocalRecheckTransferGroup struct {
	source                    matchingLocalRecheckSnapshotSource
	fileMembers               []uuid.UUID
	destinationReleaseTrackID uuid.UUID
}

type matchingLocalRecheckIncumbentAssociation struct {
	TrackID                uuid.UUID  `bun:"track_id"`
	MediaVariantID         uuid.UUID  `bun:"media_variant_id"`
	Selected               bool       `bun:"selected"`
	Compatible             bool       `bun:"compatible"`
	DecisionMethod         string     `bun:"decision_method"`
	PolicyVersion          *string    `bun:"policy_version"`
	SourceMetadataResultID *uuid.UUID `bun:"source_metadata_result_id"`
}

// transferMatchingLocalRecheckSourcesTx moves exactly the automatic Local
// associations owned by one immutable incumbent snapshot onto the caller's
// already-resolved MusicBrainz library positions.
//
// The destinations map is keyed by incumbent file member and must contain the
// exact existing or materialized release_track ID for every incumbent member and
// nothing else. A destination is accepted only when that exact release_track
// position already carries an established MusicBrainz binding: its local
// release is bound to a MusicBrainz release, its local track is bound to a
// present provider track of that release, and the provider track ordinal equals
// the library position ordinal. Equal medium/position ordinals alone are never
// treated as "the same position".
//
// A position is "the same" only when the supplied destination release_track ID
// equals the incumbent release_track ID and that proof holds. In that case the
// Local association is retained and MusicBrainz links are left in place.
//
// The helper only inserts destination track_source rows and deletes the exact
// owned automatic Local rows. It never deletes reused rows, rows owned by
// another policy, rows selected by publication, rows held by publication, rows
// witnessed by another live recheck, provider bindings, or Local catalog
// entities. A selected owned row is preserved as a publication barrier; this
// helper never sets selected=true on a destination row.
//
// Callers must already hold the output admission gate, the source mutation
// gate, and the Local/provider catalog SHARE fences. This helper deliberately
// does not acquire or upgrade those gates, schedule work, change recheck state,
// or enqueue publication.
func transferMatchingLocalRecheckSourcesTx(
	ctx context.Context,
	tx bun.Tx,
	incumbent matchingLocalRecheckSnapshot,
	destinations map[uuid.UUID]uuid.UUID,
	providerID, providerSourceID uuid.UUID,
	decision MatchingLayoutDecision,
	movePositions map[uuid.UUID]uuid.UUID,
) (MatchingLocalRecheckTransferResult, error) {
	result := MatchingLocalRecheckTransferResult{
		IncumbentProviderReleaseID: incumbent.ProviderReleaseID,
		IncumbentLocalReleaseID:    incumbent.LocalReleaseID,
		Associations:               make([]MatchingLocalRecheckTransferAssociation, 0, len(incumbent.Sources)),
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	groups, err := validateMatchingLocalRecheckTransfer(incumbent, destinations)
	if err != nil {
		return result, err
	}

	var stored struct {
		ID       uuid.UUID `bun:"id"`
		State    string    `bun:"state"`
		Snapshot []byte    `bun:"incumbent_snapshot"`
	}
	err = tx.NewRaw(`SELECT id,state,incumbent_snapshot
		FROM matching_local_recheck WHERE local_apply_operation_id=? FOR UPDATE`,
		incumbent.LocalApplyOperationID).Scan(ctx, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("transfer Local recheck sources: incumbent is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return result, fmt.Errorf("transfer Local recheck sources: read incumbent: %w", err)
	}
	result.RecheckID = stored.ID
	if stored.State != "waiting" {
		return result, fmt.Errorf("transfer Local recheck sources: incumbent already belongs to publication: %w", ErrIncomingMatchConflict)
	}
	encoded, err := json.Marshal(incumbent)
	if err != nil {
		return result, fmt.Errorf("transfer Local recheck sources: encode supplied incumbent: %w", err)
	}
	if !sameJSONValue(stored.Snapshot, encoded) {
		return result, fmt.Errorf("transfer Local recheck sources: supplied incumbent is not the stored evidence: %w", ErrIncomingMatchConflict)
	}

	if err := matchingLocalRecheckValidateHeldSourcesTx(ctx, tx, stored.ID, incumbent); err != nil {
		return result, err
	}
	if err := matchingLocalRecheckValidateCurrentSourcesTx(ctx, tx, incumbent); err != nil {
		return result, err
	}

	// Prove destinations before mutating any association, including reused
	// incumbent rows. A valid MusicBrainz binding at another edition/track is
	// not a destination authorized by the winning decision.
	if decision.ProviderReleaseID == nil || len(decision.Assignments) != len(destinations) {
		return result, ErrIncomingMatchConflict
	}
	proven := make(map[uuid.UUID]matchingLocalRecheckProvenPosition, len(destinations))
	for _, assignment := range decision.Assignments {
		position := destinations[assignment.FileMemberID]
		binding, ok := proven[position]
		if !ok {
			var err error
			binding, err = matchingLocalRecheckProvenMusicBrainzPositionTx(ctx, tx, providerSourceID, position)
			if err != nil {
				return result, err
			}
			proven[position] = binding
		}
		if binding.binding.ProviderID != providerID ||
			binding.binding.ProviderReleaseID != *decision.ProviderReleaseID ||
			binding.binding.ProviderTrackID != assignment.ProviderTrackID {
			return result, ErrIncomingMatchConflict
		}
	}
	for _, group := range groups {
		association, barrier, err := matchingLocalRecheckTransferGroupTx(ctx, tx, stored.ID, incumbent, group, proven[group.destinationReleaseTrackID],
			movePositions[group.source.ReleaseTrackID] == group.destinationReleaseTrackID)
		if err != nil {
			return result, err
		}
		result.Associations = append(result.Associations, association)
		result.SelectionBarrier = result.SelectionBarrier || barrier
	}
	return result, nil
}

// validateMatchingLocalRecheckTransfer validates caller-supplied identities and
// folds the per-capture snapshot into one group per unique track_source row. It
// returns groups in deterministic track_source order.
func validateMatchingLocalRecheckTransfer(
	incumbent matchingLocalRecheckSnapshot,
	destinations map[uuid.UUID]uuid.UUID,
) ([]matchingLocalRecheckTransferGroup, error) {
	if incumbent.Version != 1 || incumbent.LocalApplyOperationID == uuid.Nil ||
		incumbent.ProviderReleaseID == uuid.Nil || incumbent.LocalReleaseID == uuid.Nil ||
		incumbent.PolicyVersion != MatchingLayoutPolicyVersion || len(incumbent.Sources) == 0 {
		return nil, errors.New("transfer Local recheck sources: incumbent is incomplete")
	}
	if len(destinations) == 0 {
		return nil, errors.New("transfer Local recheck sources: destination positions are required")
	}

	seenCapture := make(map[matchingLocalRecheckCaptureIdentity]bool, len(incumbent.Sources))
	seenWork := make(map[uuid.UUID]bool, len(incumbent.Sources))
	memberSeen := make(map[uuid.UUID]bool, len(incumbent.Sources))
	groups := make(map[uuid.UUID]*matchingLocalRecheckTransferGroup, len(incumbent.Sources))
	for _, source := range incumbent.Sources {
		if source.WorkID == uuid.Nil || source.SourceLocationID == uuid.Nil || source.SourceRootID == uuid.Nil ||
			source.MediaVariantID == uuid.Nil || source.ProviderTrackID == uuid.Nil ||
			source.ReleaseTrackID == uuid.Nil || source.LocalTrackID == uuid.Nil || source.TrackSourceID == uuid.Nil ||
			source.Capture.VariantID == uuid.Nil || source.Capture.WorkID != source.WorkID ||
			source.Capture.LocationID != source.SourceLocationID || source.Capture.RootID != source.SourceRootID ||
			source.Capture.MediaVariantID == nil || *source.Capture.MediaVariantID != source.MediaVariantID {
			return nil, fmt.Errorf("transfer Local recheck sources: incumbent source identity is invalid: %w", ErrIncomingMatchConflict)
		}
		captureIdentity := matchingLocalRecheckCaptureIdentity{
			VariantID:      source.Capture.VariantID,
			WorkID:         source.WorkID,
			RootID:         source.SourceRootID,
			LocationID:     source.SourceLocationID,
			MediaVariantID: source.MediaVariantID,
		}
		if seenCapture[captureIdentity] || seenWork[source.WorkID] {
			return nil, fmt.Errorf("transfer Local recheck sources: incumbent source identity is ambiguous: %w", ErrIncomingMatchConflict)
		}
		seenCapture[captureIdentity] = true
		seenWork[source.WorkID] = true
		memberSeen[source.Capture.VariantID] = true

		destination, exists := destinations[source.Capture.VariantID]
		if !exists || destination == uuid.Nil {
			return nil, fmt.Errorf("transfer Local recheck sources: file member %s has no destination position: %w", source.Capture.VariantID, ErrIncomingMatchConflict)
		}
		group, exists := groups[source.TrackSourceID]
		if !exists {
			groups[source.TrackSourceID] = &matchingLocalRecheckTransferGroup{
				source:                    source,
				fileMembers:               []uuid.UUID{source.Capture.VariantID},
				destinationReleaseTrackID: destination,
			}
			continue
		}
		if group.source.MediaVariantID != source.MediaVariantID || group.source.LocalTrackID != source.LocalTrackID ||
			group.source.ReleaseTrackID != source.ReleaseTrackID || group.source.ProviderTrackID != source.ProviderTrackID ||
			group.source.TrackSourceInserted != source.TrackSourceInserted ||
			group.source.TrackSourceSelected != source.TrackSourceSelected ||
			!sameOptionalUUID(group.source.TrackSourceMetadata, source.TrackSourceMetadata) {
			return nil, fmt.Errorf("transfer Local recheck sources: one association has conflicting incumbent identities: %w", ErrIncomingMatchConflict)
		}
		if group.destinationReleaseTrackID != destination {
			return nil, fmt.Errorf("transfer Local recheck sources: one association maps to multiple destinations: %w", ErrIncomingMatchConflict)
		}
		duplicateMember := false
		for _, member := range group.fileMembers {
			if member == source.Capture.VariantID {
				duplicateMember = true
				break
			}
		}
		if !duplicateMember {
			group.fileMembers = append(group.fileMembers, source.Capture.VariantID)
		}
	}
	for member := range destinations {
		if !memberSeen[member] {
			return nil, fmt.Errorf("transfer Local recheck sources: destination names a foreign file member: %w", ErrIncomingMatchConflict)
		}
	}

	ids := make([]uuid.UUID, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	ordered := make([]matchingLocalRecheckTransferGroup, 0, len(ids))
	for _, id := range ids {
		group := groups[id]
		sort.Slice(group.fileMembers, func(i, j int) bool {
			return group.fileMembers[i].String() < group.fileMembers[j].String()
		})
		ordered = append(ordered, *group)
	}
	return ordered, nil
}

// matchingLocalRecheckValidateHeldSourcesTx requires the current source-hold
// rows to still name exactly the immutable incumbent scope. The rows are the
// cleanup veto, not editable progress.
func matchingLocalRecheckValidateHeldSourcesTx(ctx context.Context, tx bun.Tx, recheckID uuid.UUID, incumbent matchingLocalRecheckSnapshot) error {
	held := make([]matchingLocalRecheckHeldSource, 0, len(incumbent.Sources))
	if err := tx.NewRaw(`SELECT work_id,source_location_id,source_root_id,media_variant_id,unlink_target
		FROM matching_local_recheck_source WHERE recheck_id=? ORDER BY work_id FOR UPDATE`,
		recheckID).Scan(ctx, &held); err != nil {
		return fmt.Errorf("transfer Local recheck sources: read source holds: %w", err)
	}
	if len(held) != len(incumbent.Sources) {
		return fmt.Errorf("transfer Local recheck sources: source hold scope changed: %w", ErrIncomingMatchConflict)
	}
	byWork := make(map[uuid.UUID]matchingLocalRecheckHeldSource, len(held))
	for _, row := range held {
		if _, exists := byWork[row.WorkID]; exists {
			return fmt.Errorf("transfer Local recheck sources: source hold scope is ambiguous: %w", ErrIncomingMatchConflict)
		}
		byWork[row.WorkID] = row
	}
	for _, source := range incumbent.Sources {
		row, exists := byWork[source.WorkID]
		if !exists || row.LocationID != source.SourceLocationID || row.RootID != source.SourceRootID ||
			row.MediaVariantID != source.MediaVariantID {
			return fmt.Errorf("transfer Local recheck sources: source hold identity changed: %w", ErrIncomingMatchConflict)
		}
		target, err := matchingLocalRecheckUnlinkTarget(source.Capture.ConfiguredPath, source.Capture.RelativePath)
		if err != nil {
			return fmt.Errorf("transfer Local recheck sources: resolve source hold target: %w", err)
		}
		if target == "" || row.UnlinkTarget != target {
			return fmt.Errorf("transfer Local recheck sources: source hold target changed: %w", ErrIncomingMatchConflict)
		}
	}
	return nil
}

// matchingLocalRecheckValidateCurrentSourcesTx re-reads the protected physical
// sources. Analysis-derived facts may change; the exact file identity, root
// availability and cleanup ownership may not.
func matchingLocalRecheckValidateCurrentSourcesTx(ctx context.Context, tx bun.Tx, incumbent matchingLocalRecheckSnapshot) error {
	locationIDs := make([]uuid.UUID, 0, len(incumbent.Sources))
	seenLocation := make(map[uuid.UUID]bool, len(incumbent.Sources))
	for _, source := range incumbent.Sources {
		if !seenLocation[source.SourceLocationID] {
			seenLocation[source.SourceLocationID] = true
			locationIDs = append(locationIDs, source.SourceLocationID)
		}
	}
	loaded, err := loadIncomingGroupingCapturesForLocations(ctx, tx, locationIDs)
	if err != nil {
		return fmt.Errorf("transfer Local recheck sources: read current captures: %w", err)
	}
	current := make(map[uuid.UUID]IncomingGroupingCapture, len(loaded))
	for _, capture := range loaded {
		if _, exists := current[capture.LocationID]; exists {
			return fmt.Errorf("transfer Local recheck sources: current capture scope is ambiguous: %w", ErrIncomingMatchConflict)
		}
		current[capture.LocationID] = capture
	}
	for _, source := range incumbent.Sources {
		capture, exists := current[source.SourceLocationID]
		if !exists {
			return fmt.Errorf("transfer Local recheck sources: protected source location is missing: %w", ErrIncomingMatchConflict)
		}
		expected := source.Capture
		expected.TagEncoding = source.TagEncoding
		if !jsonObject(expected.TagEncoding) {
			expected.TagEncoding = nil
		}
		if !matchingLocalRecheckCaptureEqual(capture, expected) {
			return fmt.Errorf("transfer Local recheck sources: protected source capture changed: %w", ErrIncomingMatchConflict)
		}
		target, err := matchingLocalRecheckUnlinkTarget(source.Capture.ConfiguredPath, source.Capture.RelativePath)
		if err != nil {
			return fmt.Errorf("transfer Local recheck sources: resolve protected source target: %w", err)
		}
		owner, err := matchingLocalRecheckCleanupOwnerExists(ctx, tx, source.WorkID, source.SourceLocationID, target)
		if err != nil {
			return fmt.Errorf("transfer Local recheck sources: check cleanup owner: %w", err)
		}
		if owner {
			return fmt.Errorf("transfer Local recheck sources: protected source is cleanup-owned: %w", ErrIncomingMatchConflict)
		}
		var completed bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM source_cleanup_completion
			WHERE work_id=? AND source_root_id=? AND source_location_id=? AND media_variant_id=?
		)`, source.WorkID, source.SourceRootID, source.SourceLocationID, source.MediaVariantID).Scan(ctx, &completed); err != nil {
			return fmt.Errorf("transfer Local recheck sources: check source cleanup completion: %w", err)
		}
		if completed {
			return fmt.Errorf("transfer Local recheck sources: protected source cleanup already completed: %w", ErrIncomingMatchConflict)
		}
	}
	return nil
}

func matchingLocalRecheckTransferGroupTx(
	ctx context.Context,
	tx bun.Tx,
	recheckID uuid.UUID,
	incumbent matchingLocalRecheckSnapshot,
	group matchingLocalRecheckTransferGroup,
	binding matchingLocalRecheckProvenPosition,
	moveAuthorized bool,
) (MatchingLocalRecheckTransferAssociation, bool, error) {
	source := group.source
	result := MatchingLocalRecheckTransferAssociation{
		TrackSourceID:             source.TrackSourceID,
		ProviderTrackID:           source.ProviderTrackID,
		LocalTrackID:              source.LocalTrackID,
		ReleaseTrackID:            source.ReleaseTrackID,
		MediaVariantID:            source.MediaVariantID,
		FileMemberIDs:             append([]uuid.UUID(nil), group.fileMembers...),
		Inserted:                  source.TrackSourceInserted,
		DestinationReleaseTrackID: binding.ReleaseTrackID,
		DestinationLocalTrackID:   binding.LocalTrackID,
		DestinationBinding:        binding.binding,
	}

	var association matchingLocalRecheckIncumbentAssociation
	err := tx.NewRaw(`SELECT track_id,media_variant_id,selected,compatible,decision_method,policy_version,source_metadata_result_id
		FROM track_source WHERE id=? FOR UPDATE`, source.TrackSourceID).Scan(ctx, &association)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, fmt.Errorf("transfer Local recheck sources: owned association is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return result, false, fmt.Errorf("transfer Local recheck sources: read owned association: %w", err)
	}
	if association.TrackID != source.LocalTrackID || association.MediaVariantID != source.MediaVariantID ||
		!sameOptionalUUID(association.SourceMetadataResultID, source.TrackSourceMetadata) {
		return result, false, fmt.Errorf("transfer Local recheck sources: owned association identity changed: %w", ErrIncomingMatchConflict)
	}
	if association.DecisionMethod == "manual" {
		return result, false, fmt.Errorf("transfer Local recheck sources: owned association became manual: %w", ErrMatchingManualProtection)
	}
	if association.DecisionMethod != "automatic" {
		return result, false, fmt.Errorf("transfer Local recheck sources: owned association decision is invalid: %w", ErrIncomingMatchConflict)
	}
	if !association.Compatible {
		return result, false, fmt.Errorf("transfer Local recheck sources: owned association is incompatible: %w", ErrIncomingMatchConflict)
	}
	result.Selected = association.Selected

	if !source.TrackSourceInserted {
		result.Retained = true
		result.Retention = MatchingLocalRecheckRetainedReused
		return result, false, nil
	}
	result.Owned = true
	if association.PolicyVersion == nil {
		return result, false, fmt.Errorf("transfer Local recheck sources: owned automatic association has no policy: %w", ErrIncomingMatchConflict)
	}
	if *association.PolicyVersion != MatchingLayoutPolicyVersion {
		result.Retained = true
		result.Retention = MatchingLocalRecheckRetainedForeign
		return result, false, nil
	}

	if err := matchingLocalRecheckValidateOwnedSourceTx(ctx, tx, incumbent, source); err != nil {
		return result, false, err
	}
	if group.destinationReleaseTrackID == source.ReleaseTrackID {
		if binding.LocalTrackID != source.LocalTrackID {
			return result, false, fmt.Errorf("transfer Local recheck sources: same-position destination changed the local track: %w", ErrIncomingMatchConflict)
		}
		result.SamePosition = true
		result.Retained = true
		result.Retention = MatchingLocalRecheckRetainedSamePosition
		result.DestinationTrackSourceID = source.TrackSourceID
		return result, false, nil
	}
	if binding.LocalTrackID == source.LocalTrackID {
		result.Retained = true
		result.Retention = MatchingLocalRecheckRetainedSharedTrack
		result.DestinationTrackSourceID = source.TrackSourceID
		return result, association.Selected && !moveAuthorized, nil
	}

	if association.Selected {
		result.Retained = true
		result.Retention = MatchingLocalRecheckRetainedSelected
		if !moveAuthorized {
			return result, true, nil
		}
		id, created, err := matchingLocalRecheckEnsureDestinationAssociationTx(ctx, tx, binding.LocalTrackID, source, association.SourceMetadataResultID)
		result.DestinationTrackSourceID, result.DestinationCreated = id, created
		return result, false, err
	}
	var held bool
	if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM publication_source_hold WHERE media_variant_id=?)`,
		source.MediaVariantID).Scan(ctx, &held); err != nil {
		return result, false, fmt.Errorf("transfer Local recheck sources: check publication source hold: %w", err)
	}
	if held {
		result.Retained = true
		result.Retention = MatchingLocalRecheckRetainedHeld
		return result, false, nil
	}
	shared, err := matchingLocalRecheckTransferAssociationSharedTx(ctx, tx, recheckID, source.TrackSourceID)
	if err != nil {
		return result, false, err
	}
	if shared {
		result.Shared = true
		result.Retained = true
		result.Retention = MatchingLocalRecheckRetainedSharedRecheck
		return result, false, nil
	}

	destinationTrackSourceID, created, err := matchingLocalRecheckEnsureDestinationAssociationTx(
		ctx, tx, binding.LocalTrackID, source, association.SourceMetadataResultID)
	if err != nil {
		return result, false, err
	}
	result.DestinationTrackSourceID = destinationTrackSourceID
	result.DestinationCreated = created

	// track_source is track-level. A different library position using the
	// same old track still consumes the existing association even though this
	// recheck originally inserted it.
	if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM release_track WHERE track_id=? AND id<>?)`,
		source.LocalTrackID, source.ReleaseTrackID).Scan(ctx, &shared); err != nil {
		return result, false, fmt.Errorf("transfer local recheck sources: check shared library track: %w", err)
	}
	if shared {
		result.Shared = true
		result.Retained = true
		result.Retention = MatchingLocalRecheckRetainedSharedTrack
		return result, false, nil
	}

	deleted, err := tx.NewRaw(`DELETE FROM track_source
		WHERE id=? AND track_id=? AND media_variant_id=? AND decision_method='automatic'
		AND policy_version=? AND selected IS FALSE AND compatible IS TRUE`,
		source.TrackSourceID, source.LocalTrackID, source.MediaVariantID, MatchingLayoutPolicyVersion).Exec(ctx)
	if err != nil {
		return result, false, fmt.Errorf("transfer Local recheck sources: delete owned association: %w", err)
	}
	affected, err := deleted.RowsAffected()
	if err != nil {
		return result, false, fmt.Errorf("transfer Local recheck sources: read deleted association count: %w", err)
	}
	if affected != 1 {
		return result, false, fmt.Errorf("transfer Local recheck sources: owned association changed during transfer: %w", ErrIncomingMatchConflict)
	}
	result.Removed = true
	return result, false, nil
}

// matchingLocalRecheckValidateOwnedSourceTx re-proves the Local provider
// binding and the incumbent library position. The Local binding is read, never
// deleted or rewritten.
func matchingLocalRecheckValidateOwnedSourceTx(
	ctx context.Context,
	tx bun.Tx,
	incumbent matchingLocalRecheckSnapshot,
	source matchingLocalRecheckSnapshotSource,
) error {
	var boundTrackID uuid.UUID
	err := tx.NewRaw(`SELECT local_track_id FROM provider_local_track
		WHERE provider_id=? AND provider_track_id=? FOR SHARE`,
		localProviderID, source.ProviderTrackID).Scan(ctx, &boundTrackID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("transfer Local recheck sources: Local provider track binding is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return fmt.Errorf("transfer Local recheck sources: read Local provider track binding: %w", err)
	}
	if boundTrackID != source.LocalTrackID {
		return fmt.Errorf("transfer Local recheck sources: Local provider track binding changed: %w", ErrIncomingMatchConflict)
	}

	var providerTrack struct {
		ReleaseID uuid.UUID `bun:"release_id"`
		Medium    int       `bun:"medium"`
		Position  int       `bun:"position"`
	}
	err = tx.NewRaw(`SELECT release_id,medium,position FROM provider_release_track
		WHERE provider_id=? AND id=? AND present IS TRUE FOR SHARE`,
		localProviderID, source.ProviderTrackID).Scan(ctx, &providerTrack)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("transfer Local recheck sources: Local provider track is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return fmt.Errorf("transfer Local recheck sources: read Local provider track: %w", err)
	}
	if providerTrack.ReleaseID != incumbent.ProviderReleaseID {
		return fmt.Errorf("transfer Local recheck sources: Local provider track changed release: %w", ErrIncomingMatchConflict)
	}

	var position struct {
		ReleaseID uuid.UUID `bun:"release_id"`
		TrackID   uuid.UUID `bun:"track_id"`
		Medium    int       `bun:"medium"`
		Position  int       `bun:"position"`
	}
	err = tx.NewRaw(`SELECT release_id,track_id,medium,position FROM release_track
		WHERE id=? FOR SHARE`, source.ReleaseTrackID).Scan(ctx, &position)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("transfer Local recheck sources: incumbent library position is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return fmt.Errorf("transfer Local recheck sources: read incumbent library position: %w", err)
	}
	if position.ReleaseID != incumbent.LocalReleaseID || position.TrackID != source.LocalTrackID ||
		position.Medium != providerTrack.Medium || position.Position != providerTrack.Position {
		return fmt.Errorf("transfer Local recheck sources: incumbent library position changed: %w", ErrIncomingMatchConflict)
	}
	return nil
}

type matchingLocalRecheckProvenPosition struct {
	ReleaseTrackID uuid.UUID
	LocalReleaseID uuid.UUID
	LocalTrackID   uuid.UUID
	Medium         int
	Position       int
	binding        MatchingLocalRecheckTransferMusicBrainzBinding
}

// matchingLocalRecheckProvenMusicBrainzPositionTx proves that the exact library
// position already has an established MusicBrainz binding. Matching ordinals
// alone never prove it: the position's own local release must be bound to the
// MusicBrainz release, and the position's own local track must be bound to a
// present provider track of that release at the same ordinal.
func matchingLocalRecheckProvenMusicBrainzPositionTx(
	ctx context.Context,
	tx bun.Tx,
	providerSourceID, releaseTrackID uuid.UUID,
) (matchingLocalRecheckProvenPosition, error) {
	var result matchingLocalRecheckProvenPosition
	err := tx.NewRaw(`SELECT release_id,track_id,medium,position FROM release_track
		WHERE id=? FOR SHARE`, releaseTrackID).Scan(ctx,
		&result.LocalReleaseID, &result.LocalTrackID, &result.Medium, &result.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("transfer Local recheck sources: destination library position is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return result, fmt.Errorf("transfer Local recheck sources: read destination library position: %w", err)
	}

	rows := make([]struct {
		ProviderID        uuid.UUID `bun:"provider_id"`
		ProviderReleaseID uuid.UUID `bun:"provider_release_id"`
		ProviderTrackID   uuid.UUID `bun:"provider_track_id"`
		LocalTrackID      uuid.UUID `bun:"local_track_id"`
		Method            string    `bun:"method"`
		PolicyVersion     *string   `bun:"policy_version"`
		Medium            int       `bun:"medium"`
		Position          int       `bun:"position"`
	}, 0, 1)
	err = tx.NewRaw(`SELECT binding.provider_id, track.release_id AS provider_release_id, track.track_id AS provider_track_id,
			binding.local_track_id, binding.method, binding.policy_version, track.medium, track.position
		FROM provider_local_release release_binding
		JOIN provider ON provider.id=release_binding.provider_id AND provider.code='musicbrainz'
		JOIN provider_local_track binding
			ON binding.provider_id=release_binding.provider_id AND binding.local_track_id=?
		JOIN provider_source_release_track track
			ON track.provider_id=binding.provider_id AND track.track_id=binding.provider_track_id
		WHERE track.provider_source_id=? AND release_binding.local_release_id=?
			AND track.release_id=release_binding.provider_release_id
			AND track.present IS TRUE
			AND track.medium=? AND track.position=?
		ORDER BY binding.provider_id,track.track_id
		FOR SHARE OF release_binding,binding,track,provider`,
		result.LocalTrackID, providerSourceID, result.LocalReleaseID, result.Medium, result.Position).Scan(ctx, &rows)
	if err != nil {
		return result, fmt.Errorf("transfer Local recheck sources: read MusicBrainz position binding: %w", err)
	}
	if len(rows) != 1 {
		return result, fmt.Errorf("transfer Local recheck sources: destination position has no unique proven MusicBrainz binding: %w", ErrIncomingMatchConflict)
	}
	row := rows[0]
	if row.LocalTrackID != result.LocalTrackID || row.Medium != result.Medium || row.Position != result.Position {
		return result, fmt.Errorf("transfer Local recheck sources: destination MusicBrainz binding is inconsistent: %w", ErrIncomingMatchConflict)
	}
	if row.Method == "manual" && row.PolicyVersion != nil {
		return result, fmt.Errorf("transfer Local recheck sources: manual MusicBrainz binding carries a policy: %w", ErrIncomingMatchConflict)
	}
	if row.Method == "automatic" && (row.PolicyVersion == nil || *row.PolicyVersion != MatchingLayoutPolicyVersion) {
		return result, fmt.Errorf("transfer Local recheck sources: automatic MusicBrainz binding policy changed: %w", ErrIncomingMatchConflict)
	}
	if row.Method != "manual" && row.Method != "automatic" {
		return result, fmt.Errorf("transfer Local recheck sources: MusicBrainz binding method is invalid: %w", ErrIncomingMatchConflict)
	}
	result.ReleaseTrackID = releaseTrackID
	result.binding = MatchingLocalRecheckTransferMusicBrainzBinding{
		ProviderID:        row.ProviderID,
		ProviderReleaseID: row.ProviderReleaseID,
		ProviderTrackID:   row.ProviderTrackID,
		LocalReleaseID:    result.LocalReleaseID,
		LocalTrackID:      result.LocalTrackID,
		Method:            row.Method,
		PolicyVersion:     row.PolicyVersion,
		Medium:            row.Medium,
		Position:          row.Position,
	}
	return result, nil
}

func matchingLocalRecheckTransferAssociationSharedTx(
	ctx context.Context,
	tx bun.Tx,
	recheckID uuid.UUID,
	trackSourceID uuid.UUID,
) (bool, error) {
	var shared bool
	if err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM matching_local_recheck other
		WHERE other.id<>? AND other.state IN ('waiting','publishing')
		AND EXISTS (
			SELECT 1 FROM jsonb_array_elements(
				CASE WHEN jsonb_typeof(other.incumbent_snapshot->'sources')='array'
					THEN other.incumbent_snapshot->'sources'
					ELSE '[]'::jsonb END
			) entry
			WHERE entry->>'track_source_id'=?
		)
	)`, recheckID, trackSourceID.String()).Scan(ctx, &shared); err != nil {
		return false, fmt.Errorf("transfer Local recheck sources: check shared association evidence: %w", err)
	}
	return shared, nil
}

// matchingLocalRecheckEnsureDestinationAssociationTx never selects the
// destination and never overwrites a foreign or manual association.
func matchingLocalRecheckEnsureDestinationAssociationTx(
	ctx context.Context,
	tx bun.Tx,
	destinationLocalTrackID uuid.UUID,
	source matchingLocalRecheckSnapshotSource,
	incumbentMetadataResultID *uuid.UUID,
) (uuid.UUID, bool, error) {
	existing := make([]struct {
		ID             uuid.UUID `bun:"id"`
		Compatible     bool      `bun:"compatible"`
		DecisionMethod string    `bun:"decision_method"`
		PolicyVersion  *string   `bun:"policy_version"`
	}, 0, 1)
	if err := tx.NewRaw(`SELECT id,compatible,decision_method,policy_version
		FROM track_source WHERE track_id=? AND media_variant_id=? ORDER BY id FOR UPDATE`,
		destinationLocalTrackID, source.MediaVariantID).Scan(ctx, &existing); err != nil {
		return uuid.Nil, false, fmt.Errorf("transfer Local recheck sources: read destination association: %w", err)
	}
	if len(existing) > 1 {
		return uuid.Nil, false, fmt.Errorf("transfer Local recheck sources: destination association is ambiguous: %w", ErrIncomingMatchConflict)
	}
	if len(existing) == 1 {
		row := existing[0]
		if row.DecisionMethod == "manual" {
			return uuid.Nil, false, fmt.Errorf("transfer Local recheck sources: destination association is manual: %w", ErrMatchingManualProtection)
		}
		if row.DecisionMethod != "automatic" || !row.Compatible ||
			row.PolicyVersion == nil || *row.PolicyVersion != MatchingLayoutPolicyVersion {
			return uuid.Nil, false, fmt.Errorf("transfer Local recheck sources: destination association is foreign: %w", ErrIncomingMatchConflict)
		}
		return row.ID, false, nil
	}

	destinationID := uuid.New()
	metadataResultID := incumbentMetadataResultID
	if source.TrackSourceMetadata != nil {
		metadataResultID = source.TrackSourceMetadata
	}
	if _, err := tx.NewRaw(`INSERT INTO track_source
		(id,track_id,media_variant_id,selected,compatible,decision_method,policy_version,source_metadata_result_id)
		VALUES (?,?,?,false,true,'automatic',?,?)`,
		destinationID, destinationLocalTrackID, source.MediaVariantID, MatchingLayoutPolicyVersion, metadataResultID).Exec(ctx); err != nil {
		return uuid.Nil, false, fmt.Errorf("transfer Local recheck sources: create destination association: %w", err)
	}
	return destinationID, true, nil
}
