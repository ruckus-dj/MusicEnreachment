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

// ReadLibraryRecordingSearchCache reads the captured cache entry and its
// source-scoped accepted recording projections in one configuration-fenced
// transaction. It never starts provider I/O or creates source/config rows.
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
		// projection. Keep it locked while reading all projection evidence so a
		// result cannot combine different accepted graph versions.
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
			Count      int `json:"count"`
			Offset     int `json:"offset"`
			Recordings []struct {
				ID string `json:"id"`
			} `json:"recordings"`
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
			item, err := readLibraryRecordingSearchItem(ctx, tx, sourceProviderID, sourceID, mbid, hit.ID)
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

func readLibraryRecordingSearchItem(ctx context.Context, tx bun.Tx, providerID, sourceID, providerRecordingID uuid.UUID, providerKey string) (LibraryRecordingSearchItem, error) {
	var fields, evidence, title string
	var recordingID uuid.UUID
	err := tx.NewRaw(`SELECT r.id,e.fields::text,e.field_evidence::text,e.name
		FROM provider_recording r JOIN provider_source_entity e ON e.provider_id=r.provider_id AND e.provider_key=r.provider_key AND e.provider_source_id=? AND e.entity_kind='recording'
		WHERE r.provider_id=? AND r.provider_key=?`, sourceID, providerID, strings.ToLower(providerKey)).Scan(ctx, &recordingID, &fields, &evidence, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryRecordingSearchItem{}, ErrLibraryRecordingSearchCacheUnavailable
	}
	if err != nil {
		return LibraryRecordingSearchItem{}, fmt.Errorf("read library recording search cache: read accepted recording: %w", err)
	}
	if recordingID != uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:recording:"+strings.ToLower(providerKey))) {
		return LibraryRecordingSearchItem{}, ErrLibraryRecordingSearchCacheUnavailable
	}
	var fieldEvidence map[string]ProviderFieldEvidence
	var projection map[string]json.RawMessage
	if json.Unmarshal([]byte(evidence), &fieldEvidence) != nil || json.Unmarshal([]byte(fields), &projection) != nil || projection == nil {
		return LibraryRecordingSearchItem{}, ErrLibraryRecordingSearchCacheUnavailable
	}
	if _, ok := fieldEvidence["title"]; !ok {
		return LibraryRecordingSearchItem{}, ErrLibraryRecordingSearchCacheUnavailable
	}
	if title == "" {
		if _, ok := fieldEvidence["title"]; !ok || json.Unmarshal(projection["title"], &title) != nil {
			return LibraryRecordingSearchItem{}, ErrLibraryRecordingSearchCacheUnavailable
		}
	}
	if title == "" {
		return LibraryRecordingSearchItem{}, ErrLibraryRecordingSearchCacheUnavailable
	}
	item := LibraryRecordingSearchItem{ProviderRecordingID: recordingID, ProviderKey: strings.ToLower(providerKey), Title: title, Artists: make([]LibraryRecordingSearchArtist, 0)}
	if _, ok := fieldEvidence["length"]; ok && len(projection["length"]) > 0 && string(projection["length"]) != "null" {
		var duration int64
		if json.Unmarshal(projection["length"], &duration) == nil && duration >= 0 {
			item.DurationMS = &duration
		}
	}
	if _, ok := fieldEvidence["artist-credit"]; ok {
		credits, err := acceptedRecordingArtistCredits(ctx, tx, providerID, sourceID, recordingID)
		if err != nil {
			return LibraryRecordingSearchItem{}, err
		}
		for _, credit := range credits {
			id := strings.ToLower(credit.ProviderKey)
			item.Artists = append(item.Artists, LibraryRecordingSearchArtist{
				Name: credit.Name, JoinPhrase: credit.JoinPhrase, ProviderArtistID: &id,
			})
		}
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
