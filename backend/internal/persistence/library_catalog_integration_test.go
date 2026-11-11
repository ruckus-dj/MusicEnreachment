//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
	guestArtistID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_artist(id,name,metadata) VALUES (?, 'AAA Guest Artist', '{"genres":[]}')`, guestArtistID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO track_artist(track_id,position,artist_id,name,name_join_phrase) VALUES (?,0,?,'AAA Guest Artist','')`, fixtures[0].track, guestArtistID).Exec(ctx); err != nil {
		t.Fatal(err)
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
	if page.Items[0].LibraryRelease.Title != "A Alpha" || page.Items[1].LibraryRelease.Title != "B Beta" || page.Items[2].LibraryRelease.Title != "C Gamma" {
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
		if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].Kind != "library_release" || filtered.Items[0].LibraryRelease.ID != test.wantID {
			t.Fatalf("%s release page = %#v", test.publication, filtered)
		}
	}

	filtered, err := repository.ReadLibraryReleases(ctx, LibraryCatalogRequest{Limit: 1, Publication: "all", ArtistID: &artistID})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 2 || len(filtered.Items) != 1 || filtered.Items[0].LibraryRelease.ID != fixtures[0].id {
		t.Fatalf("artist release page = %#v", filtered)
	}
	filtered, err = repository.ReadLibraryReleases(ctx, LibraryCatalogRequest{Limit: 1, Query: "gamma", Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].LibraryRelease.ID != fixtures[2].id {
		t.Fatalf("search release page = %#v", filtered)
	}

	// Accepted MusicBrainz projections contribute one missing-group row for a
	// group, regardless of how many cached editions describe it. Concrete local
	// releases remain separate rows, including Alpha without source or publication.
	const configIdentity = "library-catalog-test-config"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES
		('musicbrainz_mode','public'),('musicbrainz_base_url',''),('musicbrainz_config_identity',?)
		ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, configIdentity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	providerRepo := NewProviderCacheRepository(db)
	provider := Provider{ID: uuid.New(), Code: "musicbrainz"}
	if err := providerRepo.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	const endpoint = "https://musicbrainz.org/ws/2"
	source := ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "musicbrainz", Endpoint: endpoint, ConfigurationIdentity: configIdentity}
	if err := providerRepo.CreateProviderSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	groupID := uuid.New()
	groupKey := groupID.String()
	groupMissingID := uuid.New()
	missingGroupKey := groupMissingID.String()
	providerArtistKey := uuid.New().String()
	artistFields, err := json.Marshal([]map[string]any{{"name": "Shared Artist", "artist": map[string]string{"id": providerArtistKey, "name": "Shared Artist"}}})
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	providerReleases := make([]ProviderRelease, 0, len(keys))
	entities := make([]ProviderSourceEntity, 0, len(keys))
	for i, key := range keys {
		providerReleases = append(providerReleases, ProviderRelease{ID: uuid.New(), ProviderKey: key, Title: key})
		group := groupKey
		if i >= 2 {
			group = missingGroupKey
		}
		fields, err := json.Marshal(map[string]any{"release-group": map[string]string{"id": group, "title": "Shared Group"}})
		if err != nil {
			t.Fatal(err)
		}
		if i >= 2 {
			fields, err = json.Marshal(map[string]any{"release-group": map[string]string{"id": group, "title": "Shared Group"}, "artist-credit": json.RawMessage(artistFields)})
			if err != nil {
				t.Fatal(err)
			}
		}
		entities = append(entities, ProviderSourceEntity{Kind: "release", ProviderKey: key, Fields: fields, RawSource: json.RawMessage(`{}`), Authority: 1, FieldEvidence: map[string]ProviderFieldEvidence{"release-group": {Authority: 1}}, Complete: false})
	}
	ticket, err := providerRepo.BeginRefresh(ctx, source.ID, "release-group-catalog", configIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := providerRepo.ApplySuccessfulResponse(ctx, ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{}`),
		Graph: ProviderGraph{Artists: []ProviderArtist{{ID: uuid.New(), ProviderKey: providerArtistKey, Name: "Shared Artist"}}, Releases: providerReleases, Entities: entities},
	}); err != nil {
		t.Fatalf("accept MusicBrainz release-group projections: %v", err)
	}
	// The same provider's accepted projection from an unconfigured endpoint is
	// not catalog authority, even when its fields and artist credit are valid.
	otherSource := ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "other", Endpoint: "https://other.invalid/ws/2", ConfigurationIdentity: configIdentity}
	if err := providerRepo.CreateProviderSource(ctx, &otherSource); err != nil {
		t.Fatal(err)
	}
	otherRelease := ProviderRelease{ID: uuid.New(), ProviderKey: "foreign-source-edition", Title: "Foreign Source Edition"}
	otherGroupID := uuid.New()
	otherFields, err := json.Marshal(map[string]any{"release-group": map[string]string{"id": otherGroupID.String(), "title": "Foreign Group"}, "artist-credit": json.RawMessage(artistFields)})
	if err != nil {
		t.Fatal(err)
	}
	otherTicket, err := providerRepo.BeginRefresh(ctx, otherSource.ID, "foreign-source", configIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := providerRepo.ApplySuccessfulResponse(ctx, ProviderSuccessfulResponse{
		Ticket: otherTicket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{}`),
		Graph: ProviderGraph{Releases: []ProviderRelease{otherRelease}, Entities: []ProviderSourceEntity{{Kind: "release", ProviderKey: otherRelease.ProviderKey, Fields: otherFields, RawSource: json.RawMessage(`{}`), Authority: 1, FieldEvidence: map[string]ProviderFieldEvidence{"release-group": {Authority: 1}}, Complete: false}}},
	}); err != nil {
		t.Fatalf("accept other-source projection: %v", err)
	}
	for i, release := range providerReleases[:2] {
		if _, err := db.NewRaw(`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual')`, provider.ID, release.ID, fixtures[i].id).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var providerArtistID uuid.UUID
	if err := db.NewRaw(`SELECT id FROM provider_artist WHERE provider_id=? AND provider_key=?`, provider.ID, providerArtistKey).Scan(ctx, &providerArtistID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method) VALUES (?,?,?,'manual')`, provider.ID, providerArtistID, artistID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	artistPage, err := repository.ReadLibraryArtists(ctx, LibraryCatalogRequest{Limit: 50, Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if artistPage.Total != 2 {
		t.Fatalf("artist list after accepted group projections = %#v", artistPage)
	}
	artistPage, err = repository.ReadLibraryArtists(ctx, LibraryCatalogRequest{Limit: 1, Offset: 1, Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if artistPage.Total != 2 || len(artistPage.Items) != 1 || artistPage.Items[0].ID != artistID {
		t.Fatalf("artist list pagination with track-only guest = %#v", artistPage)
	}
	var listedArtist *LibraryCatalogArtist
	for i := range artistPage.Items {
		if artistPage.Items[i].ID == artistID {
			listedArtist = &artistPage.Items[i]
			break
		}
	}
	if listedArtist == nil || listedArtist.ReleaseCount != 3 {
		t.Fatalf("artist list accepted release count = %#v, want 3", artistPage.Items)
	}
	artistDetail, err := repository.ReadLibraryArtist(ctx, artistID)
	if err != nil {
		t.Fatal(err)
	}
	if artistDetail == nil || artistDetail.ReleaseCount != 3 {
		t.Fatalf("artist detail concrete release count = %#v, want 3", artistDetail)
	}
	groupPage, err := repository.ReadLibraryReleases(ctx, LibraryCatalogRequest{Limit: 1, Publication: "all", ArtistID: &artistID})
	if err != nil {
		t.Fatal(err)
	}
	if groupPage.Total != 3 || len(groupPage.Items) != 1 || groupPage.Items[0].Kind != "library_release" || groupPage.Items[0].LibraryRelease == nil {
		t.Fatalf("artist catalog first page = %#v, want two concrete editions plus one distinct missing group", groupPage)
	}
	groupPage, err = repository.ReadLibraryReleases(ctx, LibraryCatalogRequest{Limit: 10, Publication: "all", ArtistID: &artistID})
	if err != nil {
		t.Fatal(err)
	}
	if groupPage.Total != 3 || len(groupPage.Items) != 3 {
		t.Fatalf("artist catalog complete page = %#v, want total 3", groupPage)
	}
	var missingGroups int
	for _, item := range groupPage.Items {
		if item.Kind == "missing_release_group" {
			missingGroups++
			if item.MissingReleaseGroup == nil || item.MissingReleaseGroup.ProviderKey != missingGroupKey {
				t.Fatalf("missing group item = %#v", item)
			}
		}
	}
	if missingGroups != 1 {
		t.Fatalf("missing group rows = %d, want one for repeated cached editions: %#v", missingGroups, groupPage.Items)
	}
	editions, err := repository.ReadLibraryReleaseGroupEditions(ctx, groupMissingID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	editionKeys := map[string]bool{}
	for _, edition := range editions.Items {
		editionKeys[edition.ProviderKey] = edition.TrackCount == nil
	}
	if editions.Total != 2 || len(editions.Items) != 2 || !editionKeys[keys[2]] || !editionKeys[keys[3]] {
		t.Fatalf("cached missing-group editions = %#v, want both editions and unknown track counts", editions)
	}
	// Tracklist inspection of an accepted but incomplete edition is read-only and
	// must preserve unknown completeness/count rather than infer either value.
	tracks, err := repository.ReadLibraryReleaseGroupEditionTracks(ctx, groupMissingID, keys[2])
	if err != nil {
		t.Fatal(err)
	}
	if tracks.Complete != nil || tracks.TrackCount != nil || len(tracks.Tracks) != 0 {
		t.Fatalf("incomplete cached edition tracklist = %#v", tracks)
	}
	var localReleaseCount int
	if err := db.NewRaw(`SELECT count(*) FROM local_release`).Scan(ctx, &localReleaseCount); err != nil || localReleaseCount != len(fixtures) {
		t.Fatalf("tracklist read created or removed local releases: count %d, err %v", localReleaseCount, err)
	}
	filtered, err = repository.ReadLibraryReleases(ctx, LibraryCatalogRequest{Limit: 1, Offset: 50, Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 4 || len(filtered.Items) != 0 {
		t.Fatalf("out-of-range release page = %#v", filtered)
	}

	positions, err := repository.ReadLibraryPositions(ctx, LibraryCatalogRequest{Limit: 50, Publication: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if positions.Total != 2 || len(positions.Items) != 2 {
		t.Fatalf("all positions page = %#v", positions)
	}
	var guestCreditPreserved bool
	for _, credit := range positions.Items[0].Artists {
		if credit.ArtistID != nil && *credit.ArtistID == guestArtistID && credit.CreditName == "AAA Guest Artist" {
			guestCreditPreserved = true
		}
	}
	if !guestCreditPreserved {
		t.Fatalf("track-only guest credit missing from position: %#v", positions.Items[0].Artists)
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
	if availableReleases.Total != 1 || len(availableReleases.Items) != 1 || availableReleases.Items[0].Kind != "library_release" || availableReleases.Items[0].LibraryRelease == nil || availableReleases.Items[0].LibraryRelease.ID != fixtures[0].id || availableReleases.Items[0].LibraryRelease.Counts.AvailablePositionCount != 1 {
		t.Fatalf("source-only availability page = %#v", availableReleases)
	}
	missingRequest := availableRequest
	missingRequest.Availability = "missing"
	missing, err := repository.ReadLibraryReleases(ctx, missingRequest)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Total != 2 || len(missing.Items) != 1 || missing.Items[0].Kind != "library_release" || missing.Items[0].LibraryRelease == nil || missing.Items[0].LibraryRelease.ID != fixtures[1].id || missing.Items[0].LibraryRelease.Counts.AvailablePositionCount != 0 {
		t.Fatalf("partial release missing availability page = %#v", missing)
	}
	availableRequest.AvailablePositionIDs = append(observed, fixtures[1].position)
	availableRequest.Offset = 1
	availableReleases, err = repository.ReadLibraryReleases(ctx, availableRequest)
	if err != nil {
		t.Fatal(err)
	}
	if availableReleases.Total != 2 || len(availableReleases.Items) != 1 || availableReleases.Items[0].Kind != "library_release" || availableReleases.Items[0].LibraryRelease == nil || availableReleases.Items[0].LibraryRelease.ID != fixtures[1].id || availableReleases.Items[0].LibraryRelease.Counts.AvailablePositionCount != 1 {
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
