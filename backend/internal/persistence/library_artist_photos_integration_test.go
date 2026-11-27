//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestReadLibraryArtistPhotoBindingPostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := NewLibraryRepository(db)

	missingID := uuid.New()
	missing, err := repository.ReadLibraryArtistPhotoBinding(ctx, missingID)
	if err != nil {
		t.Fatal(err)
	}
	if missing.ArtistExists || len(missing.ProviderKeys) != 0 {
		t.Fatalf("missing artist binding = %#v", missing)
	}

	unlinkedID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_artist(id,name,metadata) VALUES (?,?, '{}')`, unlinkedID, "Unlinked").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	unlinked, err := repository.ReadLibraryArtistPhotoBinding(ctx, unlinkedID)
	if err != nil {
		t.Fatal(err)
	}
	if !unlinked.ArtistExists || len(unlinked.ProviderKeys) != 0 {
		t.Fatalf("unlinked artist binding = %#v", unlinked)
	}

	providerID, otherProviderID := uuid.New(), uuid.New()
	artistID, providerArtistID, otherArtistID := uuid.New(), uuid.New(), uuid.New()
	mbid := uuid.NewString()
	if _, err := db.NewRaw(`INSERT INTO provider(id,code) VALUES (?, 'musicbrainz'), (?, 'other')`, providerID, otherProviderID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_artist(id,name,metadata) VALUES (?,?, '{}')`, artistID, "Linked").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_artist(id,provider_id,provider_key,name) VALUES (?,?,?, 'Artist'), (?,?,?, 'Other')`,
		providerArtistID, providerID, mbid, otherArtistID, otherProviderID, uuid.NewString()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method) VALUES (?,?,?, 'manual'), (?,?,?, 'manual')`,
		providerID, providerArtistID, artistID, otherProviderID, otherArtistID, artistID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	linked, err := repository.ReadLibraryArtistPhotoBinding(ctx, artistID)
	if err != nil {
		t.Fatal(err)
	}
	if !linked.ArtistExists || len(linked.ProviderKeys) != 1 || linked.ProviderKeys[0] != mbid {
		t.Fatalf("linked artist binding = %#v", linked)
	}
}
