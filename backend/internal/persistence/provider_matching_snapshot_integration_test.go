//go:build integration

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
	rawRelease := json.RawMessage(`{"id":"` + releaseKey + `","title":"Release","country":"GB","artist-credit":[{"name":"Artist","artist":{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"Artist"},"joinphrase":" & "},{"name":"Artist","artist":{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"Artist"}}],"media":[{"position":1,"tracks":[{"id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd","number":"1","length":9000,"recording":{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","title":"Track one","length":10000}}]},{"position":2,"tracks":[{"id":"eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee","number":"1","recording":{"id":"ffffffff-ffff-4fff-8fff-ffffffffffff","title":"Track two"}}]}]}`)
	graph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: rawRelease}})
	if err != nil {
		t.Fatal(err)
	}
	graph.Entities = append(graph.Entities,
		persistence.ProviderSourceEntity{
			Kind: "recording", ProviderKey: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
			Name: "Track one", Fields: json.RawMessage(`{"artist-credit":[{"name":"" ,"artist":{"id":"11111111-1111-4111-8111-111111111111"}},{"name":"","artist":{"id":"11111111-1111-4111-8111-111111111111"}}],"length":10000}`),
			RawSource: json.RawMessage(`{"artist-credit":[{"name":"" ,"artist":{"id":"11111111-1111-4111-8111-111111111111"}},{"name":"","artist":{"id":"11111111-1111-4111-8111-111111111111"}}],"length":10000}`),
			Authority: 3,
		},
		persistence.ProviderSourceEntity{
			Kind: "recording", ProviderKey: "ffffffff-ffff-4fff-8fff-ffffffffffff",
			Name: "Track two", Fields: json.RawMessage(`{"normalized_artists":["22222222-2222-4222-8222-222222222222","22222222-2222-4222-8222-222222222222"],"length":0}`),
			RawSource: json.RawMessage(`{"normalized_artists":["22222222-2222-4222-8222-222222222222","22222222-2222-4222-8222-222222222222"],"length":0}`), Authority: 3,
		},
		persistence.ProviderSourceEntity{Kind: "artist", ProviderKey: "11111111-1111-4111-8111-111111111111", Name: "Recording Artist", Fields: json.RawMessage(`{}`), RawSource: json.RawMessage(`{}`), Authority: 3},
		persistence.ProviderSourceEntity{Kind: "artist", ProviderKey: "22222222-2222-4222-8222-222222222222", Name: "Second Artist", Fields: json.RawMessage(`{}`), RawSource: json.RawMessage(`{}`), Authority: 3},
	)
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
	if snapshot.Source.ID != source.ID || len(snapshot.Releases) != 1 || snapshot.Releases[0].ID == uuid.Nil {
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
	if got := release.Tracks[0].RecordingArtists; len(got) != 2 || got[0] != "Recording Artist" || got[1] != "Recording Artist" || release.Tracks[0].RecordingLengthMS == nil || *release.Tracks[0].RecordingLengthMS != 10000 {
		t.Fatalf("recording projection = %#v", release.Tracks[0])
	}
	if release.Tracks[0].ReleaseLengthMS == nil || *release.Tracks[0].ReleaseLengthMS != 9000 {
		t.Fatalf("release track duration was not read independently from recording length: %#v", release.Tracks[0])
	}
	if got := release.Tracks[1].RecordingArtists; len(got) != 2 || got[0] != "Second Artist" || got[1] != "Second Artist" || release.Tracks[1].RecordingLengthMS == nil || *release.Tracks[1].RecordingLengthMS != 0 {
		t.Fatalf("normalized recording projection = %#v", release.Tracks[1])
	}

	// A publication context resolves the selected local release-track edge by
	// release_track.id (deliberately distinct from track_id) and preserves local
	// manual-override facts without making a protection decision.
	localReleaseID, localTrackID, localRecordingID := uuid.New(), uuid.New(), uuid.New()
	positionID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_release(id,title,metadata) VALUES(?,?, '{}'::jsonb)`, localReleaseID, "Local album").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_track(id,title,metadata) VALUES(?,?,?::jsonb)`, localTrackID, "Local track", `{"manual_matching_override":{"title":"keep"},"local_metadata":{"scope":"position","desired":{"title":{"state":"set","value":"Local title"}}}}`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_recording(id,title,metadata) VALUES(?,?,?::jsonb)`, localRecordingID, "Local recording", `{"manual_matching_override":{"genre":"keep"}}`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO release_track(id,release_id,medium,position,track_id,recording_id,displayed_number) VALUES(?,?,0,0,?,?, '1')`, positionID, localReleaseID, localTrackID, localRecordingID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES(?,?,?,'manual')`, provider.ID, release.ID, localReleaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method) VALUES(?,?,?,'manual')`, provider.ID, release.Tracks[0].ID, localTrackID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var providerRecordingID uuid.UUID
	if err := db.NewRaw(`SELECT id FROM provider_recording WHERE provider_id=? AND provider_key=?`, provider.ID, release.Tracks[0].RecordingProviderKey).Scan(ctx, &providerRecordingID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method) VALUES(?,?,?,'manual')`, provider.ID, providerRecordingID, localRecordingID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	contextRepository := persistence.NewSetupManagerRepository(db)
	providerContext, err := contextRepository.ReadPublicationProviderTagContextForPosition(ctx, localReleaseID, positionID, source.Endpoint, identity)
	if err != nil {
		t.Fatalf("read accepted publication provider context: %v", err)
	}
	if providerContext.PositionOwner.TrackID != localTrackID || providerContext.PositionOwner.Owner.ID != positionID || len(providerContext.PositionOwner.Owner.Metadata) == 0 || providerContext.PositionOwner.Owner.Revision == 0 || providerContext.TrackOwner.ID != localTrackID || !providerContext.RecordingBound || providerContext.RecordingOwner.ID != localRecordingID {
		t.Fatalf("publication provider owner identities = %+v", providerContext)
	}
	if !providerContext.TrackOwner.ManualOverridePresent || providerContext.TrackOwner.Revision == 0 || providerContext.Digest == "" {
		t.Fatalf("publication provider manual override/revision/digest facts = %+v", providerContext.TrackOwner)
	}
	if _, err := db.NewRaw(`UPDATE release_track SET metadata='{"local_metadata":{"version":1,"scope":"position","desired":{}}}'::jsonb,metadata_revision=metadata_revision+1 WHERE id=?`, positionID).Exec(ctx); err != nil {
		t.Fatalf("mutate position metadata authority: %v", err)
	}
	mutatedContext, err := contextRepository.ReadPublicationProviderTagContextForPosition(ctx, localReleaseID, positionID, source.Endpoint, identity)
	if err != nil {
		t.Fatalf("rebuild context after position metadata mutation: %v", err)
	}
	if mutatedContext.PositionOwner.Owner.Revision == providerContext.PositionOwner.Owner.Revision || mutatedContext.Digest == providerContext.Digest {
		t.Fatalf("position metadata mutation did not invalidate frozen digest: before=%+v after=%+v", providerContext.PositionOwner.Owner, mutatedContext.PositionOwner.Owner)
	}
	if _, err := db.NewRaw(`DELETE FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=?`, provider.ID, providerRecordingID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	withoutRecordingBinding, err := contextRepository.ReadPublicationProviderTagContextForPosition(ctx, localReleaseID, positionID, source.Endpoint, identity)
	if err != nil || withoutRecordingBinding.RecordingBound || withoutRecordingBinding.RecordingOwner.ID != localRecordingID || !withoutRecordingBinding.RecordingOwner.ManualOverridePresent || withoutRecordingBinding.Recording != nil {
		t.Fatalf("unbound local recording must not be assumed authoritative: context=%+v err=%v", withoutRecordingBinding, err)
	}
	if providerContext.Release.FieldEvidence["title"].Authority == 0 || providerContext.Release.FieldEvidence["title"].FetchOrder == 0 {
		t.Fatalf("publication provider release title lacks accepted field evidence: %+v", providerContext.Release.FieldEvidence)
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
