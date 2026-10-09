package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// NormalizeMusicBrainzReleaseSearch converts SDK projections to the persisted
// provider graph. The raw SDK entity, rather than a re-marshalled DTO, is the
// source of unknown fields and nested ordering.
func NormalizeMusicBrainzReleaseSearch(result musicbrainz.SearchResult) (persistence.ProviderGraph, error) {
	graph := persistence.ProviderGraph{}
	for _, entity := range result.Items {
		if err := normalizeRelease(entity.Raw, &graph, true, false, 2); err != nil {
			return persistence.ProviderGraph{}, err
		}
	}
	return graph, nil
}

// NormalizeMusicBrainzRecordingSearch normalizes search-hit recordings and
// their deliberately partial release summaries.
func NormalizeMusicBrainzRecordingSearch(result musicbrainz.SearchResult) (persistence.ProviderGraph, error) {
	graph := persistence.ProviderGraph{}
	for _, entity := range result.Items {
		if err := normalizeRecording(entity.Raw, &graph, 2); err != nil {
			return persistence.ProviderGraph{}, err
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(entity.Raw, &raw); err != nil {
			return persistence.ProviderGraph{}, fmt.Errorf("decode MusicBrainz recording: %w", err)
		}
		var releases []json.RawMessage
		if len(raw["releases"]) > 0 && string(raw["releases"]) != "null" {
			if err := json.Unmarshal(raw["releases"], &releases); err != nil {
				return persistence.ProviderGraph{}, fmt.Errorf("decode MusicBrainz recording releases: %w", err)
			}
		}
		for _, release := range releases {
			if err := normalizeRelease(release, &graph, false, false, 1); err != nil {
				return persistence.ProviderGraph{}, err
			}
		}
	}
	return graph, nil
}

// NormalizeMusicBrainzReleaseLookup marks only the requested top-level release
// complete. Nested release summaries never become complete by implication.
func NormalizeMusicBrainzReleaseLookup(result musicbrainz.LookupResult) (persistence.ProviderGraph, error) {
	var graph persistence.ProviderGraph
	if err := normalizeRelease(result.Entity.Raw, &graph, true, true, 3); err != nil {
		return persistence.ProviderGraph{}, err
	}
	graph.Complete = true
	return graph, nil
}

// NormalizeMusicBrainzRecordingLookup stores the recording and its release
// summaries as partial evidence only.
func NormalizeMusicBrainzRecordingLookup(result musicbrainz.LookupResult) (persistence.ProviderGraph, error) {
	var graph persistence.ProviderGraph
	if err := normalizeRecording(result.Entity.Raw, &graph, 3); err != nil {
		return persistence.ProviderGraph{}, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(result.Entity.Raw, &raw); err != nil {
		return persistence.ProviderGraph{}, fmt.Errorf("decode MusicBrainz recording: %w", err)
	}
	var releases []json.RawMessage
	if len(raw["releases"]) > 0 && string(raw["releases"]) != "null" {
		if err := json.Unmarshal(raw["releases"], &releases); err != nil {
			return persistence.ProviderGraph{}, fmt.Errorf("decode MusicBrainz recording releases: %w", err)
		}
	}
	for _, release := range releases {
		if err := normalizeRelease(release, &graph, false, false, 1); err != nil {
			return persistence.ProviderGraph{}, err
		}
	}
	return graph, nil
}

func normalizeRelease(data json.RawMessage, graph *persistence.ProviderGraph, withTracks, complete bool, authority int) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode MusicBrainz release: %w", err)
	}
	id, title := stringField(raw, "id"), stringField(raw, "title")
	if !validMBIDString(id) || title == "" {
		return fmt.Errorf("MusicBrainz release is missing a valid id or title")
	}
	graph.Releases = appendUniqueRelease(graph.Releases, persistence.ProviderRelease{
		ID:          uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:release:"+strings.ToLower(id))),
		ProviderKey: strings.ToLower(id), Title: title, RawSource: cloneJSON(data),
	})
	if err := appendSourceEntity(graph, "release", id, title, raw, authority, complete); err != nil {
		return err
	}
	if err := normalizeCredits(id, raw["artist-credit"], graph, authority); err != nil {
		return err
	}
	if err := setReleaseRelations(graph, id, raw, authority); err != nil {
		return err
	}
	if !withTracks {
		return nil
	}
	var media []json.RawMessage
	if len(raw["media"]) == 0 || string(raw["media"]) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw["media"], &media); err != nil {
		return fmt.Errorf("decode MusicBrainz release media: %w", err)
	}
	for mediumIndex, medium := range media {
		var mediumRaw map[string]json.RawMessage
		if err := json.Unmarshal(medium, &mediumRaw); err != nil {
			return fmt.Errorf("decode MusicBrainz medium: %w", err)
		}
		var tracks []json.RawMessage
		if len(mediumRaw["tracks"]) == 0 || string(mediumRaw["tracks"]) == "null" {
			continue
		}
		if err := json.Unmarshal(mediumRaw["tracks"], &tracks); err != nil {
			return fmt.Errorf("decode MusicBrainz tracks: %w", err)
		}
		for trackIndex, track := range tracks {
			var trackRaw map[string]json.RawMessage
			if err := json.Unmarshal(track, &trackRaw); err != nil {
				return fmt.Errorf("decode MusicBrainz track: %w", err)
			}
			recordingRaw := trackRaw["recording"]
			var recording map[string]json.RawMessage
			if len(recordingRaw) == 0 || string(recordingRaw) == "null" || json.Unmarshal(recordingRaw, &recording) != nil {
				return fmt.Errorf("MusicBrainz track is missing recording")
			}
			if err := normalizeRecording(recordingRaw, graph, authority); err != nil {
				return err
			}
			recordingID := stringField(recording, "id")
			displayed := stringField(trackRaw, "number")
			if displayed == "" {
				displayed = strconv.Itoa(trackIndex + 1)
			}
			trackMBID := stringField(trackRaw, "id")
			trackKey := fmt.Sprintf("musicbrainz:track:%s:%d:%d:%s", strings.ToLower(id), mediumIndex, trackIndex, strings.ToLower(recordingID))
			if validMBIDString(trackMBID) {
				trackKey = "musicbrainz:track:" + strings.ToLower(trackMBID)
			}
			trackID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(trackKey))
			normalizedTrack := persistence.ProviderReleaseTrack{
				ID: trackID, ReleaseID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:release:"+strings.ToLower(id))),
				RecordingID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:recording:"+strings.ToLower(recordingID))),
				Medium:      mediumIndex, Position: trackIndex, DisplayedNumber: displayed,
			}
			foundTrack := false
			for index := range graph.Tracks {
				if graph.Tracks[index].ID == trackID {
					graph.Tracks[index] = normalizedTrack
					foundTrack = true
					break
				}
			}
			if !foundTrack {
				graph.Tracks = append(graph.Tracks, normalizedTrack)
			}
		}
	}
	return setReleaseRelations(graph, id, raw, authority)
}

