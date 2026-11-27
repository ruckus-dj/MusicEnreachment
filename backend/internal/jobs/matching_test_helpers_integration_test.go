//go:build integration

package jobs

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/uptrace/bun"
)

func setMatchingRiverJobRunning(t *testing.T, ctx context.Context, database *bun.DB, jobID int64) {
	t.Helper()
	if _, err := database.NewRaw(`UPDATE river_job SET state='running', attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatalf("mark River job %d running: %v", jobID, err)
	}
}

func assertLiveMatchingReview(t *testing.T, ctx context.Context, db *bun.DB, groupID uuid.UUID, stored []persistence.IncomingMatchCandidate) {
	t.Helper()
	if len(stored) != 0 {
		t.Fatalf("review unexpectedly persisted scores: %+v", stored)
	}
	review, err := service.NewMatchingReviewService(persistence.NewIncomingMatchCandidateRepository(db)).Get(ctx, groupID)
	if err != nil || !review.Evaluated || review.AutomaticAssignment == nil || len(review.Candidates) != 1 || len(review.Candidates[0].Positions) != 1 {
		t.Fatalf("live matching review=%+v err=%v", review, err)
	}
	position := review.Candidates[0].Positions[0]
	if position.Medium != 0 || position.Position != 0 || position.AssignmentConfidence == nil || *position.AssignmentConfidence < .7 {
		t.Fatalf("live candidate position = %+v, want qualifying score for the first track", position)
	}
}
