package publication

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

// TagProjectionInput is the already-resolved metadata for one publication.
// Desired envelopes are scoped and resolved by the caller; this package does
// not perform fallback to source or provider metadata.
type TagProjectionInput struct {
	Release  ReleaseTags
	Position PositionTags
}

type ReleaseTags struct {
	Title       string
	Artist      []librarymetadata.ArtistCredit
	ID          *uuid.UUID
	GroupID     *uuid.UUID
	Desired     *librarymetadata.Envelope
	Compilation *bool
}

type PositionTags struct {
	Title          string
	Artist         []librarymetadata.ArtistCredit
	RecordingID    *uuid.UUID
	ReleaseTrackID *uuid.UUID
	DiscNumber     *int
	DiscTotal      *int
	TrackNumber    *int
	TrackTotal     *int
	Desired        *librarymetadata.Envelope
}

type TagFieldResult struct {
	Status string
	Reason string
}

// TagProjection reports per-field capability independently of tag intent.
// A projected value is not evidence that a writer/readback supports it.
type TagProjection struct {
	Tags   map[string][]string
	Fields map[string]TagFieldResult
}

// ProjectTags projects the approved core normalized tag mapping. Unsupported
// registered fields remain visible in Fields instead of being silently lost.
func ProjectTags(input TagProjectionInput) (TagProjection, error) {
	if !hasMeaningfulCredit(input.Release.Artist) || !hasMeaningfulCredit(input.Position.Artist) || strings.TrimSpace(input.Release.Title) == "" || strings.TrimSpace(input.Position.Title) == "" {
		return TagProjection{}, fmt.Errorf("publication tags require release artist, release title, and track artist and title")
	}
	if err := validateDesiredEnvelope(input.Release.Desired, librarymetadata.ScopeRelease); err != nil {
		return TagProjection{}, err
	}
	if err := validateDesiredEnvelope(input.Position.Desired, librarymetadata.ScopePosition); err != nil {
		return TagProjection{}, err
	}
	projection := TagProjection{Tags: make(map[string][]string), Fields: make(map[string]TagFieldResult)}
	setCredits(projection.Tags, projection.Fields, "artist_credit", "ARTIST", "ARTISTS", "MUSICBRAINZ_ARTISTID", input.Position.Artist)
	setCredits(projection.Tags, projection.Fields, "album_artist_credit", "ALBUMARTIST", "ALBUMARTISTS", "MUSICBRAINZ_ALBUMARTISTID", input.Release.Artist)
	projection.Tags["ALBUM"] = []string{input.Release.Title}
	projection.Tags["TITLE"] = []string{input.Position.Title}
	projection.Fields["release.title"] = TagFieldResult{Status: "represented"}
	projection.Fields["position.title"] = TagFieldResult{Status: "represented"}
	setID(projection.Tags, projection.Fields, "release_mbid", "MUSICBRAINZ_ALBUMID", input.Release.ID)
	setID(projection.Tags, projection.Fields, "release_group_mbid", "MUSICBRAINZ_RELEASEGROUPID", input.Release.GroupID)
	setID(projection.Tags, projection.Fields, "recording_mbid", "MUSICBRAINZ_TRACKID", input.Position.RecordingID)
	setID(projection.Tags, projection.Fields, "release_track_mbid", "MUSICBRAINZ_RELEASETRACKID", input.Position.ReleaseTrackID)
	setNumber(projection.Tags, projection.Fields, "disc_number", "DISCNUMBER", input.Position.DiscNumber)
	setNumber(projection.Tags, projection.Fields, "disc_total", "DISCTOTAL", input.Position.DiscTotal)
	setNumber(projection.Tags, projection.Fields, "track_number", "TRACKNUMBER", input.Position.TrackNumber)
	setNumber(projection.Tags, projection.Fields, "track_total", "TRACKTOTAL", input.Position.TrackTotal)
	if input.Release.Compilation != nil {
		value := "0"
		if *input.Release.Compilation {
			value = "1"
		}
		projection.Tags["COMPILATION"] = []string{value}
		projection.Fields["compilation"] = TagFieldResult{Status: "represented"}
	}
	applyDesired(projection.Tags, projection.Fields, librarymetadata.ScopeRelease, input.Release.Desired)
	applyDesired(projection.Tags, projection.Fields, librarymetadata.ScopePosition, input.Position.Desired)
	if !hasMeaningfulTag(projection.Tags["ALBUMARTIST"]) || !hasMeaningfulTag(projection.Tags["ARTIST"]) || !hasMeaningfulTag(projection.Tags["ALBUM"]) || !hasMeaningfulTag(projection.Tags["TITLE"]) {
		return TagProjection{}, fmt.Errorf("publication tags require release artist, release title, and track artist and title")
	}
	return projection, nil
}

func validateDesiredEnvelope(envelope *librarymetadata.Envelope, scope librarymetadata.Scope) error {
	if envelope == nil {
		return nil
	}
	if envelope.Scope != scope {
		return fmt.Errorf("local metadata scope %q does not match publication scope %q", envelope.Scope, scope)
	}
	return envelope.Validate()
}

func hasMeaningfulCredit(credits []librarymetadata.ArtistCredit) bool {
	for _, credit := range credits {
		if strings.TrimSpace(credit.ArtistName) != "" || strings.TrimSpace(credit.CreditName) != "" {
			return true
		}
	}
	return false
}

