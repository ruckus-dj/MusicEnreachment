package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

type PublicationAutoAlbumArgs struct {
	ApplyOperationID uuid.UUID `json:"apply_operation_id"`
}

func (PublicationAutoAlbumArgs) Kind() string { return "publication_auto_album_v1" }

type PublicationAutoAlbumPosition struct {
	PositionID      uuid.UUID   `json:"position_id"`
	ProviderTrackID uuid.UUID   `json:"provider_track_id"`
	WorkIDs         []uuid.UUID `json:"work_ids"`
}

type PublicationAutoAlbumRequest struct {
	ApplyOperationID uuid.UUID                      `bun:"apply_operation_id"`
	OutputEpoch      uuid.UUID                      `bun:"output_epoch"`
	ReleaseID        uuid.UUID                      `bun:"release_id"`
	Scope            []PublicationAutoAlbumPosition `bun:"scope,type:jsonb"`
	// LocalLayout is null for the MusicBrainz matching apply path. A non-null
	// value binds this request to one succeeded matching_evaluation_v1
	// local_fallback_applied operation and its exact Local source evidence.
	LocalLayout json.RawMessage `bun:"local_layout"`
}

func (request PublicationAutoAlbumRequest) PositionWorkIDs(positionID uuid.UUID) []uuid.UUID {
	for _, position := range request.Scope {
		if position.PositionID == positionID {
			return position.WorkIDs
		}
	}
	return nil
}

func (repository *SetupManagerRepository) requestAutoAlbumPublication(ctx context.Context, tx bun.Tx, operationID, runID, groupID uuid.UUID) error {
	if repository.riverInserter == nil {
		return nil
	}
	var enabled bool
	if err := tx.NewRaw(`SELECT COALESCE((SELECT setting_value='true' FROM app_setting WHERE setting_name='publication_auto_complete_albums'),false)`).Scan(ctx, &enabled); err != nil || !enabled {
		return err
	}
	var evidence struct {
		Decision    json.RawMessage `bun:"evaluation_evidence"`
		SourceID    uuid.UUID       `bun:"provider_source_id"`
		ProviderID  uuid.UUID       `bun:"provider_id"`
		Layout      json.RawMessage `bun:"layout_target"`
		ApplySchema int             `bun:"apply_schema"`
	}
	err := tx.NewRaw(`SELECT evaluation.evaluation_evidence,evaluation.provider_source_id,evaluation.provider_id,
			COALESCE(acquisition.intent->'layout_target','null'::jsonb) AS layout_target, request.schema_version AS apply_schema
		FROM incoming_match_evaluation evaluation
		JOIN matching_acquisition acquisition ON acquisition.run_id=evaluation.target_run_id
		JOIN matching_apply_request request ON request.group_id=evaluation.group_id AND request.parent_run_id=acquisition.run_id
		WHERE evaluation.group_id=? AND acquisition.run_id=? AND request.apply_operation_id=?`, groupID, runID, operationID).Scan(ctx, &evidence)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read automatic album matching evidence: %w", err)
	}
	if !jsonObject(evidence.Layout) {
		// Only the layout solver proves the current configured threshold.
		return nil
	}
	decision, err := decodePersistedMatchingDecision(evidence.Decision)
	if err != nil {
		return err
	}
	if decision.Outcome != "unique" || decision.SelectedRelease == nil {
		return nil
	}
	var releases []MatchingRelease
	if err := readMatchingReleases(ctx, tx, evidence.SourceID, evidence.ProviderID, []string{decision.SelectedRelease.ProviderReleaseKey}, &releases); err != nil {
		return fmt.Errorf("read automatic album accepted edition: %w", err)
	}
	if len(releases) != 1 || !releases[0].Complete || len(releases[0].Tracks) == 0 || releases[0].ID != decision.SelectedRelease.ProviderReleaseID {
		return nil
	}
	byTrack := make(map[uuid.UUID]map[uuid.UUID]struct{})
	var members []IncomingMatchMemberFence
	if err := tx.NewRaw(`SELECT file_member_id,work_id FROM incoming_match_evaluation_member WHERE group_id=?`, groupID).Scan(ctx, &members); err != nil {
		return fmt.Errorf("read automatic album member identities: %w", err)
	}
	works := make(map[uuid.UUID]uuid.UUID, len(members))
	for _, member := range members {
		works[member.FileMemberID] = member.WorkID
	}
	for _, assignment := range decision.Assignments {
		work := works[assignment.FileMemberID]
		if work == uuid.Nil {
			return nil
		}
		if byTrack[assignment.ProviderTrackID] == nil {
			byTrack[assignment.ProviderTrackID] = make(map[uuid.UUID]struct{})
		}
		byTrack[assignment.ProviderTrackID][work] = struct{}{}
	}
	request := PublicationAutoAlbumRequest{ApplyOperationID: operationID}
	// The frozen apply projection uses one-based positions, whereas the artist
	// apply materializes the original provider ordinals.
	var coordinateOffset int
	if evidence.ApplySchema == 2 {
		coordinateOffset = 1
	}
	for _, track := range releases[0].Tracks {
		if len(byTrack[track.ID]) == 0 {
			return nil
		}
		position := PublicationAutoAlbumPosition{ProviderTrackID: track.ID}
		var releaseID uuid.UUID
		err := tx.NewRaw(`SELECT local_position.id,local_position.release_id
			FROM provider_local_release release_binding
			JOIN provider_local_track track_binding ON track_binding.provider_id=release_binding.provider_id
			JOIN release_track local_position ON local_position.release_id=release_binding.local_release_id
				AND local_position.track_id=track_binding.local_track_id AND local_position.medium=? AND local_position.position=?
			WHERE release_binding.provider_id=? AND release_binding.provider_release_id=? AND track_binding.provider_track_id=?`,
			track.Medium+coordinateOffset, track.Position+coordinateOffset, evidence.ProviderID, releases[0].ID, track.ID).Scan(ctx, &position.PositionID, &releaseID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read automatic album local edition: %w", err)
		}
		if request.ReleaseID != uuid.Nil && request.ReleaseID != releaseID {
			return nil
		}
		request.ReleaseID = releaseID
		for work := range byTrack[track.ID] {
			position.WorkIDs = append(position.WorkIDs, work)
		}
		sort.Slice(position.WorkIDs, func(i, j int) bool { return position.WorkIDs[i].String() < position.WorkIDs[j].String() })
		request.Scope = append(request.Scope, position)
	}
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &request.OutputEpoch); err != nil {
		return fmt.Errorf("read automatic album output epoch: %w", err)
	}
	payload, err := json.Marshal(request.Scope)
	if err != nil {
		return err
	}
	_, err = tx.NewRaw(`INSERT INTO publication_automatic_album (apply_operation_id,output_epoch,release_id,scope)
		VALUES (?,?,?,?::jsonb)`, operationID, request.OutputEpoch, request.ReleaseID, string(payload)).Exec(ctx)
	if err != nil {
		return fmt.Errorf("store automatic album request: %w", err)
	}
	_, err = repository.riverInserter.InsertTx(ctx, tx.Tx, PublicationAutoAlbumArgs{ApplyOperationID: operationID}, &river.InsertOpts{Queue: "matching"})
	return err
}

