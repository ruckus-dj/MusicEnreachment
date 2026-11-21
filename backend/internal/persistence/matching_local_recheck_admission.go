package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ApplyAutomaticLocalFallbackTx materializes a complete, already authorized
// Local layout and acquires cleanup protection in the same transaction. The
// caller must hold the output, source, and catalog fences and roll back on any
// error. Neither a preview nor this helper authorizes publication.
func ApplyAutomaticLocalFallbackTx(ctx context.Context, tx bun.Tx, operationID uuid.UUID, preview MatchingLocalPreview, captures []IncomingGroupingCapture) (uuid.UUID, uuid.UUID, error) {
	if operationID == uuid.Nil || len(captures) == 0 {
		return uuid.Nil, uuid.Nil, ErrIncomingMatchConflict
	}
	localReleaseID, sources, err := materializeAutomaticMatchingLocalTx(ctx, tx, preview)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	recheckID, err := createMatchingLocalRecheckTx(ctx, tx, matchingLocalRecheckAdmission{
		OperationID: operationID, ProviderReleaseID: preview.ProviderReleaseID, LocalReleaseID: localReleaseID,
		Captures: captures, Sources: sources,
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return localReleaseID, recheckID, nil
}

type matchingLocalRecheckAdmission struct {
	OperationID       uuid.UUID
	ProviderReleaseID uuid.UUID
	LocalReleaseID    uuid.UUID
	// Captures are the exact current incoming grouping captures that backed the
	// layout. They are re-read below and only the authoritative persisted values
	// are written to the snapshot.
	Captures []IncomingGroupingCapture
	// Sources identifies the exact materialized association and library
	// position for every (file member, media variant) pair.
	Sources []matchingLocalMaterializedSource
}

// matchingLocalRecheckSnapshot is the immutable version-1 incumbent evidence.
// It is a witness, not editable recheck progress: the migration guard rejects
// any later change to this column.
type matchingLocalRecheckSnapshot struct {
	Version               int                                  `json:"version"`
	LocalApplyOperationID uuid.UUID                            `json:"local_apply_operation_id"`
	ProviderReleaseID     uuid.UUID                            `json:"provider_release_id"`
	LocalReleaseID        uuid.UUID                            `json:"local_release_id"`
	PolicyVersion         string                               `json:"policy_version"`
	Sources               []matchingLocalRecheckSnapshotSource `json:"sources"`
}

// matchingLocalRecheckSnapshotSource pins one exact physical capture to the
// association the automatic layout materialized. Several captures may share a
// media variant and therefore the same track_source row; each capture still
// gets its own immutable entry.
type matchingLocalRecheckSnapshotSource struct {
	WorkID              uuid.UUID               `json:"work_id"`
	SourceLocationID    uuid.UUID               `json:"source_location_id"`
	SourceRootID        uuid.UUID               `json:"source_root_id"`
	MediaVariantID      uuid.UUID               `json:"media_variant_id"`
	ProviderTrackID     uuid.UUID               `json:"provider_track_id"`
	ReleaseTrackID      uuid.UUID               `json:"release_track_id"`
	LocalTrackID        uuid.UUID               `json:"local_track_id"`
	TrackSourceID       uuid.UUID               `json:"track_source_id"`
	TrackSourceSelected bool                    `json:"track_source_selected"`
	TrackSourceInserted bool                    `json:"track_source_inserted"`
	TrackSourceMetadata *uuid.UUID              `json:"track_source_metadata_result_id"`
	Capture             IncomingGroupingCapture `json:"capture"`
	TagEncoding         json.RawMessage         `json:"tag_encoding"`
}

type matchingLocalRecheckCaptureIdentity struct {
	VariantID      uuid.UUID
	WorkID         uuid.UUID
	RootID         uuid.UUID
	LocationID     uuid.UUID
	MediaVariantID uuid.UUID
}

type matchingLocalRecheckSourceIdentity struct {
	FileMemberID   uuid.UUID
	MediaVariantID uuid.UUID
}

type matchingLocalRecheckValidatedSource struct {
	providerTrackID uuid.UUID
	releaseTrackID  uuid.UUID
	localTrackID    uuid.UUID
	trackSourceID   uuid.UUID
	selected        bool
	inserted        bool
	metadataResult  *uuid.UUID
}

type matchingLocalRecheckChild struct {
	workID           uuid.UUID
	sourceLocationID uuid.UUID
	sourceRootID     uuid.UUID
	mediaVariantID   uuid.UUID
	unlinkTarget     string
}

// createMatchingLocalRecheckTx writes the durable source veto for one automatic
// Local layout. It persists evidence only; it never schedules, enqueues or
// otherwise activates a recheck.
//
// The caller must already hold the output admission gate, the source mutation
// gate and the provider/catalog locks. The supplied captures must be the
// current prevalidated matching captures; this function re-reads them by
// location and refuses any scope or fact drift.
func createMatchingLocalRecheckTx(ctx context.Context, tx bun.Tx, input matchingLocalRecheckAdmission) (uuid.UUID, error) {
	if err := ctx.Err(); err != nil {
		return uuid.Nil, err
	}
	if input.OperationID == uuid.Nil || input.ProviderReleaseID == uuid.Nil || input.LocalReleaseID == uuid.Nil {
		return uuid.Nil, errors.New("create Local recheck: operation and release identities are required")
	}
	if len(input.Captures) == 0 || len(input.Sources) == 0 {
		return uuid.Nil, errors.New("create Local recheck: sources and captures are required")
	}

	sourceByKey := make(map[matchingLocalRecheckSourceIdentity]matchingLocalMaterializedSource, len(input.Sources))
	for _, source := range input.Sources {
		if source.FileMemberID == uuid.Nil || source.MediaVariantID == uuid.Nil || source.ProviderTrackID == uuid.Nil ||
			source.LocalTrackID == uuid.Nil || source.ReleaseTrackID == uuid.Nil || source.TrackSourceID == uuid.Nil {
			return uuid.Nil, errors.New("create Local recheck: materialized source identity is invalid")
		}
		key := matchingLocalRecheckSourceIdentity{FileMemberID: source.FileMemberID, MediaVariantID: source.MediaVariantID}
		if _, exists := sourceByKey[key]; exists {
			return uuid.Nil, errors.New("create Local recheck: materialized sources are ambiguous")
		}
		sourceByKey[key] = source
	}

	locationIDs := make([]uuid.UUID, 0, len(input.Captures))
	seenLocations := make(map[uuid.UUID]bool, len(input.Captures))
	requested := make(map[matchingLocalRecheckCaptureIdentity]IncomingGroupingCapture, len(input.Captures))
	for _, capture := range input.Captures {
		identity, ok := matchingLocalRecheckCaptureIdentityOf(capture)
		if !ok {
			return uuid.Nil, fmt.Errorf("create Local recheck: capture identity is incomplete: %w", ErrIncomingMatchConflict)
		}
		if _, exists := requested[identity]; exists {
			return uuid.Nil, fmt.Errorf("create Local recheck: capture scope is ambiguous: %w", ErrIncomingMatchConflict)
		}
		requested[identity] = capture
		if !seenLocations[capture.LocationID] {
			seenLocations[capture.LocationID] = true
			locationIDs = append(locationIDs, capture.LocationID)
		}
	}

	loaded, err := loadIncomingGroupingCapturesForLocations(ctx, tx, locationIDs)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create Local recheck: read current captures: %w", err)
	}
	if len(loaded) == 0 {
		return uuid.Nil, fmt.Errorf("create Local recheck: current capture scope is empty: %w", ErrIncomingMatchConflict)
	}
	authoritative := make(map[matchingLocalRecheckCaptureIdentity]IncomingGroupingCapture, len(loaded))
	for _, capture := range loaded {
		identity, ok := matchingLocalRecheckCaptureIdentityOf(capture)
		if !ok {
			continue
		}
		if _, exists := authoritative[identity]; exists {
			return uuid.Nil, fmt.Errorf("create Local recheck: current capture scope is ambiguous: %w", ErrIncomingMatchConflict)
		}
		authoritative[identity] = capture
	}
	if err := validateMatchingLocalRecheckReleaseBinding(ctx, tx, input.ProviderReleaseID, input.LocalReleaseID); err != nil {
		return uuid.Nil, err
	}
	validatedSources := make(map[matchingLocalRecheckSourceIdentity]matchingLocalRecheckValidatedSource, len(sourceByKey))
	for key, source := range sourceByKey {
		validated, err := validateMatchingLocalRecheckSourceBinding(ctx, tx, input.ProviderReleaseID, input.LocalReleaseID, source)
		if err != nil {
			return uuid.Nil, err
		}
		validatedSources[key] = validated
	}

	usedSources := make(map[matchingLocalRecheckSourceIdentity]bool, len(sourceByKey))
	children := make([]matchingLocalRecheckChild, 0, len(input.Captures))
	snapshotSources := make([]matchingLocalRecheckSnapshotSource, 0, len(input.Captures))
	for _, expected := range input.Captures {
		identity, ok := matchingLocalRecheckCaptureIdentityOf(expected)
		if !ok {
			return uuid.Nil, fmt.Errorf("create Local recheck: capture identity is incomplete: %w", ErrIncomingMatchConflict)
		}
		current, ok := authoritative[identity]
		if !ok || !matchingLocalRecheckCaptureEqual(expected, current) {
			return uuid.Nil, fmt.Errorf("create Local recheck: current capture facts changed: %w", ErrIncomingMatchConflict)
		}
		sourceKey := matchingLocalRecheckSourceIdentity{
			FileMemberID:   current.VariantID,
			MediaVariantID: *current.MediaVariantID,
		}
		validated, ok := validatedSources[sourceKey]
		if !ok {
			return uuid.Nil, fmt.Errorf("create Local recheck: capture has no materialized source: %w", ErrIncomingMatchConflict)
		}
		usedSources[sourceKey] = true

		unlinkTarget, err := matchingLocalRecheckUnlinkTarget(current.ConfiguredPath, current.RelativePath)
		if err != nil {
			return uuid.Nil, fmt.Errorf("create Local recheck: resolve unlink target: %w", err)
		}
		if unlinkTarget == "" {
			return uuid.Nil, errors.New("create Local recheck: unlink target is empty")
		}
		owned, err := matchingLocalRecheckCleanupOwnerExists(ctx, tx, current.WorkID, current.LocationID, unlinkTarget)
		if err != nil {
			return uuid.Nil, fmt.Errorf("create Local recheck: check cleanup owner: %w", err)
		}
		if owned {
			return uuid.Nil, fmt.Errorf("create Local recheck: source is owned by cleanup: %w", ErrIncomingMatchConflict)
		}
		var completed bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM source_cleanup_completion
			WHERE work_id=? AND source_root_id=? AND source_location_id=? AND media_variant_id=?
		)`, current.WorkID, current.RootID, current.LocationID, *current.MediaVariantID).Scan(ctx, &completed); err != nil {
			return uuid.Nil, fmt.Errorf("create Local recheck: check completed cleanup: %w", err)
		}
		if completed {
			return uuid.Nil, fmt.Errorf("create Local recheck: source cleanup already completed: %w", ErrIncomingMatchConflict)
		}

		children = append(children, matchingLocalRecheckChild{
			workID:           current.WorkID,
			sourceLocationID: current.LocationID,
			sourceRootID:     current.RootID,
			mediaVariantID:   *current.MediaVariantID,
			unlinkTarget:     unlinkTarget,
		})
		snapshotSources = append(snapshotSources, matchingLocalRecheckSnapshotSource{
			WorkID:              current.WorkID,
			SourceLocationID:    current.LocationID,
			SourceRootID:        current.RootID,
			MediaVariantID:      *current.MediaVariantID,
			ProviderTrackID:     validated.providerTrackID,
			ReleaseTrackID:      validated.releaseTrackID,
			LocalTrackID:        validated.localTrackID,
			TrackSourceID:       validated.trackSourceID,
			TrackSourceSelected: validated.selected,
			TrackSourceInserted: validated.inserted,
			TrackSourceMetadata: validated.metadataResult,
			Capture:             current,
			TagEncoding:         append(json.RawMessage(nil), current.TagEncoding...),
		})
	}
	for key := range sourceByKey {
		if !usedSources[key] {
			return uuid.Nil, fmt.Errorf("create Local recheck: materialized source has no current capture: %w", ErrIncomingMatchConflict)
		}
	}

	snapshot := matchingLocalRecheckSnapshot{
		Version:               1,
		LocalApplyOperationID: input.OperationID,
		ProviderReleaseID:     input.ProviderReleaseID,
		LocalReleaseID:        input.LocalReleaseID,
		PolicyVersion:         MatchingLayoutPolicyVersion,
		Sources:               snapshotSources,
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create Local recheck: encode incumbent snapshot: %w", err)
	}
	recheckID := uuid.New()
	if _, err := tx.NewRaw(`INSERT INTO matching_local_recheck
		(id,local_apply_operation_id,state,next_recheck_at,incumbent_snapshot)
		VALUES (?,?,'waiting',now(),?::jsonb)`, recheckID, input.OperationID, string(encoded)).Exec(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("create Local recheck: insert incumbent: %w", err)
	}
	for _, child := range children {
		if _, err := tx.NewRaw(`INSERT INTO matching_local_recheck_source
			(recheck_id,work_id,source_location_id,source_root_id,media_variant_id,unlink_target)
			VALUES (?,?,?,?,?,?)`,
			recheckID, child.workID, child.sourceLocationID, child.sourceRootID, child.mediaVariantID, child.unlinkTarget).Exec(ctx); err != nil {
			return uuid.Nil, fmt.Errorf("create Local recheck: insert source hold: %w", err)
		}
	}
	return recheckID, nil
}

func validateMatchingLocalRecheckReleaseBinding(ctx context.Context, tx bun.Tx, providerReleaseID, localReleaseID uuid.UUID) error {
	var boundReleaseID uuid.UUID
	err := tx.NewRaw(`SELECT local_release_id FROM provider_local_release WHERE provider_id=? AND provider_release_id=?`,
		localProviderID, providerReleaseID).Scan(ctx, &boundReleaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("create Local recheck: Local provider release binding is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return fmt.Errorf("create Local recheck: read Local provider release binding: %w", err)
	}
	if boundReleaseID != localReleaseID {
		return fmt.Errorf("create Local recheck: Local provider release binding changed: %w", ErrIncomingMatchConflict)
	}
	return nil
}

// validateMatchingLocalRecheckSourceBinding requires the exact Local provider
// tracks, library position and automatic association. It deliberately accepts a
// manual provider binding: manual Local edition creation must not hide the
// automatic association this recheck owns.
func validateMatchingLocalRecheckSourceBinding(ctx context.Context, tx bun.Tx, providerReleaseID, localReleaseID uuid.UUID, source matchingLocalMaterializedSource) (matchingLocalRecheckValidatedSource, error) {
	result := matchingLocalRecheckValidatedSource{
		providerTrackID: source.ProviderTrackID,
		releaseTrackID:  source.ReleaseTrackID,
		localTrackID:    source.LocalTrackID,
		trackSourceID:   source.TrackSourceID,
		inserted:        source.Inserted,
	}
	var providerTrack struct {
		ReleaseID uuid.UUID `bun:"release_id"`
		Medium    int       `bun:"medium"`
		Position  int       `bun:"position"`
	}
	err := tx.NewRaw(`SELECT release_id,medium,position FROM provider_release_track
		WHERE provider_id=? AND id=? AND present IS TRUE`, localProviderID, source.ProviderTrackID).Scan(ctx, &providerTrack)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("create Local recheck: chosen provider release track is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return result, fmt.Errorf("create Local recheck: read chosen provider release track: %w", err)
	}
	if providerTrack.ReleaseID != providerReleaseID {
		return result, fmt.Errorf("create Local recheck: chosen provider release track changed release: %w", ErrIncomingMatchConflict)
	}

	var boundTrackID uuid.UUID
	err = tx.NewRaw(`SELECT local_track_id FROM provider_local_track WHERE provider_id=? AND provider_track_id=?`,
		localProviderID, source.ProviderTrackID).Scan(ctx, &boundTrackID)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("create Local recheck: Local provider track binding is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return result, fmt.Errorf("create Local recheck: read Local provider track binding: %w", err)
	}
	if boundTrackID != source.LocalTrackID {
		return result, fmt.Errorf("create Local recheck: Local provider track binding changed: %w", ErrIncomingMatchConflict)
	}

	var position struct {
		ReleaseID uuid.UUID `bun:"release_id"`
		TrackID   uuid.UUID `bun:"track_id"`
		Medium    int       `bun:"medium"`
		Position  int       `bun:"position"`
	}
	err = tx.NewRaw(`SELECT release_id,track_id,medium,position FROM release_track WHERE id=?`, source.ReleaseTrackID).Scan(ctx, &position)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("create Local recheck: library position is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return result, fmt.Errorf("create Local recheck: read library position: %w", err)
	}
	if position.ReleaseID != localReleaseID || position.TrackID != source.LocalTrackID ||
		position.Medium != providerTrack.Medium || position.Position != providerTrack.Position {
		return result, fmt.Errorf("create Local recheck: library position changed: %w", ErrIncomingMatchConflict)
	}

	var association struct {
		TrackID                uuid.UUID  `bun:"track_id"`
		MediaVariantID         uuid.UUID  `bun:"media_variant_id"`
		Selected               bool       `bun:"selected"`
		Compatible             bool       `bun:"compatible"`
		DecisionMethod         string     `bun:"decision_method"`
		PolicyVersion          *string    `bun:"policy_version"`
		SourceMetadataResultID *uuid.UUID `bun:"source_metadata_result_id"`
	}
	err = tx.NewRaw(`SELECT track_id,media_variant_id,selected,compatible,decision_method,policy_version,source_metadata_result_id
		FROM track_source WHERE id=? FOR UPDATE`, source.TrackSourceID).Scan(ctx, &association)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("create Local recheck: track source association is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return result, fmt.Errorf("create Local recheck: read track source association: %w", err)
	}
	if association.TrackID != source.LocalTrackID || association.MediaVariantID != source.MediaVariantID ||
		!association.Compatible || association.DecisionMethod != "automatic" ||
		association.PolicyVersion == nil || *association.PolicyVersion != MatchingLayoutPolicyVersion {
		return result, fmt.Errorf("create Local recheck: track source association changed: %w", ErrIncomingMatchConflict)
	}
	result.selected = association.Selected
	if association.SourceMetadataResultID != nil {
		metadataResult := *association.SourceMetadataResultID
		result.metadataResult = &metadataResult
	}
	return result, nil
}

func matchingLocalRecheckCaptureIdentityOf(capture IncomingGroupingCapture) (matchingLocalRecheckCaptureIdentity, bool) {
	if !capture.Ready || !capture.RootEnabled || capture.RootStale || capture.CaptureAnalysisID == nil ||
		*capture.CaptureAnalysisID == uuid.Nil || capture.VariantID == uuid.Nil || capture.WorkID == uuid.Nil || capture.RootID == uuid.Nil ||
		capture.LocationID == uuid.Nil || capture.MediaVariantID == nil || *capture.MediaVariantID == uuid.Nil {
		return matchingLocalRecheckCaptureIdentity{}, false
	}
	return matchingLocalRecheckCaptureIdentity{
		VariantID:      capture.VariantID,
		WorkID:         capture.WorkID,
		RootID:         capture.RootID,
		LocationID:     capture.LocationID,
		MediaVariantID: *capture.MediaVariantID,
	}, true
}

func matchingLocalRecheckCaptureEqual(left, right IncomingGroupingCapture) bool {
	leftJSON, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		return false
	}
	return sameJSONValue(leftJSON, rightJSON) && matchingLocalRecheckJSONEqual(left.TagEncoding, right.TagEncoding)
}

func matchingLocalRecheckJSONEqual(left, right json.RawMessage) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == 0 && len(right) == 0
	}
	return sameJSONValue(left, right)
}
