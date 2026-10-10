package service

import (
	"context"
	"testing"
)

func TestSelectMatchingAssignmentsUsesCombinedEdgesAndIgnoresRecordingMBID(t *testing.T) {
	folder := []MatchingAssignmentLocation{{RootID: "root", RelativeParent: "folder"}}
	candidates := []MatchingAssignmentCandidate{
		{FileVariantID: "v1", CandidateID: "v1-a", PositionID: "position-a", RecordingMBID: "same", Score: MatchingScore{Profile: RecordingReleaseMatchingProfile, Confidence: .98}, Locations: folder},
		{FileVariantID: "v1", CandidateID: "v1-b", PositionID: "position-b", RecordingMBID: "same", Score: MatchingScore{Profile: RecordingReleaseMatchingProfile, Confidence: .80}, Locations: folder},
		{FileVariantID: "v2", CandidateID: "v2-a", PositionID: "position-a", RecordingMBID: "same", Score: MatchingScore{Profile: RecordingReleaseMatchingProfile, Confidence: .97}, Locations: folder},
		{FileVariantID: "v2", CandidateID: "v2-b", PositionID: "position-b", RecordingMBID: "different", Score: MatchingScore{Profile: RecordingReleaseMatchingProfile, Confidence: .80}, Locations: folder},
	}
	result, err := SelectMatchingAssignments(context.Background(), candidates)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Feasible || !result.Unique || result.Choices["v1"] != "v1-a" || result.Choices["v2"] != "v2-b" {
		t.Fatalf("unexpected assignment result: %+v", result)
	}
}

func TestSelectMatchingAssignmentsReportsInfeasibleWithoutError(t *testing.T) {
	folder := []MatchingAssignmentLocation{{RootID: "root", RelativeParent: "folder"}}
	result, err := SelectMatchingAssignments(context.Background(), []MatchingAssignmentCandidate{
		{FileVariantID: "v1", CandidateID: "v1-only", PositionID: "position", Score: MatchingScore{Profile: RecordingReleaseMatchingProfile, Confidence: .9}, Locations: folder},
		{FileVariantID: "v2", CandidateID: "v2-only", PositionID: "position", Score: MatchingScore{Profile: RecordingReleaseMatchingProfile, Confidence: .9}, Locations: folder},
	})
	if err != nil {
		t.Fatalf("infeasible assignment returned error: %v", err)
	}
	if result.Feasible || result.Unique || len(result.Choices) != 0 {
		t.Fatalf("infeasible assignment returned a choice: %+v", result)
	}
}

func TestSelectMatchingAssignmentsRequiresCombinedScoreProfile(t *testing.T) {
	_, err := SelectMatchingAssignments(context.Background(), []MatchingAssignmentCandidate{{
		FileVariantID: "variant", CandidateID: "edge", PositionID: "position", Score: MatchingScore{Profile: ReleaseMatchingProfile, Confidence: .9},
	}})
	if err == nil {
		t.Fatal("non-combined score profile was accepted")
	}
}

func TestSelectMatchingAssignmentsRejectsInconsistentVariantFolders(t *testing.T) {
	_, err := SelectMatchingAssignments(context.Background(), []MatchingAssignmentCandidate{
		{
			FileVariantID: "variant", CandidateID: "first", PositionID: "p1",
			Score:     MatchingScore{Profile: RecordingReleaseMatchingProfile, Confidence: .9},
			Locations: []MatchingAssignmentLocation{{RootID: "root", RelativeParent: "one"}, {RootID: "root", RelativeParent: "two"}},
		},
		{
			FileVariantID: "variant", CandidateID: "second", PositionID: "p2",
			Score:     MatchingScore{Profile: RecordingReleaseMatchingProfile, Confidence: .8},
			Locations: []MatchingAssignmentLocation{{RootID: "root", RelativeParent: "one"}},
		},
	})
	if err == nil {
		t.Fatal("inconsistent folders for the same variant were accepted")
	}
}
