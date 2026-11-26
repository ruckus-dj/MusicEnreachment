package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// readProviderRecordingLinks reads exact local-recording provider bindings. A
// recording link is never inferred from a release or track binding.
func readProviderRecordingLinks(ctx context.Context, tx bun.Tx, recordingID uuid.UUID) ([]LibraryCatalogProviderLink, error) {
	links := make([]LibraryCatalogProviderLink, 0)
	if err := tx.NewRaw(`SELECT provider.code AS provider_code,projection.provider_key,binding.method
		FROM provider_local_recording binding
		JOIN provider_recording projection ON projection.id=binding.provider_recording_id AND projection.provider_id=binding.provider_id
		JOIN provider ON provider.id=binding.provider_id
		WHERE binding.local_recording_id=?
		ORDER BY provider.code,projection.provider_key`, recordingID).Scan(ctx, &links); err != nil {
		return nil, fmt.Errorf("read library recording provider links: %w", err)
	}
	return links, nil
}

type providerPositionBinding struct {
	ProviderReleaseID    uuid.UUID `bun:"provider_release_id,type:uuid"`
	ProviderTrackID      uuid.UUID `bun:"provider_track_id,type:uuid"`
	TrackMethod          string    `bun:"track_method"`
	ReleaseProviderKey   string    `bun:"release_provider_key"`
	RecordingID          uuid.UUID `bun:"recording_id,type:uuid"`
	RecordingProviderKey string    `bun:"recording_provider_key"`
}

type providerPositionTrackSource struct {
	TrackID     uuid.UUID `bun:"track_id,type:uuid"`
	ReleaseID   uuid.UUID `bun:"release_id,type:uuid"`
	RecordingID uuid.UUID `bun:"recording_id,type:uuid"`
	Medium      int       `bun:"medium"`
	Position    int       `bun:"position"`
	Authority   int       `bun:"authority"`
	FetchOrder  int64     `bun:"fetch_order"`
}

// readProviderPositionLinks derives one read-only MusicBrainz track link from
// the exact release_track edge, the same-edition local provider bindings and
// the configured MusicBrainz source. It never exposes the SHA1-derived
// provider_release_track.id and never replays raw_source or the response cache.
func readProviderPositionLinks(ctx context.Context, tx bun.Tx, releaseID, positionID uuid.UUID) ([]LibraryCatalogProviderLink, error) {
	source, found, err := catalogMusicBrainzSource(ctx, tx)
	if err != nil || !found {
		return nil, err
	}
	var binding providerPositionBinding
	err = tx.NewRaw(`SELECT release_binding.provider_release_id,track_binding.provider_track_id,
			track_binding.method AS track_method,release.provider_key AS release_provider_key,
			track.recording_id,recording.provider_key AS recording_provider_key
		FROM release_track position
		JOIN provider_local_release release_binding ON release_binding.local_release_id=position.release_id AND release_binding.provider_id=?
		JOIN provider_release release ON release.id=release_binding.provider_release_id AND release.provider_id=release_binding.provider_id
		JOIN provider_local_track track_binding ON track_binding.local_track_id=position.track_id AND track_binding.provider_id=release_binding.provider_id
		JOIN provider_release_track track ON track.id=track_binding.provider_track_id AND track.provider_id=track_binding.provider_id AND track.release_id=release_binding.provider_release_id
		JOIN provider_recording recording ON recording.id=track.recording_id AND recording.provider_id=track.provider_id
		WHERE position.id=? AND position.release_id=?`, source.ProviderID, positionID, releaseID).Scan(ctx, &binding)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read library position provider binding: %w", err)
	}
	var track providerPositionTrackSource
	err = tx.NewRaw(`SELECT track_id,release_id,recording_id,medium,position,authority,fetch_order
		FROM provider_source_release_track
		WHERE provider_source_id=? AND provider_id=? AND track_id=? AND present`, source.ID, source.ProviderID, binding.ProviderTrackID).Scan(ctx, &track)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read accepted position provider track: %w", err)
	}
	if track.ReleaseID != binding.ProviderReleaseID || track.RecordingID != binding.RecordingID {
		return nil, nil
	}
	var entity struct {
		Fields   json.RawMessage `bun:"fields"`
		Evidence json.RawMessage `bun:"evidence"`
	}
	err = tx.NewRaw(`SELECT fields,field_evidence AS evidence FROM provider_source_entity
		WHERE provider_source_id=? AND provider_id=? AND entity_kind='release' AND provider_key=?`,
		source.ID, source.ProviderID, binding.ReleaseProviderKey).Scan(ctx, &entity)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read accepted position provider release: %w", err)
	}
	nativeKey, ok := providerPositionNativeTrackKey(entity.Fields, entity.Evidence, track, binding.RecordingProviderKey)
	if !ok {
		return nil, nil
	}
	return []LibraryCatalogProviderLink{{ProviderCode: "musicbrainz", ProviderKey: nativeKey, Method: binding.TrackMethod}}, nil
}

