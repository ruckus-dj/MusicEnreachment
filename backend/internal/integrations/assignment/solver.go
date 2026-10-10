// Package assignment implements exact weighted assignment using a pure-Go SAT
// solver and Boolean-circuit objective encoding.
package assignment

import (
	"context"
	"fmt"
	"math"
	"math/bits"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/go-air/gini"
	"github.com/go-air/gini/logic"
	"github.com/go-air/gini/z"
)

// Location identifies a folder occupied by a candidate assignment. Repeated
// locations on one candidate are collapsed using RootID and RelativeParent.
type Location struct {
	RootID         string
	RelativeParent string
}

// Candidate is one possible position assignment for a file variant.
type Candidate struct {
	FileID     string
	ID         string
	PositionID string
	Confidence float64
	Locations  []Location
}

// Result contains the unique maximum-confidence mapping. Choices maps each
// file ID to the selected candidate ID.
type Result struct {
	Choices map[string]string
	Unique  bool
}

// Solve requires exactly one candidate for every input file and at most one
// assignment to each position in each folder.
func Solve(ctx context.Context, candidates []Candidate) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	files, fileCandidates, fileLocations, err := validate(candidates)
	if err != nil {
		return Result{}, err
	}
	if len(files) == 0 {
		return Result{Choices: map[string]string{}, Unique: true}, nil
	}

	circuit := logic.NewC()
	edges := make([]z.Lit, len(candidates))
	constraints := make([]z.Lit, 0)
	for i := range edges {
		edges[i] = circuit.Lit()
	}
	for _, file := range files {
		vars := make([]z.Lit, 0, len(fileCandidates[file]))
		for _, index := range fileCandidates[file] {
			vars = append(vars, edges[index])
		}
		sort.Slice(vars, func(i, j int) bool { return vars[i] < vars[j] })
		card := circuit.CardSort(vars)
		constraints = append(constraints, card.Geq(1), card.Leq(1))
	}

	bySlot := make(map[string][]z.Lit)
	for i, candidate := range candidates {
		for _, location := range fileLocations[candidate.FileID] {
			slot := encode(location.RootID, location.RelativeParent, candidate.PositionID)
			bySlot[slot] = append(bySlot[slot], edges[i])
		}
	}
	for _, vars := range bySlot {
		if len(vars) > 1 {
			constraints = append(constraints, circuit.CardSort(vars).Leq(1))
		}
	}

	width := 54 + bits.Len(uint(len(files)-1))
	words := make([][]z.Lit, len(files))
	for fileIndex, file := range files {
		word := make([]z.Lit, 54)
		for bit := 0; bit < 54; bit++ {
			terms := make([]z.Lit, 0, len(fileCandidates[file]))
			for _, index := range fileCandidates[file] {
				if scoreInteger(candidates[index].Confidence)&(uint64(1)<<bit) != 0 {
					terms = append(terms, edges[index])
				}
			}
			word[bit] = circuit.Ors(terms...)
		}
		words[fileIndex] = word
	}
	objective := words[0]
	for _, word := range words[1:] {
		objective = addWords(circuit, objective, word, width)
	}

	solver := gini.New()
	circuit.ToCnf(solver)
	// Assert every cardinality constraint; all gates were encoded by ToCnf.
	for _, constraint := range constraints {
		solver.Add(constraint)
		solver.Add(0)
	}

	prefix := make([]z.Lit, 0, width)
	for bit := width - 1; bit >= 0; bit-- {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		trial := append(append([]z.Lit(nil), prefix...), objective[bit])
		status, err := solve(ctx, solver, trial)
		if err != nil {
			return Result{}, err
		}
		if status == 1 {
			prefix = append(prefix, objective[bit])
		} else {
			prefix = append(prefix, objective[bit].Not())
		}
	}
	status, err := solve(ctx, solver, prefix)
	if err != nil {
		return Result{}, err
	}
	if status != 1 {
		return Result{}, fmt.Errorf("assignment objective became infeasible")
	}
	choices := make(map[string]string, len(files))
	selected := make([]z.Lit, 0, len(files))
	for _, file := range files {
		for _, index := range fileCandidates[file] {
			if solver.Value(edges[index]) {
				choices[file] = candidates[index].ID
				selected = append(selected, edges[index])
				break
			}
		}
	}
	if len(choices) != len(files) {
		return Result{}, fmt.Errorf("SAT solver returned an incomplete assignment")
	}
	block := make([]z.Lit, len(selected))
	for i, lit := range selected {
		block[i] = lit.Not()
	}
	for _, lit := range block {
		solver.Add(lit)
	}
	solver.Add(0)
	status, err = solve(ctx, solver, prefix)
	if err != nil {
		return Result{}, err
	}
	if status == 1 {
		return Result{Unique: false}, nil
	}
	return Result{Choices: choices, Unique: true}, nil
}

