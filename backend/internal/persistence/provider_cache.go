package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type ProviderCacheRepository struct {
	db bun.IDB
}

// NormalizeMusicBrainzEndpoint applies the same trailing-slash normalization
// used by the HTTP adapter before deriving provider-source identity.
func NormalizeMusicBrainzEndpoint(endpoint string) string {
	return strings.TrimSuffix(endpoint, "/")
}

func NewProviderCacheRepository(db *bun.DB) *ProviderCacheRepository {
	return &ProviderCacheRepository{db: db}
}

func (repository *ProviderCacheRepository) CreateProvider(ctx context.Context, provider *Provider) error {
	if provider.ID == uuid.Nil {
		provider.ID = uuid.New()
	}
	if strings.TrimSpace(provider.Code) == "" {
		return fmt.Errorf("create provider: code is required")
	}
	if _, err := repository.db.NewInsert().Model(provider).Exec(ctx); err != nil {
		return fmt.Errorf("create provider: %w", err)
	}
	return nil
}

func (repository *ProviderCacheRepository) CreateProviderSource(ctx context.Context, source *ProviderSource) error {
	if source.ID == uuid.Nil {
		source.ID = uuid.New()
	}
	if source.ProviderID == uuid.Nil || strings.TrimSpace(source.Namespace) == "" || source.Endpoint == "" || source.ConfigurationIdentity == "" {
		return fmt.Errorf("create provider source: provider, namespace, endpoint, and configuration identity are required")
	}
	if _, err := repository.db.NewInsert().Model(source).Exec(ctx); err != nil {
		return fmt.Errorf("create provider source: %w", err)
	}
	return nil
}

// ResolveMusicBrainzSource returns the source synchronized with the current
// saved configuration. It is read-only and safe to repeat.
func (repository *ProviderCacheRepository) ResolveMusicBrainzSource(ctx context.Context, endpoint, expectedIdentity string) (ProviderSource, error) {
	if endpoint == "" || expectedIdentity == "" {
		return ProviderSource{}, fmt.Errorf("resolve MusicBrainz provider source: endpoint and configuration identity are required")
	}
	var source ProviderSource
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("resolve MusicBrainz provider source: lock configuration: %w", err)
		}
		var identity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &identity); err != nil {
			return fmt.Errorf("resolve MusicBrainz provider source: read current identity: %w", err)
		}
		if identity != expectedIdentity {
			return fmt.Errorf("resolve MusicBrainz provider source: configuration changed")
		}
		if err := tx.NewRaw(`SELECT id, provider_id, namespace, endpoint, configuration_identity
			FROM provider_source WHERE provider_id=(SELECT id FROM provider WHERE code='musicbrainz')
			AND endpoint=? AND configuration_identity=?`, endpoint, identity).Scan(ctx, &source); err != nil {
			return fmt.Errorf("resolve MusicBrainz provider source: read source: %w", err)
		}
		return nil
	})
	if err != nil {
		return ProviderSource{}, err
	}
	return source, nil
}