func (repository *SetupManagerRepository) ReadPublicationAutoAlbum(ctx context.Context, applyID uuid.UUID) (*PublicationAutoAlbumRequest, error) {
	return readPublicationAutoAlbum(ctx, repository.db, applyID)
}

func readPublicationAutoAlbum(ctx context.Context, db bun.IDB, applyID uuid.UUID) (*PublicationAutoAlbumRequest, error) {
	if local, err := readPublicationLocalAutoAlbum(ctx, db, applyID); err != nil || local != nil {
		return local, err
	}
	var request PublicationAutoAlbumRequest
	query := `SELECT album.apply_operation_id,album.output_epoch,album.release_id,album.scope
		FROM publication_automatic_album album
		JOIN publication_namespace_state state ON state.singleton AND state.output_epoch=album.output_epoch
		JOIN matching_apply_receipt receipt ON receipt.apply_operation_id=album.apply_operation_id AND receipt.outcome='applied'
		JOIN matching_apply_request apply ON apply.apply_operation_id=receipt.apply_operation_id
		JOIN matching_run_target target ON target.run_id=apply.parent_run_id AND target.group_id=receipt.group_id
		JOIN incoming_group current_group ON current_group.id=target.group_id AND current_group.revision=target.captured_group_revision
		JOIN incoming_grouping_state grouping ON grouping.singleton AND NOT grouping.needs_refresh
		JOIN incoming_match_evaluation evaluation ON evaluation.group_id=target.group_id AND evaluation.evaluation_revision=apply.evaluation_revision
		JOIN incoming_matching_generation generation ON generation.group_id=target.group_id AND generation.current_run_id=target.run_id AND generation.high_water=target.target_generation
		LEFT JOIN incoming_group_matching_authority authority ON authority.group_id=target.group_id
		WHERE album.apply_operation_id=? AND album.local_layout IS NULL AND COALESCE(authority.mode,'automatic')='automatic'
		AND COALESCE(authority.revision,0)=target.authority_revision
		AND EXISTS (SELECT 1 FROM app_setting WHERE setting_name='publication_auto_complete_albums' AND setting_value='true')`
	if _, locking := db.(bun.Tx); locking {
		query += ` FOR SHARE OF album, current_group, grouping, generation, evaluation`
	}
	err := db.NewRaw(query, applyID).Scan(ctx, &request)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &request, err
}

