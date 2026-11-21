package service

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"path"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const (
	matchingExperimentMaximumMembers = matchingLayoutMaximumMembers
	matchingExperimentScoreShift     = 53
)

// MatchingExperimentStructuralStatus separates a complete raw layout from an
// incomplete one. "Raw" means every member has an unused position in the
// containing-folder constraints, without applying the assignment score floor.
type MatchingExperimentStructuralStatus string

const (
	MatchingExperimentStructuralFull          MatchingExperimentStructuralStatus = "full"
	MatchingExperimentStructuralIncomplete    MatchingExperimentStructuralStatus = "incomplete"
	MatchingExperimentStructuralIndeterminate MatchingExperimentStructuralStatus = "indeterminate"
)

// MatchingExperimentScoreStatus separates a qualifying full assignment from a
// structurally complete layout whose combined scores never reach the floor.
type MatchingExperimentScoreStatus string

const (
	MatchingExperimentScoreQualified      MatchingExperimentScoreStatus = "qualified"
	MatchingExperimentScoreBelowThreshold MatchingExperimentScoreStatus = "below_threshold"
	MatchingExperimentScoreIndeterminate  MatchingExperimentScoreStatus = "indeterminate"
	MatchingExperimentScoreNotEvaluated   MatchingExperimentScoreStatus = "not_evaluated"
)

// MatchingExperimentReleaseProfileMember records the current release-profile
// gate evidence for one explicit member. BestConfidence is the member's best
// release-profile confidence within the release, matching the existing release
// selection calculation.
type MatchingExperimentReleaseProfileMember struct {
	FileID         string  `json:"file_id"`
	Present        bool    `json:"present"`
	BestConfidence float64 `json:"best_confidence"`
}

// MatchingExperimentLayoutReport is the diagnostic result for one release.
// Choices are returned only for a uniquely optimal qualifying full assignment;
// they are nil for a tie. AcrossLayoutTie is an annotation only: no automatic
// winner or selected release is produced.
type MatchingExperimentLayoutReport struct {
	ReleaseID string `json:"release_id"`

	StructuralStatus MatchingExperimentStructuralStatus `json:"structural_status"`
	StructuralFull   bool                               `json:"structural_full"`
	StructuralReason string                             `json:"structural_reason,omitempty"`

	ScoreStatus MatchingExperimentScoreStatus `json:"score_status"`
	ScoreReason string                        `json:"score_reason,omitempty"`

	QualifiedFull bool `json:"qualified_full"`
	// BestFullScore is the exact optimal full-assignment sum of qualifying
	// combined scores, scaled by 2^53 and accumulated without floating-point
	// addition.
	BestFullScore      uint64  `json:"best_full_score,string"`
	BestFullScoreFloat float64 `json:"best_full_score_float"`
	// DistinctOptimalAssignments is 0 when no qualifying full assignment was
	// proven, 1 for a unique optimum, and 2 for at least two optimal layouts.
	DistinctOptimalAssignments int  `json:"distinct_optimal_assignments"`
	WithinLayoutTie            bool `json:"within_layout_tie"`
	AcrossLayoutTie            bool `json:"across_layout_tie"`

	// Choices maps file member IDs to release position candidate IDs for a
	// unique qualifying optimum only. A tied layout never exposes a witness.
	Choices map[string]string `json:"choices,omitempty"`

	// ReleaseProfileGate is the existing per-member best release-profile >= 0.70
	// gate for all members. It is reported independently of assignment status.
	ReleaseProfileGate    bool                                     `json:"release_profile_gate"`
	ReleaseProfileSum     float64                                  `json:"release_profile_sum"`
	ReleaseProfileMembers []MatchingExperimentReleaseProfileMember `json:"release_profile_members"`

	IndeterminateReason string `json:"indeterminate_reason,omitempty"`
}

// MatchingExperimentLayoutComparison is the complete deterministic comparison.
// Layouts are ordered by release ID; scores are compared by the caller and no
// layout is chosen automatically.
type MatchingExperimentLayoutComparison struct {
	Members int                              `json:"members"`
	Layouts []MatchingExperimentLayoutReport `json:"layouts"`
}

