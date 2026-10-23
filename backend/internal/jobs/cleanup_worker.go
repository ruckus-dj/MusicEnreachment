package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

const infrastructurePeriodicInterval = time.Hour

type CleanupArgs struct{}

func (CleanupArgs) Kind() string { return "operation_cleanup_v1" }

type operationCleaner interface {
	DeleteSucceededBefore(context.Context, time.Time) error
}

type terminalPublicationCleaner interface {
	ListTerminalPublicationCandidates(context.Context, int) ([]persistence.TerminalPublicationCandidate, error)
	FailTerminalPublication(context.Context, persistence.TerminalPublicationCandidate) (bool, error)
}

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	repository operationCleaner
}

func NewCleanupWorker(repository operationCleaner) *CleanupWorker {
	return &CleanupWorker{repository: repository}
}

func (worker *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	if err := worker.repository.DeleteSucceededBefore(ctx, time.Now().UTC().Add(-24*time.Hour)); err != nil {
		return err
	}
	publicationCleaner, ok := worker.repository.(terminalPublicationCleaner)
	if !ok {
		return nil
	}
	candidates, err := publicationCleaner.ListTerminalPublicationCandidates(ctx, 100)
	if err != nil {
		return fmt.Errorf("list terminal publications for maintenance: %w", err)
	}
	for _, candidate := range candidates {
		if _, err := publicationCleaner.FailTerminalPublication(ctx, candidate); err != nil {
			return fmt.Errorf("settle terminal publication %s: %w", candidate.OperationID, err)
		}
	}
	return nil
}

func NewCleanupPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(infrastructurePeriodicInterval),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{}, nil },
		&river.PeriodicJobOpts{ID: "setup-operation-cleanup", RunOnStart: true},
	)
}
