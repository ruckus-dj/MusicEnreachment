package persistence

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const providerRefreshReconciliationScope = "provider_refresh"

// requestProviderRefreshPublicationReconciliation projects only this accepted
// source graph through existing MusicBrainz/local bindings to current outputs.
func requestProviderRefreshPublicationReconciliation(ctx context.Context, tx bun.Tx, ticket ProviderRefreshTicket, accepted acceptedProviderSourceChanges) error {
	releaseKeys := providerChangeKeys(accepted.ReleaseKeys)
	recordingKeys := providerChangeKeys(accepted.RecordingKeys)
	artistKeys := providerChangeKeys(accepted.ArtistKeys)
	trackReleaseKeys := providerChangeKeys(accepted.TracksReleaseKeys)
	if len(releaseKeys)+len(recordingKeys)+len(artistKeys)+len(trackReleaseKeys) == 0 {
		return nil
	}

	var epoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		return fmt.Errorf("read current output epoch: %w", err)
	}
	positions := make(map[uuid.UUID]struct{})
	queries := []struct {
		query string
		args  []any
	}{
		{
			query: `SELECT DISTINCT position.id FROM provider_source_entity refreshed
				JOIN provider_release release ON release.provider_id=refreshed.provider_id AND release.provider_key=refreshed.provider_key
				JOIN provider_local_release binding ON binding.provider_id=release.provider_id AND binding.provider_release_id=release.id
				JOIN release_track position ON position.release_id=binding.local_release_id
				JOIN track_publication publication ON publication.position_id=position.id AND publication.epoch=?
				WHERE refreshed.provider_source_id=? AND refreshed.provider_id=? AND refreshed.entity_kind='release' AND refreshed.provider_key IN (?)`,
			args: []any{epoch, ticket.ProviderSourceID, ticket.ProviderID, bun.List(releaseKeys)},
		},
		{
			query: `SELECT DISTINCT position.id FROM provider_source_release_track source_track
				JOIN provider_release_track track ON track.provider_id=source_track.provider_id AND track.id=source_track.track_id
				JOIN provider_local_track binding ON binding.provider_id=track.provider_id AND binding.provider_track_id=track.id
				JOIN release_track position ON position.track_id=binding.local_track_id
				JOIN track_publication publication ON publication.position_id=position.id AND publication.epoch=?
				WHERE source_track.provider_source_id=? AND source_track.provider_id=? AND source_track.release_id IN (
					SELECT id FROM provider_release WHERE provider_id=? AND provider_key IN (?))`,
			args: []any{epoch, ticket.ProviderSourceID, ticket.ProviderID, ticket.ProviderID, bun.List(trackReleaseKeys)},
		},
		{
			query: `SELECT DISTINCT position.id FROM provider_source_entity refreshed
				JOIN provider_recording recording ON recording.provider_id=refreshed.provider_id AND recording.provider_key=refreshed.provider_key
				JOIN provider_local_recording binding ON binding.provider_id=recording.provider_id AND binding.provider_recording_id=recording.id
				JOIN release_track position ON position.recording_id=binding.local_recording_id
				JOIN track_publication publication ON publication.position_id=position.id AND publication.epoch=?
				WHERE refreshed.provider_source_id=? AND refreshed.provider_id=? AND refreshed.entity_kind='recording' AND refreshed.provider_key IN (?)`,
			args: []any{epoch, ticket.ProviderSourceID, ticket.ProviderID, bun.List(recordingKeys)},
		},
		{
			query: `SELECT DISTINCT position.id FROM provider_source_entity refreshed
				JOIN provider_artist artist ON artist.provider_id=refreshed.provider_id AND artist.provider_key=refreshed.provider_key
				JOIN provider_source_release_artist_credit credit ON credit.provider_source_id=refreshed.provider_source_id
					AND credit.provider_id=artist.provider_id AND credit.artist_id=artist.id
			JOIN provider_local_release binding ON binding.provider_id=credit.provider_id AND binding.provider_release_id=credit.release_id
			JOIN release_track position ON position.release_id=binding.local_release_id
			JOIN track_publication publication ON publication.position_id=position.id AND publication.epoch=?
				WHERE refreshed.provider_source_id=? AND refreshed.provider_id=? AND refreshed.entity_kind='artist' AND refreshed.provider_key IN (?)`,
			args: []any{epoch, ticket.ProviderSourceID, ticket.ProviderID, bun.List(artistKeys)},
		},
		{
			query: `SELECT DISTINCT position.id FROM provider_source_entity refreshed
				JOIN provider_recording recording ON recording.provider_id=refreshed.provider_id AND recording.provider_key=refreshed.provider_key
				JOIN provider_local_recording binding ON binding.provider_id=recording.provider_id AND binding.provider_recording_id=recording.id
			JOIN release_track position ON position.recording_id=binding.local_recording_id
			JOIN track_publication publication ON publication.position_id=position.id AND publication.epoch=?
			CROSS JOIN LATERAL jsonb_array_elements(CASE
				WHEN jsonb_typeof(refreshed.fields->'artist-credit')='array' THEN refreshed.fields->'artist-credit'
				ELSE '[]'::jsonb END) credit
			WHERE refreshed.provider_source_id=? AND refreshed.provider_id=? AND refreshed.entity_kind='recording'
				AND jsonb_exists(refreshed.field_evidence,'artist-credit')
				AND lower(credit->'artist'->>'id') IN (?)`,
			args: []any{epoch, ticket.ProviderSourceID, ticket.ProviderID, bun.List(artistKeys)},
		},
		{
			query: `SELECT DISTINCT position.id FROM provider_source_entity refreshed
				JOIN provider_release release ON release.provider_id=refreshed.provider_id AND release.provider_key=refreshed.provider_key
				JOIN provider_local_release binding ON binding.provider_id=release.provider_id AND binding.provider_release_id=release.id
			JOIN release_track position ON position.release_id=binding.local_release_id
			JOIN track_publication publication ON publication.position_id=position.id AND publication.epoch=?
			JOIN provider_local_recording recording_binding ON recording_binding.provider_id=release.provider_id
				AND recording_binding.local_recording_id=position.recording_id
			JOIN provider_recording recording ON recording.provider_id=recording_binding.provider_id
				AND recording.id=recording_binding.provider_recording_id
			CROSS JOIN LATERAL jsonb_array_elements(CASE
				WHEN jsonb_typeof(refreshed.fields->'normalized_tracks')='array' THEN refreshed.fields->'normalized_tracks'
				ELSE '[]'::jsonb END) normalized_track
			CROSS JOIN LATERAL jsonb_array_elements(CASE
				WHEN jsonb_typeof(normalized_track->'artist-credit')='array' THEN normalized_track->'artist-credit'
				ELSE '[]'::jsonb END) credit
			WHERE refreshed.provider_source_id=? AND refreshed.provider_id=? AND refreshed.entity_kind='release'
				AND jsonb_exists(refreshed.field_evidence,'normalized_tracks')
				AND normalized_track->>'medium'=position.medium::text
				AND normalized_track->>'position'=position.position::text
				AND lower(normalized_track->>'recording_provider_key')=recording.provider_key
				AND lower(credit->'artist'->>'id') IN (?)`,
			args: []any{epoch, ticket.ProviderSourceID, ticket.ProviderID, bun.List(artistKeys)},
		},
	}
	inputs := [][]string{releaseKeys, trackReleaseKeys, recordingKeys, artistKeys, artistKeys, artistKeys}
	for i, query := range queries {
		if len(inputs[i]) == 0 {
			continue
		}
		var affected []uuid.UUID
		if err := tx.NewRaw(query.query, query.args...).Scan(ctx, &affected); err != nil {
			return fmt.Errorf("derive current positions for refreshed provider graph: %w", err)
		}
		for _, position := range affected {
			positions[position] = struct{}{}
		}
	}
	if len(positions) == 0 {
		return nil
	}
	positionIDs := make([]uuid.UUID, 0, len(positions))
	for position := range positions {
		positionIDs = append(positionIDs, position)
	}
	return requestPublicationReconciliationWith(ctx, tx, epoch, positionIDs, []string{providerRefreshReconciliationScope})
}

func providerChangeKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
