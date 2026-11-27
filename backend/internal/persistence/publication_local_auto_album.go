package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

const publicationAutoAlbumLocalLayoutVersion = 1

// publicationAutoAlbumLocalSource is the exact source provenance frozen into a
// Local automatic-album grant. It is intentionally independent of the incoming
// group and of the replaceable recheck obligation.
type publicationAutoAlbumLocalSource struct {
	FileMemberID        uuid.UUID       `json:"file_member_id"`
	WorkID              uuid.UUID       `json:"work_id"`
	SourceRootID        uuid.UUID       `json:"source_root_id"`
	SourceLocationID    uuid.UUID       `json:"source_location_id"`
	MediaVariantID      uuid.UUID       `json:"media_variant_id"`
	ProviderTrackID     uuid.UUID       `json:"provider_track_id"`
	ReleaseTrackID      uuid.UUID       `json:"release_track_id"`
	LocalTrackID        uuid.UUID       `json:"local_track_id"`
	TrackSourceID       uuid.UUID       `json:"track_source_id"`
	TrackSourceInserted bool            `json:"track_source_inserted"`
	ConfiguredPath      string          `json:"configured_path"`
	InventoryPath       string          `json:"inventory_path"`
	RelativePath        string          `json:"relative_path"`
	SizeBytes           int64           `json:"size_bytes"`
	MTime               time.Time       `json:"mtime"`
	SourceSHA256        []byte          `json:"source_sha256,omitempty"`
	TagEncoding         json.RawMessage `json:"tag_encoding,omitempty"`
}

// publicationAutoAlbumLocalLayout is the durable Local side of
// publication_automatic_album. It anchors the grant to the succeeded Local
// apply operation rather than to matching_local_recheck, which a retained Local
// outcome deliberately deletes.
type publicationAutoAlbumLocalLayout struct {
	Version                   int                               `json:"version"`
	LocalApplyOperationID     uuid.UUID                         `json:"local_apply_operation_id"`
	GroupID                   uuid.UUID                         `json:"group_id"`
	GroupRevision             string                            `json:"group_revision"`
	AuthorityRevision         int64                             `json:"authority_revision"`
	OutageRunID               uuid.UUID                         `json:"outage_run_id"`
	ObservedRequestGeneration int64                             `json:"observed_request_generation"`
	HighWater                 int64                             `json:"high_water"`
	ProviderReleaseID         uuid.UUID                         `json:"provider_release_id"`
	LocalReleaseID            uuid.UUID                         `json:"local_release_id"`
	PolicyVersion             string                            `json:"policy_version"`
	DecisionDigest            string                            `json:"decision_digest"`
	CatalogDigest             string                            `json:"catalog_digest"`
	LayoutTarget              json.RawMessage                   `json:"layout_target"`
	Sources                   []publicationAutoAlbumLocalSource `json:"sources"`
}

