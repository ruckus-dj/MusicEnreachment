package app

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type publicationSourceCleanupWorkers struct {
	cleanup *jobs.PublicationSourceCleanupWorker
	retry   *jobs.PublicationSourceCleanupRetryWorker
}

func newPublicationSourceCleanupWorkers(repository *persistence.SetupManagerRepository, bootUUID uuid.UUID) (publicationSourceCleanupWorkers, error) {
	cleanupRunner, err := service.NewPublicationSourceCleanupRunner(repository, sourcefs.SourceRemover{}, bootUUID)
	if err != nil {
		return publicationSourceCleanupWorkers{}, fmt.Errorf("create publication source cleanup runner: %w", err)
	}
	retryRunner, err := service.NewPublicationSourceCleanupExplicitRetryRunner(repository, sourcefs.SourceRemover{}, bootUUID)
	if err != nil {
		return publicationSourceCleanupWorkers{}, fmt.Errorf("create explicit publication source cleanup retry runner: %w", err)
	}
	return publicationSourceCleanupWorkers{
		cleanup: jobs.NewPublicationSourceCleanupWorker(cleanupRunner),
		retry:   jobs.NewPublicationSourceCleanupRetryWorker(retryRunner),
	}, nil
}

func registerPublicationSourceCleanupWorkers(workers *river.Workers, cleanupWorkers publicationSourceCleanupWorkers) {
	river.AddWorker(workers, cleanupWorkers.cleanup)
	river.AddWorker(workers, cleanupWorkers.retry)
}