type matchingExperimentPosition = matchingLayoutPosition
type matchingExperimentEdge = matchingLayoutEdge
type matchingExperimentRow = matchingLayoutRow

type matchingExperimentLayout struct {
	releaseID  string
	positions  []matchingExperimentPosition
	rows       []*matchingExperimentRow
	components [][]int
}

type matchingExperimentComponentResult struct {
	structuralFull          bool
	structuralIndeterminate bool
	structuralReason        string

	scoreFull          bool
	scoreIndeterminate bool
	scoreReason        string
	bestScore          uint64
	withinTie          bool
	uniquenessUnknown  bool
	// witness is a deterministic optimum even when withinTie is set. The
	// experiment report only exposes choices for a unique optimum.
	witness map[string]int
	choices map[string]int
}

// CompareMatchingExperimentLayouts compares complete release layouts for the
// explicit group members. Every member receives exactly one position; members
// that share a canonical containing folder cannot reuse a position, while
// members in disjoint folders may reuse the same release position. The result
// reports raw structural feasibility separately from the combined-score floor,
// reports the existing release-profile gate separately, annotates both tie
// scopes, and never returns an automatic release winner.
//
// The calculation is deterministic and offline. A deterministic search bound
// reports indeterminate instead of substituting a partial or guessed layout.
func CompareMatchingExperimentLayouts(ctx context.Context, input MatchingGroupEvaluationInput) (MatchingExperimentLayoutComparison, error) {
	if err := ctx.Err(); err != nil {
		return MatchingExperimentLayoutComparison{}, err
	}
	layouts, memberCount, err := matchingExperimentBuildLayouts(input)
	if err != nil {
		return MatchingExperimentLayoutComparison{}, err
	}
	comparison := MatchingExperimentLayoutComparison{
		Members: memberCount,
		Layouts: make([]MatchingExperimentLayoutReport, 0, len(layouts)),
	}
	for _, layout := range layouts {
		report, err := layout.report(ctx)
		if err != nil {
			return MatchingExperimentLayoutComparison{}, err
		}
		comparison.Layouts = append(comparison.Layouts, report)
	}

	bestKnown := false
	unresolved := false
	var bestScore uint64
	bestCount := 0
	for _, report := range comparison.Layouts {
		if report.StructuralStatus == MatchingExperimentStructuralIndeterminate ||
			(report.ScoreStatus == MatchingExperimentScoreIndeterminate && !report.QualifiedFull) {
			unresolved = true
		}
		if !report.QualifiedFull {
			continue
		}
		if !bestKnown || report.BestFullScore > bestScore {
			bestKnown = true
			bestScore = report.BestFullScore
			bestCount = 1
		} else if report.BestFullScore == bestScore {
			bestCount++
		}
	}
	if bestKnown && bestCount > 1 && !unresolved {
		for i := range comparison.Layouts {
			if comparison.Layouts[i].QualifiedFull && comparison.Layouts[i].BestFullScore == bestScore {
				comparison.Layouts[i].AcrossLayoutTie = true
			}
		}
	}
	return comparison, nil
}

func matchingExperimentBuildLayouts(input MatchingGroupEvaluationInput) ([]*matchingExperimentLayout, int, error) {
	return matchingBuildLayouts(input, minimumReleaseCandidateConfidence, nil)
}

