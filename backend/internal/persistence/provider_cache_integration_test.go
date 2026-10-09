package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestProviderCacheGenerationAndGraphPersistenceWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewProviderCacheRepository(db)
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