// requestLocalAutoAlbumPublication freezes the fresh automatic Local outcome
// into the existing publication_automatic_album table and wakes the existing
// publication_auto_album_v1 worker on the matching queue. It deliberately does
// nothing for a replay or when the setting is disabled.
func (repository *IncomingMatchCandidateRepository) requestLocalAutoAlbumPublication(
	ctx context.Context, tx bun.Tx, operationID uuid.UUID, commit AutomaticLocalFallbackCommit,
	generation automaticLocalFallbackGeneration, incumbent matchingLocalRecheckSnapshot, decisionDigest string,
) error {
	if repository.riverInserter == nil || repository.albumScorer == nil {
		return nil
	}
	if incumbent.LocalApplyOperationID != operationID || incumbent.ProviderReleaseID != commit.ProviderReleaseID ||
		incumbent.LocalReleaseID == uuid.Nil || len(incumbent.Sources) == 0 || decisionDigest == "" {
		return ErrIncomingMatchConflict
	}
	var enabled bool
	if err := tx.NewRaw(`SELECT COALESCE((SELECT setting_value='true' FROM app_setting WHERE setting_name='publication_auto_complete_albums'),false)`).Scan(ctx, &enabled); err != nil || !enabled {
		return err
	}
	var target matchingLayoutTargetEvidence
	if err := json.Unmarshal(commit.LayoutTarget, &target); err != nil ||
		target.SchemaVersion != matchingLayoutTargetEvidenceVersion || target.Target.SchemaVersion != 2 ||
		target.Target.GroupID != commit.GroupID || target.Target.GroupID == uuid.Nil ||
		target.Target.GroupRevision == "" || target.Target.AuthorityRevision < 0 || target.PolicyVersion != MatchingLayoutPolicyVersion {
		return fmt.Errorf("automatic Local album target is invalid: %w", ErrIncomingMatchConflict)
	}
	threshold, err := parseMatchingLayoutPairThreshold(target.PairThreshold)
	if err != nil {
		return fmt.Errorf("automatic Local album target threshold is invalid: %w", ErrIncomingMatchConflict)
	}
	if generation.CurrentRunID == nil || *generation.CurrentRunID != commit.OutageRunID ||
		generation.RequestedGeneration != commit.ObservedRequestGeneration || generation.HighWater < 0 {
		return fmt.Errorf("automatic Local album generation changed: %w", ErrIncomingMatchConflict)
	}
	selected, err := readMatchingLocalSelectionSnapshotTx(ctx, tx, []uuid.UUID{commit.ProviderReleaseID})
	if err != nil {
		return fmt.Errorf("read selected Local automatic album edition: %w", err)
	}
	if len(selected.Releases) != 1 || selected.Releases[0].ID != commit.ProviderReleaseID || selected.Digest == "" {
		return fmt.Errorf("automatic Local album edition is unavailable: %w", ErrIncomingMatchConflict)
	}
	release := selected.Releases[0]
	tracks := make(map[uuid.UUID]MatchingReleaseTrack, len(release.Tracks))
	for _, track := range release.Tracks {
		if track.ID == uuid.Nil {
			return fmt.Errorf("automatic Local album edition has an invalid track: %w", ErrIncomingMatchConflict)
		}
		if _, duplicate := tracks[track.ID]; duplicate {
			return fmt.Errorf("automatic Local album edition has an ambiguous track: %w", ErrIncomingMatchConflict)
		}
		tracks[track.ID] = track
	}
	if !release.Complete || len(tracks) == 0 {
		return nil
	}

	byTrack := make(map[uuid.UUID][]publicationAutoAlbumLocalSource, len(tracks))
	for _, source := range incumbent.Sources {
		track, ok := tracks[source.ProviderTrackID]
		if !ok {
			return fmt.Errorf("automatic Local album source is outside the selected edition: %w", ErrIncomingMatchConflict)
		}
		// The incumbent constructor already checked the materialized bindings.
		// Score each physical copy: the solver only proves a representative.
		capture := source.Capture
		if !bytes.Equal(source.TagEncoding, []byte("null")) {
			capture.TagEncoding = source.TagEncoding
		}
		score, trackEvidence, err := repository.albumScorer(capture, PublicationProviderTagContext{Release: release, Position: track})
		if err != nil {
			return fmt.Errorf("score automatic Local album source: %w", err)
		}
		if score != nil && trackEvidence && *score >= threshold && *score <= 1 {
			byTrack[source.ProviderTrackID] = append(byTrack[source.ProviderTrackID], publicationLocalAlbumSource(source))
		}
	}

	scope := make([]PublicationAutoAlbumPosition, 0, len(release.Tracks))
	sources := make([]publicationAutoAlbumLocalSource, 0, len(incumbent.Sources))
	for _, track := range release.Tracks {
		candidates := byTrack[track.ID]
		if len(candidates) == 0 {
			// The frozen Local layout did not prove a complete album.
			return nil
		}
		positionID := candidates[0].ReleaseTrackID
		works := make([]uuid.UUID, 0, len(candidates))
		seenWorks := make(map[uuid.UUID]bool, len(candidates))
		for _, candidate := range candidates {
			if candidate.ReleaseTrackID != positionID {
				return fmt.Errorf("automatic Local album position is ambiguous: %w", ErrIncomingMatchConflict)
			}
			sources = append(sources, candidate)
			if !seenWorks[candidate.WorkID] {
				seenWorks[candidate.WorkID] = true
				works = append(works, candidate.WorkID)
			}
		}
		sort.Slice(works, func(i, j int) bool { return works[i].String() < works[j].String() })
		scope = append(scope, PublicationAutoAlbumPosition{
			PositionID: positionID, ProviderTrackID: track.ID, WorkIDs: works,
		})
	}
	if len(scope) != len(release.Tracks) || len(sources) == 0 {
		return fmt.Errorf("automatic Local album scope is incomplete: %w", ErrIncomingMatchConflict)
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].ReleaseTrackID != sources[j].ReleaseTrackID {
			return sources[i].ReleaseTrackID.String() < sources[j].ReleaseTrackID.String()
		}
		if sources[i].WorkID != sources[j].WorkID {
			return sources[i].WorkID.String() < sources[j].WorkID.String()
		}
		if sources[i].SourceLocationID != sources[j].SourceLocationID {
			return sources[i].SourceLocationID.String() < sources[j].SourceLocationID.String()
		}
		return sources[i].MediaVariantID.String() < sources[j].MediaVariantID.String()
	})
	var outputEpoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &outputEpoch); err != nil {
		return fmt.Errorf("read automatic Local album output epoch: %w", err)
	}
	if outputEpoch == uuid.Nil {
		return fmt.Errorf("automatic Local album output epoch is missing: %w", ErrIncomingMatchConflict)
	}
	layout := publicationAutoAlbumLocalLayout{
		Version:                   publicationAutoAlbumLocalLayoutVersion,
		LocalApplyOperationID:     operationID,
		GroupID:                   commit.GroupID,
		GroupRevision:             target.Target.GroupRevision,
		AuthorityRevision:         target.Target.AuthorityRevision,
		OutageRunID:               commit.OutageRunID,
		ObservedRequestGeneration: commit.ObservedRequestGeneration,
		HighWater:                 generation.HighWater,
		ProviderReleaseID:         commit.ProviderReleaseID,
		LocalReleaseID:            incumbent.LocalReleaseID,
		PolicyVersion:             MatchingLayoutPolicyVersion,
		DecisionDigest:            decisionDigest,
		CatalogDigest:             selected.Digest,
		LayoutTarget:              append(json.RawMessage(nil), commit.LayoutTarget...),
		Sources:                   sources,
	}
	layoutJSON, err := json.Marshal(layout)
	if err != nil {
		return fmt.Errorf("encode automatic Local album layout: %w", err)
	}
	layoutJSON, err = canonicalMatchingJSON(layoutJSON)
	if err != nil {
		return fmt.Errorf("canonicalize automatic Local album layout: %w", err)
	}
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return fmt.Errorf("encode automatic Local album scope: %w", err)
	}
	if _, err := tx.NewRaw(`INSERT INTO publication_automatic_album (apply_operation_id,output_epoch,release_id,scope,local_layout)
		VALUES (?,?,?,?::jsonb,?::jsonb)`, operationID, outputEpoch, incumbent.LocalReleaseID, string(scopeJSON), string(layoutJSON)).Exec(ctx); err != nil {
		return fmt.Errorf("store automatic Local album request: %w", err)
	}
	_, err = repository.riverInserter.InsertTx(ctx, tx.Tx, PublicationAutoAlbumArgs{ApplyOperationID: operationID}, &river.InsertOpts{Queue: "matching"})
	return err
}

