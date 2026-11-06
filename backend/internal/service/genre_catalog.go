package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

const genreCatalogPageSize = 100

type GenreCatalogItem struct {
	ID         string `json:"id"`
	SourceName string `json:"source_name"`
	Name       string `json:"name"`
}

type GenrePageReader interface {
	GenrePage(context.Context, int, int) (musicbrainz.GenrePage, error)
}

// LoadGenreCatalog fetches and validates every page before returning any data;
// callers can therefore replace persisted state atomically only on success.
func LoadGenreCatalog(ctx context.Context, provider GenrePageReader) ([]GenreCatalogItem, error) {
	if provider == nil {
		return nil, fmt.Errorf("musicbrainz genre provider is unavailable")
	}
	var items []GenreCatalogItem
	seen := make(map[string]struct{})
	count := -1
	for offset := 0; count < 0 || offset < count; {
		page, err := provider.GenrePage(ctx, offset, genreCatalogPageSize)
		if err != nil {
			return nil, fmt.Errorf("load MusicBrainz genre page: %w", err)
		}
		if page.Offset != offset || page.Count < 0 || page.Genres == nil || count >= 0 && page.Count != count || len(page.Genres) == 0 && offset < page.Count {
			return nil, fmt.Errorf("MusicBrainz genre catalog page is incomplete")
		}
		if count < 0 {
			count = page.Count
		}
		if len(page.Genres) > genreCatalogPageSize || offset > count || len(page.Genres) > count-offset {
			return nil, fmt.Errorf("MusicBrainz genre catalog page exceeds reported count")
		}
		for _, genre := range page.Genres {
			id, err := uuid.Parse(genre.ID)
			if err != nil || id == uuid.Nil || strings.TrimSpace(genre.Name) == "" {
				return nil, fmt.Errorf("MusicBrainz genre is missing a valid id or name")
			}
			key := strings.ToLower(id.String())
			if _, duplicate := seen[key]; duplicate {
				return nil, fmt.Errorf("MusicBrainz genre catalog contains a duplicate id")
			}
			seen[key] = struct{}{}
			items = append(items, GenreCatalogItem{ID: key, SourceName: genre.Name, Name: librarymetadata.NormalizeGenreName(genre.Name)})
		}
		if len(page.Genres) < genreCatalogPageSize && offset+len(page.Genres) < count {
			return nil, fmt.Errorf("MusicBrainz genre catalog page is incomplete")
		}
		offset += len(page.Genres)
	}
	if len(items) != count {
		return nil, fmt.Errorf("MusicBrainz genre catalog count does not match downloaded entries")
	}
	return items, nil
}
