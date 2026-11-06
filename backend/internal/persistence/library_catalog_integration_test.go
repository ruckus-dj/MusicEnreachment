//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

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

	observed := []uuid.UUID{fixtures[0].position, fixtures[0].position}
	availableRequest := LibraryCatalogRequest{Limit: 1, Publication: "all", Availability: "available", AvailablePositionIDs: observed}
	availableReleases, err := repository.ReadLibraryReleases(ctx, availableRequest)
	if err != nil {
		t.Fatal(err)
	}
	if availableReleases.Total != 1 || len(availableReleases.Items) != 1 || availableReleases.Items[0].ID != fixtures[0].id || availableReleases.Items[0].Counts.AvailablePositionCount != 1 {
		t.Fatalf("source-only availability page = %#v", availableReleases)
	}
	missingRequest := availableRequest
	missingRequest.Availability = "missing"
	missing, err := repository.ReadLibraryReleases(ctx, missingRequest)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Total != 1 || len(missing.Items) != 1 || missing.Items[0].ID != fixtures[1].id || missing.Items[0].Counts.AvailablePositionCount != 0 {
		t.Fatalf("partial release missing availability page = %#v", missing)
	}
	availableRequest.AvailablePositionIDs = append(observed, fixtures[1].position)
	availableRequest.Offset = 1
	availableReleases, err = repository.ReadLibraryReleases(ctx, availableRequest)
	if err != nil {
		t.Fatal(err)
	}
	if availableReleases.Total != 2 || len(availableReleases.Items) != 1 || availableReleases.Items[0].ID != fixtures[1].id || availableReleases.Items[0].Counts.AvailablePositionCount != 1 {
		t.Fatalf("both-kind availability page with offset = %#v", availableReleases)
	}
	availableArtists, err := repository.ReadLibraryArtists(ctx, LibraryCatalogRequest{Limit: 50, Publication: "all", Availability: "available", AvailablePositionIDs: observed})
	if err != nil {
		t.Fatal(err)
	}
	if availableArtists.Total != 1 || availableArtists.Items[0].AvailablePositionCount != 1 || availableArtists.Items[0].PositionCount != 2 {
		t.Fatalf("partial artist availability = %#v", availableArtists)
	}
	availablePositions, err := repository.ReadLibraryPositions(ctx, LibraryCatalogRequest{Limit: 50, Publication: "all", Availability: "available", AvailablePositionIDs: observed})
	if err != nil {
		t.Fatal(err)
	}
	if availablePositions.Total != 1 || len(availablePositions.Items) != 1 || !availablePositions.Items[0].AudioAvailable {
		t.Fatalf("observed position availability = %#v", availablePositions)
	}
	for _, test := range []struct {
		name         string
		availability string
		wantTotal    int
	}{
		{name: "all", availability: "all", wantTotal: 2},
		{name: "available", availability: "available", wantTotal: 1},
		{name: "missing", availability: "missing", wantTotal: 1},
	} {
		t.Run("artist "+test.name, func(t *testing.T) {
			artists, err := repository.ReadLibraryArtists(ctx, LibraryCatalogRequest{Limit: 1, Offset: 99, Publication: "all", Availability: test.availability, AvailablePositionIDs: observed[:1]})
			if err != nil {
				t.Fatal(err)
			}
			if artists.Total != test.wantTotal || len(artists.Items) != 0 {
				t.Fatalf("out-of-range artist page = total %d, items %#v", artists.Total, artists.Items)
			}
		})
	}

	// Candidate reads use actual source-location/variant associations and only
	// current publications in the current configured output namespace.
	rootID, variantID, unlocatedVariantID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.NewRaw(`INSERT INTO source_root(id,configured_path,display_name,enabled,scan_generation,inventory_path,status) VALUES (?, '/candidate-root','candidate',true,1,'/candidate-root','available')`, rootID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{variantID, unlocatedVariantID} {
		if _, err := db.NewRaw(`INSERT INTO media_variant(id,size_bytes) VALUES (?,123)`, id).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.NewRaw(`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?,false,true,'manual','{}'),(?,?,?,false,true,'manual','{}')`, uuid.New(), fixtures[0].track, variantID, uuid.New(), fixtures[0].track, unlocatedVariantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?, 'album/source.flac',123,'2026-11-06T00:00:00Z',1,'audio',?)`, uuid.New(), rootID, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var currentEpoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &currentEpoch); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE track_publication SET epoch=? WHERE position_id=?`, currentEpoch, published.position).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES ('output_directory','/current-output') ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	candidates, err := repository.ReadLibraryAudioCandidates(ctx, LibraryCatalogRequest{ReleaseID: &fixtures[0].id})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Kind != "source" || candidates[0].RelativePath != "album/source.flac" || candidates[0].RootPath != "/candidate-root" {
		t.Fatalf("source candidates = %#v", candidates)
	}
	candidates, err = repository.ReadLibraryAudioCandidates(ctx, LibraryCatalogRequest{ReleaseID: &fixtures[1].id})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Kind != "publication" || candidates[0].RootPath != "/current-output" {
		t.Fatalf("current publication candidates = %#v", candidates)
	}
	if _, err := db.NewRaw(`UPDATE track_publication SET epoch=? WHERE position_id=?`, uuid.New(), published.position).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	candidates, err = repository.ReadLibraryAudioCandidates(ctx, LibraryCatalogRequest{ReleaseID: &fixtures[1].id})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("stale-epoch publication candidates = %#v", candidates)
	}
	if _, err := db.NewRaw(`UPDATE source_root SET status='unavailable',safe_error='test unavailable' WHERE id=?`, rootID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	candidates, err = repository.ReadLibraryAudioCandidates(ctx, LibraryCatalogRequest{ReleaseID: &fixtures[0].id})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("unavailable root candidates = %#v", candidates)
	}
	if _, err := db.NewRaw(`UPDATE source_root SET status='available',safe_error=NULL,inventory_path='/stale-inventory' WHERE id=?`, rootID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	candidates, err = repository.ReadLibraryAudioCandidates(ctx, LibraryCatalogRequest{ReleaseID: &fixtures[0].id})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("stale inventory candidates = %#v", candidates)
	}
}
