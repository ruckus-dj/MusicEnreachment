package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

const (
	releaseMBID   = "00000000-0000-4000-8000-000000000001"
	recordingMBID = "00000000-0000-4000-8000-000000000002"
	artistMBID    = "00000000-0000-4000-8000-000000000003"
	trackMBID     = "00000000-0000-4000-8000-000000000004"
)

func TestNormalizeMusicBrainzReleaseLookupPreservesOrderedCreditsTracksAndUnknownFields(t *testing.T) {
	result := musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: json.RawMessage(`{
		"id":"` + releaseMBID + `","title":"Album","unknown_release":{"kept":true},
		"artist-credit":[
			{"artist":{"id":"` + artistMBID + `","name":"Artist","unknown_artist":1},"joinphrase":" feat. "},
			{"artist":{"id":"` + artistMBID + `","name":"Artist"},"joinphrase":""}
		],
		"media":[{"tracks":[{"id":"` + trackMBID + `","number":"A-1","position":"1","recording":{"id":"` + recordingMBID + `","title":"Song","unknown_recording":true}},
		{"number":"pregap","recording":{"id":"00000000-0000-4000-8000-000000000005","title":"Pregap"}}]}]
	}`)}}
	graph, err := NormalizeMusicBrainzReleaseLookup(result)
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Complete || len(graph.Releases) != 1 || len(graph.Credits) != 2 || len(graph.Tracks) != 2 {
		t.Fatalf("normalized release graph = %+v", graph)
	}
	if graph.Credits[0].Position != 0 || graph.Credits[1].Position != 1 || graph.Credits[0].ArtistID != graph.Credits[1].ArtistID || graph.Credits[0].NameJoinPhrase != " feat. " {
		t.Fatalf("ordered repeated artist credits were lost: %+v", graph.Credits)
	}
	wantTrackID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:track:"+trackMBID))
	if graph.Tracks[0].ID != wantTrackID || graph.Tracks[0].Position != 0 || graph.Tracks[0].Medium != 0 || graph.Tracks[0].DisplayedNumber != "A-1" || graph.Tracks[1].DisplayedNumber != "pregap" {
		t.Fatalf("track numeric/display positions = %+v", graph.Tracks)
	}
	var releaseRaw map[string]json.RawMessage
	if err := json.Unmarshal(graph.Releases[0].RawSource, &releaseRaw); err != nil {
		t.Fatal(err)
	}
	if releaseRaw["unknown_release"] == nil {
		t.Fatalf("unknown raw release field was dropped: %s", graph.Releases[0].RawSource)
	}
	if graph.Entities[0].Complete != true {
		t.Fatalf("top-level lookup release was not marked complete: %+v", graph.Entities)
	}
	var releaseFields map[string]json.RawMessage
	if err := json.Unmarshal(graph.Entities[0].Fields, &releaseFields); err != nil {
		t.Fatal(err)
	}
	var normalizedCredits, normalizedTracks []json.RawMessage
	if err := json.Unmarshal(releaseFields["normalized_artist_credits"], &normalizedCredits); err != nil {
		t.Fatalf("normalized ordered credits missing: %s", graph.Entities[0].Fields)
	}
	if err := json.Unmarshal(releaseFields["normalized_tracks"], &normalizedTracks); err != nil || len(normalizedCredits) != 2 || len(normalizedTracks) != 2 {
		t.Fatalf("normalized credit/track projection incomplete: credits=%d tracks=%d err=%v", len(normalizedCredits), len(normalizedTracks), err)
	}
}

func TestNormalizeMusicBrainzSearchAndRecordingLookupRemainPartial(t *testing.T) {
	search := musicbrainz.SearchResult{Items: []musicbrainz.Entity{{Raw: json.RawMessage(`{
		"id":"` + releaseMBID + `","title":"Search hit","score":0,"release_extra":"unknown"
	}`)}}}
	graph, err := NormalizeMusicBrainzReleaseSearch(search)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Complete || len(graph.Entities) != 1 || graph.Entities[0].Complete {
		t.Fatalf("release search became complete: %+v", graph)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(graph.Entities[0].Fields, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["score"]) != "0" || fields["release_extra"] == nil {
		t.Fatalf("zero score or unknown field was not retained: %s", graph.Entities[0].Fields)
	}

	lookup := musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: json.RawMessage(`{
		"id":"` + recordingMBID + `","title":"Recording","releases":[{"id":"` + releaseMBID + `","title":"Summary","release_extra":true}]
	}`)}}
	partial, err := NormalizeMusicBrainzRecordingLookup(lookup)
	if err != nil {
		t.Fatal(err)
	}
	if partial.Complete || len(partial.Recordings) != 1 || len(partial.Releases) != 1 || partial.Entities[1].Complete {
		t.Fatalf("recording lookup incorrectly completed nested release summary: %+v", partial)
	}
}

