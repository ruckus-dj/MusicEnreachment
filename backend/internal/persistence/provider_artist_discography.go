package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ArtistDiscographyCompletion is the accepted evidence that every page of one
// MusicBrainz artist release browse was persisted. Callers build it only from
// validated pages; it is never derived from a partial cache.
type ArtistDiscographyCompletion struct {
	ProviderSourceID      uuid.UUID
	ProviderID            uuid.UUID
	ConfigurationIdentity string
	ArtistMBID            string
	ReleaseCount          int
	PageCacheKeys         []string
	ReleaseKeys           []string
}

// IsArtistDiscographyComplete reads the completion marker fenced by the current
// saved MusicBrainz configuration. It performs no refresh.
func (repository *ProviderCacheRepository) IsArtistDiscographyComplete(ctx context.Context, sourceID uuid.UUID, configurationIdentity, artistMBID string) (bool, error) {
	artist, err := canonicalArtistMBID(artistMBID)
	if err != nil {
		return false, fmt.Errorf("read artist discography completion: %w", err)
	}
	if sourceID == uuid.Nil || configurationIdentity == "" {
		return false, fmt.Errorf("read artist discography completion: source and configuration identity are required")
	}
	var complete bool
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("read artist discography completion: lock configuration: %w", err)
		}
		var identity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &identity); err != nil {
			return fmt.Errorf("read artist discography completion: read current configuration: %w", err)
		}
		if identity != configurationIdentity {
			return nil
		}
		return tx.NewRaw(`SELECT EXISTS(
			SELECT 1 FROM provider_artist_discography completion
			JOIN provider_source source ON source.id=completion.provider_source_id AND source.provider_id=completion.provider_id
			WHERE completion.provider_source_id=? AND completion.configuration_identity=?
				AND completion.artist_mbid=? AND source.configuration_identity=completion.configuration_identity)`,
			sourceID, configurationIdentity, artist).Scan(ctx, &complete)
	})
	if err != nil {
		return false, fmt.Errorf("read artist discography completion: %w", err)
	}
	return complete, nil
}

// CompleteArtistDiscography records completion only while the saved
// configuration, bound source, every accepted page response, and every
// normalized release entity are present. It runs no network I/O and holds no
// transaction open for the caller's browse requests.
func (repository *ProviderCacheRepository) CompleteArtistDiscography(ctx context.Context, completion ArtistDiscographyCompletion) error {
	artist, err := canonicalArtistMBID(completion.ArtistMBID)
	if err != nil {
		return fmt.Errorf("complete artist discography: %w", err)
	}
	if completion.ProviderSourceID == uuid.Nil || completion.ProviderID == uuid.Nil || completion.ConfigurationIdentity == "" {
		return fmt.Errorf("complete artist discography: source, provider, and configuration identity are required")
	}
	if completion.ReleaseCount < 0 || completion.ReleaseCount != len(completion.ReleaseKeys) || len(completion.PageCacheKeys) == 0 {
		return fmt.Errorf("complete artist discography: release count and accepted pages are inconsistent")
	}
	pagePrefix := "artist-release-browse:" + artist.String() + ":"
	seenPages := make(map[string]struct{}, len(completion.PageCacheKeys))
	for _, key := range completion.PageCacheKeys {
		if !strings.HasPrefix(key, pagePrefix) || len(key) == len(pagePrefix) {
			return fmt.Errorf("complete artist discography: accepted page key is invalid")
		}
		if _, duplicate := seenPages[key]; duplicate {
			return fmt.Errorf("complete artist discography: accepted page keys contain a duplicate")
		}
		seenPages[key] = struct{}{}
	}
	releaseKeys := make([]string, 0, len(completion.ReleaseKeys))
	seenReleases := make(map[string]struct{}, len(completion.ReleaseKeys))
	for _, key := range completion.ReleaseKeys {
		parsed, err := uuid.Parse(key)
		if err != nil || parsed.String() != key {
			return fmt.Errorf("complete artist discography: accepted release key is invalid")
		}
		if _, duplicate := seenReleases[key]; duplicate {
			return fmt.Errorf("complete artist discography: accepted release keys contain a duplicate")
		}
		seenReleases[key] = struct{}{}
		releaseKeys = append(releaseKeys, key)
	}
	if len(releaseKeys) != completion.ReleaseCount {
		return fmt.Errorf("complete artist discography: accepted releases do not match the reported total")
	}
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("complete artist discography: lock configuration: %w", err)
		}
		var identity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &identity); err != nil {
			return fmt.Errorf("complete artist discography: read current configuration: %w", err)
		}
		if identity != completion.ConfigurationIdentity {
			return fmt.Errorf("complete artist discography: configuration changed")
		}
		var source ProviderSource
		if err := tx.NewRaw(`SELECT id, provider_id, configuration_identity FROM provider_source WHERE id=? FOR SHARE`, completion.ProviderSourceID).Scan(ctx, &source); err != nil {
			return fmt.Errorf("complete artist discography: read source: %w", err)
		}
		if source.ProviderID != completion.ProviderID || source.ConfigurationIdentity != completion.ConfigurationIdentity {
			return fmt.Errorf("complete artist discography: source configuration changed")
		}
		var pages int
		if err := tx.NewRaw(`SELECT count(*) FROM provider_response_cache
			WHERE provider_source_id=? AND cache_key IN (?) AND payload IS NOT NULL AND jsonb_typeof(payload)='object'`,
			completion.ProviderSourceID, bun.List(completion.PageCacheKeys)).Scan(ctx, &pages); err != nil {
			return fmt.Errorf("complete artist discography: read accepted pages: %w", err)
		}
		if pages != len(completion.PageCacheKeys) {
			return fmt.Errorf("complete artist discography: accepted pages are incomplete")
		}
		var releases int
		if len(releaseKeys) > 0 {
			if err := tx.NewRaw(`SELECT count(*) FROM provider_source_entity
				WHERE provider_id=? AND provider_source_id=? AND entity_kind='release' AND provider_key IN (?)`,
				completion.ProviderID, completion.ProviderSourceID, bun.List(releaseKeys)).Scan(ctx, &releases); err != nil {
				return fmt.Errorf("complete artist discography: read accepted releases: %w", err)
			}
		}
		if releases != len(releaseKeys) {
			return fmt.Errorf("complete artist discography: accepted release projection is incomplete")
		}
		_, err := tx.NewRaw(`INSERT INTO provider_artist_discography
			(provider_source_id, provider_id, configuration_identity, artist_mbid, release_count, completed_at)
			VALUES (?, ?, ?, ?, ?, now())
			ON CONFLICT (provider_source_id, configuration_identity, artist_mbid) DO UPDATE
			SET provider_id=EXCLUDED.provider_id, release_count=EXCLUDED.release_count, completed_at=EXCLUDED.completed_at`,
			completion.ProviderSourceID, completion.ProviderID, completion.ConfigurationIdentity, artist, completion.ReleaseCount).Exec(ctx)
		if err != nil {
			return fmt.Errorf("complete artist discography: save completion: %w", err)
		}
		return nil
	})
	return err
}

func canonicalArtistMBID(value string) (uuid.UUID, error) {
	trimmed := strings.TrimSpace(value)
	artist, err := uuid.Parse(trimmed)
	if err != nil || !strings.EqualFold(trimmed, artist.String()) {
		return uuid.Nil, errors.New("artist MusicBrainz id is invalid")
	}
	return artist, nil
}
