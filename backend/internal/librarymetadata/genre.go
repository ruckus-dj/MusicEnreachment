package librarymetadata

import (
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// GenreReference identifies a MusicBrainz genre and retains its source and
// readable display names.
type GenreReference struct {
	ID         string `json:"id"`
	SourceName string `json:"source_name"`
	Name       string `json:"name"`
}

var protectedGenreAcronyms = map[string]string{
	"edm": "EDM", "idm": "IDM", "ebm": "EBM", "uk": "UK", "us": "US",
	"aor": "AOR", "nwobhm": "NWOBHM", "nrg": "NRG",
}

// NormalizeGenreName applies conservative display casing without rewriting
// separators or inferring genre aliases.
func NormalizeGenreName(raw string) string {
	runes := []rune(raw)
	var normalized strings.Builder
	lower := cases.Lower(language.English)
	upper := cases.Upper(language.English)
	consumed := 0
	for start := 0; start < len(runes); {
		if !unicode.IsLetter(runes[start]) && !unicode.IsDigit(runes[start]) {
			start++
			continue
		}
		end := start + 1
		for end < len(runes) {
			if unicode.IsLetter(runes[end]) || unicode.IsDigit(runes[end]) {
				end++
				continue
			}
			if (runes[end] == '\'' || runes[end] == '’') && end+1 < len(runes) && (unicode.IsLetter(runes[end+1]) || unicode.IsDigit(runes[end+1])) {
				end += 2
				continue
			}
			break
		}
		word := string(runes[start:end])
		normalized.WriteString(string(runes[consumed:start]))
		if acronym, ok := protectedGenreAcronyms[lower.String(word)]; ok {
			normalized.WriteString(acronym)
		} else {
			wordRunes := []rune(word)
			normalized.WriteString(upper.String(string(wordRunes[0])))
			normalized.WriteString(lower.String(string(wordRunes[1:])))
		}
		consumed = end
		start = end
	}
	normalized.WriteString(string(runes[consumed:]))
	return normalized.String()
}
