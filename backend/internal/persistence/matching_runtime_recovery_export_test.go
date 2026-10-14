//go:build integration

package persistence

import (
	"context"

	"github.com/google/uuid"
)

// MatchingRuntimeRecoveryCandidateForTest is a test-only bridge for exercising
// recovery with a candidate captured before a concurrent state change.
type MatchingRuntimeRecoveryCandidateForTest struct {
	RunID      uuid.UUID
	Attempt    int
	Revision   int64
	Phase      string
	CurrentJob *int64
}

// RecoverSealedMatchingWakeForTest invokes the production transaction using a
// previously captured candidate, bypassing only the outer candidate query.
func (repository *SetupManagerRepository) RecoverSealedMatchingWakeForTest(
	ctx context.Context,
	client RiverInserter,
	candidate MatchingRuntimeRecoveryCandidateForTest,
) (bool, error) {
	return repository.recoverSealedMatchingWake(ctx, client, matchingRuntimeRecoveryCandidate{
		RunID: candidate.RunID, Attempt: candidate.Attempt, Revision: candidate.Revision,
		Phase: candidate.Phase, CurrentJob: candidate.CurrentJob,
	})
}