func publicationLocalAlbumSource(source matchingLocalRecheckSnapshotSource) publicationAutoAlbumLocalSource {
	return publicationAutoAlbumLocalSource{
		FileMemberID:        source.Capture.VariantID,
		WorkID:              source.WorkID,
		SourceRootID:        source.SourceRootID,
		SourceLocationID:    source.SourceLocationID,
		MediaVariantID:      source.MediaVariantID,
		ProviderTrackID:     source.ProviderTrackID,
		ReleaseTrackID:      source.ReleaseTrackID,
		LocalTrackID:        source.LocalTrackID,
		TrackSourceID:       source.TrackSourceID,
		TrackSourceInserted: source.TrackSourceInserted,
		ConfiguredPath:      source.Capture.ConfiguredPath,
		InventoryPath:       source.Capture.InventoryPath,
		RelativePath:        source.Capture.RelativePath,
		SizeBytes:           source.Capture.SizeBytes,
		MTime:               source.Capture.Mtime.UTC(),
		SourceSHA256:        append([]byte(nil), source.Capture.SourceSHA256...),
		TagEncoding:         append(json.RawMessage(nil), source.TagEncoding...),
	}
}

func decodePublicationLocalLayout(raw json.RawMessage) (publicationAutoAlbumLocalLayout, error) {
	var layout publicationAutoAlbumLocalLayout
	if err := json.Unmarshal(raw, &layout); err != nil {
		return layout, fmt.Errorf("decode Local automatic album layout: %w", err)
	}
	if layout.Version != publicationAutoAlbumLocalLayoutVersion || layout.LocalApplyOperationID == uuid.Nil ||
		layout.GroupID == uuid.Nil || layout.GroupRevision == "" || layout.AuthorityRevision < 0 ||
		layout.OutageRunID == uuid.Nil || layout.ObservedRequestGeneration < 1 || layout.HighWater < 0 ||
		layout.ProviderReleaseID == uuid.Nil || layout.LocalReleaseID == uuid.Nil ||
		layout.PolicyVersion != MatchingLayoutPolicyVersion || layout.DecisionDigest == "" ||
		!publicationLocalCatalogDigest(layout.CatalogDigest) || !jsonObject(layout.LayoutTarget) || len(layout.Sources) == 0 {
		return layout, errors.New("invalid Local automatic album layout")
	}
	return layout, nil
}

