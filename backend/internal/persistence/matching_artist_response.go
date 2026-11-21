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

// MatchingArtistResponse preserves one accepted search, browse, or release
// lookup. The dependency key owns it; cache replacement cannot change it.
type MatchingArtistResponse struct {
	Kind       string                    `json:"kind"`
	CacheKey   string                    `json:"cache_key"`
	Payload    json.RawMessage           `json:"payload"`
	Revision   *string                   `json:"revision,omitempty"`
	Projection *ProviderMatchingSnapshot `json:"projection,omitempty"`
}

const artistResponseScope = `
	FROM matching_provider_dependency dependency
	JOIN matching_run run ON run.id=dependency.parent_run_id
	JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
	JOIN operation child ON child.id=dependency.request_id
	WHERE child.kind IN ('provider_artist_search','provider_artist_release_browse','provider_release_lookup')
		AND acquisition.intent_version=2 AND jsonb_typeof(acquisition.intent->'layout_target')='object'
		AND run.immutable_plan->>'intent'='automatic_artist_acquisition'
		AND run.immutable_plan->>'mode'='automatic'
		AND child.provider_source_id=run.provider_source_id
		AND child.provider_configuration_identity=run.provider_configuration_identity
		AND child.provider_cache_key=dependency.cache_key`

func (repository *SetupManagerRepository) GetMatchingArtistResponse(ctx context.Context, runID, requestID uuid.UUID) (*MatchingArtistResponse, error) {
	return getMatchingArtistResponse(ctx, repository.db, runID, requestID, "", nil)
}

func getMatchingArtistResponse(ctx context.Context, db bun.IDB, runID, requestID uuid.UUID, cacheKey string, fence *ProviderDeliveryFence) (*MatchingArtistResponse, error) {
	query := `SELECT dependency.accepted_artist_response::text ` + artistResponseScope + `
		AND dependency.request_id=? AND dependency.accepted_artist_response IS NOT NULL`
	args := []any{requestID}
	if runID != uuid.Nil {
		query += ` AND dependency.parent_run_id=? AND dependency.terminal_outcome='succeeded' AND child.state='succeeded'`
		args = append(args, runID)
	}
	if fence != nil {
		query += ` AND child.state='running' AND child.attempt=? AND child.river_job_id=? AND child.provider_execution_epoch=?`
		args = append(args, fence.Attempt, fence.RiverJobID, fence.ExecutionEpoch)
	}
	if cacheKey != "" {
		query += ` AND dependency.cache_key=?`
		args = append(args, cacheKey)
	}
	var encoded string
	if err := db.NewRaw(query, args...).Scan(ctx, &encoded); errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("read accepted artist matching response: %w", err)
	}
	var receipt MatchingArtistResponse
	if err := json.Unmarshal([]byte(encoded), &receipt); err != nil {
		return nil, fmt.Errorf("decode accepted artist matching response: %w", err)
	}
	return &receipt, nil
}

func (repository *ProviderCacheRepository) ReplayMatchingArtistResponse(ctx context.Context, fence ProviderDeliveryFence) (bool, error) {
	receipt, err := getMatchingArtistResponse(ctx, repository.db, uuid.Nil, fence.OperationID, "", &fence)
	return receipt != nil, err
}

