package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

var (
	ErrLibraryRecordingSearchInvalid              = errors.New("library recording search is invalid")
	ErrLibraryRecordingSearchUnavailable          = errors.New("library recording search is unavailable")
	ErrLibraryRecordingSearchNotFound             = errors.New("library recording search was not found")
	ErrLibraryRecordingSearchConflict             = errors.New("library recording search result is not current")
	ErrLibraryRecordingSearchConfigurationChanged = errors.New("MusicBrainz recording search configuration changed")
)

type libraryRecordingSearchStarter interface {
	StartProviderOperation(context.Context, string, string, int, int, bool) (*persistence.Operation, error)
}

type libraryRecordingSearchOperationReader interface {
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
}

type libraryRecordingSearchRepository interface {
	ReadLibraryRecordingSearchCache(context.Context, uuid.UUID, string, string, int, int) (*persistence.LibraryRecordingSearchCacheResult, error)
}

type LibraryRecordingSearch struct {
	provider   libraryRecordingSearchStarter
	operations libraryRecordingSearchOperationReader
	repository libraryRecordingSearchRepository
}

type LibraryRecordingSearchArtist struct {
	Name             string  `json:"name"`
	JoinPhrase       string  `json:"join_phrase"`
	ProviderArtistID *string `json:"provider_artist_id" nullable:"true"`
}

type LibraryRecordingSearchItem struct {
	ProviderID          uuid.UUID                      `json:"provider_id"`
	ProviderSourceID    uuid.UUID                      `json:"provider_source_id"`
	ProviderRecordingID uuid.UUID                      `json:"provider_recording_id"`
	ProviderKey         string                         `json:"provider_key"`
	Title               string                         `json:"title"`
	Artists             []LibraryRecordingSearchArtist `json:"artists" nullable:"false"`
	DurationMS          *int64                         `json:"duration_ms" nullable:"true"`
	LocalRecordingID    *uuid.UUID                     `json:"local_recording_id" nullable:"true"`
}

type LibraryRecordingSearchResult struct {
	OperationID           uuid.UUID                    `json:"operation_id"`
	ProviderSourceID      uuid.UUID                    `json:"provider_source_id"`
	ConfigurationIdentity string                       `json:"configuration_identity"`
	Total                 int                          `json:"total"`
	Offset                int                          `json:"offset"`
	Limit                 int                          `json:"limit"`
	Items                 []LibraryRecordingSearchItem `json:"items" nullable:"false"`
}

func NewLibraryRecordingSearch(provider libraryRecordingSearchStarter, operations libraryRecordingSearchOperationReader, repository libraryRecordingSearchRepository) *LibraryRecordingSearch {
	return &LibraryRecordingSearch{provider: provider, operations: operations, repository: repository}
}

func (search *LibraryRecordingSearch) Start(ctx context.Context, query string, offset, limit int) (*persistence.Operation, error) {
	if strings.TrimSpace(query) == "" || offset < 0 || limit < 1 || limit > 100 {
		return nil, ErrLibraryRecordingSearchInvalid
	}
	if search == nil || search.provider == nil {
		return nil, ErrLibraryRecordingSearchUnavailable
	}
	operation, err := search.provider.StartProviderOperation(ctx, "recording_search", query, offset, limit, false)
	if err != nil {
		return nil, err
	}
	if operation == nil || operation.ID == uuid.Nil {
		return nil, ErrLibraryRecordingSearchUnavailable
	}
	return operation, nil
}

func (search *LibraryRecordingSearch) Result(ctx context.Context, operationID uuid.UUID) (*LibraryRecordingSearchResult, error) {
	if operationID == uuid.Nil {
		return nil, ErrLibraryRecordingSearchNotFound
	}
	if search == nil || search.operations == nil || search.repository == nil {
		return nil, ErrLibraryRecordingSearchUnavailable
	}
	operation, err := search.operations.GetOperation(ctx, operationID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryRecordingSearchNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read library recording search operation: %w", err)
	}
	if operation == nil || operation.Kind != "provider_recording_search" {
		return nil, ErrLibraryRecordingSearchNotFound
	}
	if operation.State != "succeeded" {
		return nil, ErrLibraryRecordingSearchConflict
	}
	if operation.ProviderSourceID == nil || operation.ProviderConfigurationIdentity == nil || operation.ProviderCacheKey == nil {
		return nil, ErrLibraryRecordingSearchConflict
	}
	var intent struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}
	if json.Unmarshal(operation.InputSnapshot, &intent) != nil || intent.SchemaVersion != 1 || strings.TrimSpace(intent.Query) == "" || intent.Offset < 0 || intent.Limit < 1 || intent.Limit > 100 {
		return nil, ErrLibraryRecordingSearchConflict
	}
	expectedKey := fmt.Sprintf("recording-search:%d:%d:%s", intent.Offset, intent.Limit, intent.Query)
	if *operation.ProviderCacheKey != expectedKey {
		return nil, ErrLibraryRecordingSearchConflict
	}
	cacheResult, err := search.repository.ReadLibraryRecordingSearchCache(ctx, *operation.ProviderSourceID, *operation.ProviderConfigurationIdentity, expectedKey, intent.Offset, intent.Limit)
	if errors.Is(err, persistence.ErrLibraryRecordingSearchConfigurationChanged) {
		return nil, ErrLibraryRecordingSearchConfigurationChanged
	}
	if errors.Is(err, persistence.ErrLibraryRecordingSearchCacheUnavailable) {
		return nil, ErrLibraryRecordingSearchConflict
	}
	if err != nil {
		return nil, fmt.Errorf("read library recording search cache: %w", err)
	}
	result := &LibraryRecordingSearchResult{
		OperationID: operationID, ProviderSourceID: *operation.ProviderSourceID,
		ConfigurationIdentity: *operation.ProviderConfigurationIdentity,
		Total:                 cacheResult.Total, Offset: cacheResult.Offset, Limit: intent.Limit,
		Items: make([]LibraryRecordingSearchItem, 0, len(cacheResult.Items)),
	}
	for _, item := range cacheResult.Items {
		artists := make([]LibraryRecordingSearchArtist, 0, len(item.Artists))
		for _, artist := range item.Artists {
			artists = append(artists, LibraryRecordingSearchArtist{Name: artist.Name, JoinPhrase: artist.JoinPhrase, ProviderArtistID: artist.ProviderArtistID})
		}
		result.Items = append(result.Items, LibraryRecordingSearchItem{
			ProviderID: cacheResult.ProviderID, ProviderSourceID: *operation.ProviderSourceID,
			ProviderRecordingID: item.ProviderRecordingID, ProviderKey: item.ProviderKey,
			Title: item.Title, Artists: artists, DurationMS: item.DurationMS, LocalRecordingID: item.LocalRecordingID,
		})
	}
	return result, nil
}
