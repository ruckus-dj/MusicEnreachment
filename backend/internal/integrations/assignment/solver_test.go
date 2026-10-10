package assignment

import (
	"context"
	"math"
	"math/big"
	"testing"

	"github.com/go-air/gini"
	"github.com/go-air/gini/logic"
	"github.com/go-air/gini/z"
)

func TestSolveAllowsCrossFolderReuseButEnforcesEverySharedFolder(t *testing.T) {
	locations := []Location{{RootID: "root", RelativeParent: "one"}, {RootID: "root", RelativeParent: "two"}}
	candidates := []Candidate{
		{FileID: "a", ID: "a-high", PositionID: "p1", Confidence: .95, Locations: locations},
		{FileID: "a", ID: "a-low", PositionID: "p2", Confidence: .80, Locations: locations},
		{FileID: "b", ID: "b-high", PositionID: "p1", Confidence: .94, Locations: locations},
		{FileID: "b", ID: "b-low", PositionID: "p2", Confidence: .80, Locations: locations},
		// This position ID is shared but lives in another folder, so it is reusable.
		{FileID: "c", ID: "c-only", PositionID: "p1", Confidence: .90, Locations: []Location{{RootID: "another-root", RelativeParent: "one"}}},
	}
	result, err := Solve(context.Background(), candidates)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Unique || result.Choices["a"] != "a-high" || result.Choices["b"] != "b-low" || result.Choices["c"] != "c-only" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestSolveDeduplicatesRepeatedLocationsAndReportsTies(t *testing.T) {
	locations := []Location{{RootID: "r", RelativeParent: "dir"}, {RootID: "r", RelativeParent: "dir"}}
	result, err := Solve(context.Background(), []Candidate{
		{FileID: "v", ID: "left", PositionID: "p", Confidence: .9, Locations: locations},
		{FileID: "v", ID: "right", PositionID: "q", Confidence: .9, Locations: locations},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Unique || len(result.Choices) != 0 {
		t.Fatalf("tie returned a winner: %+v", result)
	}
}

func TestSolveReportsInfeasibleAssignmentWithoutError(t *testing.T) {
	location := []Location{{RootID: "root", RelativeParent: "album"}}
	result, err := Solve(context.Background(), []Candidate{
		{FileID: "a", ID: "a-only", PositionID: "position", Confidence: .9, Locations: location},
		{FileID: "b", ID: "b-only", PositionID: "position", Confidence: .9, Locations: location},
	})
	if err != nil {
		t.Fatalf("infeasible assignment returned error: %v", err)
	}
	if result.Feasible || result.Unique || len(result.Choices) != 0 {
		t.Fatalf("infeasible assignment returned a choice: %+v", result)
	}
}

func TestSolvePreservesWhitespaceInFolderPaths(t *testing.T) {
	// Folders that differ only by surrounding whitespace are distinct
	// locations, so the same position ID in each is not a conflict.
	result, err := Solve(context.Background(), []Candidate{
		{FileID: "a", ID: "a-only", PositionID: "p1", Confidence: .95, Locations: []Location{
			{RootID: "root", RelativeParent: "album"},
		}},
		{FileID: "b", ID: "b-only", PositionID: "p1", Confidence: .95, Locations: []Location{
			{RootID: "root", RelativeParent: " album "},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Unique || result.Choices["a"] != "a-only" || result.Choices["b"] != "b-only" {
		t.Fatalf("whitespace-only folder difference was collapsed: %+v", result)
	}

	deduped, err := canonicalLocations([]Location{
		{RootID: "root", RelativeParent: " album "},
		{RootID: "root", RelativeParent: " album "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deduped) != 1 || deduped[0].RelativeParent != " album " {
		t.Fatalf("repeated whitespace folder was not deduplicated: %+v", deduped)
	}
}

func TestSolveRequiresConsistentNonemptyCanonicalFoldersPerFile(t *testing.T) {
	_, err := Solve(context.Background(), []Candidate{
		{FileID: "v", ID: "first", PositionID: "p1", Confidence: .9, Locations: []Location{
			{RootID: "root", RelativeParent: "folder"},
			{RootID: "root", RelativeParent: "other"},
		}},
		{FileID: "v", ID: "second", PositionID: "p2", Confidence: .8, Locations: []Location{
			{RootID: "root", RelativeParent: "folder"},
		}},
	})
	if err == nil {
		t.Fatal("candidate-specific folder subsets were accepted")
	}

	_, err = Solve(context.Background(), []Candidate{{
		FileID: "v", ID: "empty", PositionID: "p", Confidence: .9,
	}})
	if err == nil {
		t.Fatal("candidate without a containing folder was accepted")
	}
}

func TestSolveCanonicalizesAndSharesFolderSetAcrossCandidates(t *testing.T) {
	result, err := Solve(context.Background(), []Candidate{
		{FileID: "a", ID: "a1", PositionID: "p1", Confidence: .95, Locations: []Location{
			{RootID: " root ", RelativeParent: "./folder/child/.."},
			{RootID: "root", RelativeParent: "folder"},
		}},
		{FileID: "a", ID: "a2", PositionID: "p2", Confidence: .8, Locations: []Location{
			{RootID: "root", RelativeParent: "folder"},
		}},
		{FileID: "b", ID: "b1", PositionID: "p1", Confidence: .94, Locations: []Location{
			{RootID: "root", RelativeParent: "folder"},
		}},
		{FileID: "b", ID: "b2", PositionID: "p2", Confidence: .8, Locations: []Location{
			{RootID: "root", RelativeParent: "folder"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Unique || result.Choices["a"] != "a1" || result.Choices["b"] != "b2" {
		t.Fatalf("canonical shared folders did not constrain assignment: %+v", result)
	}
}

func TestSolveMatchesExhaustiveSmallOracle(t *testing.T) {
	location := []Location{{RootID: "root", RelativeParent: "dir"}}
	cases := [][]Candidate{
		{
			{FileID: "a", ID: "a1", PositionID: "p1", Confidence: .91, Locations: location},
			{FileID: "a", ID: "a2", PositionID: "p2", Confidence: .89, Locations: location},
			{FileID: "b", ID: "b1", PositionID: "p1", Confidence: .90, Locations: location},
			{FileID: "b", ID: "b2", PositionID: "p2", Confidence: .88, Locations: location},
		},
		{
			{FileID: "a", ID: "a1", PositionID: "p1", Confidence: .90, Locations: location},
			{FileID: "a", ID: "a2", PositionID: "p2", Confidence: .90, Locations: location},
		},
	}
	for i, candidates := range cases {
		wantChoices, wantUnique := exhaustiveOracle(candidates)
		got, err := Solve(context.Background(), candidates)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got.Unique != wantUnique {
			t.Fatalf("case %d uniqueness = %v, want %v", i, got.Unique, wantUnique)
		}
		if wantUnique {
			for file, candidateID := range wantChoices {
				if got.Choices[file] != candidateID {
					t.Fatalf("case %d choice[%q] = %q, want %q", i, file, got.Choices[file], candidateID)
				}
			}
		}
	}
}

func TestSolveUsesExactBinaryFloatScoreAndCarriesBeyondInt64(t *testing.T) {
	candidates := make([]Candidate, 0, 1025*2)
	location := []Location{{RootID: "root", RelativeParent: "dir"}}
	for i := 0; i < 1024; i++ {
		id := stringID(i)
		candidates = append(candidates, Candidate{FileID: id, ID: "only", PositionID: "p" + id, Confidence: 1, Locations: location})
	}
	candidates = append(candidates,
		Candidate{FileID: "last", ID: "near", PositionID: "near-position", Confidence: math.Nextafter(1, 0), Locations: location},
		Candidate{FileID: "last", ID: "max", PositionID: "max-position", Confidence: 1, Locations: location},
	)
	result, err := Solve(context.Background(), candidates)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Unique || result.Choices["last"] != "max" {
		t.Fatalf("did not maximize exact objective above int64 range: %+v", result)
	}
}

func TestSolveHonorsCancellationWithoutReturningPartialChoice(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := Solve(ctx, []Candidate{{FileID: "v", ID: "c", PositionID: "p", Confidence: .9}})
	if err == nil || result.Unique || len(result.Choices) != 0 {
		t.Fatalf("canceled solve returned a result: %+v, %v", result, err)
	}
}

func TestSolveStopsActiveSearchOnCancellation(t *testing.T) {
	const pigeons, holes = 30, 29
	solver := gini.New()
	variables := make([][]z.Lit, pigeons)
	for pigeon := range variables {
		variables[pigeon] = make([]z.Lit, holes)
		for hole := range variables[pigeon] {
			variables[pigeon][hole] = solver.Lit()
		}
	}
	for pigeon := range variables {
		addClause(solver, variables[pigeon]...)
	}
	for hole := 0; hole < holes; hole++ {
		for first := 0; first < pigeons; first++ {
			for second := first + 1; second < pigeons; second++ {
				addClause(solver, variables[first][hole].Not(), variables[second][hole].Not())
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observedPending := false
	status, err := solveWithPending(ctx, solver, nil, func() {
		observedPending = true
		cancel()
	})
	if !observedPending || err != context.Canceled || status != 0 {
		t.Fatalf("active canceled solve = (%d, %v), pending observed %v", status, err, observedPending)
	}
}

func TestAdderTruthTable(t *testing.T) {
	circuit := logic.NewC()
	a := []z.Lit{circuit.Lit(), circuit.Lit()}
	b := []z.Lit{circuit.Lit(), circuit.Lit()}
	sum := addWords(circuit, a, b, 3)
	solver := gini.New()
	circuit.ToCnf(solver)
	for left := 0; left < 4; left++ {
		for right := 0; right < 4; right++ {
			assumptions := append(valueLits(a, left), valueLits(b, right)...)
			assumptions = append(assumptions, valueLits(sum, left+right)...)
			solver.Assume(assumptions...)
			if got := solver.Solve(); got != 1 {
				t.Fatalf("%d + %d solve status %d", left, right, got)
			}
		}
	}
}

func TestSolveInstancesAreConcurrentAndIndependent(t *testing.T) {
	results := make(chan error, 2)
	for _, id := range []string{"first", "second"} {
		id := id
		go func() {
			result, err := Solve(context.Background(), []Candidate{
				{FileID: id, ID: "low", PositionID: "low-" + id, Confidence: .8, Locations: []Location{{RootID: id}}},
				{FileID: id, ID: "high", PositionID: "high-" + id, Confidence: .9, Locations: []Location{{RootID: id}}},
			})
			if err == nil && (!result.Unique || result.Choices[id] != "high") {
				err = context.DeadlineExceeded
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

func valueLits(lits []z.Lit, value int) []z.Lit {
	result := make([]z.Lit, len(lits))
	for i, lit := range lits {
		if value&(1<<i) == 0 {
			result[i] = lit.Not()
		} else {
			result[i] = lit
		}
	}
	return result
}

func addClause(solver *gini.Gini, literals ...z.Lit) {
	for _, literal := range literals {
		solver.Add(literal)
	}
	solver.Add(0)
}

func exhaustiveOracle(candidates []Candidate) (map[string]string, bool) {
	files, perFile, _, err := validate(candidates)
	if err != nil {
		panic(err)
	}
	best := new(big.Int)
	bestCount := 0
	var bestChoices map[string]string
	var visit func(int, map[string]string, map[string]bool, *big.Int)
	visit = func(fileIndex int, choices map[string]string, occupied map[string]bool, total *big.Int) {
		if fileIndex == len(files) {
			comparison := total.Cmp(best)
			if bestCount == 0 || comparison > 0 {
				best.Set(total)
				bestCount = 1
				bestChoices = make(map[string]string, len(choices))
				for file, candidateID := range choices {
					bestChoices[file] = candidateID
				}
			} else if comparison == 0 {
				bestCount++
			}
			return
		}
		file := files[fileIndex]
		for _, index := range perFile[file] {
			candidate := candidates[index]
			var slots []string
			valid := true
			locations, err := canonicalLocations(candidate.Locations)
			if err != nil {
				panic(err)
			}
			for _, location := range locations {
				slot := encode(location.RootID, location.RelativeParent, candidate.PositionID)
				if occupied[slot] {
					valid = false
					break
				}
				slots = append(slots, slot)
			}
			if !valid {
				continue
			}
			for _, slot := range slots {
				occupied[slot] = true
			}
			choices[file] = candidate.ID
			value := new(big.Int).Set(total)
			value.Add(value, new(big.Int).SetUint64(scoreInteger(candidate.Confidence)))
			visit(fileIndex+1, choices, occupied, value)
			delete(choices, file)
			for _, slot := range slots {
				delete(occupied, slot)
			}
		}
	}
	visit(0, make(map[string]string), make(map[string]bool), new(big.Int))
	return bestChoices, bestCount == 1
}

func stringID(i int) string {
	const digits = "0123456789abcdef"
	if i == 0 {
		return "0"
	}
	var out [8]byte
	pos := len(out)
	for i > 0 {
		pos--
		out[pos] = digits[i&15]
		i >>= 4
	}
	return string(out[pos:])
}
