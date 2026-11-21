package service

import (
	"context"
	"fmt"
	"math"
	"math/big"
)

const (
	// matchingLayoutSelectionMantissaBits is the significand width of float64.
	// A scale of 2^(53-exponent) turns one confidence into an exact integer.
	matchingLayoutSelectionMantissaBits = 53
	// matchingLayoutSelectionScoreBudget keeps the shared solver's exact
	// uint64/int64 arithmetic well inside its range. 256 members scaled to less
	// than 2^53 produce a sum below 2^61.
	matchingLayoutSelectionScoreBudget = uint64(1) << 62
	matchingLayoutSelectionScoreLimit  = float64(matchingLayoutSelectionScoreBudget)
	matchingLayoutCanonicalProbeLimit  = 2_000
)

// MatchingLayoutSelectionStatus describes the deterministic production result.
type MatchingLayoutSelectionStatus string

const (
	MatchingLayoutSelectionSelected      MatchingLayoutSelectionStatus = "selected"
	MatchingLayoutSelectionNone          MatchingLayoutSelectionStatus = "none"
	MatchingLayoutSelectionIndeterminate MatchingLayoutSelectionStatus = "indeterminate"
)

// MatchingLayoutSelection is one selected release layout, or a fail-closed
// status when no complete qualifying layout exists or the exact comparison is
// indeterminate. It contains no persistence DTOs and performs no work.
type MatchingLayoutSelection struct {
	Status    MatchingLayoutSelectionStatus `json:"status"`
	ReleaseID string                        `json:"release_id,omitempty"`
	Choices   map[string]string             `json:"choices,omitempty"`
	Score     float64                       `json:"score"`
	Reason    string                        `json:"reason,omitempty"`
}

type matchingLayoutSelectionScale struct {
	shift    int
	maxScore uint64
	overflow bool
}

type matchingLayoutSelectionCandidate struct {
	releaseID string
	score     *big.Rat
	choices   map[string]string
}

type matchingLayoutSelectionOutcome struct {
	usable        bool
	indeterminate bool
	reason        string
	score         uint64
	choices       map[string]string
}

// SelectMatchingLayout chooses at most one release layout for the whole group.
//
// A layout is eligible only when every member receives a distinct position
// within its containing folder and every selected pair reaches threshold.
// Among eligible layouts the maximum exact sum of pair scores wins. Equal sums
// resolve to the lowest release ID; within a release the exact solver's
// canonical member/position ordering supplies a deterministic witness even
// when several assignments are optimal. The release-profile gate is not used:
// it does not veto a complete qualifying layout.
//
// Scores are scaled per layout from the exact float64 decomposition so low
// confidence values remain comparable. A layout whose scaled dynamic range
// would overflow the shared integer solver fails closed as indeterminate.
func SelectMatchingLayout(
	ctx context.Context, input MatchingGroupEvaluationInput, threshold float64,
) (MatchingLayoutSelection, error) {
	if err := ctx.Err(); err != nil {
		return MatchingLayoutSelection{}, err
	}
	if !validMatchingConfidence(threshold) {
		return MatchingLayoutSelection{}, fmt.Errorf("matching layout selection threshold must be between 0 and 1")
	}
	if len(input.ReleaseLayouts) > MatchingReleaseShortlistLimit {
		return MatchingLayoutSelection{}, fmt.Errorf("matching layout selection requires at most ten shortlisted editions")
	}
	for _, member := range input.Members {
		if input.ConflictingFingerprints[member.FileMemberID] {
			return MatchingLayoutSelection{
				Status: MatchingLayoutSelectionNone,
				Reason: "a member has conflicting saved fingerprint identities",
			}, nil
		}
	}
	for _, layout := range input.ReleaseLayouts {
		if !layout.Complete {
			return MatchingLayoutSelection{
				Status: MatchingLayoutSelectionIndeterminate,
				Reason: "a shortlisted edition has no complete tracklist",
			}, nil
		}
	}
	edges := make([]MatchingInput, 0, len(input.Inputs))
	for _, edge := range input.Inputs {
		if !validMatchingConfidence(edge.AssignmentScore.Confidence) ||
			!validMatchingConfidence(edge.ReleaseScore.Confidence) {
			return MatchingLayoutSelection{}, fmt.Errorf("matching layout selection edge has invalid confidence")
		}
		if matchingLayoutHasTrackEvidence(edge.AssignmentScore) {
			edges = append(edges, edge)
		}
	}
	input.Inputs = edges
	scales := matchingLayoutSelectionScales(input, threshold)
	shifts := make(map[string]int, len(scales))
	for releaseID, scale := range scales {
		if scale.overflow {
			// The layout is reported indeterminate below. Building it at scale
			// zero validates inputs without constructing overflowing scores.
			shifts[releaseID] = 0
			continue
		}
		shifts[releaseID] = scale.shift
	}
	layouts, _, err := matchingBuildLayouts(input, threshold, shifts)
	if err != nil {
		return MatchingLayoutSelection{}, err
	}
	for _, layout := range layouts {
		if scales[layout.releaseID].overflow {
			return MatchingLayoutSelection{
				Status: MatchingLayoutSelectionIndeterminate,
				Reason: fmt.Sprintf("layout %q dynamic score range exceeds the exact solver", layout.releaseID),
			}, nil
		}
	}

	var best *matchingLayoutSelectionCandidate
	for _, layout := range layouts {
		outcome, err := layout.selectMatchingLayout(ctx)
		if err != nil {
			return MatchingLayoutSelection{}, err
		}
		if outcome.indeterminate {
			return MatchingLayoutSelection{
				Status: MatchingLayoutSelectionIndeterminate,
				Reason: outcome.reason,
			}, nil
		}
		if !outcome.usable {
			continue
		}
		scale := scales[layout.releaseID]
		score := new(big.Rat).SetFrac(
			new(big.Int).SetUint64(outcome.score),
			new(big.Int).Lsh(big.NewInt(1), uint(scale.shift)),
		)
		if best == nil || score.Cmp(best.score) > 0 ||
			(score.Cmp(best.score) == 0 && layout.releaseID < best.releaseID) {
			best = &matchingLayoutSelectionCandidate{
				releaseID: layout.releaseID,
				score:     score,
				choices:   outcome.choices,
			}
		}
	}
	if best == nil {
		return MatchingLayoutSelection{
			Status: MatchingLayoutSelectionNone,
			Reason: "no complete assignment meets the matching threshold",
		}, nil
	}
	score, _ := best.score.Float64()
	return MatchingLayoutSelection{
		Status:    MatchingLayoutSelectionSelected,
		ReleaseID: best.releaseID,
		Choices:   best.choices,
		Score:     score,
	}, nil
}

