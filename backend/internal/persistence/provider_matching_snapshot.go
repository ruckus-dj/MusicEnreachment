package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ProviderMatchingSnapshot is a cache-only, source-scoped view of the accepted
// MusicBrainz projection and the exact cached query responses requested by the
// caller. A nil response entry means that no cache row has ever been created;
// a non-nil row with a nil payload is a reserved, not-yet-loaded response.
type ProviderMatchingSnapshot struct {
	Source                     ProviderSource
	Releases                   []MatchingRelease
	Responses                  map[string]*ProviderCachedResponse
	SearchScores               map[string]map[string]MatchingSearchScore
	AcceptedProjectionRevision string
	ProjectionEntities         []MatchingProjectionEntity
}

type MatchingProjectionEntity struct {
	Kind          string
	ProviderKey   string
	Name          string
	Fields        json.RawMessage
	FieldEvidence json.RawMessage
	RawSource     json.RawMessage
	Authority     int
	FetchOrder    int64
	Complete      bool
}

// MatchingSearchScore is scoped to one exact cached query key and candidate.
// Present=false is distinct from a valid numeric score of zero.
type MatchingSearchScore struct {
	Value   float64
	Present bool
}

// MatchingRelease and its children contain only persistence-owned values; no
// MusicBrainz SDK types cross the repository boundary.
type MatchingRelease struct {
	ProviderKey   string
	Name          string
	Fields        json.RawMessage
	FieldEvidence map[string]ProviderFieldEvidence
	RawSource     json.RawMessage
	Authority     int
	FetchOrder    int64
	Complete      bool
	Credits       []MatchingReleaseCredit
	Tracks        []MatchingReleaseTrack
}

type MatchingReleaseCredit struct {
	Position          int
	ArtistProviderKey string
	Name              string
	NameJoinPhrase    string
	Authority         int
	FetchOrder        int64
}

type MatchingReleaseTrack struct {
	ID                   uuid.UUID
	Medium               int // Persisted zero-based medium ordinal.
	Position             int // Persisted zero-based track ordinal.
	DisplayedNumber      string
	RecordingProviderKey string
	RecordingTitle       string
	RecordingArtists     []string
	RecordingLengthMS    *int64
	ReleaseLengthMS      *int64
	Authority            int
	FetchOrder           int64
}

// GetMusicBrainzMatchingSnapshot reads a consistent cache-only projection for
// one configured source. releaseKeys and cacheKeys are exact provider keys and
// cache keys, respectively; successful-response revisions are deliberately not
// used as projection fences because an unsuccessful refresh reservation must
// not invalidate previously accepted data.
func (repository *ProviderCacheRepository) GetMusicBrainzMatchingSnapshot(
	ctx context.Context,
	endpoint string,
	expectedIdentity string,
	releaseKeys []string,
	cacheKeys []string,
) (ProviderMatchingSnapshot, error) {
	if endpoint == "" || expectedIdentity == "" {
		return ProviderMatchingSnapshot{}, fmt.Errorf("get MusicBrainz matching snapshot: endpoint and configuration identity are required")
	}
	var snapshot ProviderMatchingSnapshot
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var err error
		snapshot, err = readMusicBrainzMatchingSnapshot(ctx, tx, endpoint, expectedIdentity, releaseKeys, cacheKeys, true)
		return err
	})
	if err != nil {
		return ProviderMatchingSnapshot{}, err
	}
	return snapshot, nil
}

