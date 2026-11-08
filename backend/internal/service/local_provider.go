package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

var ErrLocalProviderInvalid = errors.New("local provider catalog request is invalid")

type LocalProviderStore interface {
	List(context.Context, string, string, string, int, int) (persistence.LocalProviderList, error)
	Read(context.Context, string, uuid.UUID) (persistence.LocalProviderItem, error)
	Save(context.Context, persistence.LocalProviderItem, bool) (persistence.LocalProviderItem, error)
}

// LocalProviderGenreReferences converts catalog entries into the canonical
// genre references stored and returned by the local provider catalog.
func LocalProviderGenreReferences(items []GenreCatalogItem) []librarymetadata.GenreReference {
	references := make([]librarymetadata.GenreReference, 0, len(items))
	for _, item := range items {
		references = append(references, librarymetadata.GenreReference{ID: item.ID, SourceName: item.SourceName, Name: item.Name})
	}
	return references
}

type LocalProviderItem struct {
	ID                uuid.UUID                        `json:"id"`
	ProviderID        uuid.UUID                        `json:"provider_id"`
	ProviderCode      string                           `json:"provider_code"`
	ProviderKey       string                           `json:"provider_key"`
	Kind              string                           `json:"kind"`
	Name              string                           `json:"name,omitempty"`
	SortName          *string                          `json:"sort_name,omitempty"`
	Title             string                           `json:"title,omitempty"`
	ReleaseGroupTitle string                           `json:"release_group_title,omitempty"`
	DurationMS        *int64                           `json:"duration_ms,omitempty"`
	PrimaryType       *string                          `json:"release_group_primary_type,omitempty"`
	SecondaryTypes    []string                         `json:"release_group_secondary_types,omitempty"`
	Language          *string                          `json:"language,omitempty"`
	Script            *string                          `json:"script,omitempty"`
	Genres            []librarymetadata.GenreReference `json:"genres"`
	Tags              []string                         `json:"tags,omitempty"`
	Credits           []LocalProviderCredit            `json:"credits"`
	Tracks            []LocalProviderTrack             `json:"tracks"`
}

type LocalProviderCredit struct {
	ArtistID     uuid.UUID `json:"artist_id"`
	ProviderID   uuid.UUID `json:"provider_id"`
	ProviderCode string    `json:"provider_code"`
	ArtistName   string    `json:"artist_name"`
	Name         string    `json:"credit_name"`
	JoinPhrase   string    `json:"join_phrase"`
}

type LocalProviderTrack struct {
	ID                  uuid.UUID `json:"id"`
	RecordingID         uuid.UUID `json:"recording_id"`
	ProviderID          uuid.UUID `json:"provider_id"`
	ProviderCode        string    `json:"provider_code"`
	RecordingTitle      string    `json:"recording_title"`
	RecordingDurationMS *int64    `json:"recording_duration_ms"`
	Position            int       `json:"position"`
	DisplayedNumber     string    `json:"displayed_number"`
	TitleOverride       *string   `json:"title_override"`
	DurationOverrideMS  *int64    `json:"duration_override_ms"`
}