// matchingBuildLayouts validates and builds one layout per release. shifts
// overrides the integer scale exponent per release; a nil map uses the
// experiment's fixed 2^53 scale. An edge is eligible only when its assignment
// confidence reaches threshold.
func matchingBuildLayouts(
	input MatchingGroupEvaluationInput, threshold float64, shifts map[string]int,
) ([]*matchingExperimentLayout, int, error) {
	if !validMatchingConfidence(threshold) {
		return nil, 0, fmt.Errorf("matching layout threshold must be between 0 and 1")
	}
	if len(input.Members) == 0 {
		return nil, 0, fmt.Errorf("matching experiment layout comparison has no members")
	}
	if len(input.Members) > matchingExperimentMaximumMembers {
		return nil, 0, fmt.Errorf("matching experiment layout comparison member count exceeds the exact solver cap of %d", matchingExperimentMaximumMembers)
	}
	members := make(map[uuid.UUID]bool, len(input.Members))
	for _, member := range input.Members {
		if member.FileMemberID == uuid.Nil || members[member.FileMemberID] {
			return nil, 0, fmt.Errorf("matching experiment layout member identity is invalid or duplicated")
		}
		members[member.FileMemberID] = true
	}
	files := make(map[uuid.UUID]IncomingFile, len(input.Files))
	for _, file := range input.Files {
		if file.VariantID == uuid.Nil || files[file.VariantID].VariantID != uuid.Nil {
			return nil, 0, fmt.Errorf("matching experiment layout file identity is invalid or duplicated")
		}
		if !members[file.VariantID] {
			return nil, 0, fmt.Errorf("matching experiment layout files contain a foreign group member")
		}
		files[file.VariantID] = file
	}
	locations := make(map[uuid.UUID][]string, len(input.Members))
	for memberID := range members {
		file, exists := files[memberID]
		if !exists {
			return nil, 0, fmt.Errorf("matching experiment layout member is absent from files")
		}
		canonical, err := matchingExperimentCanonicalLocations(file)
		if err != nil {
			return nil, 0, err
		}
		locations[memberID] = canonical
	}

	type releaseLayout struct {
		id        string
		positions []matchingExperimentPosition
		byCoord   map[[2]int]int
		rows      map[uuid.UUID]*matchingExperimentRow
		order     []uuid.UUID
	}
	releases := make(map[string]*releaseLayout, len(input.ReleaseLayouts))
	for _, release := range input.ReleaseLayouts {
		if release.ProviderKey == "" || releases[release.ProviderKey] != nil {
			return nil, 0, fmt.Errorf("matching experiment layout release identity is invalid or duplicated")
		}
		built := &releaseLayout{
			id:      release.ProviderKey,
			byCoord: make(map[[2]int]int, len(release.Tracks)),
			rows:    make(map[uuid.UUID]*matchingExperimentRow, len(members)),
		}
		for _, track := range release.Tracks {
			if track.Medium < 0 || track.Position < 0 {
				return nil, 0, fmt.Errorf("matching experiment layout release %q position is invalid", release.ProviderKey)
			}
			built.positions = append(built.positions, matchingExperimentPosition{medium: track.Medium, position: track.Position})
		}
		sort.Slice(built.positions, func(i, j int) bool {
			if built.positions[i].medium != built.positions[j].medium {
				return built.positions[i].medium < built.positions[j].medium
			}
			return built.positions[i].position < built.positions[j].position
		})
		for index, position := range built.positions {
			coord := [2]int{position.medium, position.position}
			if _, exists := built.byCoord[coord]; exists {
				return nil, 0, fmt.Errorf("matching experiment layout release %q position is duplicated", release.ProviderKey)
			}
			built.byCoord[coord] = index
		}
		for memberID := range members {
			built.rows[memberID] = &matchingExperimentRow{
				id:             memberID.String(),
				locations:      append([]string(nil), locations[memberID]...),
				edgeByPosition: make(map[int]matchingExperimentEdge),
			}
			built.order = append(built.order, memberID)
		}
		sort.Slice(built.order, func(i, j int) bool { return built.order[i].String() < built.order[j].String() })
		releases[release.ProviderKey] = built
	}

	seenEdges := make(map[string]bool, len(input.Inputs))
	for _, candidate := range input.Inputs {
		if !members[candidate.FileID] {
			return nil, 0, fmt.Errorf("matching experiment layout edge references a foreign file member")
		}
		release, exists := releases[candidate.ReleaseID]
		if !exists {
			return nil, 0, fmt.Errorf("matching experiment layout edge references unknown release %q", candidate.ReleaseID)
		}
		position, exists := release.byCoord[[2]int{candidate.Medium, candidate.Track}]
		if !exists {
			return nil, 0, fmt.Errorf("matching experiment layout edge references unknown release position")
		}
		if candidate.ReleaseScore.Profile != ReleaseMatchingProfile || candidate.AssignmentScore.Profile != RecordingReleaseMatchingProfile {
			return nil, 0, fmt.Errorf("matching experiment layout edge has an invalid scoring profile")
		}
		if !validMatchingConfidence(candidate.ReleaseScore.Confidence) || !validMatchingConfidence(candidate.AssignmentScore.Confidence) {
			return nil, 0, fmt.Errorf("matching experiment layout edge has an invalid confidence")
		}
		if candidate.ReleaseScore.PolicyVersion != "" && candidate.ReleaseScore.PolicyVersion != MatchingPolicyVersion ||
			candidate.AssignmentScore.PolicyVersion != "" && candidate.AssignmentScore.PolicyVersion != MatchingPolicyVersion {
			return nil, 0, fmt.Errorf("matching experiment layout edge has an invalid scoring policy version")
		}
		identity := fmt.Sprintf("%s/%s/%d/%d", candidate.FileID, candidate.ReleaseID, candidate.Medium, candidate.Track)
		if seenEdges[identity] {
			return nil, 0, fmt.Errorf("matching experiment layout contains a duplicate file-release-position edge")
		}
		seenEdges[identity] = true

		row := release.rows[candidate.FileID]
		shift := matchingExperimentScoreShift
		if shifts != nil {
			shift = shifts[candidate.ReleaseID]
		}
		qualifying := candidate.AssignmentScore.Confidence >= threshold
		edge := matchingExperimentEdge{
			position:          position,
			candidateID:       fmt.Sprintf("%s/%d/%d", candidate.ReleaseID, candidate.Medium, candidate.Track),
			qualifying:        qualifying,
			releaseConfidence: candidate.ReleaseScore.Confidence,
		}
		if qualifying {
			edge.score = uint64(math.Ldexp(candidate.AssignmentScore.Confidence, shift))
		}
		row.edges = append(row.edges, edge)
		row.edgeByPosition[position] = edge
		if !row.releasePresent || edge.releaseConfidence > row.releaseBest {
			row.releasePresent = true
			row.releaseBest = edge.releaseConfidence
		}
	}

	releaseIDs := make([]string, 0, len(releases))
	for releaseID := range releases {
		releaseIDs = append(releaseIDs, releaseID)
	}
	sort.Strings(releaseIDs)
	layouts := make([]*matchingExperimentLayout, 0, len(releaseIDs))
	for _, releaseID := range releaseIDs {
		release := releases[releaseID]
		layout := &matchingExperimentLayout{
			releaseID: release.id,
			positions: release.positions,
			rows:      make([]*matchingExperimentRow, 0, len(release.order)),
		}
		for _, memberID := range release.order {
			row := release.rows[memberID]
			sort.Slice(row.edges, func(i, j int) bool { return row.edges[i].position < row.edges[j].position })
			layout.rows = append(layout.rows, row)
		}
		layout.components = matchingLayoutComponents(layout.rows)
		layouts = append(layouts, layout)
	}
	return layouts, len(input.Members), nil
}

