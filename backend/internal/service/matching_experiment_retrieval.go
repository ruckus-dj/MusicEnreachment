package service

import (
	"fmt"
	"sort"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// MatchingReleaseHeader is the complete public release-header evidence shared
// by the pure T02 experiment and the Plan 14 shortlist. It deliberately
// contains no tracklist or assignment evidence: callers compare headers
// against frozen group metadata only.
type MatchingReleaseHeader struct {
	MatchingRetrievalCandidate
	// PositionCount is the known number of release positions. Nil means the
	// provider did not expose a count; it is never treated as zero.
	PositionCount *int
}

// MatchingExperimentReleaseHeader keeps the frozen experiment, fixture, and
// report name for the shared header shape. It is not a distinct type.
type MatchingExperimentReleaseHeader = MatchingReleaseHeader

// MatchingReleaseShortlistLimit is the approved maximum number of release
// headers a pure shortlist may select. It bounds selection only; it never
// truncates a diagnostic ranking.
const MatchingReleaseShortlistLimit = 10

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

type matchingReleaseHeaderEntry struct {
	evidence      matchingRetrievalRank
	positionCount *int
	countDelta    *int
	countKnown    bool
	countDistance int
	baselineRank  int
}

// RankMatchingReleaseShortlist is the pure Plan 14 release-selection step. It
// evaluates complete public headers against frozen group evidence and returns
// at most MatchingReleaseShortlistLimit canonical provider release IDs, best
// first. Ordering is the experiment-proven exact comparator: full-release text
// score, exact DATE/catalog evidence, a known position count as the last
// evidence before the stable release ID, and finally the canonical release ID.
//
// A known count ranks an exact agreement first, keeps an unknown count neutral,
// and orders other known counts by absolute distance from the captured file
// count. Counts never reject a header. The function performs no provider,
// network, database, or filesystem I/O and never assigns files to positions.
// Duplicate canonical release IDs are rejected rather than collapsed.
func RankMatchingReleaseShortlist(captured []persistence.MatchingAcquisitionCapturedMember, headers []MatchingReleaseHeader) ([]string, error) {
	if len(captured) == 0 {
		return nil, fmt.Errorf("matching release shortlist requires captured members")
	}
	if len(headers) == 0 {
		return []string{}, nil
	}
	entries, err := matchingReleaseHeaderEntries(captured, headers)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return compareMatchingReleaseHeaderRank(entries[i], entries[j]) < 0
	})
	limit := min(MatchingReleaseShortlistLimit, len(entries))
	selected := make([]string, limit)
	for index := range selected {
		selected[index] = entries[index].evidence.ReleaseID
	}
	return selected, nil
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
	entries, err := matchingReleaseHeaderEntries(captured, candidates)
	if err != nil {
		return nil, err
	}
	baseline := append([]*matchingReleaseHeaderEntry(nil), entries...)
	sort.Slice(baseline, func(i, j int) bool {
		return compareMatchingRetrievalRank(baseline[i].evidence, baseline[j].evidence) < 0
	})
	for index, entry := range baseline {
		entry.baselineRank = index + 1
	}
	ordered := append([]*matchingReleaseHeaderEntry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool {
		return compareMatchingReleaseHeaderRank(ordered[i], ordered[j]) < 0
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

// matchingReleaseHeaderEntries decodes the frozen group evidence once and
// evaluates every header through the shared ranking inputs. Headers are
// processed in raw ID order so duplicate detection and error reporting are
// deterministic; canonical duplicate identities are still rejected.
func matchingReleaseHeaderEntries(captured []persistence.MatchingAcquisitionCapturedMember, headers []MatchingReleaseHeader) ([]*matchingReleaseHeaderEntry, error) {
	files, err := matchingRetrievalSourceFiles(captured)
	if err != nil {
		return nil, err
	}
	headers = append([]MatchingReleaseHeader(nil), headers...)
	sort.Slice(headers, func(i, j int) bool { return headers[i].ID < headers[j].ID })
	entries := make([]*matchingReleaseHeaderEntry, 0, len(headers))
	seen := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		if header.PositionCount != nil && *header.PositionCount < 0 {
			return nil, fmt.Errorf("matching release header %q has a negative position count", header.ID)
		}
		evidence, rankErr := matchingRetrievalRankFor(files, header.MatchingRetrievalCandidate)
		if rankErr != nil {
			return nil, rankErr
		}
		if _, duplicate := seen[evidence.ReleaseID]; duplicate {
			return nil, fmt.Errorf("matching release headers repeat candidate %q", evidence.ReleaseID)
		}
		seen[evidence.ReleaseID] = struct{}{}
		entry := &matchingReleaseHeaderEntry{evidence: evidence}
		if header.PositionCount != nil {
			delta := *header.PositionCount - len(captured)
			distance := delta
			if distance < 0 {
				distance = -distance
			}
			entry.positionCount = intPointer(*header.PositionCount)
			entry.countDelta = intPointer(delta)
			entry.countKnown = true
			entry.countDistance = distance
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// compareMatchingReleaseHeaderRank orders the shared pure shortlist: text
// evidence first, the known count as the last evidence before the stable
// canonical release ID.
func compareMatchingReleaseHeaderRank(left, right *matchingReleaseHeaderEntry) int {
	if comparison := compareMatchingRetrievalTextEvidence(left.evidence, right.evidence); comparison != 0 {
		return comparison
	}
	if comparison := compareMatchingReleaseHeaderCount(left, right); comparison != 0 {
		return comparison
	}
	switch {
	case left.evidence.ReleaseID < right.evidence.ReleaseID:
		return -1
	case left.evidence.ReleaseID > right.evidence.ReleaseID:
		return 1
	default:
		return 0
	}
}

// compareMatchingReleaseHeaderCount orders a remaining text/exact tie. Exact
// known agreement comes first, an unknown count is neutral next, and known
// non-exact counts follow by absolute delta. Equal distances stay tied for the
// stable release ID order.
func compareMatchingReleaseHeaderCount(left, right *matchingReleaseHeaderEntry) int {
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
