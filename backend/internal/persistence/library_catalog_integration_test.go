//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestLibraryCatalogPagesUseLiteralNormalizedSearchAndKeepTotalsOutOfRangePostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	artistID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_artist(id,name,metadata) VALUES (?,?,'{"genres":[]}')`, artistID, "  100%_ Mix  ").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	repository := NewLibraryRepository(db)
	page, err := repository.ReadLibraryArtists(ctx, LibraryCatalogRequest{Limit: 10, Query: "%_", Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != artistID || page.Items[0].ProviderLinks == nil || page.Items[0].Genres == nil {
		t.Fatalf("literal search result = %#v", page)
	}
	page, err = repository.ReadLibraryArtists(ctx, LibraryCatalogRequest{Limit: 10, Offset: 100, Query: "%_", Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 0 {
		t.Fatalf("out-of-range page = %#v", page)
	}
}