// readMusicBrainzMatchingSnapshot is shared by standalone reads and publication
// validation. The latter passes a transaction that already owns the config lock.
func readMusicBrainzMatchingSnapshot(ctx context.Context, tx bun.Tx, endpoint, expectedIdentity string, releaseKeys, cacheKeys []string, lockConfig bool) (ProviderMatchingSnapshot, error) {
	snapshot := ProviderMatchingSnapshot{Responses: make(map[string]*ProviderCachedResponse, len(cacheKeys)), SearchScores: make(map[string]map[string]MatchingSearchScore, len(cacheKeys))}
	for _, key := range cacheKeys {
		snapshot.Responses[key] = nil
	}
	if lockConfig {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return snapshot, fmt.Errorf("get MusicBrainz matching snapshot: lock configuration: %w", err)
		}
	}
	var identity string
	if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &identity); err != nil {
		return snapshot, fmt.Errorf("get MusicBrainz matching snapshot: read configuration: %w", err)
	}
	if identity != expectedIdentity {
		return snapshot, fmt.Errorf("get MusicBrainz matching snapshot: configuration changed")
	}
	if err := tx.NewRaw(`SELECT id, provider_id, namespace, endpoint, configuration_identity FROM provider_source WHERE provider_id=(SELECT id FROM provider WHERE code='musicbrainz') AND endpoint=? AND configuration_identity=?`, endpoint, identity).Scan(ctx, &snapshot.Source); err != nil {
		return snapshot, fmt.Errorf("get MusicBrainz matching snapshot: read active source: %w", err)
	}
	if len(releaseKeys) != 0 {
		if err := readMatchingReleases(ctx, tx, snapshot.Source.ID, snapshot.Source.ProviderID, releaseKeys, &snapshot.Releases); err != nil {
			return snapshot, fmt.Errorf("get MusicBrainz matching snapshot: read release graph: %w", err)
		}
		artistKeys, recordingKeys := map[string]bool{}, map[string]bool{}
		for _, release := range snapshot.Releases {
			for _, credit := range release.Credits {
				artistKeys[credit.ArtistProviderKey] = true
			}
			for _, track := range release.Tracks {
				recordingKeys[track.RecordingProviderKey] = true
			}
		}
		artists, recordings := mapKeys(artistKeys), mapKeys(recordingKeys)
		if err := tx.NewRaw(`SELECT entity_kind AS kind, provider_key, name, fields, field_evidence, raw_source, authority, fetch_order, complete
			FROM provider_source_entity WHERE provider_source_id=? AND provider_id=? AND
			((entity_kind='release' AND provider_key IN (?)) OR (entity_kind='artist' AND provider_key IN (?)) OR (entity_kind='recording' AND provider_key IN (?)))
			ORDER BY entity_kind, provider_key`, snapshot.Source.ID, snapshot.Source.ProviderID, bun.List(releaseKeys), bun.List(artists), bun.List(recordings)).Scan(ctx, &snapshot.ProjectionEntities); err != nil {
			return snapshot, fmt.Errorf("read related source entity fences: %w", err)
		}
		recordingArtistKeys := make(map[string]bool)
		for _, entity := range snapshot.ProjectionEntities {
			if entity.Kind == "recording" {
				for _, artistKey := range matchingRecordingArtistKeys(entity.Fields) {
					if !artistKeysContains(artists, artistKey) {
						recordingArtistKeys[artistKey] = true
					}
				}
			}
		}
		if keys := mapKeys(recordingArtistKeys); len(keys) != 0 {
			rows := make([]matchingEntityRow, 0, len(keys))
			if err := tx.NewRaw(`SELECT provider_key, name, fields, field_evidence, raw_source, authority, fetch_order, complete
				FROM provider_source_entity WHERE provider_source_id=? AND provider_id=? AND entity_kind='artist' AND provider_key IN (?)
				ORDER BY provider_key`, snapshot.Source.ID, snapshot.Source.ProviderID, bun.List(keys)).Scan(ctx, &rows); err != nil {
				return snapshot, fmt.Errorf("read recording artist projections: %w", err)
			}
			for _, row := range rows {
				snapshot.ProjectionEntities = append(snapshot.ProjectionEntities, MatchingProjectionEntity{
					Kind: "artist", ProviderKey: row.ProviderKey, Name: row.Name, Fields: row.Fields,
					FieldEvidence: row.FieldEvidence, RawSource: row.RawSource, Authority: row.Authority,
					FetchOrder: row.FetchOrder, Complete: row.Complete,
				})
			}
		}
	}
	if len(cacheKeys) != 0 {
		rows := make([]ProviderCachedResponse, 0, len(cacheKeys))
		if err := tx.NewSelect().TableExpr("provider_response_cache").Column("provider_source_id", "provider_id", "cache_key", "payload", "fetched_at", "revision", "generation", "fetch_order").Where("provider_source_id = ?", snapshot.Source.ID).Where("cache_key IN (?)", bun.List(cacheKeys)).Scan(ctx, &rows); err != nil {
			return snapshot, fmt.Errorf("read cached query responses: %w", err)
		}
		for index := range rows {
			response := rows[index]
			snapshot.Responses[response.CacheKey] = &response
			scores, err := cachedMatchingSearchScores(response.Payload)
			if err != nil {
				return snapshot, fmt.Errorf("read cached query response %q scores: %w", response.CacheKey, err)
			}
			snapshot.SearchScores[response.CacheKey] = scores
		}
	}
	recordingProjections := make(map[string]MatchingProjectionEntity)
	sourceArtistNames := make(map[string]string)
	for _, entity := range snapshot.ProjectionEntities {
		switch entity.Kind {
		case "recording":
			recordingProjections[entity.ProviderKey] = entity
		case "artist":
			sourceArtistNames[entity.ProviderKey] = entity.Name
		}
	}
	for releaseIndex := range snapshot.Releases {
		for trackIndex := range snapshot.Releases[releaseIndex].Tracks {
			track := &snapshot.Releases[releaseIndex].Tracks[trackIndex]
			projection, ok := recordingProjections[track.RecordingProviderKey]
			if !ok {
				continue
			}
			track.RecordingArtists = matchingRecordingArtists(projection.Fields, sourceArtistNames)
			track.RecordingLengthMS = matchingRecordingLength(projection.Fields)
		}
	}
	snapshot.AcceptedProjectionRevision = acceptedProjectionRevision(snapshot.Releases, snapshot.ProjectionEntities, snapshot.Responses)
	return snapshot, nil
}