type LocalProviderPage struct {
	Items  []LocalProviderItem `json:"items"`
	Total  int                 `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

type LocalProviderGenreReader interface {
	GenreCatalogItems(context.Context) ([]librarymetadata.GenreReference, error)
}

type LocalProviderCatalog struct {
	store  LocalProviderStore
	genres LocalProviderGenreReader
}

func NewLocalProviderCatalog(store LocalProviderStore, genres LocalProviderGenreReader) *LocalProviderCatalog {
	return &LocalProviderCatalog{store: store, genres: genres}
}

func (catalog *LocalProviderCatalog) List(ctx context.Context, kind, query, provider string, limit, offset int) (LocalProviderPage, error) {
	if catalog == nil || catalog.store == nil || limit < 1 || limit > 100 || offset < 0 {
		return LocalProviderPage{}, ErrLocalProviderInvalid
	}
	page, err := catalog.store.List(ctx, kind, query, provider, limit, offset)
	if err != nil {
		return LocalProviderPage{}, err
	}
	result := LocalProviderPage{Total: page.Total, Limit: page.Limit, Offset: page.Offset, Items: make([]LocalProviderItem, 0, len(page.Items))}
	for _, item := range page.Items {
		converted, err := localProviderItem(item)
		if err != nil {
			return LocalProviderPage{}, err
		}
		result.Items = append(result.Items, converted)
	}
	return result, nil
}

func (catalog *LocalProviderCatalog) Read(ctx context.Context, kind string, id uuid.UUID) (LocalProviderItem, error) {
	if catalog == nil || catalog.store == nil || id == uuid.Nil || !validLocalProviderKind(kind) {
		return LocalProviderItem{}, ErrLocalProviderInvalid
	}
	item, err := catalog.store.Read(ctx, kind, id)
	if err != nil {
		return LocalProviderItem{}, err
	}
	return localProviderItem(item)
}

func (catalog *LocalProviderCatalog) Create(ctx context.Context, kind string, item LocalProviderItem) (LocalProviderItem, error) {
	item.Kind = kind
	item, err := catalog.validate(ctx, item)
	if err != nil {
		return LocalProviderItem{}, err
	}
	result, err := catalog.store.Save(ctx, persistenceLocalProviderItem(item), true)
	if err != nil {
		return LocalProviderItem{}, err
	}
	return localProviderItem(result)
}

func (catalog *LocalProviderCatalog) Update(ctx context.Context, kind string, id uuid.UUID, item LocalProviderItem) (LocalProviderItem, error) {
	if catalog == nil || catalog.store == nil {
		return LocalProviderItem{}, ErrLocalProviderInvalid
	}
	item.Kind, item.ID = kind, id
	current, err := catalog.store.Read(ctx, kind, id)
	if err != nil {
		return LocalProviderItem{}, err
	}
	item, err = catalog.validate(ctx, item, current.Genres)
	if err != nil {
		return LocalProviderItem{}, err
	}
	result, err := catalog.store.Save(ctx, persistenceLocalProviderItem(item), false)
	if err != nil {
		return LocalProviderItem{}, err
	}
	return localProviderItem(result)
}

func (catalog *LocalProviderCatalog) validate(ctx context.Context, item LocalProviderItem, previousJSON ...json.RawMessage) (LocalProviderItem, error) {
	if catalog == nil || catalog.store == nil || !validLocalProviderKind(item.Kind) {
		return item, ErrLocalProviderInvalid
	}
	if (item.Kind == "artist" && strings.TrimSpace(item.Name) == "") || (item.Kind != "artist" && strings.TrimSpace(item.Title) == "") {
		return item, ErrLocalProviderInvalid
	}
	if item.Kind == "release" && strings.TrimSpace(item.ReleaseGroupTitle) == "" {
		return item, ErrLocalProviderInvalid
	}
	switch item.Kind {
	case "artist":
		if item.DurationMS != nil || item.ReleaseGroupTitle != "" || item.Title != "" || item.PrimaryType != nil || len(item.SecondaryTypes) > 0 || item.Language != nil || item.Script != nil || len(item.Credits) > 0 || len(item.Tracks) > 0 {
			return item, ErrLocalProviderInvalid
		}
	case "recording":
		if item.SortName != nil || item.ReleaseGroupTitle != "" || item.PrimaryType != nil || len(item.SecondaryTypes) > 0 || item.Language != nil || item.Script != nil || len(item.Tracks) > 0 {
			return item, ErrLocalProviderInvalid
		}
	case "release":
		if item.SortName != nil || item.DurationMS != nil || len(item.Tags) > 0 {
			return item, ErrLocalProviderInvalid
		}
	}
	if item.DurationMS != nil && *item.DurationMS < 0 {
		return item, ErrLocalProviderInvalid
	}
	seenTrackIDs := make(map[uuid.UUID]struct{}, len(item.Tracks))
	for _, track := range item.Tracks {
		if track.RecordingID == uuid.Nil || track.DurationOverrideMS != nil && *track.DurationOverrideMS < 0 {
			return item, ErrLocalProviderInvalid
		}
		if track.ID != uuid.Nil {
			if _, exists := seenTrackIDs[track.ID]; exists {
				return item, ErrLocalProviderInvalid
			}
			seenTrackIDs[track.ID] = struct{}{}
		}
	}
	for _, credit := range item.Credits {
		if credit.ArtistID == uuid.Nil {
			return item, ErrLocalProviderInvalid
		}
	}
	if item.Genres == nil {
		item.Genres = []librarymetadata.GenreReference{}
	}
	if item.Tags == nil {
		item.Tags = []string{}
	}
	if item.SecondaryTypes == nil {
		item.SecondaryTypes = []string{}
	}
	if item.Credits == nil {
		item.Credits = []LocalProviderCredit{}
	}
	if item.Tracks == nil {
		item.Tracks = []LocalProviderTrack{}
	}
	requested := item.Genres
	if len(requested) > 0 {
		byID := make(map[string]librarymetadata.GenreReference)
		if len(previousJSON) > 0 && len(previousJSON[0]) > 0 {
			var previous []librarymetadata.GenreReference
			if err := json.Unmarshal(previousJSON[0], &previous); err != nil {
				return item, fmt.Errorf("decode previous local provider genres: %w", err)
			}
			for _, genre := range previous {
				byID[strings.ToLower(genre.ID)] = genre
			}
		}
		needCatalog := false
		for _, genre := range requested {
			if _, ok := byID[strings.ToLower(genre.ID)]; !ok {
				needCatalog = true
				break
			}
		}
		if needCatalog {
			if catalog.genres == nil {
				return item, ErrLocalProviderInvalid
			}
			available, err := catalog.genres.GenreCatalogItems(ctx)
			if err != nil {
				return item, fmt.Errorf("read MusicBrainz genre catalog: %w", err)
			}
			for _, genre := range available {
				byID[strings.ToLower(genre.ID)] = genre
			}
		}
		for i := range requested {
			canonical, ok := byID[strings.ToLower(requested[i].ID)]
			if !ok {
				return item, ErrLocalProviderInvalid
			}
			requested[i] = canonical
		}
		item.Genres = requested
	}
	return item, nil
}

func validLocalProviderKind(kind string) bool {
	return kind == "artist" || kind == "recording" || kind == "release"
}

func localProviderItem(item persistence.LocalProviderItem) (LocalProviderItem, error) {
	result := LocalProviderItem{ID: item.ID, ProviderID: item.ProviderID, ProviderCode: item.ProviderCode, ProviderKey: item.ProviderKey, Kind: item.Kind, Name: item.Name, SortName: item.SortName, Title: item.Title, ReleaseGroupTitle: item.ReleaseGroupTitle, DurationMS: item.DurationMS, PrimaryType: item.PrimaryType, SecondaryTypes: item.SecondaryTypes, Language: item.Language, Script: item.Script, Tags: item.Tags, Credits: make([]LocalProviderCredit, 0, len(item.Credits)), Tracks: make([]LocalProviderTrack, 0, len(item.Tracks))}
	if err := json.Unmarshal(item.Genres, &result.Genres); err != nil {
		return LocalProviderItem{}, fmt.Errorf("decode local provider genres: %w", err)
	}
	for _, credit := range item.Credits {
		result.Credits = append(result.Credits, LocalProviderCredit{ArtistID: credit.ArtistID, ProviderID: credit.ProviderID, ProviderCode: credit.ProviderCode, ArtistName: credit.ArtistName, Name: credit.Name, JoinPhrase: credit.JoinPhrase})
	}
	for _, track := range item.Tracks {
		result.Tracks = append(result.Tracks, LocalProviderTrack{ID: track.ID, RecordingID: track.RecordingID, ProviderID: track.ProviderID, ProviderCode: track.ProviderCode, RecordingTitle: track.RecordingTitle, RecordingDurationMS: track.RecordingDurationMS, Position: track.Position, DisplayedNumber: track.DisplayedNumber, TitleOverride: track.TitleOverride, DurationOverrideMS: track.DurationOverrideMS})
	}
	return result, nil
}

func persistenceLocalProviderItem(item LocalProviderItem) persistence.LocalProviderItem {
	genres, _ := json.Marshal(item.Genres)
	result := persistence.LocalProviderItem{ID: item.ID, ProviderID: item.ProviderID, ProviderCode: item.ProviderCode, ProviderKey: item.ProviderKey, Kind: item.Kind, Name: item.Name, SortName: item.SortName, Title: item.Title, ReleaseGroupTitle: item.ReleaseGroupTitle, DurationMS: item.DurationMS, PrimaryType: item.PrimaryType, SecondaryTypes: item.SecondaryTypes, Language: item.Language, Script: item.Script, Genres: genres, Tags: item.Tags}
	for _, credit := range item.Credits {
		result.Credits = append(result.Credits, persistence.LocalProviderCredit{ArtistID: credit.ArtistID, ProviderID: credit.ProviderID, ProviderCode: credit.ProviderCode, ArtistName: credit.ArtistName, Name: credit.Name, JoinPhrase: credit.JoinPhrase})
	}
	for _, track := range item.Tracks {
		result.Tracks = append(result.Tracks, persistence.LocalProviderTrack{ID: track.ID, RecordingID: track.RecordingID, ProviderID: track.ProviderID, ProviderCode: track.ProviderCode, RecordingTitle: track.RecordingTitle, RecordingDurationMS: track.RecordingDurationMS, Position: track.Position, DisplayedNumber: track.DisplayedNumber, TitleOverride: track.TitleOverride, DurationOverrideMS: track.DurationOverrideMS})
	}
	return result
}
