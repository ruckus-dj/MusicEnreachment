//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestLibraryCatalogSearchNormalizesUnicodeWhitespaceAndKeepsLiteralTotalsPostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	matchingID := uuid.New()
	otherID := uuid.New()
	for _, value := range []struct {
		id   uuid.UUID
		name string
	}{
		{matchingID, "Cafe\u00a0  Mix"},
		{otherID, "Literal 100%_ Artist"},
	} {
		if _, err := db.NewRaw(`INSERT INTO local_artist(id,name,metadata) VALUES (?,?,'{"genres":[]}')`, value.id, value.name).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	repository := NewLibraryRepository(db)
	page, err := repository.ReadLibraryArtists(ctx, LibraryCatalogRequest{Limit: 1, Query: "Ｃａｆｅ Mix", Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != matchingID {
		t.Fatalf("NFKC whitespace search = %#v", page)
	}
	page, err = repository.ReadLibraryArtists(ctx, LibraryCatalogRequest{Limit: 1, Offset: 1, Query: "100%_", Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 0 {
		t.Fatalf("literal out-of-range search = %#v", page)
	}
}
