package service

import (
	"fmt"
	"sort"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// MatchingExperimentReleaseHeader is one release header used by the pure T02
// retrieval experiment. It deliberately contains no tracklist or assignment
// evidence: the experiment compares headers against frozen group metadata only.
type MatchingExperimentReleaseHeader struct {
	MatchingRetrievalCandidate
	// PositionCount is the known number of release positions. Nil means the
	// provider did not expose a count; it is never treated as zero.
	PositionCount *int
}

// MatchingExperimentTextEvidence reports how much full-release text evidence
// was available for one header. It is a diagnostic, not a score modifier.
type MatchingExperimentTextEvidence struct {
	// Available reports whether at least one full-artist or album/title
	// comparison had usable text on both sides. It stays true when the
	// available similarity itself is zero.
	Available         bool
	Files             int
	ArtistComparisons int
	AlbumComparisons  int
}

// MatchingExperimentReleaseRank is one result of the pure experiment ranking.
type MatchingExperimentReleaseRank struct {
	// ReleaseID is the canonical lowercase provider release UUID.
	ReleaseID string
	// Rank is the 1-based position in the full experiment ranking.
	Rank int
	// BaselineRank is the 1-based position in the current shortlist order,
	// computed without the position-count signal.
	BaselineRank int
	// Shortlisted reports whether this header is inside the requested top
	// limit. It does not imply that the release was selected or assigned.
	Shortlisted bool
	// TextScore is the display value of the current full-release text score.
	// Ordering itself uses the exact rational score, so equal display values
	// do not reorder headers.
	TextScore float64
	// ExactMatches is the current exact full DATE/catalog evidence count.
	ExactMatches int
	// AvailableText diagnoses how many files contributed text evidence.
	AvailableText MatchingExperimentTextEvidence
	// PositionCount is the header's known release position count, if any.
	PositionCount *int
	// CountDelta is PositionCount minus the number of captured files. It is
	// zero for an exact agreement, positive when the release is wider than the
	// group, negative when it is narrower, and nil when the count is unknown.
	CountDelta *int
}

type matchingExperimentEntry struct {
	evidence      matchingRetrievalRank
	positionCount *int
	countDelta    *int
	countKnown    bool
	countDistance int
	baselineRank  int
}

// RankMatchingExperimentCatalog returns the complete reproducible T02 ranking.
// The primary key is the current full-release text score, followed by the
// current exact DATE/catalog evidence. A known position count then acts as a
// soft signal on a remaining tie: exact agreement ranks first, an unknown count
// stays neutral between exact agreement and a known non-exact count, and known
// non-exact counts follow by absolute proximity. Counts never reject a header.
// The release ID is only the final display order for fully equal headers.
//
// limit marks the top entries as Shortlisted; it never truncates the returned
// ranking. A negative limit is invalid. An empty header list is a valid empty
// experiment, not a malformed fixture; only missing captured group evidence is
// rejected.
func RankMatchingExperimentCatalog(captured []persistence.MatchingAcquisitionCapturedMember, candidates []MatchingExperimentReleaseHeader, limit int) ([]MatchingExperimentReleaseRank, error) {
	if limit < 0 {
		return nil, fmt.Errorf("matching experiment requires a non-negative shortlist limit")
	}
	if len(captured) == 0 {
		return nil, fmt.Errorf("matching experiment retrieval requires captured members")
	}
	if len(candidates) == 0 {
		return []MatchingExperimentReleaseRank{}, nil
	}
	files, err := matchingRetrievalSourceFiles(captured)
	if err != nil {
		return nil, err
	}
	candidates = append([]MatchingExperimentReleaseHeader(nil), candidates...)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	entries := make([]*matchingExperimentEntry, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.PositionCount != nil && *candidate.PositionCount < 0 {
			return nil, fmt.Errorf("matching experiment candidate %q has a negative position count", candidate.ID)
		}
		evidence, rankErr := matchingRetrievalRankFor(files, candidate.MatchingRetrievalCandidate)
		if rankErr != nil {
			return nil, rankErr
		}
		if _, duplicate := seen[evidence.ReleaseID]; duplicate {
			return nil, fmt.Errorf("matching experiment repeats candidate %q", evidence.ReleaseID)
		}
		seen[evidence.ReleaseID] = struct{}{}
		entry := &matchingExperimentEntry{evidence: evidence}
		if candidate.PositionCount != nil {
			delta := *candidate.PositionCount - len(captured)
			distance := delta
			if distance < 0 {
				distance = -distance
			}
			entry.positionCount = intPointer(*candidate.PositionCount)
			entry.countDelta = intPointer(delta)
			entry.countKnown = true
			entry.countDistance = distance
		}
		entries = append(entries, entry)
	}
	baseline := append([]*matchingExperimentEntry(nil), entries...)
	sort.Slice(baseline, func(i, j int) bool {
		return compareMatchingRetrievalRank(baseline[i].evidence, baseline[j].evidence) < 0
	})
	for index, entry := range baseline {
		entry.baselineRank = index + 1
	}
	ordered := append([]*matchingExperimentEntry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if comparison := compareMatchingRetrievalTextEvidence(left.evidence, right.evidence); comparison != 0 {
			return comparison < 0
		}
		if comparison := compareMatchingExperimentCount(left, right); comparison != 0 {
			return comparison < 0
		}
		return left.evidence.ReleaseID < right.evidence.ReleaseID
	})
	results := make([]MatchingExperimentReleaseRank, len(ordered))
	for index, entry := range ordered {
		textScore, _ := entry.evidence.Score.Float64()
		results[index] = MatchingExperimentReleaseRank{
			ReleaseID:    entry.evidence.ReleaseID,
			Rank:         index + 1,
			BaselineRank: entry.baselineRank,
			Shortlisted:  index < limit,
			TextScore:    textScore,
			ExactMatches: entry.evidence.ExactMatches,
			AvailableText: MatchingExperimentTextEvidence{
				Available:         entry.evidence.ArtistComparisons > 0 || entry.evidence.AlbumComparisons > 0,
				Files:             entry.evidence.Files,
				ArtistComparisons: entry.evidence.ArtistComparisons,
				AlbumComparisons:  entry.evidence.AlbumComparisons,
			},
			PositionCount: entry.positionCount,
			CountDelta:    entry.countDelta,
		}
	}
	return results, nil
}

// compareMatchingExperimentCount orders a remaining text/exact tie. Exact known
// agreement comes first, an unknown count is neutral next, and known non-exact
// counts follow by absolute delta. Equal distances stay tied for the display ID
// order.
func compareMatchingExperimentCount(left, right *matchingExperimentEntry) int {
	leftExact := left.countKnown && left.countDistance == 0
	rightExact := right.countKnown && right.countDistance == 0
	if leftExact != rightExact {
		if leftExact {
			return -1
		}
		return 1
	}
	if left.countKnown != right.countKnown {
		if !left.countKnown {
			return -1
		}
		return 1
	}
	if !left.countKnown || left.countDistance == right.countDistance {
		return 0
	}
	if left.countDistance < right.countDistance {
		return -1
	}
	return 1
}
