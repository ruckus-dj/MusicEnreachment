package service

import (
	"context"
	"fmt"
	"sort"
)

const (
	// matchingLayoutMaximumMembers keeps the exact scaled sum within int64:
	// 256 * 2^53 is at most 2^61.
	matchingLayoutMaximumMembers = 256
	// matchingLayoutMaximumCliqueRows bounds the per-alternative uniqueness
	// proof for a pairwise-conflicting component. Larger components still get an
	// exact optimum, but their uniqueness is reported as indeterminate.
	matchingLayoutMaximumCliqueRows = 48
	// matchingLayoutSearchNodeLimit is a deterministic work bound, not a
	// wall-clock timeout. Exhausting it is reported as indeterminate.
	matchingLayoutSearchNodeLimit = 1_000_000
	matchingLayoutForbiddenCost   = -1
)

// matchingLayoutPosition is one release position in canonical
// (medium, position) order. Both fields are zero-based persisted ordinals.
type matchingLayoutPosition struct {
	medium   int
	position int
}

// matchingLayoutEdge is one candidate file-to-position edge. The score is an
// exact integer in the caller's common binary scale; a caller that uses a
// different scale must compare scores through the same scale.
type matchingLayoutEdge struct {
	position          int
	candidateID       string
	score             uint64
	qualifying        bool
	releaseConfidence float64
}

// matchingLayoutRow is one file member and its candidate positions. release
// annotations are optional diagnostics used by the experiment report; the
// solver itself never reads them.
type matchingLayoutRow struct {
	id             string
	locations      []string
	edges          []matchingLayoutEdge
	edgeByPosition map[int]matchingLayoutEdge
	releasePresent bool
	releaseBest    float64
}