// Require evidence that relates the audio to a track, not merely a high
// available-only average from a position number or duration. A known album,
// exact track number and positive duration fit can corroborate a missing title.
func matchingLayoutHasTrackEvidence(score MatchingScore) bool {
	var matchedAlbum, matchedNumber, matchedDuration bool
	for _, factor := range score.Factors {
		if factor.State != MatchingFactorAvailable || factor.Value <= 0 {
			continue
		}
		switch factor.Component {
		case MatchingRecordingTitle, MatchingAcoustID:
			return true
		case MatchingReleaseTitle:
			matchedAlbum = true
		case MatchingTrackNumber:
			matchedNumber = true
		case MatchingRecordingDuration, MatchingReleaseDuration:
			matchedDuration = true
		}
	}
	return matchedAlbum && matchedNumber && matchedDuration
}

// matchingLayoutSelectionScales derives one exact integer scale per release
// from the binary decomposition of its eligible confidence values.
func matchingLayoutSelectionScales(
	input MatchingGroupEvaluationInput, threshold float64,
) map[string]matchingLayoutSelectionScale {
	confidences := make(map[string][]float64)
	for _, candidate := range input.Inputs {
		confidence := candidate.AssignmentScore.Confidence
		if confidence >= threshold {
			confidences[candidate.ReleaseID] = append(confidences[candidate.ReleaseID], confidence)
		}
	}
	scales := make(map[string]matchingLayoutSelectionScale, len(confidences))
	for releaseID, values := range confidences {
		scale := matchingLayoutSelectionScale{}
		for _, value := range values {
			if shift := matchingLayoutSelectionShift(value); shift > scale.shift {
				scale.shift = shift
			}
		}
		for _, value := range values {
			scaled := math.Ldexp(value, scale.shift)
			if math.IsInf(scaled, 1) || scaled >= matchingLayoutSelectionScoreLimit {
				scale.overflow = true
				break
			}
			if score := uint64(scaled); score > scale.maxScore {
				scale.maxScore = score
			}
		}
		if !scale.overflow && scale.maxScore > 0 {
			members := uint64(len(input.Members))
			if members > (matchingLayoutSelectionScoreBudget-1)/scale.maxScore {
				scale.overflow = true
			}
		}
		scales[releaseID] = scale
	}
	return scales
}

func matchingLayoutSelectionShift(value float64) int {
	if value <= 0 || !validMatchingConfidence(value) {
		return 0
	}
	_, exponent := math.Frexp(value)
	return matchingLayoutSelectionMantissaBits - exponent
}

