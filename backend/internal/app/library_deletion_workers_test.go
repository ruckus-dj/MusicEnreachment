package app

import (
	"testing"

	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
)

func TestLibraryDeletionWorkerRegistered(t *testing.T) {
	workers := river.NewWorkers()
	registerLibraryDeletionWorker(workers, jobs.NewLibraryDeletionWorker(nil, nil))
	if err := river.AddWorkerSafely(workers, jobs.NewLibraryDeletionWorker(nil, nil)); err == nil {
		t.Fatal("library deletion worker was not registered")
	}
}
