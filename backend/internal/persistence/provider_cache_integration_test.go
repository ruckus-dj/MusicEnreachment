package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestProviderCacheGenerationAndGraphPersistenceWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewProviderCacheRepository(db)
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name, setting_value) VALUES('musicbrainz_config_identity', 'cfg-1') ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	providerA := persistence.Provider{ID: uuid.New(), Code: "provider-a"}
	providerB := persistence.Provider{ID: uuid.New(), Code: "provider-b"}
	if err := repository.CreateProvider(ctx, &providerA); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateProvider(ctx, &providerB); err != nil {
		t.Fatal(err)
	}
	source := persistence.ProviderSource{ID: uuid.New(), ProviderID: providerA.ID, Namespace: "catalog", Endpoint: "https://example.invalid/api", ConfigurationIdentity: "cfg-1"}
	if err := repository.CreateProviderSource(ctx, &source); err != nil {
		t.Fatal(err)
	}

	first, err := repository.BeginRefresh(ctx, source.ID, "release/search:empty", "cfg-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.BeginRefresh(ctx, source.ID, "release/search:empty", "cfg-1")
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation <= first.Generation {
		t.Fatalf("generation %d did not supersede %d", second.Generation, first.Generation)
	}
	if err := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: first, FetchedAt: time.Now(), Payload: json.RawMessage(`[]`)}); err == nil {
		t.Fatal("out-of-order refresh was accepted")
	}
	response := persistence.ProviderSuccessfulResponse{Ticket: second, FetchedAt: time.Now().UTC(), Revision: "r-empty", Payload: json.RawMessage(`[]`)}
	if err := repository.ApplySuccessfulResponse(ctx, response); err != nil {
		t.Fatalf("apply empty successful response: %v", err)
	}
	stored, err := repository.GetCachedResponse(ctx, source.ID, second.CacheKey)
	if err != nil || stored == nil || string(stored.Payload) != `[]` || stored.FetchedAt == nil || stored.Revision == nil || *stored.Revision != "r-empty" {
		t.Fatalf("cached empty response = %+v, %v", stored, err)
	}

	artist := persistence.ProviderArtist{ID: uuid.New(), ProviderKey: "artist-1", Name: "Same Artist", Metadata: json.RawMessage(`{"country":"GB"}`), RawSource: json.RawMessage(`{"artist-extra":true}`)}
	release := persistence.ProviderRelease{ID: uuid.New(), ProviderKey: "release-1", Title: "Release", Metadata: json.RawMessage(`{"year":2024}`), RawSource: json.RawMessage(`{"release-extra":true}`)}
	recording := persistence.ProviderRecording{ID: uuid.New(), ProviderKey: "recording-1", Title: "Track", Metadata: json.RawMessage(`{"duration":123}`), RawSource: json.RawMessage(`{"recording-extra":true}`)}
	recording2 := persistence.ProviderRecording{ID: uuid.New(), ProviderKey: "recording-2", Title: "Track 2", Metadata: json.RawMessage(`{"duration":456}`), RawSource: json.RawMessage(`{"recording-extra":2}`)}
	track := persistence.ProviderReleaseTrack{ID: uuid.New(), ReleaseID: release.ID, RecordingID: recording.ID, Medium: 0, Position: 0, DisplayedNumber: "A-1"}
	track2 := persistence.ProviderReleaseTrack{ID: uuid.New(), ReleaseID: release.ID, RecordingID: recording2.ID, Medium: 0, Position: 1, DisplayedNumber: "A-2"}
	graph := persistence.ProviderGraph{Complete: true,
		Artists: []persistence.ProviderArtist{artist}, Releases: []persistence.ProviderRelease{release}, Recordings: []persistence.ProviderRecording{recording, recording2},
		Credits: []persistence.ProviderReleaseArtistCredit{
			{ReleaseID: release.ID, Position: 0, ArtistID: artist.ID, NameJoinPhrase: " & "},
			{ReleaseID: release.ID, Position: 1, ArtistID: artist.ID},
		}, Tracks: []persistence.ProviderReleaseTrack{track, track2},
	}
	next, err := repository.BeginRefresh(ctx, source.ID, "release/1", "cfg-1")
	if err != nil {
		t.Fatal(err)
	}
	response = persistence.ProviderSuccessfulResponse{Ticket: next, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"complete":true}`), Graph: graph}
	if err := repository.ApplySuccessfulResponse(ctx, response); err != nil {
		t.Fatalf("apply complete graph: %v", err)
	}
	var credits int
	if err := db.NewRaw(`SELECT count(*) FROM provider_release_artist_credit WHERE provider_id=? AND release_id=? AND present`, providerA.ID, release.ID).Scan(ctx, &credits); err != nil || credits != 2 {
		t.Fatalf("repeated credits = %d, %v; want 2", credits, err)
	}
	var trackID uuid.UUID
	if err := db.NewRaw(`SELECT id FROM provider_release_track WHERE provider_id=? AND release_id=? AND present AND medium=0 AND position=0 AND displayed_number='A-1'`, providerA.ID, release.ID).Scan(ctx, &trackID); err != nil || trackID != track.ID {
		t.Fatalf("pregap track = %s, %v", trackID, err)
	}
	stableIDRefresh, err := repository.BeginRefresh(ctx, source.ID, "release/1", "cfg-1")
	if err != nil {
		t.Fatal(err)
	}
	changedIDs := graph
	changedIDs.Complete = false
	changedIDs.Artists = []persistence.ProviderArtist{{ID: uuid.New(), ProviderKey: artist.ProviderKey, Name: artist.Name}}
	changedIDs.Releases = []persistence.ProviderRelease{{ID: uuid.New(), ProviderKey: release.ProviderKey, Title: release.Title}}
	changedIDs.Recordings = []persistence.ProviderRecording{
		{ID: uuid.New(), ProviderKey: recording.ProviderKey, Title: recording.Title},
		{ID: uuid.New(), ProviderKey: recording2.ProviderKey, Title: recording2.Title},
	}
	changedIDs.Credits = []persistence.ProviderReleaseArtistCredit{{ReleaseID: changedIDs.Releases[0].ID, Position: 0, ArtistID: changedIDs.Artists[0].ID}}
	changedIDs.Tracks = []persistence.ProviderReleaseTrack{
		{ID: track.ID, ReleaseID: changedIDs.Releases[0].ID, RecordingID: changedIDs.Recordings[0].ID, Medium: 0, Position: 0, DisplayedNumber: "A-1"},
		{ID: track2.ID, ReleaseID: changedIDs.Releases[0].ID, RecordingID: changedIDs.Recordings[1].ID, Medium: 0, Position: 1, DisplayedNumber: "A-2"},
	}
	if err := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: stableIDRefresh, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{}`), Graph: changedIDs}); err != nil {
		t.Fatalf("refresh provider keys with regenerated input IDs: %v", err)
	}
	var stableReleaseID uuid.UUID
	if err := db.NewRaw(`SELECT id FROM provider_release WHERE provider_id=? AND provider_key=?`, providerA.ID, release.ProviderKey).Scan(ctx, &stableReleaseID); err != nil || stableReleaseID != release.ID {
		t.Fatalf("release ID changed across provider-key refresh: %s, %v", stableReleaseID, err)
	}
	for _, entity := range []struct {
		table string
		id    uuid.UUID
		key   string
		meta  string
		raw   string
	}{
		{"provider_artist", artist.ID, artist.ProviderKey, `{"country":"GB"}`, `{"artist-extra":true}`},
		{"provider_release", release.ID, release.ProviderKey, `{"year":2024}`, `{"release-extra":true}`},
		{"provider_recording", recording.ID, recording.ProviderKey, `{"duration":123}`, `{"recording-extra":true}`},
	} {
		var preserved bool
		query := `SELECT metadata=?::jsonb AND raw_source=?::jsonb FROM ` + entity.table + ` WHERE provider_id=? AND provider_key=?`
		if err := db.NewRaw(query, entity.meta, entity.raw, providerA.ID, entity.key).Scan(ctx, &preserved); err != nil || !preserved {
			t.Fatalf("partial title-only refresh erased %s metadata/source: preserved=%v err=%v", entity.table, preserved, err)
		}
	}
	// An explicitly supplied empty object is distinct from an omitted partial
	// field and intentionally clears the stored source detail.
	clearDetails := changedIDs
	clearDetails.Artists[0].Metadata = json.RawMessage(`{}`)
	clearDetails.Artists[0].RawSource = json.RawMessage(`{}`)
	clearDetails.Releases[0].Metadata = json.RawMessage(`{}`)
	clearDetails.Releases[0].RawSource = json.RawMessage(`{}`)
	clearDetails.Recordings[0].Metadata = json.RawMessage(`{}`)
	clearDetails.Recordings[0].RawSource = json.RawMessage(`{}`)
	clearTicket, err := repository.BeginRefresh(ctx, source.ID, "release/1", "cfg-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: clearTicket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{}`), Graph: clearDetails}); err != nil {
		t.Fatalf("apply explicit empty metadata/source: %v", err)
	}
	for _, entity := range []struct {
		table string
		id    uuid.UUID
	}{{"provider_artist", artist.ID}, {"provider_release", release.ID}, {"provider_recording", recording.ID}} {
		var cleared bool
		if err := db.NewRaw(`SELECT metadata='{}'::jsonb AND raw_source='{}'::jsonb FROM `+entity.table+` WHERE provider_id=? AND id=?`, providerA.ID, entity.id).Scan(ctx, &cleared); err != nil || !cleared {
			t.Fatalf("explicit empty JSON did not clear %s details: cleared=%v err=%v", entity.table, cleared, err)
		}
	}

	// Complete graph reconciliation defers position uniqueness while tracks swap,
	// retains absent track IDs, and permits reintroducing an inactive position.
	applyCompleteTracks := func(name string, tracks []persistence.ProviderReleaseTrack) {
		t.Helper()
		ticket, beginErr := repository.BeginRefresh(ctx, source.ID, "release/1", "cfg-1")
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		trackGraph := persistence.ProviderGraph{
			Complete: true,
			Releases: []persistence.ProviderRelease{release},
			Credits: []persistence.ProviderReleaseArtistCredit{
				{ReleaseID: release.ID, Position: 0, ArtistID: artist.ID, NameJoinPhrase: " & "},
				{ReleaseID: release.ID, Position: 1, ArtistID: artist.ID},
			},
			Tracks: tracks,
		}
		if applyErr := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{}`), Graph: trackGraph}); applyErr != nil {
			t.Fatalf("%s: %v", name, applyErr)
		}
	}
	track.Position, track2.Position = 1, 0
	applyCompleteTracks("swap tracks", []persistence.ProviderReleaseTrack{track, track2})
	assertTrackPosition := func(id uuid.UUID, position int, present bool) {
		t.Helper()
		var actualPosition *int
		var actualPresent bool
		if err := db.NewRaw(`SELECT position, present FROM provider_release_track WHERE provider_id=? AND id=?`, providerA.ID, id).Scan(ctx, &actualPosition, &actualPresent); err != nil || actualPresent != present || (present && (actualPosition == nil || *actualPosition != position)) {
			t.Fatalf("track %s position/presence = %v/%v, err=%v; want %d/%v", id, actualPosition, actualPresent, err, position, present)
		}
	}
	assertTrackPosition(track.ID, 1, true)
	assertTrackPosition(track2.ID, 0, true)
	track.Position = 0
	applyCompleteTracks("remove second track", []persistence.ProviderReleaseTrack{track})
	assertTrackPosition(track.ID, 0, true)
	assertTrackPosition(track2.ID, 0, false)
	track.Position, track2.Position = 1, 0
	applyCompleteTracks("reintroduce removed track", []persistence.ProviderReleaseTrack{track, track2})
	assertTrackPosition(track.ID, 1, true)
	assertTrackPosition(track2.ID, 0, true)

	// A search result is partial and cannot hide the previously complete track list
	// or ordered credits, even when it contains no graph rows.
	partial, err := repository.BeginRefresh(ctx, source.ID, "release/1", "cfg-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: partial, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{}`), Graph: persistence.ProviderGraph{}}); err != nil {
		t.Fatal(err)
	}
	var presentTracks, presentCredits int
	if err := db.NewRaw(`SELECT count(*) FROM provider_release_track WHERE provider_id=? AND release_id=? AND present`, providerA.ID, release.ID).Scan(ctx, &presentTracks); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM provider_release_artist_credit WHERE provider_id=? AND release_id=? AND present`, providerA.ID, release.ID).Scan(ctx, &presentCredits); err != nil {
		t.Fatal(err)
	}
	if presentTracks != 2 || presentCredits != 2 {
		t.Fatalf("partial refresh erased complete graph: tracks=%d credits=%d", presentTracks, presentCredits)
	}

	// Invalid responses and unsuccessful calls do not replace the prior payload or
	// fetched timestamp. A successful malformed graph also rolls back its cache write.
	before, err := repository.GetCachedResponse(ctx, source.ID, "release/1")
	if err != nil {
		t.Fatal(err)
	}
	bad, err := repository.BeginRefresh(ctx, source.ID, "release/1", "cfg-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: bad, FetchedAt: time.Now(), Payload: json.RawMessage(`not json`)}); err == nil {
		t.Fatal("invalid JSON response was accepted")
	}
	after, err := repository.GetCachedResponse(ctx, source.ID, "release/1")
	if err != nil || string(after.Payload) != string(before.Payload) || !after.FetchedAt.Equal(*before.FetchedAt) {
		t.Fatalf("failed refresh changed successful cache: before=%+v after=%+v err=%v", before, after, err)
	}

	// Provider identity is part of every catalog foreign key, so cross-provider
	// links are rejected even when the referenced UUID exists.
	otherRelease := persistence.ProviderRelease{ID: uuid.New(), ProviderKey: "release-b", Title: "Other"}
	otherRecording := persistence.ProviderRecording{ID: uuid.New(), ProviderKey: "recording-b", Title: "Other"}
	if _, err := db.NewRaw(`INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES(?,?,?,?)`, otherRelease.ID, providerB.ID, otherRelease.ProviderKey, otherRelease.Title).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_recording(id,provider_id,provider_key,title) VALUES(?,?,?,?)`, otherRecording.ID, providerB.ID, otherRecording.ProviderKey, otherRecording.Title).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_release_track(id,provider_id,release_id,recording_id,medium,position,displayed_number) VALUES(?,?,?,?,?,?,?)`, uuid.New(), providerA.ID, otherRelease.ID, otherRecording.ID, 0, 0, "1").Exec(ctx); err == nil {
		t.Fatal("cross-provider track references were accepted")
	}
	fenced, err := repository.BeginRefresh(ctx, source.ID, "release/1", "cfg-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE provider_source SET configuration_identity='cfg-2' WHERE id=?`, source.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: fenced, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("response from obsolete source configuration was accepted")
	}
}

