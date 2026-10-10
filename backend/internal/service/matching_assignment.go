package service

import (
	"context"
	"fmt"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/assignment"
)

// MatchingAssignmentLocation is one inventory location occupied by a file
// variant. A variant may occur in multiple folders and still receive one edge.
type MatchingAssignmentLocation struct {
	RootID         string
	RelativeParent string
}

// MatchingAssignmentCandidate is a scored file-variant-to-position edge.
// RecordingMBID is retained as evidence only and never constrains assignment.
type MatchingAssignmentCandidate struct {
	FileVariantID string
	CandidateID   string
	PositionID    string
	RecordingMBID string
	Score         MatchingScore
	Locations     []MatchingAssignmentLocation
}

// MatchingAssignmentResult distinguishes infeasible constraints from a tie;
// choices are populated only when the exact optimum is unique.
type MatchingAssignmentResult struct {
	Feasible bool
	Unique   bool
	Choices  map[string]string
}

// SelectMatchingAssignments maximizes the exact sum of combined assignment
// confidence while enforcing one position per variant and one variant per
// position in each containing folder.
func SelectMatchingAssignments(ctx context.Context, candidates []MatchingAssignmentCandidate) (MatchingAssignmentResult, error) {
	edges := make([]assignment.Candidate, len(candidates))
	for i, candidate := range candidates {
		if candidate.Score.Profile != RecordingReleaseMatchingProfile {
			return MatchingAssignmentResult{}, fmt.Errorf("assignment candidate %q does not use combined recording-release score", candidate.CandidateID)
		}
		locations := make([]assignment.Location, len(candidate.Locations))
		for j, location := range candidate.Locations {
			locations[j] = assignment.Location{RootID: location.RootID, RelativeParent: location.RelativeParent}
		}
		edges[i] = assignment.Candidate{
			FileID: candidate.FileVariantID, ID: candidate.CandidateID, PositionID: candidate.PositionID,
			Confidence: candidate.Score.Confidence, Locations: locations,
		}
	}
	result, err := assignment.Solve(ctx, edges)
	if err != nil {
		return MatchingAssignmentResult{}, err
	}
	return MatchingAssignmentResult{Feasible: result.Feasible, Unique: result.Unique, Choices: result.Choices}, nil
}
