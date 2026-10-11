// Package matchingfit contains the shared, side-effect-free artist-fit policy
// used by both acquisition and its persistence admission boundary.
package matchingfit

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anyascii/go"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/textsimilarity"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Fit averages one full-value comparison per captured member. Missing artist
// fields are excluded; aliases and fallback behavior match saved-tag matching.
func Fit(captures []json.RawMessage, artist string) (float64, bool, error) {
	if strings.TrimSpace(artist) == "" {
		return 0, false, nil
	}
	total := new(big.Rat)
	count := 0
	for _, capture := range captures {
		var tags map[string][]string
		if err := json.Unmarshal(capture, &tags); err != nil {
			return 0, false, fmt.Errorf("decode captured metadata for artist fit: %w", err)
		}
		values := make([]string, 0)
		keys := make([]string, 0, len(tags))
		for key := range tags {
			keys = append(keys, key)
		}
		// Map order cannot influence a comparison's joined value.
		sort.Strings(keys)
		for _, key := range keys {
			canonical := strings.ToUpper(strings.TrimSpace(key))
			if canonical == "ALBUMARTIST" || canonical == "ALBUM ARTIST" || canonical == "ALBUM_ARTIST" {
				values = append(values, tags[key]...)
			}
		}
		if !hasUsable(values) {
			for _, key := range keys {
				if strings.EqualFold(strings.TrimSpace(key), "ARTIST") {
					values = append(values, tags[key]...)
				}
			}
		}
		value, available, err := similarity(values, []string{artist})
		if err != nil {
			return 0, false, err
		}
		if available {
			total.Add(total, new(big.Rat).SetFloat64(value))
			count++
		}
	}
	if count == 0 {
		return 0, false, nil
	}
	average, _ := new(big.Rat).Quo(total, big.NewRat(int64(count), 1)).Float64()
	return average, true, nil
}

func similarity(left, right []string) (float64, bool, error) {
	left, err := comparisonValues(left)
	if err != nil {
		return 0, false, err
	}
	right, err = comparisonValues(right)
	if err != nil {
		return 0, false, err
	}
	if len(left) == 0 || len(right) == 0 {
		return 0, false, nil
	}
	a, b := strings.Join(left, "; "), strings.Join(right, "; ")
	value, err := maxSimilarity(canonical(a), canonical(b), canonical(anyascii.Transliterate(a)), canonical(anyascii.Transliterate(b)))
	return value, true, err
}

func comparisonValues(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !utf8.ValidString(value) {
			return nil, fmt.Errorf("text is not valid UTF-8")
		}
		if !hasUsable([]string{value}) {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func hasUsable(values []string) bool {
	for _, value := range values {
		for _, r := range value {
			if !unicode.IsSpace(r) && (r < 0x1c || r > 0x1f) {
				return true
			}
		}
	}
	return false
}

func canonical(value string) string {
	value = norm.NFC.String(cases.Fold().String(norm.NFC.String(value)))
	var result strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			result.WriteRune(r)
		} else {
			result.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(result.String()), " ")
}

func maxSimilarity(left, right, transliteratedLeft, transliteratedRight string) (float64, error) {
	normalized, err := softSimilarity(left, right)
	if err != nil {
		return 0, err
	}
	transliterated, err := softSimilarity(transliteratedLeft, transliteratedRight)
	if transliterated > normalized {
		return transliterated, err
	}
	return normalized, err
}

func softSimilarity(left, right string) (float64, error) {
	a, b := strings.Fields(left), strings.Fields(right)
	if len(a) == 0 || len(b) == 0 {
		return 0, nil
	}
	left, right = strings.Join(a, " "), strings.Join(b, " ")
	tokenCoverage := float64(min(len(a), len(b))) / float64(max(len(a), len(b)))
	leftLength, rightLength := utf8.RuneCountInString(left), utf8.RuneCountInString(right)
	characterCoverage := float64(min(leftLength, rightLength)) / float64(max(leftLength, rightLength))
	ratio, err := textsimilarity.TokenSetSimilarity([]rune(left), []rune(right))
	if err != nil {
		return 0, fmt.Errorf("compare tokens with RapidFuzz: %w", err)
	}
	return ratio * (0.6 + 0.4*((tokenCoverage+characterCoverage)/2)), nil
}
