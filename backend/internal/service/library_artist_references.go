package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type LibraryArtistReference struct {
	ID             uuid.UUID
	Name           string
	Disambiguation *string
	ProviderLinks  []LibraryCatalogProviderLink
}

type LibraryArtistReferencePage struct {
	Items  []LibraryArtistReference
	Total  int
	Limit  int
	Offset int
}

type libraryArtistReferencesRepository interface {
	ReadLibraryArtistReferences(context.Context, persistence.LibraryArtistReferenceRequest) (persistence.LibraryArtistReferencePage, error)
}

// ArtistReferences searches the complete set of local artists for metadata-credit selection.
func (catalog *LibraryCatalog) ArtistReferences(ctx context.Context, query string, limit, offset int) (LibraryArtistReferencePage, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return LibraryArtistReferencePage{}, fmt.Errorf("library artist reference pagination is invalid")
	}
	repository, err := catalog.repo()
	if err != nil {
		return LibraryArtistReferencePage{}, err
	}
	referenceRepository, ok := repository.(libraryArtistReferencesRepository)
	if !ok {
		return LibraryArtistReferencePage{}, fmt.Errorf("library artist references: %w", errLibraryCatalogUnavailable)
	}
	query = normalizeLibraryCatalogQuery(query)
	page, err := referenceRepository.ReadLibraryArtistReferences(ctx, persistence.LibraryArtistReferenceRequest{Query: query, Limit: limit, Offset: offset})
	if err != nil {
		return LibraryArtistReferencePage{}, fmt.Errorf("read library artist references: %w", err)
	}
	items := make([]LibraryArtistReference, 0, len(page.Items))
	for _, artist := range page.Items {
		links := make([]LibraryCatalogProviderLink, 0, len(artist.ProviderLinks))
		for _, link := range artist.ProviderLinks {
			links = append(links, LibraryCatalogProviderLink{ProviderCode: link.ProviderCode, ProviderKey: link.ProviderKey, Method: link.Method})
		}
		items = append(items, LibraryArtistReference{ID: artist.ID, Name: artist.Name, Disambiguation: artist.Disambiguation, ProviderLinks: links})
	}
	return LibraryArtistReferencePage{Items: items, Total: page.Total, Limit: limit, Offset: offset}, nil
}