func artistKeysContains(keys []string, target string) bool {
	for _, key := range keys {
		if key == target {
			return true
		}
	}
	return false
}

func matchingRecordingArtists(fields json.RawMessage, sourceArtistNames map[string]string) []string {
	var projection map[string]json.RawMessage
	if json.Unmarshal(fields, &projection) != nil {
		return nil
	}
	if credits, present := projection["artist-credit"]; present {
		var values []json.RawMessage
		if json.Unmarshal(credits, &values) == nil {
			names := make([]string, 0, len(values))
			for _, value := range values {
				var credit map[string]json.RawMessage
				if json.Unmarshal(value, &credit) != nil {
					continue
				}
				name := matchingJSONString(credit["name"])
				if name == "" {
					var artist map[string]json.RawMessage
					_ = json.Unmarshal(credit["artist"], &artist)
					name = sourceArtistNames[strings.ToLower(matchingJSONString(artist["id"]))]
				}
				if name != "" {
					names = append(names, name)
				}
			}
			if len(names) != 0 {
				return names
			}
		} else {
			return nil
		}
	}
	var artistKeys []string
	if json.Unmarshal(projection["normalized_artists"], &artistKeys) == nil {
		names := make([]string, 0, len(artistKeys))
		for _, key := range artistKeys {
			if name := sourceArtistNames[strings.ToLower(key)]; name != "" {
				names = append(names, name)
			}
		}
		return names
	}
	return nil
}

func matchingRecordingArtistKeys(fields json.RawMessage) []string {
	var projection map[string]json.RawMessage
	if json.Unmarshal(fields, &projection) != nil {
		return nil
	}
	var credits []map[string]json.RawMessage
	if json.Unmarshal(projection["artist-credit"], &credits) == nil && len(credits) != 0 {
		keys := make([]string, 0, len(credits))
		for _, credit := range credits {
			var artist map[string]json.RawMessage
			if json.Unmarshal(credit["artist"], &artist) == nil {
				if key := matchingJSONString(artist["id"]); key != "" {
					keys = append(keys, strings.ToLower(key))
				}
			}
		}
		return keys
	}
	var keys []string
	if json.Unmarshal(projection["normalized_artists"], &keys) == nil {
		for index := range keys {
			keys[index] = strings.ToLower(keys[index])
		}
		return keys
	}
	return nil
}

func matchingRecordingLength(fields json.RawMessage) *int64 {
	var projection map[string]json.RawMessage
	if json.Unmarshal(fields, &projection) != nil {
		return nil
	}
	raw, ok := projection["length"]
	if !ok || len(raw) == 0 {
		return nil
	}
	var value *int64
	if json.Unmarshal(raw, &value) != nil || value == nil || *value < 0 {
		return nil
	}
	return value
}

func matchingJSONString(value json.RawMessage) string {
	var result string
	_ = json.Unmarshal(value, &result)
	return result
}

