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

var ErrLibraryRecordingSearchCacheUnavailable = errors.New("current MusicBrainz recording search cache is unavailable")
var ErrLibraryRecordingSearchConfigurationChanged = errors.New("MusicBrainz recording search configuration changed")

type LibraryRecordingSearchArtist struct {
	Name             string  `json:"name"`
	JoinPhrase       string  `json:"join_phrase"`
	ProviderArtistID *string `json:"provider_artist_id"`
}

type LibraryRecordingSearchItem struct {
	ProviderRecordingID uuid.UUID                      `json:"provider_recording_id"`
	ProviderKey         string                         `json:"provider_key"`
	Title               string                         `json:"title"`
	Artists             []LibraryRecordingSearchArtist `json:"artists"`
	DurationMS          *int64                         `json:"duration_ms"`
	LocalRecordingID    *uuid.UUID                     `json:"local_recording_id"`
}

type LibraryRecordingSearchCacheResult struct {
	ProviderID uuid.UUID                    `json:"provider_id"`
	Total      int                          `json:"total"`
	Offset     int                          `json:"offset"`
	Items      []LibraryRecordingSearchItem `json:"items"`
}

// ReadLibraryRecordingSearchCache reads the captured hits and their local
// identities in one configuration-fenced transaction. A search hit does not
// require, replace, or read the shared recording metadata.
func (repository *LibraryRepository) ReadLibraryRecordingSearchCache(ctx context.Context, sourceID uuid.UUID, configurationIdentity, cacheKey string, expectedOffset, expectedLimit int) (*LibraryRecordingSearchCacheResult, error) {
	if sourceID == uuid.Nil || configurationIdentity == "" || cacheKey == "" || expectedOffset < 0 || expectedLimit < 1 || expectedLimit > 100 {
		return nil, fmt.Errorf("read library recording search cache: captured provider authority is incomplete")
	}
	var result *LibraryRecordingSearchCacheResult
	// PostgreSQL does not permit row-level FOR SHARE locks in a read-only
	// transaction. This remains a read-only operation at the application level;
	// the writable transaction mode is required only for its consistency locks.
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		// Acquire the shared configuration gate before any statement takes a
		// snapshot. At READ COMMITTED each later statement sees the committed
		// configuration after a wait on an exclusive config update.
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("read library recording search cache: lock configuration: %w", err)
		}
		var currentIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentIdentity); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrLibraryRecordingSearchConfigurationChanged
			}
			return fmt.Errorf("read library recording search cache: read current configuration: %w", err)
		}
		if currentIdentity != configurationIdentity {
			return ErrLibraryRecordingSearchConfigurationChanged
		}
		var sourceIdentity string
		var sourceProviderID uuid.UUID
		// Refresh writers lock this source row before changing its accepted
		// cache and registry identities. Keep the receipt and its identities
		// consistent while reading them.
		if err := tx.NewRaw(`SELECT provider_id,configuration_identity FROM provider_source WHERE id=? FOR SHARE`, sourceID).Scan(ctx, &sourceProviderID, &sourceIdentity); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrLibraryRecordingSearchConfigurationChanged
			}
			return fmt.Errorf("read library recording search cache: read source: %w", err)
		}
		if sourceProviderID == uuid.Nil || sourceIdentity != configurationIdentity {
			return ErrLibraryRecordingSearchConfigurationChanged
		}
		var payload []byte
		var cacheProviderID uuid.UUID
		// Preserve the exact cache membership/payload while projection rows are
		// materialized below; refresh writers update this row in their transaction.
		if err := tx.NewRaw(`SELECT provider_id,payload FROM provider_response_cache WHERE provider_source_id=? AND cache_key=? FOR SHARE`, sourceID, cacheKey).Scan(ctx, &cacheProviderID, &payload); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrLibraryRecordingSearchCacheUnavailable
			}
			return fmt.Errorf("read library recording search cache: read response: %w", err)
		}
		if cacheProviderID != sourceProviderID {
			return ErrLibraryRecordingSearchCacheUnavailable
		}
		var cached struct {
			Count      int                         `json:"count"`
			Offset     int                         `json:"offset"`
			Recordings []libraryRecordingSearchHit `json:"recordings"`
		}
		if err := json.Unmarshal(payload, &cached); err != nil || cached.Count < 0 || cached.Offset != expectedOffset || len(cached.Recordings) > expectedLimit || cached.Recordings == nil {
			return ErrLibraryRecordingSearchCacheUnavailable
		}
		read := &LibraryRecordingSearchCacheResult{ProviderID: sourceProviderID, Total: cached.Count, Offset: cached.Offset, Items: make([]LibraryRecordingSearchItem, 0, len(cached.Recordings))}
		for _, hit := range cached.Recordings {
			mbid, err := uuid.Parse(hit.ID)
			if err != nil || !strings.EqualFold(hit.ID, mbid.String()) {
				return ErrLibraryRecordingSearchCacheUnavailable
			}
			item, err := readLibraryRecordingSearchItem(ctx, tx, sourceProviderID, mbid.String(), hit)
			if err != nil {
				return err
			}
			read.Items = append(read.Items, item)
		}
		result = read
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type libraryRecordingSearchHit struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Length       json.RawMessage `json:"length"`
	ArtistCredit []struct {
		Name       string `json:"name"`
		JoinPhrase string `json:"joinphrase"`
		Artist     struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"artist"`
	} `json:"artist-credit"`
}

func readLibraryRecordingSearchItem(ctx context.Context, tx bun.Tx, providerID uuid.UUID, providerKey string, hit libraryRecordingSearchHit) (LibraryRecordingSearchItem, error) {
	var recordingID uuid.UUID
	err := tx.NewRaw(`SELECT id FROM provider_recording WHERE provider_id=? AND provider_key=?`, providerID, providerKey).Scan(ctx, &recordingID)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryRecordingSearchItem{}, ErrLibraryRecordingSearchCacheUnavailable
	}
	if err != nil {
		return LibraryRecordingSearchItem{}, fmt.Errorf("read library recording search cache: read recording identity: %w", err)
	}
	if recordingID != uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:recording:"+providerKey)) || hit.Title == "" {
		return LibraryRecordingSearchItem{}, ErrLibraryRecordingSearchCacheUnavailable
	}
	item := LibraryRecordingSearchItem{ProviderRecordingID: recordingID, ProviderKey: providerKey, Title: hit.Title, Artists: make([]LibraryRecordingSearchArtist, 0, len(hit.ArtistCredit))}
	var duration *int64
	if json.Unmarshal(hit.Length, &duration) == nil && duration != nil && *duration >= 0 {
		item.DurationMS = duration
	}
	for _, credit := range hit.ArtistCredit {
		artistID, err := uuid.Parse(credit.Artist.ID)
		if err != nil || !strings.EqualFold(credit.Artist.ID, artistID.String()) {
			continue
		}
		name := credit.Name
		if name == "" {
			name = credit.Artist.Name
		}
		id := artistID.String()
		item.Artists = append(item.Artists, LibraryRecordingSearchArtist{Name: name, JoinPhrase: credit.JoinPhrase, ProviderArtistID: &id})
	}
	var localID uuid.UUID
	err = tx.NewRaw(`SELECT local_recording_id FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=?`, providerID, recordingID).Scan(ctx, &localID)
	if err == nil {
		item.LocalRecordingID = &localID
	} else if !errors.Is(err, sql.ErrNoRows) {
		return LibraryRecordingSearchItem{}, fmt.Errorf("read library recording search cache: read local recording mapping: %w", err)
	}
	return item, nil
}