// providerPositionNativeTrackKey proves that the accepted release projection's
// media, normalized_tracks and __tracks evidence all belong to the exact
// provider_source_release_track authority/fetch order before returning the
// native track MBID. Any malformed, missing or ambiguous member yields no key.
func providerPositionNativeTrackKey(fields, evidence json.RawMessage, track providerPositionTrackSource, recordingProviderKey string) (string, bool) {
	fieldValues, err := decodeFrozenObject(fields)
	if err != nil {
		return "", false
	}
	var evidenceValues map[string]json.RawMessage
	if json.Unmarshal(evidence, &evidenceValues) != nil || evidenceValues == nil {
		return "", false
	}
	for _, name := range []string{"media", "normalized_tracks", "__tracks"} {
		if !providerPositionEvidenceMatches(evidenceValues[name], track.Authority, track.FetchOrder) {
			return "", false
		}
	}
	normalized, ok := matchingNormalizedProviderTrack(fieldValues["normalized_tracks"], track)
	if !ok || !strings.EqualFold(normalized.RecordingProviderKey, recordingProviderKey) {
		return "", false
	}
	recordingID, err := uuid.Parse(normalized.RecordingID)
	if err != nil || recordingID == uuid.Nil || recordingID != track.RecordingID {
		return "", false
	}
	native, ok := providerMediaTrackMBID(fieldValues["media"], track.Medium, track.Position, recordingProviderKey)
	if !ok {
		return "", false
	}
	if uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:track:"+strings.ToLower(native))) != track.TrackID {
		return "", false
	}
	return strings.ToLower(native), true
}

func providerPositionEvidenceMatches(raw json.RawMessage, authority int, fetchOrder int64) bool {
	var object map[string]json.RawMessage
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &object) != nil || object == nil {
		return false
	}
	var gotAuthority *int
	var gotFetchOrder *int64
	return json.Unmarshal(object["authority"], &gotAuthority) == nil &&
		gotAuthority != nil && *gotAuthority == authority &&
		json.Unmarshal(object["fetch_order"], &gotFetchOrder) == nil &&
		gotFetchOrder != nil && *gotFetchOrder == fetchOrder
}

type normalizedProviderTrack struct {
	ID                   string `json:"id"`
	RecordingID          string `json:"recording_id"`
	RecordingProviderKey string `json:"recording_provider_key"`
	Medium               *int   `json:"medium"`
	Position             *int   `json:"position"`
}

func matchingNormalizedProviderTrack(raw json.RawMessage, track providerPositionTrackSource) (normalizedProviderTrack, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return normalizedProviderTrack{}, false
	}
	var entries []normalizedProviderTrack
	if json.Unmarshal(raw, &entries) != nil {
		return normalizedProviderTrack{}, false
	}
	var match normalizedProviderTrack
	matches := 0
	for _, entry := range entries {
		id, err := uuid.Parse(entry.ID)
		if err != nil || id == uuid.Nil || entry.Medium == nil || entry.Position == nil {
			return normalizedProviderTrack{}, false
		}
		sameSlot := *entry.Medium == track.Medium && *entry.Position == track.Position
		if !sameSlot && id != track.TrackID {
			continue
		}
		if !sameSlot || id != track.TrackID {
			return normalizedProviderTrack{}, false
		}
		matches++
		match = entry
	}
	if matches != 1 {
		return normalizedProviderTrack{}, false
	}
	return match, true
}

func providerMediaTrackMBID(raw json.RawMessage, medium, position int, recordingProviderKey string) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var media []struct {
		Tracks []struct {
			ID        string `json:"id"`
			Recording struct {
				ID string `json:"id"`
			} `json:"recording"`
		} `json:"tracks"`
	}
	if json.Unmarshal(raw, &media) != nil {
		return "", false
	}
	if medium < 0 || medium >= len(media) || position < 0 || position >= len(media[medium].Tracks) {
		return "", false
	}
	track := media[medium].Tracks[position]
	if !providerValidMBID(track.ID) || !strings.EqualFold(track.Recording.ID, recordingProviderKey) {
		return "", false
	}
	return track.ID, true
}

func providerValidMBID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && strings.EqualFold(value, id.String())
}
