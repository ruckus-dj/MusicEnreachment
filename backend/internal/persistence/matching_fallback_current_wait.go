package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// GetCurrentMatchingFallbackEvaluationWaitRevision derives the fallback wait
// from the persisted handoff and active acquisition cursor, rather than from a
// caller-supplied label or historical seal.
func (repository *SetupManagerRepository) GetCurrentMatchingFallbackEvaluationWaitRevision(ctx context.Context, runID uuid.UUID) (int64, bool, error) {
	if repository == nil || runID == uuid.Nil {
		return 0, false, fmt.Errorf("read current fallback evaluation wait: run is required")
	}
	var waitRevision int64
	err := repository.db.NewRaw(`SELECT handoff.wait_revision
		FROM matching_acquisition acquisition
		JOIN matching_fallback_evaluation_handoff handoff ON handoff.run_id=acquisition.run_id
		WHERE acquisition.run_id=?
			AND acquisition.current_phase=?
			AND acquisition.revision=handoff.wait_revision+1`,
		runID, MatchingAcquisitionFallbackEvaluationSealed).Scan(ctx, &waitRevision)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read current fallback evaluation wait: %w", err)
	}
	return waitRevision, true, nil
}

// Get exposes the current published evaluation snapshot to matching workers.
func (repository *SetupManagerRepository) Get(ctx context.Context, groupID uuid.UUID) (*IncomingMatchEvaluationHeader, []IncomingMatchCandidate, error) {
	return NewIncomingMatchCandidateRepository(repository.db).Get(ctx, groupID)
}