func hasMeaningfulTag(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func setCredits(tags map[string][]string, fields map[string]TagFieldResult, name, fullKey, listKey, mbidKey string, credits []librarymetadata.ArtistCredit) {
	if len(credits) == 0 {
		return
	}
	full := make([]string, 0, len(credits))
	individual := make([]string, 0, len(credits))
	ids := make([]string, 0, len(credits))
	completeIDs := true
	for _, credit := range credits {
		creditedName := strings.TrimSpace(credit.CreditName)
		if creditedName == "" {
			creditedName = credit.ArtistName
		}
		full = append(full, creditedName+credit.JoinPhrase)
		individual = append(individual, credit.ArtistName)
		if credit.MBID == nil || strings.TrimSpace(*credit.MBID) == "" {
			completeIDs = false
		} else {
			ids = append(ids, *credit.MBID)
		}
	}
	tags[fullKey] = []string{strings.Join(full, "")}
	tags[listKey] = individual
	fields[name] = TagFieldResult{Status: "represented"}
	if completeIDs {
		tags[mbidKey] = ids
		fields[name+".mbids"] = TagFieldResult{Status: "represented"}
	} else {
		fields[name+".mbids"] = TagFieldResult{Status: "unsupported", Reason: "artist MBIDs are incomplete; ordered per-artist alignment cannot be represented"}
	}
}

func setID(tags map[string][]string, fields map[string]TagFieldResult, field, key string, id *uuid.UUID) {
	if id == nil {
		return
	}
	tags[key] = []string{id.String()}
	fields[field] = TagFieldResult{Status: "represented"}
}

func setNumber(tags map[string][]string, fields map[string]TagFieldResult, field, key string, number *int) {
	if number == nil {
		return
	}
	tags[key] = []string{strconv.Itoa(*number)}
	fields[field] = TagFieldResult{Status: "represented"}
}

func applyDesired(tags map[string][]string, fields map[string]TagFieldResult, scope librarymetadata.Scope, envelope *librarymetadata.Envelope) {
	if envelope == nil {
		return
	}
	for name, value := range envelope.Desired {
		if value.State == librarymetadata.StateAbsent {
			continue
		}
		key, supported := normalizedTagKey(scope, name)
		if !supported {
			fields[string(scope)+"."+name] = TagFieldResult{Status: "unsupported", Reason: "no approved normalized TagLib mapping"}
			continue
		}
		if value.State == librarymetadata.StateExplicitEmpty {
			tags[key] = []string{}
			fields[string(scope)+"."+name] = TagFieldResult{Status: "explicit_empty"}
			continue
		}
		values, ok := normalizedTagValues(name, value.Value)
		if !ok {
			fields[string(scope)+"."+name] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
			continue
		}
		tags[key] = values
		fields[string(scope)+"."+name] = TagFieldResult{Status: "represented"}
	}
}

func normalizedTagKey(scope librarymetadata.Scope, field string) (string, bool) {
	if field == "title" {
		if scope == librarymetadata.ScopeRelease {
			return "ALBUM", true
		}
		return "TITLE", true
	}
	if field == "artist_credit" || field == "artists" {
		if scope == librarymetadata.ScopeRelease {
			if field == "artist_credit" {
				return "ALBUMARTIST", true
			}
			return "ALBUMARTISTS", true
		}
		if field == "artist_credit" {
			return "ARTIST", true
		}
		return "ARTISTS", true
	}
	keys := map[string]string{
		"title": "TITLE", "artist_credit": "ARTIST", "artists": "ARTISTS", "release_date": "DATE", "original_release_date": "ORIGINALDATE",
		"release_primary_type": "RELEASETYPE", "release_secondary_types": "RELEASETYPES", "release_version": "SUBTITLE", "release_mbid": "MUSICBRAINZ_ALBUMID",
		"release_group_mbid": "MUSICBRAINZ_RELEASEGROUPID", "recording_mbid": "MUSICBRAINZ_TRACKID", "release_track_mbid": "MUSICBRAINZ_RELEASETRACKID",
		"artist_mbid": "MUSICBRAINZ_ARTISTID", "album_artist_mbid": "MUSICBRAINZ_ALBUMARTISTID", "isrc": "ISRC", "label": "LABEL", "catalog_number": "CATALOGNUMBER",
		"compilation": "COMPILATION", "genre": "GENRE", "mood": "MOOD", "grouping": "GROUPING", "comment": "COMMENT", "bpm": "BPM",
		"copyright": "COPYRIGHT", "license": "LICENSE", "website": "WEBSITE", "language": "LANGUAGE", "media": "MEDIA", "disc_title": "DISCNAME",
		"track_number": "TRACKNUMBER", "track_total": "TRACKTOTAL", "disc_number": "DISCNUMBER", "disc_total": "DISCTOTAL",
	}
	if field == "artist_mbid" && scope == librarymetadata.ScopeRelease {
		return "MUSICBRAINZ_ALBUMARTISTID", true
	}
	key, ok := keys[field]
	return key, ok
}

func normalizedTagValues(field string, value any) ([]string, bool) {
	switch typed := value.(type) {
	case string:
		return []string{typed}, true
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			values = append(values, text)
		}
		return values, true
	case bool:
		if field == "compilation" {
			if typed {
				return []string{"1"}, true
			}
			return []string{"0"}, true
		}
	case int:
		return []string{strconv.Itoa(typed)}, true
	case int64:
		return []string{strconv.FormatInt(typed, 10)}, true
	case json.Number:
		return []string{typed.String()}, true
	case float64:
		return []string{strconv.FormatFloat(typed, 'f', -1, 64)}, true
	}
	return nil, false
}