func normalizeRecording(data json.RawMessage, graph *persistence.ProviderGraph, authority int) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode MusicBrainz recording: %w", err)
	}
	id, title := stringField(raw, "id"), stringField(raw, "title")
	if !validMBIDString(id) || title == "" {
		return fmt.Errorf("MusicBrainz recording is missing a valid id or title")
	}
	graph.Recordings = appendUniqueRecording(graph.Recordings, persistence.ProviderRecording{
		ID:          uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:recording:"+strings.ToLower(id))),
		ProviderKey: strings.ToLower(id), Title: title, RawSource: cloneJSON(data),
	})
	if err := appendSourceEntity(graph, "recording", id, title, raw, authority, false); err != nil {
		return err
	}
	return normalizeArtistEvidence(raw["artist-credit"], graph, authority)
}

func appendSourceEntity(graph *persistence.ProviderGraph, kind, id, name string, raw map[string]json.RawMessage, authority int, complete bool) error {
	fields := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		if key == "id" {
			continue
		}
		fields[key] = cloneJSON(value)
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("encode MusicBrainz %s fields: %w", kind, err)
	}
	rawBytes, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("encode MusicBrainz %s source: %w", kind, err)
	}
	key := strings.ToLower(id)
	for index := range graph.Entities {
		entity := &graph.Entities[index]
		if entity.Kind != kind || entity.ProviderKey != key {
			continue
		}
		var previous map[string]json.RawMessage
		if err := json.Unmarshal(entity.Fields, &previous); err != nil {
			return err
		}
		if entity.FieldEvidence == nil {
			entity.FieldEvidence = make(map[string]persistence.ProviderFieldEvidence)
		}
		for field, value := range fields {
			if fieldEvidenceWins(entity.FieldEvidence[field], authority) {
				previous[field] = value
				entity.FieldEvidence[field] = persistence.ProviderFieldEvidence{Authority: authority}
			}
		}
		entity.Fields, err = json.Marshal(previous)
		if err != nil {
			return err
		}
		if fieldEvidenceWins(entity.FieldEvidence["name"], authority) {
			entity.Name = name
			entity.FieldEvidence["name"] = persistence.ProviderFieldEvidence{Authority: authority}
		}
		if authority > entity.Authority {
			entity.Authority = authority
			entity.RawSource = cloneJSON(rawBytes)
		}
		entity.Complete = entity.Complete || complete
		return nil
	}
	evidence := make(map[string]persistence.ProviderFieldEvidence, len(fields)+1)
	for field := range fields {
		evidence[field] = persistence.ProviderFieldEvidence{Authority: authority}
	}
	evidence["name"] = persistence.ProviderFieldEvidence{Authority: authority}
	graph.Entities = append(graph.Entities, persistence.ProviderSourceEntity{
		Kind: kind, ProviderKey: key, Name: name, Fields: encoded, RawSource: rawBytes, Authority: authority, FieldEvidence: evidence, Complete: complete,
	})
	return nil
}

