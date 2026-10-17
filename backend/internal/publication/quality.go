package publication

import "sort"

// QualityCandidate contains only technical facts used to compare audio streams.
// Nil dimensions mean that the corresponding fact is unknown, not that it has
// a default value. Identity fields are used only to make equal selections
// stable; they do not participate in the quality comparison.
type QualityCandidate struct {
	VariantUUID  string
	LocationUUID string
	StreamIndex  int

	Lossless   *bool
	Codec      string
	Channels   *int
	Layout     string
	BitDepth   *int
	SampleRate *int
	Bitrate    *int
}

// QualityComparison is the partial ordering between two technical candidates.
type QualityComparison uint8

const (
	QualityUnknown QualityComparison = iota
	QualityBetter
	QualityEqual
	QualityWorse
	QualityIncomparable
)

// CompareQuality compares candidate a with candidate b. Better and worse are
// directional; incomparable means both candidates are known but no ranking is
// approved, while unknown means a required fact is unavailable.
func CompareQuality(a, b QualityCandidate) QualityComparison {
	if a.Channels != nil && b.Channels != nil && *a.Channels != *b.Channels {
		return QualityIncomparable
	}
	if a.Layout != "" && b.Layout != "" && a.Layout != b.Layout {
		return QualityIncomparable
	}
	if a.Channels == nil || b.Channels == nil || a.Layout == "" || b.Layout == "" {
		return QualityUnknown
	}
	if a.Lossless == nil || b.Lossless == nil {
		return QualityUnknown
	}
	if *a.Lossless != *b.Lossless {
		if *a.Lossless {
			return QualityBetter
		}
		return QualityWorse
	}
	if *a.Lossless {
		if a.BitDepth == nil || b.BitDepth == nil {
			return QualityUnknown
		}
		if *a.BitDepth != *b.BitDepth {
			return compareInt(*a.BitDepth, *b.BitDepth)
		}
		if a.SampleRate == nil || b.SampleRate == nil {
			return QualityUnknown
		}
		return compareInt(*a.SampleRate, *b.SampleRate)
	}
	if a.Codec == "" || b.Codec == "" {
		return QualityUnknown
	}
	if a.Codec != b.Codec {
		return QualityIncomparable
	}
	if a.Bitrate == nil || b.Bitrate == nil {
		return QualityUnknown
	}
	return compareInt(*a.Bitrate, *b.Bitrate)
}

func compareInt(a, b int) QualityComparison {
	switch {
	case a > b:
		return QualityBetter
	case a < b:
		return QualityWorse
	default:
		return QualityEqual
	}
}

// QualitySelection describes a safe automatic selection. Candidate is set
// only when selection is resolved. RequiresManual is true when the available
// candidates do not have a proven, unambiguous best choice.
type QualitySelection struct {
	Candidate       *QualityCandidate
	RequiresManual  bool
	ReplacesCurrent bool
}

// SelectQuality chooses a proven maximum from technically suitable candidates.
// Suitability and verification are intentionally the caller's responsibility.
// On an initial publication a single candidate is allowed even when its
// dimensions are unknown. With a current candidate, replacement occurs only
// when the best alternative is proven better than that current candidate.
func SelectQuality(candidates []QualityCandidate, current *QualityCandidate) QualitySelection {
	if len(candidates) == 0 {
		if current == nil {
			return QualitySelection{RequiresManual: true}
		}
		kept := *current
		return QualitySelection{Candidate: &kept}
	}

	all := make([]QualityCandidate, 0, len(candidates)+1)
	all = append(all, candidates...)
	currentIndex := -1
	if current != nil {
		currentIndex = len(all)
		all = append(all, *current)
	}
	if len(all) == 1 {
		selected := all[0]
		return QualitySelection{Candidate: &selected}
	}

	maxima := make([]int, 0, len(all))
	for i := range all {
		isMaximum := true
		for j := range all {
			if i == j {
				continue
			}
			comparison := CompareQuality(all[i], all[j])
			if comparison != QualityBetter && comparison != QualityEqual {
				isMaximum = false
				break
			}
		}
		if isMaximum {
			maxima = append(maxima, i)
		}
	}
	if len(maxima) == 0 {
		if current != nil {
			kept := *current
			return QualitySelection{Candidate: &kept}
		}
		return QualitySelection{RequiresManual: true}
	}

	// Any maxima must be mutually equal: distinct non-equal maxima cannot both
	// dominate one another. Stable identity ordering resolves only this tie.
	for _, i := range maxima[1:] {
		if CompareQuality(all[maxima[0]], all[i]) != QualityEqual {
			if current != nil {
				kept := *current
				return QualitySelection{Candidate: &kept}
			}
			return QualitySelection{RequiresManual: true}
		}
	}

	sort.Slice(maxima, func(i, j int) bool {
		return stableIdentityLess(all[maxima[i]], all[maxima[j]])
	})
	winner := maxima[0]
	if currentIndex >= 0 && winner == currentIndex {
		kept := *current
		return QualitySelection{Candidate: &kept}
	}
	if currentIndex >= 0 && CompareQuality(all[winner], all[currentIndex]) != QualityBetter {
		kept := *current
		return QualitySelection{Candidate: &kept}
	}
	selected := all[winner]
	return QualitySelection{Candidate: &selected, ReplacesCurrent: currentIndex >= 0}
}

func stableIdentityLess(a, b QualityCandidate) bool {
	if a.VariantUUID != b.VariantUUID {
		return a.VariantUUID < b.VariantUUID
	}
	if a.LocationUUID != b.LocationUUID {
		return a.LocationUUID < b.LocationUUID
	}
	return a.StreamIndex < b.StreamIndex
}
