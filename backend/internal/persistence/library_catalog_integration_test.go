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

func TestReadLibraryReleasesAndPositionsPostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := NewLibraryRepository(db)

	// Exercise both repository methods against the real empty schema first; in
	// particular, the release page SQL must parse even when there are no rows.
	emptyReleases, err := repository.ReadLibraryReleases(ctx, LibraryCatalogRequest{Limit: 50, Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if emptyReleases.Total != 0 || len(emptyReleases.Items) != 0 {
		t.Fatalf("empty release page = %#v", emptyReleases)
	}
	emptyPositions, err := repository.ReadLibraryPositions(ctx, LibraryCatalogRequest{Limit: 50, Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if emptyPositions.Total != 0 || len(emptyPositions.Items) != 0 {
		t.Fatalf("empty position page = %#v", emptyPositions)
	}

	artistID, otherArtistID := uuid.New(), uuid.New()
	for _, artist := range []struct {
		id   uuid.UUID
		name string
	}{{artistID, "Shared Artist"}, {otherArtistID, "Other Artist"}} {
		if _, err := db.NewRaw(`INSERT INTO local_artist(id,name,metadata) VALUES (?,?, '{"genres":[]}')`, artist.id, artist.name).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	type releaseFixture struct {
		id       uuid.UUID
		title    string
		artist   uuid.UUID
		track    uuid.UUID
		position uuid.UUID
	}
	fixtures := []releaseFixture{
		{id: uuid.New(), title: "A Alpha", artist: artistID, track: uuid.New(), position: uuid.New()},
		{id: uuid.New(), title: "B Beta", artist: artistID, track: uuid.New(), position: uuid.New()},
		{id: uuid.New(), title: "C Gamma", artist: otherArtistID},
	}
	for i, fixture := range fixtures {
		if _, err := db.NewRaw(`INSERT INTO local_release(id,title) VALUES (?,?)`, fixture.id, fixture.title).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewRaw(`INSERT INTO release_artist(release_id,position,artist_id,name,name_join_phrase) VALUES (?,0,?,?,'')`, fixture.id, fixture.artist, map[bool]string{true: "Shared credit", false: "Other credit"}[i < 2]).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if fixture.track != uuid.Nil {
			if _, err := db.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,?)`, fixture.track, fixture.title+" track").Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := db.NewRaw(`INSERT INTO release_track(id,release_id,track_id,position,medium,displayed_number) VALUES (?,?,?,0,0,'1')`, fixture.position, fixture.id, fixture.track).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	published := fixtures[1]
	if _, err := db.NewRaw(`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?, ?, ?, 'track.flac', '{}', '{}', '{}', '{}', 1, now())`, published.position, uuid.New(), uuid.New()).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	page, err := repository.ReadLibraryReleases(ctx, LibraryCatalogRequest{Limit: 50, Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Items) != 3 {
		t.Fatalf("all releases page = %#v", page)
	}
	if page.Items[0].Title != "A Alpha" || page.Items[1].Title != "B Beta" || page.Items[2].Title != "C Gamma" {
		t.Fatalf("release ordering = %#v", page.Items)
	}

	for _, test := range []struct {
		publication string
		wantID      uuid.UUID
	}{
		{publication: "published", wantID: fixtures[1].id},
		{publication: "unpublished", wantID: fixtures[0].id},
	} {
		filtered, err := repository.ReadLibraryReleases(ctx, LibraryCatalogRequest{Limit: 50, Publication: test.publication})
		if err != nil {
			t.Fatal(err)
		}
		if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].ID != test.wantID {
			t.Fatalf("%s release page = %#v", test.publication, filtered)
		}
	}

	filtered, err := repository.ReadLibraryReleases(ctx, LibraryCatalogRequest{Limit: 1, Publication: "all", ArtistID: &artistID})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 2 || len(filtered.Items) != 1 || filtered.Items[0].ID != fixtures[0].id {
		t.Fatalf("artist release page = %#v", filtered)
	}
	filtered, err = repository.ReadLibraryReleases(ctx, LibraryCatalogRequest{Limit: 1, Query: "gamma", Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].ID != fixtures[2].id {
		t.Fatalf("search release page = %#v", filtered)
	}
	filtered, err = repository.ReadLibraryReleases(ctx, LibraryCatalogRequest{Limit: 1, Offset: 50, Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 3 || len(filtered.Items) != 0 {
		t.Fatalf("out-of-range release page = %#v", filtered)
	}

	positions, err := repository.ReadLibraryPositions(ctx, LibraryCatalogRequest{Limit: 50, Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if positions.Total != 2 || len(positions.Items) != 2 {
		t.Fatalf("all positions page = %#v", positions)
	}
	positions, err = repository.ReadLibraryPositions(ctx, LibraryCatalogRequest{Limit: 50, Publication: "published"})
	if err != nil {
		t.Fatal(err)
	}
	if positions.Total != 1 || len(positions.Items) != 1 || positions.Items[0].ID != published.position {
		t.Fatalf("published positions page = %#v", positions)
	}
}
