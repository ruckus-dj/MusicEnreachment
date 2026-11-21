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

	// The remaining fields are retrieval-time diagnostics. They do not change
	// the runtime shortlist ordering.
	Files             int
	ArtistComparisons int
	AlbumComparisons  int
}

type matchingRetrievalSourceFile struct {
	id     uuid.UUID
	fields map[string][]string
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

// RankMatchingCatalog is a pure, reproducible candidate ranking policy. Each
// source file contributes the mean of its available full-artist and
// album/title text comparisons. Exact DATE/catalog matches only break text ties.
func RankMatchingCatalog(captured []persistence.MatchingAcquisitionCapturedMember, candidates []MatchingRetrievalCandidate) ([]string, error) {
	if len(captured) == 0 || len(candidates) == 0 {
		return nil, fmt.Errorf("matching retrieval requires captured members and catalog candidates")
	}
	files, err := matchingRetrievalSourceFiles(captured)
	if err != nil {
		return nil, err
	}
	candidates = append([]MatchingRetrievalCandidate(nil), candidates...)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	ranks := make([]matchingRetrievalRank, 0, len(candidates))
	for _, candidate := range candidates {
		rank, rankErr := matchingRetrievalRankFor(files, candidate)
		if rankErr != nil {
			return nil, rankErr
		}
		ranks = append(ranks, rank)
	}
	sort.Slice(ranks, func(i, j int) bool { return compareMatchingRetrievalRank(ranks[i], ranks[j]) < 0 })
	limit := min(10, len(ranks))
	selected := make([]string, limit)
	for i := range selected {
		selected[i] = ranks[i].ReleaseID
	}
	sort.Strings(selected) // persistence's immutable shortlist is canonical.
	return selected, nil
}

// matchingRetrievalSourceFiles decodes the frozen capture once so every
// candidate is compared against identical source evidence.
func matchingRetrievalSourceFiles(captured []persistence.MatchingAcquisitionCapturedMember) ([]matchingRetrievalSourceFile, error) {
	files := make([]matchingRetrievalSourceFile, 0, len(captured))
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
		files = append(files, matchingRetrievalSourceFile{id: member.MemberID, fields: fields})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].id.String() < files[j].id.String() })
	return files, nil
}

// matchingRetrievalRankFor evaluates one candidate against the complete group
// evidence. It is shared by the runtime shortlist and the pure experiment.
func matchingRetrievalRankFor(files []matchingRetrievalSourceFile, candidate MatchingRetrievalCandidate) (matchingRetrievalRank, error) {
	id, err := canonicalMBID(candidate.ID)
	if err != nil {
		return matchingRetrievalRank{}, fmt.Errorf("matching retrieval candidate id is invalid")
	}
	rank := matchingRetrievalRank{
		ReleaseID: id, Score: new(big.Rat),
	}
	artistValues := candidate.ArtistCredits
	if len(artistValues) == 0 && strings.TrimSpace(candidate.ArtistCredit) != "" {
		artistValues = []string{candidate.ArtistCredit}
	}
	for _, file := range files {
		var textTotal big.Rat
		textCount := 0
		artistFactor, err := matchingTextFactor(MatchingReleaseArtist, MatchingTextPair{Source: file.fields["album_artist"], Candidate: artistValues})
		if err != nil {
			return matchingRetrievalRank{}, err
		}
		if artistFactor.State == MatchingFactorAvailable {
			textTotal.Add(&textTotal, new(big.Rat).SetFloat64(artistFactor.Value))
			textCount++
			rank.ArtistComparisons++
		}
		albumFactor, err := matchingTextFactor(MatchingReleaseTitle, MatchingTextPair{Source: file.fields["album"], Candidate: []string{candidate.Title}})
		if err != nil {
			return matchingRetrievalRank{}, err
		}
		if strings.TrimSpace(candidate.Disambiguation) != "" {
			qualifiedFactor, factorErr := matchingTextFactor(MatchingReleaseTitle, MatchingTextPair{
				Source: file.fields["album"], Candidate: []string{candidate.Title + " " + candidate.Disambiguation},
			})
			if factorErr != nil {
				return matchingRetrievalRank{}, factorErr
			}
			if qualifiedFactor.State == MatchingFactorAvailable && (albumFactor.State != MatchingFactorAvailable || qualifiedFactor.Value > albumFactor.Value) {
				albumFactor = qualifiedFactor
			}
		}
		if albumFactor.State == MatchingFactorAvailable {
			textTotal.Add(&textTotal, new(big.Rat).SetFloat64(albumFactor.Value))
			textCount++
			rank.AlbumComparisons++
		}
		if textCount > 0 {
			rank.Files++
			rank.Score.Add(rank.Score, new(big.Rat).Quo(&textTotal, big.NewRat(int64(textCount), 1)))
		}
		// Date comparison is deliberately exact against the complete saved
		// string: a year is not inferred from a full release date.
		for _, date := range file.fields["date"] {
			if date != "" && candidate.Date != "" {
				if date == candidate.Date {
					rank.ExactMatches++
					break
				}
			}
		}
		// A catalog match is one point per file, independent of how many
		// identifiers that file happens to contain.
		for _, catalogNumber := range file.fields["catalog_number"] {
			if catalogNumber != "" && containsExactString(candidate.CatalogNumbers, catalogNumber) {
				rank.ExactMatches++
				break
			}
		}
	}
	return rank, nil
}

// compareMatchingRetrievalTextEvidence orders by full-release text score and
// then by exact DATE/catalog evidence. Both are descending.
func compareMatchingRetrievalTextEvidence(left, right matchingRetrievalRank) int {
	if comparison := left.Score.Cmp(right.Score); comparison != 0 {
		return -comparison
	}
	switch {
	case left.ExactMatches > right.ExactMatches:
		return -1
	case left.ExactMatches < right.ExactMatches:
		return 1
	default:
		return 0
	}
}

// compareMatchingRetrievalRank is the runtime shortlist order. The release ID
// resolves the final tie for a reproducible shortlist only.
func compareMatchingRetrievalRank(left, right matchingRetrievalRank) int {
	if comparison := compareMatchingRetrievalTextEvidence(left, right); comparison != 0 {
		return comparison
	}
	switch {
	case left.ReleaseID < right.ReleaseID:
		return -1
	case left.ReleaseID > right.ReleaseID:
		return 1
	default:
		return 0
	}
}

func containsExactString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
