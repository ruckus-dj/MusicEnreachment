package service

import (
	"fmt"
	"math"
	"math/big"
	"sort"
)

const minimumReleaseCandidateConfidence = 0.70

type ReleaseSelectionFile struct {
	ID         string
	Candidates []ReleaseSelectionCandidate
}

type ReleaseSelectionCandidate struct {
	ReleaseID string
	Score     MatchingScore
}

type ReleaseSelectionReason string

const (
	ReleaseSelectionSelected    ReleaseSelectionReason = "selected"
	ReleaseSelectionReview      ReleaseSelectionReason = "review"
	ReleaseSelectionNoQualified ReleaseSelectionReason = "no_qualified_selected"
)

type ReleaseSelectionResult struct {
	Reason            ReleaseSelectionReason
	SelectedReleaseID string
	Candidates        []ReleaseCandidateAggregate
}

type ReleaseCandidateAggregate struct {
	ReleaseID string
	Qualified bool
	ScoreSum  float64
	Files     []ReleaseCandidateFileEvidence
}

type ReleaseCandidateFileEvidence struct {
	FileID     string
	Present    bool
	Confidence float64
}

// SelectReleaseGroup chooses a unique highest-sum release candidate that meets
// the per-file confidence floor for every file. It is a pure calculation: it
// neither modifies inputs nor performs source or provider I/O.
func SelectReleaseGroup(files []ReleaseSelectionFile) (ReleaseSelectionResult, error) {
	fileIDs := make(map[string]struct{}, len(files))
	byRelease := make(map[string]map[string]float64)
	for _, file := range files {
		if file.ID == "" {
			return ReleaseSelectionResult{}, fmt.Errorf("release selection file ID is empty")
		}
		if _, exists := fileIDs[file.ID]; exists {
			return ReleaseSelectionResult{}, fmt.Errorf("duplicate release selection file ID %q", file.ID)
		}
		fileIDs[file.ID] = struct{}{}
		seen := make(map[string]float64, len(file.Candidates))
		for _, candidate := range file.Candidates {
			if candidate.ReleaseID == "" {
				return ReleaseSelectionResult{}, fmt.Errorf("release selection ID is empty for file %q", file.ID)
			}
			confidence := candidate.Score.Confidence
			if candidate.Score.Profile != ReleaseMatchingProfile {
				return ReleaseSelectionResult{}, fmt.Errorf("release candidate %q for file %q does not use release profile", candidate.ReleaseID, file.ID)
			}
			if math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 || confidence > 1 {
				return ReleaseSelectionResult{}, fmt.Errorf("release candidate %q for file %q has confidence outside [0, 1]", candidate.ReleaseID, file.ID)
			}
			if prior, exists := seen[candidate.ReleaseID]; exists {
				if prior != confidence {
					return ReleaseSelectionResult{}, fmt.Errorf("conflicting duplicate scores for release %q and file %q", candidate.ReleaseID, file.ID)
				}
				return ReleaseSelectionResult{}, fmt.Errorf("duplicate release candidate %q for file %q", candidate.ReleaseID, file.ID)
			}
			seen[candidate.ReleaseID] = confidence
		}
		for releaseID, confidence := range seen {
			if byRelease[releaseID] == nil {
				byRelease[releaseID] = make(map[string]float64)
			}
			byRelease[releaseID][file.ID] = confidence
		}
	}

	fileOrder := make([]string, 0, len(fileIDs))
	for id := range fileIDs {
		fileOrder = append(fileOrder, id)
	}
	sort.Strings(fileOrder)
	releaseOrder := make([]string, 0, len(byRelease))
	for id := range byRelease {
		releaseOrder = append(releaseOrder, id)
	}
	sort.Strings(releaseOrder)

	result := ReleaseSelectionResult{Reason: ReleaseSelectionNoQualified, Candidates: make([]ReleaseCandidateAggregate, 0, len(releaseOrder))}
	maxSum := new(big.Rat)
	maxCount := 0
	for _, releaseID := range releaseOrder {
		perFile := byRelease[releaseID]
		aggregate := ReleaseCandidateAggregate{ReleaseID: releaseID, Qualified: len(files) > 0 && len(perFile) == len(files), Files: make([]ReleaseCandidateFileEvidence, 0, len(fileOrder))}
		sum := new(big.Rat)
		for _, fileID := range fileOrder {
			confidence, present := perFile[fileID]
			evidence := ReleaseCandidateFileEvidence{FileID: fileID, Present: present}
			if present {
				evidence.Confidence = confidence
				if confidence < minimumReleaseCandidateConfidence {
					aggregate.Qualified = false
				}
				// SetFloat64 represents the exact value of the input binary float.
				sum.Add(sum, new(big.Rat).SetFloat64(confidence))
			}
			aggregate.Files = append(aggregate.Files, evidence)
		}
		aggregate.ScoreSum, _ = sum.Float64()
		result.Candidates = append(result.Candidates, aggregate)
		if !aggregate.Qualified {
			continue
		}
		comparison := sum.Cmp(maxSum)
		if maxCount == 0 || comparison > 0 {
			maxSum.Set(sum)
			result.SelectedReleaseID = releaseID
			maxCount = 1
		} else if comparison == 0 {
			maxCount++
		}
	}
	if maxCount == 1 {
		result.Reason = ReleaseSelectionSelected
	} else if maxCount > 1 {
		result.Reason = ReleaseSelectionReview
		result.SelectedReleaseID = ""
	}
	return result, nil
}
