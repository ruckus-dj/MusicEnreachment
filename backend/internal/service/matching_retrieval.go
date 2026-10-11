package service

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/matchingfit"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// matchingRetrievalCandidate is the neutral catalog material used before full
// release hydration. It deliberately carries no recording or duration evidence.
type MatchingRetrievalCandidate struct {
	ID             string
	Title          string
	Disambiguation string
	Date           string
	ArtistCredit   string
	ArtistCredits  []string
	CatalogNumbers []string
}

type matchingRetrievalRank struct {
	ReleaseID    string
	Score        *big.Rat
	ExactMatches int
}

// MatchingArtistFit averages one full-value comparison per captured file.
// Missing artist fields are excluded, and all source values are concatenated
// before comparison (never individually maximized).
func MatchingArtistFit(captured []persistence.MatchingAcquisitionCapturedMember, artistCredit string) (float64, bool, error) {
	tags := make([]json.RawMessage, 0, len(captured))
	seenMembers := make(map[uuid.UUID]struct{}, len(captured))
	for _, member := range captured {
		if member.MemberID != uuid.Nil {
			if _, exists := seenMembers[member.MemberID]; exists {
				return 0, false, fmt.Errorf("matching artist fit capture contains duplicate member id %q", member.MemberID)
			}
			seenMembers[member.MemberID] = struct{}{}
		}
		tags = append(tags, member.Tags)
	}
	return matchingfit.Fit(tags, artistCredit)
}

// rankMatchingCatalog is a pure, reproducible candidate ranking policy. Each
// source file contributes the mean of its available full-artist and
// album/title text comparisons. Exact DATE/catalog matches only break text ties.
func RankMatchingCatalog(captured []persistence.MatchingAcquisitionCapturedMember, candidates []MatchingRetrievalCandidate) ([]string, error) {
	if len(captured) == 0 || len(candidates) == 0 {
		return nil, fmt.Errorf("matching retrieval requires captured members and catalog candidates")
	}
	type sourceFile struct {
		id     uuid.UUID
		fields map[string][]string
	}
	files := make([]sourceFile, 0, len(captured))
	seenFiles := make(map[uuid.UUID]struct{}, len(captured))
	for _, member := range captured {
		if member.MemberID == uuid.Nil || len(member.Tags) == 0 {
			return nil, fmt.Errorf("matching retrieval capture is incomplete")
		}
		if _, exists := seenFiles[member.MemberID]; exists {
			return nil, fmt.Errorf("matching retrieval capture contains duplicate member id %q", member.MemberID)
		}
		seenFiles[member.MemberID] = struct{}{}
		var tags map[string][]string
		if err := json.Unmarshal(member.Tags, &tags); err != nil {
			return nil, fmt.Errorf("decode captured metadata for matching retrieval: %w", err)
		}
		fields := matchingSourceFields(tags)
		keys := make([]string, 0, len(tags))
		for key := range tags {
			if strings.EqualFold(strings.TrimSpace(key), "DATE") {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			fields["date"] = append(fields["date"], tags[key]...)
		}
		keys = keys[:0]
		for key := range tags {
			canonical := strings.ToUpper(strings.TrimSpace(key))
			if canonical == "CATALOGNUMBER" || canonical == "CATALOG NUMBER" || canonical == "CATALOG_NUMBER" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			fields["catalog_number"] = append(fields["catalog_number"], tags[key]...)
		}
		files = append(files, sourceFile{id: member.MemberID, fields: fields})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].id.String() < files[j].id.String() })
	candidates = append([]MatchingRetrievalCandidate(nil), candidates...)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	ranks := make([]matchingRetrievalRank, 0, len(candidates))
	for _, candidate := range candidates {
		id, err := canonicalMBID(candidate.ID)
		if err != nil {
			return nil, fmt.Errorf("matching retrieval candidate id is invalid")
		}
		score := new(big.Rat)
		exactMatches := 0
		for _, file := range files {
			var textTotal big.Rat
			textCount := 0
			artistValues := candidate.ArtistCredits
			if len(artistValues) == 0 && strings.TrimSpace(candidate.ArtistCredit) != "" {
				artistValues = []string{candidate.ArtistCredit}
			}
			artistFactor, err := matchingTextFactor(MatchingReleaseArtist, MatchingTextPair{Source: file.fields["album_artist"], Candidate: artistValues})
			if err != nil {
				return nil, err
			}
			if artistFactor.State == MatchingFactorAvailable {
				textTotal.Add(&textTotal, new(big.Rat).SetFloat64(artistFactor.Value))
				textCount++
			}
			albumFactor, err := matchingTextFactor(MatchingReleaseTitle, MatchingTextPair{Source: file.fields["album"], Candidate: []string{candidate.Title}})
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(candidate.Disambiguation) != "" {
				qualifiedFactor, factorErr := matchingTextFactor(MatchingReleaseTitle, MatchingTextPair{
					Source: file.fields["album"], Candidate: []string{candidate.Title + " " + candidate.Disambiguation},
				})
				if factorErr != nil {
					return nil, factorErr
				}
				if qualifiedFactor.State == MatchingFactorAvailable && (albumFactor.State != MatchingFactorAvailable || qualifiedFactor.Value > albumFactor.Value) {
					albumFactor = qualifiedFactor
				}
			}
			if albumFactor.State == MatchingFactorAvailable {
				textTotal.Add(&textTotal, new(big.Rat).SetFloat64(albumFactor.Value))
				textCount++
			}
			if textCount > 0 {
				score.Add(score, new(big.Rat).Quo(&textTotal, big.NewRat(int64(textCount), 1)))
			}
			// Date comparison is deliberately exact against the complete saved
			// string: a year is not inferred from a full release date.
			for _, date := range file.fields["date"] {
				if date != "" && candidate.Date != "" {
					if date == candidate.Date {
						exactMatches++
						break
					}
				}
			}
			// A catalog match is one point per file, independent of how many
			// identifiers that file happens to contain.
			for _, catalogNumber := range file.fields["catalog_number"] {
				if catalogNumber != "" && containsExactString(candidate.CatalogNumbers, catalogNumber) {
					exactMatches++
					break
				}
			}
		}
		ranks = append(ranks, matchingRetrievalRank{ReleaseID: id, Score: score, ExactMatches: exactMatches})
	}
	sort.Slice(ranks, func(i, j int) bool {
		if comparison := ranks[i].Score.Cmp(ranks[j].Score); comparison != 0 {
			return comparison > 0
		}
		if ranks[i].ExactMatches != ranks[j].ExactMatches {
			return ranks[i].ExactMatches > ranks[j].ExactMatches
		}
		return ranks[i].ReleaseID < ranks[j].ReleaseID
	})
	limit := min(10, len(ranks))
	selected := make([]string, limit)
	for i := range selected {
		selected[i] = ranks[i].ReleaseID
	}
	sort.Strings(selected) // persistence's immutable shortlist is canonical.
	return selected, nil
}

func containsExactString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
