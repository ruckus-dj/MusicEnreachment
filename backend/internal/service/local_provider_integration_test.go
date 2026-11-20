//go:build integration

package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

type localProviderGenreFixture struct {
	items []librarymetadata.GenreReference
}

func (fixture *localProviderGenreFixture) GenreCatalogItems(context.Context) ([]librarymetadata.GenreReference, error) {
	return fixture.items, nil
}

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
	recording, err := catalog.Create(ctx, "recording", LocalProviderItem{
		Title: "Saved recording",
		Credits: []LocalProviderCredit{
			{ArtistID: externalArtistID, Name: "Credited artist", JoinPhrase: " & "},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	recordings, err := catalog.List(ctx, "recordings", "Saved recording", "all", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recordings.Items) != 1 || recordings.Items[0].ID != recording.ID ||
		recordings.Items[0].ArtistCredit != "Credited artist & " ||
		len(recording.Credits) != 1 || len(recordings.Items[0].Credits) != 1 ||
		recordings.Items[0].Credits[0] != recording.Credits[0] ||
		recording.Credits[0].ProviderID != externalProviderID {
		t.Fatalf("saved recording lookup changed the caption or reference: %#v", recordings)
	}
	savedByArtist, err := catalog.List(ctx, "recordings", "credited saved", "all", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if savedByArtist.Total != 1 || len(savedByArtist.Items) != 1 || savedByArtist.Items[0].ID != recording.ID {
		t.Fatalf("recording lookup did not match saved artist tokens: %#v", savedByArtist)
	}
	mbProvider, mbSource, mbRecording := uuid.New(), uuid.New(), uuid.New()
	mbKey := uuid.NewString()
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value)
		VALUES('musicbrainz_mode','public'),('musicbrainz_config_identity','catalog-test')
		ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`WITH provider_insert AS (
			INSERT INTO provider(id,code) VALUES (?,'musicbrainz') RETURNING id
		), source_insert AS (
			INSERT INTO provider_source(id,provider_id,namespace,endpoint,configuration_identity)
			SELECT ?,id,'musicbrainz','https://musicbrainz.org/ws/2','catalog-test' FROM provider_insert RETURNING id,provider_id
		), recording_insert AS (
			INSERT INTO provider_recording(id,provider_id,provider_key,title)
			SELECT ?,id,?,'Saved MusicBrainz recording' FROM provider_insert RETURNING provider_key
		)
		INSERT INTO provider_source_entity(provider_source_id,provider_id,entity_kind,provider_key,name,fields,field_evidence,authority,complete,fetch_order)
		SELECT s.id,s.provider_id,'recording',r.provider_key,'Saved MusicBrainz recording',
			'{"artist-credit":[{"name":"Credited A","joinphrase":" feat. "},{"artist":{"name":"Credited B"}}]}'::jsonb,
			'{"artist-credit":{"authority":3,"fetch_order":7}}'::jsonb,3,true,7
		FROM source_insert s CROSS JOIN recording_insert r`, mbProvider, mbSource, mbRecording, mbKey).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	savedExternal, err := catalog.List(ctx, "recordings", "Saved MusicBrainz recording", "all", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(savedExternal.Items) != 1 || savedExternal.Items[0].ID != mbRecording ||
		savedExternal.Items[0].ArtistCredit != "Credited A feat. Credited B" || len(savedExternal.Items[0].Credits) != 0 {
		t.Fatalf("saved MusicBrainz credit did not preserve display-only artist names: %#v", savedExternal)
	}
	created, err := catalog.Create(ctx, "release", LocalProviderItem{
		Title: "Local edition", ReleaseGroupTitle: "Local group",
		Credits: []LocalProviderCredit{
			{ArtistID: externalArtistID, Name: "Live credit", JoinPhrase: " & "},
			{ArtistID: externalArtistID, Name: "Alpha credit"},
		},
		Tracks: []LocalProviderTrack{{RecordingID: externalRecordingID}},
		Edition: &LocalProviderEdition{
			ReleaseDate: "1999-02", Country: "JP", Format: "CD", Label: "First label", CatalogNumber: "CAT-1", Disambiguation: "Limited",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ProviderCode != "local" || created.ProviderKey == "" || len(created.Credits) != 2 || created.Credits[0].ProviderID != externalProviderID || created.Credits[0].ProviderCode != "external-fixture" || created.Credits[1].Name != "Alpha credit" {
		t.Fatalf("created release identity and credit ownership = %#v", created)
	}
	if len(created.Tracks) != 1 || created.Tracks[0].ProviderID != externalProviderID || created.Tracks[0].ProviderCode != "external-fixture" || created.Tracks[0].Position != 0 || created.Tracks[0].DisplayedNumber != "1" {
		t.Fatalf("created release track reference = %#v", created.Tracks)
	}
	if created.Edition == nil || created.Edition.TrackCount != 1 || created.Edition.ReleaseDate != "1999-02" {
		t.Fatalf("create response release edition = %#v", created.Edition)
	}
	read, err := catalog.Read(ctx, "release", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if edition := read.Edition; edition == nil || edition.TrackCount != 1 || edition.ReleaseDate != "1999-02" || edition.Country != "JP" || edition.Format != "CD" || edition.Label != "First label" || edition.CatalogNumber != "CAT-1" || edition.Disambiguation != "Limited" {
		t.Fatalf("created release edition = %#v", edition)
	}

	trackID := created.Tracks[0].ID
	created.Title = "Renamed edition"
	created.Edition.ReleaseDate = "2001-03-04"
	created.Edition.Country = "US"
	created.Edition.Format = "Vinyl"
	created.Edition.Label = "Second label"
	created.Edition.CatalogNumber = "CAT-2"
	created.Edition.Disambiguation = ""
	updated, err := catalog.Update(ctx, "release", created.ID, created)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProviderKey != created.ProviderKey || updated.Tracks[0].ID != trackID || updated.Title != "Renamed edition" {
		t.Fatalf("update changed native or track identity: before=%#v after=%#v", created, updated)
	}
	if edition := updated.Edition; edition == nil || edition.TrackCount != 1 || edition.ReleaseDate != "2001-03-04" || edition.Country != "US" || edition.Format != "Vinyl" || edition.Label != "Second label" || edition.CatalogNumber != "CAT-2" || edition.Disambiguation != "" {
		t.Fatalf("updated release edition = %#v", edition)
	}
	for _, kind := range []string{"release", "releases"} {
		page, err := catalog.List(ctx, kind, "renamed live external", "local", 10, 0)
		if err != nil {
			t.Fatalf("list local releases by title and ordered credit terms: %v", err)
		}
		if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != created.ID {
			t.Fatalf("multi-term local release search across title, credit name and artist name = %#v", page)
		}
		if edition := page.Items[0].Edition; edition == nil || edition.ArtistCredit != "Live credit & Alpha credit" || edition.TrackCount != 1 || edition.ReleaseDate != "2001-03-04" || edition.Country != "US" || edition.Format != "Vinyl" || edition.Label != "Second label" || edition.CatalogNumber != "CAT-2" || edition.Disambiguation != "" {
			t.Fatalf("local release %q edition summary = %#v", kind, edition)
		}
		page, err = catalog.List(ctx, kind, "renamed live absent", "local", 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 0 || len(page.Items) != 0 {
			t.Fatalf("local release search did not require every term: %#v", page)
		}
	}
	impossible := updated
	impossibleEdition := *updated.Edition
	impossibleEdition.ReleaseDate = "2021-02-30"
	impossible.Edition = &impossibleEdition
	if _, err := catalog.Update(ctx, "release", created.ID, impossible); !errors.Is(err, ErrLocalProviderInvalid) {
		t.Fatalf("impossible partial release date error = %v", err)
	}
	unchanged, err := catalog.Read(ctx, "release", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if edition := unchanged.Edition; edition == nil || edition.TrackCount != 1 || edition.ReleaseDate != "2001-03-04" || edition.Country != "US" || edition.Format != "Vinyl" || edition.Label != "Second label" || edition.CatalogNumber != "CAT-2" {
		t.Fatalf("rejected partial date changed accepted edition fields = %#v", edition)
	}

	genreAID, genreBID := uuid.NewString(), uuid.NewString()
	genreCatalog := &localProviderGenreFixture{items: []librarymetadata.GenreReference{{ID: genreAID, SourceName: "old source name", Name: "Old name"}}}
	catalog.genres = genreCatalog
	updated.Genres = []librarymetadata.GenreReference{{ID: genreAID}}
	updated, err = catalog.Update(ctx, "release", created.ID, updated)
	if err != nil {
		t.Fatal(err)
	}
	genreCatalog.items = []librarymetadata.GenreReference{
		{ID: genreAID, SourceName: "refreshed source name", Name: "Refreshed name"},
		{ID: genreBID, SourceName: "new source name", Name: "New name"},
	}
	updated.Genres = []librarymetadata.GenreReference{
		{ID: genreAID, SourceName: "client supplied name", Name: "Client supplied name"},
		{ID: genreBID},
	}
	updated, err = catalog.Update(ctx, "release", created.ID, updated)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Genres) != 2 || updated.Genres[0].ID != genreAID || updated.Genres[0].SourceName != "old source name" || updated.Genres[0].Name != "Old name" || updated.Genres[1].ID != genreBID || updated.Genres[1].SourceName != "new source name" || updated.Genres[1].Name != "New name" {
		t.Fatalf("genre refresh changed a saved name or failed to canonicalize the new genre: %#v", updated.Genres)
	}

	updated.Tracks = []LocalProviderTrack{}
	updated.Edition.ReleaseDate = ""
	updated.Edition.CatalogNumber = ""
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
	page, err := catalog.List(ctx, "releases", "renamed", "local", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Edition == nil || page.Items[0].Edition.ArtistCredit != "Live credit & Alpha credit" || page.Items[0].Edition.TrackCount != 0 || page.Items[0].Edition.ReleaseDate != "" || page.Items[0].Edition.CatalogNumber != "" || page.Items[0].Edition.Country != "US" {
		t.Fatalf("retired local release edition summary = %#v", page)
	}
}
