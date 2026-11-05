//go:build integration

package persistence

import (
	"encoding/json"

	"github.com/google/uuid"
)

// This exercises only the provider-authority prefix validator. It does not
// exercise publication admission, result delivery, or River enqueueing.
func publicationProviderTagAuthorityGraph() ProviderGraph {
	const releaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const artistKey = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const recordingKey = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	const trackKey = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	releaseID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:release:"+releaseKey))
	artistID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:artist:"+artistKey))
	recordingID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:recording:"+recordingKey))
	trackID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:track:"+trackKey))
	raw := json.RawMessage(`{"id":"` + releaseKey + `","title":"Release","artist-credit":[{"name":"Artist","artist":{"id":"` + artistKey + `","name":"Artist"}}],"media":[{"position":1,"tracks":[{"id":"` + trackKey + `","number":"1","recording":{"id":"` + recordingKey + `","title":"Track"}}]}]}`)
	artistRaw := json.RawMessage(`{"id":"` + artistKey + `","name":"Artist"}`)
	recordingRaw := json.RawMessage(`{"id":"` + recordingKey + `","title":"Track"}`)
	fields := json.RawMessage(`{"title":"Release","artist-credit":[{"name":"Artist","artist":{"id":"` + artistKey + `","name":"Artist"}}],"normalized_tracks":[{"id":"` + trackID.String() + `","recording_id":"` + recordingID.String() + `","recording_provider_key":"` + recordingKey + `","medium":0,"position":0,"displayed_number":"1"}]}`)
	evidence := map[string]ProviderFieldEvidence{
		"title": {Authority: 3}, "artist-credit": {Authority: 3}, "normalized_tracks": {Authority: 3},
	}
	return ProviderGraph{
		Complete:   true,
		Artists:    []ProviderArtist{{ID: artistID, ProviderKey: artistKey, Name: "Artist", RawSource: artistRaw}},
		Releases:   []ProviderRelease{{ID: releaseID, ProviderKey: releaseKey, Title: "Release", RawSource: raw}},
		Recordings: []ProviderRecording{{ID: recordingID, ProviderKey: recordingKey, Title: "Track", RawSource: recordingRaw}},
		Credits:    []ProviderReleaseArtistCredit{{ReleaseID: releaseID, Position: 0, ArtistID: artistID, Name: "Artist"}},
		Tracks:     []ProviderReleaseTrack{{ID: trackID, ReleaseID: releaseID, RecordingID: recordingID, Medium: 0, Position: 0, DisplayedNumber: "1", RecordingProviderKey: recordingKey}},
		Entities: []ProviderSourceEntity{
			{Kind: "release", ProviderKey: releaseKey, Name: "Release", Fields: fields, RawSource: raw, Authority: 3, FieldEvidence: evidence, Complete: true},
			{Kind: "artist", ProviderKey: artistKey, Name: "Artist", Fields: json.RawMessage(`{}`), RawSource: artistRaw, Authority: 3, FieldEvidence: map[string]ProviderFieldEvidence{"name": {Authority: 3}}, Complete: true},
			{Kind: "recording", ProviderKey: recordingKey, Name: "Track", Fields: json.RawMessage(`{"title":"Track"}`), RawSource: recordingRaw, Authority: 3, FieldEvidence: map[string]ProviderFieldEvidence{"title": {Authority: 3}}, Complete: true},
		},
	}
}