// BeginRefresh reserves a generation before network work starts. The repository
// performs no HTTP and holds no transaction open for the caller's request.
func (repository *ProviderCacheRepository) BeginRefresh(ctx context.Context, sourceID uuid.UUID, cacheKey, expectedConfigurationIdentity string) (ProviderRefreshTicket, error) {
	if sourceID == uuid.Nil || cacheKey == "" || expectedConfigurationIdentity == "" {
		return ProviderRefreshTicket{}, fmt.Errorf("begin provider refresh: source, key, and configuration identity are required")
	}
	var ticket ProviderRefreshTicket
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("begin provider refresh: lock configuration: %w", err)
		}
		var currentIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentIdentity); err != nil {
			return fmt.Errorf("begin provider refresh: read current configuration: %w", err)
		}
		if currentIdentity != expectedConfigurationIdentity {
			return fmt.Errorf("begin provider refresh: configuration changed")
		}
		var source ProviderSource
		if err := tx.NewRaw(`SELECT id, provider_id, namespace, endpoint, configuration_identity
			FROM provider_source WHERE id = ? FOR SHARE`, sourceID).Scan(ctx, &source); err != nil {
			return fmt.Errorf("begin provider refresh: read source: %w", err)
		}
		if source.ConfigurationIdentity != expectedConfigurationIdentity {
			return fmt.Errorf("begin provider refresh: source configuration changed")
		}
		var generation, fetchOrder int64
		if err := tx.NewRaw(`INSERT INTO provider_response_cache
			(provider_source_id, provider_id, cache_key, generation, fetch_order)
			VALUES (?, ?, ?, 1, nextval('provider_fetch_order_seq'))
			ON CONFLICT (provider_source_id, cache_key) DO UPDATE
			SET generation = provider_response_cache.generation + 1,
			fetch_order = nextval('provider_fetch_order_seq')
			WHERE provider_response_cache.provider_id = EXCLUDED.provider_id
			RETURNING generation, fetch_order`, sourceID, source.ProviderID, cacheKey).Scan(ctx, &generation, &fetchOrder); err != nil {
			return fmt.Errorf("begin provider refresh: reserve generation: %w", err)
		}
		ticket = ProviderRefreshTicket{ProviderSourceID: sourceID, ProviderID: source.ProviderID, CacheKey: cacheKey, Generation: generation, ConfigurationIdentity: expectedConfigurationIdentity, FetchOrder: fetchOrder}
		return nil
	})
	if err != nil {
		return ProviderRefreshTicket{}, err
	}
	return ticket, nil
}

func (repository *ProviderCacheRepository) GetCachedResponse(ctx context.Context, sourceID uuid.UUID, cacheKey string) (*ProviderCachedResponse, error) {
	response := new(ProviderCachedResponse)
	err := repository.db.NewRaw(`SELECT provider_source_id, provider_id, cache_key, payload, fetched_at, revision, generation, fetch_order
		FROM provider_response_cache WHERE provider_source_id = ? AND cache_key = ?`, sourceID, cacheKey).Scan(ctx, response)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cached provider response: %w", err)
	}
	return response, nil
}