// matchingLayoutComponents groups rows that share a canonical containing
// folder. Positions may be reused across components, so each component is
// solved independently.
func matchingLayoutComponents(rows []*matchingLayoutRow) [][]int {
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

func matchingLayoutPairwiseConflicting(rows []*matchingLayoutRow) bool {
	for left := 0; left < len(rows); left++ {
		for right := left + 1; right < len(rows); right++ {
			if !matchingLayoutShareLocation(rows[left], rows[right]) {
				return false
			}
		}
	}
	return true
}

func matchingLayoutShareLocation(left, right *matchingLayoutRow) bool {
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

func matchingLayoutMaximumWeight(rows []*matchingLayoutRow, qualifying bool) uint64 {
	var maximum uint64
	for _, row := range rows {
		for _, edge := range row.edges {
			if qualifying && !edge.qualifying {
				continue
			}
			weight := matchingLayoutEdgeWeight(edge, qualifying)
			if weight > maximum {
				maximum = weight
			}
		}
	}
	return maximum
}

func matchingLayoutEdgeWeight(edge matchingLayoutEdge, qualifying bool) uint64 {
	if qualifying {
		return edge.score
	}
	return 1
}

// Missing edges are forbidden, not expensive dummy edges: a higher-scoring
// partial assignment must never displace a feasible full assignment.
func matchingLayoutHungarianAssignment(
	ctx context.Context, rows []*matchingLayoutRow, positionCount int, qualifying bool, skipRow, skipPosition int,
) ([]int, uint64, bool) {
	if positionCount < len(rows) || ctx.Err() != nil {
		return nil, 0, false
	}
	maxWeight := matchingLayoutMaximumWeight(rows, qualifying)
	cost := make([][]int64, len(rows))
	for i, row := range rows {
		cost[i] = make([]int64, positionCount)
		for j := range cost[i] {
			cost[i][j] = matchingLayoutForbiddenCost
		}
		for _, edge := range row.edges {
			if (qualifying && !edge.qualifying) || (i == skipRow && edge.position == skipPosition) {
				continue
			}
			cost[i][edge.position] = int64(maxWeight - matchingLayoutEdgeWeight(edge, qualifying))
		}
	}
	assignment := matchingLayoutHungarian(ctx, cost)
	if assignment == nil {
		return nil, 0, false
	}
	var score uint64
	for i, position := range assignment {
		score += matchingLayoutEdgeWeight(rows[i].edgeByPosition[position], qualifying)
	}
	return assignment, score, true
}

// matchingLayoutHungarian is the standard rectangular Hungarian algorithm for
// a minimum-cost assignment of every row to one distinct column. The input must
// have at least as many columns as rows.
func matchingLayoutHungarian(ctx context.Context, cost [][]int64) []int {
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
				if candidateCost != matchingLayoutForbiddenCost {
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

type matchingLayoutConflictState struct {
	rowLocations [][]int
	words        int
	used         []uint64
}

func matchingLayoutNewConflictState(rows []*matchingLayoutRow, positionCount int) *matchingLayoutConflictState {
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
	return &matchingLayoutConflictState{
		rowLocations: rowLocations,
		words:        words,
		used:         make([]uint64, len(indexByLocation)*words),
	}
}

func (state *matchingLayoutConflictState) available(row, position int) bool {
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

func (state *matchingLayoutConflictState) occupy(row, position int) {
	if state.words == 0 {
		return
	}
	bit := uint64(1) << uint(position%64)
	word := position / 64
	for _, location := range state.rowLocations[row] {
		state.used[location*state.words+word] |= bit
	}
}

func (state *matchingLayoutConflictState) release(row, position int) {
	if state.words == 0 {
		return
	}
	bit := uint64(1) << uint(position%64)
	word := position / 64
	for _, location := range state.rowLocations[row] {
		state.used[location*state.words+word] &^= bit
	}
}

func matchingLayoutGreedyWitness(rows []*matchingLayoutRow, positionCount int, qualifying bool) bool {
	state := matchingLayoutNewConflictState(rows, positionCount)
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

type matchingLayoutFeasibility struct {
	rows          []*matchingLayoutRow
	state         *matchingLayoutConflictState
	qualifying    bool
	assigned      []bool
	nodes         int
	ctx           context.Context
	found         bool
	indeterminate bool
	reason        string
	err           error
}

func matchingLayoutAnyAssignment(ctx context.Context, rows []*matchingLayoutRow, positionCount int, qualifying bool) (bool, bool, string, error) {
	search := &matchingLayoutFeasibility{
		rows:       rows,
		state:      matchingLayoutNewConflictState(rows, positionCount),
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

func (search *matchingLayoutFeasibility) walk() {
	if search.found || search.indeterminate || search.err != nil {
		return
	}
	search.nodes++
	if search.nodes > matchingLayoutSearchNodeLimit {
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

type matchingLayoutBranchOutcome struct {
	found         bool
	indeterminate bool
	reason        string
	bestScore     uint64
	withinTie     bool
	choices       []int
}

type matchingLayoutBranch struct {
	rows          []*matchingLayoutRow
	state         *matchingLayoutConflictState
	positions     int
	branch        [][]matchingLayoutEdge
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

func matchingLayoutBranchAndBound(ctx context.Context, rows []*matchingLayoutRow, positionCount int) (matchingLayoutBranchOutcome, error) {
	return matchingLayoutBranchAndBoundFixed(ctx, rows, positionCount, nil)
}

// matchingLayoutBranchAndBoundFixed solves the maximum-weight assignment with
// an optional fixed prefix. A fixed entry of -1 leaves the row unassigned.
func matchingLayoutBranchAndBoundFixed(
	ctx context.Context, rows []*matchingLayoutRow, positionCount int, fixed []int,
) (matchingLayoutBranchOutcome, error) {
	if len(fixed) != 0 && len(fixed) != len(rows) {
		return matchingLayoutBranchOutcome{}, fmt.Errorf("matching layout fixed assignment has %d rows, want %d", len(fixed), len(rows))
	}
	branch := &matchingLayoutBranch{
		rows:      rows,
		state:     matchingLayoutNewConflictState(rows, positionCount),
		positions: positionCount,
		branch:    make([][]matchingLayoutEdge, len(rows)),
		assigned:  make([]int, len(rows)),
		ctx:       ctx,
	}
	for index := range branch.assigned {
		branch.assigned[index] = -1
	}
	for index, row := range rows {
		edges := make([]matchingLayoutEdge, 0, len(row.edges))
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
	var prefix uint64
	for index, position := range fixed {
		if position < 0 {
			continue
		}
		edge, exists := rows[index].edgeByPosition[position]
		if !exists || !edge.qualifying {
			return matchingLayoutBranchOutcome{}, fmt.Errorf("matching layout fixed assignment references an ineligible edge")
		}
		if !branch.state.available(index, position) {
			return matchingLayoutBranchOutcome{}, fmt.Errorf("matching layout fixed assignment conflicts with a containing folder")
		}
		branch.assigned[index] = position
		branch.state.occupy(index, position)
		prefix += edge.score
	}
	branch.seedGreedy(fixed)
	branch.walk(prefix)
	if branch.err != nil {
		return matchingLayoutBranchOutcome{}, branch.err
	}
	if branch.indeterminate {
		return matchingLayoutBranchOutcome{indeterminate: true, reason: branch.reason}, nil
	}
	if !branch.bestSet {
		return matchingLayoutBranchOutcome{}, nil
	}
	// bestChoices is the first optimum found by the canonical search order.
	// Returning it on a tie keeps production selection deterministic without
	// an extra lexicographic search; uniqueness remains reported separately.
	return matchingLayoutBranchOutcome{
		found:     true,
		bestScore: branch.bestScore,
		withinTie: branch.bestCount >= 2,
		choices:   append([]int(nil), branch.bestChoices...),
	}, nil
}

func (branch *matchingLayoutBranch) seedGreedy(fixed []int) {
	state := matchingLayoutNewConflictState(branch.rows, branch.positions)
	choices := make([]int, len(branch.rows))
	for index := range choices {
		choices[index] = -1
	}
	var score uint64
	for index, position := range fixed {
		if position < 0 {
			continue
		}
		edge, exists := branch.rows[index].edgeByPosition[position]
		if !exists || !edge.qualifying || !state.available(index, position) {
			return
		}
		state.occupy(index, position)
		choices[index] = position
		score += edge.score
	}
	for rowIndex := range branch.rows {
		if choices[rowIndex] >= 0 {
			continue
		}
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

func (branch *matchingLayoutBranch) seed(score uint64, choices []int) {
	branch.bestSet = true
	branch.bestScore = score
	branch.bestCount = 0
	branch.bestChoices = append([]int(nil), choices...)
}

func (branch *matchingLayoutBranch) record(score uint64, choices []int) {
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

func (branch *matchingLayoutBranch) walk(current uint64) {
	if branch.indeterminate || branch.err != nil {
		return
	}
	branch.nodes++
	if branch.nodes > matchingLayoutSearchNodeLimit {
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

func (branch *matchingLayoutBranch) selectRow() (int, int, uint64) {
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