func deleteLocalAutomaticAlbumGrantTx(ctx context.Context, tx bun.Tx, localApplyOperationID uuid.UUID) error {
	if localApplyOperationID == uuid.Nil {
		return nil
	}
	if _, err := tx.NewRaw(`DELETE FROM publication_automatic_album
		WHERE apply_operation_id=? AND local_layout IS NOT NULL`, localApplyOperationID).Exec(ctx); err != nil {
		return fmt.Errorf("delete Local automatic album grant: %w", err)
	}
	return nil
}

func publicationLocalCatalogDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func publicationLocalScopeByPosition(scope []PublicationAutoAlbumPosition) (map[uuid.UUID]PublicationAutoAlbumPosition, error) {
	result := make(map[uuid.UUID]PublicationAutoAlbumPosition, len(scope))
	seenTrack := make(map[uuid.UUID]bool, len(scope))
	for _, position := range scope {
		if position.PositionID == uuid.Nil || position.ProviderTrackID == uuid.Nil || len(position.WorkIDs) == 0 {
			return nil, errors.New("incomplete Local automatic album scope position")
		}
		if _, duplicate := result[position.PositionID]; duplicate || seenTrack[position.ProviderTrackID] {
			return nil, errors.New("ambiguous Local automatic album scope position")
		}
		seenTrack[position.ProviderTrackID] = true
		result[position.PositionID] = position
	}
	return result, nil
}

