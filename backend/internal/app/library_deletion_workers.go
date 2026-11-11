package app

import (
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
)

func registerLibraryDeletionWorker(workers *river.Workers, worker *jobs.LibraryDeletionWorker) {
	river.AddWorker(workers, worker)
}
