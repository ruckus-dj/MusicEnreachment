//go:build integration

package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestLibraryArtistMetadataRepositoryUsesRevisionAndCatalog(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	artistID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_artist(id,name) VALUES(?,?)`, artistID, "Genre Test Artist").Exec(ctx); err != nil {
		t.Fatalf("insert local artist: %v", err)
	}
	repository := NewLibraryRepository(db)
	if _, err := db.NewRaw(`UPDATE genre_catalog SET items=?::jsonb,updated_at=now() WHERE singleton=true`, `[{"id":"55555555-5555-4555-8555-555555555555","source_name":"art rock","name":"Art Rock"}]`).Exec(ctx); err != nil {
		t.Fatalf("seed genre catalog: %v", err)
	}
	catalog, err := repository.GenreCatalogItems(ctx)
	if err != nil {
		t.Fatalf("read genre catalog for metadata: %v", err)
	}
	if len(catalog) != 1 || catalog[0].ID != "55555555-5555-4555-8555-555555555555" || catalog[0].Name != "Art Rock" {
		t.Fatalf("genre catalog = %#v", catalog)
	}
	read, err := repository.ReadMetadata(ctx, librarymetadata.ScopeArtist, uuid.Nil, artistID)
	if err != nil {
		t.Fatalf("read initial artist metadata: %v", err)
	}
	if read.Revision != 1 || read.Envelope.Scope != librarymetadata.ScopeArtist {
		t.Fatalf("initial artist metadata = %#v", read)
	}
	genreID := "55555555-5555-4555-8555-555555555555"
	envelope := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion,
		Scope:   librarymetadata.ScopeArtist,
		Desired: map[string]librarymetadata.FieldValue{
			"genre": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []librarymetadata.GenreReference{{ID: genreID, SourceName: "art rock", Name: "Art Rock"}}, true),
		},
	}
	updated, err := repository.UpdateMetadata(ctx, librarymetadata.ScopeArtist, uuid.Nil, artistID, read.Revision, envelope)
	if err != nil {
		t.Fatalf("update artist metadata: %v", err)
	}
	if updated.Revision != 2 {
		t.Fatalf("updated artist revision = %d, want 2", updated.Revision)
	}
	if _, err := repository.UpdateMetadata(ctx, librarymetadata.ScopeArtist, uuid.Nil, artistID, read.Revision, envelope); !errors.Is(err, ErrLibraryMetadataConflict) {
		t.Fatalf("stale artist update error = %v, want conflict", err)
	}
	read, err = repository.ReadMetadata(ctx, librarymetadata.ScopeArtist, uuid.Nil, artistID)
	if err != nil {
		t.Fatalf("read updated artist metadata: %v", err)
	}
	if read.Revision != 2 {
		t.Fatalf("read artist revision = %d, want 2", read.Revision)
	}
	genres, ok := read.Envelope.Desired["genre"].Value.([]any)
	if !ok || len(genres) != 1 {
		t.Fatalf("read artist genres = %#v", read.Envelope.Desired["genre"].Value)
	}
}
