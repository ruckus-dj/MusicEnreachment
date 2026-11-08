//go:build integration

package service

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestLocalProviderCRUDPreservesIdentityAndCrossProviderReferencesPostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	externalProviderID, externalArtistID, externalRecordingID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.NewRaw(`INSERT INTO provider(id,code) VALUES (?,'external-fixture')`, externalProviderID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_artist(id,provider_id,provider_key,name) VALUES (?,?,?,'External artist')`, externalArtistID, externalProviderID, "external-artist-key").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_recording(id,provider_id,provider_key,title) VALUES (?,?,?,'External recording')`, externalRecordingID, externalProviderID, "external-recording-key").Exec(ctx); err != nil {
		t.Fatal(err)
	}

	catalog := NewLocalProviderCatalog(persistence.NewLocalProviderRepository(db), nil)
	created, err := catalog.Create(ctx, "release", LocalProviderItem{
		Title: "Local edition", ReleaseGroupTitle: "Local group",
		Credits: []LocalProviderCredit{{ArtistID: externalArtistID, Name: "Live credit", JoinPhrase: " & "}},
		Tracks:  []LocalProviderTrack{{RecordingID: externalRecordingID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ProviderCode != "local" || created.ProviderKey == "" || len(created.Credits) != 1 || created.Credits[0].ProviderID != externalProviderID || created.Credits[0].ProviderCode != "external-fixture" {
		t.Fatalf("created release identity and credit ownership = %#v", created)
	}
	if len(created.Tracks) != 1 || created.Tracks[0].ProviderID != externalProviderID || created.Tracks[0].ProviderCode != "external-fixture" || created.Tracks[0].Position != 0 || created.Tracks[0].DisplayedNumber != "1" {
		t.Fatalf("created release track reference = %#v", created.Tracks)
	}

	trackID := created.Tracks[0].ID
	created.Title = "Renamed edition"
	updated, err := catalog.Update(ctx, "release", created.ID, created)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProviderKey != created.ProviderKey || updated.Tracks[0].ID != trackID || updated.Title != "Renamed edition" {
		t.Fatalf("update changed native or track identity: before=%#v after=%#v", created, updated)
	}

	updated.Tracks = []LocalProviderTrack{}
	if _, err := catalog.Update(ctx, "release", created.ID, updated); err != nil {
		t.Fatal(err)
	}
	var present bool
	var medium, position sql.NullInt64
	if err := db.NewRaw(`SELECT present,medium,position FROM provider_release_track WHERE id=?`, trackID).Scan(ctx, &present, &medium, &position); err != nil {
		t.Fatal(err)
	}
	if present || medium.Valid || position.Valid {
		t.Fatalf("omitted track retained live position: present=%v medium=%v position=%v", present, medium, position)
	}
}
