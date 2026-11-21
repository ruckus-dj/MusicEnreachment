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
	// matchingExperimentMaximumMembers keeps the exact scaled sum within int64:
	// 256 * 2^53 is at most 2^61.
	matchingExperimentMaximumMembers = 256
	// matchingExperimentMaximumCliqueRows bounds the per-alternative uniqueness
	// proof for a pairwise-conflicting component. Larger components still get an
	// exact optimum, but their uniqueness is reported as indeterminate.
	matchingExperimentMaximumCliqueRows = 48
	// matchingExperimentSearchNodeLimit is a deterministic work bound, not a
	// wall-clock timeout. Exhausting it is reported as indeterminate.
	matchingExperimentSearchNodeLimit = 1_000_000
	matchingExperimentScoreShift      = 53
	matchingExperimentForbiddenCost   = -1
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

type matchingExperimentPosition struct {
	medium   int
	position int
}

type matchingExperimentEdge struct {
	position          int
	candidateID       string
	score             uint64
	qualifying        bool
	releaseConfidence float64
}

type matchingExperimentRow struct {
	id             string
	locations      []string
	edges          []matchingExperimentEdge
	edgeByPosition map[int]matchingExperimentEdge
	releasePresent bool
	releaseBest    float64
}

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
	choices            map[string]int
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
		edge := matchingExperimentEdge{
			position:          position,
			candidateID:       fmt.Sprintf("%s/%d/%d", candidate.ReleaseID, candidate.Medium, candidate.Track),
			score:             uint64(math.Ldexp(candidate.AssignmentScore.Confidence, matchingExperimentScoreShift)),
			qualifying:        candidate.AssignmentScore.Confidence >= minimumReleaseCandidateConfidence,
			releaseConfidence: candidate.ReleaseScore.Confidence,
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
		layout.components = matchingExperimentComponents(layout.rows)
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

func matchingExperimentComponents(rows []*matchingExperimentRow) [][]int {
	parent := make([]int, len(rows))
	for i := range parent {
		parent[i] = i
	}
	find := func(value int) int {
		for parent[value] != value {
			parent[value] = parent[parent[value]]
			value = parent[value]
		}
		return value
	}
	union := func(left, right int) {
		left, right = find(left), find(right)
		if left != right {
			parent[right] = left
		}
	}
	firstByLocation := make(map[string]int)
	for index, row := range rows {
		for _, location := range row.locations {
			if first, exists := firstByLocation[location]; exists {
				union(first, index)
			} else {
				firstByLocation[location] = index
			}
		}
	}
	groups := make(map[int][]int)
	for index := range rows {
		root := find(index)
		groups[root] = append(groups[root], index)
	}
	components := make([][]int, 0, len(groups))
	for _, component := range groups {
		sort.Ints(component)
		components = append(components, component)
	}
	sort.Slice(components, func(i, j int) bool {
		return rows[components[i][0]].id < rows[components[j][0]].id
	})
	return components
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

	if matchingExperimentGreedyWitness(rows, len(layout.positions), false) {
		// A complete Cartesian layout is normally witnessed here without
		// entering either exact solver.
		result.structuralFull = true
	} else if matchingExperimentPairwiseConflicting(rows) {
		_, _, full := matchingExperimentHungarianAssignment(ctx, rows, len(layout.positions), false, -1, -1)
		if err := ctx.Err(); err != nil {
			return matchingExperimentComponentResult{}, err
		}
		result.structuralFull = full
	} else {
		found, indeterminate, reason, err := matchingExperimentAnyAssignment(ctx, rows, len(layout.positions), false)
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

	if matchingExperimentPairwiseConflicting(rows) {
		choices, score, full := matchingExperimentHungarianAssignment(ctx, rows, len(layout.positions), true, -1, -1)
		if err := ctx.Err(); err != nil {
			return matchingExperimentComponentResult{}, err
		}
		if !full {
			return result, nil
		}
		result.scoreFull = true
		result.bestScore = score
		if len(rows) > matchingExperimentMaximumCliqueRows {
			result.uniquenessUnknown = true
			return result, nil
		}
		for row, position := range choices {
			if err := ctx.Err(); err != nil {
				return matchingExperimentComponentResult{}, err
			}
			_, alternative, full := matchingExperimentHungarianAssignment(ctx, rows, len(layout.positions), true, row, position)
			if err := ctx.Err(); err != nil {
				return matchingExperimentComponentResult{}, err
			}
			if full && alternative == score {
				result.withinTie = true
				break
			}
		}
		if !result.withinTie {
			result.choices = make(map[string]int, len(rows))
			for i, row := range rows {
				result.choices[row.id] = choices[i]
			}
		}
		return result, nil
	}

	outcome, err := matchingExperimentBranchAndBound(ctx, rows, len(layout.positions))
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
	if !result.withinTie {
		result.choices = make(map[string]int, len(rows))
		for i, row := range rows {
			result.choices[row.id] = outcome.choices[i]
		}
	}
	return result, nil
}

func matchingExperimentPairwiseConflicting(rows []*matchingExperimentRow) bool {
	for left := 0; left < len(rows); left++ {
		for right := left + 1; right < len(rows); right++ {
			if !matchingExperimentShareLocation(rows[left], rows[right]) {
				return false
			}
		}
	}
	return true
}

func matchingExperimentShareLocation(left, right *matchingExperimentRow) bool {
	for i, j := 0, 0; i < len(left.locations) && j < len(right.locations); {
		switch {
		case left.locations[i] < right.locations[j]:
			i++
		case left.locations[i] > right.locations[j]:
			j++
		default:
			return true
		}
	}
	return false
}

func matchingExperimentMaximumWeight(rows []*matchingExperimentRow, qualifying bool) uint64 {
	var maximum uint64
	for _, row := range rows {
		for _, edge := range row.edges {
			if qualifying && !edge.qualifying {
				continue
			}
			weight := matchingExperimentEdgeWeight(edge, qualifying)
			if weight > maximum {
				maximum = weight
			}
		}
	}
	return maximum
}

func matchingExperimentEdgeWeight(edge matchingExperimentEdge, qualifying bool) uint64 {
	if qualifying {
		return edge.score
	}
	return 1
}

// Missing edges are forbidden, not expensive dummy edges: a higher-scoring
// partial assignment must never displace a feasible full assignment.
func matchingExperimentHungarianAssignment(
	ctx context.Context, rows []*matchingExperimentRow, positionCount int, qualifying bool, skipRow, skipPosition int,
) ([]int, uint64, bool) {
	if positionCount < len(rows) || ctx.Err() != nil {
		return nil, 0, false
	}
	maxWeight := matchingExperimentMaximumWeight(rows, qualifying)
	cost := make([][]int64, len(rows))
	for i, row := range rows {
		cost[i] = make([]int64, positionCount)
		for j := range cost[i] {
			cost[i][j] = matchingExperimentForbiddenCost
		}
		for _, edge := range row.edges {
			if (qualifying && !edge.qualifying) || (i == skipRow && edge.position == skipPosition) {
				continue
			}
			cost[i][edge.position] = int64(maxWeight - matchingExperimentEdgeWeight(edge, qualifying))
		}
	}
	assignment := matchingExperimentHungarian(ctx, cost)
	if assignment == nil {
		return nil, 0, false
	}
	var score uint64
	for i, position := range assignment {
		score += matchingExperimentEdgeWeight(rows[i].edgeByPosition[position], qualifying)
	}
	return assignment, score, true
}

// matchingExperimentHungarian is the standard rectangular Hungarian algorithm
// for a minimum-cost assignment of every row to one distinct column. The input
// must have at least as many columns as rows.
func matchingExperimentHungarian(ctx context.Context, cost [][]int64) []int {
	rows := len(cost)
	if rows == 0 {
		return []int{}
	}
	columns := len(cost[0])
	if columns < rows {
		return nil
	}
	const infinity = int64(1) << 62
	potentialRow := make([]int64, rows+1)
	potentialColumn := make([]int64, columns+1)
	matchedRow := make([]int, columns+1)
	previousColumn := make([]int, columns+1)
	for row := 1; row <= rows; row++ {
		if ctx.Err() != nil {
			return nil
		}
		matchedRow[0] = row
		column := 0
		minValue := make([]int64, columns+1)
		used := make([]bool, columns+1)
		for j := 1; j <= columns; j++ {
			minValue[j] = infinity
		}
		for {
			used[column] = true
			currentRow := matchedRow[column]
			delta := infinity
			nextColumn := 0
			for j := 1; j <= columns; j++ {
				if used[j] {
					continue
				}
				candidateCost := cost[currentRow-1][j-1]
				if candidateCost != matchingExperimentForbiddenCost {
					current := candidateCost - potentialRow[currentRow] - potentialColumn[j]
					if current < minValue[j] {
						minValue[j] = current
						previousColumn[j] = column
					}
				}
				if minValue[j] < delta {
					delta = minValue[j]
					nextColumn = j
				}
			}
			if nextColumn == 0 {
				return nil
			}
			for j := 0; j <= columns; j++ {
				if used[j] {
					potentialRow[matchedRow[j]] += delta
					potentialColumn[j] -= delta
				} else if minValue[j] != infinity {
					minValue[j] -= delta
				}
			}
			column = nextColumn
			if matchedRow[column] == 0 {
				break
			}
		}
		for {
			nextColumn := previousColumn[column]
			matchedRow[column] = matchedRow[nextColumn]
			column = nextColumn
			if column == 0 {
				break
			}
		}
	}
	assignment := make([]int, rows)
	for column := 1; column <= columns; column++ {
		if matchedRow[column] != 0 {
			assignment[matchedRow[column]-1] = column - 1
		}
	}
	return assignment
}

type matchingExperimentConflictState struct {
	rowLocations [][]int
	words        int
	used         []uint64
}

func matchingExperimentNewConflictState(rows []*matchingExperimentRow, positionCount int) *matchingExperimentConflictState {
	indexByLocation := make(map[string]int)
	rowLocations := make([][]int, len(rows))
	for rowIndex, row := range rows {
		for _, location := range row.locations {
			index, exists := indexByLocation[location]
			if !exists {
				index = len(indexByLocation)
				indexByLocation[location] = index
			}
			rowLocations[rowIndex] = append(rowLocations[rowIndex], index)
		}
	}
	words := (positionCount + 63) / 64
	return &matchingExperimentConflictState{
		rowLocations: rowLocations,
		words:        words,
		used:         make([]uint64, len(indexByLocation)*words),
	}
}

func (state *matchingExperimentConflictState) available(row, position int) bool {
	if state.words == 0 {
		return false
	}
	bit := uint64(1) << uint(position%64)
	word := position / 64
	for _, location := range state.rowLocations[row] {
		if state.used[location*state.words+word]&bit != 0 {
			return false
		}
	}
	return true
}

func (state *matchingExperimentConflictState) occupy(row, position int) {
	if state.words == 0 {
		return
	}
	bit := uint64(1) << uint(position%64)
	word := position / 64
	for _, location := range state.rowLocations[row] {
		state.used[location*state.words+word] |= bit
	}
}

func (state *matchingExperimentConflictState) release(row, position int) {
	if state.words == 0 {
		return
	}
	bit := uint64(1) << uint(position%64)
	word := position / 64
	for _, location := range state.rowLocations[row] {
		state.used[location*state.words+word] &^= bit
	}
}

func matchingExperimentGreedyWitness(rows []*matchingExperimentRow, positionCount int, qualifying bool) bool {
	state := matchingExperimentNewConflictState(rows, positionCount)
	for rowIndex, row := range rows {
		found := false
		for _, edge := range row.edges {
			if qualifying && !edge.qualifying {
				continue
			}
			if !state.available(rowIndex, edge.position) {
				continue
			}
			state.occupy(rowIndex, edge.position)
			found = true
			break
		}
		if !found {
			return false
		}
	}
	return true
}

type matchingExperimentFeasibility struct {
	rows          []*matchingExperimentRow
	state         *matchingExperimentConflictState
	qualifying    bool
	assigned      []bool
	nodes         int
	ctx           context.Context
	found         bool
	indeterminate bool
	reason        string
	err           error
}

func matchingExperimentAnyAssignment(ctx context.Context, rows []*matchingExperimentRow, positionCount int, qualifying bool) (bool, bool, string, error) {
	search := &matchingExperimentFeasibility{
		rows:       rows,
		state:      matchingExperimentNewConflictState(rows, positionCount),
		qualifying: qualifying,
		assigned:   make([]bool, len(rows)),
		ctx:        ctx,
	}
	search.walk()
	if search.err != nil {
		return false, false, "", search.err
	}
	if search.indeterminate {
		return false, true, search.reason, nil
	}
	return search.found, false, "", nil
}

func (search *matchingExperimentFeasibility) walk() {
	if search.found || search.indeterminate || search.err != nil {
		return
	}
	search.nodes++
	if search.nodes > matchingExperimentSearchNodeLimit {
		search.indeterminate = true
		search.reason = "deterministic structural search limit reached"
		return
	}
	if search.nodes&1023 == 0 {
		if err := search.ctx.Err(); err != nil {
			search.err = err
			return
		}
	}
	rowIndex := -1
	bestCount := len(search.rows) + 1
	for index, row := range search.rows {
		if search.assigned[index] {
			continue
		}
		count := 0
		for _, edge := range row.edges {
			if search.qualifying && !edge.qualifying {
				continue
			}
			if search.state.available(index, edge.position) {
				count++
			}
		}
		if rowIndex == -1 || count < bestCount {
			bestCount = count
			rowIndex = index
			if count == 0 {
				break
			}
		}
	}
	if rowIndex == -1 {
		search.found = true
		return
	}
	if bestCount == 0 {
		return
	}
	for _, edge := range search.rows[rowIndex].edges {
		if search.qualifying && !edge.qualifying {
			continue
		}
		if !search.state.available(rowIndex, edge.position) {
			continue
		}
		search.assigned[rowIndex] = true
		search.state.occupy(rowIndex, edge.position)
		search.walk()
		search.state.release(rowIndex, edge.position)
		search.assigned[rowIndex] = false
		if search.found || search.indeterminate || search.err != nil {
			return
		}
	}
}

type matchingExperimentBranchOutcome struct {
	found         bool
	indeterminate bool
	reason        string
	bestScore     uint64
	withinTie     bool
	choices       []int
}

type matchingExperimentBranch struct {
	rows          []*matchingExperimentRow
	state         *matchingExperimentConflictState
	positions     int
	branch        [][]matchingExperimentEdge
	assigned      []int
	nodes         int
	ctx           context.Context
	bestSet       bool
	bestScore     uint64
	bestCount     int
	bestChoices   []int
	indeterminate bool
	reason        string
	err           error
}

func matchingExperimentBranchAndBound(ctx context.Context, rows []*matchingExperimentRow, positionCount int) (matchingExperimentBranchOutcome, error) {
	branch := &matchingExperimentBranch{
		rows:      rows,
		state:     matchingExperimentNewConflictState(rows, positionCount),
		positions: positionCount,
		branch:    make([][]matchingExperimentEdge, len(rows)),
		assigned:  make([]int, len(rows)),
		ctx:       ctx,
	}
	for index := range branch.assigned {
		branch.assigned[index] = -1
	}
	for index, row := range rows {
		edges := make([]matchingExperimentEdge, 0, len(row.edges))
		for _, edge := range row.edges {
			if edge.qualifying {
				edges = append(edges, edge)
			}
		}
		sort.SliceStable(edges, func(i, j int) bool {
			if edges[i].score != edges[j].score {
				return edges[i].score > edges[j].score
			}
			return edges[i].position < edges[j].position
		})
		branch.branch[index] = edges
	}
	branch.seedGreedy()
	branch.walk(0)
	if branch.err != nil {
		return matchingExperimentBranchOutcome{}, branch.err
	}
	if branch.indeterminate {
		return matchingExperimentBranchOutcome{indeterminate: true, reason: branch.reason}, nil
	}
	if !branch.bestSet {
		return matchingExperimentBranchOutcome{}, nil
	}
	withinTie := branch.bestCount >= 2
	if withinTie {
		return matchingExperimentBranchOutcome{found: true, bestScore: branch.bestScore, withinTie: true}, nil
	}
	return matchingExperimentBranchOutcome{
		found: true, bestScore: branch.bestScore,
		choices: append([]int(nil), branch.bestChoices...),
	}, nil
}

func (branch *matchingExperimentBranch) seedGreedy() {
	state := matchingExperimentNewConflictState(branch.rows, branch.positions)
	choices := make([]int, len(branch.rows))
	for index := range choices {
		choices[index] = -1
	}
	var score uint64
	for rowIndex := range branch.rows {
		found := false
		for _, edge := range branch.branch[rowIndex] {
			if !state.available(rowIndex, edge.position) {
				continue
			}
			state.occupy(rowIndex, edge.position)
			choices[rowIndex] = edge.position
			score += edge.score
			found = true
			break
		}
		if !found {
			return
		}
	}
	branch.seed(score, choices)
}

func (branch *matchingExperimentBranch) seed(score uint64, choices []int) {
	branch.bestSet = true
	branch.bestScore = score
	branch.bestCount = 0
	branch.bestChoices = append([]int(nil), choices...)
}

func (branch *matchingExperimentBranch) record(score uint64, choices []int) {
	if !branch.bestSet || score > branch.bestScore {
		branch.bestSet = true
		branch.bestScore = score
		branch.bestCount = 1
		branch.bestChoices = append([]int(nil), choices...)
		return
	}
	if score == branch.bestScore && branch.bestCount < 2 {
		branch.bestCount++
	}
}

func (branch *matchingExperimentBranch) walk(current uint64) {
	if branch.indeterminate || branch.err != nil {
		return
	}
	branch.nodes++
	if branch.nodes > matchingExperimentSearchNodeLimit {
		branch.indeterminate = true
		branch.reason = "deterministic assignment search limit reached"
		return
	}
	if branch.nodes&1023 == 0 {
		if err := branch.ctx.Err(); err != nil {
			branch.err = err
			return
		}
	}
	rowIndex, available, upper := branch.selectRow()
	if available == 0 {
		return
	}
	total := current + upper
	if branch.bestSet {
		if total < branch.bestScore {
			return
		}
		if total == branch.bestScore && branch.bestCount >= 2 {
			return
		}
	}
	if rowIndex == -1 {
		branch.record(current, branch.assigned)
		return
	}
	for _, edge := range branch.branch[rowIndex] {
		if !branch.state.available(rowIndex, edge.position) {
			continue
		}
		branch.assigned[rowIndex] = edge.position
		branch.state.occupy(rowIndex, edge.position)
		branch.walk(current + edge.score)
		branch.state.release(rowIndex, edge.position)
		branch.assigned[rowIndex] = -1
		if branch.indeterminate || branch.err != nil {
			return
		}
		if branch.bestSet && branch.bestCount >= 2 && total == branch.bestScore {
			return
		}
	}
}

func (branch *matchingExperimentBranch) selectRow() (int, int, uint64) {
	rowIndex := -1
	bestCount := len(branch.rows) + 1
	var upper uint64
	for index := range branch.rows {
		if branch.assigned[index] >= 0 {
			continue
		}
		count := 0
		var maximum uint64
		for _, edge := range branch.branch[index] {
			if !branch.state.available(index, edge.position) {
				continue
			}
			count++
			if edge.score > maximum {
				maximum = edge.score
			}
		}
		if count == 0 {
			return index, 0, 0
		}
		upper += maximum
		if rowIndex == -1 || count < bestCount {
			bestCount = count
			rowIndex = index
		}
	}
	return rowIndex, bestCount, upper
}
