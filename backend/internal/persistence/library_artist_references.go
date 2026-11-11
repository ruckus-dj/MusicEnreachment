package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type LibraryArtistReferenceRequest struct {
	Query  string
	Limit  int
	Offset int
}

type LibraryArtistReference struct {
	ID             uuid.UUID
	Name           string
	Disambiguation *string
	ProviderLinks  []LibraryCatalogProviderLink
}

type LibraryArtistReferencePage struct {
	Items []LibraryArtistReference
	Total int
}

func (repository *LibraryRepository) ReadLibraryArtistReferences(ctx context.Context, request LibraryArtistReferenceRequest) (LibraryArtistReferencePage, error) {
	page := LibraryArtistReferencePage{Items: []LibraryArtistReference{}}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		nameExpression := `lower(btrim(regexp_replace(normalize(name,NFKC),'[[:space:]]+',' ','g')))`
		queryExpression := `lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g')))`
		if err := tx.NewRaw(`SELECT count(*) FROM local_artist WHERE (?='' OR strpos(`+nameExpression+`,`+queryExpression+`)>0)`, request.Query, request.Query).Scan(ctx, &page.Total); err != nil {
			return fmt.Errorf("count local artist references: %w", err)
		}
		var artists []LibraryCatalogArtist
		if err := tx.NewRaw(`SELECT id,name,NULLIF(metadata->>'disambiguation','') AS disambiguation
			FROM local_artist
			WHERE (?='' OR strpos(`+nameExpression+`,`+queryExpression+`)>0)
			ORDER BY `+nameExpression+`,id LIMIT ? OFFSET ?`, request.Query, request.Query, request.Limit, request.Offset).Scan(ctx, &artists); err != nil {
			return fmt.Errorf("read local artist references: %w", err)
		}
		if artists == nil {
			artists = []LibraryCatalogArtist{}
		}
		for i := range artists {
			artists[i].ProviderLinks = []LibraryCatalogProviderLink{}
		}
		if err := readArtistProviderLinksBatch(ctx, tx, artists); err != nil {
			return fmt.Errorf("read local artist reference provider links: %w", err)
		}
		for _, artist := range artists {
			page.Items = append(page.Items, LibraryArtistReference{ID: artist.ID, Name: artist.Name, Disambiguation: artist.Disambiguation, ProviderLinks: artist.ProviderLinks})
		}
		return nil
	})
	return page, err
}