func validatePublicationLocalLayoutShape(layout publicationAutoAlbumLocalLayout, scope []PublicationAutoAlbumPosition) error {
	byPosition, err := publicationLocalScopeByPosition(scope)
	if err != nil {
		return err
	}
	seen := make(map[uuid.UUID]bool, len(layout.Sources))
	sourceWorks := make(map[uuid.UUID]map[uuid.UUID]bool, len(layout.Sources))
	for _, source := range layout.Sources {
		if source.FileMemberID == uuid.Nil || source.WorkID == uuid.Nil || source.SourceRootID == uuid.Nil ||
			source.SourceLocationID == uuid.Nil || source.MediaVariantID == uuid.Nil ||
			source.ProviderTrackID == uuid.Nil || source.ReleaseTrackID == uuid.Nil ||
			source.LocalTrackID == uuid.Nil || source.TrackSourceID == uuid.Nil ||
			source.ConfiguredPath == "" || source.InventoryPath == "" || source.RelativePath == "" ||
			source.SizeBytes < 0 || source.MTime.IsZero() {
			return errors.New("incomplete Local automatic album source")
		}
		if seen[source.SourceLocationID] {
			return errors.New("duplicate Local automatic album source")
		}
		seen[source.SourceLocationID] = true
		position, ok := byPosition[source.ReleaseTrackID]
		if !ok || position.ProviderTrackID != source.ProviderTrackID {
			return errors.New("source falls outside the Local automatic album scope")
		}
		allowed := false
		for _, work := range position.WorkIDs {
			if work == source.WorkID {
				allowed = true
				break
			}
		}
		if !allowed {
			return errors.New("source work falls outside the Local automatic album scope")
		}
		if sourceWorks[source.ReleaseTrackID] == nil {
			sourceWorks[source.ReleaseTrackID] = make(map[uuid.UUID]bool)
		}
		sourceWorks[source.ReleaseTrackID][source.WorkID] = true
	}
	for positionID, position := range byPosition {
		works := sourceWorks[positionID]
		if len(works) == 0 {
			return errors.New("missing source for a Local automatic album position")
		}
		for _, work := range position.WorkIDs {
			if !works[work] {
				return errors.New("missing source for a Local automatic album work")
			}
		}
	}
	return nil
}

func publicationLocalSourceMatchesSelection(source publicationAutoAlbumLocalSource, selection *PublicationSourceSelection) bool {
	return selection != nil && selection.RootID == source.SourceRootID &&
		selection.LocationID == source.SourceLocationID && selection.MediaVariantID == source.MediaVariantID &&
		selection.WorkID == source.WorkID && selection.ConfiguredPath == source.ConfiguredPath &&
		selection.InventoryPath == source.InventoryPath && selection.RelativePath == source.RelativePath &&
		selection.SizeBytes == source.SizeBytes && selection.MTime.UTC().Equal(source.MTime.UTC())
}

func publicationLocalSourceMatchesCapture(source publicationAutoAlbumLocalSource, capture IncomingGroupingCapture) bool {
	return capture.WorkID == source.WorkID && capture.RootID == source.SourceRootID &&
		capture.LocationID == source.SourceLocationID && capture.MediaVariantID != nil &&
		*capture.MediaVariantID == source.MediaVariantID && capture.ConfiguredPath == source.ConfiguredPath &&
		capture.InventoryPath == source.InventoryPath && capture.RelativePath == source.RelativePath &&
		capture.SizeBytes == source.SizeBytes && capture.Mtime.UTC().Equal(source.MTime.UTC()) &&
		bytes.Equal(capture.SourceSHA256, source.SourceSHA256)
}

func publicationLocalSourceForPosition(
	layout publicationAutoAlbumLocalLayout, positionID uuid.UUID, selection *PublicationSourceSelection,
) bool {
	for _, source := range layout.Sources {
		if source.ReleaseTrackID == positionID && publicationLocalSourceMatchesSelection(source, selection) {
			return true
		}
	}
	return false
}

// readPublicationLocalAutoAlbum returns the Local grant only while the exact
// succeeded Local operation, current group revision, automatic authority,
// outage generation and output epoch still describe it. A retained Local
// recheck is deliberately not required: its own row is deleted after a proved
// non-winning comparison while this grant remains valid.
func readPublicationLocalAutoAlbum(ctx context.Context, db bun.IDB, applyID uuid.UUID) (*PublicationAutoAlbumRequest, error) {
	if applyID == uuid.Nil {
		return nil, nil
	}
	var request PublicationAutoAlbumRequest
	query := `SELECT apply_operation_id,output_epoch,release_id,scope,local_layout
		FROM publication_automatic_album WHERE apply_operation_id=? AND local_layout IS NOT NULL`
	if _, locking := db.(bun.Tx); locking {
		query += ` FOR SHARE`
	}
	err := db.NewRaw(query, applyID).Scan(ctx, &request)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	layout, err := decodePublicationLocalLayout(request.LocalLayout)
	if err != nil {
		return nil, nil
	}
	if err := validatePublicationLocalLayoutShape(layout, request.Scope); err != nil {
		return nil, nil
	}
	valid, err := publicationLocalGrantCurrent(ctx, db, request, layout)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, nil
	}
	return &request, nil
}