func TestAppendSourceEntityMergesFieldAuthorityIndependently(t *testing.T) {
	var graph persistence.ProviderGraph
	if err := normalizeRecording(json.RawMessage(`{"id":"`+recordingMBID+`","title":"Recording","country":"GB"}`), &graph, 2); err != nil {
		t.Fatal(err)
	}
	if err := normalizeRelease(json.RawMessage(`{"id":"`+releaseMBID+`","title":"Release search","date":"2026"}`), &graph, false, false, 2); err != nil {
		t.Fatal(err)
	}
	if err := normalizeRelease(json.RawMessage(`{"id":"`+releaseMBID+`","title":"Nested summary","country":"US"}`), &graph, false, false, 1); err != nil {
		t.Fatal(err)
	}
	entity := &graph.Entities[1]
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(entity.Fields, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["country"]) != `"US"` || string(fields["date"]) != `"2026"` || entity.FieldEvidence["country"].Authority != 1 {
		t.Fatalf("field-level winner lost during mixed-authority merge: %s", entity.Fields)
	}
	if err := normalizeRelease(json.RawMessage(`{"id":"`+releaseMBID+`","title":"Release search","country":"CA"}`), &graph, false, false, 2); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(entity.Fields, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["country"]) != `"CA"` {
		t.Fatalf("later search did not supersede nested country: %s", entity.Fields)
	}
	if entity.Authority != 2 || entity.FieldEvidence["country"].Authority != 2 || entity.FieldEvidence["date"].Authority != 2 || entity.FieldEvidence["name"].Authority != 2 {
		t.Fatalf("entity or field provenance incorrect: %+v", entity)
	}
}

func TestNormalizeReleaseTracksPreserveOmittedCollectionsAndHonorExplicitEmpty(t *testing.T) {
	lookup := musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: json.RawMessage(`{
		"id":"` + releaseMBID + `","title":"Album","media":[{"tracks":[{"id":"` + trackMBID + `","number":"1","recording":{"id":"` + recordingMBID + `","title":"Song"}}]}]
	}`)}}
	graph, err := NormalizeMusicBrainzReleaseLookup(lookup)
	if err != nil {
		t.Fatal(err)
	}
	entity := &graph.Entities[0]
	var populated map[string]json.RawMessage
	if err := json.Unmarshal(entity.Fields, &populated); err != nil {
		t.Fatal(err)
	}
	if len(graph.Tracks) != 1 || len(populated["normalized_tracks"]) == 0 {
		t.Fatalf("initial populated track projection missing: tracks=%d fields=%s", len(graph.Tracks), entity.Fields)
	}
	if err := normalizeRelease(json.RawMessage(`{"id":"`+releaseMBID+`","title":"Album","media":[{}]}`), &graph, true, false, 2); err != nil {
		t.Fatal(err)
	}
	var preserved map[string]json.RawMessage
	if err := json.Unmarshal(entity.Fields, &preserved); err != nil {
		t.Fatal(err)
	}
	if string(preserved["normalized_tracks"]) != string(populated["normalized_tracks"]) || len(graph.Tracks) != 1 {
		t.Fatalf("omitted medium tracks erased existing data: fields=%s tracks=%d", entity.Fields, len(graph.Tracks))
	}

	var emptyGraph persistence.ProviderGraph
	if err := normalizeRelease(json.RawMessage(`{"id":"`+releaseMBID+`","title":"Album","media":[{"tracks":[]}]}`), &emptyGraph, true, true, 3); err != nil {
		t.Fatal(err)
	}
	var emptyFields map[string]json.RawMessage
	if err := json.Unmarshal(emptyGraph.Entities[0].Fields, &emptyFields); err != nil {
		t.Fatal(err)
	}
	if string(emptyFields["normalized_tracks"]) != "[]" {
		t.Fatalf("explicit complete empty track list was not represented: %s", emptyGraph.Entities[0].Fields)
	}

	var partialGraph persistence.ProviderGraph
	if err := normalizeRelease(json.RawMessage(`{"id":"`+releaseMBID+`","title":"Album","media":[{"tracks":[]},{}]}`), &partialGraph, true, false, 2); err != nil {
		t.Fatal(err)
	}
	var partialFields map[string]json.RawMessage
	if err := json.Unmarshal(partialGraph.Entities[0].Fields, &partialFields); err != nil {
		t.Fatal(err)
	}
	if partialFields["normalized_tracks"] != nil {
		t.Fatalf("partially omitted media was treated as a complete empty tracklist: %s", partialGraph.Entities[0].Fields)
	}
}