func validatePublicationAutoAlbum(ctx context.Context, tx bun.Tx, admission PublicationAdmission, expected *PublicationAutoAlbumRequest) error {
	if admission.Source == nil || !slices.Contains(expected.PositionWorkIDs(admission.PositionID), admission.Source.WorkID) ||
		expected.ReleaseID != admission.ExpectedReleaseID || expected.OutputEpoch != admission.ExpectedEpoch {
		return ErrPublicationAdmissionConflict
	}
	current, err := readPublicationAutoAlbum(ctx, tx, expected.ApplyOperationID)
	if err != nil {
		return err
	}
	if current == nil || !reflect.DeepEqual(current, expected) {
		return ErrPublicationAdmissionConflict
	}
	if expected.LocalLayout != nil {
		return validatePublicationLocalAutoAlbum(ctx, tx, admission, expected)
	}
	var target struct {
		SourceID uuid.UUID       `bun:"provider_source_id"`
		Target   json.RawMessage `bun:"layout_target"`
	}
	if err := tx.NewRaw(`SELECT evaluation.provider_source_id, acquisition.intent->'layout_target' AS layout_target
		FROM matching_apply_request apply
		JOIN matching_acquisition acquisition ON acquisition.run_id=apply.parent_run_id
		JOIN incoming_match_evaluation evaluation ON evaluation.group_id=apply.group_id
		WHERE apply.apply_operation_id=?`, expected.ApplyOperationID).Scan(ctx, &target); err != nil {
		return err
	}
	var layout matchingLayoutTargetEvidence
	if err := json.Unmarshal(target.Target, &layout); err != nil {
		return err
	}
	threshold, err := matchingLayoutTargetPairThreshold(ctx, tx)
	if err != nil {
		return err
	}
	if layout.PolicyVersion != MatchingLayoutPolicyVersion || layout.PairThreshold != threshold {
		return ErrPublicationAdmissionConflict
	}
	var accepted struct {
		ReleaseID       uuid.UUID `bun:"release_id"`
		CatalogComplete bool      `bun:"complete"`
		Tracks          int       `bun:"tracks"`
		BoundTracks     int       `bun:"bound_tracks"`
	}
	keys := make([]uuid.UUID, 0, len(expected.Scope))
	for _, pos := range expected.Scope {
		keys = append(keys, pos.ProviderTrackID)
	}
	err = tx.NewRaw(`SELECT release.id AS release_id, entity.complete,
			count(edge.track_id) AS tracks, count(edge.track_id) FILTER (WHERE edge.track_id IN (?)) AS bound_tracks
		FROM provider_local_release binding
		JOIN provider_release release ON release.id=binding.provider_release_id AND release.provider_id=binding.provider_id
		JOIN provider_source_entity entity ON entity.provider_id=release.provider_id AND entity.provider_source_id=?
			AND entity.entity_kind='release' AND entity.provider_key=release.provider_key
		JOIN provider_source source ON source.id=entity.provider_source_id AND source.provider_id=entity.provider_id
		JOIN app_setting config ON config.setting_name='musicbrainz_config_identity' AND config.setting_value=source.configuration_identity
		JOIN provider_source_release_track edge ON edge.provider_source_id=entity.provider_source_id
			AND edge.provider_id=entity.provider_id AND edge.release_id=release.id AND edge.present
			AND edge.authority=(entity.field_evidence->'__tracks'->>'authority')::integer
			AND edge.fetch_order=(entity.field_evidence->'__tracks'->>'fetch_order')::bigint
		WHERE binding.local_release_id=?
		GROUP BY release.id,entity.complete`, bun.List(keys), target.SourceID, expected.ReleaseID).Scan(ctx, &accepted)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPublicationAdmissionConflict
	}
	if err != nil {
		return fmt.Errorf("revalidate complete automatic album edition: %w", err)
	}
	if !accepted.CatalogComplete || accepted.Tracks != len(keys) || accepted.BoundTracks != accepted.Tracks {
		return ErrPublicationAdmissionConflict
	}
	return nil
}

func (repository *SetupManagerRepository) CompletePublicationAutoAlbum(ctx context.Context, applyID uuid.UUID) error {
	_, err := repository.db.NewRaw(`DELETE FROM publication_automatic_album WHERE apply_operation_id=?`, applyID).Exec(ctx)
	return err
}