func publicationLocalGrantCurrent(ctx context.Context, db bun.IDB, request PublicationAutoAlbumRequest, layout publicationAutoAlbumLocalLayout) (bool, error) {
	if layout.LocalApplyOperationID != request.ApplyOperationID || layout.LocalReleaseID != request.ReleaseID {
		return false, nil
	}
	query := `SELECT op.input_snapshot::text,grouping.needs_refresh,
			generation.reconciled_generation=generation.requested_generation FROM operation op
		JOIN incoming_group current_group ON current_group.id=? AND current_group.revision=? AND NOT current_group.manual
		JOIN incoming_grouping_state grouping ON grouping.singleton
		JOIN incoming_matching_generation generation ON generation.group_id=current_group.id
			AND generation.current_run_id=? AND generation.high_water=?
			AND generation.requested_generation>=?
		JOIN publication_namespace_state namespace ON namespace.singleton AND namespace.output_epoch=?
		LEFT JOIN incoming_group_matching_authority authority ON authority.group_id=current_group.id
		WHERE op.id=? AND op.kind='matching_evaluation_v1' AND op.state='succeeded' AND op.stage='local_fallback_applied'
		AND COALESCE(authority.mode,'automatic')='automatic' AND COALESCE(authority.revision,0)=?
		AND EXISTS (SELECT 1 FROM app_setting WHERE setting_name='publication_auto_complete_albums' AND setting_value='true')
		AND NOT EXISTS (SELECT 1 FROM matching_local_recheck WHERE local_apply_operation_id=op.id AND state='publishing')`
	if _, locking := db.(bun.Tx); locking {
		// Use the Local apply order: generation, then group and authority when
		// the full layout target is recaptured.
		query += ` FOR SHARE OF generation`
	}
	var snapshot string
	var needsRefresh, reconciled bool
	err := db.NewRaw(query, layout.GroupID, layout.GroupRevision, layout.OutageRunID, layout.HighWater,
		layout.ObservedRequestGeneration, request.OutputEpoch, request.ApplyOperationID, layout.AuthorityRevision).Scan(ctx, &snapshot, &needsRefresh, &reconciled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read current Local automatic album authority: %w", err)
	}
	var stored automaticLocalFallbackOperationSnapshot
	if json.Unmarshal([]byte(snapshot), &stored) != nil ||
		stored.SchemaVersion != 1 || stored.GroupID != layout.GroupID || stored.OutageRunID != layout.OutageRunID ||
		stored.ObservedRequestGeneration != layout.ObservedRequestGeneration ||
		stored.ProviderReleaseID != layout.ProviderReleaseID || stored.DecisionDigest != layout.DecisionDigest ||
		stored.PolicyVersion != layout.PolicyVersion {
		return false, nil
	}
	// Reconciliation is requested globally. Wait for it to settle; the original
	// outage and unchanged group/run fences, not the latest wake number, identify
	// this grant. Admission also recaptures the exact layout and source bindings.
	if needsRefresh || !reconciled {
		return false, ErrMatchingTargetNeedsRefresh
	}
	outageRunID, err := currentMatchingMusicBrainzOutageFallback(ctx, db, layout.GroupID, layout.ObservedRequestGeneration)
	return outageRunID == layout.OutageRunID, err
}

