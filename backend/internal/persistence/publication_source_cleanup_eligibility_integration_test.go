//go:build integration

package persistence

import ()

func assessmentHasCandidateBlocker(assessment PublicationSourceCleanupAssessment, blocker string) bool {
	for _, candidate := range assessment.Candidates {
		for _, candidateBlocker := range candidate.Blockers {
			if candidateBlocker == blocker {
				return true
			}
		}
	}
	return false
}
