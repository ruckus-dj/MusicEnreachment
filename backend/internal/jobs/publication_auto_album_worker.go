package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
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
	if err == nil && request != nil {
		err = worker.service.AdmitAutomaticAlbum(ctx, *request)
	}
	if errors.Is(err, service.ErrPublicationArtistDiscography) && errors.Is(err, musicbrainz.ErrTemporarilyUnavailable) {
		return river.JobSnooze(15 * time.Minute)
	}
	// First-publication artwork is pending or temporarily unavailable. Snooze
	// before the attempt-exhaustion branch so a durable album request is never
	// completed as if the missing cover were a definitive absence.
	if errors.Is(err, service.ErrPublicationArtwork) {
		return river.JobSnooze(15 * time.Minute)
	}
	if errors.Is(err, persistence.ErrMatchingTargetNeedsRefresh) {
		return river.JobSnooze(time.Minute)
	}
	if err != nil && (job.MaxAttempts < 1 || job.Attempt < job.MaxAttempts || ctx.Err() != nil) {
		return err
	}
	return errors.Join(err, worker.repository.CompletePublicationAutoAlbum(ctx, id))
}