// GetCurrentCachedResponse is a cache-only read fenced by the current saved
// MusicBrainz configuration. It performs no refresh and cannot return a prior
// endpoint's cache after a concurrent configuration switch.
func (repository *ProviderCacheRepository) GetCurrentCachedResponse(ctx context.Context, sourceID uuid.UUID, cacheKey, expectedIdentity string) (*ProviderCachedResponse, error) {
	var response *ProviderCachedResponse
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("get current cached provider response: lock configuration: %w", err)
		}
		var identity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &identity); err != nil {
			return fmt.Errorf("get current cached provider response: read configuration: %w", err)
		}
		if identity != expectedIdentity {
			return fmt.Errorf("get current cached provider response: configuration changed")
		}
		var sourceIdentity string
		if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=?`, sourceID).Scan(ctx, &sourceIdentity); err != nil {
			return fmt.Errorf("get current cached provider response: read source: %w", err)
		}
		if sourceIdentity != expectedIdentity {
			return fmt.Errorf("get current cached provider response: source configuration changed")
		}
		result := new(ProviderCachedResponse)
		err := tx.NewRaw(`SELECT provider_source_id, provider_id, cache_key, payload, fetched_at, revision, generation, fetch_order
			FROM provider_response_cache WHERE provider_source_id=? AND cache_key=?`, sourceID, cacheKey).Scan(ctx, result)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get current cached provider response: read cache: %w", err)
		}
		response = result
		return nil
	})
	return response, err
}

// ApplySuccessfulResponse commits the successful raw response and its normalized
// graph only while both the reserved generation and source configuration remain
// current. Failed requests must not call this method.
func (repository *ProviderCacheRepository) ApplySuccessfulResponse(ctx context.Context, response ProviderSuccessfulResponse) error {
	ticket := response.Ticket
	if ticket.ProviderSourceID == uuid.Nil || ticket.ProviderID == uuid.Nil || ticket.CacheKey == "" || ticket.Generation < 1 || ticket.FetchOrder < 1 || ticket.ConfigurationIdentity == "" || response.FetchedAt.IsZero() {
		return fmt.Errorf("apply successful provider response: refresh ticket and fetch time are required")
	}
	if err := validProviderJSON(response.Payload, true); err != nil {
		return fmt.Errorf("apply successful provider response: invalid payload: %w", err)
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("apply successful provider response: lock configuration: %w", err)
		}
		var currentIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentIdentity); err != nil {
			return fmt.Errorf("apply successful provider response: read current configuration: %w", err)
		}
		if currentIdentity != ticket.ConfigurationIdentity {
			return fmt.Errorf("apply successful provider response: configuration changed")
		}
		if response.Delivery != nil {
			fence := response.Delivery
			if fence.OperationID == uuid.Nil || fence.Attempt < 1 || fence.RiverJobID < 1 || fence.ExecutionEpoch < 1 {
				return fmt.Errorf("apply successful provider response: invalid operation delivery fence")
			}
			var kind, state, configurationIdentity, cacheKey string
			var attempt int
			var executionEpoch int64
			var riverJobID sql.NullInt64
			var sourceID uuid.UUID
			if err := tx.NewRaw(`SELECT kind, state, attempt, river_job_id, provider_source_id, provider_configuration_identity, provider_cache_key, provider_execution_epoch
					FROM operation WHERE id=? FOR UPDATE`, fence.OperationID).Scan(ctx, &kind, &state, &attempt, &riverJobID, &sourceID, &configurationIdentity, &cacheKey, &executionEpoch); err != nil {
				return fmt.Errorf("apply successful provider response: read operation delivery: %w", err)
			}
			if !isProviderOperationKind(kind) || state != "running" || attempt != fence.Attempt || !riverJobID.Valid || riverJobID.Int64 != fence.RiverJobID || executionEpoch != fence.ExecutionEpoch ||
				sourceID != ticket.ProviderSourceID || configurationIdentity != ticket.ConfigurationIdentity || cacheKey != ticket.CacheKey {
				return fmt.Errorf("apply successful provider response: operation delivery is stale")
			}
		}
		var source ProviderSource
		if err := tx.NewRaw(`SELECT id, provider_id, configuration_identity FROM provider_source WHERE id = ? FOR UPDATE`, ticket.ProviderSourceID).Scan(ctx, &source); err != nil {
			return fmt.Errorf("apply successful provider response: read source: %w", err)
		}
		if source.ProviderID != ticket.ProviderID || source.ConfigurationIdentity != ticket.ConfigurationIdentity {
			return fmt.Errorf("apply successful provider response: source configuration changed")
		}
		var currentGeneration, currentFetchOrder int64
		if err := tx.NewRaw(`SELECT generation, fetch_order FROM provider_response_cache
			WHERE provider_source_id = ? AND cache_key = ? FOR UPDATE`, ticket.ProviderSourceID, ticket.CacheKey).Scan(ctx, &currentGeneration, &currentFetchOrder); err != nil {
			return fmt.Errorf("apply successful provider response: read refresh generation: %w", err)
		}
		if currentGeneration != ticket.Generation || currentFetchOrder != ticket.FetchOrder {
			return fmt.Errorf("apply successful provider response: refresh generation is stale")
		}
		if _, err := tx.NewRaw(`UPDATE provider_response_cache SET payload = ?::jsonb, fetched_at = ?, revision = NULLIF(?, '')
				WHERE provider_source_id = ? AND cache_key = ? AND generation = ? AND fetch_order = ?`, string(response.Payload), response.FetchedAt, response.Revision, ticket.ProviderSourceID, ticket.CacheKey, ticket.Generation, ticket.FetchOrder).Exec(ctx); err != nil {
			return fmt.Errorf("apply successful provider response: save response: %w", err)
		}
		if err := applyProviderSourceGraph(ctx, tx, ticket, response.Graph); err != nil {
			return fmt.Errorf("apply successful provider response: save source projection: %w", err)
		}
		if len(response.Graph.Entities) == 0 {
			if len(response.Graph.Entities) == 0 {
				if err := applyProviderGraph(ctx, tx, ticket, response.Graph); err != nil {
					return fmt.Errorf("apply successful provider response: save graph: %w", err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func applyProviderSourceGraph(ctx context.Context, tx bun.Tx, ticket ProviderRefreshTicket, graph ProviderGraph) error {
	for _, entity := range graph.Entities {
		if entity.ProviderKey == "" || (entity.Kind != "artist" && entity.Kind != "release" && entity.Kind != "recording") || entity.Authority < 0 {
			return fmt.Errorf("source entity kind, provider key, and authority are required")
		}
		fields, err := providerJSON(entity.Fields, true)
		if err != nil {
			return fmt.Errorf("source entity fields: %w", err)
		}
		raw, err := providerJSON(entity.RawSource, false)
		if err != nil {
			return fmt.Errorf("source entity source: %w", err)
		}
		_, err = tx.NewRaw(`INSERT INTO provider_source_entity
			(provider_source_id, provider_id, entity_kind, provider_key, name, fields, field_evidence, raw_source, authority, complete, fetch_order)
			VALUES (?, ?, ?, ?, '', '{}'::jsonb, '{}'::jsonb, ?::jsonb, ?, false, ?)
			ON CONFLICT (provider_source_id, entity_kind, provider_key) DO NOTHING`,
			ticket.ProviderSourceID, ticket.ProviderID, entity.Kind, entity.ProviderKey, raw, entity.Authority, ticket.FetchOrder).Exec(ctx)
		if err != nil {
			return err
		}
		var currentFields, currentEvidence string
		if err := tx.NewRaw(`SELECT fields, field_evidence FROM provider_source_entity
			WHERE provider_source_id=? AND entity_kind=? AND provider_key=? FOR UPDATE`, ticket.ProviderSourceID, entity.Kind, entity.ProviderKey).Scan(ctx, &currentFields, &currentEvidence); err != nil {
			return err
		}
		storedFields := map[string]json.RawMessage{}
		storedEvidence := map[string]ProviderFieldEvidence{}
		if err := json.Unmarshal([]byte(currentFields), &storedFields); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(currentEvidence), &storedEvidence); err != nil {
			return err
		}
		incomingFields := map[string]json.RawMessage{}
		if err := json.Unmarshal(json.RawMessage(fields), &incomingFields); err != nil {
			return err
		}
		changed := false
		applyField := func(key string, value json.RawMessage, supplied bool) {
			if !supplied {
				return
			}
			incomingAuthority := entity.Authority
			if evidence, ok := entity.FieldEvidence[key]; ok {
				incomingAuthority = evidence.Authority
			}
			previous, exists := storedEvidence[key]
			if !exists || incomingAuthority > previous.Authority ||
				(incomingAuthority == previous.Authority && ticket.FetchOrder > previous.FetchOrder) {
				storedFields[key] = value
				storedEvidence[key] = ProviderFieldEvidence{Authority: incomingAuthority, FetchOrder: ticket.FetchOrder}
				changed = true
			}
		}
		for key, value := range incomingFields {
			applyField(key, value, true)
		}
		if entity.Name != "" {
			applyField("name", json.RawMessage(strconv.Quote(entity.Name)), true)
		}
		if entity.Complete {
			for _, key := range []string{"__tracks", "__credits"} {
				previous, exists := storedEvidence[key]
				if !exists || entity.Authority > previous.Authority ||
					(entity.Authority == previous.Authority && ticket.FetchOrder > previous.FetchOrder) {
					storedEvidence[key] = ProviderFieldEvidence{Authority: entity.Authority, FetchOrder: ticket.FetchOrder}
					changed = true
				}
			}
		}
		encodedFields, err := json.Marshal(storedFields)
		if err != nil {
			return err
		}
		encodedEvidence, err := json.Marshal(storedEvidence)
		if err != nil {
			return err
		}
		if changed {
			if _, err := tx.NewRaw(`UPDATE provider_source_entity SET fields=?::jsonb, field_evidence=?::jsonb,
				name=COALESCE(NULLIF((?::jsonb->>'name'), ''), name), raw_source=?::jsonb,
				authority=GREATEST(authority, ?), complete=complete OR ?, fetch_order=GREATEST(fetch_order, ?), updated_at=now()
				WHERE provider_source_id=? AND entity_kind=? AND provider_key=?`, string(encodedFields), string(encodedEvidence), string(encodedFields), raw,
				entity.Authority, entity.Complete, ticket.FetchOrder, ticket.ProviderSourceID, entity.Kind, entity.ProviderKey).Exec(ctx); err != nil {
				return err
			}
		} else if entity.Complete {
			if _, err := tx.NewRaw(`UPDATE provider_source_entity SET complete=true WHERE provider_source_id=? AND entity_kind=? AND provider_key=?`, ticket.ProviderSourceID, entity.Kind, entity.ProviderKey).Exec(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func validProviderJSON(value json.RawMessage, allowArray bool) error {
	if len(value) == 0 || !json.Valid(value) {
		return fmt.Errorf("JSON value is required")
	}
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return err
	}
	switch decoded.(type) {
	case map[string]any:
		return nil
	case []any:
		if allowArray {
			return nil
		}
	}
	return fmt.Errorf("expected JSON object or array")
}

func providerJSON(value json.RawMessage, objectOnly bool) (string, error) {
	if len(value) == 0 {
		return "{}", nil
	}
	if err := validProviderJSON(value, !objectOnly); err != nil {
		return "", err
	}
	return string(value), nil
}

func applyProviderGraph(ctx context.Context, tx bun.Tx, ticket ProviderRefreshTicket, graph ProviderGraph) error {
	providerID := ticket.ProviderID
	artistIDs := make(map[uuid.UUID]uuid.UUID, len(graph.Artists))
	releaseIDs := make(map[uuid.UUID]uuid.UUID, len(graph.Releases))
	recordingIDs := make(map[uuid.UUID]uuid.UUID, len(graph.Recordings))
	for index := range graph.Artists {
		artist := &graph.Artists[index]
		if artist.ID == uuid.Nil || artist.ProviderKey == "" || artist.Name == "" {
			return fmt.Errorf("artist id, provider key, and name are required")
		}
		metadata, err := providerJSON(artist.Metadata, true)
		if err != nil {
			return fmt.Errorf("artist metadata: %w", err)
		}
		raw, err := providerJSON(artist.RawSource, false)
		if err != nil {
			return fmt.Errorf("artist source: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_artist(id, provider_id, provider_key, name, metadata, raw_source)
			VALUES (?, ?, ?, ?, ?::jsonb, ?::jsonb)
			ON CONFLICT (provider_id, provider_key) DO UPDATE SET name=EXCLUDED.name,
			metadata=CASE WHEN ? THEN EXCLUDED.metadata ELSE provider_artist.metadata END,
			raw_source=CASE WHEN ? THEN EXCLUDED.raw_source ELSE provider_artist.raw_source END, updated_at=now()`,
			artist.ID, providerID, artist.ProviderKey, artist.Name, metadata, raw, len(artist.Metadata) > 0, len(artist.RawSource) > 0).Exec(ctx); err != nil {
			return err
		}
		inputID := artist.ID
		if err := tx.NewRaw(`SELECT id FROM provider_artist WHERE provider_id=? AND provider_key=?`, providerID, artist.ProviderKey).Scan(ctx, &artist.ID); err != nil {
			return err
		}
		artistIDs[inputID] = artist.ID
	}
	for index := range graph.Releases {
		release := &graph.Releases[index]
		if release.ID == uuid.Nil || release.ProviderKey == "" || release.Title == "" {
			return fmt.Errorf("release id, provider key, and title are required")
		}
		metadata, err := providerJSON(release.Metadata, true)
		if err != nil {
			return fmt.Errorf("release metadata: %w", err)
		}
		raw, err := providerJSON(release.RawSource, false)
		if err != nil {
			return fmt.Errorf("release source: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_release(id, provider_id, provider_key, title, metadata, raw_source)
			VALUES (?, ?, ?, ?, ?::jsonb, ?::jsonb)
			ON CONFLICT (provider_id, provider_key) DO UPDATE SET title=EXCLUDED.title,
			metadata=CASE WHEN ? THEN EXCLUDED.metadata ELSE provider_release.metadata END,
			raw_source=CASE WHEN ? THEN EXCLUDED.raw_source ELSE provider_release.raw_source END, updated_at=now()`,
			release.ID, providerID, release.ProviderKey, release.Title, metadata, raw, len(release.Metadata) > 0, len(release.RawSource) > 0).Exec(ctx); err != nil {
			return err
		}
		inputID := release.ID
		if err := tx.NewRaw(`SELECT id FROM provider_release WHERE provider_id=? AND provider_key=?`, providerID, release.ProviderKey).Scan(ctx, &release.ID); err != nil {
			return err
		}
		releaseIDs[inputID] = release.ID
	}
	for index := range graph.Recordings {
		recording := &graph.Recordings[index]
		if recording.ID == uuid.Nil || recording.ProviderKey == "" || recording.Title == "" {
			return fmt.Errorf("recording id, provider key, and title are required")
		}
		metadata, err := providerJSON(recording.Metadata, true)
		if err != nil {
			return fmt.Errorf("recording metadata: %w", err)
		}
		raw, err := providerJSON(recording.RawSource, false)
		if err != nil {
			return fmt.Errorf("recording source: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_recording(id, provider_id, provider_key, title, metadata, raw_source)
			VALUES (?, ?, ?, ?, ?::jsonb, ?::jsonb)
			ON CONFLICT (provider_id, provider_key) DO UPDATE SET title=EXCLUDED.title,
			metadata=CASE WHEN ? THEN EXCLUDED.metadata ELSE provider_recording.metadata END,
			raw_source=CASE WHEN ? THEN EXCLUDED.raw_source ELSE provider_recording.raw_source END, updated_at=now()`,
			recording.ID, providerID, recording.ProviderKey, recording.Title, metadata, raw, len(recording.Metadata) > 0, len(recording.RawSource) > 0).Exec(ctx); err != nil {
			return err
		}
		inputID := recording.ID
		if err := tx.NewRaw(`SELECT id FROM provider_recording WHERE provider_id=? AND provider_key=?`, providerID, recording.ProviderKey).Scan(ctx, &recording.ID); err != nil {
			return err
		}
		recordingIDs[inputID] = recording.ID
	}
	for index := range graph.Credits {
		credit := &graph.Credits[index]
		if id, ok := releaseIDs[credit.ReleaseID]; ok {
			credit.ReleaseID = id
		}
		if id, ok := artistIDs[credit.ArtistID]; ok {
			credit.ArtistID = id
		}
	}
	for index := range graph.Tracks {
		track := &graph.Tracks[index]
		if id, ok := releaseIDs[track.ReleaseID]; ok {
			track.ReleaseID = id
		}
		if id, ok := recordingIDs[track.RecordingID]; ok {
			track.RecordingID = id
		}
	}
	collectionAuthority := make(map[uuid.UUID]int)
	collectionAccepted := make(map[uuid.UUID]map[string]bool)
	for _, entity := range graph.Entities {
		if entity.Kind == "release" {
			releaseID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:release:"+strings.ToLower(entity.ProviderKey)))
			collectionAuthority[releaseID] = entity.Authority
			var raw string
			if err := tx.NewRaw(`SELECT field_evidence FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, ticket.ProviderSourceID, entity.ProviderKey).Scan(ctx, &raw); err != nil {
				return err
			}
			var evidence map[string]ProviderFieldEvidence
			if err := json.Unmarshal([]byte(raw), &evidence); err != nil {
				return err
			}
			accepted := map[string]bool{}
			for _, key := range []string{"tracks", "credits"} {
				fence := evidence["__"+key]
				accepted[key] = fence.Authority == entity.Authority && fence.FetchOrder == ticket.FetchOrder
			}
			collectionAccepted[releaseID] = accepted
		}
	}
	collectionRank := func(releaseID uuid.UUID) int {
		if authority, ok := collectionAuthority[releaseID]; ok {
			return authority
		}
		if graph.Complete {
			return 32767
		}
		return 0
	}
	if graph.Complete {
		for _, release := range graph.Releases {
			accepted := collectionAccepted[release.ID]
			authority := collectionRank(release.ID)
			if accepted == nil || accepted["tracks"] {
				if _, err := tx.NewRaw(`UPDATE provider_release_track SET present=false, medium=NULL, position=NULL, authority=?, fetch_order=? WHERE provider_id=? AND release_id=?
					AND (authority < ? OR (authority = ? AND fetch_order < ?))`, authority, ticket.FetchOrder, providerID, release.ID, authority, authority, ticket.FetchOrder).Exec(ctx); err != nil {
					return err
				}
			}
			if accepted == nil || accepted["credits"] {
				if _, err := tx.NewRaw(`UPDATE provider_release_artist_credit SET present=false, authority=?, fetch_order=? WHERE provider_id=? AND release_id=?
				AND (authority < ? OR (authority = ? AND fetch_order < ?))`, authority, ticket.FetchOrder, providerID, release.ID, authority, authority, ticket.FetchOrder).Exec(ctx); err != nil {
					return err
				}
			}
		}
		if _, err := tx.NewRaw(`SET CONSTRAINTS ALL DEFERRED`).Exec(ctx); err != nil {
			return err
		}
	}
	for _, credit := range graph.Credits {
		if credit.ReleaseID == uuid.Nil || credit.ArtistID == uuid.Nil || credit.Position < 0 {
			return fmt.Errorf("valid ordered release credit is required")
		}
		authority := collectionRank(credit.ReleaseID)
		if accepted := collectionAccepted[credit.ReleaseID]; accepted != nil && !accepted["credits"] {
			continue
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_release_artist_credit(provider_id, release_id, position, artist_id, name_join_phrase, present, authority, fetch_order)
			VALUES (?, ?, ?, ?, ?, true, ?, ?) ON CONFLICT (provider_id, release_id, position) DO UPDATE
			SET artist_id=EXCLUDED.artist_id, name_join_phrase=EXCLUDED.name_join_phrase, present=true, authority=EXCLUDED.authority, fetch_order=EXCLUDED.fetch_order
			WHERE EXCLUDED.authority > provider_release_artist_credit.authority OR (EXCLUDED.authority = provider_release_artist_credit.authority AND (EXCLUDED.fetch_order > provider_release_artist_credit.fetch_order OR (EXCLUDED.fetch_order = provider_release_artist_credit.fetch_order AND NOT provider_release_artist_credit.present)))`, providerID, credit.ReleaseID, credit.Position, credit.ArtistID, credit.NameJoinPhrase, authority, ticket.FetchOrder).Exec(ctx); err != nil {
			return err
		}
	}
	for _, track := range graph.Tracks {
		if track.ID == uuid.Nil || track.ReleaseID == uuid.Nil || track.RecordingID == uuid.Nil || track.Medium < 0 || track.Position < 0 {
			return fmt.Errorf("valid release track position is required")
		}
		authority := collectionRank(track.ReleaseID)
		if accepted := collectionAccepted[track.ReleaseID]; accepted != nil && !accepted["tracks"] {
			continue
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_release_track(id, provider_id, release_id, recording_id, medium, position, displayed_number, present, authority, fetch_order)
			VALUES (?, ?, ?, ?, ?, ?, ?, true, ?, ?) ON CONFLICT (provider_id, id) DO UPDATE SET
		release_id=EXCLUDED.release_id, recording_id=EXCLUDED.recording_id, medium=EXCLUDED.medium, position=EXCLUDED.position,
		displayed_number=EXCLUDED.displayed_number, present=true, authority=EXCLUDED.authority, fetch_order=EXCLUDED.fetch_order
		WHERE EXCLUDED.authority > provider_release_track.authority OR (EXCLUDED.authority = provider_release_track.authority AND (EXCLUDED.fetch_order > provider_release_track.fetch_order OR (EXCLUDED.fetch_order = provider_release_track.fetch_order AND NOT provider_release_track.present)))`, track.ID, providerID, track.ReleaseID, track.RecordingID, track.Medium, track.Position, track.DisplayedNumber, authority, ticket.FetchOrder).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