func TestProviderSourceProjectionAuthorityAndEndpointIsolation(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name, setting_value) VALUES('musicbrainz_config_identity', 'cfg-source') ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	providerID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz-provider"))
	if _, err := db.NewRaw(`INSERT INTO provider(id, code) VALUES(?, 'musicbrainz') ON CONFLICT (code) DO NOTHING`, providerID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT id FROM provider WHERE code='musicbrainz'`).Scan(ctx, &providerID); err != nil {
		t.Fatal(err)
	}
	newSource := func(namespace, endpoint string) persistence.ProviderSource {
		t.Helper()
		source := persistence.ProviderSource{ID: uuid.New(), ProviderID: providerID, Namespace: namespace, Endpoint: endpoint, ConfigurationIdentity: "cfg-source"}
		if err := persistence.NewProviderCacheRepository(db).CreateProviderSource(ctx, &source); err != nil {
			t.Fatal(err)
		}
		return source
	}
	sourceA := newSource("mb-a", "https://mb-a.invalid/ws/2")
	sourceB := newSource("mb-b", "https://mb-b.invalid/ws/2")
	repository := persistence.NewProviderCacheRepository(db)
	const key = "00000000-0000-4000-8000-000000000001"
	olderSearch, err := repository.BeginRefresh(ctx, sourceA.ID, "search", "cfg-source")
	if err != nil {
		t.Fatal(err)
	}
	newerLookup, err := repository.BeginRefresh(ctx, sourceA.ID, "lookup", "cfg-source")
	if err != nil {
		t.Fatal(err)
	}
	apply := func(ticket persistence.ProviderRefreshTicket, fields, name string, authority int) {
		t.Helper()
		graph := persistence.ProviderGraph{Entities: []persistence.ProviderSourceEntity{{
			Kind: "release", ProviderKey: key, Name: name, Fields: json.RawMessage(fields),
			RawSource: json.RawMessage(`{"unknown":true}`), Authority: authority,
		}}}
		if err := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{}`), Graph: graph}); err != nil {
			t.Fatal(err)
		}
	}
	apply(newerLookup, `{"title":"lookup","media":[{"tracks":[]}]}`, "Lookup", 3)
	apply(olderSearch, `{"title":"search","score":95}`, "Search", 2)
	olderLookup, err := repository.BeginRefresh(ctx, sourceA.ID, "lookup-old", "cfg-source")
	if err != nil {
		t.Fatal(err)
	}
	newerLookup, err = repository.BeginRefresh(ctx, sourceA.ID, "lookup-new", "cfg-source")
	if err != nil {
		t.Fatal(err)
	}
	apply(newerLookup, `{"title":"new lookup","media":[]}`, "New lookup", 3)
	apply(olderLookup, `{"title":"old lookup"}`, "Old lookup", 3)
	newSourceBTicket, err := repository.BeginRefresh(ctx, sourceB.ID, "lookup", "cfg-source")
	if err != nil {
		t.Fatal(err)
	}
	apply(newSourceBTicket, `{"title":"other source"}`, "Other", 3)
	var fieldsA, fieldsB string
	if err := db.NewRaw(`SELECT fields::text FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, sourceA.ID, key).Scan(ctx, &fieldsA); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT fields::text FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, sourceB.ID, key).Scan(ctx, &fieldsB); err != nil {
		t.Fatal(err)
	}
	var decodedA, decodedB map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fieldsA), &decodedA); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(fieldsB), &decodedB); err != nil {
		t.Fatal(err)
	}
	var titleA, titleB string
	_ = json.Unmarshal(decodedA["title"], &titleA)
	_ = json.Unmarshal(decodedB["title"], &titleB)
	if titleA != "new lookup" || string(decodedA["media"]) != "[]" || string(decodedA["score"]) != "95" {
		t.Fatalf("lookup/search projection precedence = %s", fieldsA)
	}
	if titleB != "other source" || titleA == titleB {
		t.Fatalf("endpoint projections mixed: A=%s B=%s", fieldsA, fieldsB)
	}
	priorityKey := "00000000-0000-4000-8000-000000000006"
	olderLookupTicket, err := repository.BeginRefresh(ctx, sourceA.ID, "lookup-before-search", "cfg-source")
	if err != nil {
		t.Fatal(err)
	}
	newerSearchTicket, err := repository.BeginRefresh(ctx, sourceA.ID, "search-after-lookup", "cfg-source")
	if err != nil {
		t.Fatal(err)
	}
	applyPriority := func(ticket persistence.ProviderRefreshTicket, fields string, authority int) {
		t.Helper()
		graph := persistence.ProviderGraph{Entities: []persistence.ProviderSourceEntity{{Kind: "release", ProviderKey: priorityKey, Fields: json.RawMessage(fields), RawSource: json.RawMessage(`{}`), Authority: authority}}}
		if err := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{}`), Graph: graph}); err != nil {
			t.Fatal(err)
		}
	}
	applyPriority(newerSearchTicket, `{"title":"search","score":88}`, 2)
	applyPriority(olderLookupTicket, `{"title":"lookup"}`, 3)
	var priorityFields string
	if err := db.NewRaw(`SELECT fields::text FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, sourceA.ID, priorityKey).Scan(ctx, &priorityFields); err != nil {
		t.Fatal(err)
	}
	var priority map[string]json.RawMessage
	if err := json.Unmarshal([]byte(priorityFields), &priority); err != nil {
		t.Fatal(err)
	}
	var priorityTitle string
	_ = json.Unmarshal(priority["title"], &priorityTitle)
	if priorityTitle != "lookup" || string(priority["score"]) != "88" {
		t.Fatalf("older lookup failed to override search while preserving search-only field: %s", priorityFields)
	}
	before, err := repository.GetCachedResponse(ctx, sourceA.ID, "lookup-new")
	if err != nil {
		t.Fatal(err)
	}
	malformed, err := repository.BeginRefresh(ctx, sourceA.ID, "lookup-new", "cfg-source")
	if err != nil {
		t.Fatal(err)
	}
	err = repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: malformed, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"bad_graph":true}`),
		Graph: persistence.ProviderGraph{Entities: []persistence.ProviderSourceEntity{{Kind: "release", ProviderKey: key, Fields: json.RawMessage(`[]`), RawSource: json.RawMessage(`{}`), Authority: 3}}},
	})
	if err == nil {
		t.Fatal("malformed normalized graph was accepted")
	}
	after, err := repository.GetCachedResponse(ctx, sourceA.ID, "lookup-new")
	if err != nil || before == nil || after == nil || string(before.Payload) != string(after.Payload) {
		t.Fatalf("malformed graph did not roll back cache payload: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestMusicBrainzStableSourceIdentityAndConfigurationSwitchback(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	registry := settings.New(persistence.NewSettingsRepository(db), nil)
	cache := persistence.NewProviderCacheRepository(db)
	if err := registry.SetMusicBrainzConfig(ctx, "public", ""); err != nil {
		t.Fatal(err)
	}
	publicConfig, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	publicA, err := cache.ResolveMusicBrainzSource(ctx, "https://musicbrainz.org/ws/2", publicConfig.Identity)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := cache.BeginRefresh(ctx, publicA.ID, "release-lookup:one", publicConfig.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(), Revision: "saved-revision", Payload: json.RawMessage(`{"id":"saved"}`),
		Graph: persistence.ProviderGraph{Entities: []persistence.ProviderSourceEntity{{Kind: "release", ProviderKey: "release-one", Name: "Saved", Fields: json.RawMessage(`{"title":"Saved"}`), RawSource: json.RawMessage(`{"title":"Saved"}`), Authority: 3, Complete: true}}},
	}); err != nil {
		t.Fatal(err)
	}
	staleTicket, err := cache.BeginRefresh(ctx, publicA.ID, "stale-after-switch", publicConfig.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", "https://local.invalid/ws/2"); err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: staleTicket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"stale":true}`),
	}); err == nil {
		t.Fatal("response reserved under an old configuration was accepted")
	}
	localConfig, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	local, err := cache.ResolveMusicBrainzSource(ctx, localConfig.BaseURL, localConfig.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if local.ID == publicA.ID || local.ProviderID != publicA.ProviderID {
		t.Fatalf("source identity must vary by endpoint while provider remains stable: public=%+v local=%+v", publicA, local)
	}
	if err := registry.SetMusicBrainzConfig(ctx, "public", ""); err != nil {
		t.Fatal(err)
	}
	publicConfigAgain, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	publicAgain, err := cache.ResolveMusicBrainzSource(ctx, "https://musicbrainz.org/ws/2", publicConfigAgain.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if publicAgain.ID != publicA.ID || publicAgain.ConfigurationIdentity == publicA.ConfigurationIdentity {
		t.Fatalf("public source was not deterministically reused with the current config fence: first=%+v second=%+v", publicA, publicAgain)
	}
	cached, err := cache.GetCachedResponse(ctx, publicAgain.ID, "release-lookup:one")
	var cachedPayload struct {
		ID string `json:"id"`
	}
	if cached != nil {
		if err := json.Unmarshal(cached.Payload, &cachedPayload); err != nil {
			t.Fatalf("decode switchback cache payload: %v", err)
		}
	}
	if err != nil || cached == nil || cachedPayload.ID != "saved" || cached.Revision == nil || *cached.Revision != "saved-revision" {
		t.Fatalf("switchback did not retain successful endpoint cache: cached=%+v err=%v", cached, err)
	}
}
