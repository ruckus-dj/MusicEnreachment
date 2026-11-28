//go:build integration

package persistence_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestMusicBrainzOnlyRecordingLookupWritesSharedMetadata(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db := testpostgres.OpenMigratedWithRiver(t)
	cache := persistence.NewProviderCacheRepository(db)
	const config = "recording-scope"
	const recording = "00000000-0000-4000-8000-000000000001"
	const editionA = "00000000-0000-4000-8000-000000000002"
	const editionB = "00000000-0000-4000-8000-000000000003"
	const artist = "00000000-0000-4000-8000-000000000004"
	provider := persistence.Provider{ID: uuid.New(), Code: "musicbrainz"}
	if err := cache.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	source := persistence.ProviderSource{ID: uuid.New(), ProviderID: provider.ID,
		Namespace: config, Endpoint: "https://musicbrainz.example/ws/2", ConfigurationIdentity: config}
	if err := cache.CreateProviderSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES ('musicbrainz_config_identity',?)
		ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, config).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	accept := func(key string, payload json.RawMessage, graph persistence.ProviderGraph, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := cache.BeginRefresh(ctx, source.ID, key, config)
		if err != nil {
			t.Fatal(err)
		}
		if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
			Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payload, Graph: graph,
		}); err != nil {
			t.Fatalf("accept %s: %v", key, err)
		}
	}
	acceptRelease := func(id, position, embedded string) {
		t.Helper()
		body := json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Edition","media":[{"tracks":[
			{"title":%q,"artist-credit":[{"name":%q,"artist":{"id":%q,"name":"Artist"}}],
			"recording":{"id":%q,"title":%q}}
		]}]}`, id, position, position, artist, recording, embedded))
		graph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: body}})
		accept("release-lookup:"+id, body, graph, err)
	}
	acceptSearch := func() {
		t.Helper()
		const key = "recording-search:0:10:recording"
		body := json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Search hit","length":120000,"artist-credit":[
			{"name":"Search credit","artist":{"id":%q,"name":"Artist"}}]}`, recording, artist))
		graph, err := service.NormalizeMusicBrainzRecordingSearch(musicbrainz.SearchResult{Items: []musicbrainz.Entity{{Raw: body}}})
		accept(key, json.RawMessage(`{"count":1,"offset":0,"recordings":[`+string(body)+`]}`), graph, err)
		result, err := persistence.NewLibraryRepository(db).ReadLibraryRecordingSearchCache(ctx, source.ID, config, key, 0, 10)
		if err != nil || result == nil || len(result.Items) != 1 || result.Items[0].Title != "Search hit" ||
			len(result.Items[0].Artists) != 1 || result.Items[0].Artists[0].Name != "Search credit" ||
			result.Items[0].DurationMS == nil || *result.Items[0].DurationMS != 120000 {
			t.Fatalf("search did not use its own cached hit: %+v err=%v", result, err)
		}
	}
	readShared := func() (title, raw string) {
		t.Helper()
		if err := db.NewRaw(`SELECT title,raw_source::text FROM provider_recording WHERE provider_id=? AND provider_key=?`,
			provider.ID, recording).Scan(ctx, &title, &raw); err != nil {
			t.Fatal(err)
		}
		return title, raw
	}

	acceptRelease(editionA, "Track A", "Embedded A")
	acceptSearch()
	if title, raw := readShared(); title != "" || raw != "{}" {
		t.Fatalf("release/search wrote shared recording metadata: title=%q raw=%s", title, raw)
	}
	var sharedEntities int
	if err := db.NewRaw(`SELECT count(*) FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='recording' AND provider_key=?`,
		source.ID, recording).Scan(ctx, &sharedEntities); err != nil || sharedEntities != 0 {
		t.Fatalf("release/search wrote shared source projection: count=%d err=%v", sharedEntities, err)
	}
	// Before strict scoping, a release lookup could write authority 3 as well.
	// A new direct lookup must not inherit fields omitted from its own response.
	if _, err := db.NewRaw(`INSERT INTO provider_source_entity
		(provider_source_id,provider_id,entity_kind,provider_key,name,fields,field_evidence,authority,fetch_order)
		VALUES (?,?,'recording',?,'Legacy projection','{"length":900000}',
		'{"length":{"authority":3,"fetch_order":1}}',3,1)`, source.ID, provider.ID, recording).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	for _, title := range []string{"Original direct title", "Direct recording"} {
		body := json.RawMessage(fmt.Sprintf(`{"id":%q,"title":%q,"artist-credit":[
			{"name":"Direct credit","artist":{"id":%q,"name":"Artist"}}]}`, recording, title, artist))
		graph, err := service.NormalizeMusicBrainzRecordingLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: body}})
		accept("recording-lookup:"+recording, body, graph, err)
		if storedTitle, _ := readShared(); storedTitle != title {
			t.Fatalf("direct lookup did not write new title %q: got %q", title, storedTitle)
		}
	}
	markedPayload := json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Do not accept"}`, recording))
	markedGraph, err := service.NormalizeMusicBrainzRecordingLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: markedPayload}})
	if err != nil {
		t.Fatal(err)
	}
	for _, denied := range []struct {
		key     string
		payload json.RawMessage
	}{
		{"release-lookup:" + editionA, markedPayload},
		{"recording-search:0:10:wrong", markedPayload},
		{"recording-lookup:" + editionB, markedPayload},
		{"recording-lookup:" + recording, json.RawMessage(fmt.Sprintf(`{"id":%q}`, editionB))},
	} {
		ticket, err := cache.BeginRefresh(ctx, source.ID, denied.key, config)
		if err != nil {
			t.Fatal(err)
		}
		if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
			Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: denied.payload, Graph: markedGraph,
		}); err == nil {
			t.Fatalf("provider accepted recording metadata outside its direct lookup: %s", denied.key)
		}
	}
	for i := range markedGraph.Entities {
		delete(markedGraph.Entities[i].FieldEvidence, persistence.DirectRecordingLookupEvidence)
	}
	unmarkedTicket, err := cache.BeginRefresh(ctx, source.ID, "release-lookup:"+editionA, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: unmarkedTicket, FetchedAt: time.Now().UTC(), Payload: markedPayload, Graph: markedGraph,
	}); err == nil {
		t.Fatal("provider accepted unmarked recording details from a release lookup")
	}
	beforeTitle, beforeRaw := readShared()
	acceptRelease(editionB, "Track B", "Embedded B")
	acceptSearch()
	if title, raw := readShared(); beforeTitle != "Direct recording" || title != beforeTitle || raw != beforeRaw {
		t.Fatalf("shared recording changed after release/search: before=%q after=%q raw=%s", beforeTitle, title, raw)
	}
	var sharedName, sharedCredit string
	var inheritedLegacyLength bool
	if err := db.NewRaw(`SELECT name,fields#>>'{artist-credit,0,name}',jsonb_exists(fields,'length') FROM provider_source_entity
		WHERE provider_source_id=? AND entity_kind='recording' AND provider_key=?`,
		source.ID, recording).Scan(ctx, &sharedName, &sharedCredit, &inheritedLegacyLength); err != nil || sharedName != "Direct recording" ||
		sharedCredit != "Direct credit" || inheritedLegacyLength {
		t.Fatalf("shared recording projection: name=%q credit=%q legacyLength=%t err=%v", sharedName, sharedCredit, inheritedLegacyLength, err)
	}
	for _, edition := range []struct{ id, track, nested string }{
		{editionA, "Track A", "Embedded A"}, {editionB, "Track B", "Embedded B"},
	} {
		var title, credit, nested string
		if err := db.NewRaw(`SELECT fields#>>'{normalized_tracks,0,title}',fields#>>'{normalized_tracks,0,artist-credit,0,name}',
			fields#>>'{normalized_tracks,0,recording,title}' FROM provider_source_entity
			WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, source.ID, edition.id).
			Scan(ctx, &title, &credit, &nested); err != nil || title != edition.track || credit != edition.track || nested != edition.nested {
			t.Fatalf("edition %s metadata crossed scope: title=%q credit=%q nested=%q err=%v", edition.id, title, credit, nested, err)
		}
	}
}
