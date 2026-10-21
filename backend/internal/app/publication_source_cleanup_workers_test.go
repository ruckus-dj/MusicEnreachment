package app

import (
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestPublicationSourceCleanupWorkersRegistered(t *testing.T) {
	bootUUID := uuid.New()
	registeredWorkers, err := newPublicationSourceCleanupWorkers(persistence.NewSetupManagerRepository(nil), bootUUID)
	if err != nil {
		t.Fatal(err)
	}
	registry := river.NewWorkers()
	registerPublicationSourceCleanupWorkers(registry, registeredWorkers)

	if err := river.AddWorkerSafely(registry, jobs.NewPublicationSourceCleanupWorker(nil)); err == nil {
		t.Fatal("publication source cleanup worker was not registered")
	}
	if err := river.AddWorkerSafely(registry, jobs.NewPublicationSourceCleanupRetryWorker(nil)); err == nil {
		t.Fatal("publication source cleanup retry worker was not registered")
	}
}
