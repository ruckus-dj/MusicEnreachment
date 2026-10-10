package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestMusicBrainzMatchingSnapshotUsesAcceptedSourceProjection(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewProviderCacheRepository(db)
	const identity = "matching-snapshot"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name, setting_value) VALUES('musicbrainz_config_identity', ?) ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	provider := persistence.Provider{ID: uuid.New(), Code: "musicbrainz"}
	if err := repository.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	source := persistence.ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "matching-snapshot", Endpoint: "https://musicbrainz.example/ws/2", ConfigurationIdentity: identity}
	if err := repository.CreateProviderSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	otherSource := persistence.ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "other-matching-snapshot", Endpoint: "https://other.example/ws/2", ConfigurationIdentity: identity}
	if err := repository.CreateProviderSource(ctx, &otherSource); err != nil {
		t.Fatal(err)
	}
	const releaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	rawRelease := json.RawMessage(`{"id":"` + releaseKey + `","title":"Release","country":"GB","artist-credit":[{"name":"Artist","artist":{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"Artist"},"joinphrase":" & "},{"name":"Artist","artist":{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"Artist"}}],"media":[{"position":1,"tracks":[{"id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd","number":"1","recording":{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","title":"Track one"}}]},{"position":2,"tracks":[{"id":"eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee","number":"1","recording":{"id":"ffffffff-ffff-4fff-8fff-ffffffffffff","title":"Track two"}}]}]}`)
	graph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: rawRelease}})
	if err != nil {
		t.Fatal(err)
	}
	apply := func(key string, payload string, sourceID uuid.UUID) persistence.ProviderRefreshTicket {
		t.Helper()
		ticket, err := repository.BeginRefresh(ctx, sourceID, key, identity)
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(payload), Graph: graph}); err != nil {
			t.Fatal(err)
		}
		return ticket
	}
	apply("release-search:0:2:query-one", `{"releases":[{"id":"`+releaseKey+`","score":0},{"id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd"}]}`, source.ID)
	acceptedTicket := apply("release-search:0:2:query-two", `{"releases":[{"id":"`+releaseKey+`","score":87}]}`, source.ID)
	partialTicket, err := repository.BeginRefresh(ctx, source.ID, "release-search:0:2:partial", identity)
	if err != nil {
		t.Fatal(err)
	}
	partial := persistence.ProviderGraph{Entities: []persistence.ProviderSourceEntity{{
		Kind: "release", ProviderKey: releaseKey,
		Fields: json.RawMessage(`{"genre":"ambient"}`), RawSource: json.RawMessage(`{"genre":"ambient"}`), Authority: 2,
	}}}
	if err := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: partialTicket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"releases":[]}`), Graph: partial}); err != nil {
		t.Fatal(err)
	}

	// A refresh reservation advances response generation/fetch order but is not
	// accepted data and must not change the projection's collection fence.
	if _, err := repository.BeginRefresh(ctx, source.ID, "reserved-but-fails", identity); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.GetMusicBrainzMatchingSnapshot(ctx, source.Endpoint, identity, []string{releaseKey, "missing-release"}, []string{"release-search:0:2:query-one", "release-search:0:2:query-two", "reserved-but-fails", "never-loaded"})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Source.ID != source.ID || len(snapshot.Releases) != 1 {
		t.Fatalf("source-scoped releases = %#v; source = %s", snapshot.Releases, snapshot.Source.ID)
	}
	release := snapshot.Releases[0]
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(release.Fields, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["country"]) != `"GB"` || string(fields["title"]) != `"Release"` || string(fields["genre"]) != `"ambient"` || release.FieldEvidence["country"].Authority != 3 || release.FieldEvidence["__credits"].FetchOrder != acceptedTicket.FetchOrder || string(release.RawSource) != `{"genre": "ambient"}` && string(release.RawSource) != `{"genre":"ambient"}` {
		t.Fatalf("release projection/evidence = %#v", release)
	}
	if len(release.Credits) != 2 || release.Credits[0].Position != 0 || release.Credits[1].Position != 1 || release.Credits[0].ArtistProviderKey != release.Credits[1].ArtistProviderKey {
		t.Fatalf("ordered repeated credits = %#v", release.Credits)
	}
	if len(release.Tracks) != 2 || release.Tracks[0].Medium != 0 || release.Tracks[0].Position != 0 || release.Tracks[1].Medium != 1 || release.Tracks[1].Position != 0 {
		t.Fatalf("zero-based medium/track positions = %#v", release.Tracks)
	}
	queryOne := snapshot.SearchScores["release-search:0:2:query-one"]
	queryTwo := snapshot.SearchScores["release-search:0:2:query-two"]
	if !queryOne[releaseKey].Present || queryOne[releaseKey].Value != 0 || queryOne["dddddddd-dddd-4ddd-8ddd-dddddddddddd"].Present {
		t.Fatalf("query one scores = %#v", queryOne)
	}
	if !queryTwo[releaseKey].Present || queryTwo[releaseKey].Value != 87 {
		t.Fatalf("query two scores = %#v", queryTwo)
	}
	if snapshot.Responses["never-loaded"] != nil || snapshot.Responses["reserved-but-fails"] == nil || snapshot.Responses["reserved-but-fails"].Payload != nil {
		t.Fatalf("cache miss vs reserved empty response not distinguished: %#v", snapshot.Responses)
	}

	other := apply("release-search:0:2:other", `{"releases":[{"id":"`+releaseKey+`","score":12}]}`, otherSource.ID)
	_ = other
	otherSnapshot, err := repository.GetMusicBrainzMatchingSnapshot(ctx, otherSource.Endpoint, identity, nil, []string{"release-search:0:2:query-one"})
	if err != nil {
		t.Fatal(err)
	}
	if otherSnapshot.Source.ID != otherSource.ID || otherSnapshot.Responses["release-search:0:2:query-one"] != nil {
		t.Fatalf("source cache crossed endpoint boundary: %#v", otherSnapshot)
	}
}

func TestMusicBrainzMatchingSnapshotKeepsReleaseCollectionsSourceScoped(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewProviderCacheRepository(db)
	const identity = "source-scoped-collections"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name, setting_value) VALUES('musicbrainz_config_identity', ?) ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	provider := persistence.Provider{ID: uuid.New(), Code: "musicbrainz"}
	if err := repository.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	sourceA := persistence.ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "collections-a", Endpoint: "https://a.example/ws/2", ConfigurationIdentity: identity}
	sourceB := persistence.ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "collections-b", Endpoint: "https://b.example/ws/2", ConfigurationIdentity: identity}
	for _, source := range []*persistence.ProviderSource{&sourceA, &sourceB} {
		if err := repository.CreateProviderSource(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	const releaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const artistKey = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const recordingKey = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	const trackKey = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	lookup := func(artistName, creditName, recordingTitle string, prefixTracks bool) persistence.ProviderGraph {
		t.Helper()
		mediaTracks := `{"id":"` + trackKey + `","number":"1","recording":{"id":"` + recordingKey + `","title":"` + recordingTitle + `"}}`
		if prefixTracks {
			mediaTracks = `{"id":"eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee","number":"1","recording":{"id":"ffffffff-ffff-4fff-8fff-ffffffffffff","title":"Leading track"}},` + mediaTracks
		}
		raw := json.RawMessage(`{"id":"` + releaseKey + `","title":"Shared release","artist-credit":[{"name":"` + creditName + `","artist":{"id":"` + artistKey + `","name":"` + artistName + `"}}],"media":[{"position":1,"tracks":[` + mediaTracks + `]}]}`)
		graph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: raw}})
		if err != nil {
			t.Fatal(err)
		}
		return graph
	}
	apply := func(source persistence.ProviderSource, key string, graph persistence.ProviderGraph) {
		t.Helper()
		ticket, err := repository.BeginRefresh(ctx, source.ID, key, identity)
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"releases":[]}`), Graph: graph}); err != nil {
			t.Fatal(err)
		}
	}
	apply(sourceA, "lookup-a", lookup("Artist A", "", "Recording A", false))
	apply(sourceB, "lookup-b", lookup("Artist B", "Explicit B credit", "Recording B", true))

	// A lower-authority search summary can add sparse evidence, but must not
	// replace source B's accepted lookup names or collection projection.
	staleGraph, err := service.NormalizeMusicBrainzReleaseSearch(musicbrainz.SearchResult{Items: []musicbrainz.Entity{{Raw: json.RawMessage(`{"id":"` + releaseKey + `","title":"Shared release","artist-credit":[{"name":"Stale credit","artist":{"id":"` + artistKey + `","name":"Stale artist"}}],"media":[{"tracks":[{"id":"` + trackKey + `","number":"99","recording":{"id":"` + recordingKey + `","title":"Stale recording"}}]}]}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	apply(sourceB, "search-b", staleGraph)

	read := func(source persistence.ProviderSource) persistence.MatchingRelease {
		t.Helper()
		snapshot, err := repository.GetMusicBrainzMatchingSnapshot(ctx, source.Endpoint, identity, []string{releaseKey}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Releases) != 1 {
			t.Fatalf("source %s releases = %#v", source.ID, snapshot.Releases)
		}
		return snapshot.Releases[0]
	}
	releaseA, releaseB := read(sourceA), read(sourceB)
	if len(releaseA.Credits) != 1 || releaseA.Credits[0].Name != "Artist A" || len(releaseA.Tracks) != 1 || releaseA.Tracks[0].Position != 0 || releaseA.Tracks[0].RecordingTitle != "Recording A" {
		t.Fatalf("source A projection was overwritten: %#v", releaseA)
	}
	if len(releaseB.Credits) != 1 || releaseB.Credits[0].Name != "Explicit B credit" || len(releaseB.Tracks) != 2 || releaseB.Tracks[1].Position != 1 || releaseB.Tracks[1].RecordingTitle != "Recording B" {
		t.Fatalf("source B projection or accepted lookup values = %#v", releaseB)
	}
}
