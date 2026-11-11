package publication

import (
	"encoding/json"
	"fmt"
	"sort"
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
	Title                 string
	Artist                []librarymetadata.ArtistCredit
	ArtistCreditsResolved bool
	ArtistCreditTerminal  bool
	ID                    *uuid.UUID
	GroupID               *uuid.UUID
	Desired               *librarymetadata.Envelope
	Compilation           *bool
}

type PositionTags struct {
	Title                 string
	Artist                []librarymetadata.ArtistCredit
	ArtistCreditsResolved bool
	ArtistCreditTerminal  bool
	RecordingID           *uuid.UUID
	ReleaseTrackID        *uuid.UUID
	DiscNumber            *int
	DiscTotal             *int
	TrackNumber           *int
	TrackTotal            *int
	Desired               *librarymetadata.Envelope
}

type TagFieldResult struct {
	Status string
	Reason string
}

// TagProjection reports per-field capability independently of tag intent.
// A projected value is not evidence that a writer/readback supports it.
type TagProjection struct {
	Tags    map[string][]string
	Fields  map[string]TagFieldResult
	Notices []string
}

// ProjectTags projects the approved core normalized tag mapping. Unsupported
// registered fields remain visible in Fields instead of being silently lost.
func ProjectTags(input TagProjectionInput) (TagProjection, error) {
	if err := validateDesiredEnvelope(input.Release.Desired, librarymetadata.ScopeRelease); err != nil {
		return TagProjection{}, err
	}
	if err := validateDesiredEnvelope(input.Position.Desired, librarymetadata.ScopePosition); err != nil {
		return TagProjection{}, err
	}
	if (!hasMeaningfulCredit(input.Release.Artist) && !input.Release.ArtistCreditTerminal) || (!hasMeaningfulCredit(input.Position.Artist) && !input.Position.ArtistCreditTerminal) || strings.TrimSpace(input.Release.Title) == "" || strings.TrimSpace(input.Position.Title) == "" {
		return TagProjection{}, fmt.Errorf("publication tags require release artist, release title, and track artist and title")
	}
	projection := TagProjection{Tags: make(map[string][]string), Fields: make(map[string]TagFieldResult)}
	if input.Position.ArtistCreditsResolved || !hasDesiredCreditField(input.Position.Desired) {
		projection.Notices = append(projection.Notices, setCredits(projection.Tags, projection.Fields, "artist_credit", "ARTIST", "ARTISTS", "MUSICBRAINZ_ARTISTID", "ARTISTSORT", "ARTISTSSORT", input.Position.Artist)...)
	}
	if input.Release.ArtistCreditsResolved || !hasDesiredCreditField(input.Release.Desired) {
		projection.Notices = append(projection.Notices, setCredits(projection.Tags, projection.Fields, "album_artist_credit", "ALBUMARTIST", "ALBUMARTISTS", "MUSICBRAINZ_ALBUMARTISTID", "ALBUMARTISTSORT", "ALBUMARTISTSSORT", input.Release.Artist)...)
	}
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
	projection.Notices = append(projection.Notices, applyDesired(projection.Tags, projection.Fields, librarymetadata.ScopeRelease, input.Release.Desired, input.Release.ArtistCreditsResolved)...)
	projection.Notices = append(projection.Notices, applyDesired(projection.Tags, projection.Fields, librarymetadata.ScopePosition, input.Position.Desired, input.Position.ArtistCreditsResolved)...)
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

func setCredits(tags map[string][]string, fields map[string]TagFieldResult, name, fullKey, listKey, mbidKey, sortKey, sortListKey string, credits []librarymetadata.ArtistCredit) []string {
	if len(credits) == 0 {
		for _, key := range []string{fullKey, listKey, mbidKey, sortKey, sortListKey} {
			tags[key] = []string{}
		}
		fields[name] = TagFieldResult{Status: "explicit_empty"}
		return nil
	}
	full := make([]string, 0, len(credits))
	individual, nameCollisions := projectedArtistNames(credits)
	ids := make([]string, 0, len(credits))
	sortNames := make([]string, 0, len(credits))
	completeIDs := true
	for _, credit := range credits {
		full = append(full, credit.CreditName+credit.JoinPhrase)
		sortName := credit.SortName
		if sortName == "" {
			sortName = credit.ArtistName
		}
		sortNames = append(sortNames, sortName)
		if credit.MBID == nil || strings.TrimSpace(*credit.MBID) == "" {
			completeIDs = false
		} else {
			ids = append(ids, *credit.MBID)
		}
	}
	tags[fullKey] = []string{strings.Join(full, "")}
	fields[name] = TagFieldResult{Status: "represented"}
	var notices []string
	if nameCollisions {
		tags[listKey] = []string{}
		tags[mbidKey] = []string{}
		fields[name] = TagFieldResult{Status: "unsupported", Reason: "artist names remain ambiguous after disambiguation; ordered artist tags were omitted"}
		notices = append(notices, "artist names remain ambiguous after disambiguation; ordered artist tags were omitted")
	} else if !completeIDs {
		tags[listKey] = []string{}
		tags[mbidKey] = []string{}
		fields[name] = TagFieldResult{Status: "unsupported", Reason: "artist MBIDs are incomplete; ordered artist tags were omitted"}
		fields[name+".mbids"] = TagFieldResult{Status: "unsupported", Reason: "artist MBIDs are incomplete; ordered per-artist alignment cannot be represented"}
		notices = append(notices, "artist MBIDs are incomplete; ordered artist tags were omitted")
	} else {
		tags[listKey] = individual
		tags[mbidKey] = ids
		fields[name+".mbids"] = TagFieldResult{Status: "represented"}
	}
	if hasDuplicateValuesAcrossArtists(sortNames, credits) {
		tags[sortKey] = []string{}
		tags[sortListKey] = []string{}
		fields[name+".sort_names"] = TagFieldResult{Status: "unsupported", Reason: "artist sort names collide; ordered sort tags were omitted"}
		notices = append(notices, "artist sort names collide; ordered sort tags were omitted")
	} else {
		tags[sortKey] = append([]string(nil), sortNames...)
		tags[sortListKey] = append([]string(nil), sortNames...)
		fields[name+".sort_names"] = TagFieldResult{Status: "represented"}
	}
	return notices
}

func hasDesiredCreditField(envelope *librarymetadata.Envelope) bool {
	if envelope == nil {
		return false
	}
	_, exists := envelope.Desired["artist_credit"]
	return exists
}

func hasMeaningfulCredit(credits []librarymetadata.ArtistCredit) bool {
	for _, credit := range credits {
		if strings.TrimSpace(credit.ArtistName) != "" || strings.TrimSpace(credit.CreditName) != "" {
			return true
		}
	}
	return false
}

func projectedArtistNames(credits []librarymetadata.ArtistCredit) ([]string, bool) {
	names := make([]string, len(credits))
	for i, credit := range credits {
		names[i] = credit.ArtistName
	}
	for i, credit := range credits {
		for j := 0; j < i; j++ {
			if credits[j].ArtistName != credit.ArtistName || sameArtist(credits[i], credits[j]) {
				continue
			}
			names[i] = disambiguatedArtistName(credit)
			names[j] = disambiguatedArtistName(credits[j])
		}
	}
	return names, hasDuplicateValuesAcrossArtists(names, credits)
}

func disambiguatedArtistName(credit librarymetadata.ArtistCredit) string {
	if credit.Disambiguation == "" {
		return credit.ArtistName
	}
	return credit.ArtistName + " (" + credit.Disambiguation + ")"
}

func sameArtist(left, right librarymetadata.ArtistCredit) bool {
	if left.MBID != nil && right.MBID != nil {
		return *left.MBID == *right.MBID
	}
	return left.ArtistID != "" && left.ArtistID == right.ArtistID
}

func hasDuplicateValuesAcrossArtists(values []string, credits []librarymetadata.ArtistCredit) bool {
	for i, value := range values {
		for j := 0; j < i; j++ {
			if values[j] == value && !sameArtist(credits[i], credits[j]) {
				return true
			}
		}
	}
	return false
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

func applyDesired(tags map[string][]string, fields map[string]TagFieldResult, scope librarymetadata.Scope, envelope *librarymetadata.Envelope, artistCreditsResolved bool) []string {
	if envelope == nil {
		return nil
	}
	var notices []string
	names := make([]string, 0, len(envelope.Desired))
	for name := range envelope.Desired {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if artistCreditsResolved && isArtistCreditProjectionField(name) {
			continue
		}
		value := envelope.Desired[name]
		if value.State == librarymetadata.StateAbsent {
			continue
		}
		// Artist sort tags are derived from artist credits. Stored legacy
		// independent sort lists remain decodable but must not override the
		// credit-derived values.
		if name == "artist_sort_names" || name == "album_artist_sort_names" {
			continue
		}
		keys, supported := normalizedTagKeys(scope, name)
		if !supported {
			fields[string(scope)+"."+name] = TagFieldResult{Status: "unsupported", Reason: "no approved normalized TagLib mapping"}
			continue
		}
		if value.State == librarymetadata.StateExplicitEmpty {
			for _, key := range keys {
				tags[key] = []string{}
			}
			delete(fields, string(scope)+"."+name+".mbids")
			delete(fields, string(scope)+"."+name+".sort_names")
			fields[string(scope)+"."+name] = TagFieldResult{Status: "explicit_empty"}
			continue
		}
		if name == "movement" || name == "works" || name == "contributors" || name == "content_advisory" {
			projectStructuredDesired(tags, fields, scope, name, value.Value)
			continue
		}
		if name == "artist_credit" {
			encoded, err := json.Marshal(value.Value)
			if err != nil {
				fields[string(scope)+"."+name] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
				continue
			}
			var credits []librarymetadata.ArtistCredit
			if err := json.Unmarshal(encoded, &credits); err != nil {
				fields[string(scope)+"."+name] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
				continue
			}
			prefix := "artist"
			full, list, mbids, sortOne, sortList := "ARTIST", "ARTISTS", "MUSICBRAINZ_ARTISTID", "ARTISTSORT", "ARTISTSSORT"
			if scope == librarymetadata.ScopeRelease {
				prefix, full, list, mbids, sortOne, sortList = "album_artist_credit", "ALBUMARTIST", "ALBUMARTISTS", "MUSICBRAINZ_ALBUMARTISTID", "ALBUMARTISTSORT", "ALBUMARTISTSSORT"
			}
			notices = append(notices, setCredits(tags, fields, prefix, full, list, mbids, sortOne, sortList, credits)...)
			if len(credits) == 0 {
				for _, key := range []string{full, list, mbids, sortOne, sortList} {
					tags[key] = []string{}
				}
				fields[string(scope)+"."+name] = TagFieldResult{Status: "represented"}
			}
			continue
		}
		if name == "genre" {
			values, ok := genreTagValues(value.Value)
			if !ok {
				fields[string(scope)+"."+name] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
				continue
			}
			for _, key := range keys {
				tags[key] = values
			}
			fields[string(scope)+"."+name] = TagFieldResult{Status: "represented"}
			continue
		}
		values, ok := normalizedTagValues(name, value.Value)
		if !ok {
			fields[string(scope)+"."+name] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
			continue
		}
		for _, key := range keys {
			tags[key] = values
		}
		fields[string(scope)+"."+name] = TagFieldResult{Status: "represented"}
	}
	return notices
}

func isArtistCreditProjectionField(name string) bool {
	switch name {
	case "artist_credit", "artists", "artist_mbid", "album_artist_mbid", "artist_sort_names", "album_artist_sort_names":
		return true
	default:
		return false
	}
}

func genreTagValues(value any) ([]string, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var genres []librarymetadata.GenreReference
	if err := json.Unmarshal(encoded, &genres); err != nil {
		return nil, false
	}
	values := make([]string, 0, len(genres))
	for _, genre := range genres {
		if strings.TrimSpace(genre.Name) == "" {
			return nil, false
		}
		values = append(values, genre.Name)
	}
	return values, true
}

func projectStructuredDesired(tags map[string][]string, fields map[string]TagFieldResult, scope librarymetadata.Scope, field string, value any) {
	fieldName := string(scope) + "." + field
	if field == "contributors" {
		projectContributors(tags, fields, fieldName, value)
		return
	}
	if field == "content_advisory" {
		advisory, ok := value.(string)
		if !ok || (advisory != "explicit" && advisory != "clean") {
			fields[fieldName] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
			return
		}
		value := "1"
		if advisory == "clean" {
			value = "2"
		}
		// This is the portable ITUNESADVISORY property, not M4A's native
		// rtng atom; normalized TagLib ReadTags does not observe rtng.
		tags["ITUNESADVISORY"] = []string{value}
		fields[fieldName] = TagFieldResult{Status: "represented"}
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		fields[fieldName] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
		return
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	var normalized any
	if decoder.Decode(&normalized) != nil {
		fields[fieldName] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
		return
	}
	if field == "movement" {
		movement, ok := normalized.(map[string]any)
		name, nameOK := movement["name"].(string)
		number, numberOK := movement["number"].(json.Number)
		count, countOK := movement["count"].(json.Number)
		if !ok || !nameOK || !numberOK || !countOK {
			fields[fieldName] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
			return
		}
		tags["MOVEMENTNAME"] = []string{name}
		tags["MOVEMENTNUMBER"] = []string{number.String()}
		tags["MOVEMENTCOUNT"] = []string{count.String()}
		fields[fieldName] = TagFieldResult{Status: "represented"}
		return
	}
	works, ok := normalized.([]any)
	if !ok {
		fields[fieldName] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
		return
	}
	names := make([]string, 0, len(works))
	ids := make([]string, 0, len(works))
	hasID, missingID := false, false
	for _, entry := range works {
		work, ok := entry.(map[string]any)
		name, nameOK := work["name"].(string)
		if !ok || !nameOK {
			fields[fieldName] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
			return
		}
		names = append(names, name)
		if rawID, exists := work["mbid"]; exists && rawID != nil {
			id, ok := rawID.(string)
			if !ok {
				fields[fieldName] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
				return
			}
			hasID = true
			ids = append(ids, id)
		} else {
			missingID = true
		}
	}
	tags["WORK"] = names
	fields[fieldName] = TagFieldResult{Status: "represented"}
	if hasID && missingID {
		fields[fieldName+".mbids"] = TagFieldResult{Status: "unsupported", Reason: "work MBIDs are incomplete; ordered per-work alignment cannot be represented"}
	} else if hasID {
		tags["MUSICBRAINZ_WORKID"] = ids
		fields[fieldName+".mbids"] = TagFieldResult{Status: "represented"}
	} else {
		delete(tags, "MUSICBRAINZ_WORKID")
		delete(fields, fieldName+".mbids")
	}
}

func projectContributors(tags map[string][]string, fields map[string]TagFieldResult, fieldName string, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		fields[fieldName] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
		return
	}
	var contributors []librarymetadata.Contributor
	if err := json.Unmarshal(encoded, &contributors); err != nil {
		fields[fieldName] = TagFieldResult{Status: "unsupported", Reason: "value cannot be represented by the approved normalized mapping"}
		return
	}
	// Map only these standard role properties; no instrument taxonomy is
	// implied, and unknown roles remain unsupported rather than becoming PERFORMER.
	roleKeys := map[string]string{
		"composer": "COMPOSER", "lyricist": "LYRICIST", "conductor": "CONDUCTOR", "arranger": "ARRANGER",
		"engineer": "ENGINEER", "producer": "PRODUCER", "remixer": "REMIXER", "djmixer": "DJMIXER", "mixer": "MIXER",
	}
	unsupported := false
	mbidsIncomplete := false
	mbidsPresent := false
	for _, contributor := range contributors {
		if contributor.MBID == nil || strings.TrimSpace(*contributor.MBID) == "" {
			mbidsIncomplete = true
		} else {
			mbidsPresent = true
		}
		role := strings.ToLower(strings.TrimSpace(contributor.Role))
		key, ok := roleKeys[role]
		if !ok {
			unsupported = true
			continue
		}
		tags[key] = append(tags[key], contributor.Name)
	}
	if unsupported {
		fields[fieldName] = TagFieldResult{Status: "unsupported", Reason: "one or more contributor roles have no approved normalized TagLib mapping"}
	} else {
		fields[fieldName] = TagFieldResult{Status: "represented"}
	}
	if mbidsIncomplete {
		fields[fieldName+".mbids"] = TagFieldResult{Status: "unsupported", Reason: "contributor MBIDs are incomplete; contributor names are represented without MBIDs"}
	} else if mbidsPresent {
		fields[fieldName+".mbids"] = TagFieldResult{Status: "unsupported", Reason: "contributor MBIDs have no approved normalized TagLib mapping"}
	} else {
		delete(fields, fieldName+".mbids")
	}
}

func normalizedTagKeys(scope librarymetadata.Scope, field string) ([]string, bool) {
	if field == "artist_credit" {
		if scope == librarymetadata.ScopeRelease {
			return []string{"ALBUMARTIST", "ALBUMARTISTS", "MUSICBRAINZ_ALBUMARTISTID", "ALBUMARTISTSORT", "ALBUMARTISTSSORT"}, true
		}
		if scope == librarymetadata.ScopePosition {
			return []string{"ARTIST", "ARTISTS", "MUSICBRAINZ_ARTISTID", "ARTISTSORT", "ARTISTSSORT"}, true
		}
	}
	switch field {
	case "sort_title":
		if scope == librarymetadata.ScopeRelease {
			return []string{"ALBUMSORT"}, true
		}
		if scope == librarymetadata.ScopePosition {
			return []string{"TITLESORT"}, true
		}
	case "barcode":
		if scope == librarymetadata.ScopeRelease {
			return []string{"BARCODE"}, true
		}
	case "release_country":
		if scope == librarymetadata.ScopeRelease {
			return []string{"RELEASECOUNTRY"}, true
		}
	case "release_status":
		if scope == librarymetadata.ScopeRelease {
			return []string{"RELEASESTATUS"}, true
		}
	case "movement":
		if scope == librarymetadata.ScopePosition {
			return []string{"MOVEMENTNAME", "MOVEMENTNUMBER", "MOVEMENTCOUNT"}, true
		}
	case "works":
		if scope == librarymetadata.ScopePosition {
			return []string{"WORK", "MUSICBRAINZ_WORKID"}, true
		}
	case "contributors":
		if scope == librarymetadata.ScopePosition {
			return []string{"COMPOSER", "LYRICIST", "CONDUCTOR", "ARRANGER", "ENGINEER", "PRODUCER", "REMIXER", "DJMIXER", "MIXER"}, true
		}
	case "content_advisory":
		return []string{"ITUNESADVISORY"}, true
	}
	key, ok := normalizedTagKey(scope, field)
	if !ok {
		return nil, false
	}
	return []string{key}, true
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
		"title": "TITLE", "artist_credit": "ARTIST", "artists": "ARTISTS", "release_date": "RELEASEDATE", "recording_date": "DATE", "original_release_date": "ORIGINALDATE",
		"release_primary_type": "RELEASETYPE", "release_secondary_types": "RELEASETYPES", "release_version": "ALBUMVERSION", "subtitle": "SUBTITLE", "release_mbid": "MUSICBRAINZ_ALBUMID",
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
		values := make([]string, len(typed))
		copy(values, typed)
		return values, true
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