// validatePublicationLocalAutoAlbum rechecks the complete Local grant inside
// the first-publication admission transaction. It proves the frozen layout
// target, the selected-only Local catalog snapshot and every release/position/
// source binding before any namespace or publication row is written.
func validatePublicationLocalAutoAlbum(ctx context.Context, tx bun.Tx, admission PublicationAdmission, expected *PublicationAutoAlbumRequest) error {
	layout, err := decodePublicationLocalLayout(expected.LocalLayout)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPublicationAdmissionConflict, err)
	}
	if err := validatePublicationLocalLayoutShape(layout, expected.Scope); err != nil {
		return fmt.Errorf("%w: %v", ErrPublicationAdmissionConflict, err)
	}
	scope, err := publicationLocalScopeByPosition(expected.Scope)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPublicationAdmissionConflict, err)
	}
	if !publicationLocalSourceForPosition(layout, admission.PositionID, admission.Source) {
		return ErrPublicationAdmissionConflict
	}
	captures, err := RecheckMatchingLayoutTargetTx(ctx, tx, layout.GroupID, layout.PolicyVersion, layout.LayoutTarget)
	if err != nil {
		if errors.Is(err, ErrIncomingMatchConflict) || errors.Is(err, sql.ErrNoRows) {
			return ErrPublicationAdmissionConflict
		}
		return fmt.Errorf("recheck automatic Local album target: %w", err)
	}
	capturesByLocation := make(map[uuid.UUID]IncomingGroupingCapture, len(captures))
	for _, capture := range captures {
		if capture.LocationID == uuid.Nil {
			return ErrPublicationAdmissionConflict
		}
		if _, duplicate := capturesByLocation[capture.LocationID]; duplicate {
			return ErrPublicationAdmissionConflict
		}
		capturesByLocation[capture.LocationID] = capture
	}
	if err := lockMatchingLocalCatalogEvidenceTx(ctx, tx); err != nil {
		return err
	}
	selected, err := readMatchingLocalSelectionSnapshotTx(ctx, tx, []uuid.UUID{layout.ProviderReleaseID})
	if err != nil {
		if errors.Is(err, ErrIncomingMatchConflict) || errors.Is(err, sql.ErrNoRows) {
			return ErrPublicationAdmissionConflict
		}
		return fmt.Errorf("recheck automatic Local album catalog: %w", err)
	}
	if len(selected.Releases) != 1 || selected.Releases[0].ID != layout.ProviderReleaseID ||
		selected.Digest != layout.CatalogDigest || !selected.Releases[0].Complete {
		return ErrPublicationAdmissionConflict
	}
	tracks := make(map[uuid.UUID]bool, len(selected.Releases[0].Tracks))
	for _, track := range selected.Releases[0].Tracks {
		if track.ID == uuid.Nil || tracks[track.ID] {
			return ErrPublicationAdmissionConflict
		}
		tracks[track.ID] = true
	}
	if len(scope) != len(tracks) {
		return ErrPublicationAdmissionConflict
	}
	for _, position := range scope {
		if !tracks[position.ProviderTrackID] {
			return ErrPublicationAdmissionConflict
		}
	}
	for _, source := range layout.Sources {
		capture, ok := capturesByLocation[source.SourceLocationID]
		if !ok || capture.VariantID != source.FileMemberID || !publicationLocalSourceMatchesCapture(source, capture) {
			return ErrPublicationAdmissionConflict
		}
		if _, err := validateMatchingLocalRecheckSourceBinding(ctx, tx, layout.ProviderReleaseID, layout.LocalReleaseID, matchingLocalMaterializedSource{
			FileMemberID: source.FileMemberID, MediaVariantID: source.MediaVariantID,
			ProviderTrackID: source.ProviderTrackID, LocalTrackID: source.LocalTrackID,
			ReleaseTrackID: source.ReleaseTrackID, TrackSourceID: source.TrackSourceID, Inserted: source.TrackSourceInserted,
		}); err != nil {
			if errors.Is(err, ErrIncomingMatchConflict) || errors.Is(err, ErrMatchingManualProtection) {
				return ErrPublicationAdmissionConflict
			}
			return fmt.Errorf("recheck automatic Local album source: %w", err)
		}
	}
	return nil
}
