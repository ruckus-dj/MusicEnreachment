package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/anyascii/go"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// TypedFallbackQueryPlan is an immutable, finite sequence of typed provider
// searches. It is shared by planning and persistence so durable plans do not
// depend on the service package.
type TypedFallbackQueryPlan struct {
	SchemaVersion int                       `json:"schema_version"`
	Stages        []TypedFallbackQueryStage `json:"stages"`
}

type TypedFallbackQueryStage struct {
	Kind   string `json:"kind"`
	Query  string `json:"query"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// PlanFallbackQueries derives the approved finite fallback plan from selected
// metadata captures. The bool reports whether any file has usable artist
// metadata under the album-artist-first source semantics.
func PlanFallbackQueries(tagSets []map[string][]string) (TypedFallbackQueryPlan, bool) {
	releaseTerms, recordingTerms := make([]string, 0), make([]string, 0)
	artistBearing := false
	for _, tags := range tagSets {
		albumArtist, present := fallbackTagValues(tags, "ALBUMARTIST", "ALBUM ARTIST", "ALBUM_ARTIST")
		artists := albumArtist
		if !present {
			artists, _ = fallbackTagValues(tags, "ARTIST")
		}
		for _, artist := range artists {
			if fallbackHasText(artist) {
				artistBearing = true
			}
		}
		album, _ := fallbackTagValues(tags, "ALBUM", "ALBUMTITLE", "ALBUM TITLE")
		track, _ := fallbackTagValues(tags, "TITLE", "TRACK", "TRACKTITLE", "TRACK TITLE")
		releaseTerms = appendFallbackQueryTerms(releaseTerms, album)
		recordingTerms = appendFallbackQueryTerms(recordingTerms, track)
	}
	releaseTerms = fallbackUnique(releaseTerms)
	recordingTerms = fallbackUnique(recordingTerms)
	stages := make([]TypedFallbackQueryStage, 0, 3)
	switch {
	case len(releaseTerms) > 0 && len(recordingTerms) > 0:
		albumQuery := fallbackFieldQuery("release", releaseTerms)
		trackQuery := fallbackFieldQuery("recording", recordingTerms)
		stages = append(stages,
			TypedFallbackQueryStage{Kind: "album_track", Query: "(" + albumQuery + ") AND (" + trackQuery + ")", Limit: 10},
			TypedFallbackQueryStage{Kind: "album", Query: albumQuery, Limit: 10},
			TypedFallbackQueryStage{Kind: "track", Query: trackQuery, Limit: 10},
		)
	case len(releaseTerms) > 0:
		stages = append(stages, TypedFallbackQueryStage{Kind: "album", Query: fallbackFieldQuery("release", releaseTerms), Limit: 10})
	case len(recordingTerms) > 0:
		stages = append(stages, TypedFallbackQueryStage{Kind: "track", Query: fallbackFieldQuery("recording", recordingTerms), Limit: 10})
	}
	return TypedFallbackQueryPlan{SchemaVersion: 1, Stages: stages}, artistBearing
}

func fallbackTagValues(tags map[string][]string, aliases ...string) ([]string, bool) {
	lookup := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		lookup[strings.ToUpper(strings.TrimSpace(alias))] = struct{}{}
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0)
	present := false
	for _, key := range keys {
		if _, ok := lookup[strings.ToUpper(strings.TrimSpace(key))]; !ok {
			continue
		}
		present = true
		values = append(values, tags[key]...)
	}
	return values, present
}

func appendFallbackQueryTerms(terms, values []string) []string {
	for _, value := range values {
		if !fallbackHasText(value) {
			continue
		}
		canonical := fallbackCanonicalText(value)
		terms = append(terms, value, canonical, anyascii.Transliterate(value), anyascii.Transliterate(canonical))
	}
	return terms
}

func fallbackFieldQuery(field string, values []string) string {
	terms := make([]string, 0, len(values))
	for _, value := range values {
		if fallbackHasText(value) {
			terms = append(terms, field+":\""+fallbackEscapePhrase(value)+"\"")
		}
	}
	if len(terms) == 1 {
		return terms[0]
	}
	return "(" + strings.Join(terms, " OR ") + ")"
}

func fallbackUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func fallbackCanonicalText(value string) string {
	value = norm.NFC.String(cases.Fold().String(norm.NFC.String(value)))
	var canonical strings.Builder
	canonical.Grow(len(value))
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			canonical.WriteRune(character)
		} else {
			canonical.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(canonical.String()), " ")
}

func fallbackHasText(value string) bool {
	for _, character := range value {
		if !unicode.IsSpace(character) && (character < 0x1c || character > 0x1f) {
			return true
		}
	}
	return false
}

func fallbackEscapePhrase(value string) string {
	var escaped strings.Builder
	for _, character := range value {
		if strings.ContainsRune(`+-&|!(){}[]^"~*?:\\/`, character) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(character)
	}
	return escaped.String()
}

// CanonicalFallbackQueryPlan validates the bounded stage vocabulary and returns
// canonical JSON suitable for a durable semantic receipt.
func CanonicalFallbackQueryPlan(raw json.RawMessage) (json.RawMessage, error) {
	var plan TypedFallbackQueryPlan
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return nil, fmt.Errorf("decode fallback query plan: %w", err)
	}
	if plan.SchemaVersion != 1 || len(plan.Stages) > 3 {
		return nil, fmt.Errorf("fallback query plan version or stage count is invalid")
	}
	previous := -1
	for _, stage := range plan.Stages {
		order := map[string]int{"album_track": 0, "album": 1, "track": 2}[stage.Kind]
		if _, ok := map[string]bool{"album_track": true, "album": true, "track": true}[stage.Kind]; !ok || order <= previous ||
			strings.TrimSpace(stage.Query) == "" || stage.Offset != 0 || stage.Limit != 10 {
			return nil, fmt.Errorf("fallback query plan stage is invalid")
		}
		previous = order
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}