func fieldEvidenceWins(previous persistence.ProviderFieldEvidence, authority int) bool {
	return authority >= previous.Authority
}

func normalizeCredits(releaseID string, rawCredits json.RawMessage, graph *persistence.ProviderGraph, authority int) error {
	if len(rawCredits) == 0 || string(rawCredits) == "null" {
		return nil
	}
	var credits []json.RawMessage
	if err := json.Unmarshal(rawCredits, &credits); err != nil {
		return fmt.Errorf("decode MusicBrainz artist credits: %w", err)
	}
	for position, item := range credits {
		var credit map[string]json.RawMessage
		if err := json.Unmarshal(item, &credit); err != nil {
			return fmt.Errorf("decode MusicBrainz artist credit: %w", err)
		}
		var artist map[string]json.RawMessage
		if len(credit["artist"]) == 0 || json.Unmarshal(credit["artist"], &artist) != nil {
			continue // Preserve non-artist credit text in release RawSource.
		}
		id := stringField(artist, "id")
		name := stringField(artist, "name")
		if !validMBIDString(id) || name == "" {
			continue
		}
		artistKey := strings.ToLower(id)
		artistID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:artist:"+artistKey))
		graph.Artists = appendUniqueArtist(graph.Artists, persistence.ProviderArtist{
			ID: artistID, ProviderKey: artistKey, Name: name, RawSource: cloneJSON(credit["artist"]),
		})
		if err := appendSourceEntity(graph, "artist", id, name, artist, authority, false); err != nil {
			return err
		}
		normalizedCredit := persistence.ProviderReleaseArtistCredit{
			ReleaseID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:release:"+strings.ToLower(releaseID))),
			Position:  position, ArtistID: artistID, NameJoinPhrase: stringField(credit, "joinphrase"),
		}
		foundCredit := false
		for index := range graph.Credits {
			if graph.Credits[index].ReleaseID == normalizedCredit.ReleaseID && graph.Credits[index].Position == position {
				graph.Credits[index] = normalizedCredit
				foundCredit = true
				break
			}
		}
		if !foundCredit {
			graph.Credits = append(graph.Credits, normalizedCredit)
		}
	}
	return nil
}

func normalizeArtistEvidence(rawCredits json.RawMessage, graph *persistence.ProviderGraph, authority int) error {
	if len(rawCredits) == 0 || string(rawCredits) == "null" {
		return nil
	}
	var credits []json.RawMessage
	if err := json.Unmarshal(rawCredits, &credits); err != nil {
		return fmt.Errorf("decode MusicBrainz artist credits: %w", err)
	}
	for _, item := range credits {
		var credit map[string]json.RawMessage
		if err := json.Unmarshal(item, &credit); err != nil {
			return fmt.Errorf("decode MusicBrainz artist credit: %w", err)
		}
		var artist map[string]json.RawMessage
		if len(credit["artist"]) == 0 || json.Unmarshal(credit["artist"], &artist) != nil {
			continue
		}
		id, name := stringField(artist, "id"), stringField(artist, "name")
		if !validMBIDString(id) || name == "" {
			continue
		}
		artistKey := strings.ToLower(id)
		artistID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:artist:"+artistKey))
		graph.Artists = appendUniqueArtist(graph.Artists, persistence.ProviderArtist{
			ID: artistID, ProviderKey: artistKey, Name: name, RawSource: cloneJSON(credit["artist"]),
		})
		if err := appendSourceEntity(graph, "artist", id, name, artist, authority, false); err != nil {
			return err
		}
	}
	return nil
}

