package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// libraryArtworkArtistProjection is the DB-derived artwork view. Every method is
// re-read at delivery so an artist removed, hidden, or relinked after enqueue
// receives no provider work.
type libraryArtworkArtistProjection interface {
	ReadLibraryArtist(context.Context, uuid.UUID) (*persistence.LibraryCatalogArtist, error)
	ReadLibraryArtistCoverArtReleaseMBIDs(context.Context, uuid.UUID) ([]string, error)
	ReadCoverArtArchiveRelease(context.Context, string) (persistence.CoverArtArchiveRelease, error)
	ReadCoverArtArchiveReleaseStatuses(context.Context, []string) (map[string]persistence.CoverArtArchiveRelease, error)
	CoverArtArchiveGalleryEligible(context.Context, string) (bool, error)
}

type libraryArtistDiscographyEnsurer interface {
	EnsureArtistDiscography(context.Context, string) error
}

type libraryArtistPhotoReader interface {
	Read(context.Context, uuid.UUID) (service.LibraryArtistPhotoResponse, error)
}

type libraryArtistEditionEnqueuer interface {
	EnqueueEdition(context.Context, uuid.UUID, string, string) error
}

type libraryArtworkEditionProvider interface {
	EnsureFront(context.Context, string) error
	EnsureGallery(context.Context, string) error
}

type libraryArtworkDispatcher interface {
	DispatchPage(context.Context, int) error
}

// LibraryArtworkDispatchWorker advances one durable backfill page. The page job
// enqueues its own next offset, so a process stop leaves the next page in River
// rather than only in memory.
type LibraryArtworkDispatchWorker struct {
	river.WorkerDefaults[service.LibraryArtworkDispatchArgs]
	dispatcher libraryArtworkDispatcher
}

func NewLibraryArtworkDispatchWorker(dispatcher libraryArtworkDispatcher) *LibraryArtworkDispatchWorker {
	return &LibraryArtworkDispatchWorker{dispatcher: dispatcher}
}

func (worker *LibraryArtworkDispatchWorker) Work(ctx context.Context, job *river.Job[service.LibraryArtworkDispatchArgs]) error {
	if worker == nil || worker.dispatcher == nil || job == nil {
		return fmt.Errorf("library artwork dispatch worker is unavailable")
	}
	return worker.dispatcher.DispatchPage(ctx, job.Args.Offset)
}

var _ river.Worker[service.LibraryArtworkDispatchArgs] = (*LibraryArtworkDispatchWorker)(nil)

// LibraryArtistArtworkWorker warms one exact artist: complete MusicBrainz
// discography, durable photo, then only the edition work whose status still
// needs a provider answer.
type LibraryArtistArtworkWorker struct {
	river.WorkerDefaults[service.LibraryArtistArtworkArgs]
	projection  libraryArtworkArtistProjection
	discography libraryArtistDiscographyEnsurer
	photos      libraryArtistPhotoReader
	editions    libraryArtistEditionEnqueuer
}

func NewLibraryArtistArtworkWorker(
	projection libraryArtworkArtistProjection,
	discography libraryArtistDiscographyEnsurer,
	photos libraryArtistPhotoReader,
	editions libraryArtistEditionEnqueuer,
) *LibraryArtistArtworkWorker {
	return &LibraryArtistArtworkWorker{
		projection: projection, discography: discography, photos: photos, editions: editions,
	}
}

// A complete discography can span many provider pages and exceed River's
// default deadline. Cancellation and shutdown still propagate through ctx.
func (*LibraryArtistArtworkWorker) Timeout(*river.Job[service.LibraryArtistArtworkArgs]) time.Duration {
	return -1
}

func (worker *LibraryArtistArtworkWorker) Work(ctx context.Context, job *river.Job[service.LibraryArtistArtworkArgs]) error {
	if worker == nil || worker.projection == nil || worker.discography == nil ||
		worker.photos == nil || worker.editions == nil || job == nil {
		return fmt.Errorf("library artist artwork worker is unavailable")
	}
	args := job.Args
	if args.ArtistID == uuid.Nil || args.ArtistMBID == "" {
		return fmt.Errorf("library artist artwork args are invalid")
	}
	artist, err := worker.projection.ReadLibraryArtist(ctx, args.ArtistID)
	if err != nil {
		return fmt.Errorf("read library artist artwork eligibility: %w", err)
	}
	if artist == nil {
		return nil // Removed or hidden since enqueue.
	}
	if got := service.CanonicalMusicBrainzArtistMBIDFromLinks(artist.ProviderLinks); got == "" || got != args.ArtistMBID {
		return nil // The exact identity changed; the new sweep will enqueue it.
	}
	problems := make([]error, 0)
	if err := worker.discography.EnsureArtistDiscography(ctx, args.ArtistMBID); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// The projection also returns exact source-linked local editions while
		// the provider discography is incomplete. Keep that work moving and let
		// the joined error retry only the failed provider branch.
		problems = append(problems, fmt.Errorf("ensure library artist discography: %w", err))
	}
	if _, err := worker.photos.Read(ctx, args.ArtistID); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// Any photo failure must not hide otherwise usable edition work.
		problems = append(problems, fmt.Errorf("read library artist photo: %w", err))
	}
	releaseMBIDs, err := worker.projection.ReadLibraryArtistCoverArtReleaseMBIDs(ctx, args.ArtistID)
	if err != nil {
		problems = append(problems, fmt.Errorf("read library artist artwork editions: %w", err))
		return errors.Join(problems...)
	}
	canonicalMBIDs := make([]string, 0, len(releaseMBIDs))
	seen := make(map[string]struct{}, len(releaseMBIDs))
	for _, raw := range releaseMBIDs {
		releaseMBID, ok := service.CanonicalMusicBrainzReleaseMBID(raw)
		if !ok {
			continue
		}
		if _, duplicate := seen[releaseMBID]; duplicate {
			continue
		}
		seen[releaseMBID] = struct{}{}
		canonicalMBIDs = append(canonicalMBIDs, releaseMBID)
	}
	// One bounded status read replaces a per-edition query for a full
	// discography. Missing rows stay unchecked and therefore remain pending.
	statuses, err := worker.projection.ReadCoverArtArchiveReleaseStatuses(ctx, canonicalMBIDs)
	if err != nil {
		problems = append(problems, fmt.Errorf("read library artist artwork statuses: %w", err))
		return errors.Join(problems...)
	}
	for _, releaseMBID := range canonicalMBIDs {
		state, found := statuses[releaseMBID]
		if !found {
			state = persistence.CoverArtArchiveRelease{
				ReleaseMBID: releaseMBID, FrontStatus: persistence.CoverArtArchiveUnchecked,
				GalleryStatus: persistence.CoverArtArchiveUnchecked,
			}
		}
		needsWork := state.FrontStatus == persistence.CoverArtArchiveUnchecked
		if !needsWork && state.GalleryStatus != persistence.CoverArtArchiveGalleryComplete {
			eligible, err := worker.projection.CoverArtArchiveGalleryEligible(ctx, releaseMBID)
			if err != nil {
				problems = append(problems, fmt.Errorf("read edition %s gallery eligibility: %w", releaseMBID, err))
				continue
			}
			needsWork = eligible
		}
		if !needsWork {
			continue
		}
		if err := worker.editions.EnqueueEdition(ctx, args.ArtistID, args.ArtistMBID, releaseMBID); err != nil {
			problems = append(problems, fmt.Errorf("enqueue edition %s artwork: %w", releaseMBID, err))
		}
	}
	return errors.Join(problems...)
}

