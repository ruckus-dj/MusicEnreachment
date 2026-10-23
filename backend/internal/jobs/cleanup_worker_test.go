package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type terminalPublicationMaintenanceFixture struct {
	deleted    int
	limits     []int
	settled    []uuid.UUID
	candidates []persistence.TerminalPublicationCandidate
}

func (fixture *terminalPublicationMaintenanceFixture) DeleteSucceededBefore(context.Context, time.Time) error {
	fixture.deleted++
	return nil
}

func (fixture *terminalPublicationMaintenanceFixture) ListTerminalPublicationCandidates(_ context.Context, limit int) ([]persistence.TerminalPublicationCandidate, error) {
	fixture.limits = append(fixture.limits, limit)
	return fixture.candidates, nil
}

func (fixture *terminalPublicationMaintenanceFixture) FailTerminalPublication(_ context.Context, candidate persistence.TerminalPublicationCandidate) (bool, error) {
	fixture.settled = append(fixture.settled, candidate.OperationID)
	return true, nil
}

func TestCleanupWorkerSettlesBoundedTerminalPublicationCandidates(t *testing.T) {
	one, two := uuid.New(), uuid.New()
	fixture := &terminalPublicationMaintenanceFixture{candidates: []persistence.TerminalPublicationCandidate{
		{OperationID: one, ApplicationAttempt: 1, RiverJobID: 10, RiverState: "cancelled"},
		{OperationID: two, ApplicationAttempt: 2, RiverJobID: 20, RiverState: "discarded"},
	}}
	if err := jobs.NewCleanupWorker(fixture).Work(context.Background(), nil); err != nil {
		t.Fatalf("run cleanup maintenance: %v", err)
	}
	if fixture.deleted != 1 || len(fixture.limits) != 1 || fixture.limits[0] != 100 {
		t.Fatalf("cleanup and candidate query = %d/%v, want one cleanup and a bounded 100-item query", fixture.deleted, fixture.limits)
	}
	if len(fixture.settled) != 2 || fixture.settled[0] != one || fixture.settled[1] != two {
		t.Fatalf("settled terminal publication IDs = %v", fixture.settled)
	}
}
