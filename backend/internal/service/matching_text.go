package service

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anyascii/go"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/textsimilarity"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

func matchingTextFactor(component MatchingComponent, pair MatchingTextPair) (MatchingFactor, error) {
	factor := MatchingFactor{
		Component: component, Provenance: pair.Provenance,
		SourceRaw:    append([]string(nil), pair.Source...),
		CandidateRaw: append([]string(nil), pair.Candidate...),
	}
	sourceValues, err := matchingComparisonValues(pair.Source)
	if err != nil {
		return MatchingFactor{}, fmt.Errorf("normalize source matching text: %w", err)
	}
	candidateValues, err := matchingComparisonValues(pair.Candidate)
	if err != nil {
		return MatchingFactor{}, fmt.Errorf("normalize candidate matching text: %w", err)
	}
	if len(sourceValues) == 0 || len(candidateValues) == 0 {
		factor.State = MatchingFactorMissing
		return factor, nil
	}
	left := strings.Join(sourceValues, "; ")
	right := strings.Join(candidateValues, "; ")
	factor.State = MatchingFactorAvailable
	factor.SourceNormalized = canonicalMatchingText(left)
	factor.CandidateNormalized = canonicalMatchingText(right)
	factor.TransliteratedSourceNormalized = canonicalMatchingText(anyascii.Transliterate(left))
	factor.TransliteratedCandidateNormalized = canonicalMatchingText(anyascii.Transliterate(right))
	factor.Value, err = matchingTextSimilarityProcessed(
		factor.SourceNormalized,
		factor.CandidateNormalized,
		factor.TransliteratedSourceNormalized,
		factor.TransliteratedCandidateNormalized,
	)
	if err != nil {
		return MatchingFactor{}, fmt.Errorf("compare matching text: %w", err)
	}
	return factor, nil
}

func matchingComparisonValues(values []string) ([]string, error) {
	unique := stableUniqueMatchingValues(values)
	comparison := make([]string, 0, len(unique))
	for _, value := range unique {
		if !utf8.ValidString(value) {
			return nil, fmt.Errorf("text is not valid UTF-8")
		}
		if hasNonWhitespaceRune(value) {
			comparison = append(comparison, value)
		}
	}
	return comparison, nil
}

func matchingTextSimilarity(left, right string) (float64, error) {
	if !utf8.ValidString(left) || !utf8.ValidString(right) {
		return 0, fmt.Errorf("matching text is not valid UTF-8")
	}
	leftNormalized := canonicalMatchingText(left)
	rightNormalized := canonicalMatchingText(right)
	leftTransliterated := canonicalMatchingText(anyascii.Transliterate(left))
	rightTransliterated := canonicalMatchingText(anyascii.Transliterate(right))
	return matchingTextSimilarityProcessed(leftNormalized, rightNormalized, leftTransliterated, rightTransliterated)
}

func matchingTextSimilarityProcessed(left, right, transliteratedLeft, transliteratedRight string) (float64, error) {
	normalized, err := softTokenSimilarity(left, right)
	if err != nil {
		return 0, err
	}
	transliterated, err := softTokenSimilarity(transliteratedLeft, transliteratedRight)
	if err != nil {
		return 0, err
	}
	return max(normalized, transliterated), nil
}

func softTokenSimilarity(left, right string) (float64, error) {
	leftTokens := strings.Fields(left)
	rightTokens := strings.Fields(right)
	if len(leftTokens) == 0 || len(rightTokens) == 0 {
		return 0, nil
	}
	joinedLeft := strings.Join(leftTokens, " ")
	joinedRight := strings.Join(rightTokens, " ")
	tokenCoverage := float64(min(len(leftTokens), len(rightTokens))) / float64(max(len(leftTokens), len(rightTokens)))
	leftCharacters := utf8.RuneCountInString(joinedLeft)
	rightCharacters := utf8.RuneCountInString(joinedRight)
	characterCoverage := float64(min(leftCharacters, rightCharacters)) / float64(max(leftCharacters, rightCharacters))
	coveragePenalty := 0.6 + 0.4*((tokenCoverage+characterCoverage)/2)
	ratio, err := textsimilarity.TokenSetSimilarity([]rune(joinedLeft), []rune(joinedRight))
	if err != nil {
		return 0, fmt.Errorf("compare tokens with RapidFuzz: %w", err)
	}
	return ratio * coveragePenalty, nil
}

func canonicalMatchingText(value string) string {
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

func hasNonWhitespaceRune(value string) bool {
	for _, character := range value {
		if !unicode.IsSpace(character) && (character < 0x1c || character > 0x1f) {
			return true
		}
	}
	return false
}