var _ river.Worker[service.LibraryArtistArtworkArgs] = (*LibraryArtistArtworkWorker)(nil)

// LibraryEditionArtworkWorker re-checks the exact artist identity and the
// per-MBID front/gallery state before any provider request. Front and gallery
// are independent: one failure never hides the other's progress.
type LibraryEditionArtworkWorker struct {
	river.WorkerDefaults[service.LibraryEditionArtworkArgs]
	projection libraryArtworkArtistProjection
	artwork    libraryArtworkEditionProvider
}

func NewLibraryEditionArtworkWorker(
	projection libraryArtworkArtistProjection,
	artwork libraryArtworkEditionProvider,
) *LibraryEditionArtworkWorker {
	return &LibraryEditionArtworkWorker{projection: projection, artwork: artwork}
}

// A complete gallery can contain hundreds of originals. River shutdown and
// rescue cancellation still propagate through ctx.
func (*LibraryEditionArtworkWorker) Timeout(*river.Job[service.LibraryEditionArtworkArgs]) time.Duration {
	return -1
}

func (worker *LibraryEditionArtworkWorker) Work(ctx context.Context, job *river.Job[service.LibraryEditionArtworkArgs]) error {
	if worker == nil || worker.projection == nil || worker.artwork == nil || job == nil {
		return fmt.Errorf("library edition artwork worker is unavailable")
	}
	args := job.Args
	if args.ArtistID == uuid.Nil || args.ArtistMBID == "" || args.ReleaseMBID == "" {
		return fmt.Errorf("library edition artwork args are invalid")
	}
	artist, err := worker.projection.ReadLibraryArtist(ctx, args.ArtistID)
	if err != nil {
		return fmt.Errorf("read library edition artwork eligibility: %w", err)
	}
	if artist == nil {
		return nil
	}
	if got := service.CanonicalMusicBrainzArtistMBIDFromLinks(artist.ProviderLinks); got == "" || got != args.ArtistMBID {
		return nil
	}
	releaseMBID, ok := service.CanonicalMusicBrainzReleaseMBID(args.ReleaseMBID)
	if !ok || releaseMBID != args.ReleaseMBID {
		return nil
	}
	state, err := worker.projection.ReadCoverArtArchiveRelease(ctx, releaseMBID)
	if err != nil {
		return fmt.Errorf("read library edition artwork status: %w", err)
	}
	problems := make([]error, 0)
	if state.FrontStatus == persistence.CoverArtArchiveUnchecked {
		if err := worker.artwork.EnsureFront(ctx, releaseMBID); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			problems = append(problems, fmt.Errorf("ensure edition %s front: %w", releaseMBID, err))
		}
	}
	eligible, err := worker.projection.CoverArtArchiveGalleryEligible(ctx, releaseMBID)
	if err != nil {
		problems = append(problems, fmt.Errorf("read edition %s gallery eligibility: %w", releaseMBID, err))
		return errors.Join(problems...)
	}
	if eligible && state.GalleryStatus != persistence.CoverArtArchiveGalleryComplete {
		if err := worker.artwork.EnsureGallery(ctx, releaseMBID); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			problems = append(problems, fmt.Errorf("ensure edition %s gallery: %w", releaseMBID, err))
		}
	}
	return errors.Join(problems...)
}

var _ river.Worker[service.LibraryEditionArtworkArgs] = (*LibraryEditionArtworkWorker)(nil)

// NewLibraryArtworkDispatchPeriodicJob starts the durable DB-derived backfill
// sweep at boot and then on its bounded cadence. The root insert is unique for
// the dispatch period, so the shorter tick cannot pile up overlapping sweeps.
func NewLibraryArtworkDispatchPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(service.LibraryArtworkDispatchInterval),
		service.LibraryArtworkDispatchInsert,
		&river.PeriodicJobOpts{ID: "library-artwork-dispatch", RunOnStart: true},
	)
}