func validate(candidates []Candidate) ([]string, map[string][]int, map[string][]Location, error) {
	fileCandidates := make(map[string][]int)
	fileLocations := make(map[string][]Location)
	seen := make(map[struct{ file, id string }]struct{}, len(candidates))
	for i, candidate := range candidates {
		if candidate.FileID == "" || candidate.ID == "" || candidate.PositionID == "" {
			return nil, nil, nil, fmt.Errorf("assignment candidate identifiers must not be empty")
		}
		if math.IsNaN(candidate.Confidence) || math.IsInf(candidate.Confidence, 0) || candidate.Confidence < 0.70 || candidate.Confidence > 1 {
			return nil, nil, nil, fmt.Errorf("assignment candidate %q confidence must be in [0.70, 1]", candidate.ID)
		}
		key := struct{ file, id string }{candidate.FileID, candidate.ID}
		if _, ok := seen[key]; ok {
			return nil, nil, nil, fmt.Errorf("duplicate assignment candidate %q for file %q", candidate.ID, candidate.FileID)
		}
		seen[key] = struct{}{}
		locations, err := canonicalLocations(candidate.Locations)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("assignment candidate %q: %w", candidate.ID, err)
		}
		if len(locations) == 0 {
			return nil, nil, nil, fmt.Errorf("assignment candidate %q has no containing folders", candidate.ID)
		}
		if prior, ok := fileLocations[candidate.FileID]; ok {
			if !sameLocations(prior, locations) {
				return nil, nil, nil, fmt.Errorf("assignment candidates for file %q have inconsistent containing folders", candidate.FileID)
			}
		} else {
			fileLocations[candidate.FileID] = locations
		}
		fileCandidates[candidate.FileID] = append(fileCandidates[candidate.FileID], i)
	}
	files := make([]string, 0, len(fileCandidates))
	for file := range fileCandidates {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, indexes := range fileCandidates {
		sort.Slice(indexes, func(i, j int) bool { return candidates[indexes[i]].ID < candidates[indexes[j]].ID })
	}
	return files, fileCandidates, fileLocations, nil
}

func canonicalLocations(locations []Location) ([]Location, error) {
	seen := make(map[string]struct{}, len(locations))
	result := make([]Location, 0, len(locations))
	for _, location := range locations {
		location.RootID = strings.TrimSpace(location.RootID)
		if location.RootID == "" {
			return nil, fmt.Errorf("containing folder root ID is empty")
		}
		location.RelativeParent = strings.TrimPrefix(path.Clean("/"+location.RelativeParent), "/")
		key := encode(location.RootID, location.RelativeParent)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	sort.Slice(result, func(i, j int) bool {
		return encode(result[i].RootID, result[i].RelativeParent) < encode(result[j].RootID, result[j].RelativeParent)
	})
	return result, nil
}

func sameLocations(left, right []Location) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func encode(parts ...string) string {
	var key string
	for _, part := range parts {
		key += fmt.Sprintf("%d:%s", len(part), part)
	}
	return key
}

func scoreInteger(score float64) uint64 { return uint64(math.Ldexp(score, 53)) }

func addWords(c *logic.C, a, b []z.Lit, width int) []z.Lit {
	result := make([]z.Lit, width)
	carry := c.F
	for bit := 0; bit < width; bit++ {
		x, y := c.F, c.F
		if bit < len(a) {
			x = a[bit]
		}
		if bit < len(b) {
			y = b[bit]
		}
		propagate := c.Xor(x, y)
		result[bit] = c.Xor(propagate, carry)
		carry = c.Or(c.And(x, y), c.And(propagate, carry))
	}
	return result
}

func solve(ctx context.Context, solver *gini.Gini, assumptions []z.Lit) (int, error) {
	return solveWithPending(ctx, solver, assumptions, nil)
}

func solveWithPending(ctx context.Context, solver *gini.Gini, assumptions []z.Lit, onPending func()) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	solver.Assume(assumptions...)
	search := solver.GoSolve()
	timer := time.NewTimer(time.Millisecond)
	defer timer.Stop()
	pendingObserved := false
	for {
		select {
		case <-ctx.Done():
			search.Stop()
			return 0, ctx.Err()
		case <-timer.C:
			if result, ready := search.Test(); ready {
				if result == 0 {
					return 0, fmt.Errorf("SAT solver stopped without a result")
				}
				return result, nil
			}
			if !pendingObserved {
				pendingObserved = true
				if onPending != nil {
					onPending()
				}
			}
			timer.Reset(time.Millisecond)
		}
	}
}