func captureMatchingArtistResponse(ctx context.Context, tx bun.Tx, fence ProviderDeliveryFence, source ProviderSource, key string, payload []byte, revision *string) error {
	var rows []struct {
		RunID uuid.UUID `bun:"parent_run_id"`
		Kind  string    `bun:"kind"`
	}
	if err := tx.NewRaw(`SELECT dependency.parent_run_id,child.kind `+artistResponseScope+`
		AND dependency.request_id=? AND dependency.cache_key=? AND dependency.terminal_outcome IS NULL
		AND child.state='running' AND child.attempt=? AND child.river_job_id=? AND child.provider_execution_epoch=?
		AND run.provider_source_id=? AND run.provider_configuration_identity=?
		FOR UPDATE OF dependency`, fence.OperationID, key, fence.Attempt, fence.RiverJobID,
		fence.ExecutionEpoch, source.ID, source.ConfigurationIdentity).Scan(ctx, &rows); err != nil {
		return fmt.Errorf("lock accepted artist matching dependency: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	receipt := MatchingArtistResponse{Kind: rows[0].Kind, CacheKey: key, Payload: payload, Revision: revision}
	if receipt.Kind == "provider_release_lookup" {
		releaseKey := strings.TrimPrefix(key, "release-lookup:")
		if id, err := uuid.Parse(releaseKey); err != nil || key != "release-lookup:"+id.String() {
			return errors.New("accepted artist lookup has an invalid release key")
		}
		projection, err := readMusicBrainzMatchingSnapshot(ctx, tx, source.Endpoint, source.ConfigurationIdentity,
			[]string{releaseKey}, []string{key}, false)
		if err != nil {
			return err
		}
		accepted := projection.Responses[key]
		if len(projection.Releases) != 1 || projection.Releases[0].ProviderKey != releaseKey ||
			!projection.Releases[0].Complete || accepted == nil ||
			!matchingJSONEqual(accepted.Payload, payload) || !equalOptionalString(accepted.Revision, revision) {
			return errors.New("accepted artist lookup projection is incomplete or differs from its response")
		}
		receipt.Projection = &projection
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	for _, row := range rows {
		result, err := tx.NewRaw(`UPDATE matching_provider_dependency SET accepted_artist_response=?::jsonb
			WHERE parent_run_id=? AND request_id=?
				AND (accepted_artist_response IS NULL OR accepted_artist_response=?::jsonb)`,
			string(raw), row.RunID, fence.OperationID, string(raw)).Exec(ctx)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return errors.New("accepted artist matching response conflicts with saved response")
		}
	}
	return nil
}

// CaptureMatchingArtistResponseReuse freezes cache reuse under the same source
// lock order as accepting a new response.
func (repository *ProviderCacheRepository) CaptureMatchingArtistResponseReuse(ctx context.Context, fence ProviderDeliveryFence, expected *ProviderCachedResponse) (bool, error) {
	var needed bool
	if err := repository.db.NewRaw(`SELECT EXISTS(SELECT 1 `+artistResponseScope+`
		AND dependency.request_id=?)`, fence.OperationID).Scan(ctx, &needed); err != nil || !needed {
		return false, err
	}
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
			return err
		}
		var operationID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM operation WHERE id=? AND state='running' AND attempt=? AND river_job_id=?
			AND provider_execution_epoch=? FOR UPDATE`, fence.OperationID, fence.Attempt, fence.RiverJobID, fence.ExecutionEpoch).
			Scan(ctx, &operationID); err != nil {
			return fmt.Errorf("lock artist matching response delivery: %w", err)
		}
		var source ProviderSource
		if err := tx.NewRaw(`SELECT source.id,source.provider_id,source.endpoint,source.configuration_identity FROM provider_source source
			JOIN app_setting config ON config.setting_name='musicbrainz_config_identity' AND config.setting_value=source.configuration_identity
			WHERE source.id=? FOR SHARE OF source`, expected.ProviderSourceID).Scan(ctx, &source); err != nil {
			return fmt.Errorf("lock artist matching response source: %w", err)
		}
		var cached ProviderCachedResponse
		if err := tx.NewRaw(`SELECT payload,revision FROM provider_response_cache WHERE provider_source_id=? AND cache_key=? FOR SHARE`,
			source.ID, expected.CacheKey).Scan(ctx, &cached); err != nil {
			return err
		}
		if !matchingJSONEqual(expected.Payload, cached.Payload) || !equalOptionalString(expected.Revision, cached.Revision) {
			return errors.New("artist matching response changed before cache reuse")
		}
		if err := captureMatchingArtistResponse(ctx, tx, fence, source, expected.CacheKey, cached.Payload, cached.Revision); err != nil {
			return err
		}
		receipt, err := getMatchingArtistResponse(ctx, tx, uuid.Nil, fence.OperationID, expected.CacheKey, &fence)
		if err == nil && receipt == nil {
			return errors.New("artist matching response lost its delivery")
		}
		return err
	})
	return err == nil, err
}
