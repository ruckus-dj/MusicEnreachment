//go:build integration

package persistence_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestLocalProviderUnhydratedMusicBrainzRecordingsPostgreSQL(t *testing.T) {
	ctx := t.Context()
	db := testpostgres.OpenMigrated(t)
	repository := persistence.NewLocalProviderRepository(db)

	musicBrainzProviderID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO provider(id,code) VALUES (?,'musicbrainz')`, musicBrainzProviderID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	localProviderID := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	insertRecording := func(id, providerID uuid.UUID, key, title string) {
		t.Helper()
		if _, err := db.NewRaw(`INSERT INTO provider_recording(id,provider_id,provider_key,title) VALUES (?,?,?,?)`,
			id, providerID, key, title).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	unhydratedID, hydratedID, localID, unknownID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	insertRecording(unhydratedID, musicBrainzProviderID, "unhydrated-key", "")
	insertRecording(hydratedID, musicBrainzProviderID, "hydrated-key", "Hydrated recording")
	insertRecording(localID, localProviderID, "local-key", "")

	keys, err := repository.UnhydratedMusicBrainzRecordings(ctx,
		[]uuid.UUID{unhydratedID, hydratedID, localID, unknownID})
	if err != nil {
		t.Fatal(err)
	}
	want := []persistence.LocalProviderRecordingIdentity{{ProviderID: musicBrainzProviderID, ProviderKey: "unhydrated-key"}}
	if !slices.Equal(keys, want) {
		t.Fatalf("unhydrated recordings = %v, want %v", keys, want)
	}

	keys, err = repository.UnhydratedMusicBrainzRecordings(ctx, []uuid.UUID{localID})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("local recording returned MusicBrainz hydration candidates: %v", keys)
	}
	if keys, err := repository.UnhydratedMusicBrainzRecordings(ctx, nil); err != nil || len(keys) != 0 {
		t.Fatalf("empty selection = %v err=%v", keys, err)
	}
}
