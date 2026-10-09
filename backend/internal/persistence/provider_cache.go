package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type ProviderCacheRepository struct {
	db bun.IDB
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

// BeginRefresh reserves a generation before network work starts. The repository
// performs no HTTP and holds no transaction open for the caller's request.
func (repository *ProviderCacheRepository) BeginRefresh(ctx context.Context, sourceID uuid.UUID, cacheKey, expectedConfigurationIdentity string) (ProviderRefreshTicket, error) {
	if sourceID == uuid.Nil || cacheKey == "" || expectedConfigurationIdentity == "" {
		return ProviderRefreshTicket{}, fmt.Errorf("begin provider refresh: source, key, and configuration identity are required")
	}
	var ticket ProviderRefreshTicket
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var source ProviderSource
		if err := tx.NewRaw(`SELECT id, provider_id, namespace, endpoint, configuration_identity
			FROM provider_source WHERE id = ? FOR SHARE`, sourceID).Scan(ctx, &source); err != nil {
			return fmt.Errorf("begin provider refresh: read source: %w", err)
		}
		if source.ConfigurationIdentity != expectedConfigurationIdentity {
			return fmt.Errorf("begin provider refresh: source configuration changed")
		}
		var generation int64
		if err := tx.NewRaw(`INSERT INTO provider_response_cache
			(provider_source_id, provider_id, cache_key, generation)
			VALUES (?, ?, ?, 1)
			ON CONFLICT (provider_source_id, cache_key) DO UPDATE
			SET generation = provider_response_cache.generation + 1
			WHERE provider_response_cache.provider_id = EXCLUDED.provider_id
			RETURNING generation`, sourceID, source.ProviderID, cacheKey).Scan(ctx, &generation); err != nil {
			return fmt.Errorf("begin provider refresh: reserve generation: %w", err)
		}
		ticket = ProviderRefreshTicket{ProviderSourceID: sourceID, ProviderID: source.ProviderID, CacheKey: cacheKey, Generation: generation, ConfigurationIdentity: expectedConfigurationIdentity}
		return nil
	})
	if err != nil {
		return ProviderRefreshTicket{}, err
	}
	return ticket, nil
}

func (repository *ProviderCacheRepository) GetCachedResponse(ctx context.Context, sourceID uuid.UUID, cacheKey string) (*ProviderCachedResponse, error) {
	response := new(ProviderCachedResponse)
	err := repository.db.NewRaw(`SELECT provider_source_id, provider_id, cache_key, payload, fetched_at, revision, generation
		FROM provider_response_cache WHERE provider_source_id = ? AND cache_key = ?`, sourceID, cacheKey).Scan(ctx, response)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cached provider response: %w", err)
	}
	return response, nil
}

// ApplySuccessfulResponse commits the successful raw response and its normalized
// graph only while both the reserved generation and source configuration remain
// current. Failed requests must not call this method.
func (repository *ProviderCacheRepository) ApplySuccessfulResponse(ctx context.Context, response ProviderSuccessfulResponse) error {
	ticket := response.Ticket
	if ticket.ProviderSourceID == uuid.Nil || ticket.ProviderID == uuid.Nil || ticket.CacheKey == "" || ticket.Generation < 1 || ticket.ConfigurationIdentity == "" || response.FetchedAt.IsZero() {
		return fmt.Errorf("apply successful provider response: refresh ticket and fetch time are required")
	}
	if err := validProviderJSON(response.Payload, true); err != nil {
		return fmt.Errorf("apply successful provider response: invalid payload: %w", err)
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var source ProviderSource
		if err := tx.NewRaw(`SELECT id, provider_id, configuration_identity FROM provider_source WHERE id = ? FOR UPDATE`, ticket.ProviderSourceID).Scan(ctx, &source); err != nil {
			return fmt.Errorf("apply successful provider response: read source: %w", err)
		}
		if source.ProviderID != ticket.ProviderID || source.ConfigurationIdentity != ticket.ConfigurationIdentity {
			return fmt.Errorf("apply successful provider response: source configuration changed")
		}
		var currentGeneration int64
		if err := tx.NewRaw(`SELECT generation FROM provider_response_cache
			WHERE provider_source_id = ? AND cache_key = ? FOR UPDATE`, ticket.ProviderSourceID, ticket.CacheKey).Scan(ctx, &currentGeneration); err != nil {
			return fmt.Errorf("apply successful provider response: read refresh generation: %w", err)
		}
		if currentGeneration != ticket.Generation {
			return fmt.Errorf("apply successful provider response: refresh generation is stale")
		}
		if _, err := tx.NewRaw(`UPDATE provider_response_cache SET payload = ?::jsonb, fetched_at = ?, revision = NULLIF(?, '')
			WHERE provider_source_id = ? AND cache_key = ? AND generation = ?`, string(response.Payload), response.FetchedAt, response.Revision, ticket.ProviderSourceID, ticket.CacheKey, ticket.Generation).Exec(ctx); err != nil {
			return fmt.Errorf("apply successful provider response: save response: %w", err)
		}
		if err := applyProviderGraph(ctx, tx, ticket.ProviderID, response.Graph); err != nil {
			return fmt.Errorf("apply successful provider response: save graph: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
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

func applyProviderGraph(ctx context.Context, tx bun.Tx, providerID uuid.UUID, graph ProviderGraph) error {
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
	if graph.Complete {
		for _, release := range graph.Releases {
			if _, err := tx.NewRaw(`UPDATE provider_release_track SET present=false, medium=NULL, position=NULL WHERE provider_id=? AND release_id=?`, providerID, release.ID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`UPDATE provider_release_artist_credit SET present=false WHERE provider_id=? AND release_id=?`, providerID, release.ID).Exec(ctx); err != nil {
				return err
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
		if _, err := tx.NewRaw(`INSERT INTO provider_release_artist_credit(provider_id, release_id, position, artist_id, name_join_phrase, present)
			VALUES (?, ?, ?, ?, ?, true) ON CONFLICT (provider_id, release_id, position) DO UPDATE
			SET artist_id=EXCLUDED.artist_id, name_join_phrase=EXCLUDED.name_join_phrase, present=true`, providerID, credit.ReleaseID, credit.Position, credit.ArtistID, credit.NameJoinPhrase).Exec(ctx); err != nil {
			return err
		}
	}
	for _, track := range graph.Tracks {
		if track.ID == uuid.Nil || track.ReleaseID == uuid.Nil || track.RecordingID == uuid.Nil || track.Medium < 0 || track.Position < 0 {
			return fmt.Errorf("valid release track position is required")
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_release_track(id, provider_id, release_id, recording_id, medium, position, displayed_number, present)
			VALUES (?, ?, ?, ?, ?, ?, ?, true) ON CONFLICT (provider_id, id) DO UPDATE SET
		release_id=EXCLUDED.release_id, recording_id=EXCLUDED.recording_id, medium=EXCLUDED.medium, position=EXCLUDED.position,
		displayed_number=EXCLUDED.displayed_number, present=true`, track.ID, providerID, track.ReleaseID, track.RecordingID, track.Medium, track.Position, track.DisplayedNumber).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