func matchingExperimentCanonicalLocations(file IncomingFile) ([]string, error) {
	if len(file.Locations) == 0 {
		return nil, fmt.Errorf("matching experiment layout member %s has no containing folder", file.VariantID)
	}
	seen := make(map[string]bool, len(file.Locations))
	result := make([]string, 0, len(file.Locations))
	for _, location := range file.Locations {
		if location.RootID == uuid.Nil {
			return nil, fmt.Errorf("matching experiment layout member %s has an empty containing folder root", file.VariantID)
		}
		parent := strings.TrimPrefix(path.Clean("/"+path.Dir(location.RelativePath)), "/")
		key := location.RootID.String() + "\x00" + parent
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, key)
	}
	sort.Strings(result)
	return result, nil
}

func (layout *matchingExperimentLayout) report(ctx context.Context) (MatchingExperimentLayoutReport, error) {
	report := MatchingExperimentLayoutReport{
		ReleaseID:        layout.releaseID,
		ScoreStatus:      MatchingExperimentScoreNotEvaluated,
		StructuralReason: "",
	}
	report.ReleaseProfileGate, report.ReleaseProfileSum, report.ReleaseProfileMembers = matchingExperimentReleaseProfile(layout.rows)

	structuralIncomplete := false
	structuralIndeterminate := false
	scoreNoFull := false
	scoreIndeterminate := false
	withinTie := false
	uniquenessUnknown := false
	var structuralReason string
	var scoreReason string
	var bestScore uint64
	choices := make(map[string]string)

	for _, component := range layout.components {
		result, err := layout.evaluateComponent(ctx, component)
		if err != nil {
			return MatchingExperimentLayoutReport{}, err
		}
		if result.structuralIndeterminate {
			structuralIndeterminate = true
			if structuralReason == "" {
				structuralReason = result.structuralReason
			}
		} else if !result.structuralFull {
			structuralIncomplete = true
		}
		if result.scoreIndeterminate {
			scoreIndeterminate = true
			if scoreReason == "" {
				scoreReason = result.scoreReason
			}
			if result.scoreFull {
				bestScore += result.bestScore
			}
			continue
		}
		if !result.scoreFull {
			scoreNoFull = true
			continue
		}
		bestScore += result.bestScore
		uniquenessUnknown = uniquenessUnknown || result.uniquenessUnknown
		if result.withinTie {
			withinTie = true
		}
		for fileID, position := range result.choices {
			for _, row := range layout.rows {
				if row.id != fileID {
					continue
				}
				edge, exists := row.edgeByPosition[position]
				if exists {
					choices[fileID] = edge.candidateID
				}
				break
			}
		}
	}

	switch {
	case structuralIncomplete:
		report.StructuralStatus = MatchingExperimentStructuralIncomplete
		report.ScoreStatus = MatchingExperimentScoreNotEvaluated
		report.ScoreReason = "layout is structurally incomplete"
	case structuralIndeterminate:
		report.StructuralStatus = MatchingExperimentStructuralIndeterminate
		report.StructuralReason = structuralReason
		report.ScoreStatus = MatchingExperimentScoreNotEvaluated
		report.ScoreReason = "structural feasibility is indeterminate"
	default:
		report.StructuralStatus = MatchingExperimentStructuralFull
		report.StructuralFull = true
		switch {
		case scoreNoFull:
			report.ScoreStatus = MatchingExperimentScoreBelowThreshold
			report.ScoreReason = "no full assignment meets the combined-score floor"
		case scoreIndeterminate:
			report.ScoreStatus = MatchingExperimentScoreIndeterminate
			report.ScoreReason = scoreReason
			report.IndeterminateReason = scoreReason
		default:
			report.ScoreStatus = MatchingExperimentScoreQualified
			report.QualifiedFull = true
			report.BestFullScore = bestScore
			report.BestFullScoreFloat = float64(bestScore) / float64(uint64(1)<<matchingExperimentScoreShift)
			report.WithinLayoutTie = withinTie
			if withinTie {
				report.DistinctOptimalAssignments = 2
			} else if uniquenessUnknown {
				report.IndeterminateReason = "assignment uniqueness exceeds the deterministic clique cap"
			} else {
				report.DistinctOptimalAssignments = 1
				report.Choices = choices
			}
		}
	}
	if report.StructuralStatus == MatchingExperimentStructuralIndeterminate && report.IndeterminateReason == "" {
		report.IndeterminateReason = report.StructuralReason
	}
	return report, nil
}

