package service

import (
	"math"
	"reflect"
	"testing"
)

func selectionFile(id string, candidates ...ReleaseSelectionCandidate) ReleaseSelectionFile {
	return ReleaseSelectionFile{ID: id, Candidates: candidates}
}

func releaseCandidate(id string, confidence float64) ReleaseSelectionCandidate {
	return ReleaseSelectionCandidate{ReleaseID: id, Score: MatchingScore{Profile: ReleaseMatchingProfile, Confidence: confidence}}
}

func TestSelectReleaseGroupRequiresIntersectionAndPerFileThreshold(t *testing.T) {
	result, err := SelectReleaseGroup([]ReleaseSelectionFile{
		selectionFile("a", releaseCandidate("shared", .70), releaseCandidate("only-a", 1)),
		selectionFile("b", releaseCandidate("shared", 1), releaseCandidate("only-b", 1)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reason != ReleaseSelectionSelected || result.SelectedReleaseID != "shared" {
		t.Fatalf("selection = %+v, want shared intersection winner", result)
	}
	for _, candidate := range result.Candidates {
		if candidate.ReleaseID != "shared" && candidate.Qualified {
			t.Errorf("union-only candidate qualified: %+v", candidate)
		}
	}

	below := math.Nextafter(.70, 0)
	result, err = SelectReleaseGroup([]ReleaseSelectionFile{
		selectionFile("a", releaseCandidate("shared", below)),
		selectionFile("b", releaseCandidate("shared", 1)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reason != ReleaseSelectionNoQualified || result.SelectedReleaseID != "" || result.Candidates[0].Qualified {
		t.Fatalf("below-threshold selection = %+v, want no qualified winner", result)
	}
}

func TestSelectReleaseGroupTiesAndExactNearULPSums(t *testing.T) {
	t.Run("exact tie requires review", func(t *testing.T) {
		result, err := SelectReleaseGroup([]ReleaseSelectionFile{
			selectionFile("file", releaseCandidate("a", .8), releaseCandidate("b", .8)),
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Reason != ReleaseSelectionReview || result.SelectedReleaseID != "" {
			t.Fatalf("selection = %+v, want review on tie", result)
		}
	})

	t.Run("exact binary sum distinguishes close totals", func(t *testing.T) {
		low := 0.8
		high := math.Nextafter(low, 1)
		result, err := SelectReleaseGroup([]ReleaseSelectionFile{
			selectionFile("a", releaseCandidate("low", low), releaseCandidate("high", high)),
			selectionFile("b", releaseCandidate("low", low), releaseCandidate("high", high)),
			selectionFile("c", releaseCandidate("low", low), releaseCandidate("high", high)),
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Reason != ReleaseSelectionSelected || result.SelectedReleaseID != "high" {
			t.Fatalf("selection = %+v, want exact-sum winner high", result)
		}
	})
}

func TestSelectReleaseGroupIsOrderInvariantAndDoesNotMutateInputs(t *testing.T) {
	files := []ReleaseSelectionFile{
		selectionFile("z", releaseCandidate("r2", .9), releaseCandidate("r1", .8)),
		selectionFile("a", releaseCandidate("r1", .9), releaseCandidate("r2", .8)),
	}
	original := []ReleaseSelectionFile{
		selectionFile("z", releaseCandidate("r2", .9), releaseCandidate("r1", .8)),
		selectionFile("a", releaseCandidate("r1", .9), releaseCandidate("r2", .8)),
	}
	first, err := SelectReleaseGroup(files)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files, original) {
		t.Fatal("selector mutated input file/candidate arrays")
	}
	files[0], files[1] = files[1], files[0]
	for i := range files {
		for left, right := 0, len(files[i].Candidates)-1; left < right; left, right = left+1, right-1 {
			files[i].Candidates[left], files[i].Candidates[right] = files[i].Candidates[right], files[i].Candidates[left]
		}
	}
	reordered := make([]ReleaseSelectionFile, len(files))
	for i := range files {
		reordered[i] = files[i]
		reordered[i].Candidates = append([]ReleaseSelectionCandidate(nil), files[i].Candidates...)
	}
	second, err := SelectReleaseGroup(files)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("order changed result:\nfirst:  %+v\nsecond: %+v", first, second)
	}
	if !reflect.DeepEqual(files, reordered) {
		t.Fatal("selector mutated input file/candidate arrays")
	}
}

func TestSelectReleaseGroupRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name  string
		files []ReleaseSelectionFile
	}{
		{"duplicate file IDs", []ReleaseSelectionFile{selectionFile("f"), selectionFile("f")}},
		{"empty file ID", []ReleaseSelectionFile{selectionFile("")}},
		{"empty release ID", []ReleaseSelectionFile{selectionFile("f", releaseCandidate("", .8))}},
		{"duplicate release ID", []ReleaseSelectionFile{selectionFile("f", releaseCandidate("r", .8), releaseCandidate("r", .8))}},
		{"conflicting duplicate score", []ReleaseSelectionFile{selectionFile("f", releaseCandidate("r", .8), releaseCandidate("r", .9))}},
		{"wrong profile", []ReleaseSelectionFile{{ID: "f", Candidates: []ReleaseSelectionCandidate{{ReleaseID: "r", Score: MatchingScore{Profile: RecordingMatchingProfile, Confidence: .8}}}}}},
		{"nan", []ReleaseSelectionFile{selectionFile("f", releaseCandidate("r", math.NaN()))}},
		{"positive infinity", []ReleaseSelectionFile{selectionFile("f", releaseCandidate("r", math.Inf(1)))}},
		{"negative infinity", []ReleaseSelectionFile{selectionFile("f", releaseCandidate("r", math.Inf(-1)))}},
		{"below zero", []ReleaseSelectionFile{selectionFile("f", releaseCandidate("r", -math.SmallestNonzeroFloat64))}},
		{"above one", []ReleaseSelectionFile{selectionFile("f", releaseCandidate("r", math.Nextafter(1, 2)))}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := SelectReleaseGroup(test.files); err == nil {
				t.Fatal("expected invalid input error")
			}
		})
	}
}