func setReleaseRelations(graph *persistence.ProviderGraph, releaseID string, raw map[string]json.RawMessage, authority int) error {
	providerKey := strings.ToLower(releaseID)
	var target *persistence.ProviderSourceEntity
	for index := range graph.Entities {
		if graph.Entities[index].Kind == "release" && graph.Entities[index].ProviderKey == providerKey {
			target = &graph.Entities[index]
			break
		}
	}
	if target == nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(target.Fields, &fields); err != nil {
		return err
	}
	releaseUUID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:release:"+providerKey))
	if _, present := raw["artist-credit"]; present && string(raw["artist-credit"]) != "null" {
		credits := make([]map[string]any, 0)
		for _, credit := range graph.Credits {
			if credit.ReleaseID == releaseUUID {
				credits = append(credits, map[string]any{"position": credit.Position, "artist_id": credit.ArtistID, "join_phrase": credit.NameJoinPhrase})
			}
		}
		encoded, err := json.Marshal(credits)
		if err != nil {
			return err
		}
		if fieldEvidenceWins(target.FieldEvidence["normalized_artist_credits"], authority) {
			fields["normalized_artist_credits"] = encoded
			target.FieldEvidence["normalized_artist_credits"] = persistence.ProviderFieldEvidence{Authority: authority}
		}
	}
	if _, present := raw["media"]; present && string(raw["media"]) != "null" {
		var media []json.RawMessage
		if err := json.Unmarshal(raw["media"], &media); err != nil {
			return fmt.Errorf("decode MusicBrainz release media: %w", err)
		}
		tracksComplete := len(media) > 0
		for _, medium := range media {
			var mediumRaw map[string]json.RawMessage
			if err := json.Unmarshal(medium, &mediumRaw); err != nil {
				return fmt.Errorf("decode MusicBrainz medium: %w", err)
			}
			if len(mediumRaw["tracks"]) == 0 || string(mediumRaw["tracks"]) == "null" {
				tracksComplete = false
			}
		}
		if tracksComplete {
			tracks := make([]map[string]any, 0)
			for _, track := range graph.Tracks {
				if track.ReleaseID == releaseUUID {
					tracks = append(tracks, map[string]any{"id": track.ID, "recording_id": track.RecordingID, "medium": track.Medium, "position": track.Position, "displayed_number": track.DisplayedNumber})
				}
			}
			if len(tracks) > 0 || target.Complete {
				encoded, err := json.Marshal(tracks)
				if err != nil {
					return err
				}
				if fieldEvidenceWins(target.FieldEvidence["normalized_tracks"], authority) {
					fields["normalized_tracks"] = encoded
					target.FieldEvidence["normalized_tracks"] = persistence.ProviderFieldEvidence{Authority: authority}
				}
			}
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	target.Fields = encoded
	return nil
}

func stringField(raw map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(raw[key], &value)
	return value
}

func validMBIDString(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && strings.EqualFold(value, id.String())
}

func cloneJSON(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func appendUniqueArtist(values []persistence.ProviderArtist, value persistence.ProviderArtist) []persistence.ProviderArtist {
	for _, existing := range values {
		if existing.ProviderKey == value.ProviderKey {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueRelease(values []persistence.ProviderRelease, value persistence.ProviderRelease) []persistence.ProviderRelease {
	for _, existing := range values {
		if existing.ProviderKey == value.ProviderKey {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueRecording(values []persistence.ProviderRecording, value persistence.ProviderRecording) []persistence.ProviderRecording {
	for _, existing := range values {
		if existing.ProviderKey == value.ProviderKey {
			return values
		}
	}
	return append(values, value)
}