func matchingExperimentReleaseProfile(rows []*matchingExperimentRow) (bool, float64, []MatchingExperimentReleaseProfileMember) {
	gate := true
	sum := new(big.Rat)
	members := make([]MatchingExperimentReleaseProfileMember, 0, len(rows))
	for _, row := range rows {
		member := MatchingExperimentReleaseProfileMember{FileID: row.id, Present: row.releasePresent, BestConfidence: row.releaseBest}
		members = append(members, member)
		if !row.releasePresent {
			gate = false
			continue
		}
		if row.releaseBest < minimumReleaseCandidateConfidence {
			gate = false
		}
		sum.Add(sum, new(big.Rat).SetFloat64(row.releaseBest))
	}
	value, _ := sum.Float64()
	return gate, value, members
}

func (layout *matchingExperimentLayout) evaluateComponent(ctx context.Context, indexes []int) (matchingExperimentComponentResult, error) {
	rows := make([]*matchingExperimentRow, len(indexes))
	for i, index := range indexes {
		rows[i] = layout.rows[index]
	}
	var result matchingExperimentComponentResult

	if matchingLayoutGreedyWitness(rows, len(layout.positions), false) {
		// A complete Cartesian layout is normally witnessed here without
		// entering either exact solver.
		result.structuralFull = true
	} else if matchingLayoutPairwiseConflicting(rows) {
		_, _, full := matchingLayoutHungarianAssignment(ctx, rows, len(layout.positions), false, -1, -1)
		if err := ctx.Err(); err != nil {
			return matchingExperimentComponentResult{}, err
		}
		result.structuralFull = full
	} else {
		found, indeterminate, reason, err := matchingLayoutAnyAssignment(ctx, rows, len(layout.positions), false)
		if err != nil {
			return matchingExperimentComponentResult{}, err
		}
		result.structuralFull = found
		result.structuralIndeterminate = indeterminate
		result.structuralReason = reason
	}
	if !result.structuralFull {
		return result, nil
	}

	if matchingLayoutPairwiseConflicting(rows) {
		choices, score, full := matchingLayoutHungarianAssignment(ctx, rows, len(layout.positions), true, -1, -1)
		if err := ctx.Err(); err != nil {
			return matchingExperimentComponentResult{}, err
		}
		if !full {
			return result, nil
		}
		result.scoreFull = true
		result.bestScore = score
		result.witness = make(map[string]int, len(rows))
		for i, row := range rows {
			result.witness[row.id] = choices[i]
		}
		if len(rows) > matchingLayoutMaximumCliqueRows {
			result.uniquenessUnknown = true
			return result, nil
		}
		for row, position := range choices {
			if err := ctx.Err(); err != nil {
				return matchingExperimentComponentResult{}, err
			}
			_, alternative, full := matchingLayoutHungarianAssignment(ctx, rows, len(layout.positions), true, row, position)
			if err := ctx.Err(); err != nil {
				return matchingExperimentComponentResult{}, err
			}
			if full && alternative == score {
				result.withinTie = true
				break
			}
		}
		if !result.withinTie {
			result.choices = result.witness
		}
		return result, nil
	}

	outcome, err := matchingLayoutBranchAndBound(ctx, rows, len(layout.positions))
	if err != nil {
		return matchingExperimentComponentResult{}, err
	}
	if outcome.indeterminate {
		result.scoreIndeterminate = true
		result.scoreReason = outcome.reason
		return result, nil
	}
	if !outcome.found {
		return result, nil
	}
	result.scoreFull = true
	result.bestScore = outcome.bestScore
	result.withinTie = outcome.withinTie
	if len(outcome.choices) != len(rows) {
		return matchingExperimentComponentResult{}, fmt.Errorf("matching layout assignment witness has %d rows, want %d", len(outcome.choices), len(rows))
	}
	result.witness = make(map[string]int, len(rows))
	for i, row := range rows {
		result.witness[row.id] = outcome.choices[i]
	}
	if !result.withinTie {
		result.choices = result.witness
	}
	return result, nil
}

// matchingExperimentHungarianAssignment retains the experiment's local name
// while the exact assignment solver is shared with production selection.
func matchingExperimentHungarianAssignment(
	ctx context.Context, rows []*matchingExperimentRow, positionCount int, qualifying bool, skipRow, skipPosition int,
) ([]int, uint64, bool) {
	return matchingLayoutHungarianAssignment(ctx, rows, positionCount, qualifying, skipRow, skipPosition)
}