func acceptedProjectionRevision(releases []MatchingRelease, entities []MatchingProjectionEntity, responses map[string]*ProviderCachedResponse) string {
	type responseRevision struct {
		Key           string  `json:"key"`
		Revision      *string `json:"revision"`
		PayloadSHA256 string  `json:"payload_sha256"`
	}
	keys := make([]string, 0, len(responses))
	for key := range responses {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	revisions := make([]responseRevision, 0, len(keys))
	for _, key := range keys {
		var revision *string
		var payloadDigest string
		if response := responses[key]; response != nil {
			revision = response.Revision
			payloadHash := sha256.Sum256(response.Payload)
			payloadDigest = hex.EncodeToString(payloadHash[:])
		}
		revisions = append(revisions, responseRevision{key, revision, payloadDigest})
	}
	encoded, _ := json.Marshal(struct {
		Releases  []MatchingRelease          `json:"releases"`
		Entities  []MatchingProjectionEntity `json:"entities"`
		Responses []responseRevision         `json:"responses"`
	}{releases, entities, revisions})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func mapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cachedMatchingSearchScores(payload json.RawMessage) (map[string]MatchingSearchScore, error) {
	result := make(map[string]MatchingSearchScore)
	if len(payload) == 0 {
		return result, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode search envelope: %w", err)
	}
	for _, key := range []string{"releases", "recordings"} {
		rawItems, ok := envelope[key]
		if !ok || string(rawItems) == "null" {
			continue
		}
		var items []json.RawMessage
		if err := json.Unmarshal(rawItems, &items); err != nil {
			return nil, fmt.Errorf("decode %s search results: %w", key, err)
		}
		for _, rawItem := range items {
			var item struct {
				ID    string          `json:"id"`
				Score json.RawMessage `json:"score"`
			}
			if err := json.Unmarshal(rawItem, &item); err != nil {
				return nil, fmt.Errorf("decode %s search result: %w", key, err)
			}
			if item.ID == "" {
				continue
			}
			score := MatchingSearchScore{}
			if len(item.Score) != 0 && string(item.Score) != "null" {
				var value float64
				if err := json.Unmarshal(item.Score, &value); err != nil || value < 0 || value > 100 {
					return nil, fmt.Errorf("invalid MusicBrainz search score")
				}
				score = MatchingSearchScore{Value: value, Present: true}
			}
			result[item.ID] = score
		}
	}
	return result, nil
}

type matchingEntityRow struct {
	ProviderKey   string `bun:"provider_key"`
	Name          string `bun:"name"`
	Fields        []byte `bun:"fields"`
	FieldEvidence []byte `bun:"field_evidence"`
	RawSource     []byte `bun:"raw_source"`
	Authority     int    `bun:"authority"`
	FetchOrder    int64  `bun:"fetch_order"`
	Complete      bool   `bun:"complete"`
}

type matchingCreditRow struct {
	ReleaseKey string `bun:"release_key"`
	Position   int    `bun:"position"`
	ArtistKey  string `bun:"artist_key"`
	Name       string `bun:"name"`
	JoinPhrase string `bun:"name_join_phrase"`
	Authority  int    `bun:"authority"`
	FetchOrder int64  `bun:"fetch_order"`
}

type matchingTrackRow struct {
	ReleaseKey      string    `bun:"release_key"`
	ID              uuid.UUID `bun:"id"`
	Medium          int       `bun:"medium"`
	Position        int       `bun:"position"`
	DisplayedNumber string    `bun:"displayed_number"`
	RecordingKey    string    `bun:"recording_key"`
	RecordingTitle  string    `bun:"recording_title"`
	Authority       int       `bun:"authority"`
	FetchOrder      int64     `bun:"fetch_order"`
}

func readMatchingReleases(ctx context.Context, tx bun.Tx, sourceID, providerID uuid.UUID, keys []string, releases *[]MatchingRelease) error {
	entities := make([]matchingEntityRow, 0, len(keys))
	if err := tx.NewRaw(`SELECT e.provider_key, e.name, e.fields, e.field_evidence, e.raw_source, e.authority, e.fetch_order, e.complete
		FROM provider_source_entity e
		WHERE e.provider_source_id=? AND e.provider_id=? AND e.entity_kind='release' AND e.provider_key IN (?)
		ORDER BY e.provider_key`, sourceID, providerID, bun.List(keys)).Scan(ctx, &entities); err != nil {
		return err
	}
	byKey := make(map[string]int, len(entities))
	for _, row := range entities {
		release := MatchingRelease{ProviderKey: row.ProviderKey, Name: row.Name, Fields: append(json.RawMessage(nil), row.Fields...), RawSource: append(json.RawMessage(nil), row.RawSource...), Authority: row.Authority, FetchOrder: row.FetchOrder, Complete: row.Complete, FieldEvidence: make(map[string]ProviderFieldEvidence)}
		if err := json.Unmarshal(row.FieldEvidence, &release.FieldEvidence); err != nil {
			return err
		}
		*releases = append(*releases, release)
		byKey[row.ProviderKey] = len(*releases) - 1
	}
	if len(byKey) == 0 {
		return nil
	}
	requested := make([]string, 0, len(byKey))
	for key := range byKey {
		requested = append(requested, key)
	}
	sort.Strings(requested)
	credits := make([]matchingCreditRow, 0)
	if err := tx.NewRaw(`SELECT r.provider_key AS release_key, c.position, a.provider_key AS artist_key,
		COALESCE(NULLIF(c.name, ''), NULLIF(ae.name, ''), ae.fields->>'name', '') AS name,
		c.name_join_phrase, c.authority, c.fetch_order
		FROM provider_source_release_artist_credit c
		JOIN provider_release r ON r.id=c.release_id AND r.provider_id=c.provider_id
		JOIN provider_artist a ON a.id=c.artist_id AND a.provider_id=c.provider_id
		LEFT JOIN provider_source_entity ae ON ae.provider_source_id=c.provider_source_id AND ae.provider_id=c.provider_id AND ae.entity_kind='artist' AND ae.provider_key=a.provider_key
		JOIN provider_source_entity e ON e.provider_source_id=c.provider_source_id AND e.provider_id=c.provider_id AND e.entity_kind='release' AND e.provider_key=r.provider_key
		WHERE c.provider_source_id=? AND c.provider_id=? AND c.present=true AND r.provider_key IN (?) AND COALESCE((e.field_evidence->'__credits'->>'authority')::integer, -1)=c.authority
		AND COALESCE((e.field_evidence->'__credits'->>'fetch_order')::bigint, -1)=c.fetch_order
		ORDER BY r.provider_key, c.position`, sourceID, providerID, bun.List(requested)).Scan(ctx, &credits); err != nil {
		return err
	}
	for _, row := range credits {
		index := byKey[row.ReleaseKey]
		(*releases)[index].Credits = append((*releases)[index].Credits, MatchingReleaseCredit{Position: row.Position, ArtistProviderKey: row.ArtistKey, Name: row.Name, NameJoinPhrase: row.JoinPhrase, Authority: row.Authority, FetchOrder: row.FetchOrder})
	}
	tracks := make([]matchingTrackRow, 0)
	if err := tx.NewRaw(`SELECT r.provider_key AS release_key, t.track_id AS id, t.medium, t.position, t.displayed_number, rec.provider_key AS recording_key,
		COALESCE(NULLIF(re.source_name, ''), re.fields->>'title', '') AS recording_title, t.authority, t.fetch_order
		FROM provider_source_release_track t
		JOIN provider_release r ON r.id=t.release_id AND r.provider_id=t.provider_id
		JOIN provider_recording rec ON rec.id=t.recording_id AND rec.provider_id=t.provider_id
		LEFT JOIN LATERAL (SELECT e.name AS source_name, e.fields FROM provider_source_entity e
			WHERE e.provider_source_id=t.provider_source_id AND e.provider_id=t.provider_id AND e.entity_kind='recording' AND e.provider_key=rec.provider_key) re ON true
		JOIN provider_source_entity e ON e.provider_source_id=? AND e.provider_id=t.provider_id AND e.entity_kind='release' AND e.provider_key=r.provider_key
		WHERE t.provider_source_id=? AND t.provider_id=? AND t.present=true AND r.provider_key IN (?) AND COALESCE((e.field_evidence->'__tracks'->>'authority')::integer, -1)=t.authority
		AND COALESCE((e.field_evidence->'__tracks'->>'fetch_order')::bigint, -1)=t.fetch_order
		ORDER BY r.provider_key, t.medium, t.position, t.track_id`, sourceID, sourceID, providerID, bun.List(requested)).Scan(ctx, &tracks); err != nil {
		return err
	}
	for _, row := range tracks {
		index := byKey[row.ReleaseKey]
		release := &(*releases)[index]
		release.Tracks = append(release.Tracks, MatchingReleaseTrack{
			ID: row.ID, Medium: row.Medium, Position: row.Position, DisplayedNumber: row.DisplayedNumber,
			RecordingProviderKey: row.RecordingKey, RecordingTitle: row.RecordingTitle,
			ReleaseLengthMS: matchingReleaseTrackLength(release.Fields, row.Medium, row.Position, row.RecordingKey),
			Authority:       row.Authority, FetchOrder: row.FetchOrder,
		})
	}
	return nil
}

func matchingReleaseTrackLength(fields json.RawMessage, medium, position int, recordingKey string) *int64 {
	var release struct {
		Tracks []struct {
			Medium               int             `json:"medium"`
			Position             int             `json:"position"`
			RecordingProviderKey string          `json:"recording_provider_key"`
			Length               json.RawMessage `json:"length"`
		} `json:"normalized_tracks"`
	}
	if json.Unmarshal(fields, &release) != nil {
		return nil
	}
	for _, track := range release.Tracks {
		if track.Medium != medium || track.Position != position || !strings.EqualFold(track.RecordingProviderKey, recordingKey) {
			continue
		}
		var length *int64
		if len(track.Length) == 0 || json.Unmarshal(track.Length, &length) != nil || length == nil || *length < 0 {
			return nil
		}
		return length
	}
	return nil
}