// selectMatchingLayout evaluates one layout without applying the
// release-profile gate. Every component must produce a complete assignment
// above the configured threshold; a component tie is not indeterminate because
// the solver witness is deterministic.
func (layout *matchingExperimentLayout) selectMatchingLayout(ctx context.Context) (matchingLayoutSelectionOutcome, error) {
	positions := make(map[string]int, len(layout.rows))
	var score uint64
	for _, component := range layout.components {
		result, err := layout.evaluateComponent(ctx, component)
		if err != nil {
			return matchingLayoutSelectionOutcome{}, err
		}
		if result.structuralIndeterminate || result.scoreIndeterminate {
			reason := result.structuralReason
			if result.scoreIndeterminate {
				reason = result.scoreReason
			}
			if reason == "" {
				reason = "layout assignment is indeterminate"
			}
			return matchingLayoutSelectionOutcome{indeterminate: true, reason: reason}, nil
		}
		if !result.scoreFull {
			return matchingLayoutSelectionOutcome{}, nil
		}
		score += result.bestScore
		witness := result.witness
		if result.withinTie || result.uniquenessUnknown {
			witness, err = layout.canonicalOptimum(ctx, component, result)
			if err != nil {
				return matchingLayoutSelectionOutcome{}, err
			}
			if witness == nil {
				return matchingLayoutSelectionOutcome{
					indeterminate: true, reason: "canonical optimum proof exceeded the solver bound",
				}, nil
			}
		}
		for fileID, position := range witness {
			positions[fileID] = position
		}
	}
	if len(positions) != len(layout.rows) {
		return matchingLayoutSelectionOutcome{
			indeterminate: true,
			reason:        "layout assignment witness is incomplete",
		}, nil
	}
	choices := make(map[string]string, len(positions))
	for _, row := range layout.rows {
		position, exists := positions[row.id]
		if !exists {
			return matchingLayoutSelectionOutcome{
				indeterminate: true,
				reason:        "layout assignment witness is incomplete",
			}, nil
		}
		edge, exists := row.edgeByPosition[position]
		if !exists || !edge.qualifying {
			return matchingLayoutSelectionOutcome{
				indeterminate: true,
				reason:        "layout assignment witness is ineligible",
			}, nil
		}
		choices[row.id] = edge.candidateID
	}
	return matchingLayoutSelectionOutcome{usable: true, score: score, choices: choices}, nil
}

// Fix the lowest usable position for each canonical member in order, without
// changing the already-proved optimum. The witness bounds the probes: only
// positions lower than the current optimum need to be tested.
func (layout *matchingExperimentLayout) canonicalOptimum(
	ctx context.Context, component []int, initial matchingExperimentComponentResult,
) (map[string]int, error) {
	rows := make([]*matchingLayoutRow, len(component))
	witness := make([]int, len(component))
	for i, index := range component {
		row := *layout.rows[index]
		rows[i] = &row
		position, ok := initial.witness[row.id]
		if !ok {
			return nil, fmt.Errorf("matching layout optimum has no witness for member %s", row.id)
		}
		witness[i] = position
	}
	clique := matchingLayoutPairwiseConflicting(rows)
	probes := 0
	for i, row := range rows {
		edges := row.edges
		for _, edge := range edges {
			if edge.position >= witness[i] {
				break
			}
			if !edge.qualifying {
				continue
			}
			if probes == matchingLayoutCanonicalProbeLimit {
				return nil, nil
			}
			probes++
			row.edges = []matchingLayoutEdge{edge}
			alternative, sum, full, determinate, err := tryMatchingLayoutOptimum(ctx, rows, len(layout.positions), clique)
			if err != nil || !determinate {
				return nil, err
			}
			if full && sum == initial.bestScore {
				witness = alternative
				break
			}
		}
		row.edges = []matchingLayoutEdge{row.edgeByPosition[witness[i]]}
	}
	result := make(map[string]int, len(rows))
	for i, row := range rows {
		result[row.id] = witness[i]
	}
	return result, nil
}

func tryMatchingLayoutOptimum(
	ctx context.Context, rows []*matchingLayoutRow, positionCount int, clique bool,
) (choices []int, score uint64, full bool, determinate bool, err error) {
	if clique {
		choices, score, full := matchingLayoutHungarianAssignment(ctx, rows, positionCount, true, -1, -1)
		return choices, score, full, true, ctx.Err()
	}
	result, err := matchingLayoutBranchAndBound(ctx, rows, positionCount)
	return result.choices, result.bestScore, result.found, !result.indeterminate, err
}
