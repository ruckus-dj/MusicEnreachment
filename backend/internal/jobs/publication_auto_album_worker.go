package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type PublicationAutoAlbumWorker struct {
	river.WorkerDefaults[persistence.PublicationAutoAlbumArgs]
	repository *persistence.SetupManagerRepository
	service    *service.LibraryPublications
}

func NewPublicationAutoAlbumWorker(repository *persistence.SetupManagerRepository, publications *service.LibraryPublications) *PublicationAutoAlbumWorker {
	return &PublicationAutoAlbumWorker{repository: repository, service: publications}
}

func (*PublicationAutoAlbumWorker) Timeout(*river.Job[persistence.PublicationAutoAlbumArgs]) time.Duration {
	return 10 * time.Minute
}

func (worker *PublicationAutoAlbumWorker) Work(ctx context.Context, job *river.Job[persistence.PublicationAutoAlbumArgs]) error {
	id := job.Args.ApplyOperationID
	if id == uuid.Nil {
		return nil
	}
	request, err := worker.repository.ReadPublicationAutoAlbum(ctx, id)
	if err != nil {
		return err
	}
	if request != nil {
		if err := worker.service.AdmitAutomaticAlbum(ctx, *request); err != nil {
			return err
		}
	}
	return worker.repository.CompletePublicationAutoAlbum(ctx, id)
}
